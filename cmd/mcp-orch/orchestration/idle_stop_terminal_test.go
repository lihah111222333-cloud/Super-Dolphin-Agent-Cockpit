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
)

// TestIdleStopTerminalLifecycle 验证已完成 turn 的显式归档只新增进程生命周期终态。
func TestIdleStopTerminalLifecycle(t *testing.T) {
	t.Run("completed-idle", testCompletedIdleStop)
	t.Run("durable-before-projection", testStopBeforeTerminalProjection)
	t.Run("active-stop", testActiveTurnExplicitStop)
	t.Run("no-turn-session", testUnstartedSessionStop)
	t.Run("unexpected-active", testUnexpectedActiveExit)
	t.Run("unexpected-idle", testUnexpectedIdleExit)
	t.Run("fence-rejection", testStopFenceRejection)
	t.Run("activation-error", testStopActivationError)
	t.Run("commit-error", testUnexpectedExitCommitError)
}

// testCompletedIdleStop 归档后复查旧行完整性、重复停止与新代际冷读。
func testCompletedIdleStop(t *testing.T) {
	svc, db, agent := completedIdleStopFixture(t)
	before := completedHistoryRow(t, db)
	archived, err := svc.archiveAgentViaLauncher(context.Background(), agent.id, "archived")
	if err != nil || !archived {
		t.Fatalf("archive: archived=%v err=%v", archived, err)
	}
	if err := svc.ProcessTerminalOutcomeOutbox(context.Background(), "stop-projector", time.Minute, 10); err != nil {
		t.Fatal(err)
	}
	if agent.state != agentdto.StateStopped {
		t.Fatalf("idle completed turn stop remains %s; expected stopped lifecycle", agent.state)
	}
	assertTerminalOutcomeRowCounts(t, db, 2, 2)
	if !reflect.DeepEqual(before, completedHistoryRow(t, db)) {
		t.Fatal("completed turn history rewritten")
	}
	var count int
	if err := db.QueryRow("SELECT COUNT(*) FROM public_terminal_outcome_history WHERE projection_kind='turn_completed'").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("model completion duplicated: %d", count)
	}
	assertIdleStopColdRead(t, svc, db, agent)
}

// assertIdleStopColdRead 验证重复停止不追加历史，恢复代际后旧终态不覆盖新状态。
func assertIdleStopColdRead(t *testing.T, svc *service, db *sql.DB, agent *agentRuntime) {
	t.Helper()
	snapshot := AgentSnapshot{AgentID: agent.id, State: string(agent.state)}
	if err := svc.overlayPublicTerminalOutcome(context.Background(), &snapshot); err != nil {
		t.Fatal(err)
	}
	if snapshot.State != "stopped" {
		t.Fatalf("cold read undid stop: %s", snapshot.State)
	}
	if _, err := svc.archiveAgentViaLauncher(context.Background(), agent.id, "archived"); err != nil {
		t.Fatal(err)
	}
	assertTerminalOutcomeRowCounts(t, db, 2, 2)
	agent.state = agentdto.StateIdle
	agent.launchSeq++
	agent.remoteThreadID = "new-thread"
	agent.threadID = "new-thread"
	if err := svc.BindSessionGeneration(context.Background(), agent.id, agent.sessionGeneration+1); err != nil {
		t.Fatal(err)
	}
	snapshot.State = "idle"
	if err := svc.overlayPublicTerminalOutcome(context.Background(), &snapshot); err != nil {
		t.Fatal(err)
	}
	if snapshot.State != "idle" {
		t.Fatal("old process terminal overlaid recovered active head")
	}

}

// completedIdleStopFixture 真实落库并投影 turn_completed 后保留仍存活的 launcher runtime。
func completedIdleStopFixture(t *testing.T) (*service, *sql.DB, *agentRuntime) {
	t.Helper()
	svc, db := newTerminalOutcomeTestService(t)
	agent := addTerminalOutcomeTestAgent(svc)
	if handled, err := svc.CommitTurnCompleted(context.Background(), terminalOutcomeEvent(true)); err != nil || !handled {
		t.Fatalf("commit turn: %v", err)
	}
	if err := svc.ProcessTerminalOutcomeOutbox(context.Background(), "turn-projector", time.Minute, 10); err != nil {
		t.Fatal(err)
	}
	if agent.state != agentdto.StateIdle || agent.activeTurnID != "" {
		t.Fatal("fixture is not completed idle")
	}
	svc.lifecycle.launcher = &idleStopLauncher{}
	return svc, db, agent
}

type idleStopLauncher struct{ archiveAgentLauncher }

// IsRunning 在真实归档动作后报告进程已退出，不把元数据当作活体。
func (l *idleStopLauncher) IsRunning(_ context.Context, agent *agentRuntime) bool {
	return agent != nil && l.archivedAgentID == ""
}

