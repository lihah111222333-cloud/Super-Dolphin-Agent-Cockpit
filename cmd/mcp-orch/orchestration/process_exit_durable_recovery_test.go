package orchestration

import (
	"context"
	"database/sql"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"

	agentdto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/agent"
	shareddto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/shared"
	turndto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/turn"
)

// processExitRecoveryBackend 记录原生恢复及提交次数，防止把进程恢复误作旧turn重放。
type processExitRecoveryBackend struct {
	durableRecoveryBackend
	recoverCalls int
	submitCalls  int
}

// Recover 复用严格reservation语义并记录原生恢复调用。
func (b *processExitRecoveryBackend) Recover(ctx context.Context, activation PersistedRuntimeActivation) (LaunchResult, error) {
	b.recoverCalls++
	return b.durableRecoveryBackend.Recover(ctx, activation)
}

// SubmitTurn 只计数显式提交，恢复过程中任何额外调用均由测试拒绝。
func (b *processExitRecoveryBackend) SubmitTurn(ctx context.Context, submission TurnSubmission) (string, error) {
	b.submitCalls++
	return b.cliBackendFixture.SubmitTurn(ctx, submission)
}

type processExitRecoveryCase struct {
	name      string
	turn      bool
	completed bool
	success   bool
	projected bool
}

// TestProcessExitDurableAutoRecovery 用真实SQLite验证退出持久化、同线程新代际及旧turn不重放。
func TestProcessExitDurableAutoRecovery(t *testing.T) {
	t.Run("submit-window", TestProcessExitDuringUnacknowledgedSubmit)
	t.Run("boundary", TestProcessExitRecoveryBoundary)
	t.Run("early-terminal", TestProcessExitAfterEarlyTerminalBeforeAck)
	t.Run("report-fence", TestRecoveryReportPreservesCanonicalFence)
	t.Run("stop-regression", TestIdleStopTerminalLifecycle)
	t.Run("manual-recovery-regression", TestRecoverDurableTerminalBeforeProjection)
	t.Run("manual-active-recovery-rejected", testProcessExitManualActiveRecoveryRejected)
	t.Run("stale-recovery-context-rejected", testProcessExitStaleRecoveryContextRejected)
	for _, tc := range []processExitRecoveryCase{
		{name: "idle-session"},
		{name: "active-turn-no-replay", turn: true},
		{name: "completed-success", turn: true, completed: true, success: true, projected: true},
		{name: "completed-failure", turn: true, completed: true, projected: true},
		{name: "late-success-outbox", turn: true, completed: true, success: true},
		{name: "late-failure-outbox", turn: true, completed: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc, db, agent, backend := processExitAutoRecoveryFixture(t)
			before := prepareProcessExitRecoveryTurn(t, svc, db, agent, tc)
			backend.running, backend.reserved = false, false
			svc.handleProcessExit(context.Background(), agent.id, 1, errors.New("fixture CLI crashed"))
			assertProcessExitRecovered(t, svc, agent, backend)
			wantRows := assertProcessExitRecoveryHistory(t, db, backend, tc, before)
			// 两次旧seq退出在新代际均不得重复恢复或追加历史。
			svc.handleProcessExit(context.Background(), agent.id, 1, errors.New("late duplicate exit"))
			svc.handleProcessExit(context.Background(), agent.id, 0, errors.New("stale exit"))
			if err := svc.ProcessTerminalOutcomeOutbox(context.Background(), "after-recovery", time.Minute, 10); err != nil {
				t.Fatal(err)
			}
			assertProcessExitRecovered(t, svc, agent, backend)
			assertTerminalOutcomeRowCounts(t, db, wantRows, wantRows)
			var projected int
			if err := db.QueryRow("SELECT COUNT(*) FROM terminal_outcome_outbox_v2 WHERE status='projected'").Scan(&projected); err != nil {
				t.Fatal(err)
			}
			if projected != wantRows {
				t.Fatalf("old outbox not ACKed: got=%d want=%d", projected, wantRows)
			}
		})
	}
}

// processExitAutoRecoveryFixture 保留真实store并接线带提交计数的CLI后端。
func processExitAutoRecoveryFixture(t *testing.T) (*service, *sql.DB, *agentRuntime, *processExitRecoveryBackend) {
	t.Helper()
	svc, db := newTerminalOutcomeTestService(t)
	svc.lifecycle.sessionCleaner = &stopTestSessionCleaner{}
	backend := &processExitRecoveryBackend{durableRecoveryBackend: durableRecoveryBackend{cliBackendFixture: cliBackendFixture{running: true}, reserved: true}}
	launcher := &cliLauncher{backend: backend}
	svc.lifecycle.launcher, svc.lifecycle.recovery.launcher, svc.turns.launcher = launcher, launcher, launcher
	agent := svc.newAgentLocked("agent-1")
	agent.state = agentdto.StateIdle
	agent.provider, agent.providerSource = "codex", "inferred"
	agent.threadID, agent.remoteThreadID = "old-thread", "old-thread"
	agent.launchSeq = 1
	agent.cwd = t.TempDir()
	agent.env = []string{"AGENT_PROVIDER=codex", "AGENT_MODEL=test"}
	svc.registry.agents[agent.id] = agent
	if err := svc.BindSessionGeneration(context.Background(), agent.id, 1); err != nil {
		t.Fatal(err)
	}
	return svc, db, agent, backend
}

