package orchestration

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kelindar/event"
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/taskdag"
	terminaloutcomestore "github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/terminaloutcome"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
	agentdto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/agent"
	sharedto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/shared"
	threaddto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/thread"
	turndto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/turn"
	_ "modernc.org/sqlite"
)

const terminalSecretFixture = "provider-token=secret-value /private/agent/config.go"

func TestCommitTurnCompletedPersistsPublicSSOTBeforeRuntimeProjection(t *testing.T) {
	svc, db := newTerminalOutcomeTestService(t)
	agent := addTerminalOutcomeTestAgent(svc)
	agent.reportRequesters = []string{"requester-1"}
	ev := terminalOutcomeEvent(true)

	handled, err := svc.CommitTurnCompleted(context.Background(), ev)
	if err != nil || !handled {
		t.Fatalf("CommitTurnCompleted() = (%v, %v), want handled nil", handled, err)
	}
	if agent.state != agentdto.StateTurnRunning || agent.activeTurnID != "turn-1" {
		t.Fatalf("runtime changed before outbox projection = state:%q turn:%q", agent.state, agent.activeTurnID)
	}
	if agent.lastReport != "" || len(agent.reportRequesters) != 1 || agent.outcome != nil {
		t.Fatalf("runtime side effects before projection = report:%q requesters:%v outcome:%#v", agent.lastReport, agent.reportRequesters, agent.outcome)
	}
	if err := svc.ProcessTerminalOutcomeOutbox(context.Background(), "projector-after-commit", time.Minute, 10); err != nil {
		t.Fatalf("ProcessTerminalOutcomeOutbox() error = %v", err)
	}
	if agent.state != agentdto.StateIdle || agent.activeTurnID != "" ||
		agent.lastReport != "public success summary" || len(agent.reportRequesters) != 0 ||
		agent.outcome == nil || agent.outcome.Summary != "public success summary" {
		t.Fatalf("runtime after projection = state:%q turn:%q report:%q requesters:%v outcome:%#v",
			agent.state, agent.activeTurnID, agent.lastReport, agent.reportRequesters, agent.outcome)
	}
	assertTerminalOutcomeRowCounts(t, db, 1, 1)

	handled, err = svc.CommitTurnCompleted(context.Background(), ev)
	if err != nil || !handled {
		t.Fatalf("CommitTurnCompleted(replay) = (%v, %v), want handled nil", handled, err)
	}
	assertTerminalOutcomeRowCounts(t, db, 1, 1)
}

func TestOutboxProjectorRecoversCommitAfterRuntimeCrashWindow(t *testing.T) {
	svc, db := newTerminalOutcomeTestService(t)
	agent := addTerminalOutcomeTestAgent(svc)
	commit, err := terminalOutcomeCommitFromEvent(agent, terminalOutcomeEvent(true))
	if err != nil {
		t.Fatalf("terminalOutcomeCommitFromEvent() error = %v", err)
	}
	if _, err := svc.terminalOutcomes.CommitTerminalOutcome(context.Background(), commit); err != nil {
		t.Fatalf("CommitTerminalOutcome() error = %v", err)
	}
	if agent.state != agentdto.StateTurnRunning || agent.activeTurnID != "turn-1" {
		t.Fatalf("runtime changed before projector: state=%q turn=%q", agent.state, agent.activeTurnID)
	}

	if err := svc.ProcessTerminalOutcomeOutbox(context.Background(), "projector-restart", time.Minute, 10); err != nil {
		t.Fatalf("ProcessTerminalOutcomeOutbox() error = %v", err)
	}
	if agent.state != agentdto.StateIdle || agent.activeTurnID != "" || agent.lastReport != "public success summary" {
		t.Fatalf("runtime after replay = state:%q turn:%q report:%q", agent.state, agent.activeTurnID, agent.lastReport)
	}
	var status string
	if err := db.QueryRow("SELECT status FROM terminal_outcome_outbox_v2").Scan(&status); err != nil {
		t.Fatalf("query outbox status: %v", err)
	}
	if status != "projected" {
		t.Fatalf("outbox status = %q, want projected", status)
	}
}

func TestOutboxProjectorAdvancesDAGOnlyAfterDurableCommitAndReplaysOnce(t *testing.T) {
	svc, _ := newTerminalOutcomeTestService(t)
	agent := addTerminalOutcomeTestAgent(svc)
	lookup := &dagSubscriberLookupSpy{nodes: []taskdag.Node{{
		DagKey: "dag-terminal", NodeKey: "node-terminal", Status: "running",
	}}}
	flow := &dagSubscriberFlowSpy{}
	svc.terminalDAG = &DAGSubscriberDeps{LookupStore: lookup, FlowStore: flow, EventBus: svc.eventBus}

	ev := terminalOutcomeEvent(true)
	ev.Result = "private DAG artifact"
	commit, err := terminalOutcomeCommitFromEvent(agent, ev)
	if err != nil {
		t.Fatalf("terminalOutcomeCommitFromEvent() error = %v", err)
	}
	if _, err := svc.terminalOutcomes.CommitTerminalOutcome(context.Background(), commit); err != nil {
		t.Fatalf("CommitTerminalOutcome() error = %v", err)
	}
	if len(flow.completeCalls) != 0 {
		t.Fatalf("DAG changed before outbox projection: completeCalls=%d", len(flow.completeCalls))
	}

	if err := svc.ProcessTerminalOutcomeOutbox(context.Background(), "dag-projector", time.Minute, 10); err != nil {
		t.Fatalf("ProcessTerminalOutcomeOutbox() error = %v", err)
	}
	if len(flow.completeCalls) != 1 {
		t.Fatalf("DAG completeCalls = %d, want 1 after durable projection", len(flow.completeCalls))
	}
	if got := string(flow.completeCalls[0].Result); got != `{"text":"private DAG artifact"}` {
		t.Fatalf("DAG owner-scoped result = %s, want private artifact distinct from public summary", got)
	}
	if err := svc.ProcessTerminalOutcomeOutbox(context.Background(), "dag-projector-replay", time.Minute, 10); err != nil {
		t.Fatalf("ProcessTerminalOutcomeOutbox(replay) error = %v", err)
	}
	if len(flow.completeCalls) != 1 {
		t.Fatalf("DAG replay completeCalls = %d, want idempotent 1", len(flow.completeCalls))
	}
}