// completedHistoryRow 动态枚举实际SQL列以证明原始模型终态逐字段未被修改。
func completedHistoryRow(t *testing.T, db *sql.DB) []any {
	t.Helper()
	rows, err := db.Query("SELECT * FROM public_terminal_outcome_history WHERE projection_kind='turn_completed'")
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := rows.Close(); err != nil {
			t.Error(err)
		}
	}()
	columns, err := rows.Columns()
	if err != nil {
		t.Fatal(err)
	}
	if !rows.Next() {
		t.Fatal("completed history absent")
	}
	values := make([]any, len(columns))
	dest := make([]any, len(columns))
	for i := range values {
		dest[i] = &values[i]
	}
	if err := rows.Scan(dest...); err != nil {
		t.Fatal(err)
	}
	if rows.Next() {
		t.Fatal("model terminal duplicated")
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return values
}

// testActiveTurnExplicitStop 保留真实turn身份直到process_stopped落库及投影。
func testActiveTurnExplicitStop(t *testing.T) {
	svc, db := newTerminalOutcomeTestService(t)
	agent := addTerminalOutcomeTestAgent(svc)
	svc.lifecycle.launcher = &idleStopLauncher{}
	if _, err := svc.archiveAgentViaLauncher(context.Background(), agent.id, "stop"); err != nil {
		t.Fatal(err)
	}
	if err := svc.ProcessTerminalOutcomeOutbox(context.Background(), "active-stop", time.Minute, 10); err != nil {
		t.Fatal(err)
	}
	outcome, err := svc.terminalOutcomes.GetPublicTerminalOutcome(context.Background(), agent.id)
	if err != nil {
		t.Fatal(err)
	}
	if agent.state != agentdto.StateStopped || outcome.ProjectionKind != "process_stopped" || outcome.Identity.ProviderTurnID != "turn-1" {
		t.Fatalf("active stop identity: state=%s outcome=%+v", agent.state, outcome)
	}
	assertTerminalOutcomeRowCounts(t, db, 1, 1)
}

// testUnstartedSessionStop 无模型turn时沿用原session head，不制造turn_completed。
func testUnstartedSessionStop(t *testing.T) {
	svc, db := newTerminalOutcomeTestService(t)
	agent := svc.newAgentLocked("agent-1")
	agent.state = agentdto.StateIdle
	agent.threadID, agent.remoteThreadID = "thread-1", "thread-1"
	agent.launchSeq = 3
	svc.registry.agents[agent.id] = agent
	if err := svc.BindSessionGeneration(context.Background(), agent.id, 7); err != nil {
		t.Fatal(err)
	}
	svc.lifecycle.launcher = &idleStopLauncher{}
	if _, err := svc.archiveAgentViaLauncher(context.Background(), agent.id, "stop"); err != nil {
		t.Fatal(err)
	}
	if err := svc.ProcessTerminalOutcomeOutbox(context.Background(), "session-stop", time.Minute, 10); err != nil {
		t.Fatal(err)
	}
	outcome, err := svc.terminalOutcomes.GetPublicTerminalOutcome(context.Background(), agent.id)
	if err != nil {
		t.Fatal(err)
	}
	if outcome.Identity.ProviderTurnID != "session-terminal:3" || agent.state != agentdto.StateStopped {
		t.Fatalf("session stop failed: %+v", outcome)
	}
	assertTerminalOutcomeRowCounts(t, db, 1, 1)
}

// testUnexpectedActiveExit 活动turn真实异常退出仍提交失败终态。
func testUnexpectedActiveExit(t *testing.T) {
	svc, db := newTerminalOutcomeTestService(t)
	agent := addTerminalOutcomeTestAgent(svc)
	if handled, err := svc.CommitProcessExitTerminalLocked(context.Background(), agent, agent.launchSeq, errors.New("crash")); err != nil || !handled {
		t.Fatalf("crash commit: %v", err)
	}
	if err := svc.ProcessTerminalOutcomeOutbox(context.Background(), "crash", time.Minute, 10); err != nil {
		t.Fatal(err)
	}
	outcome, err := svc.terminalOutcomes.GetPublicTerminalOutcome(context.Background(), agent.id)
	if err != nil {
		t.Fatal(err)
	}
	if outcome.ProjectionKind != "process_failed" || outcome.Identity.ProviderTurnID != "turn-1" || agent.state != agentdto.StateFailed {
		t.Fatalf("crash lost active identity: %+v", outcome)
	}
	assertTerminalOutcomeRowCounts(t, db, 1, 1)
}

// testUnexpectedIdleExit 已完成turn后的异常退出也使用独立进程生命周期。
func testUnexpectedIdleExit(t *testing.T) {
	svc, db, agent := completedIdleStopFixture(t)
	if handled, err := svc.CommitProcessExitTerminalLocked(context.Background(), agent, agent.launchSeq, errors.New("crash")); err != nil || !handled {
		t.Fatalf("idle crash: %v", err)
	}
	if err := svc.ProcessTerminalOutcomeOutbox(context.Background(), "idle-crash", time.Minute, 10); err != nil {
		t.Fatal(err)
	}
	if agent.state != agentdto.StateFailed {
		t.Fatal("idle crash did not fail runtime")
	}
	assertTerminalOutcomeRowCounts(t, db, 2, 2)
}

