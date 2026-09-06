package orchestration

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/kelindar/event"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
	agentdto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/agent"
	turndto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/turn"
)

func TestProcessExitStopUsesPreStopHeadFence(t *testing.T) {
	svc, db := newTerminalOutcomeTestService(t)
	agent := addTerminalOutcomeTestAgent(svc)
	agent.state = agentdto.StateStopping
	agent.stopRequested = true

	if committed, err := svc.CommitProcessExitTerminalLocked(context.Background(), agent, agent.launchSeq, nil); err != nil || !committed {
		t.Fatalf("CommitProcessExitTerminalLocked() = (%v, %v), want committed nil", committed, err)
	}
	if agent.state != agentdto.StateStopping || agent.activeTurnID != "turn-1" {
		t.Fatalf("runtime changed before stop projection = state:%q turn:%q", agent.state, agent.activeTurnID)
	}
	if err := svc.ProcessTerminalOutcomeOutbox(context.Background(), "stop-projector", time.Minute, 10); err != nil {
		t.Fatalf("ProcessTerminalOutcomeOutbox() error = %v", err)
	}
	if agent.state != agentdto.StateStopped || agent.activeTurnID != "" {
		t.Fatalf("runtime after stop projection = state:%q turn:%q, want stopped and cleared", agent.state, agent.activeTurnID)
	}
	assertTerminalOutcomeRowCounts(t, db, 1, 1)
}

func TestHandleProcessExitDefersSessionCleanupUntilTerminalProjectionACK(t *testing.T) {
	svc, db := newTerminalOutcomeTestService(t)
	cleaner := &stopTestSessionCleaner{}
	svc.lifecycle.sessionCleaner = cleaner
	agent := addTerminalOutcomeTestAgent(svc)
	agent.state = agentdto.StateStopping
	agent.stopRequested = true

	svc.handleProcessExit(context.Background(), agent.id, agent.launchSeq, nil)
	assertProcessExitRetainsSession(t, agent, cleaner)
	assertProcessExitOutboxStatus(t, db, "pending", "before projection")

	if err := svc.ProcessTerminalOutcomeOutbox(context.Background(), "process-exit-projector", time.Minute, 10); err != nil {
		t.Fatalf("ProcessTerminalOutcomeOutbox() error = %v", err)
	}
	assertProcessExitProjected(t, agent, cleaner)
	assertProcessExitOutboxStatus(t, db, "projected", "after projection")
}

// assertProcessExitRetainsSession 确认 terminal commit 后 session 在 ACK 前仍被保留。
func assertProcessExitRetainsSession(t *testing.T, agent *agentRuntime, cleaner *stopTestSessionCleaner) {
	t.Helper()
	if agent.sessionGeneration != 7 {
		t.Fatalf("sessionGeneration after process exit = %d, want retained generation 7 until ACK", agent.sessionGeneration)
	}
	if len(cleaner.removeGeneration) != 0 {
		t.Fatalf("session cleanup before terminal projection ACK = %#v, want none", cleaner.removeGeneration)
	}
}

// assertProcessExitProjected 确认 terminal projection ACK 后才清理旧 session。
func assertProcessExitProjected(t *testing.T, agent *agentRuntime, cleaner *stopTestSessionCleaner) {
	t.Helper()
	if agent.state != agentdto.StateStopped || agent.activeTurnID != "" {
		t.Fatalf("runtime after terminal projection = state:%q turn:%q, want stopped and cleared", agent.state, agent.activeTurnID)
	}
	if agent.sessionGeneration != 0 {
		t.Fatalf("sessionGeneration after terminal projection ACK = %d, want 0", agent.sessionGeneration)
	}
	if len(cleaner.removeGeneration) != 1 || cleaner.removeGeneration[0] != 7 {
		t.Fatalf("session cleanup after terminal projection ACK = %#v, want [7]", cleaner.removeGeneration)
	}
}

// assertTerminalOutboxStatus 读取并比较 outbox 单行状态，避免测试主体堆叠分支。
func assertProcessExitOutboxStatus(t *testing.T, db *sql.DB, want, phase string) {
	t.Helper()
	var status string
	if err := db.QueryRow("SELECT status FROM terminal_outcome_outbox_v2").Scan(&status); err != nil {
		t.Fatalf("read %s terminal outbox status: %v", phase, err)
	}
	if status != want {
		t.Fatalf("terminal outbox status %s = %q, want %q", phase, status, want)
	}
}