func TestOutboxDAGWriteAndRetryFailureDoesNotAckOrLosePrivateArtifact(t *testing.T) {
	svc, db := newTerminalOutcomeTestService(t)
	agent := addTerminalOutcomeTestAgent(svc)
	ev := terminalOutcomeEvent(true)
	ev.Result = "private artifact survives replay"
	commit, err := terminalOutcomeCommitFromEvent(agent, ev)
	if err != nil {
		t.Fatalf("terminalOutcomeCommitFromEvent() error = %v", err)
	}
	if _, err := svc.terminalOutcomes.CommitTerminalOutcome(context.Background(), commit); err != nil {
		t.Fatalf("CommitTerminalOutcome() error = %v", err)
	}
	runID := int64(1)
	lookup := &dagSubscriberLookupSpy{nodes: []taskdag.Node{{
		DagKey: "dag-failure", NodeKey: "node-failure", Status: "running", RunID: &runID,
	}}}
	flow := &dagSubscriberFlowSpy{
		completeErr: errors.New("complete unavailable"),
		enqueueErr:  errors.New("retry unavailable"),
	}
	svc.terminalDAG = &DAGSubscriberDeps{LookupStore: lookup, FlowStore: flow, EventBus: svc.eventBus}

	if err := svc.ProcessTerminalOutcomeOutbox(context.Background(), "dag-failure-projector", time.Minute, 10); err == nil {
		t.Fatal("ProcessTerminalOutcomeOutbox() error = nil, want DAG durability failure")
	}
	var status, privateResult string
	if err := db.QueryRow(`
		SELECT o.status, json_extract(d.payload_json, '$.result')
		FROM terminal_outcome_outbox_v2 o
		JOIN terminal_outcome_private_dag_payloads d ON d.id = o.private_dag_payload_id
	`).Scan(&status, &privateResult); err != nil {
		t.Fatalf("read retained private artifact: %v", err)
	}
	if status != "claimed" || privateResult != ev.Result {
		t.Fatalf("failed DAG projection = status:%q private:%q", status, privateResult)
	}
}

func TestOutboxProjectorHeartbeatPreventsConcurrentReclaimDuringSlowDAGProjection(t *testing.T) {
	svc, db := newTerminalOutcomeTestService(t)
	agent := addTerminalOutcomeTestAgent(svc)
	commit, err := terminalOutcomeCommitFromEvent(agent, terminalOutcomeEvent(true))
	if err != nil {
		t.Fatalf("terminalOutcomeCommitFromEvent() error = %v", err)
	}
	if _, err := svc.terminalOutcomes.CommitTerminalOutcome(context.Background(), commit); err != nil {
		t.Fatalf("CommitTerminalOutcome() error = %v", err)
	}
	lookup := &blockingTerminalOutcomeLookup{
		started: make(chan struct{}, 1),
		release: make(chan struct{}),
		nodes:   []taskdag.Node{{DagKey: "dag-heartbeat", NodeKey: "node-heartbeat", Status: "running"}},
	}
	svc.terminalDAG = &DAGSubscriberDeps{
		LookupStore: lookup,
		FlowStore:   &dagSubscriberFlowSpy{},
		EventBus:    svc.eventBus,
	}
	const lease = 500 * time.Millisecond
	projected := make(chan error, 1)
	go func() {
		projected <- svc.ProcessTerminalOutcomeOutbox(context.Background(), "worker-heartbeat-a", lease, 1)
	}()
	select {
	case <-lookup.started:
	case <-time.After(time.Second):
		t.Fatal("slow DAG projection did not start")
	}
	time.Sleep(2*lease + lease/2)
	reclaimed, err := svc.terminalOutcomes.ClaimTerminalOutcomeOutbox(context.Background(), "worker-heartbeat-b", lease, 1)
	if err != nil {
		t.Fatalf("worker B claim: %v", err)
	}
	if len(reclaimed) != 0 {
		t.Fatalf("worker B reclaimed live item = %#v", reclaimed)
	}
	close(lookup.release)
	if err := <-projected; err != nil {
		t.Fatalf("worker A projection: %v", err)
	}
	var status string
	if err := db.QueryRow("SELECT status FROM terminal_outcome_outbox_v2").Scan(&status); err != nil {
		t.Fatalf("read projected outbox: %v", err)
	}
	if status != "projected" {
		t.Fatalf("outbox status = %q, want projected", status)
	}
}

