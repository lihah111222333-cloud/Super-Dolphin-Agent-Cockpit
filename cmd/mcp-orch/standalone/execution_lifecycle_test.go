package standalone

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
	turndto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/turn"
)

func TestCLIExecutionLifecycleFences(t *testing.T) {
	t.Run("stale_turn_cannot_clear_busy_or_append", testCLIStaleTurn)
	t.Run("duplicate_completion_is_not_redelivered", testCLIDuplicateTerminal)
	t.Run("stopped_pending_launch_is_fenced", testCLIStopReservation)
	t.Run("settled_stopped_session_releases_identity", testCLISettledStopReclaimsIdentity)
	t.Run("settled_failed_session_releases_identity", testCLISettledFailureReclaimsIdentity)
	t.Run("old_exit_callback_cannot_reclaim_new_session", testCLIOldExitCannotReclaimNewSession)
	t.Run("missing_store_fails_before_launch", func(t *testing.T) {
		e := NewExecution(Config{})
		if err := e.requireSessionStore(); err == nil {
			t.Fatal("missing session store accepted")
		}
	})
	t.Run("previous_terminal_during_submit_is_ignored", func(t *testing.T) {
		session := &cliSession{submitting: true, completedTurnID: "previous"}
		if session.acceptCompletion("previous") {
			t.Fatal("previous completed turn accepted during next submission")
		}
	})
	t.Run("direct_launch_sequences_are_monotonic", func(t *testing.T) {
		e := NewExecution(Config{})
		e.BindTerminalSink(func(context.Context, turndto.TurnTerminalV2) {})
		first, err := e.reserveSession(contract.LaunchRequest{AgentID: "agent-1"}, Provider{})
		if err != nil {
			t.Fatal(err)
		}
		second, err := e.reserveSession(contract.LaunchRequest{AgentID: "agent-2"}, Provider{})
		if err != nil {
			t.Fatal(err)
		}
		if first.launchSeq == 0 || second.launchSeq != first.launchSeq+1 {
			t.Fatalf("direct launch sequences = %d, %d; want monotonic allocation", first.launchSeq, second.launchSeq)
		}
		e.launches.Done()
		e.launches.Done()
	})
}

// TestCLILaunchCancellationOwnership 覆盖停止前后绑定取消句柄和整个执行器关闭。
func TestCLILaunchCancellationOwnership(t *testing.T) {
	for _, stopFirst := range []bool{false, true} {
		session := &cliSession{}
		if stopFirst {
			session.stopLaunch()
		}
		ctx, cancel := session.bindLaunchContext(context.Background())
		session.stopLaunch()
		if !errors.Is(ctx.Err(), context.Canceled) {
			t.Fatal("session stop did not cancel pending launch")
		}
		cancel()
	}
	e := NewExecution(Config{})
	session := &cliSession{}
	ctx, cancel := session.bindLaunchContext(context.Background())
	defer cancel()
	e.sessions["agent"] = session
	if err := e.Close(); err != nil {
		t.Fatal(err)
	}
	if !errors.Is(ctx.Err(), context.Canceled) {
		t.Fatal("execution close did not cancel launch context")
	}
}

// TestCLIOutputOverflowCannotReportSuccess 超限正文不得在 provider completed 后变成截断成功。
func TestCLIOutputOverflowCannotReportSuccess(t *testing.T) {
	e := NewExecution(Config{})
	var terminal turndto.TurnTerminalV2
	e.BindTerminalSink(func(_ context.Context, result turndto.TurnTerminalV2) { terminal = result })
	session := &cliSession{threadID: "thread", turnID: "turn", output: strings.Repeat("x", 1024*1024)}
	event := codexEvent{ThreadID: "thread", TurnID: "turn", Delta: "overflow"}
	e.handleCodexEvent(session, "item/agentMessage/delta", event)
	if session.failed == nil {
		t.Fatal("overflow did not fault session")
	}
	event.Delta = "more"
	e.handleCodexEvent(session, "item/agentMessage/delta", event)
	if len(session.output) != 1024*1024 {
		t.Fatal("faulted session continued accepting output")
	}
	event.Turn.ID, event.Turn.Status = "turn", "completed"
	e.handleCodexEvent(session, "turn/completed", event)
	if terminal.Outcome != "failed" || terminal.PublicSummary != "" || terminal.PublicError == nil {
		t.Fatalf("overflow reported as success or leaked partial summary: %#v", terminal)
	}
	if err := turndto.ValidateTurnTerminalV2(terminal); err != nil {
		t.Fatal(err)
	}
}

