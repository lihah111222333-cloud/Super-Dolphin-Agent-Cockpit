package claudecli

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
	shareddto "github.com/anthropic-ai/super-agent-v3/internal/dto/shared"
)

type recordingWriteCloser struct {
	writes chan string
}

func (r *recordingWriteCloser) Write(p []byte) (int, error) {
	if r.writes != nil {
		r.writes <- string(append([]byte(nil), p...))
	}
	return len(p), nil
}

func (r *recordingWriteCloser) Close() error { return nil }

type scriptedTransport struct {
	tr        *transport
	stdin     *recordingWriteCloser
	stdout    io.WriteCloser
	closeOnce sync.Once
}

type startTurnResult struct {
	handle any
	err    error
}

func newScriptedTransport() *scriptedTransport {
	reader, writer := io.Pipe()
	stdin := &recordingWriteCloser{writes: make(chan string, 8)}
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 64*1024), maxCLILineBytes)
	return &scriptedTransport{
		tr: &transport{
			stdin:  stdin,
			stdout: scanner,
			stderr: newLimitedBuffer(stderrLimitBytes),
			done:   make(chan struct{}),
		},
		stdin:  stdin,
		stdout: writer,
	}
}

func newBufferedTransport(t *testing.T, threadID string) *scriptedTransport {
	t.Helper()
	line := marshalSystemInit(t, threadID) + "\n"
	stdin := &recordingWriteCloser{writes: make(chan string, 8)}
	scanner := bufio.NewScanner(strings.NewReader(line))
	scanner.Buffer(make([]byte, 64*1024), maxCLILineBytes)
	return &scriptedTransport{
		tr: &transport{
			stdin:  stdin,
			stdout: scanner,
			stderr: newLimitedBuffer(stderrLimitBytes),
			done:   make(chan struct{}),
		},
		stdin: stdin,
	}
}

func (s *scriptedTransport) emitSystemInit(t *testing.T, threadID string) {
	t.Helper()
	if s.stdout == nil {
		t.Fatal("scripted transport has no writable stdout")
	}
	if _, err := io.WriteString(s.stdout, marshalSystemInit(t, threadID)+"\n"); err != nil {
		t.Fatalf("emit system:init: %v", err)
	}
}

func (s *scriptedTransport) finish() {
	s.closeOnce.Do(func() {
		if s.stdout != nil {
			_ = s.stdout.Close()
		}
		close(s.tr.done)
	})
}

func marshalSystemInit(t *testing.T, threadID string) string {
	t.Helper()
	payload, err := json.Marshal(streamEvent{
		Type:      "system",
		Subtype:   "init",
		SessionID: threadID,
	})
	if err != nil {
		t.Fatalf("marshal system:init: %v", err)
	}
	return string(payload)
}

func closedTransport() *transport {
	done := make(chan struct{})
	close(done)
	return &transport{done: done}
}

var launchCLIOverrideMu sync.Mutex

const restartTestTimeout = 15 * time.Second

func overrideLaunchCLI(t *testing.T, fn func(string, string, string, string, cliLaunchConfig, dto.MCPManifest, string) (*transport, func(), error)) {
	t.Helper()
	launchCLIOverrideMu.Lock()
	prev := launchCLI
	launchCLI = fn
	t.Cleanup(func() {
		launchCLI = prev
		launchCLIOverrideMu.Unlock()
	})
}

func snapshotSessionState(s *session) (string, string, chan struct{}) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.threadID, s.sessionID, s.threadReady
}

func suppressedTurnsLen(s *session) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.suppressedTurns)
}

func waitForReadySwap(t *testing.T, s *session, oldReady chan struct{}) chan struct{} {
	t.Helper()
	deadline := time.After(restartTestTimeout)
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		_, _, ready := snapshotSessionState(s)
		if ready != nil && ready != oldReady {
			return ready
		}
		select {
		case <-deadline:
			t.Fatal("threadReady was not replaced")
		case <-ticker.C:
		}
	}
}