func TestOutboxColdReplayProjectsOwnerDAGWithEmptyRuntimeRegistry(t *testing.T) {
	writer, _ := newTerminalOutcomeTestService(t)
	agent := addTerminalOutcomeTestAgent(writer)
	ev := terminalOutcomeEvent(true)
	ev.Result = "cold owner artifact"
	commit, err := terminalOutcomeCommitFromEvent(agent, ev)
	if err != nil {
		t.Fatalf("terminalOutcomeCommitFromEvent() error = %v", err)
	}
	if _, err := writer.terminalOutcomes.CommitTerminalOutcome(context.Background(), commit); err != nil {
		t.Fatalf("CommitTerminalOutcome() error = %v", err)
	}

	cold := NewService(silentLogger(), event.NewDispatcher(), nil, nil, nil, nil)
	cold.terminalOutcomes = writer.terminalOutcomes
	lookup := &dagSubscriberLookupSpy{nodes: []taskdag.Node{{
		DagKey: "dag-cold", NodeKey: "node-cold", Status: "running",
	}}}
	flow := &dagSubscriberFlowSpy{}
	cold.terminalDAG = &DAGSubscriberDeps{LookupStore: lookup, FlowStore: flow, EventBus: cold.eventBus}
	if err := cold.ProcessTerminalOutcomeOutbox(context.Background(), "cold-projector", time.Minute, 10); err != nil {
		t.Fatalf("ProcessTerminalOutcomeOutbox() error = %v", err)
	}
	if len(flow.completeCalls) != 1 || string(flow.completeCalls[0].Result) != `{"text":"cold owner artifact"}` {
		t.Fatalf("cold DAG projection = %#v", flow.completeCalls)
	}
}

type blockingTerminalOutcomeLookup struct {
	started chan struct{}
	release chan struct{}
	nodes   []taskdag.Node
}

func (l *blockingTerminalOutcomeLookup) LookupNodesBySpawningThread(ctx context.Context, _ string) ([]taskdag.Node, error) {
	select {
	case l.started <- struct{}{}:
	default:
	}
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-l.release:
		return append([]taskdag.Node(nil), l.nodes...), nil
	}
}

func TestOutboxRuntimeFenceMismatchHasNoProjectionSideEffects(t *testing.T) {
	svc, db := newTerminalOutcomeTestService(t)
	agent := addTerminalOutcomeTestAgent(svc)
	ev := terminalOutcomeEvent(true)
	ev.Result = "must not project"
	commit, err := terminalOutcomeCommitFromEvent(agent, ev)
	if err != nil {
		t.Fatalf("terminalOutcomeCommitFromEvent() error = %v", err)
	}
	if _, err := svc.terminalOutcomes.CommitTerminalOutcome(context.Background(), commit); err != nil {
		t.Fatalf("CommitTerminalOutcome() error = %v", err)
	}
	lookup := &dagSubscriberLookupSpy{nodes: []taskdag.Node{{
		DagKey: "dag-stale", NodeKey: "node-stale", Status: "running",
	}}}
	flow := &dagSubscriberFlowSpy{}
	svc.terminalDAG = &DAGSubscriberDeps{LookupStore: lookup, FlowStore: flow, EventBus: svc.eventBus}
	agent.sessionGeneration++
	beforeState, beforeTurn, beforeReport := agent.state, agent.activeTurnID, agent.lastReport
	if err := svc.ProcessTerminalOutcomeOutbox(context.Background(), "stale-projector", time.Minute, 10); !errors.Is(err, errTerminalProjectionNotReady) {
		t.Fatalf("ProcessTerminalOutcomeOutbox() error = %v, want projection fence", err)
	}
	if agent.state != beforeState || agent.activeTurnID != beforeTurn || agent.lastReport != beforeReport ||
		len(flow.completeCalls) != 0 {
		t.Fatalf("stale projection side effects = state:%q turn:%q report:%q DAG:%d",
			agent.state, agent.activeTurnID, agent.lastReport, len(flow.completeCalls))
	}
	var status string
	if err := db.QueryRow("SELECT status FROM terminal_outcome_outbox_v2").Scan(&status); err != nil {
		t.Fatalf("read stale outbox status: %v", err)
	}
	if status != "claimed" {
		t.Fatalf("stale outbox status = %q, want claimed for lease retry", status)
	}
}

func TestTerminalProjectionReplayRequiresMatchingCanonicalFence(t *testing.T) {
	svc, _ := newTerminalOutcomeTestService(t)
	agent := addTerminalOutcomeTestAgent(svc)
	commit, err := terminalOutcomeCommitFromEvent(agent, terminalOutcomeEvent(true))
	if err != nil {
		t.Fatalf("terminalOutcomeCommitFromEvent() error = %v", err)
	}
	if err := svc.projectTerminalOutcomeLocked(context.Background(), agent, commit); err != nil {
		t.Fatalf("projectTerminalOutcomeLocked() error = %v", err)
	}
	if !terminalProjectionAlreadyApplied(agent, commit) {
		t.Fatal("matching projected terminal must be recognized as idempotent")
	}
	agent.sessionGeneration++
	if terminalProjectionAlreadyApplied(agent, commit) {
		t.Fatal("same public report with stale generation must not bypass the canonical fence")
	}
}

func TestCommitTurnCompletedFailureNeverPersistsRawProviderDetail(t *testing.T) {
	svc, db := newTerminalOutcomeTestService(t)
	addTerminalOutcomeTestAgent(svc)
	ev := terminalOutcomeEvent(false)
	ev.Error = terminalSecretFixture
	ev.Reason = terminalSecretFixture
	ev.Message = terminalSecretFixture

	if handled, err := svc.CommitTurnCompleted(context.Background(), ev); err != nil || !handled {
		t.Fatalf("CommitTurnCompleted() = (%v, %v), want handled nil", handled, err)
	}
	var durable string
	if err := db.QueryRow(`
		SELECT p.public_outcome_json || ' ' || p.public_report || ' ' || o.public_payload_json
		FROM public_terminal_outcome_history p
		JOIN terminal_outcome_outbox_v2 o ON o.event_id = p.event_id
	`).Scan(&durable); err != nil {
		t.Fatalf("query durable terminal outcome: %v", err)
	}
	if strings.Contains(durable, terminalSecretFixture) {
		t.Fatalf("durable terminal outcome leaked raw provider detail: %q", durable)
	}
}

