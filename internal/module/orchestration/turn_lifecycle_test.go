package orchestration

import (
	"testing"
	"time"

	"github.com/kelindar/event"

	agentdto "github.com/anthropic-ai/super-agent-v3/internal/dto/agent"
	shared "github.com/anthropic-ai/super-agent-v3/internal/dto/shared"
	turndto "github.com/anthropic-ai/super-agent-v3/internal/dto/turn"
)

func TestHandleTurnCompletedEventForcesIdleWhenActiveTurnMissing(t *testing.T) {
	t.Parallel()

	svc := NewService(silentLogger(), event.NewDispatcher(), nil, nil, nil)
	agent := svc.newAgentLocked("agent-1")
	agent.state = agentdto.StateTurnRunning
	agent.threadID = "thread-1"
	svc.agents[agent.id] = agent

	handleTurnCompletedEvent(svc, silentLogger(), completedEvent("agent-1", "thread-1", "turn-1", false, "boom"))

	if agent.state != agentdto.StateIdle {
		t.Fatalf("agent.state = %q, want %q", agent.state, agentdto.StateIdle)
	}
	if agent.activeTurnID != "" {
		t.Fatalf("activeTurnID = %q, want empty", agent.activeTurnID)
	}
	if agent.lastError != "boom" {
		t.Fatalf("lastError = %q, want boom", agent.lastError)
	}
}

func TestForceIdleAfterCompletionErrorKeepsDifferentActiveTurn(t *testing.T) {
	t.Parallel()

	svc := NewService(silentLogger(), event.NewDispatcher(), nil, nil, nil)
	agent := svc.newAgentLocked("agent-1")
	agent.state = agentdto.StateTurnRunning
	agent.activeTurnID = "turn-active"
	svc.agents[agent.id] = agent

	recovered, err := svc.forceIdleAfterCompletionError(
		withEventTime(nil, time.Now()),
		"agent-1",
		"turn-finished",
		false,
		"boom",
	)
	if err == nil {
		t.Fatal("forceIdleAfterCompletionError() error = nil, want non-nil")
	}
	if recovered {
		t.Fatal("forceIdleAfterCompletionError() recovered = true, want false")
	}
	if agent.state != agentdto.StateTurnRunning {
		t.Fatalf("agent.state = %q, want %q", agent.state, agentdto.StateTurnRunning)
	}
	if agent.activeTurnID != "turn-active" {
		t.Fatalf("activeTurnID = %q, want turn-active", agent.activeTurnID)
	}
}

func completedEvent(agentID, threadID, turnID string, success bool, errMsg string) turndto.TurnCompleted {
	return turndto.TurnCompleted{
		TurnHeader: shared.TurnHeader{
			AgentHeader: shared.AgentHeader{
				ThreadHeader: shared.ThreadHeader{
					EventHeader: shared.EventHeader{Timestamp: time.Now()},
					ThreadID:    threadID,
				},
				AgentID: agentID,
			},
			TurnIDHeader: shared.TurnIDHeader{TurnID: turnID},
		},
		Success: success,
		Error:   errMsg,
	}
}
