package orchestration

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
	shareddto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/shared"
)

type submitWindowCrashBackend struct {
	*processExitRecoveryBackend
	onSubmit func()
	crash    error
}

// SubmitTurn 模拟provider已接收操作但未返回turn身份便异常退出。
func (b *submitWindowCrashBackend) SubmitTurn(context.Context, TurnSubmission) (string, error) {
	b.submitCalls++
	b.running, b.reserved = false, false
	b.onSubmit()
	return "", b.crash
}

// TestProcessExitDuringUnacknowledgedSubmit 通过真实service提交边界验证两个退出顺序均不伪造确认或重放。
func TestProcessExitDuringUnacknowledgedSubmit(t *testing.T) {
	for _, exitFirst := range []bool{false, true} {
		name := "rpc-failure-before-exit"
		if exitFirst {
			name = "exit-before-rpc-failure"
		}
		t.Run(name, func(t *testing.T) { testProcessExitSubmitWindow(t, exitFirst) })
	}
}

// testProcessExitSubmitWindow 在真实SQLite与service owner上冻结未确认提交及一次性退出交错。
func testProcessExitSubmitWindow(t *testing.T, exitFirst bool) {
	t.Helper()
	svc, db, agent, backend := processExitAutoRecoveryFixture(t)
	ctx := context.Background()
	crash := errors.New("CLI died before turn/start response")
	exit := contract.CLIProcessExit{AgentID: agent.id, SessionID: agent.threadID, LaunchSeq: 1, Unexpected: true, Err: crash}
	spy := &submitWindowCrashBackend{processExitRecoveryBackend: backend, crash: crash}
	spy.onSubmit = func() {
		assertUnacknowledgedProcessExitHead(t, svc, agent)
		if exitFirst {
			svc.handleCLIProcessExit(exit)
			if backend.recoverCalls != 0 {
				t.Fatal("recovered while unacknowledged submit still owned old generation")
			}
		}
	}
	svc.lifecycle.launcher.(*cliLauncher).backend = spy
	err := svc.SubmitTurn(ctx, TurnSubmission{AgentID: agent.id, ThreadID: agent.threadID,
		Inputs: []shareddto.InputItem{{Type: "text", Content: "unacknowledged operation; never replay"}}})
	if err == nil {
		t.Fatal("unacknowledged provider request reported success")
	}
	if !exitFirst {
		svc.handleCLIProcessExit(exit)
	}
	assertProcessExitRecovered(t, svc, agent, backend)
	if backend.submitCalls != 1 {
		t.Fatalf("unacknowledged request replayed: submissions=%d", backend.submitCalls)
	}
	assertTerminalOutcomeRowCounts(t, db, 1, 1)
	var turnID, kind string
	if err := db.QueryRow("SELECT provider_turn_id, projection_kind FROM public_terminal_outcome_history").Scan(&turnID, &kind); err != nil {
		t.Fatal(err)
	}
	if turnID != "session-terminal:1" || kind != "process_failed" {
		t.Fatalf("fabricated provider acknowledgment: turn=%q kind=%q", turnID, kind)
	}
	if err := svc.ProcessTerminalOutcomeOutbox(ctx, "submit-window-exit", time.Minute, 10); err != nil {
		t.Fatal(err)
	}
	assertProcessExitRecovered(t, svc, agent, backend)
}

// assertUnacknowledgedProcessExitHead 确认本地provisional尚未冒充provider确认写入head。
func assertUnacknowledgedProcessExitHead(t *testing.T, svc *service, agent *agentRuntime) {
	t.Helper()
	head, err := svc.terminalHeadReader.LoadTerminalOutcomeCurrentHead(context.Background(), agent.id)
	if err != nil {
		t.Fatal(err)
	}
	if head.ProviderTurnID != "session-terminal:1" || agent.activeTurnID == "" {
		t.Fatalf("submit window not established: head=%+v turn=%q", head, agent.activeTurnID)
	}
}