func TestTerminalPublicSSOTOverridesRuntimeReportAndOutcomeReads(t *testing.T) {
	svc, _ := newTerminalOutcomeTestService(t)
	agent := addTerminalOutcomeTestAgent(svc)
	if handled, err := svc.CommitTurnCompleted(context.Background(), terminalOutcomeEvent(true)); err != nil || !handled {
		t.Fatalf("CommitTurnCompleted() = (%v, %v), want handled nil", handled, err)
	}
	agent.lastReport = terminalSecretFixture
	agent.outcome = &agentdto.Outcome{
		Kind: agentdto.OutcomeKindFailure, Code: "RAW", Reason: terminalSecretFixture,
		CompletedAt: time.Now().UTC(),
	}

	report, err := svc.GetReport(context.Background(), agent.id)
	if err != nil {
		t.Fatalf("GetReport() error = %v", err)
	}
	if report.Report != "public success summary" || strings.Contains(report.Report, terminalSecretFixture) {
		t.Fatalf("GetReport() = %#v, want durable public SSOT", report)
	}
	state, err := svc.GetState(context.Background(), agent.id)
	if err != nil || state.State != string(agentdto.StateIdle) {
		t.Fatalf("GetState() = %#v, %v; want durable terminal state", state, err)
	}
	snapshot, err := svc.Snapshot(context.Background(), agent.id)
	if err != nil {
		t.Fatalf("Snapshot() error = %v", err)
	}
	if snapshot.LastReport != "public success summary" || snapshot.Outcome == nil ||
		snapshot.Outcome.Kind != agentdto.OutcomeKindSuccess || strings.Contains(snapshot.LastReport, terminalSecretFixture) {
		t.Fatalf("Snapshot() = %#v, want durable public SSOT", snapshot)
	}
	list, err := svc.ListAgents(context.Background())
	if err != nil || len(list) != 1 || list[0].LastReport != "public success summary" ||
		list[0].Outcome == nil || list[0].Outcome.Kind != agentdto.OutcomeKindSuccess {
		t.Fatalf("ListAgents()/Board read = %#v, %v; want durable public SSOT", list, err)
	}
}

func TestLegacySuccessMigratesOnlyExplicitSummaryAndV2CapabilityAcceptsCanonicalResult(t *testing.T) {
	t.Run("legacy result is not public-safe", func(t *testing.T) {
		svc, db := newTerminalOutcomeTestService(t)
		addTerminalOutcomeTestAgent(svc)
		wire, _ := json.Marshal(terminalOutcomeEvent(true))
		var ev turndto.TurnCompleted
		if err := json.Unmarshal(wire, &ev); err != nil {
			t.Fatalf("decode legacy event: %v", err)
		}
		ev.Summary = ""
		ev.Result = terminalSecretFixture
		if handled, err := svc.CommitTurnCompleted(context.Background(), ev); err == nil || !handled {
			t.Fatalf("CommitTurnCompleted() = (%v, %v), want legacy public-safe rejection", handled, err)
		}
		assertTerminalOutcomeRowCounts(t, db, 0, 0)
	})

	t.Run("canonical v2 result is not public-safe", func(t *testing.T) {
		ev := terminalOutcomeEvent(true)
		ev.Summary = ""
		ev.Result = "canonical public result"
		if _, err := turndto.NewTurnTerminalV2(ev, "provider-event-v2"); err == nil {
			t.Fatal("NewTurnTerminalV2() error = nil, want missing trusted public summary")
		}
	})
}

func TestCommitTurnCompletedRejectsFenceMismatchWithZeroSideEffects(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*agentRuntime, *turndto.TurnCompleted)
	}{
		{name: "thread", mutate: func(_ *agentRuntime, ev *turndto.TurnCompleted) { ev.ThreadID = "wrong-thread" }},
		{name: "turn", mutate: func(_ *agentRuntime, ev *turndto.TurnCompleted) { ev.TurnID = "wrong-turn" }},
		{name: "session", mutate: func(agent *agentRuntime, _ *turndto.TurnCompleted) { agent.launchSeq = 0 }},
		{name: "generation", mutate: func(agent *agentRuntime, _ *turndto.TurnCompleted) { agent.sessionGeneration = 0 }},
		{name: "terminal state", mutate: func(agent *agentRuntime, _ *turndto.TurnCompleted) { agent.state = agentdto.StateFailed }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			svc, db := newTerminalOutcomeTestService(t)
			agent := addTerminalOutcomeTestAgent(svc)
			agent.lastReport = "before"
			agent.reportRequesters = []string{"requester-1"}
			ev := terminalOutcomeEvent(true)
			test.mutate(agent, &ev)

			if handled, err := svc.CommitTurnCompleted(context.Background(), ev); err == nil || !handled {
				t.Fatalf("CommitTurnCompleted() = (%v, %v), want handled error", handled, err)
			}
			if agent.activeTurnID != "turn-1" || agent.lastReport != "before" || len(agent.reportRequesters) != 1 {
				t.Fatalf("runtime changed after rejected fence: turn=%q report=%q requesters=%v", agent.activeTurnID, agent.lastReport, agent.reportRequesters)
			}
			assertTerminalOutcomeRowCounts(t, db, 0, 0)
		})
	}
}

