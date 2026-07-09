package orchestration

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/store/taskdag"
	agentdto "github.com/anthropic-ai/super-agent-v3/internal/dto/agent"
	platformdb "github.com/anthropic-ai/super-agent-v3/internal/platform/db"
)

func newTerminateDAGTestService(runStore taskdag.RunStore, threads AgentThreadLookup) *service {
	return attachDAGTestController(&service{registry: newAgentRegistry()}, dagControllerParams{
		RunStore:     runStore,
		AgentThreads: threads,
	})
}

func TestTerminateDAG_CancelsRunningRun(t *testing.T) {
	runStore := &stubRunStore{
		getRunReply: &taskdag.Run{
			ID:     77,
			RunKey: "dag-1#run-1",
			DagKey: "dag-1",
			Status: "running",
		},
	}
	svc := newTerminateDAGTestService(runStore, nil)

	err := svc.TerminateDAG(context.Background(), TerminateDAGRequest{
		DagKey: " dag-1 ",
		RunKey: " dag-1#run-1 ",
		Reason: " user_requested ",
	})
	if err != nil {
		t.Fatalf("TerminateDAG() error = %v, want nil", err)
	}
	if runStore.getRunCalls[0] != "dag-1#run-1" {
		t.Fatalf("GetRun call = %v, want dag-1#run-1", runStore.getRunCalls)
	}
	if len(runStore.terminateRunCalls) != 1 {
		t.Fatalf("TerminateRun calls = %d, want 1", len(runStore.terminateRunCalls))
	}
	got := runStore.terminateRunCalls[0]
	if got.DagKey != "dag-1" || got.RunKey != "dag-1#run-1" || got.RunID != 77 || got.Reason != "user_requested" {
		t.Fatalf("TerminateRun input = %+v, want trimmed dag/run/reason and run_id=77", got)
	}
}

func TestTerminateDAG_TerminalRunIsIdempotent(t *testing.T) {
	runStore := &stubRunStore{
		getRunReply: &taskdag.Run{
			ID:     77,
			RunKey: "dag-1#run-1",
			DagKey: "dag-1",
			Status: "cancelled",
		},
	}
	svc := newTerminateDAGTestService(runStore, nil)

	err := svc.TerminateDAG(context.Background(), TerminateDAGRequest{DagKey: "dag-1", RunKey: "dag-1#run-1"})
	if err != nil {
		t.Fatalf("TerminateDAG() terminal run error = %v, want nil", err)
	}
	if len(runStore.terminateRunCalls) != 1 {
		t.Fatalf("TerminateRun calls = %d, want 1 so cancelled run can retry spawned-agent stops", len(runStore.terminateRunCalls))
	}
}

func TestTerminateDAG_ConcurrentTerminalRunIsIdempotent(t *testing.T) {
	runStore := &stubRunStore{
		getRunReply:     &taskdag.Run{ID: 77, RunKey: "dag-1#run-1", DagKey: "dag-1", Status: "running"},
		terminateRunErr: platformdb.ErrNotFound,
	}
	svc := newTerminateDAGTestService(runStore, nil)

	err := svc.TerminateDAG(context.Background(), TerminateDAGRequest{DagKey: "dag-1", RunKey: "dag-1#run-1"})
	if err != nil {
		t.Fatalf("TerminateDAG() concurrent terminal error = %v, want nil", err)
	}
	if len(runStore.getRunCalls) != 2 {
		t.Fatalf("GetRun calls = %d, want initial plus idempotency recheck", len(runStore.getRunCalls))
	}
}

func TestTerminateDAG_RejectsDagKeyMismatch(t *testing.T) {
	runStore := &stubRunStore{
		getRunReply: &taskdag.Run{
			ID:     77,
			RunKey: "dag-1#run-1",
			DagKey: "dag-1",
			Status: "running",
		},
	}
	svc := newTerminateDAGTestService(runStore, nil)

	err := svc.TerminateDAG(context.Background(), TerminateDAGRequest{DagKey: "other", RunKey: "dag-1#run-1"})
	if err == nil || !strings.Contains(err.Error(), "does not belong") {
		t.Fatalf("TerminateDAG() error = %v, want dag mismatch", err)
	}
}

func TestTerminateDAG_PropagatesTerminateStoreFailure(t *testing.T) {
	boom := errors.New("cancel write failed")
	runStore := &stubRunStore{
		getRunReply: &taskdag.Run{
			ID:     77,
			RunKey: "dag-1#run-1",
			DagKey: "dag-1",
			Status: "running",
		},
		terminateRunErr: boom,
	}
	svc := newTerminateDAGTestService(runStore, nil)

	err := svc.TerminateDAG(context.Background(), TerminateDAGRequest{DagKey: "dag-1", RunKey: "dag-1#run-1"})
	if !errors.Is(err, boom) {
		t.Fatalf("TerminateDAG() error = %v, want wrapped boom", err)
	}
}