func testCLIStaleTurn(t *testing.T) {
	t.Helper()
	e := NewExecution(Config{})
	session := &cliSession{threadID: "thread", turnID: "current", output: "current-output"}
	old := codexEvent{ThreadID: "thread", TurnID: "old", Delta: "stale"}
	e.handleCodexEvent(session, "item/agentMessage/delta", old)
	old.Turn.ID, old.Turn.Status = "old", "completed"
	e.handleCodexEvent(session, "turn/completed", old)
	if session.output != "current-output" || session.turnID != "current" {
		t.Fatal("stale turn modified active state")
	}
}

func testCLIDuplicateTerminal(t *testing.T) {
	t.Helper()
	e := NewExecution(Config{})
	count := 0
	e.BindTerminalSink(func(context.Context, turndto.TurnTerminalV2) { count++ })
	session := &cliSession{threadID: "thread", turnID: "turn", output: "done"}
	event := codexEvent{ThreadID: "thread"}
	event.Turn.ID, event.Turn.Status = "turn", "completed"
	e.handleCodexEvent(session, "turn/completed", event)
	e.handleCodexEvent(session, "turn/completed", event)
	if count != 1 || session.turnID != "" {
		t.Fatalf("terminal deliveries=%d active=%q", count, session.turnID)
	}
}

// TestCLICodexTerminalReceiptRetryPreservesCanonicalTruth verifies that a
// strict consumer failure leaves the original canonical terminal addressable;
// retrying the exact provider event performs no provider call and reuses the
// same terminal identity.
func TestCLICodexTerminalReceiptRetryPreservesCanonicalTruth(t *testing.T) {
	e := NewExecution(Config{})
	firstErr := errors.New("consumer temporarily unavailable")
	attempts := 0
	var received []turndto.TurnTerminalV2
	e.BindTerminalSinkWithReceipt(func(_ context.Context, terminal turndto.TurnTerminalV2) error {
		attempts++
		received = append(received, terminal)
		if attempts == 1 {
			return firstErr
		}
		return nil
	})
	session := &cliSession{threadID: "thread-retry", turnID: "turn-retry", output: "stable output"}
	event := codexEvent{ThreadID: "thread-retry"}
	event.Turn.ID, event.Turn.Status = "turn-retry", "completed"

	e.handleCodexEvent(session, "turn/completed", event)
	pendingID := assertFailedCodexReceipt(t, session)
	// A late delta cannot mutate the truth retained for retry.
	event.Delta = "late mutation"
	e.handleCodexEvent(session, "item/agentMessage/delta", event)
	event.Delta = ""
	assertCodexFirstReceiptFailure(t, session, attempts, firstErr)

	// The exact provider event retries only the retained receipt.
	e.handleCodexEvent(session, "turn/completed", event)
	if attempts != 2 || len(received) != 2 || received[0].EventID != pendingID || received[1].EventID != pendingID {
		t.Fatalf("receipt retry attempts=%d received=%#v pendingID=%q", attempts, received, pendingID)
	}
	assertCodexReceiptCleanup(t, session)
}

func assertFailedCodexReceipt(t *testing.T, session *cliSession) string {
	t.Helper()
	session.mu.Lock()
	defer session.mu.Unlock()
	if session.pendingTerminal == nil || !session.pendingTerminal.retryable || session.turnID != "turn-retry" || session.completedTurnID != "" {
		t.Fatalf("failed receipt state = pending:%v retryable:%v active:%q completed:%q", session.pendingTerminal != nil, session.pendingTerminal != nil && session.pendingTerminal.retryable, session.turnID, session.completedTurnID)
	}
	return session.pendingTerminal.terminal.EventID
}