func TestCommitTurnCompletedStoreFailureHasZeroSideEffects(t *testing.T) {
	svc, _ := newTerminalOutcomeTestService(t)
	agent := addTerminalOutcomeTestAgent(svc)
	svc.terminalOutcomes = failingTerminalOutcomePort{err: errors.New("commit failed")}
	agent.lastReport = "before"
	agent.reportRequesters = []string{"requester-1"}

	if handled, err := svc.CommitTurnCompleted(context.Background(), terminalOutcomeEvent(true)); err == nil || !handled {
		t.Fatalf("CommitTurnCompleted() = (%v, %v), want handled commit error", handled, err)
	}
	if agent.state != agentdto.StateTurnRunning || agent.activeTurnID != "turn-1" ||
		agent.lastReport != "before" || len(agent.reportRequesters) != 1 {
		t.Fatalf("runtime changed after store failure: %#v", agent)
	}
}

func TestProviderTurnHeadActivationFailureHasZeroRuntimeSideEffects(t *testing.T) {
	svc := NewService(silentLogger(), event.NewDispatcher(), nil, nil, nil, nil)
	failure := errors.New("activate failed")
	svc.terminalOutcomes = failingTerminalOutcomePort{err: failure}
	svc.turns.terminalOutcomes = svc.terminalOutcomes
	agent := svc.newAgentLocked("agent-1")
	agent.state = agentdto.StateTurnStarting
	agent.threadID, agent.remoteThreadID = "thread-1", "thread-1"
	agent.activeTurnID = "local-turn"
	agent.launchSeq, agent.sessionGeneration = 3, 7
	svc.registry.agents[agent.id] = agent
	work := turnWork{agentID: agent.id, threadID: agent.threadID, turnID: agent.activeTurnID}
	fence, err := svc.turns.beginProviderTurnStart(work)
	if err != nil {
		t.Fatalf("beginProviderTurnStart() error = %v", err)
	}
	pending := terminalOutcomeEvent(true)
	pending.TurnID = "provider-turn"
	agent.pendingProviderTerminal = &pending
	beforeState, beforeTurn := agent.state, agent.activeTurnID
	beforePendingID, beforePending := agent.pendingProviderTurnID, agent.pendingProviderTerminal
	beforeAlias, beforeVersion, beforeUpdatedAt := agent.providerTurnAlias, agent.terminalHeadVersion, agent.updatedAt

	if _, err := svc.turns.finishTurnStartSuccessLocked(context.Background(), agent, fence, "provider-turn"); !errors.Is(err, failure) {
		t.Fatalf("finishTurnStartSuccessLocked() error = %v, want activation failure", err)
	}
	if agent.state != beforeState || agent.activeTurnID != beforeTurn ||
		agent.pendingProviderTurnID != beforePendingID || agent.pendingProviderTerminal != beforePending ||
		agent.providerTurnAlias != beforeAlias || agent.terminalHeadVersion != beforeVersion ||
		!agent.updatedAt.Equal(beforeUpdatedAt) {
		t.Fatalf("activation failure mutated local runtime: %#v", agent)
	}
}

func TestRemoteTurnHeadActivationFailureHasZeroRuntimeSideEffects(t *testing.T) {
	svc := NewService(silentLogger(), event.NewDispatcher(), nil, nil, nil, nil)
	failure := errors.New("activate failed")
	svc.terminalOutcomes = failingTerminalOutcomePort{err: failure}
	svc.turns.terminalOutcomes = svc.terminalOutcomes
	agent := svc.newAgentLocked("agent-1")
	agent.state = agentdto.StateTurnRunning
	agent.threadID, agent.remoteThreadID = "thread-1", "thread-1"
	agent.activeTurnID = "local-turn"
	agent.launchSeq, agent.sessionGeneration = 3, 7
	svc.registry.agents[agent.id] = agent
	attempt := remoteTurnSubmitAttempt{
		agentID: agent.id, turnID: agent.activeTurnID, threadID: agent.remoteThreadID, launchSeq: agent.launchSeq,
	}
	ref := attempt.ref()
	svc.turns.pendingRemoteTurnSubmits = map[remoteTurnSubmitRef]pendingRemoteTurnSubmit{
		ref: {terminals: []turndto.TurnTerminalV2{{TurnID: "provider-turn"}}},
	}
	svc.turns.pendingRemoteTerminalCount = 1
	beforeState, beforeTurn := agent.state, agent.activeTurnID
	beforeVersion, beforeUpdatedAt := agent.terminalHeadVersion, agent.updatedAt

	if _, err := svc.turns.finishRemoteTurnSubmitSuccess(context.Background(), attempt, "provider-turn"); !errors.Is(err, failure) {
		t.Fatalf("finishRemoteTurnSubmitSuccess() error = %v, want activation failure", err)
	}
	if agent.state != beforeState || agent.activeTurnID != beforeTurn ||
		agent.terminalHeadVersion != beforeVersion || !agent.updatedAt.Equal(beforeUpdatedAt) {
		t.Fatalf("activation failure mutated remote runtime: %#v", agent)
	}
	if svc.turns.pendingRemoteTerminalCount != 1 || len(svc.turns.pendingRemoteTurnSubmits[ref].terminals) != 1 {
		t.Fatalf("activation failure consumed pending remote terminal: count=%d pending=%#v",
			svc.turns.pendingRemoteTerminalCount, svc.turns.pendingRemoteTurnSubmits[ref])
	}
}