// commitProcessExitRecoveryTurn 使用实际提交turn身份构造唯一规范终态。
func commitProcessExitRecoveryTurn(t *testing.T, svc *service, agent *agentRuntime, success bool) {
	t.Helper()
	template := terminalOutcomeEvent(success)
	ev := turndto.TurnCompleted{TurnHeader: template.TurnHeader, Success: success, Status: template.Status, Summary: template.Summary}
	ev.AgentID, ev.ThreadID, ev.TurnID = agent.id, agent.threadID, agent.activeTurnID
	terminal, err := turndto.NewTurnTerminalV2(ev, "auto-recovery-completed")
	if err != nil {
		t.Fatal(err)
	}
	ev, err = turndto.AttachCanonicalTurnTerminal(ev, terminal)
	if err != nil {
		t.Fatal(err)
	}
	if handled, err := svc.CommitTurnCompleted(context.Background(), ev); err != nil || !handled {
		t.Fatalf("commit turn: handled=%v err=%v", handled, err)
	}
}

// assertProcessExitRecovered 同时检查内存和durable新head，防止伪idle或围栏丢失。
func assertProcessExitRecovered(t *testing.T, svc *service, agent *agentRuntime, backend *processExitRecoveryBackend) {
	t.Helper()
	assertProcessExitRecoveredIdentity(t, agent)
	if backend.recoverCalls != 1 || !backend.running {
		t.Fatalf("native recovery calls=%d running=%v", backend.recoverCalls, backend.running)
	}
	head, err := svc.terminalHeadReader.LoadTerminalOutcomeCurrentHead(context.Background(), agent.id)
	if err != nil {
		t.Fatal(err)
	}
	if head.State != "active" || head.Generation != 2 || head.PublicThreadID != "old-thread" {
		t.Fatalf("recovered durable head=%+v", head)
	}
}

// testProcessExitManualActiveRecoveryRejected 未终结活动turn不能因手动Recover被隐式重放或清理。
func testProcessExitManualActiveRecoveryRejected(t *testing.T) {
	t.Helper()
	svc, db, agent, backend := processExitAutoRecoveryFixture(t)
	if err := svc.SubmitTurn(context.Background(), TurnSubmission{AgentID: agent.id, ThreadID: agent.threadID, Inputs: []shareddto.InputItem{{Type: "text", Content: "active"}}}); err != nil {
		t.Fatal(err)
	}
	backend.running, backend.reserved = false, false
	before, err := svc.terminalHeadReader.LoadTerminalOutcomeCurrentHead(context.Background(), agent.id)
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.Recover(context.Background(), agent.id); err == nil {
		t.Fatal("manual recovery accepted unterminated active turn")
	}
	after, err := svc.terminalHeadReader.LoadTerminalOutcomeCurrentHead(context.Background(), agent.id)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, after) {
		t.Fatal("rejected recovery mutated durable head")
	}
	assertProcessExitActiveUnchanged(t, agent, backend)
	assertTerminalOutcomeRowCounts(t, db, 0, 0)
}

// testProcessExitStaleRecoveryContextRejected 模拟退出解锁后新代际已绑定，旧恢复上下文不得抢占。
func testProcessExitStaleRecoveryContextRejected(t *testing.T) {
	t.Helper()
	svc, db, agent, backend := processExitAutoRecoveryFixture(t)
	backend.running, backend.reserved = false, false
	svc.handleProcessExit(context.Background(), agent.id, 1, errors.New("first crash"))
	assertProcessExitRecovered(t, svc, agent, backend)
	backend.running, backend.reserved = false, false
	if handled, err := svc.CommitProcessExitTerminalLocked(context.Background(), agent, 2, errors.New("second crash pending handler")); err != nil || !handled {
		t.Fatalf("commit second exit: handled=%v err=%v", handled, err)
	}
	if err := svc.ProcessTerminalOutcomeOutbox(context.Background(), "second-exit", time.Minute, 10); err != nil {
		t.Fatal(err)
	}
	before, err := svc.terminalHeadReader.LoadTerminalOutcomeCurrentHead(context.Background(), agent.id)
	if err != nil {
		t.Fatal(err)
	}
	ctx := contract.WithLaunchSeq(context.Background(), 1)
	if err := svc.lifecycle.recovery.recoverWithReason(ctx, agent.id, recoverReasonProcessExit); err == nil {
		t.Fatal("old process context recovered newer generation")
	}
	after, err := svc.terminalHeadReader.LoadTerminalOutcomeCurrentHead(context.Background(), agent.id)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, after) {
		t.Fatal("stale recovery mutated durable head")
	}
	assertProcessExitStaleRecoveryUnchanged(t, agent, backend)
	assertTerminalOutcomeRowCounts(t, db, 2, 2)
}

