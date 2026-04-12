package claudecli

import (
	"context"
	"errors"
	"os/exec"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/kelindar/event"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
	tooldto "github.com/anthropic-ai/super-agent-v3/internal/dto/tool"
	turndto "github.com/anthropic-ai/super-agent-v3/internal/dto/turn"
	"github.com/anthropic-ai/super-agent-v3/internal/provider/unified"
)
func overrideSettleInterruptedTransport(t *testing.T, fn func(*transport) error) {
	t.Helper()
	prev := settleInterruptedTransport
	settleInterruptedTransport = fn
	t.Cleanup(func() {
		settleInterruptedTransport = prev
	})
}

func newInterruptTestTransport(t *testing.T, script string) *transport {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("interrupt transport test requires POSIX signals")
	}
	shell, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("sh not available")
	}
	tr, err := newTransport(shell, []string{"-c", script}, "", nil)
	if err != nil {
		t.Fatalf("newTransport() error = %v", err)
	}
	t.Cleanup(func() { _ = tr.Kill() })
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if tr.Running() {
			return tr
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("transport did not start running")
	return nil
}

func sessionStateForInterruptTest(s *session) (*transport, *turnHandle) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.transport, s.activeTurn
}

type blockingWriteCloser struct {
	started chan struct{}
	release chan struct{}
	writes  chan string
}

func (w *blockingWriteCloser) Write(p []byte) (int, error) {
	select {
	case <-w.started:
	default:
		close(w.started)
	}
	if w.writes != nil {
		w.writes <- string(append([]byte(nil), p...))
	}
	<-w.release
	return len(p), nil
}

func (w *blockingWriteCloser) Close() error { return nil }