func turnRequest(model string) dto.TurnRequest {
	return dto.TurnRequest{
		Inputs: []shareddto.InputItem{{Type: "text", Content: "hello"}},
		Overrides: dto.TurnOverrides{
			Model: model,
		},
	}
}
func TestPrepareTurnLockedPrependsStructuredTurnAssemblyUserContext(t *testing.T) {
	ready := make(chan struct{})
	close(ready)
	s := &session{
		threadID:        "thread-1",
		sessionID:       "thread-1",
		threadReady:     ready,
		transport:       &transport{stdin: &recordingWriteCloser{}, done: make(chan struct{})},
		suppressedTurns: map[string]struct{}{},
	}

	s.mu.Lock()
	payload, _, _, err := s.prepareTurnLocked(context.Background(), dto.TurnRequest{
		Inputs: []shareddto.InputItem{{Content: "hello"}},
		TurnAssembly: contract.TurnAssembly{
			UserContext: map[string]string{
				"currentDate": "Today's date is 2026-04-15.",
			},
		},
	})
	s.mu.Unlock()
	if err != nil {
		t.Fatalf("prepareTurnLocked() error = %v", err)
	}

	var decoded struct {
		Message struct {
			Content []struct {
				Text string `json:"text"`
			} `json:"content"`
		} `json:"message"`
	}
	if err := json.Unmarshal(payload, &decoded); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	// system-reminder is now injected once at session start, not per-turn.
	if got := decoded.Message.Content[0].Text; got != "hello" {
		t.Fatalf("payload text = %q, want %q", got, "hello")
	}
}

func TestPrepareTurnLockedIncludesAttachmentTextAfterUserContext(t *testing.T) {
	ready := make(chan struct{})
	close(ready)
	attachment := contract.NewRelevantMemoryAttachment(
		"project/commit-style.md",
		"Memory (saved today): project/commit-style.md:",
		"Use concise imperative commit messages.",
		time.Date(2026, 4, 14, 12, 0, 0, 0, time.UTC),
		720,
		false,
	)
	s := &session{
		threadID:        "thread-1",
		sessionID:       "thread-1",
		threadReady:     ready,
		transport:       &transport{stdin: &recordingWriteCloser{}, done: make(chan struct{})},
		suppressedTurns: map[string]struct{}{},
	}

	s.mu.Lock()
	payload, _, _, err := s.prepareTurnLocked(context.Background(), dto.TurnRequest{
		Inputs: []shareddto.InputItem{{Content: "hello"}},
		TurnAssembly: contract.TurnAssembly{
			UserContext: map[string]string{
				"currentDate": "Today's date is 2026-04-15.",
			},
			Attachments: []dto.AttachmentEnvelope{attachment},
		},
	})
	s.mu.Unlock()
	if err != nil {
		t.Fatalf("prepareTurnLocked() error = %v", err)
	}

	var decoded struct {
		Message struct {
			Content []struct {
				Text string `json:"text"`
			} `json:"content"`
		} `json:"message"`
	}
	if err := json.Unmarshal(payload, &decoded); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	got := decoded.Message.Content[0].Text
	// system-reminder is now injected once at session start, not per-turn.
	want := contract.RenderAttachmentText(attachment) + "\n\nhello"
	if got != want {
		t.Fatalf("payload text = %q, want %q", got, want)
	}
}

func collectStartTurnResults(t *testing.T, results <-chan startTurnResult) (int, int) {
	t.Helper()
	var successCount, errorCount int
	for range 2 {
		select {
		case res := <-results:
			if res.err != nil {
				errorCount++
				continue
			}
			if res.handle == nil {
				t.Fatal("successful StartTurn returned nil handle")
			}
			successCount++
		case <-time.After(restartTestTimeout):
			t.Fatal("concurrent StartTurn did not finish")
		}
	}
	return successCount, errorCount
}

func TestRestartIfNeededLockedRebuildsReadyAndPreservesIDs(t *testing.T) {
	next := newScriptedTransport()
	defer next.finish()
	resumeIDs := make(chan string, 1)
	overrideLaunchCLI(t, func(_, _, _, _ string, _ cliLaunchConfig, _ dto.MCPManifest, resumeID string) (*transport, func(), error) {
		resumeIDs <- resumeID
		return next.tr, nil, nil
	})

	oldReady := make(chan struct{})
	close(oldReady)
	s := &session{
		threadID:        "pending",
		sessionID:       "pending",
		threadReady:     oldReady,
		transport:       closedTransport(),
		suppressedTurns: map[string]struct{}{"old-turn": {}},
		model:           "claude-old",
	}
	ctx, cancel := context.WithTimeout(context.Background(), restartTestTimeout)
	defer cancel()
	result := make(chan error, 1)
	go func() {
		s.mu.Lock()
		err := s.restartIfNeededLocked(ctx, turnRequest("claude-new"))
		s.mu.Unlock()
		result <- err
	}()

	newReady := waitForReadySwap(t, s, oldReady)
	threadID, sessionID, _ := snapshotSessionState(s)
	if threadID != "pending" || sessionID != "pending" {
		t.Fatalf("ids before new ready = %q/%q, want pending/pending", threadID, sessionID)
	}
	select {
	case <-newReady:
		t.Fatal("replacement ready channel closed before new system:init")
	default:
	}

	next.emitSystemInit(t, "11111111-2222-3333-4444-555555555555")
	select {
	case err := <-result:
		if err != nil {
			t.Fatalf("restartIfNeededLocked() error = %v", err)
		}
	case <-time.After(restartTestTimeout):
		t.Fatal("restartIfNeededLocked() did not return after system:init")
	}
	select {
	case <-newReady:
	default:
		t.Fatal("replacement ready channel was not closed")
	}

	threadID, sessionID, _ = snapshotSessionState(s)
	const wantID = "11111111-2222-3333-4444-555555555555"
	if threadID != wantID || sessionID != wantID {
		t.Fatalf("ids after new ready = %q/%q, want %q/%q", threadID, sessionID, wantID, wantID)
	}
	if got := suppressedTurnsLen(s); got != 0 {
		t.Fatalf("suppressedTurns len = %d, want 0 after restart", got)
	}
	select {
	case resumeID := <-resumeIDs:
		if resumeID != "" {
			t.Fatalf("resumeID = %q, want empty resume id for unresolved thread", resumeID)
		}
	default:
		t.Fatal("restart launch was not invoked")
	}
}