func TestCommitStateChangedTerminalRequiresExactSessionAndProjectsFailure(t *testing.T) {
	svc, db := newTerminalOutcomeTestService(t)
	agent := addTerminalOutcomeTestAgent(svc)
	ev := agentdto.StateChanged{
		AgentSessionHeader: sharedto.AgentSessionHeader{
			AgentHeader: sharedto.AgentHeader{
				ThreadHeader: sharedto.ThreadHeader{
					EventHeader: sharedto.EventHeader{Timestamp: time.Date(2026, 7, 29, 2, 0, 0, 0, time.UTC)},
					ThreadID:    "thread-1",
				},
				AgentID: "agent-1",
			},
			SessionID: "3",
		},
		OldState: string(agentdto.StateTurnRunning),
		NewState: string(agentdto.StateFailed),
		Trigger:  terminalSecretFixture,
	}
	if handled, err := svc.CommitStateChangedTerminal(context.Background(), ev); err != nil || !handled {
		t.Fatalf("CommitStateChangedTerminal() = (%v, %v), want handled nil", handled, err)
	}
	if agent.state != agentdto.StateTurnRunning || agent.lastReport != "" {
		t.Fatalf("runtime changed before outbox projection = state:%q report:%q", agent.state, agent.lastReport)
	}
	if err := svc.ProcessTerminalOutcomeOutbox(context.Background(), "state-projector", time.Minute, 10); err != nil {
		t.Fatalf("ProcessTerminalOutcomeOutbox() error = %v", err)
	}
	if agent.state != agentdto.StateFailed || agent.activeTurnID != "" {
		t.Fatalf("projected state terminal = state:%q turn:%q", agent.state, agent.activeTurnID)
	}
	var durable string
	if err := db.QueryRow("SELECT public_outcome_json || ' ' || public_report FROM public_terminal_outcome_history").Scan(&durable); err != nil {
		t.Fatalf("query state terminal outcome: %v", err)
	}
	if strings.Contains(durable, terminalSecretFixture) {
		t.Fatalf("state terminal persisted raw trigger: %q", durable)
	}
}

func TestCommitStateChangedTerminalMissingSessionHasZeroSideEffects(t *testing.T) {
	svc, db := newTerminalOutcomeTestService(t)
	agent := addTerminalOutcomeTestAgent(svc)
	ev := agentdto.StateChanged{
		AgentSessionHeader: sharedto.AgentSessionHeader{
			AgentHeader: sharedto.AgentHeader{
				ThreadHeader: sharedto.ThreadHeader{
					EventHeader: sharedto.EventHeader{Timestamp: time.Date(2026, 7, 29, 2, 0, 0, 0, time.UTC)},
					ThreadID:    "thread-1",
				},
				AgentID: "agent-1",
			},
		},
		NewState: string(agentdto.StateFailed),
	}
	if handled, err := svc.CommitStateChangedTerminal(context.Background(), ev); err == nil || !handled {
		t.Fatalf("CommitStateChangedTerminal() = (%v, %v), want missing-session error", handled, err)
	}
	if agent.state != agentdto.StateTurnRunning || agent.activeTurnID != "turn-1" {
		t.Fatalf("runtime changed after missing session: state=%q turn=%q", agent.state, agent.activeTurnID)
	}
	assertTerminalOutcomeRowCounts(t, db, 0, 0)
}

