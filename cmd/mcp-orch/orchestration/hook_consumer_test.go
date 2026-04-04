package orchestration

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/kelindar/event"

	agentdto "github.com/anthropic-ai/super-agent-v3/internal/dto/agent"
	mcp "github.com/anthropic-ai/super-agent-v3/internal/dto/mcp"
	sharedto "github.com/anthropic-ai/super-agent-v3/internal/dto/shared"
	threaddto "github.com/anthropic-ai/super-agent-v3/internal/dto/thread"
)

func TestHookConsumerAfter_StateChangeMirrorsAgentState(t *testing.T) {
	svc := NewService(silentLogger(), event.NewDispatcher(), nil, nil, nil, nil)
	agent := addHookTestAgent(t, svc, "agent-1")
	agent.state = agentdto.StateTurnStarting
	agent.threadID = "thread-1"
	agent.remoteThreadID = "thread-1"
	agent.activeTurnID = "turn-1"

	consumer := NewHookConsumer(svc, silentLogger())
	stateChanged := agentdto.StateChanged{
		AgentSessionHeader: sharedto.AgentSessionHeader{
			AgentHeader: sharedto.AgentHeader{
				ThreadHeader: sharedto.ThreadHeader{
					EventHeader: sharedto.EventHeader{Timestamp: time.Unix(10, 0).UTC()},
					ThreadID:    "thread-1",
				},
				AgentID: "agent-1",
			},
		},
		NewState: agentdto.StateTurnRunning,
	}
	if _, err := consumer.After(context.Background(), hookPayload(t, hookTopicStateChange, hookRelayKindStateChanged, stateChanged)); err != nil {
		t.Fatalf("After(state running) error = %v", err)
	}

	snapshot, err := svc.Snapshot(context.Background(), "agent-1")
	if err != nil {
		t.Fatalf("Snapshot() error = %v", err)
	}
	if snapshot.State != agentdto.StateTurnRunning {
		t.Fatalf("snapshot.State = %q, want %q", snapshot.State, agentdto.StateTurnRunning)
	}
	if snapshot.ActiveTurnID != "turn-1" {
		t.Fatalf("snapshot.ActiveTurnID = %q, want turn-1", snapshot.ActiveTurnID)
	}

	stateChanged.NewState = agentdto.StateIdle
	stateChanged.Timestamp = time.Unix(11, 0).UTC()
	if _, err := consumer.After(context.Background(), hookPayload(t, hookTopicStateChange, hookRelayKindStateChanged, stateChanged)); err != nil {
		t.Fatalf("After(state idle) error = %v", err)
	}

	snapshot, err = svc.Snapshot(context.Background(), "agent-1")
	if err != nil {
		t.Fatalf("Snapshot() after idle error = %v", err)
	}
	if snapshot.State != agentdto.StateIdle {
		t.Fatalf("snapshot.State after idle = %q, want %q", snapshot.State, agentdto.StateIdle)
	}
	if snapshot.ActiveTurnID != "" {
		t.Fatalf("snapshot.ActiveTurnID after idle = %q, want empty", snapshot.ActiveTurnID)
	}
}

func TestHookConsumerAfter_ProcessExitMarksStopped(t *testing.T) {
	svc := NewService(silentLogger(), event.NewDispatcher(), nil, nil, nil, nil)
	agent := addHookTestAgent(t, svc, "agent-1")
	agent.state = agentdto.StateTurnRunning
	agent.threadID = "thread-1"
	agent.remoteThreadID = "thread-1"
	agent.activeTurnID = "turn-1"

	consumer := NewHookConsumer(svc, silentLogger())
	stopped := threaddto.Stopped{
		EventHeader: sharedto.EventHeader{Timestamp: time.Unix(20, 0).UTC()},
		ThreadID:    "thread-1",
		AgentID:     "agent-1",
		Reason:      "stopped",
	}
	if _, err := consumer.After(context.Background(), hookPayload(t, hookTopicProcessExit, hookRelayKindThreadStopped, stopped)); err != nil {
		t.Fatalf("After(process exit) error = %v", err)
	}

	snapshot, err := svc.Snapshot(context.Background(), "agent-1")
	if err != nil {
		t.Fatalf("Snapshot() error = %v", err)
	}
	if snapshot.State != agentdto.StateStopped {
		t.Fatalf("snapshot.State = %q, want %q", snapshot.State, agentdto.StateStopped)
	}
	if snapshot.ActiveTurnID != "" {
		t.Fatalf("snapshot.ActiveTurnID = %q, want empty", snapshot.ActiveTurnID)
	}
}

func TestHookConsumerAfter_UnknownAgentIsIgnored(t *testing.T) {
	svc := NewService(silentLogger(), event.NewDispatcher(), nil, nil, nil, nil)
	consumer := NewHookConsumer(svc, silentLogger())

	stateChanged := agentdto.StateChanged{
		AgentSessionHeader: sharedto.AgentSessionHeader{
			AgentHeader: sharedto.AgentHeader{
				ThreadHeader: sharedto.ThreadHeader{
					EventHeader: sharedto.EventHeader{Timestamp: time.Unix(30, 0).UTC()},
					ThreadID:    "thread-missing",
				},
				AgentID: "missing-agent",
			},
		},
		NewState: agentdto.StateIdle,
	}
	if _, err := consumer.After(context.Background(), hookPayload(t, hookTopicStateChange, hookRelayKindStateChanged, stateChanged)); err != nil {
		t.Fatalf("After(unknown agent) error = %v", err)
	}
}

func addHookTestAgent(t *testing.T, svc *service, agentID string) *agentRuntime {
	t.Helper()

	svc.mu.Lock()
	defer svc.mu.Unlock()

	agent := svc.newAgentLocked(agentID)
	svc.agents[agentID] = agent
	return agent
}

func hookPayload(t *testing.T, topic, kind string, event any) mcp.HookPayload {
	t.Helper()

	eventRaw, err := json.Marshal(event)
	if err != nil {
		t.Fatalf("json.Marshal(event) error = %v", err)
	}
	contextRaw, err := json.Marshal(hookContextEnvelope{
		Kind:  kind,
		Event: eventRaw,
	})
	if err != nil {
		t.Fatalf("json.Marshal(context) error = %v", err)
	}
	return mcp.HookPayload{
		AgentID: "agent-1",
		Topic:   topic,
		Context: contextRaw,
	}
}