func TestRestartIfNeededLockedKeepsEarlyReadyEvent(t *testing.T) {
	next := newBufferedTransport(t, "thread-early")
	overrideLaunchCLI(t, func(string, string, string, string, cliLaunchConfig, dto.MCPManifest, string) (*transport, func(), error) {
		return next.tr, nil, nil
	})

	oldReady := make(chan struct{})
	close(oldReady)
	s := &session{
		threadID:        "thread-early",
		sessionID:       "thread-early",
		threadReady:     oldReady,
		transport:       closedTransport(),
		suppressedTurns: map[string]struct{}{},
		model:           "claude-old",
	}
	ctx, cancel := context.WithTimeout(context.Background(), restartTestTimeout)
	defer cancel()

	start := time.Now()
	s.mu.Lock()
	err := s.restartIfNeededLocked(ctx, turnRequest("claude-next"))
	s.mu.Unlock()
	if err != nil {
		t.Fatalf("restartIfNeededLocked() error = %v", err)
	}
	if elapsed := time.Since(start); elapsed >= time.Second {
		t.Fatalf("restartIfNeededLocked() returned too late: %s", elapsed)
	}

	threadID, sessionID, ready := snapshotSessionState(s)
	if threadID != "thread-early" || sessionID != "thread-early" {
		t.Fatalf("ids after early ready = %q/%q, want thread-early/thread-early", threadID, sessionID)
	}
	select {
	case <-ready:
	default:
		t.Fatal("ready channel should be closed by early system:init")
	}
}

func TestStartTurnBlocksConcurrentSubmitUntilRestartReady(t *testing.T) {
	next := newScriptedTransport()
	defer next.finish()
	overrideLaunchCLI(t, func(string, string, string, string, cliLaunchConfig, dto.MCPManifest, string) (*transport, func(), error) {
		return next.tr, nil, nil
	})

	oldReady := make(chan struct{})
	close(oldReady)
	s := &session{
		threadID:        "pending",
		sessionID:       "pending",
		threadReady:     oldReady,
		transport:       closedTransport(),
		suppressedTurns: map[string]struct{}{},
		model:           "claude-old",
	}
	ctx, cancel := context.WithTimeout(context.Background(), restartTestTimeout)
	defer cancel()
	req := turnRequest("claude-new")
	results := make(chan startTurnResult, 2)
	go func() {
		handle, err := s.StartTurn(ctx, req)
		results <- startTurnResult{handle: handle, err: err}
	}()
	ready := waitForReadySwap(t, s, oldReady)
	go func() {
		handle, err := s.StartTurn(ctx, req)
		results <- startTurnResult{handle: handle, err: err}
	}()

	select {
	case write := <-next.stdin.writes:
		t.Fatalf("StartTurn wrote before restart ready: %q", write)
	case <-time.After(100 * time.Millisecond):
	}
	select {
	case res := <-results:
		t.Fatalf("StartTurn returned before restart ready: handle=%v err=%v", res.handle, res.err)
	default:
	}

	s.setResolvedThreadIDForTransport(next.tr, "thread-1")
	select {
	case <-ready:
	case <-time.After(restartTestTimeout):
		t.Fatal("replacement ready channel did not close")
	}
	select {
	case write := <-next.stdin.writes:
		if !strings.Contains(write, "hello") {
			t.Fatalf("transport write = %q, want turn payload", write)
		}
	case <-time.After(restartTestTimeout):
		t.Fatal("StartTurn did not write after ready")
	}
	successCount, errorCount := collectStartTurnResults(t, results)
	if successCount == 0 || successCount+errorCount != 2 {
		t.Fatalf("StartTurn results = success:%d error:%d, want at least one success and two completions", successCount, errorCount)
	}
}
