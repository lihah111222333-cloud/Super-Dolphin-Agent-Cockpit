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

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
	dto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/provider"
	tooldto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/tool"
	turndto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/turn"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/pidregistry"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/provider/unified"
)

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

type interruptStartResult struct {
	handle contract.TurnHandle
	err    error
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
	next := newScriptedTransport()
	defer next.finish()
	resumeIDs := make(chan string, 1)
	launchFn := overrideLaunchCLI(t, func(_, _, _, _ string, _ cliLaunchConfig, _ dto.MCPManifest, resumeID string) (*transport, func(), error) {
		resumeIDs <- resumeID
		return next.tr, nil, nil
	})
	cleanupCalled := false
	s, active, oldTransport := newInterruptRestartSession(t, script, &cleanupCalled, launchFn)
	assertInterruptStopsSession(t, s, active, oldTransport, &cleanupCalled)
	startTurnAfterInterrupt(t, s)
	assertInterruptRestartWrite(t, next)
	assertInterruptRestartReady(t, s)
	assertInterruptRestartResumeID(t, resumeIDs)
}

func newInterruptRestartSession(t *testing.T, script string, cleanupCalled *bool, launchFn func(string, string, string, string, cliLaunchConfig, dto.MCPManifest, string) (*transport, func(), error)) (*session, *turnHandle, *transport) {
	t.Helper()
	oldReady := make(chan struct{})
	close(oldReady)
	oldTransport := newInterruptTestTransport(t, script)
	active := newTurnHandle("local-1", "turn-1")
	return &session{
		threadID:        "11111111-2222-3333-4444-555555555555",
		sessionID:       "11111111-2222-3333-4444-555555555555",
		publicThreadID:  "thread-public",
		threadReady:     oldReady,
		transport:       oldTransport,
		cleanup:         func() { *cleanupCalled = true },
		activeTurn:      active,
		suppressedTurns: map[string]struct{}{},
		model:           "claude-old",
		config:          cliLaunchConfig{PromptSnapshot: validResumePromptSnapshotForTest()},
		launchCLI:       launchFn,
		settleTransport: func(tr *transport) error {
			return settleInterruptedTransportWithTimeout(tr, 50*time.Millisecond)
		},
	}, active, oldTransport
}

func assertInterruptStopsSession(t *testing.T, s *session, active *turnHandle, oldTransport *transport, cleanupCalled *bool) {
	t.Helper()
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
	if !*cleanupCalled {
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
}

func startTurnAfterInterrupt(t *testing.T, s *session) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	newHandle, err := s.StartTurn(ctx, turnRequest("claude-next"))
	if err != nil {
		t.Fatalf("StartTurn() after interrupt error = %v", err)
	}
	if newHandle == nil {
		t.Fatal("StartTurn() after interrupt returned nil handle")
	}
}

func assertInterruptRestartWrite(t *testing.T, next *scriptedTransport) {
	t.Helper()
	select {
	case write := <-next.stdin.writes:
		if !strings.Contains(write, "hello") {
			t.Fatalf("transport write = %q, want turn payload", write)
		}
	case <-time.After(time.Second):
		t.Fatal("StartTurn() did not write payload after interrupt restart")
	}
}

func assertInterruptRestartReady(t *testing.T, s *session) {
	t.Helper()
	_, _, ready := snapshotSessionState(s)
	select {
	case <-ready:
	default:
		t.Fatal("restart did not reuse known thread readiness")
	}
}