func assertCodexFirstReceiptFailure(t *testing.T, session *cliSession, attempts int, firstErr error) {
	t.Helper()
	session.mu.Lock()
	defer session.mu.Unlock()
	if attempts != 1 || !errors.Is(session.failed, firstErr) {
		t.Fatalf("first receipt attempt=%d failed=%v, want one attempt and %v", attempts, session.failed, firstErr)
	}
}

func assertCodexReceiptCleanup(t *testing.T, session *cliSession) {
	t.Helper()
	session.mu.Lock()
	defer session.mu.Unlock()
	if session.pendingTerminal != nil || session.turnID != "" || session.completedTurnID != "turn-retry" || session.failed != nil {
		t.Fatalf("successful receipt cleanup = pending:%v active:%q completed:%q failed:%v", session.pendingTerminal != nil, session.turnID, session.completedTurnID, session.failed)
	}
}

// TestCLICodexTerminalReceiptConflictDoesNotReplacePendingTruth rejects a
// same-turn payload change while retaining the first canonical terminal for a
// later exact retry.
func TestCLICodexTerminalReceiptConflictDoesNotReplacePendingTruth(t *testing.T) {
	e := NewExecution(Config{})
	want := errors.New("consumer unavailable")
	attempts := 0
	e.BindTerminalSinkWithReceipt(func(context.Context, turndto.TurnTerminalV2) error {
		attempts++
		return want
	})
	session := &cliSession{threadID: "thread-conflict", turnID: "turn-conflict", output: "first"}
	event := codexEvent{ThreadID: "thread-conflict"}
	event.Turn.ID, event.Turn.Status = "turn-conflict", "completed"
	e.handleCodexEvent(session, "turn/completed", event)
	session.mu.Lock()
	firstID := session.pendingTerminal.terminal.EventID
	session.mu.Unlock()

	conflict := event
	conflict.Turn.Status = "failed"
	e.handleCodexEvent(session, "turn/completed", conflict)
	if attempts != 1 {
		t.Fatalf("conflicting terminal triggered another sink call: attempts=%d", attempts)
	}
	session.mu.Lock()
	defer session.mu.Unlock()
	if session.pendingTerminal == nil || session.pendingTerminal.terminal.EventID != firstID || session.turnID != "turn-conflict" {
		t.Fatalf("conflict replaced pending truth: pending=%#v active=%q", session.pendingTerminal, session.turnID)
	}
}

func testCLIStopReservation(t *testing.T) {
	t.Helper()
	e := NewExecution(Config{})
	e.BindTerminalSink(func(context.Context, turndto.TurnTerminalV2) {})
	session, err := e.reserveSession(contract.LaunchRequest{AgentID: "agent"}, Provider{})
	if err != nil {
		t.Fatal(err)
	}
	defer e.launches.Done()
	if err := e.Stop(context.Background(), "agent"); err != nil {
		t.Fatal(err)
	}
	if !session.stopped {
		t.Fatal("stop before process publication did not fence launch")
	}
	if _, err := e.reserveSession(contract.LaunchRequest{AgentID: "agent"}, Provider{}); err == nil {
		t.Fatal("uncertain launch was retried with the same identity")
	}
}

func testCLISettledStopReclaimsIdentity(t *testing.T) {
	t.Helper()
	e := NewExecution(Config{})
	e.BindTerminalSink(func(context.Context, turndto.TurnTerminalV2) {})
	session, err := e.reserveSession(contract.LaunchRequest{AgentID: "agent"}, Provider{})
	if err != nil {
		t.Fatal(err)
	}
	// Model a completed launch whose provider process has already settled. The
	// explicit processSettled receipt is required even though this fixture does
	// not need a concrete process handle; cleanup must not infer settlement from
	// AgentID or a nil handle alone.
	e.finishLaunch(session)
	session.mu.Lock()
	session.threadID, session.activated, session.processSettled = "thread-old", true, true
	session.mu.Unlock()
	if err := e.Stop(context.Background(), "agent"); err != nil {
		t.Fatal(err)
	}
	if _, err := e.session("agent"); !errors.Is(err, contract.ErrAgentNotFound) {
		t.Fatalf("settled stopped session remains reserved: %v", err)
	}
	if _, err := e.reserveSession(contract.LaunchRequest{AgentID: "agent"}, Provider{}); err != nil {
		t.Fatalf("same AgentID was not reusable after settled stop: %v", err)
	}
	e.launches.Done()
	e.launches.Done()
}