func TestHandleProcessExitWithNoopSessionCleanerRetainsGenerationUntilACK(t *testing.T) {
	svc, _ := newTerminalOutcomeTestService(t)
	svc.lifecycle.sessionCleaner = terminalOutcomeNoopSessionCleaner{}
	agent := addTerminalOutcomeTestAgent(svc)
	agent.state = agentdto.StateStopping
	agent.stopRequested = true

	svc.handleProcessExit(context.Background(), agent.id, agent.launchSeq, nil)
	if agent.sessionGeneration != 7 {
		t.Fatalf("sessionGeneration with noop cleaner before ACK = %d, want 7", agent.sessionGeneration)
	}
	if err := svc.ProcessTerminalOutcomeOutbox(context.Background(), "noop-process-exit-projector", time.Minute, 10); err != nil {
		t.Fatalf("ProcessTerminalOutcomeOutbox() error = %v", err)
	}
	if agent.sessionGeneration != 0 {
		t.Fatalf("sessionGeneration with noop cleaner after ACK = %d, want 0", agent.sessionGeneration)
	}
}

func TestSupersededProcessExitOutboxACKDoesNotTouchNewGeneration(t *testing.T) {
	svc, db := newTerminalOutcomeTestService(t)
	cleaner := &stopTestSessionCleaner{}
	svc.lifecycle.sessionCleaner = cleaner
	agent := addTerminalOutcomeTestAgent(svc)
	agent.state = agentdto.StateStopping
	agent.stopRequested = true

	svc.handleProcessExit(context.Background(), agent.id, agent.launchSeq, nil)
	assertProcessExitRetainsSession(t, agent, cleaner)
	advanceProcessExitGeneration(t, svc, agent)

	if err := svc.ProcessTerminalOutcomeOutbox(context.Background(), "superseded-process-exit-projector", time.Minute, 10); err != nil {
		t.Fatalf("ProcessTerminalOutcomeOutbox(superseded) error = %v", err)
	}
	assertSupersededProcessExit(t, agent, cleaner)
	assertProcessExitOutboxStatus(t, db, "projected", "superseded")
}

// advanceProcessExitGeneration 模拟新启动并绑定新的 terminal head 代际。
func advanceProcessExitGeneration(t *testing.T, svc *service, agent *agentRuntime) {
	t.Helper()
	agent.state = agentdto.StateIdle
	agent.activeTurnID = ""
	agent.stopRequested = false
	agent.launchSeq = 8
	agent.threadID, agent.remoteThreadID = "thread-2", "thread-2"
	if err := svc.BindSessionGeneration(context.Background(), agent.id, 8); err != nil {
		t.Fatalf("BindSessionGeneration(new generation) error = %v", err)
	}
	if agent.sessionGeneration != 8 {
		t.Fatalf("new sessionGeneration = %d, want 8", agent.sessionGeneration)
	}
}

// assertSupersededProcessExit 确认旧终态只 ACK/清理旧代际，不改变新 runtime。
func assertSupersededProcessExit(t *testing.T, agent *agentRuntime, cleaner *stopTestSessionCleaner) {
	t.Helper()
	if agent.state != agentdto.StateIdle || agent.activeTurnID != "" || agent.sessionGeneration != 8 {
		t.Fatalf("new runtime changed by old projection = state:%q turn:%q generation:%d", agent.state, agent.activeTurnID, agent.sessionGeneration)
	}
	if len(cleaner.removeGeneration) != 1 || cleaner.removeGeneration[0] != 7 {
		t.Fatalf("old session cleanup = %#v, want [7]", cleaner.removeGeneration)
	}
}

