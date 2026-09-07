package standalone

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
	turndto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/turn"
)

type terminalReconcileFixture struct {
	execution *Execution
	session   *cliSession
	terminal  turndto.TurnTerminalV2
	calls     int
	sinkErr   error
}

// newTerminalReconcileFixture 复用真实Codex strict receipt路径保留结果，再通过真实exit callback验证reservation不提前释放。
func newTerminalReconcileFixture(t *testing.T) *terminalReconcileFixture {
	t.Helper()
	f := &terminalReconcileFixture{execution: NewExecution(Config{}), sinkErr: errors.New("consumer has only buffered terminal")}
	f.execution.BindTerminalSinkWithReceipt(func(_ context.Context, terminal turndto.TurnTerminalV2) error {
		f.calls++
		if f.calls > 1 && !reflect.DeepEqual(terminal, f.terminal) {
			t.Error("reconciliation changed canonical truth")
		}
		return f.sinkErr
	})
	f.session = &cliSession{request: contract.LaunchRequest{AgentID: "agent"}, provider: Provider{Name: "codex"}, threadID: "thread-retry", turnID: "turn-retry", output: "stable output", launchSeq: 7, activated: true}
	f.execution.sessions["agent"] = f.session
	event := codexEvent{ThreadID: "thread-retry"}
	event.Turn.ID, event.Turn.Status = "turn-retry", "completed"
	f.execution.handleCodexEvent(f.session, "turn/completed", event)
	assertFailedCodexReceipt(t, f.session)
	f.terminal = cloneCLITerminal(f.session.pendingTerminal.terminal)
	f.execution.processExitHandler(f.session)(contract.CLIProcessExit{AgentID: "agent", SessionID: "thread-retry", LaunchSeq: 7, Unexpected: true, Err: errors.New("provider exited")})
	if f.execution.sessions["agent"] != f.session || !f.session.processSettled {
		t.Fatal("exit lost uncertain terminal reservation")
	}
	return f
}

// TestReconcileTerminalReceiptReleasesExitedSession 验证service恢复后仅重交付同一truth并释放旧reservation。
func TestReconcileTerminalReceiptReleasesExitedSession(t *testing.T) {
	t.Run("identity-and-truth", TestReconcileTerminalReceiptRejectsIdentityAndTruth)
	t.Run("failure-retention", TestReconcileTerminalReceiptFailureRetainsTruth)
	t.Run("old-generation", TestReconcileTerminalReceiptOldFenceCannotTouchNewSession)
	f := newTerminalReconcileFixture(t)
	f.sinkErr = nil
	if err := f.execution.ReconcileTerminalReceipt(context.Background(), "agent", "thread-retry", 7, f.terminal); err != nil {
		t.Fatal(err)
	}
	if f.calls != 2 || f.session.pendingTerminal != nil || f.session.completedTurnID != "turn-retry" {
		t.Fatal("exact receipt was not acknowledged")
	}
	if _, err := f.execution.session("agent"); !errors.Is(err, contract.ErrAgentNotFound) {
		t.Fatalf("settled reservation retained: %v", err)
	}
	if err := f.execution.ReconcileTerminalReceipt(context.Background(), "agent", "thread-retry", 7, f.terminal); err == nil {
		t.Fatal("missing canonical proof accepted as duplicate ACK")
	}
}

// TestReconcileTerminalReceiptRejectsIdentityAndTruth 所有错身份、取消与inflight均不得重交付或改变原槽。
func TestReconcileTerminalReceiptRejectsIdentityAndTruth(t *testing.T) {
	for _, name := range []string{"agent", "thread", "generation", "zero-generation", "event", "turn", "summary", "timestamp", "inflight", "cancelled", "nil-context", "missing"} {
		t.Run(name, func(t *testing.T) { testRejectedTerminalReconcile(t, name) })
	}
}

