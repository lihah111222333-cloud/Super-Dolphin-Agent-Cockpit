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
	if agent.state != agentdto.StateIdle || agent.activeTurnID != "" {
		t.Fatalf("runtime = state:%q turn:%q, want idle and cleared turn", agent.state, agent.activeTurnID)
	}
	if agent.lastReport != "public success summary" || len(agent.reportRequesters) != 0 {
		t.Fatalf("projection = report:%q requesters:%v", agent.lastReport, agent.reportRequesters)
	}
	if agent.outcome == nil || agent.outcome.Summary != "public success summary" {
		t.Fatalf("agent.outcome = %#v, want public success", agent.outcome)
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
	if err := db.QueryRow("SELECT status FROM terminal_outcome_outbox").Scan(&status); err != nil {
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

	commit, err := terminalOutcomeCommitFromEvent(agent, terminalOutcomeEvent(true))
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
	if got := string(flow.completeCalls[0].Result); got != `{"text":"public success summary"}` {
		t.Fatalf("DAG public result = %s, want safe durable summary", got)
	}
	if err := svc.ProcessTerminalOutcomeOutbox(context.Background(), "dag-projector-replay", time.Minute, 10); err != nil {
		t.Fatalf("ProcessTerminalOutcomeOutbox(replay) error = %v", err)
	}
	if len(flow.completeCalls) != 1 {
		t.Fatalf("DAG replay completeCalls = %d, want idempotent 1", len(flow.completeCalls))
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
		SELECT p.public_outcome_json || ' ' || p.public_report || ' ' || o.payload_json
		FROM public_terminal_outcomes p
		JOIN terminal_outcome_outbox o ON o.event_id = p.event_id
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
	snapshot, err := svc.Snapshot(context.Background(), agent.id)
	if err != nil {
		t.Fatalf("Snapshot() error = %v", err)
	}
	if snapshot.LastReport != "public success summary" || snapshot.Outcome == nil ||
		snapshot.Outcome.Kind != agentdto.OutcomeKindSuccess || strings.Contains(snapshot.LastReport, terminalSecretFixture) {
		t.Fatalf("Snapshot() = %#v, want durable public SSOT", snapshot)
	}
}

func TestLegacySuccessMigratesOnlyExplicitSummaryAndV2CapabilityAcceptsCanonicalResult(t *testing.T) {
	t.Run("legacy result is not public-safe", func(t *testing.T) {
		svc, db := newTerminalOutcomeTestService(t)
		addTerminalOutcomeTestAgent(svc)
		ev := terminalOutcomeEvent(true)
		ev.Summary = ""
		ev.Result = terminalSecretFixture
		if handled, err := svc.CommitTurnCompleted(context.Background(), ev); err == nil || !handled {
			t.Fatalf("CommitTurnCompleted() = (%v, %v), want legacy public-safe rejection", handled, err)
		}
		assertTerminalOutcomeRowCounts(t, db, 0, 0)
	})

	t.Run("canonical v2 result is explicitly public-safe", func(t *testing.T) {
		svc, db := newTerminalOutcomeTestService(t)
		addTerminalOutcomeTestAgent(svc)
		ev := terminalOutcomeEvent(true)
		ev.Summary = ""
		ev.Result = "canonical public result"
		terminal, err := turndto.NewTurnTerminalV2(ev, "provider-event-v2")
		if err != nil {
			t.Fatalf("NewTurnTerminalV2() error = %v", err)
		}
		ev, err = turndto.AttachCanonicalTurnTerminal(ev, terminal)
		if err != nil {
			t.Fatalf("AttachCanonicalTurnTerminal() error = %v", err)
		}
		if handled, err := svc.CommitTurnCompleted(context.Background(), ev); err != nil || !handled {
			t.Fatalf("CommitTurnCompleted() = (%v, %v), want canonical v2 success", handled, err)
		}
		var capability, report string
		if err := db.QueryRow(`
			SELECT h.capability, p.public_report
			FROM terminal_outcome_heads h
			JOIN public_terminal_outcomes p USING (agent_id)
		`).Scan(&capability, &report); err != nil {
			t.Fatalf("query canonical v2 outcome: %v", err)
		}
		if capability != contract.TerminalOutcomeCapabilityV2 || report != "canonical public result" {
			t.Fatalf("canonical v2 durable result = capability:%q report:%q", capability, report)
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
	svc := NewService(silentLogger(), event.NewDispatcher(), nil, nil, nil, nil)
	svc.terminalOutcomes = failingTerminalOutcomePort{err: errors.New("commit failed")}
	agent := addTerminalOutcomeTestAgent(svc)
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
	if agent.state != agentdto.StateFailed || agent.activeTurnID != "" {
		t.Fatalf("projected state terminal = state:%q turn:%q", agent.state, agent.activeTurnID)
	}
	var durable string
	if err := db.QueryRow("SELECT public_outcome_json || ' ' || public_report FROM public_terminal_outcomes").Scan(&durable); err != nil {
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
	agent := addTerminalOutcomeTestAgent(svc)
	agent.activeTurnID = ""
	agent.state = agentdto.StateIdle
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
	if agent.state != agentdto.StateFailed || agent.lastReport == "" {
		t.Fatalf("runtime = state:%q report:%q, want projected failure", agent.state, agent.lastReport)
	}
	assertTerminalOutcomeRowCounts(t, db, 1, 1)
}

func TestLegacyThreadStoppedAdapterUsesExplicitRuntimeFenceAndDropsRawReason(t *testing.T) {
	svc, db := newTerminalOutcomeTestService(t)
	agent := addTerminalOutcomeTestAgent(svc)
	ev := threaddto.Stopped{
		EventHeader: sharedto.EventHeader{Timestamp: time.Date(2026, 7, 29, 2, 2, 0, 0, time.UTC)},
		ThreadID:    "thread-1",
		AgentID:     "agent-1",
		Status:      "stopped",
		Reason:      terminalSecretFixture,
	}
	if handled, err := svc.CommitThreadStoppedTerminal(context.Background(), ev); err != nil || !handled {
		t.Fatalf("CommitThreadStoppedTerminal() = (%v, %v), want handled nil", handled, err)
	}
	if agent.state != agentdto.StateStopped || agent.activeTurnID != "" {
		t.Fatalf("legacy stopped projection = state:%q turn:%q", agent.state, agent.activeTurnID)
	}
	var sessionID, durable string
	if err := db.QueryRow(`
		SELECT p.session_id, p.public_outcome_json || ' ' || p.public_report || ' ' || o.payload_json
		FROM public_terminal_outcomes p
		JOIN terminal_outcome_outbox o ON o.event_id = p.event_id
	`).Scan(&sessionID, &durable); err != nil {
		t.Fatalf("query legacy stopped outcome: %v", err)
	}
	if sessionID != "3" || strings.Contains(durable, terminalSecretFixture) {
		t.Fatalf("legacy adapter = session:%q durable:%q", sessionID, durable)
	}
}

func TestProcessExitCommitFailureHasZeroSideEffects(t *testing.T) {
	svc := NewService(silentLogger(), event.NewDispatcher(), nil, nil, nil, nil)
	svc.terminalOutcomes = failingTerminalOutcomePort{err: errors.New("commit failed")}
	agent := addTerminalOutcomeTestAgent(svc)
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

	if agent.lastExitedSeq != 3 || agent.state != agentdto.StateFailed || agent.activeTurnID != "" {
		t.Fatalf("process exit runtime = seq:%d state:%q turn:%q", agent.lastExitedSeq, agent.state, agent.activeTurnID)
	}
	var durable string
	if err := db.QueryRow(`
		SELECT p.public_outcome_json || ' ' || p.public_report || ' ' || o.payload_json
		FROM public_terminal_outcomes p
		JOIN terminal_outcome_outbox o ON o.event_id = p.event_id
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

func TestLegacyRuntimeLossReportUsesCurrentFenceAndNeverPersistsRawPayload(t *testing.T) {
	svc, db := newTerminalOutcomeTestService(t)
	agent := addTerminalOutcomeTestAgent(svc)
	result, err := svc.HandleReportEvent(context.Background(), ReportEvent{
		AgentID: "agent-1", EventType: "connection.dead",
		Report: terminalSecretFixture, EventData: json.RawMessage(`{"raw":"` + terminalSecretFixture + `"}`),
	})
	if err != nil {
		t.Fatalf("HandleReportEvent() error = %v", err)
	}
	if !result.Success || agent.state != agentdto.StateFailed || agent.activeTurnID != "" {
		t.Fatalf("runtime-loss result=%#v state=%q turn=%q", result, agent.state, agent.activeTurnID)
	}
	var sessionID, durable string
	if err := db.QueryRow(`
		SELECT p.session_id, p.public_outcome_json || ' ' || p.public_report || ' ' || o.payload_json
		FROM public_terminal_outcomes p
		JOIN terminal_outcome_outbox o ON o.event_id = p.event_id
	`).Scan(&sessionID, &durable); err != nil {
		t.Fatalf("query runtime-loss terminal outcome: %v", err)
	}
	if sessionID != "3" || strings.Contains(durable, terminalSecretFixture) {
		t.Fatalf("runtime-loss adapter = session:%q durable:%q", sessionID, durable)
	}
}

func TestRemoteLauncherTerminalUsesCanonicalCommitPort(t *testing.T) {
	svc, db := newTerminalOutcomeTestService(t)
	agent := addTerminalOutcomeTestAgent(svc)

	svc.handleRemoteTurnCompleted(context.Background(), terminalOutcomeEvent(true))

	if agent.state != agentdto.StateIdle || agent.activeTurnID != "" {
		t.Fatalf("remote terminal runtime = state:%q turn:%q", agent.state, agent.activeTurnID)
	}
	assertTerminalOutcomeRowCounts(t, db, 1, 1)
}

type failingTerminalOutcomePort struct {
	err error
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

func (failingTerminalOutcomePort) MarkTerminalOutcomeProjected(context.Context, int64, string) error {
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
	migrationPath := filepath.Join("..", "..", "..", "internal", "platform", "db", "sqlite", "migrations", "120_terminal_outcome_outbox.sql")
	migration, err := os.ReadFile(migrationPath)
	if err != nil {
		t.Fatalf("read terminal outcome migration: %v", err)
	}
	if _, err := db.Exec(string(migration)); err != nil {
		t.Fatalf("apply terminal outcome migration: %v", err)
	}
	svc := NewService(silentLogger(), event.NewDispatcher(), nil, nil, nil, nil)
	svc.terminalOutcomes = terminaloutcomestore.New(db)
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
	return agent
}

func terminalOutcomeEvent(success bool) turndto.TurnCompleted {
	return turndto.TurnCompleted{
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
}

func assertTerminalOutcomeRowCounts(t *testing.T, db *sql.DB, wantOutcomes, wantOutbox int) {
	t.Helper()
	for table, want := range map[string]int{"public_terminal_outcomes": wantOutcomes, "terminal_outcome_outbox": wantOutbox} {
		var got int
		if err := db.QueryRow("SELECT COUNT(*) FROM " + table).Scan(&got); err != nil {
			t.Fatalf("count %s: %v", table, err)
		}
		if got != want {
			t.Fatalf("%s row count = %d, want %d", table, got, want)
		}
	}
}