func TestTerminateDAG_CancelsRunBeforeStoppingSpawnedAgents(t *testing.T) {
	runStore := &stubRunStore{
		getRunReply: &taskdag.Run{
			ID:     77,
			RunKey: "dag-1#run-1",
			DagKey: "dag-1",
			Status: "running",
		},
		terminateRunResult: taskdag.TerminateRunResult{SpawnedThreadIDs: []string{"thr-running", "thr-ready"}},
	}
	launcher := &terminateLauncherSpy{runStore: runStore}
	svc := NewService(silentLogger(), nil, launcher, nil, nil, nil)
	svc.agentThreads = fakeAgentThreadStore{threads: []PersistedThread{
		{ThreadID: "thr-running", AgentID: "agent-running"},
		{ThreadID: "thr-ready", AgentID: "agent-ready"},
		{ThreadID: "thr-done", AgentID: "agent-done"},
	}}
	attachDAGTestController(svc, dagControllerParams{RunStore: runStore, AgentThreads: svc.agentThreads})
	agent := svc.newAgentLocked("agent-running")
	agent.state = agentdto.StateIdle
	agent.remoteThreadID = "thr-running"
	agent.launchSeq = 3
	svc.registry.agents[agent.id] = agent
	readyAgent := svc.newAgentLocked("agent-ready")
	readyAgent.state = agentdto.StateIdle
	readyAgent.remoteThreadID = "thr-ready"
	readyAgent.launchSeq = 3
	svc.registry.agents[readyAgent.id] = readyAgent

	err := svc.TerminateDAG(context.Background(), TerminateDAGRequest{
		DagKey: "dag-1",
		RunKey: "dag-1#run-1",
		Reason: "user_requested",
	})
	if err != nil {
		t.Fatalf("TerminateDAG() error = %v, want nil", err)
	}
	if len(runStore.listRunNodesCalls) != 0 {
		t.Fatalf("ListRunNodes calls = %d, want 0 because TerminateRun returns transaction-captured threads", len(runStore.listRunNodesCalls))
	}
	if got := strings.Join(launcher.stopCalls, ","); got != "agent-running,agent-ready" {
		t.Fatalf("launcher stop calls = %v, want [agent-running agent-ready]", launcher.stopCalls)
	}
	if len(runStore.terminateRunCalls) != 1 {
		t.Fatalf("TerminateRun calls = %d, want 1", len(runStore.terminateRunCalls))
	}
	if got := strings.Join(runStore.callOrder, ","); got != "terminate:dag-1,stop:agent-running,stop:agent-ready" {
		t.Fatalf("call order = %s, want terminate before spawned-agent stops", got)
	}
}

func TestTerminateDAG_ReturnsStopFailureAfterCancellingRun(t *testing.T) {
	stopErr := errors.New("remote stop failed")
	runStore := &stubRunStore{
		getRunReply: &taskdag.Run{
			ID:     77,
			RunKey: "dag-1#run-1",
			DagKey: "dag-1",
			Status: "running",
		},
		terminateRunResult: taskdag.TerminateRunResult{SpawnedThreadIDs: []string{"thr-running"}},
	}
	launcher := &terminateLauncherSpy{stopErr: stopErr, runStore: runStore}
	svc := NewService(silentLogger(), nil, launcher, nil, nil, nil)
	svc.agentThreads = fakeAgentThreadStore{threads: []PersistedThread{{ThreadID: "thr-running", AgentID: "agent-running"}}}
	attachDAGTestController(svc, dagControllerParams{RunStore: runStore, AgentThreads: svc.agentThreads})
	agent := svc.newAgentLocked("agent-running")
	agent.state = agentdto.StateIdle
	agent.remoteThreadID = "thr-running"
	agent.launchSeq = 3
	svc.registry.agents[agent.id] = agent

	err := svc.TerminateDAG(context.Background(), TerminateDAGRequest{
		DagKey: "dag-1",
		RunKey: "dag-1#run-1",
		Reason: "user_requested",
	})
	if !errors.Is(err, stopErr) {
		t.Fatalf("TerminateDAG() error = %v, want wrapped stop failure", err)
	}
	if got := strings.Join(runStore.callOrder, ","); got != "terminate:dag-1,stop:agent-running" {
		t.Fatalf("call order = %s, want cancel committed before stop failure is returned", got)
	}
}