func TestSupersededProcessExitOutboxACKAfterRuntimeRestart(t *testing.T) {
	writer, db := newTerminalOutcomeTestService(t)
	writerCleaner := &stopTestSessionCleaner{}
	writer.lifecycle.sessionCleaner = writerCleaner
	agent := addTerminalOutcomeTestAgent(writer)
	agent.state = agentdto.StateStopping
	agent.stopRequested = true
	writer.handleProcessExit(context.Background(), agent.id, agent.launchSeq, nil)
	assertProcessExitRetainsSession(t, agent, writerCleaner)

	restarted := NewService(silentLogger(), event.NewDispatcher(), nil, nil, nil, nil)
	restarted.terminalOutcomes = writer.terminalOutcomes
	restarted.terminalHeadReader = writer.terminalHeadReader
	restarted.turns.terminalOutcomes = restarted.terminalOutcomes
	restartedCleaner := &stopTestSessionCleaner{}
	restarted.lifecycle.sessionCleaner = restartedCleaner
	restartedAgent := restarted.newAgentLocked(agent.id)
	restartedAgent.state = agentdto.StateIdle
	restartedAgent.threadID, restartedAgent.remoteThreadID = "thread-1", "thread-1"
	restartedAgent.launchSeq = 1
	restartedAgent.sessionGeneration = 1
	restarted.registry.agents[restartedAgent.id] = restartedAgent

	if err := restarted.ProcessTerminalOutcomeOutbox(context.Background(), "restart-process-exit-projector", time.Minute, 10); err != nil {
		t.Fatalf("ProcessTerminalOutcomeOutbox(restart) error = %v", err)
	}
	assertRestartedSupersededProcessExit(t, restartedAgent, restartedCleaner)
	assertProcessExitOutboxStatus(t, db, "projected", "restarted superseded")
}

// assertRestartedSupersededProcessExit 确认重启后的旧 outbox 不触碰新 runtime。
func assertRestartedSupersededProcessExit(t *testing.T, agent *agentRuntime, cleaner *stopTestSessionCleaner) {
	t.Helper()
	if agent.state != agentdto.StateIdle || agent.activeTurnID != "" || agent.sessionGeneration != 1 {
		t.Fatalf("restarted runtime changed by old projection = state:%q turn:%q generation:%d", agent.state, agent.activeTurnID, agent.sessionGeneration)
	}
	if len(cleaner.removeGeneration) != 1 || cleaner.removeGeneration[0] != 7 {
		t.Fatalf("restarted old session cleanup = %#v, want [7]", cleaner.removeGeneration)
	}
}

func TestSameLifecycleGenerationMismatchRetainsProcessExitClaim(t *testing.T) {
	svc, db := newTerminalOutcomeTestService(t)
	cleaner := &stopTestSessionCleaner{}
	svc.lifecycle.sessionCleaner = cleaner
	agent := addTerminalOutcomeTestAgent(svc)
	agent.state = agentdto.StateStopping
	agent.stopRequested = true
	svc.handleProcessExit(context.Background(), agent.id, agent.launchSeq, nil)

	// A generation mutation without a new launch sequence is not enough to
	// prove supersession; the strict projection fence must keep the claim.
	agent.sessionGeneration = 8
	err := svc.ProcessTerminalOutcomeOutbox(context.Background(), "same-seq-stale-projector", time.Minute, 10)
	if !errors.Is(err, errTerminalProjectionNotReady) {
		t.Fatalf("ProcessTerminalOutcomeOutbox(same seq mismatch) error = %v, want projection fence", err)
	}
	if agent.sessionGeneration != 8 || len(cleaner.removeGeneration) != 0 {
		t.Fatalf("same-seq mismatch changed runtime/cleaner = generation:%d cleanup:%#v", agent.sessionGeneration, cleaner.removeGeneration)
	}
	var status string
	if err := db.QueryRow("SELECT status FROM terminal_outcome_outbox_v2").Scan(&status); err != nil {
		t.Fatalf("read same-seq stale outbox status: %v", err)
	}
	if status != "claimed" {
		t.Fatalf("same-seq stale outbox status = %q, want claimed", status)
	}
}