func assertInterruptRestartResumeID(t *testing.T, resumeIDs <-chan string) {
	t.Helper()
	select {
	case resumeID := <-resumeIDs:
		if resumeID != "11111111-2222-3333-4444-555555555555" {
			t.Fatalf("resumeID = %q, want UUID", resumeID)
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

func TestInterruptCleanupFailureRetainsTurnAndPIDWithoutUserTerminal(t *testing.T) {
	t.Run("settle error", func(t *testing.T) {
		assertInterruptCleanupFailureRetainsOwnership(t, func(*transport) error {
			return errors.New("injected settle failure")
		})
	})
	t.Run("signal error", func(t *testing.T) {
		assertInterruptCleanupFailureRetainsOwnership(t, func(tr *transport) error {
			tr.signal = func(sig processSig) error {
				if sig == sigInterrupt {
					return errors.New("injected signal failure")
				}
				return tr.signalProcessNative(sig)
			}
			return settleInterruptedTransportWithTimeout(tr, 50*time.Millisecond)
		})
	})
}

func assertInterruptCleanupFailureRetainsOwnership(t *testing.T, settle func(*transport) error) {
	t.Helper()
	bus := event.NewDispatcher()
	defer func() { _ = bus.Close() }()
	dispatcher := unified.NewEventDispatcher(bus, nil)
	RegisterTranslators(dispatcher)
	terminals := make(chan turndto.TurnCompleted, 1)
	cancelTerminal := event.Subscribe(bus, func(ev turndto.TurnCompleted) { terminals <- ev })
	defer cancelTerminal()

	oldWatcher := newSessionLogWatcher(sessionLogWatcherConfig{
		ResolvePath: func() (string, error) {
			return "", errors.New("interrupt test old watcher must not resolve a session log")
		},
		PollInterval: time.Millisecond,
	})
	oldWatcher.start()
	t.Cleanup(oldWatcher.stopAndWait)
	tr := newInterruptTestTransport(t, "while :; do sleep 1; done")
	reg := pidregistry.New()
	if err := registerTransportPID(reg, tr, "agent-interrupt-failure"); err != nil {
		t.Fatalf("register transport PID: %v", err)
	}
	t.Cleanup(func() { unregisterTransportPID(reg, tr) })
	handle := newTurnHandle("local-failure", "turn-failure")
	cleanupCalled := false
	s := &session{
		agentID: "agent-interrupt-failure", threadID: "thread-interrupt-failure", publicThreadID: "thread-public",
		sessionID: "11111111-2222-3333-4444-555555555555", history: &historyBackend{sessionDir: t.TempDir()},
		transport: tr, cleanup: func() { cleanupCalled = true }, logWatcher: oldWatcher,
		pidRegistry: reg, activeTurn: handle, suppressedTurns: map[string]struct{}{},
		eventDispatcher: dispatcher, settleTransport: settle,
	}
	t.Cleanup(func() {
		s.mu.Lock()
		watcher := s.detachLogWatcherLocked()
		s.mu.Unlock()
		watcher.stopAndWait()
	})

	err := s.Interrupt(context.Background(), dto.InterruptRequest{Source: "ui_stop", RequestID: "request-A"})
	if err == nil {
		t.Fatal("Interrupt() error = nil, want cleanup failure")
	}
	assertFailedInterruptOwnership(t, s, tr, handle, cleanupCalled)
	assertFailedInterruptLogWatcherRestored(t, s, oldWatcher)
	assertFailedInterruptHasNoTerminal(t, terminals, handle)
}

func assertFailedInterruptOwnership(t *testing.T, s *session, tr *transport, handle *turnHandle, cleanupCalled bool) {
	t.Helper()
	gotTransport, gotHandle := sessionStateForInterruptTest(s)
	if gotTransport != tr || gotHandle != handle || s.interrupting {
		t.Fatalf("failed interrupt ownership: transport=%p handle=%p interrupting=%v", gotTransport, gotHandle, s.interrupting)
	}
	if cleanupCalled {
		t.Fatal("cleanup callback ran after failed interrupt")
	}
	if !containsPID(currentRegistryChildPIDs(t, "claude-cli"), tr.cmd.Process.Pid) {
		t.Fatalf("PID %d is no longer managed after failed interrupt", tr.cmd.Process.Pid)
	}
}

func assertFailedInterruptLogWatcherRestored(t *testing.T, s *session, oldWatcher *sessionLogWatcher) {
	t.Helper()
	s.mu.Lock()
	watcher := s.logWatcher
	s.mu.Unlock()
	if watcher == nil {
		t.Fatal("failed interrupt did not restore the log watcher")
	}
	if watcher == oldWatcher {
		t.Fatal("failed interrupt restored the stopped log watcher")
	}
}

func assertFailedInterruptHasNoTerminal(t *testing.T, terminals <-chan turndto.TurnCompleted, handle *turnHandle) {
	t.Helper()
	select {
	case terminal := <-terminals:
		t.Fatalf("failed interrupt published terminal: %#v", terminal)
	case <-time.After(100 * time.Millisecond):
	}
	select {
	case <-handle.Done():
		t.Fatalf("failed interrupt completed active handle: %v", handle.Err())
	default:
	}
}

func TestInterruptTargetChangedLeavesActiveTurnUntouched(t *testing.T) {
	t.Parallel()

	active := newTurnHandle("local-2", "turn-2")
	cleanupCalls := 0
	s := &session{
		activeTurn:      active,
		activeToolCalls: map[string]string{"call-2": "lsp_read"},
		suppressedTurns: map[string]struct{}{},
		cleanup:         func() { cleanupCalls++ },
	}
	err := s.Interrupt(context.Background(), dto.InterruptRequest{TurnID: "turn-1", Source: "ui_stop"})
	if !errors.Is(err, contract.ErrInterruptTargetChanged) {
		t.Fatalf("Interrupt() error = %v, want target changed", err)
	}
	_, gotActive := sessionStateForInterruptTest(s)
	if gotActive != active || cleanupCalls != 0 || len(s.suppressedTurns) != 0 || len(s.activeToolCalls) != 1 {
		t.Fatalf("target-changed side effects: active=%p cleanup=%d suppressed=%v tools=%v", gotActive, cleanupCalls, s.suppressedTurns, s.activeToolCalls)
	}
	select {
	case <-active.Done():
		t.Fatal("target-changed interrupt finished the replacement turn")
	default:
	}
}

func TestInterruptDispatchesSyntheticToolEnd(t *testing.T) {
	bus := event.NewDispatcher()
	defer func() { _ = bus.Close() }()
	dispatcher := unified.NewEventDispatcher(bus, nil)
	RegisterTranslators(dispatcher)

	toolEnds := make(chan tooldto.ToolCallEnd, 1)
	turnInterrupted := make(chan turndto.TurnCompleted, 1)
	cancelTool := event.Subscribe(bus, func(ev tooldto.ToolCallEnd) { toolEnds <- ev })
	defer cancelTool()
	cancelTurn := event.Subscribe(bus, func(ev turndto.TurnCompleted) { turnInterrupted <- ev })
	defer cancelTurn()

	s := &session{
		agentID:         "agent-1",
		threadID:        "11111111-2222-3333-4444-555555555555",
		publicThreadID:  "thread-public",
		sessionID:       "11111111-2222-3333-4444-555555555555",
		activeTurn:      newTurnHandle("local-1", "turn-1"),
		activeToolCalls: map[string]string{"call-1": "lsp_read"},
		suppressedTurns: map[string]struct{}{},
		eventDispatcher: dispatcher,
	}
	if err := s.Interrupt(context.Background(), dto.InterruptRequest{Source: "ui_stop", RequestID: "stop-1"}); err != nil {
		t.Fatalf("Interrupt() error = %v", err)
	}

	assertSyntheticToolEnd(t, toolEnds)
	assertTurnInterrupted(t, turnInterrupted)
}

func assertSyntheticToolEnd(t *testing.T, toolEnds <-chan tooldto.ToolCallEnd) {
	t.Helper()
	select {
	case ev := <-toolEnds:
		if ev.CallID != "call-1" || ev.ToolName != "lsp_read" {
			t.Fatalf("ToolCallEnd = %+v, want call-1/lsp_read", ev)
		}
		if ev.Success {
			t.Fatalf("ToolCallEnd.Success = true, want false")
		}
		if !strings.HasPrefix(ev.Error, "Tool execution failed. Diagnostic ID: ") || strings.Contains(ev.Error, "ui_stop") {
			t.Fatalf("ToolCallEnd.Error = %q, want public diagnostic", ev.Error)
		}
	case <-time.After(time.Second):
		t.Fatal("Interrupt() did not dispatch synthetic ToolCallEnd")
	}
}

func assertTurnInterrupted(t *testing.T, turnInterrupted <-chan turndto.TurnCompleted) {
	t.Helper()
	select {
	case ev := <-turnInterrupted:
		if ev.TurnID != "turn-1" || ev.Success || ev.Status != "interrupted" || ev.Reason != "user_request" || ev.TerminationRequestID != "stop-1" {
			t.Fatalf("TurnCompleted = %#v, want accepted user interruption", ev)
		}
	case <-time.After(time.Second):
		t.Fatal("Interrupt() did not dispatch terminal interruption")
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
		threadID:        "11111111-2222-3333-4444-555555555555",
		sessionID:       "11111111-2222-3333-4444-555555555555",
		publicThreadID:  "thread-public",
		threadReady:     ready,
		transport:       &transport{stdin: writer, stderr: newLimitedBuffer(stderrLimitBytes), done: make(chan struct{})},
		suppressedTurns: map[string]struct{}{},
		model:           "claude-old",
	}

	startResult := startBlockingStartTurn(t, s)
	waitForBlockingWriterStarted(t, writer)
	interruptDone := startInterrupt(t, s)
	assertInterruptStillWaiting(t, interruptDone)

	close(writer.release)

	startedHandle := awaitBlockingStartResult(t, startResult)
	awaitInterruptDone(t, interruptDone)
	assertTurnHandleCanceled(t, startedHandle)
}

func startBlockingStartTurn(t testing.TB, s *session) <-chan interruptStartResult {
	t.Helper()
	startResult := make(chan interruptStartResult, 1)
	goroutines := newTestGoroutineGroup(t)
	goroutines.Go(func() {
		handle, err := s.StartTurn(context.Background(), turnRequest("claude-old"))
		startResult <- interruptStartResult{handle: handle, err: err}
	})
	return startResult
}

func waitForBlockingWriterStarted(t *testing.T, writer *blockingWriteCloser) {
	t.Helper()
	select {
	case <-time.After(time.Second):
		t.Fatal("StartTurn() did not begin transport write")
	case <-writer.started:
	}
}

func startInterrupt(t testing.TB, s *session) <-chan error {
	t.Helper()
	interruptDone := make(chan error, 1)
	goroutines := newTestGoroutineGroup(t)
	goroutines.Go(func() {
		interruptDone <- s.Interrupt(context.Background(), dto.InterruptRequest{Source: "ui_stop"})
	})
	return interruptDone
}

func assertInterruptStillWaiting(t *testing.T, interruptDone <-chan error) {
	t.Helper()
	select {
	case err := <-interruptDone:
		t.Fatalf("Interrupt() returned before send completed: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
}

func awaitBlockingStartResult(t *testing.T, startResult <-chan interruptStartResult) contract.TurnHandle {
	t.Helper()
	select {
	case <-time.After(time.Second):
		t.Fatal("StartTurn() did not finish after write release")
	case result := <-startResult:
		if result.err != nil {
			t.Fatalf("StartTurn() error = %v", result.err)
		}
		if result.handle == nil {
			t.Fatalf("StartTurn() handle = %#v, want contract.TurnHandle", result.handle)
		}
		return result.handle
	}
	return nil
}

func awaitInterruptDone(t *testing.T, interruptDone <-chan error) {
	t.Helper()
	select {
	case err := <-interruptDone:
		if err != nil {
			t.Fatalf("Interrupt() error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Interrupt() did not finish after write release")
	}
}

func assertTurnHandleCanceled(t *testing.T, startedHandle contract.TurnHandle) {
	t.Helper()
	select {
	case <-time.After(time.Second):
		t.Fatal("interrupted handle was not finished")
	case <-startedHandle.Done():
	}
	if !errors.Is(startedHandle.Err(), context.Canceled) {
		t.Fatalf("handle.Err() = %v, want context.Canceled", startedHandle.Err())
	}
}