func TestTerminateDAG_RetriesSpawnedAgentStopAfterCancelledRun(t *testing.T) {
	stopErr := errors.New("remote stop failed")
	threadID := "thr-running"
	runStore := &stubRunStore{
		getRunReply: &taskdag.Run{
			ID:     77,
			RunKey: "dag-1#run-1",
			DagKey: "dag-1",
			Status: "running",
		},
		terminateRunResult: taskdag.TerminateRunResult{SpawnedThreadIDs: []string{threadID}},
		listRunNodesReply: []taskdag.Node{
			{DagKey: "dag-1", NodeKey: "n1", RunID: int64Ptr(77), SpawningThreadID: &threadID},
		},
	}
	launcher := &terminateLauncherSpy{stopErr: stopErr, runStore: runStore}
	svc := NewService(silentLogger(), nil, launcher, nil, nil, nil)
	svc.agentThreads = fakeAgentThreadStore{threads: []PersistedThread{{ThreadID: threadID, AgentID: "agent-running"}}}
	attachDAGTestController(svc, dagControllerParams{RunStore: runStore, AgentThreads: svc.agentThreads})
	agent := svc.newAgentLocked("agent-running")
	agent.state = agentdto.StateIdle
	agent.remoteThreadID = threadID
	agent.launchSeq = 3
	svc.registry.agents[agent.id] = agent

	err := svc.TerminateDAG(context.Background(), TerminateDAGRequest{
		DagKey: "dag-1",
		RunKey: "dag-1#run-1",
		Reason: "user_requested",
	})
	if !errors.Is(err, stopErr) {
		t.Fatalf("first TerminateDAG() error = %v, want wrapped stop failure", err)
	}

	runStore.getRunReply.Status = "cancelled"
	err = svc.TerminateDAG(context.Background(), TerminateDAGRequest{
		DagKey: "dag-1",
		RunKey: "dag-1#run-1",
		Reason: "user_requested",
	})
	if err != nil && !errors.Is(err, stopErr) {
		t.Fatalf("retry TerminateDAG() error = %v, want nil or wrapped stop failure", err)
	}
	if len(runStore.terminateRunCalls) != 2 {
		t.Fatalf("TerminateRun calls = %d, want retry to ask store for cancelled-run thread IDs", len(runStore.terminateRunCalls))
	}
	if got := strings.Join(launcher.stopCalls, ","); got != "agent-running,agent-running" {
		t.Fatalf("launcher stop calls = %v, want retry to stop spawned agent again", launcher.stopCalls)
	}
}

func TestTerminateDAG_ReturnsSpawnedThreadLookupMissAfterCancellingRun(t *testing.T) {
	runStore := &stubRunStore{
		getRunReply: &taskdag.Run{
			ID:     77,
			RunKey: "dag-1#run-1",
			DagKey: "dag-1",
			Status: "running",
		},
		terminateRunResult: taskdag.TerminateRunResult{SpawnedThreadIDs: []string{"thr-missing"}},
	}
	svc := newTerminateDAGTestService(runStore, fakeAgentThreadStore{})

	err := svc.TerminateDAG(context.Background(), TerminateDAGRequest{DagKey: "dag-1", RunKey: "dag-1#run-1"})
	if err == nil || !strings.Contains(err.Error(), "skipped_no_thread_id") {
		t.Fatalf("TerminateDAG() error = %v, want spawned thread lookup miss", err)
	}
	if len(runStore.terminateRunCalls) != 1 {
		t.Fatalf("TerminateRun calls = %d, want 1 before reporting stop miss", len(runStore.terminateRunCalls))
	}
}

func TestTerminateDAG_ReturnsSpawnedBindingMissingAfterCancellingRun(t *testing.T) {
	runStore := &stubRunStore{
		getRunReply: &taskdag.Run{
			ID:     77,
			RunKey: "dag-1#run-1",
			DagKey: "dag-1",
			Status: "running",
		},
		terminateRunResult: taskdag.TerminateRunResult{SpawnedThreadIDs: []string{"thr-no-binding"}},
	}
	svc := newTerminateDAGTestService(runStore, fakeAgentThreadStore{threads: []PersistedThread{{ThreadID: "thr-no-binding"}}})

	err := svc.TerminateDAG(context.Background(), TerminateDAGRequest{DagKey: "dag-1", RunKey: "dag-1#run-1"})
	if err == nil || !strings.Contains(err.Error(), "skipped_binding_missing") {
		t.Fatalf("TerminateDAG() error = %v, want spawned binding missing", err)
	}
	if len(runStore.terminateRunCalls) != 1 {
		t.Fatalf("TerminateRun calls = %d, want 1 before reporting binding miss", len(runStore.terminateRunCalls))
	}
}

type terminateLauncherSpy struct {
	stopCalls []string
	stopErr   error
	runStore  *stubRunStore
}

func (l *terminateLauncherSpy) Launch(context.Context, *agentRuntime, LaunchRequest) (LaunchResult, error) {
	return LaunchResult{}, nil
}

func (l *terminateLauncherSpy) Fork(context.Context, *agentRuntime, *agentRuntime, LaunchRequest) (LaunchResult, error) {
	return LaunchResult{}, errors.New("fork should not be called")
}

func (l *terminateLauncherSpy) Stop(_ context.Context, agent *agentRuntime) error {
	if agent != nil {
		l.stopCalls = append(l.stopCalls, agent.id)
		if l.runStore != nil {
			l.runStore.callOrder = append(l.runStore.callOrder, "stop:"+agent.id)
		}
	}
	return l.stopErr
}

func (l *terminateLauncherSpy) Archive(context.Context, *agentRuntime) error { return nil }

func (l *terminateLauncherSpy) Interrupt(context.Context, *agentRuntime, string) error {
	return nil
}

func (l *terminateLauncherSpy) SubmitTurn(context.Context, *agentRuntime, TurnSubmission) (string, error) {
	return "", nil
}

func (l *terminateLauncherSpy) IsRunning(_ context.Context, agent *agentRuntime) bool {
	return agent != nil && strings.TrimSpace(agent.remoteThreadID) != ""
}