func testCLIOldExitCannotReclaimNewSession(t *testing.T) {
	t.Helper()
	e := NewExecution(Config{})
	var terminals []turndto.TurnTerminalV2
	e.BindTerminalSink(func(_ context.Context, terminal turndto.TurnTerminalV2) {
		terminals = append(terminals, terminal)
	})
	old, err := e.reserveSession(contract.LaunchRequest{AgentID: "agent"}, Provider{})
	if err != nil {
		t.Fatal(err)
	}
	e.finishLaunch(old)
	old.mu.Lock()
	old.threadID, old.activated, old.processSettled = "thread-old", true, true
	old.mu.Unlock()
	if err := e.Stop(context.Background(), "agent"); err != nil {
		t.Fatal(err)
	}
	e.launches.Done()
	newSession, err := e.reserveSession(contract.LaunchRequest{AgentID: "agent"}, Provider{})
	if err != nil {
		t.Fatal(err)
	}
	defer e.launches.Done()
	// This callback belongs to the old launch sequence. It may still run after
	// relaunch, but its pointer fence must prevent deleting the replacement.
	e.processExitHandler(old)(contract.CLIProcessExit{AgentID: "agent", SessionID: "thread-old", LaunchSeq: old.launchSeq, Unexpected: true})
	current, err := e.session("agent")
	if err != nil || current != newSession {
		t.Fatalf("old exit callback replaced current session: current=%p want=%p err=%v", current, newSession, err)
	}
	if newSession.failed != nil || newSession.stopped {
		t.Fatal("old exit callback polluted replacement session state")
	}
	newSession.mu.Lock()
	newSession.threadID, newSession.turnID, newSession.output = "thread-new", "turn-new", "new-output"
	newSession.mu.Unlock()
	oldTerminal := codexEvent{ThreadID: "thread-old"}
	oldTerminal.Turn.ID, oldTerminal.Turn.Status = "turn-old", "completed"
	e.handleCodexEvent(old, "turn/completed", oldTerminal)
	newTerminal := codexEvent{ThreadID: "thread-new"}
	newTerminal.Turn.ID, newTerminal.Turn.Status = "turn-new", "completed"
	e.handleCodexEvent(newSession, "turn/completed", newTerminal)
	if len(terminals) != 1 || terminals[0].ThreadID != "thread-new" {
		t.Fatalf("old session terminal polluted replacement: terminals=%#v", terminals)
	}
}

func testCLISettledFailureReclaimsIdentity(t *testing.T) {
	t.Helper()
	e := NewExecution(Config{})
	e.BindTerminalSink(func(context.Context, turndto.TurnTerminalV2) {})
	session, err := e.reserveSession(contract.LaunchRequest{AgentID: "agent"}, Provider{})
	if err != nil {
		t.Fatal(err)
	}
	e.finishLaunch(session)
	session.mu.Lock()
	session.threadID, session.activated, session.processSettled = "thread-failed", true, true
	session.mu.Unlock()
	e.failSession(session, errors.New("provider exited"))
	if _, err := e.session("agent"); !errors.Is(err, contract.ErrAgentNotFound) {
		t.Fatalf("settled failed session remains reserved: %v", err)
	}
	if _, err := e.reserveSession(contract.LaunchRequest{AgentID: "agent"}, Provider{}); err != nil {
		t.Fatalf("same AgentID was not reusable after settled failure: %v", err)
	}
	e.launches.Done()
	e.launches.Done()
}