func TestCommitStateChangedTerminalProjectsWithoutActiveTurn(t *testing.T) {
	svc, db := newTerminalOutcomeTestService(t)
	agent := svc.newAgentLocked("agent-1")
	agent.state = agentdto.StateIdle
	agent.threadID, agent.remoteThreadID = "thread-1", "thread-1"
	agent.launchSeq, agent.sessionGeneration = 3, 7
	svc.registry.agents[agent.id] = agent
	head, err := svc.terminalOutcomes.ActivateTerminalOutcomeHead(context.Background(), contract.TerminalOutcomeHeadActivation{
		Capability: contract.TerminalOutcomeCapabilityV2, AgentID: agent.id,
		PublicThreadID: "thread-1", ProviderTurnID: "session-terminal:3",
		SessionID: "3", Generation: 7, ExpectedActiveState: string(agent.state),
		ActivatedAt: time.Date(2026, 7, 29, 2, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("activate session head: %v", err)
	}
	agent.terminalHeadVersion = head.Version
	ev := agentdto.StateChanged{
		AgentSessionHeader: sharedto.AgentSessionHeader{
			AgentHeader: sharedto.AgentHeader{
				ThreadHeader: sharedto.ThreadHeader{
					EventHeader: sharedto.EventHeader{Timestamp: time.Date(2026, 7, 29, 2, 1, 0, 0, time.UTC)},
					ThreadID:    "thread-1",
				},
				AgentID: "agent-1",
			},
			SessionID: "3",
		},
		OldState: string(agentdto.StateIdle),
		NewState: string(agentdto.StateFailed),
	}
	if handled, err := svc.CommitStateChangedTerminal(context.Background(), ev); err != nil || !handled {
		t.Fatalf("CommitStateChangedTerminal() = (%v, %v), want handled nil", handled, err)
	}
	if agent.state != agentdto.StateIdle || agent.lastReport != "" {
		t.Fatalf("runtime changed before projection = state:%q report:%q", agent.state, agent.lastReport)
	}
	if err := svc.ProcessTerminalOutcomeOutbox(context.Background(), "session-state-projector", time.Minute, 10); err != nil {
		t.Fatalf("ProcessTerminalOutcomeOutbox() error = %v", err)
	}
	if agent.state != agentdto.StateFailed || agent.lastReport == "" {
		t.Fatalf("runtime after projection = state:%q report:%q", agent.state, agent.lastReport)
	}
	assertTerminalOutcomeRowCounts(t, db, 1, 1)
}

func TestLegacyThreadStoppedWithoutSessionGenerationFailsFast(t *testing.T) {
	svc, db := newTerminalOutcomeTestService(t)
	agent := addTerminalOutcomeTestAgent(svc)
	ev := threaddto.Stopped{
		EventHeader: sharedto.EventHeader{Timestamp: time.Date(2026, 7, 29, 2, 2, 0, 0, time.UTC)},
		ThreadID:    "thread-1",
		AgentID:     "agent-1",
		Status:      "stopped",
		Reason:      terminalSecretFixture,
	}
	if handled, err := svc.CommitThreadStoppedTerminal(context.Background(), ev); err == nil || !handled {
		t.Fatalf("CommitThreadStoppedTerminal() = (%v, %v), want handled fail-fast", handled, err)
	}
	if agent.state != agentdto.StateTurnRunning || agent.activeTurnID != "turn-1" {
		t.Fatalf("legacy stopped mutated runtime = state:%q turn:%q", agent.state, agent.activeTurnID)
	}
	assertTerminalOutcomeRowCounts(t, db, 0, 0)
}

func TestProcessExitCommitFailureHasZeroSideEffects(t *testing.T) {
	svc, _ := newTerminalOutcomeTestService(t)
	agent := addTerminalOutcomeTestAgent(svc)
	svc.terminalOutcomes = failingTerminalOutcomePort{err: errors.New("commit failed")}
	agent.lastReport = "before"
	agent.reportRequesters = []string{"requester-1"}

	svc.handleProcessExit(context.Background(), agent.id, agent.launchSeq, errors.New(terminalSecretFixture))

	if agent.lastExitedSeq != 0 || agent.exitedAt != nil || agent.state != agentdto.StateTurnRunning ||
		agent.activeTurnID != "turn-1" || agent.lastReport != "before" || len(agent.reportRequesters) != 1 {
		t.Fatalf("process exit mutated runtime after terminal commit failure: %#v", agent)
	}
}

func TestProcessExitPersistsSafeTerminalBeforeCleanup(t *testing.T) {
	svc, db := newTerminalOutcomeTestService(t)
	agent := addTerminalOutcomeTestAgent(svc)

	svc.handleProcessExit(context.Background(), agent.id, agent.launchSeq, errors.New(terminalSecretFixture))

	if agent.lastExitedSeq != 3 || agent.state != agentdto.StateTurnRunning || agent.activeTurnID != "turn-1" ||
		agent.lastReport != "" || agent.outcome != nil {
		t.Fatalf("process exit runtime = seq:%d state:%q turn:%q", agent.lastExitedSeq, agent.state, agent.activeTurnID)
	}
	var durable string
	if err := db.QueryRow(`
		SELECT p.public_outcome_json || ' ' || p.public_report || ' ' || o.public_payload_json
		FROM public_terminal_outcome_history p
		JOIN terminal_outcome_outbox_v2 o ON o.event_id = p.event_id
	`).Scan(&durable); err != nil {
		t.Fatalf("query process terminal outcome: %v", err)
	}
	if strings.Contains(durable, terminalSecretFixture) {
		t.Fatalf("process terminal persisted raw exit error: %q", durable)
	}
}

func TestHandleReportEventTerminalUsesCanonicalCommitAndRejectsOuterAgentMismatch(t *testing.T) {
	svc, db := newTerminalOutcomeTestService(t)
	addTerminalOutcomeTestAgent(svc)
	ev := terminalOutcomeEvent(true)
	raw, err := json.Marshal(ev)
	if err != nil {
		t.Fatalf("marshal terminal report event: %v", err)
	}
	_, err = svc.HandleReportEvent(context.Background(), ReportEvent{
		AgentID: "wrong-agent", EventType: "turn/completed", EventData: raw,
	})
	if err == nil {
		t.Fatal("HandleReportEvent() error = nil, want outer agent mismatch")
	}
	assertTerminalOutcomeRowCounts(t, db, 0, 0)
}

func TestLegacyRuntimeLossReportWithoutSessionGenerationFailsFast(t *testing.T) {
	svc, db := newTerminalOutcomeTestService(t)
	agent := addTerminalOutcomeTestAgent(svc)
	_, err := svc.HandleReportEvent(context.Background(), ReportEvent{
		AgentID: "agent-1", EventType: "connection.dead",
		Report: terminalSecretFixture, EventData: json.RawMessage(`{"raw":"` + terminalSecretFixture + `"}`),
	})
	if err == nil {
		t.Fatal("HandleReportEvent() error = nil, want legacy identity rejection")
	}
	if agent.state != agentdto.StateTurnRunning || agent.activeTurnID != "turn-1" {
		t.Fatalf("runtime-loss mutated state=%q turn=%q", agent.state, agent.activeTurnID)
	}
	assertTerminalOutcomeRowCounts(t, db, 0, 0)
}

func TestRemoteLauncherTerminalUsesCanonicalCommitPort(t *testing.T) {
	svc, db := newTerminalOutcomeTestService(t)
	agent := addTerminalOutcomeTestAgent(svc)

	svc.handleRemoteTurnCompleted(context.Background(), terminalOutcomeEvent(true))

	if err := svc.ProcessTerminalOutcomeOutbox(context.Background(), "remote-projector", time.Minute, 10); err != nil {
		t.Fatalf("ProcessTerminalOutcomeOutbox() error = %v", err)
	}
	if agent.state != agentdto.StateIdle || agent.activeTurnID != "" {
		t.Fatalf("remote terminal runtime = state:%q turn:%q", agent.state, agent.activeTurnID)
	}
	assertTerminalOutcomeRowCounts(t, db, 1, 1)
}

type failingTerminalOutcomePort struct {
	err error
}

func (p failingTerminalOutcomePort) ActivateTerminalOutcomeHead(context.Context, contract.TerminalOutcomeHeadActivation) (contract.TerminalOutcomeHead, error) {
	return contract.TerminalOutcomeHead{}, p.err
}

func (p failingTerminalOutcomePort) CommitTerminalOutcome(context.Context, contract.TerminalOutcomeCommit) (contract.TerminalOutcomeCommitResult, error) {
	return contract.TerminalOutcomeCommitResult{}, p.err
}

func (failingTerminalOutcomePort) GetPublicTerminalOutcome(context.Context, string) (contract.TerminalOutcomeCommit, error) {
	return contract.TerminalOutcomeCommit{}, sql.ErrNoRows
}

func (failingTerminalOutcomePort) ClaimTerminalOutcomeOutbox(context.Context, string, time.Duration, int) ([]contract.TerminalOutcomeOutboxItem, error) {
	return nil, nil
}

func (failingTerminalOutcomePort) RenewTerminalOutcomeOutbox(context.Context, int64, string, string, time.Duration) (time.Time, error) {
	return time.Time{}, nil
}

func (failingTerminalOutcomePort) MarkTerminalOutcomeProjected(context.Context, int64, string, string) error {
	return nil
}

func newTerminalOutcomeTestService(t *testing.T) (*service, *sql.DB) {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+strings.ReplaceAll(t.Name(), "/", "_")+"?mode=memory&cache=shared")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Errorf("close sqlite: %v", err)
		}
	})
	for _, name := range []string{"120_terminal_outcome_outbox.sql", "121_terminal_outcome_current_head.sql"} {
		migrationPath := filepath.Join("..", "..", "..", "internal", "platform", "db", "sqlite", "migrations", name)
		migration, err := os.ReadFile(migrationPath)
		if err != nil {
			t.Fatalf("read terminal outcome migration %s: %v", name, err)
		}
		if _, err := db.Exec(string(migration)); err != nil {
			t.Fatalf("apply terminal outcome migration %s: %v", name, err)
		}
	}
	svc := NewService(silentLogger(), event.NewDispatcher(), nil, nil, nil, nil)
	svc.terminalOutcomes = terminaloutcomestore.New(db)
	svc.turns.terminalOutcomes = svc.terminalOutcomes
	return svc, db
}