func runInterruptRestartScenario(t *testing.T, script string) {
	t.Helper()
	overrideSettleInterruptedTransport(t, func(tr *transport) error {
		return settleInterruptedTransportWithTimeout(tr, 50*time.Millisecond)
	})
	next := newScriptedTransport()
	defer next.finish()
	resumeIDs := make(chan string, 1)
	overrideLaunchCLI(t, func(_, _, _, _ string, _ cliLaunchConfig, _ dto.MCPManifest, resumeID string) (*transport, func(), error) {
		resumeIDs <- resumeID
		return next.tr, nil, nil
	})
	oldReady := make(chan struct{})
	close(oldReady)
	oldTransport := newInterruptTestTransport(t, script)
	cleanupCalled := false
	active := newTurnHandle("local-1", "turn-1")
	s := &session{
		threadID:        "thread-1",
		sessionID:       "thread-1",
		publicThreadID:  "thread-public",
		threadReady:     oldReady,
		transport:       oldTransport,
		cleanup:         func() { cleanupCalled = true },
		activeTurn:      active,
		suppressedTurns: map[string]struct{}{},
		model:           "claude-old",
	}
	if err := s.Interrupt(context.Background(), dto.InterruptRequest{Source: "ui_stop"}); err != nil {
		t.Fatalf("Interrupt() error = %v", err)
	}
	select {
	case <-active.Done():
	default:
		t.Fatal("Interrupt() did not finish active turn")
	}
	if !errors.Is(active.Err(), context.Canceled) {
		t.Fatalf("active.Err() = %v, want context.Canceled", active.Err())
	}
	if !cleanupCalled {
		t.Fatal("interrupt cleanup was not called")
	}
	if oldTransport.Running() {
		t.Fatal("old transport still running after interrupt cleanup")
	}
	tr, handle := sessionStateForInterruptTest(s)
	if tr != nil {
		t.Fatal("session transport was not cleared after interrupt")
	}
	if handle != nil {
		t.Fatal("active turn was not cleared after interrupt")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	newHandle, err := s.StartTurn(ctx, turnRequest("claude-next"))
	if err != nil {
		t.Fatalf("StartTurn() after interrupt error = %v", err)
	}
	if newHandle == nil {
		t.Fatal("StartTurn() after interrupt returned nil handle")
	}
	select {
	case write := <-next.stdin.writes:
		if !strings.Contains(write, "hello") {
			t.Fatalf("transport write = %q, want turn payload", write)
		}
	case <-time.After(time.Second):
		t.Fatal("StartTurn() did not write payload after interrupt restart")
	}
	_, _, ready := snapshotSessionState(s)
	select {
	case <-ready:
	default:
		t.Fatal("restart did not reuse known thread readiness")
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

func TestInterruptExitedCLIAllowsNextTurn(t *testing.T) {
	runInterruptRestartScenario(t, "while :; do sleep 1; done")
}

func TestInterruptHungCLIKillsAndAllowsNextTurn(t *testing.T) {
	runInterruptRestartScenario(t, "trap '' INT; while :; do sleep 1; done")
}

func TestInterruptDispatchesSyntheticToolEnd(t *testing.T) {
	bus := event.NewDispatcher()
	defer func() { _ = bus.Close() }()
	dispatcher := unified.NewEventDispatcher(bus, nil)
	RegisterTranslators(dispatcher)

	toolEnds := make(chan tooldto.ToolCallEnd, 1)
	turnInterrupted := make(chan turndto.TurnInterrupted, 1)
	cancelTool := event.Subscribe(bus, func(ev tooldto.ToolCallEnd) { toolEnds <- ev })
	defer cancelTool()
	cancelTurn := event.Subscribe(bus, func(ev turndto.TurnInterrupted) { turnInterrupted <- ev })
	defer cancelTurn()

	s := &session{
		agentID:         "agent-1",
		threadID:        "thread-1",
		publicThreadID:  "thread-public",
		sessionID:       "thread-1",
		activeTurn:      newTurnHandle("local-1", "turn-1"),
		activeToolCalls: map[string]string{"call-1": "lsp_read"},
		suppressedTurns: map[string]struct{}{},
		eventDispatcher: dispatcher,
	}
	if err := s.Interrupt(context.Background(), dto.InterruptRequest{Source: "ui_stop"}); err != nil {
		t.Fatalf("Interrupt() error = %v", err)
	}

	select {
	case ev := <-toolEnds:
		if ev.CallID != "call-1" || ev.ToolName != "lsp_read" {
			t.Fatalf("ToolCallEnd = %+v, want call-1/lsp_read", ev)
		}
		if ev.Success {
			t.Fatalf("ToolCallEnd.Success = true, want false")
		}
		if !strings.Contains(ev.Error, "ui_stop") {
			t.Fatalf("ToolCallEnd.Error = %q, want ui_stop context", ev.Error)
		}
	case <-time.After(time.Second):
		t.Fatal("Interrupt() did not dispatch synthetic ToolCallEnd")
	}

	select {
	case ev := <-turnInterrupted:
		if ev.TurnID != "turn-1" {
			t.Fatalf("TurnInterrupted.TurnID = %q, want turn-1", ev.TurnID)
		}
	case <-time.After(time.Second):
		t.Fatal("Interrupt() did not dispatch TurnInterrupted")
	}
}

func TestInterruptWaitsForConcurrentStartTurnSend(t *testing.T) {
	ready := make(chan struct{})
	close(ready)
	writer := &blockingWriteCloser{
		started: make(chan struct{}),
		release: make(chan struct{}),
		writes:  make(chan string, 1),
	}
	s := &session{
		threadID:        "thread-1",
		sessionID:       "thread-1",
		publicThreadID:  "thread-public",
		threadReady:     ready,
		transport:       &transport{stdin: writer, stderr: newLimitedBuffer(stderrLimitBytes), done: make(chan struct{})},
		suppressedTurns: map[string]struct{}{},
		model:           "claude-old",
	}

	startResult := make(chan struct {
		handle any
		err    error
	}, 1)
	go func() {
		handle, err := s.StartTurn(context.Background(), turnRequest("claude-old"))
		startResult <- struct {
			handle any
			err    error
		}{handle: handle, err: err}
	}()

	select {
	case <-writer.started:
	case <-time.After(time.Second):
		t.Fatal("StartTurn() did not begin transport write")
	}

	interruptDone := make(chan error, 1)
	go func() {
		interruptDone <- s.Interrupt(context.Background(), dto.InterruptRequest{Source: "ui_stop"})
	}()

	select {
	case err := <-interruptDone:
		t.Fatalf("Interrupt() returned before send completed: %v", err)
	case <-time.After(100 * time.Millisecond):
	}

	close(writer.release)

	var startedHandle contract.TurnHandle
	select {
	case result := <-startResult:
		if result.err != nil {
			t.Fatalf("StartTurn() error = %v", result.err)
		}
		var ok bool
		startedHandle, ok = result.handle.(contract.TurnHandle)
		if !ok || startedHandle == nil {
			t.Fatalf("StartTurn() handle = %#v, want contract.TurnHandle", result.handle)
		}
	case <-time.After(time.Second):
		t.Fatal("StartTurn() did not finish after write release")
	}

	select {
	case err := <-interruptDone:
		if err != nil {
			t.Fatalf("Interrupt() error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Interrupt() did not finish after write release")
	}

	select {
	case <-startedHandle.Done():
	case <-time.After(time.Second):
		t.Fatal("interrupted handle was not finished")
	}
	if !errors.Is(startedHandle.Err(), context.Canceled) {
		t.Fatalf("handle.Err() = %v, want context.Canceled", startedHandle.Err())
	}
}