// testStopFenceRejection 外部head身份或版本不匹配时不能先进入stopping。
func testStopFenceRejection(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*agentRuntime)
	}{
		{"thread", func(a *agentRuntime) { a.remoteThreadID = "foreign" }},
		{"launch", func(a *agentRuntime) { a.launchSeq++ }},
		{"generation", func(a *agentRuntime) { a.sessionGeneration++ }},
		{"head-version", func(a *agentRuntime) { a.terminalHeadVersion++ }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc, db, agent := completedIdleStopFixture(t)
			tc.mutate(agent)
			if _, err := svc.markStoppingLocked(context.Background(), agent, "stop"); !errors.Is(err, contract.ErrTerminalOutcomeConflict) {
				t.Fatalf("fence err: %v", err)
			}
			if agent.state != agentdto.StateIdle || agent.stopRequested {
				t.Fatal("rejected stop changed runtime")
			}
			assertTerminalOutcomeRowCounts(t, db, 1, 1)
		})
	}
}

// testStopActivationError 新head写入失败必须阻断停止动作。
func testStopActivationError(t *testing.T) {
	svc, db, agent := completedIdleStopFixture(t)
	failure := errors.New("activation failure")
	svc.turns.terminalOutcomes = failingTerminalOutcomePort{err: failure}
	if _, err := svc.markStoppingLocked(context.Background(), agent, "stop"); !errors.Is(err, failure) {
		t.Fatalf("activation: %v", err)
	}
	if agent.state != agentdto.StateIdle || agent.stopRequested {
		t.Fatal("activation failure changed runtime")
	}
	assertTerminalOutcomeRowCounts(t, db, 1, 1)
}

// testUnexpectedExitCommitError 异常退出落库失败不能推进可见终态。
func testUnexpectedExitCommitError(t *testing.T) {
	svc, db := newTerminalOutcomeTestService(t)
	agent := addTerminalOutcomeTestAgent(svc)
	failure := errors.New("commit failure")
	svc.terminalOutcomes = failingTerminalOutcomePort{err: failure}
	if handled, err := svc.CommitProcessExitTerminalLocked(context.Background(), agent, agent.launchSeq, errors.New("crash")); !handled || !errors.Is(err, failure) {
		t.Fatalf("commit error: %v", err)
	}
	if agent.state != agentdto.StateTurnRunning || agent.activeTurnID != "turn-1" {
		t.Fatal("failed commit changed runtime")
	}
	assertTerminalOutcomeRowCounts(t, db, 0, 0)
}

// testStopBeforeTerminalProjection 复现真实get_report先读到durable idle、后台outbox尚未领取的时序。
func testStopBeforeTerminalProjection(t *testing.T) {
	svc, db := newTerminalOutcomeTestService(t)
	agent := addTerminalOutcomeTestAgent(svc)
	if handled, err := svc.CommitTurnCompleted(context.Background(), terminalOutcomeEvent(true)); err != nil || !handled {
		t.Fatalf("commit: %v", err)
	}
	snapshot := AgentSnapshot{AgentID: agent.id, State: string(agent.state)}
	if err := svc.overlayPublicTerminalOutcome(context.Background(), &snapshot); err != nil {
		t.Fatal(err)
	}
	if snapshot.State != "idle" || agent.state != agentdto.StateTurnRunning {
		t.Fatal("did not reproduce durable/runtime split")
	}
	before := completedHistoryRow(t, db)
	svc.lifecycle.launcher = &idleStopLauncher{}
	if _, err := svc.archiveAgentViaLauncher(context.Background(), agent.id, "stop"); err != nil {
		t.Fatal(err)
	}
	if err := svc.ProcessTerminalOutcomeOutbox(context.Background(), "pending-stop", time.Minute, 10); err != nil {
		t.Fatal(err)
	}
	assertPendingStopApplied(t, db, agent, before)
}

// assertPendingStopApplied 检查旧模型历史不变且两条终态 outbox 均已确认。
func assertPendingStopApplied(t *testing.T, db *sql.DB, agent *agentRuntime, before []any) {
	t.Helper()
	if agent.state != agentdto.StateStopped {
		t.Fatalf("pending-window stop stuck: %s", agent.state)
	}
	if !reflect.DeepEqual(before, completedHistoryRow(t, db)) {
		t.Fatal("pending-window rewrote model outcome")
	}
	var projected int
	if err := db.QueryRow("SELECT COUNT(*) FROM terminal_outcome_outbox_v2 WHERE status='projected'").Scan(&projected); err != nil {
		t.Fatal(err)
	}
	if projected != 2 {
		t.Fatalf("old/new outbox not acknowledged: %d", projected)
	}
}