func addTerminalOutcomeTestAgent(svc *service) *agentRuntime {
	agent := svc.newAgentLocked("agent-1")
	agent.state = agentdto.StateTurnRunning
	agent.threadID = "thread-1"
	agent.remoteThreadID = "thread-1"
	agent.activeTurnID = "turn-1"
	agent.launchSeq = 3
	agent.sessionGeneration = 7
	svc.registry.agents[agent.id] = agent
	head, err := svc.terminalOutcomes.ActivateTerminalOutcomeHead(context.Background(), contract.TerminalOutcomeHeadActivation{
		Capability: contract.TerminalOutcomeCapabilityV2, AgentID: agent.id,
		PublicThreadID: agent.remoteThreadID, ProviderTurnID: agent.activeTurnID,
		SessionID: agentSessionID(agent), Generation: agent.sessionGeneration,
		ExpectedActiveState: string(agent.state), ActivatedAt: time.Date(2026, 7, 29, 1, 2, 2, 0, time.UTC),
	})
	if err != nil {
		panic(err)
	}
	agent.terminalHeadVersion = head.Version
	return agent
}

func terminalOutcomeEvent(success bool) turndto.TurnCompleted {
	ev := turndto.TurnCompleted{
		TurnHeader: sharedto.TurnHeader{
			AgentHeader: sharedto.AgentHeader{
				ThreadHeader: sharedto.ThreadHeader{
					EventHeader: sharedto.EventHeader{Timestamp: time.Date(2026, 7, 29, 1, 2, 3, 0, time.UTC)},
					ThreadID:    "thread-1",
				},
				AgentID: "agent-1",
			},
			TurnIDHeader: sharedto.TurnIDHeader{TurnID: "turn-1"},
		},
		Success: success, Status: map[bool]string{true: "completed", false: "failed"}[success],
		Summary: "public success summary",
	}
	terminal, err := turndto.NewTurnTerminalV2(ev, "terminal:event-1")
	if err != nil {
		panic(err)
	}
	ev, err = turndto.AttachCanonicalTurnTerminal(ev, terminal)
	if err != nil {
		panic(err)
	}
	return ev
}

func assertTerminalOutcomeRowCounts(t *testing.T, db *sql.DB, wantOutcomes, wantOutbox int) {
	t.Helper()
	for table, want := range map[string]int{"public_terminal_outcome_history": wantOutcomes, "terminal_outcome_outbox_v2": wantOutbox} {
		var got int
		if err := db.QueryRow("SELECT COUNT(*) FROM " + table).Scan(&got); err != nil {
			t.Fatalf("count %s: %v", table, err)
		}
		if got != want {
			t.Fatalf("%s row count = %d, want %d", table, got, want)
		}
	}
}
