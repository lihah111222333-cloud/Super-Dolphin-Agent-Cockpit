package orchestration

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/taskdag"
	agentdto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/agent"
)

// TestTerminalRuntimeReplayAfterDeliveryFailure 验证真实durable投影在DAG或ACK失败后按runtime目标重放。
func TestTerminalRuntimeReplayAfterDeliveryFailure(t *testing.T) {
	for _, success := range []bool{false, true} {
		for _, failure := range []string{"dag", "ack"} {
			name := "failed"
			if success {
				name = "success"
			}
			t.Run(name+"/"+failure, func(t *testing.T) { testTerminalRuntimeDeliveryRetry(t, success, failure) })
		}
	}
	t.Run("canonical-fence", testFailedTurnReplayFence)
	t.Run("process-stop", TestProcessExitSessionCleanupWaitsForSuccessfulACK)
}

// testTerminalRuntimeDeliveryRetry 保留失败领取项并重试唯一投影器，不重新创建runtime。
func testTerminalRuntimeDeliveryRetry(t *testing.T, success bool, failure string) {
	svc, db := newTerminalOutcomeTestService(t)
	agent := addTerminalOutcomeTestAgent(svc)
	ev := terminalOutcomeEvent(success)
	if handled, err := svc.CommitTurnCompleted(context.Background(), ev); err != nil || !handled {
		t.Fatalf("commit: handled=%v err=%v", handled, err)
	}
	runID := int64(1)
	flow := &dagSubscriberFlowSpy{}
	svc.terminalDAG = &DAGSubscriberDeps{LookupStore: &dagSubscriberLookupSpy{nodes: []taskdag.Node{{DagKey: "replay", NodeKey: "node", Status: "running", RunID: &runID}}}, FlowStore: flow, EventBus: svc.eventBus}
	base := svc.terminalOutcomes
	injected := errors.New("delivery unavailable")
	if failure == "dag" {
		flow.completeErr, flow.failErr, flow.enqueueErr = injected, injected, injected
	} else {
		svc.terminalOutcomes = terminalOutcomeAckFailurePort{TerminalOutcomeCommitPort: base, err: injected}
	}
	if err := svc.ProcessTerminalOutcomeOutbox(context.Background(), "first-worker", time.Minute, 1); err == nil {
		t.Fatal("delivery error swallowed")
	}
	assertTurnReplayPublicState(t, svc, agent, success)
	assertProcessExitOutboxStatus(t, db, "claimed", "failed delivery")
	before := *agent.outcome
	flow.completeErr, flow.failErr, flow.enqueueErr = nil, nil, nil
	svc.terminalOutcomes = base
	expireTurnReplayClaim(t, db)
	if err := svc.ProcessTerminalOutcomeOutbox(context.Background(), "retry-worker", time.Minute, 1); err != nil {
		t.Fatalf("retry after runtime already projected: %v", err)
	}
	assertProcessExitOutboxStatus(t, db, "projected", "delivery retry")
	if agent.state != agentdto.StateIdle || *agent.outcome != before {
		t.Fatal("retry changed projected runtime")
	}
	assertTurnReplayDAGCalls(t, flow, success)
}

// assertTurnReplayDAGCalls 验证重试确实重新交付DAG而非仅确认。
func assertTurnReplayDAGCalls(t *testing.T, flow *dagSubscriberFlowSpy, success bool) {
	t.Helper()
	if success && len(flow.completeCalls) != 2 {
		t.Fatalf("complete calls=%d", len(flow.completeCalls))
	}
	if !success && len(flow.failCalls) != 2 {
		t.Fatalf("fail calls=%d", len(flow.failCalls))
	}
}

// assertTurnReplayPublicState 区分本轮失败展示与可继续执行的idle runtime。
func assertTurnReplayPublicState(t *testing.T, svc *service, agent *agentRuntime, success bool) {
	t.Helper()
	if agent.state != agentdto.StateIdle || agent.activeTurnID != "" {
		t.Fatalf("runtime after projection: state=%s turn=%q", agent.state, agent.activeTurnID)
	}
	state, err := svc.GetState(context.Background(), agent.id)
	if err != nil {
		t.Fatal(err)
	}
	if state.State != string(agentdto.StateIdle) {
		t.Fatalf("public state=%s want=idle", state.State)
	}
	wantOutcome := agentdto.OutcomeKindFailure
	if success {
		wantOutcome = agentdto.OutcomeKindSuccess
	}
	if agent.outcome == nil || agent.outcome.Kind != wantOutcome {
		t.Fatalf("turn outcome=%#v want=%s", agent.outcome, wantOutcome)
	}
}

// expireTurnReplayClaim 模拟投影失败后的租约到期，不变异生产路径。
func expireTurnReplayClaim(t *testing.T, db *sql.DB) {
	t.Helper()
	if _, err := db.Exec("UPDATE terminal_outcome_outbox_v2 SET lease_expires_at=0 WHERE status='claimed'"); err != nil {
		t.Fatal(err)
	}
}

// testFailedTurnReplayFence 验证idle幂等不能放宽身份、报告和outcome围栏。
func testFailedTurnReplayFence(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*agentRuntime)
	}{
		{"agent", func(a *agentRuntime) { a.id = "foreign" }},
		{"thread", func(a *agentRuntime) { a.remoteThreadID = "foreign" }},
		{"session", func(a *agentRuntime) { a.launchSeq++ }},
		{"generation", func(a *agentRuntime) { a.sessionGeneration++ }},
		{"head", func(a *agentRuntime) { a.terminalHeadVersion++ }},
		{"report", func(a *agentRuntime) { a.lastReport = "foreign" }},
		{"outcome", func(a *agentRuntime) { a.outcome.Code = "foreign" }},
		{"active-turn", func(a *agentRuntime) { a.activeTurnID = "new-turn" }},
		{"state", func(a *agentRuntime) { a.state = agentdto.StateFailed }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc, _ := newTerminalOutcomeTestService(t)
			agent := addTerminalOutcomeTestAgent(svc)
			commit, err := terminalOutcomeCommitFromEvent(agent, terminalOutcomeEvent(false))
			if err != nil {
				t.Fatal(err)
			}
			if err := svc.projectTerminalOutcomeLocked(context.Background(), agent, commit); err != nil {
				t.Fatal(err)
			}
			if applied, err := terminalProjectionAlreadyApplied(agent, commit); err != nil || !applied {
				t.Fatal("matching failed turn not recognized")
			}
			tc.mutate(agent)
			if applied, err := terminalProjectionAlreadyApplied(agent, commit); err != nil || applied {
				t.Fatal("mismatched replay accepted")
			}
		})
	}
}
