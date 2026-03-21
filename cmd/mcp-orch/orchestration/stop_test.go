package orchestration

import (
	"context"
	"os/exec"
	"testing"
	"time"

	"github.com/kelindar/event"

	agentdto "github.com/anthropic-ai/super-agent-v3/internal/dto/agent"
)

type stopTestSessionCleaner struct {
	removeCurrentCalls int
	removeGeneration   []uint64
}

func (c *stopTestSessionCleaner) RemoveSession(string) {
	c.removeCurrentCalls++
}

func (c *stopTestSessionCleaner) RemoveSessionGeneration(_ string, generation uint64) {
	c.removeGeneration = append(c.removeGeneration, generation)
}

func TestStopAgentPublishesStoppedAfterObservedExit(t *testing.T) {
	t.Parallel()

	dispatcher := event.NewDispatcher()
	stopped := make(chan agentdto.AgentStopped, 1)
	cancel := event.Subscribe(dispatcher, func(ev agentdto.AgentStopped) {
		stopped <- ev
	})
	defer cancel()

	cleaner := &stopTestSessionCleaner{}
	svc := NewService(silentLogger(), dispatcher, cleaner, nil, nil)
	cmd := exec.Command("sh", "-c", "sleep 30")
	if err := cmd.Start(); err != nil {
		t.Fatalf("cmd.Start() error = %v", err)
	}

	agent := svc.newAgentLocked("agent-1")
	agent.cmd = cmd
	agent.state = agentdto.StateIdle
	agent.threadID = "thread-1"
	agent.launchSeq = 1
	agent.sessionGeneration = 7
	svc.agents[agent.id] = agent

	waitDone := make(chan struct{})
	go func() {
		err := cmd.Wait()
		svc.handleProcessExit(context.Background(), agent.id, 1, err)
		close(waitDone)
	}()

	if err := svc.StopAgent(context.Background(), agent.id); err != nil {
		t.Fatalf("StopAgent() error = %v", err)
	}

	select {
	case <-waitDone:
	case <-time.After(2 * time.Second):
		t.Fatal("process exit was not observed")
	}

	select {
	case ev := <-stopped:
		if ev.Reason != "user_requested" {
			t.Fatalf("AgentStopped reason = %q, want user_requested", ev.Reason)
		}
		if ev.AgentID != "agent-1" {
			t.Fatalf("AgentStopped agent_id = %q, want agent-1", ev.AgentID)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("expected AgentStopped event")
	}

	if agent.state != agentdto.StateStopped {
		t.Fatalf("agent.state = %q, want %q", agent.state, agentdto.StateStopped)
	}
	if agent.cmd != nil {
		t.Fatal("agent.cmd = non-nil, want nil after observed exit")
	}
	if cleaner.removeCurrentCalls != 0 {
		t.Fatalf("removeCurrentCalls = %d, want 0", cleaner.removeCurrentCalls)
	}
	if len(cleaner.removeGeneration) != 1 || cleaner.removeGeneration[0] != 7 {
		t.Fatalf("removeGeneration = %#v, want [7]", cleaner.removeGeneration)
	}
}

func TestRemoveSessionGenerationAwareCleanerDoesNotFallbackToCurrent(t *testing.T) {
	t.Parallel()

	cleaner := &stopTestSessionCleaner{}
	svc := NewService(silentLogger(), nil, cleaner, nil, nil)
	agent := svc.newAgentLocked("agent-1")
	agent.sessionGeneration = 11

	svc.removeSession(agent)
	svc.removeSession(agent)

	if cleaner.removeCurrentCalls != 0 {
		t.Fatalf("removeCurrentCalls = %d, want 0", cleaner.removeCurrentCalls)
	}
	if len(cleaner.removeGeneration) != 1 || cleaner.removeGeneration[0] != 11 {
		t.Fatalf("removeGeneration = %#v, want [11]", cleaner.removeGeneration)
	}
}

func TestHandleProcessExitClearsRuntimeState(t *testing.T) {
	t.Parallel()

	svc := NewService(nil, nil, nil, nil, nil)
	agent := runtimeTestAgent()
	agent.state = agentdto.StateStopping
	agent.stopRequested = true
	agent.stopReason = "user_requested"
	agent.launchSeq = 2
	agent.runtimePort = 9090
	agent.runtimeProvider = "claude"
	svc.agents[agent.id] = agent

	svc.handleProcessExit(context.Background(), agent.id, 2, nil)

	snapshot := svc.snapshotLocked(context.Background(), agent)
	if snapshot.Port != 8080 || snapshot.PortSource != "inferred" {
		t.Fatalf("snapshot port after exit = (%d, %q), want (8080, inferred)", snapshot.Port, snapshot.PortSource)
	}
	if snapshot.Provider != "codex" || snapshot.ProviderSource != "inferred" {
		t.Fatalf("snapshot provider after exit = (%q, %q), want (codex, inferred)", snapshot.Provider, snapshot.ProviderSource)
	}
	if agent.runtimePort != 0 {
		t.Fatalf("agent.runtimePort = %d, want 0", agent.runtimePort)
	}
	if agent.runtimeProvider != "" {
		t.Fatalf("agent.runtimeProvider = %q, want empty", agent.runtimeProvider)
	}
}
