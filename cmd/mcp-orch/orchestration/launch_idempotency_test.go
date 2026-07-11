package orchestration

import (
	"context"
	"sync/atomic"
	"testing"

	"github.com/creachadair/jrpc2/handler"
	"github.com/kelindar/event"

	agentdto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/agent"
)

func TestService_LaunchAgentRejectsActiveExplicitIDWithoutClearingRemoteIdentity(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		state         agentdto.AgentState
		launchSeq     uint64
		lastExitedSeq uint64
	}{
		{name: "provisioning", state: agentdto.StateProvisioning, launchSeq: 1, lastExitedSeq: 0},
		{name: "running", state: agentdto.StateTurnRunning, launchSeq: 1, lastExitedSeq: 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var startCalls atomic.Int64
			svc := NewService(silentLogger(), event.NewDispatcher(), remoteLocalLauncher(t, handler.Map{
				"thread/start": handler.New(func(_ context.Context, _ map[string]any) (map[string]any, error) {
					startCalls.Add(1)
					return map[string]any{"thread": map[string]any{"id": "thread-new"}, "agentId": "remote-new"}, nil
				}),
			}), nil, nil, nil)
			agent := svc.newAgentLocked("agent-active")
			agent.state = tc.state
			agent.launchSeq = tc.launchSeq
			agent.lastExitedSeq = tc.lastExitedSeq
			agent.threadID = "thread-active"
			agent.remoteThreadID = "thread-active"
			agent.remoteAgentID = "remote-active"
			svc.registry.agents[agent.id] = agent

			err := svc.LaunchAgent(context.Background(), LaunchRequest{
				AgentID: "agent-active",
				Name:    "replacement",
				Cwd:     t.TempDir(),
				Command: []string{"ignored"},
			})
			if err == nil {
				t.Fatal("LaunchAgent() error = nil, want already launched error")
			}
			if got := startCalls.Load(); got != 0 {
				t.Fatalf("thread/start calls = %d, want 0", got)
			}
			if agent.remoteThreadID != "thread-active" || agent.remoteAgentID != "remote-active" || agent.threadID != "thread-active" {
				t.Fatalf("remote identity after rejected launch = thread:%q remoteThread:%q remoteAgent:%q, want unchanged",
					agent.threadID, agent.remoteThreadID, agent.remoteAgentID)
			}
		})
	}
}

func TestService_LaunchAgentRetriesInactiveExplicitIDWithStaleRemoteIdentity(t *testing.T) {
	t.Parallel()

	var startCalls atomic.Int64
	svc := NewService(silentLogger(), event.NewDispatcher(), remoteLocalLauncher(t, handler.Map{
		"thread/start": handler.New(func(_ context.Context, _ map[string]any) (map[string]any, error) {
			startCalls.Add(1)
			return map[string]any{"thread": map[string]any{"id": "thread-new"}, "agentId": "remote-new"}, nil
		}),
	}), nil, nil, nil)
	agent := svc.newAgentLocked("agent-retry")
	agent.state = agentdto.StateFailed
	agent.remoteThreadID = "thread-stale"
	agent.remoteAgentID = "remote-stale"
	svc.registry.agents[agent.id] = agent

	if err := svc.LaunchAgent(context.Background(), LaunchRequest{
		AgentID: "agent-retry",
		Name:    "retry",
		Cwd:     t.TempDir(),
		Command: []string{"ignored"},
	}); err != nil {
		t.Fatalf("LaunchAgent() error = %v", err)
	}
	if got := startCalls.Load(); got != 1 {
		t.Fatalf("thread/start calls = %d, want 1", got)
	}
}

func TestService_LaunchAgentRejectsActiveRequestedAliasAfterRemoteRekey(t *testing.T) {
	t.Parallel()

	var startCalls atomic.Int64
	svc := NewService(silentLogger(), event.NewDispatcher(), remoteLocalLauncher(t, handler.Map{
		"thread/start": handler.New(func(_ context.Context, _ map[string]any) (map[string]any, error) {
			startCalls.Add(1)
			return map[string]any{"thread": map[string]any{"id": "thread-new"}, "agentId": "remote-new"}, nil
		}),
	}), nil, nil, nil)
	agent := svc.newAgentLocked("remote-final")
	agent.state = agentdto.StateTurnRunning
	agent.requestedAgentID = "agent-requested"
	agent.remoteThreadID = "thread-final"
	agent.remoteAgentID = "remote-final"
	svc.registry.agents[agent.id] = agent

	err := svc.LaunchAgent(context.Background(), LaunchRequest{
		AgentID: "agent-requested",
		Name:    "duplicate",
		Cwd:     t.TempDir(),
		Command: []string{"ignored"},
	})
	if err == nil {
		t.Fatal("LaunchAgent() error = nil, want already launched error")
	}
	if got := startCalls.Load(); got != 0 {
		t.Fatalf("thread/start calls = %d, want 0", got)
	}
}

func TestService_LaunchAgentDoesNotReuseInactiveRemoteIdentityForExplicitID(t *testing.T) {
	t.Parallel()

	var startCalls atomic.Int64
	svc := NewService(silentLogger(), event.NewDispatcher(), remoteLocalLauncher(t, handler.Map{
		"thread/start": handler.New(func(_ context.Context, _ map[string]any) (map[string]any, error) {
			startCalls.Add(1)
			return map[string]any{"thread": map[string]any{"id": "thread-new"}, "agentId": "remote-new"}, nil
		}),
	}), nil, nil, nil)
	existing := svc.newAgentLocked("agent-local")
	existing.state = agentdto.StateFailed
	existing.remoteThreadID = "thread-old"
	existing.remoteAgentID = "remote-alias"
	svc.registry.agents[existing.id] = existing

	if err := svc.LaunchAgent(context.Background(), LaunchRequest{
		AgentID: "remote-alias",
		Name:    "fresh explicit id",
		Cwd:     t.TempDir(),
		Command: []string{"ignored"},
	}); err != nil {
		t.Fatalf("LaunchAgent() error = %v", err)
	}
	if got := startCalls.Load(); got != 1 {
		t.Fatalf("thread/start calls = %d, want 1", got)
	}
	if existing.remoteAgentID != "remote-alias" || existing.remoteThreadID != "thread-old" {
		t.Fatalf("inactive remote identity was mutated = thread:%q agent:%q", existing.remoteThreadID, existing.remoteAgentID)
	}
	launched := svc.registry.agents["remote-new"]
	if launched == nil || launched.requestedAgentID != "remote-alias" {
		t.Fatalf("launched runtime = %#v, want remote-new with requested alias remote-alias", launched)
	}
}
