package orchestration

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
	shareddto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/shared"
	turndto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/turn"
)

type earlyTerminalExitBackend struct {
	*processExitRecoveryBackend
	beforeAck func()
	receipt   contract.ProviderSubmissionReceipt
	reconcile func(turndto.TurnTerminalV2) error
}

// SubmitTurn 在返回真实turn身份前按确定顺序交付早到终态和退出。
func (b *earlyTerminalExitBackend) SubmitTurn(context.Context, TurnSubmission) (string, error) {
	b.submitCalls++
	b.beforeAck()
	return b.receipt.TurnID, nil
}

// SubmitTurnWithReceipt 与普通入口共享同一次provider提交，不将恢复当新输入。
func (b *earlyTerminalExitBackend) SubmitTurnWithReceipt(ctx context.Context, req TurnSubmission) (contract.ProviderSubmissionReceipt, error) {
	if _, err := b.SubmitTurn(ctx, req); err != nil {
		return contract.ProviderSubmissionReceipt{}, err
	}
	return b.receipt, nil
}

// TestProcessExitAfterEarlyTerminalBeforeAck 锁住先交付早到模型结果再消费退出的service顺序。
func TestProcessExitAfterEarlyTerminalBeforeAck(t *testing.T) {
	for _, typed := range []bool{false, true} {
		name := "ordinary-submit"
		if typed {
			name = "typed-native-receipt"
		}
		t.Run(name, func(t *testing.T) { testEarlyTerminalExitBeforeAck(t, typed) })
	}
}

// testEarlyTerminalExitBeforeAck 用真实SQLite确保早到结果不被进程终态抢占或新代际丢弃。
func testEarlyTerminalExitBeforeAck(t *testing.T, typed bool) {
	t.Helper()
	svc, db, agent, backend := processExitAutoRecoveryFixture(t)
	receipt := contract.ProviderSubmissionReceipt{Provider: "codex", AgentID: agent.id, ThreadID: agent.threadID, TurnID: "early-completed-turn", ReceiptID: "native-ack", LaunchSeq: 1}
	spy := &earlyTerminalExitBackend{processExitRecoveryBackend: backend, receipt: receipt}
	terminal := earlyProcessExitTerminal(t, receipt)
	spy.reconcile = func(got turndto.TurnTerminalV2) error {
		if got.EventID != terminal.EventID || got.TurnID != terminal.TurnID {
			return errors.New("wrong retained terminal")
		}
		return svc.handleRemoteTurnTerminalResult(context.Background(), got)
	}
	spy.beforeAck = func() {
		if err := svc.handleRemoteTurnTerminalResult(context.Background(), terminal); !errors.Is(err, errRemoteTerminalRecoveryRequired) {
			t.Fatalf("early terminal must remain buffered: %v", err)
		}
		if svc.turns.pendingRemoteTerminalCount != 1 {
			t.Fatal("early terminal was not retained")
		}
		backend.running, backend.reserved = false, false
		svc.handleCLIProcessExit(contract.CLIProcessExit{AgentID: receipt.AgentID, SessionID: receipt.ThreadID, LaunchSeq: 1, Unexpected: true, Err: errors.New("CLI exited after completed before ACK returned")})
		if backend.recoverCalls != 0 {
			t.Fatal("exit recovered before native ACK settled")
		}
	}
	svc.lifecycle.launcher.(*cliLauncher).backend = spy
	submitEarlyTerminalExit(t, svc, receipt, typed)
	assertEarlyTerminalExitHistory(t, db, receipt.TurnID)
	assertProcessExitRecovered(t, svc, agent, backend)
	if backend.submitCalls != 1 {
		t.Fatalf("provider input replayed: %d submissions", backend.submitCalls)
	}
	if err := svc.ProcessTerminalOutcomeOutbox(context.Background(), "early-terminal-exit", time.Minute, 10); err != nil {
		t.Fatal(err)
	}
	assertEarlyTerminalExitHistory(t, db, receipt.TurnID)
	assertProcessExitRecovered(t, svc, agent, backend)
}

// earlyProcessExitTerminal 构造与稍后native ACK完全一致的canonical成功终态。
func earlyProcessExitTerminal(t *testing.T, receipt contract.ProviderSubmissionReceipt) turndto.TurnTerminalV2 {
	t.Helper()
	template := terminalOutcomeEvent(true)
	ev := turndto.TurnCompleted{TurnHeader: template.TurnHeader, Success: true, Status: "completed", Summary: "early public success must survive process exit"}
	ev.AgentID, ev.ThreadID, ev.TurnID = receipt.AgentID, receipt.ThreadID, receipt.TurnID
	terminal, err := turndto.NewTurnTerminalV2(ev, "early-terminal-before-exit")
	if err != nil {
		t.Fatal(err)
	}
	return terminal
}

// submitEarlyTerminalExit 只经两个真实service入口投递并核对typed确认身份。
func submitEarlyTerminalExit(t *testing.T, svc *service, receipt contract.ProviderSubmissionReceipt, typed bool) {
	t.Helper()
	req := TurnSubmission{AgentID: receipt.AgentID, ThreadID: receipt.ThreadID, Inputs: []shareddto.InputItem{{Type: "text", Content: "one operation only"}}}
	if !typed {
		if err := svc.SubmitTurn(context.Background(), req); err != nil {
			t.Fatal(err)
		}
		return
	}
	got, err := svc.SubmitTurnWithReceipt(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if got != receipt {
		t.Fatalf("native ACK changed: got=%+v want=%+v", got, receipt)
	}
}

// assertEarlyTerminalExitHistory 先检查模型成功仍在历史中，再检查独立进程失败，避免仅看新idle掩盖结果丢失。
func assertEarlyTerminalExitHistory(t *testing.T, db *sql.DB, turnID string) {
	t.Helper()
	var completed, failed int
	if err := db.QueryRow("SELECT COUNT(*) FROM public_terminal_outcome_history WHERE projection_kind='turn_completed' AND provider_turn_id=? AND public_report=?", turnID, "early public success must survive process exit").Scan(&completed); err != nil {
		t.Fatal(err)
	}
	if completed != 1 {
		t.Fatalf("buffered model success lost before exit recovery: completed rows=%d", completed)
	}
	if err := db.QueryRow("SELECT COUNT(*) FROM public_terminal_outcome_history WHERE projection_kind='process_failed' AND provider_turn_id='session-terminal:1'").Scan(&failed); err != nil {
		t.Fatal(err)
	}
	if failed != 1 {
		t.Fatalf("independent process failure rows=%d", failed)
	}
	assertTerminalOutcomeRowCounts(t, db, 2, 2)
}

// ReconcileTerminalReceipt 模拟固定旧执行器的严格重交付，不把方法调用本身当成ACK。
func (b *earlyTerminalExitBackend) ReconcileTerminalReceipt(_ context.Context, agentID, threadID string, seq uint64, terminal turndto.TurnTerminalV2) error {
	if agentID != b.receipt.AgentID || threadID != b.receipt.ThreadID || seq != b.receipt.LaunchSeq {
		return errors.New("old terminal receipt fence mismatch")
	}
	return b.reconcile(terminal)
}
