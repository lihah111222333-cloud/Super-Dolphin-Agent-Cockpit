package orchestration

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/taskdag"
	agentdto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/agent"
)

// TestCompletedTurnOutboxAfterProcessCleanup 验证旧领取项在进程清理及恢复后仍能交付DAG并确认。
func TestCompletedTurnOutboxAfterProcessCleanup(t *testing.T) {
	t.Run("foreign-runtime", testCompletedTurnSupersessionRejectsForeignRuntime)
	t.Run("generation-fence", testCompletedTurnSupersessionRequiresNewGeneration)
	for _, recoverFirst := range []bool{false, true} {
		name := "cleaned"
		if recoverFirst {
			name = "recovered"
		}
		t.Run(name, func(t *testing.T) { testDelayedCompletedTurnOutbox(t, recoverFirst) })
	}
}

// testDelayedCompletedTurnOutbox 用真实数据库保留旧lease，让新process条目先ACK。
func testDelayedCompletedTurnOutbox(t *testing.T, recoverFirst bool) {
	svc, db := newTerminalOutcomeTestService(t)
	agent := addTerminalOutcomeTestAgent(svc)
	svc.lifecycle.sessionCleaner = &stopTestSessionCleaner{}
	ev := terminalOutcomeEvent(true)
	ev.Result = "delayed private artifact"
	if handled, err := svc.CommitTurnCompleted(context.Background(), ev); err != nil || !handled {
		t.Fatalf("commit: %v", err)
	}
	old, err := svc.terminalOutcomes.ClaimTerminalOutcomeOutbox(context.Background(), "old-worker", time.Minute, 1)
	if err != nil || len(old) != 1 {
		t.Fatalf("claim: %v len=%d", err, len(old))
	}
	svc.lifecycle.launcher = &idleStopLauncher{}
	if _, err := svc.archiveAgentViaLauncher(context.Background(), agent.id, "stop"); err != nil {
		t.Fatal(err)
	}
	if err := svc.ProcessTerminalOutcomeOutbox(context.Background(), "new-worker", time.Minute, 10); err != nil {
		t.Fatal(err)
	}
	if agent.state != agentdto.StateStopped || agent.sessionGeneration != 0 {
		t.Fatalf("process not cleaned: %s/%d", agent.state, agent.sessionGeneration)
	}
	if recoverFirst {
		recoverDelayedTurnAgent(t, svc, agent, old[0].Outcome.Identity.Generation+1)
	}
	assertDelayedTurnReplay(t, svc, db, agent)
}

// recoverDelayedTurnAgent 激活严格更高会话及代际的真实durable head。
func recoverDelayedTurnAgent(t *testing.T, svc *service, agent *agentRuntime, generation uint64) {
	agent.state = agentdto.StateIdle
	agent.launchSeq++
	agent.threadID, agent.remoteThreadID = "recovered-thread", "recovered-thread"
	if err := svc.BindSessionGeneration(context.Background(), agent.id, generation); err != nil {
		t.Fatal(err)
	}
}

// assertDelayedTurnReplay 检查旧投影不改runtime且DAG与ACK最终完成。
func assertDelayedTurnReplay(t *testing.T, svc *service, db *sql.DB, agent *agentRuntime) {
	expectedState, expectedGeneration := agent.state, agent.sessionGeneration
	flow := &dagSubscriberFlowSpy{}
	svc.terminalDAG = &DAGSubscriberDeps{LookupStore: &dagSubscriberLookupSpy{nodes: []taskdag.Node{{DagKey: "delayed", NodeKey: "node", Status: "running"}}}, FlowStore: flow, EventBus: svc.eventBus}
	if _, err := db.Exec("UPDATE terminal_outcome_outbox_v2 SET lease_expires_at=0 WHERE status='claimed'"); err != nil {
		t.Fatal(err)
	}
	if err := svc.ProcessTerminalOutcomeOutbox(context.Background(), "retry-worker", time.Minute, 10); err != nil {
		t.Fatal(err)
	}
	if agent.state != expectedState || agent.sessionGeneration != expectedGeneration {
		t.Fatal("old turn rewrote lifecycle")
	}
	if len(flow.completeCalls) != 1 || string(flow.completeCalls[0].Result) != `{"text":"delayed private artifact"}` {
		t.Fatalf("DAG result: %+v", flow.completeCalls)
	}
	var projected int
	if err := db.QueryRow("SELECT COUNT(*) FROM terminal_outcome_outbox_v2 WHERE status='projected'").Scan(&projected); err != nil {
		t.Fatal(err)
	}
	if projected != 2 {
		t.Fatalf("projected=%d", projected)
	}
}

// testCompletedTurnSupersessionRejectsForeignRuntime 确保更高代际不能掩盖当前runtime与durable身份不一致。
func testCompletedTurnSupersessionRejectsForeignRuntime(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*agentRuntime)
	}{
		{"agent", func(a *agentRuntime) { a.id = "foreign" }},
		{"thread", func(a *agentRuntime) { a.remoteThreadID = "foreign" }},
		{"generation", func(a *agentRuntime) { a.sessionGeneration++ }},
		{"launch", func(a *agentRuntime) { a.launchSeq++ }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc, _, agent := completedIdleStopFixture(t)
			old, err := svc.terminalOutcomes.GetPublicTerminalOutcome(context.Background(), agent.id)
			if err != nil {
				t.Fatal(err)
			}
			recoverDelayedTurnAgent(t, svc, agent, old.Identity.Generation+1)
			tc.mutate(agent)
			superseded, err := svc.completedTurnSupersededByProcessHead(context.Background(), agent, old.Identity)
			if superseded || err == nil {
				t.Fatalf("foreign runtime accepted: superseded=%v err=%v", superseded, err)
			}
		})
	}
}

// testCompletedTurnSupersessionRequiresNewGeneration 禁止仅凭新thread和launch认定旧投影已被取代。
func testCompletedTurnSupersessionRequiresNewGeneration(t *testing.T) {
	svc, _, agent := completedIdleStopFixture(t)
	old, err := svc.terminalOutcomes.GetPublicTerminalOutcome(context.Background(), agent.id)
	if err != nil {
		t.Fatal(err)
	}
	recoverDelayedTurnAgent(t, svc, agent, old.Identity.Generation+1)
	head, err := svc.terminalHeadReader.LoadTerminalOutcomeCurrentHead(context.Background(), agent.id)
	if err != nil {
		t.Fatal(err)
	}
	head.Generation = old.Identity.Generation
	if recoveredHeadSupersedesCompletedTurn(agent, head, old.Identity) {
		t.Fatal("non-increasing generation accepted")
	}
}
