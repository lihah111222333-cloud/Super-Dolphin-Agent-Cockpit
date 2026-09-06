package orchestration

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/taskdag"
	agentdto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/agent"
)

func TestCorruptTerminalOutboxIsQuarantinedBeforeRuntimeOrDAGProjection(t *testing.T) {
	svc, db, agent, flow := newCorruptTerminalProjectionFixture(t)
	beforeState, beforeTurn, beforeReport := agent.state, agent.activeTurnID, agent.lastReport
	if err := svc.ProcessTerminalOutcomeOutbox(context.Background(), "corruption-projector", time.Minute, 1); err != nil {
		t.Fatalf("ProcessTerminalOutcomeOutbox() error = %v", err)
	}
	assertCorruptTerminalProjectionHasNoEffects(t, db, agent, flow, beforeState, beforeTurn, beforeReport)
}

func newCorruptTerminalProjectionFixture(t *testing.T) (*service, *sql.DB, *agentRuntime, *dagSubscriberFlowSpy) {
	t.Helper()
	svc, db := newTerminalOutcomeTestService(t)
	agent := addTerminalOutcomeTestAgent(svc)
	event := terminalOutcomeEvent(true)
	event.Result = "must not project"
	commit, err := terminalOutcomeCommitFromEvent(agent, event)
	if err != nil {
		t.Fatalf("terminalOutcomeCommitFromEvent() error = %v", err)
	}
	if _, err := svc.terminalOutcomes.CommitTerminalOutcome(context.Background(), commit); err != nil {
		t.Fatalf("CommitTerminalOutcome() error = %v", err)
	}
	if _, err := db.Exec("UPDATE public_terminal_outcome_history SET public_thread_id = 'foreign-thread' WHERE terminal_identity = ?", commit.Identity.TerminalIdentity); err != nil {
		t.Fatalf("corrupt terminal history: %v", err)
	}
	lookup := &dagSubscriberLookupSpy{nodes: []taskdag.Node{{DagKey: "dag-corrupt", NodeKey: "node-corrupt", Status: "running"}}}
	flow := &dagSubscriberFlowSpy{}
	svc.terminalDAG = &DAGSubscriberDeps{LookupStore: lookup, FlowStore: flow, EventBus: svc.eventBus}
	return svc, db, agent, flow
}

func assertCorruptTerminalProjectionHasNoEffects(t *testing.T, db *sql.DB, agent *agentRuntime, flow *dagSubscriberFlowSpy, beforeState agentdto.AgentState, beforeTurn, beforeReport string) {
	t.Helper()
	if agent.state != beforeState || agent.activeTurnID != beforeTurn || agent.lastReport != beforeReport || agent.outcome != nil {
		t.Fatalf("corrupt terminal projection changed runtime: state=%q turn=%q report=%q outcome=%#v", agent.state, agent.activeTurnID, agent.lastReport, agent.outcome)
	}
	if len(flow.completeCalls) != 0 {
		t.Fatalf("corrupt terminal projection changed DAG: completeCalls=%d", len(flow.completeCalls))
	}
	var status string
	if err := db.QueryRow("SELECT status FROM terminal_outcome_outbox_v2").Scan(&status); err != nil {
		t.Fatalf("read corrupt outbox status: %v", err)
	}
	if status != "poisoned" {
		t.Fatalf("corrupt terminal outbox status = %q, want poisoned", status)
	}
}