func TestProcessExitSessionCleanupWaitsForSuccessfulACK(t *testing.T) {
	svc, db := newTerminalOutcomeTestService(t)
	cleaner := &stopTestSessionCleaner{}
	svc.lifecycle.sessionCleaner = cleaner
	agent := addTerminalOutcomeTestAgent(svc)
	agent.state = agentdto.StateStopping
	agent.stopRequested = true
	svc.handleProcessExit(context.Background(), agent.id, agent.launchSeq, nil)

	base := svc.terminalOutcomes
	svc.terminalOutcomes = terminalOutcomeAckFailurePort{
		TerminalOutcomeCommitPort: base,
		err:                       errors.New("ack unavailable"),
	}
	if err := svc.ProcessTerminalOutcomeOutbox(context.Background(), "ack-failure-projector", time.Minute, 10); err == nil {
		t.Fatal("ProcessTerminalOutcomeOutbox(ack failure) error = nil, want ACK error")
	}
	assertProcessExitRetainsSession(t, agent, cleaner)
	assertProcessExitOutboxStatus(t, db, "claimed", "failed ACK")

	if _, err := db.Exec("UPDATE terminal_outcome_outbox_v2 SET lease_expires_at = 0"); err != nil {
		t.Fatalf("expire failed-ACK claim: %v", err)
	}
	svc.terminalOutcomes = base
	if err := svc.ProcessTerminalOutcomeOutbox(context.Background(), "ack-retry-projector", time.Minute, 10); err != nil {
		t.Fatalf("ProcessTerminalOutcomeOutbox(ACK retry) error = %v", err)
	}
	assertProcessExitProjected(t, agent, cleaner)
	assertProcessExitOutboxStatus(t, db, "projected", "successful ACK retry")
}

type terminalOutcomeAckFailurePort struct {
	contract.TerminalOutcomeCommitPort
	err error
}

func (p terminalOutcomeAckFailurePort) MarkTerminalOutcomeProjected(context.Context, int64, string, string) error {
	return p.err
}

type terminalOutcomeNoopSessionCleaner struct{}

func (terminalOutcomeNoopSessionCleaner) RemoveSession(sessionID string) { _ = sessionID }

func (terminalOutcomeNoopSessionCleaner) RemoveSessionGeneration(sessionID string, generation uint64) {
	_, _ = sessionID, generation
}

func TestCLIProcessExitUsesProviderSessionFenceAndExactlyOnce(t *testing.T) {
	svc := NewService(silentLogger(), nil, nil, nil, nil, nil)
	agent := svc.newAgentLocked("cli-agent")
	agent.state = agentdto.StateTurnRunning
	agent.threadID, agent.remoteThreadID = "provider-session", "provider-session"
	agent.launchSeq = 7
	svc.registry.agents[agent.id] = agent

	event := contract.CLIProcessExit{
		AgentID: agent.id, SessionID: "provider-session", LaunchSeq: agent.launchSeq,
		Err: errors.New("provider crashed"), Unexpected: true,
	}
	svc.handleCLIProcessExit(event)
	if agent.lastExitedSeq != agent.launchSeq {
		t.Fatalf("lastExitedSeq = %d, want %d", agent.lastExitedSeq, agent.launchSeq)
	}
	stateAfterFirst, errorAfterFirst := agent.state, agent.lastError
	svc.handleCLIProcessExit(event)
	if agent.lastExitedSeq != agent.launchSeq || agent.state != stateAfterFirst || agent.lastError != errorAfterFirst {
		t.Fatalf("duplicate CLI exit changed runtime: seq=%d state=%q error=%q", agent.lastExitedSeq, agent.state, agent.lastError)
	}
}

func TestCLIProcessExitRejectsStaleProviderSession(t *testing.T) {
	svc := NewService(silentLogger(), nil, nil, nil, nil, nil)
	agent := svc.newAgentLocked("cli-agent")
	agent.state = agentdto.StateTurnRunning
	agent.threadID, agent.remoteThreadID = "current-session", "current-session"
	agent.launchSeq = 7
	svc.registry.agents[agent.id] = agent

	svc.handleCLIProcessExit(contract.CLIProcessExit{
		AgentID: agent.id, SessionID: "stale-session", LaunchSeq: agent.launchSeq,
		Err: errors.New("provider crashed"), Unexpected: true,
	})
	if agent.lastExitedSeq != 0 || agent.state != agentdto.StateTurnRunning {
		t.Fatalf("stale provider session changed runtime: seq=%d state=%q", agent.lastExitedSeq, agent.state)
	}
}