// testRejectedTerminalReconcile 定向破坏单个围栏，验证失败保持同一个pending对象。
func testRejectedTerminalReconcile(t *testing.T, name string) {
	t.Helper()
	f := newTerminalReconcileFixture(t)
	f.sinkErr = nil
	agent, thread, seq, terminal := "agent", "thread-retry", uint64(7), cloneCLITerminal(f.terminal)
	ctx := context.Background()
	mutateTerminalReconcileIdentity(name, &agent, &thread, &seq)
	mutateTerminalReconcileTruth(name, f, &terminal, &ctx)
	pending := f.session.pendingTerminal
	if err := f.execution.ReconcileTerminalReceipt(ctx, agent, thread, seq, terminal); err == nil {
		t.Fatal("invalid reconciliation accepted")
	}
	if f.calls != 1 || f.session.pendingTerminal != pending || f.execution.sessions["agent"] != f.session {
		t.Fatal("rejection altered pending truth or reservation")
	}
}

// mutateTerminalReconcileIdentity 仅改变请求身份，原会话围栏保持不变。
func mutateTerminalReconcileIdentity(name string, agent, thread *string, seq *uint64) {
	switch name {
	case "agent":
		*agent = "other"
	case "thread":
		*thread = "other"
	case "generation":
		*seq = 8
	case "zero-generation":
		*seq = 0
	}
}

// mutateTerminalReconcileTruth 分别覆盖完整payload与回执生命周期拒绝分支。
func mutateTerminalReconcileTruth(name string, f *terminalReconcileFixture, terminal *turndto.TurnTerminalV2, ctx *context.Context) {
	switch name {
	case "event":
		terminal.EventID = "other-event"
	case "turn":
		terminal.TurnID = "other-turn"
	case "summary":
		terminal.PublicSummary = "different successful output"
	case "timestamp":
		terminal.OccurredAt = "2026-09-01T00:00:00Z"
	case "inflight":
		f.session.pendingTerminal.inFlight = true
	case "cancelled":
		var cancel context.CancelFunc
		*ctx, cancel = context.WithCancel(*ctx)
		cancel()
	case "nil-context":
		*ctx = nil
	case "missing":
		f.session.pendingTerminal = nil
		f.session.completedTurnID = f.terminal.TurnID
	}
}

// TestReconcileTerminalReceiptFailureRetainsTruth 验证sink仍失败时原对象保留，可用同一canonical值再次确认。
func TestReconcileTerminalReceiptFailureRetainsTruth(t *testing.T) {
	f := newTerminalReconcileFixture(t)
	pending := f.session.pendingTerminal
	if err := f.execution.ReconcileTerminalReceipt(context.Background(), "agent", "thread-retry", 7, f.terminal); !errors.Is(err, f.sinkErr) {
		t.Fatalf("sink error hidden: %v", err)
	}
	if f.session.pendingTerminal != pending || !pending.retryable || pending.inFlight || f.calls != 2 {
		t.Fatal("failed delivery lost pending truth")
	}
	f.sinkErr = nil
	if err := f.execution.ReconcileTerminalReceipt(context.Background(), "agent", "thread-retry", 7, f.terminal); err != nil {
		t.Fatal(err)
	}
	if f.calls != 3 {
		t.Fatalf("delivery attempts=%d", f.calls)
	}
}

// TestReconcileTerminalReceiptOldFenceCannotTouchNewSession 验证按AgentID替换会话后旧请求不能重交付新pending。
func TestReconcileTerminalReceiptOldFenceCannotTouchNewSession(t *testing.T) {
	old := newTerminalReconcileFixture(t)
	next := newTerminalReconcileFixture(t)
	next.session.launchSeq = 8
	old.execution.sessions["agent"] = next.session
	if err := old.execution.ReconcileTerminalReceipt(context.Background(), "agent", "thread-retry", 7, old.terminal); err == nil {
		t.Fatal("old fence accepted new session")
	}
	if old.calls != 1 || next.calls != 1 || next.session.pendingTerminal == nil || old.execution.sessions["agent"] != next.session {
		t.Fatal("old receipt touched replacement session")
	}
}