// prepareProcessExitRecoveryTurn 建立活动或已完成turn，按场景保留未投影窗口。
func prepareProcessExitRecoveryTurn(t *testing.T, svc *service, db *sql.DB, agent *agentRuntime, tc processExitRecoveryCase) []any {
	t.Helper()
	if tc.turn {
		if err := svc.SubmitTurn(context.Background(), TurnSubmission{AgentID: agent.id, ThreadID: agent.threadID, Inputs: []shareddto.InputItem{{Type: "text", Content: "original turn; must not replay"}}}); err != nil {
			t.Fatal(err)
		}
	}
	var before []any
	if tc.completed {
		commitProcessExitRecoveryTurn(t, svc, agent, tc.success)
		before = completedHistoryRow(t, db)
		if tc.projected {
			if err := svc.ProcessTerminalOutcomeOutbox(context.Background(), "before-exit", time.Minute, 10); err != nil {
				t.Fatal(err)
			}
		}
	}
	return before
}

// assertProcessExitRecoveryHistory 验证崩溃历史唯一且旧模型结果逐列不变。
func assertProcessExitRecoveryHistory(t *testing.T, db *sql.DB, backend *processExitRecoveryBackend, tc processExitRecoveryCase, before []any) int {
	t.Helper()
	wantSubmits := 0
	if tc.turn {
		wantSubmits = 1
	}
	if backend.submitCalls != wantSubmits {
		t.Fatalf("old turn replayed: submissions=%d want=%d", backend.submitCalls, wantSubmits)
	}
	wantRows := 1
	if tc.completed {
		wantRows++
	}
	assertTerminalOutcomeRowCounts(t, db, wantRows, wantRows)
	var processRows int
	if err := db.QueryRow("SELECT COUNT(*) FROM public_terminal_outcome_history WHERE projection_kind='process_failed'").Scan(&processRows); err != nil {
		t.Fatal(err)
	}
	if processRows != 1 {
		t.Fatalf("durable process_failed count=%d", processRows)
	}
	if tc.completed && !reflect.DeepEqual(before, completedHistoryRow(t, db)) {
		t.Fatal("process exit rewrote completed model history")
	}
	return wantRows
}

// assertProcessExitRecoveredIdentity 验证新代际恢复身份没有丢失或残留活动turn。
func assertProcessExitRecoveredIdentity(t *testing.T, agent *agentRuntime) {
	t.Helper()
	if agent.state != agentdto.StateIdle || agent.launchSeq != 2 || agent.sessionGeneration != 2 || agent.threadID != "old-thread" || agent.remoteThreadID != "old-thread" || agent.activeTurnID != "" {
		t.Fatalf("recovery runtime state=%s launch=%d generation=%d thread=%s remote=%s turn=%s error=%s", agent.state, agent.launchSeq, agent.sessionGeneration, agent.threadID, agent.remoteThreadID, agent.activeTurnID, agent.lastError)
	}
}

// assertProcessExitActiveUnchanged 验证拒绝手动恢复后活动turn及后端调用不变。
func assertProcessExitActiveUnchanged(t *testing.T, agent *agentRuntime, backend *processExitRecoveryBackend) {
	t.Helper()
	if agent.state != agentdto.StateTurnRunning || agent.activeTurnID != "cli-turn" || agent.launchSeq != 1 || agent.sessionGeneration != 1 || backend.recoverCalls != 0 || backend.submitCalls != 1 {
		t.Fatalf("rejected recovery mutated active turn: state=%s launch=%d generation=%d recoveries=%d submissions=%d", agent.state, agent.launchSeq, agent.sessionGeneration, backend.recoverCalls, backend.submitCalls)
	}
}

// assertProcessExitStaleRecoveryUnchanged 旧退出上下文不能启动新代际。
func assertProcessExitStaleRecoveryUnchanged(t *testing.T, agent *agentRuntime, backend *processExitRecoveryBackend) {
	t.Helper()
	if agent.launchSeq != 2 || backend.recoverCalls != 1 || backend.running {
		t.Fatalf("stale recovery mutated new generation: launch=%d recoveries=%d running=%v", agent.launchSeq, backend.recoverCalls, backend.running)
	}
}