func TestCLIProcessExitExplicitCloseIsIgnoredByService(t *testing.T) {
	svc := NewService(silentLogger(), nil, nil, nil, nil, nil)
	agent := svc.newAgentLocked("cli-agent")
	agent.state = agentdto.StateTurnRunning
	agent.threadID, agent.remoteThreadID = "provider-session", "provider-session"
	agent.launchSeq = 7
	svc.registry.agents[agent.id] = agent

	svc.handleCLIProcessExit(contract.CLIProcessExit{
		AgentID: agent.id, SessionID: "provider-session", LaunchSeq: agent.launchSeq,
		Err: errors.New("provider closed"), Unexpected: false,
	})
	if agent.lastExitedSeq != 0 || agent.state != agentdto.StateTurnRunning {
		t.Fatalf("explicit close changed runtime: seq=%d state=%q", agent.lastExitedSeq, agent.state)
	}
}

func TestManagedClaudeCLIProcessExitNeverFallsBackToLocalCommand(t *testing.T) {
	t.Parallel()

	backend := &managedCLIRecoveryBackendSpy{}
	launcher := &cliLauncher{backend: backend}
	svc := NewService(silentLogger(), nil, launcher, nil, nil, nil)
	local := &processExitLocalRecoverySpy{}
	svc.lifecycle.recovery.local = local
	agent := svc.newAgentLocked("claude-agent")
	agent.state = agentdto.StateTurnRunning
	agent.threadID, agent.remoteThreadID = "claude-session", "claude-session"
	agent.provider, agent.providerSource = "claude", "inferred"
	agent.command = []string{"/legacy/local-command", "must-not-run"}
	agent.launchSeq = 11
	svc.registry.agents[agent.id] = agent

	svc.handleCLIProcessExit(contract.CLIProcessExit{
		AgentID: agent.id, SessionID: agent.remoteThreadID, LaunchSeq: agent.launchSeq,
		Err: errors.New("Claude CLI exited unexpectedly"), Unexpected: true,
	})

	if local.calls != 0 {
		t.Fatalf("local recovery calls = %d, want 0 for managed Claude", local.calls)
	}
	if backend.launchCalls != 0 || backend.stopCalls != 0 {
		t.Fatalf("managed Claude recovery calls = launch:%d stop:%d, want no unsupported recovery", backend.launchCalls, backend.stopCalls)
	}
	if agent.lastExitedSeq != 11 || agent.state != agentdto.StateFailed || agent.lastReport == "" {
		t.Fatalf("managed Claude process exit = seq:%d state:%q report:%q, want failed terminal state and safe fallback report", agent.lastExitedSeq, agent.state, agent.lastReport)
	}
}

type processExitLocalRecoverySpy struct{ calls int }

func (s *processExitLocalRecoverySpy) startProcessLocked(context.Context, *agentRuntime) error {
	s.calls++
	return errors.New("local recovery must not run")
}

type managedCLIRecoveryBackendSpy struct {
	launchCalls int
	stopCalls   int
	bound       bool
}

func (s *managedCLIRecoveryBackendSpy) Launch(context.Context, LaunchRequest) (LaunchResult, error) {
	s.launchCalls++
	return LaunchResult{}, errors.New("managed recovery must not run")
}

func (*managedCLIRecoveryBackendSpy) Fork(context.Context, string, LaunchRequest) (LaunchResult, error) {
	return LaunchResult{}, errors.New("fork must not run")
}

func (s *managedCLIRecoveryBackendSpy) Stop(context.Context, string) error {
	s.stopCalls++
	return nil
}

func (s *managedCLIRecoveryBackendSpy) Archive(context.Context, string) error { return nil }

func (*managedCLIRecoveryBackendSpy) Interrupt(context.Context, string, string) error { return nil }

func (*managedCLIRecoveryBackendSpy) SubmitTurn(context.Context, TurnSubmission) (string, error) {
	return "", errors.New("submit must not run")
}

func (*managedCLIRecoveryBackendSpy) IsRunning(context.Context, string) bool { return false }

func (s *managedCLIRecoveryBackendSpy) BindTerminalSink(func(context.Context, turndto.TurnTerminalV2)) {
	s.bound = true
}
