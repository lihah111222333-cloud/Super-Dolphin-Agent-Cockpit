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

func overrideLaunchCLI(t *testing.T, fn func(string, string, string, string, cliLaunchConfig, dto.MCPManifest, string) (*transport, func(), error)) {
	t.Helper()
	prev := launchCLI
	launchCLI = fn
	t.Cleanup(func() {
		launchCLI = prev
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
	deadline := time.After(time.Second)
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
		case <-time.After(time.Second):
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
		threadID:        "thread-1",
		sessionID:       "thread-1",
		threadReady:     oldReady,
		transport:       closedTransport(),
		suppressedTurns: map[string]struct{}{"old-turn": {}},
		model:           "claude-old",
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
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
	if threadID != "thread-1" || sessionID != "thread-1" {
		t.Fatalf("ids before new ready = %q/%q, want thread-1/thread-1", threadID, sessionID)
	}
	select {
	case <-newReady:
		t.Fatal("replacement ready channel closed before new system:init")
	default:
	}

	next.emitSystemInit(t, "thread-1")
	select {
	case err := <-result:
		if err != nil {
			t.Fatalf("restartIfNeededLocked() error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("restartIfNeededLocked() did not return after system:init")
	}
	select {
	case <-newReady:
	default:
		t.Fatal("replacement ready channel was not closed")
	}

	threadID, sessionID, _ = snapshotSessionState(s)
	if threadID != "thread-1" || sessionID != "thread-1" {
		t.Fatalf("ids after new ready = %q/%q, want thread-1/thread-1", threadID, sessionID)
	}
	if got := suppressedTurnsLen(s); got != 0 {
		t.Fatalf("suppressedTurns len = %d, want 0 after restart", got)
	}
	select {
	case resumeID := <-resumeIDs:
		if resumeID != "thread-1" {
			t.Fatalf("resumeID = %q, want thread-1", resumeID)
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
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
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
		threadID:        "thread-1",
		sessionID:       "thread-1",
		threadReady:     oldReady,
		transport:       closedTransport(),
		suppressedTurns: map[string]struct{}{},
		model:           "claude-old",
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
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

	next.emitSystemInit(t, "thread-1")
	select {
	case <-ready:
	case <-time.After(time.Second):
		t.Fatal("replacement ready channel did not close")
	}
	select {
	case write := <-next.stdin.writes:
		if !strings.Contains(write, "hello") {
			t.Fatalf("transport write = %q, want turn payload", write)
		}
	case <-time.After(time.Second):
		t.Fatal("StartTurn did not write after ready")
	}
	successCount, errorCount := collectStartTurnResults(t, results)
	if successCount == 0 || successCount+errorCount != 2 {
		t.Fatalf("StartTurn results = success:%d error:%d, want at least one success and two completions", successCount, errorCount)
	}
}
