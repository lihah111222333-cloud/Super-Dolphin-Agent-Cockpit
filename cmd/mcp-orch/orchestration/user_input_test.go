package orchestration

import (
	"context"
	"testing"
	"time"

	"github.com/kelindar/event"

	agentdto "github.com/anthropic-ai/super-agent-v3/internal/dto/agent"
	shared "github.com/anthropic-ai/super-agent-v3/internal/dto/shared"
	tooldto "github.com/anthropic-ai/super-agent-v3/internal/dto/tool"
)

func TestHandleToolApprovalRequestedEventMarksAwaitingUserInput(t *testing.T) {
	t.Parallel()

	svc := NewService(silentLogger(), event.NewDispatcher(), nil, nil, nil, nil)
	agent := svc.newAgentLocked("agent-1")
	agent.state = agentdto.StateTurnRunning
	agent.activeTurnID = "turn-1"
	svc.registry.agents[agent.id] = agent

	handleToolApprovalRequestedEvent(svc, silentLogger(), approvalRequestedEvent("agent-1", "turn-1"))

	if agent.state != agentdto.StateAwaitingUserInput {
		t.Fatalf("agent.state = %q, want %q", agent.state, agentdto.StateAwaitingUserInput)
	}
}

func TestHandleToolApprovalRequestedEventMarksAwaitingUserInputForToolKind(t *testing.T) {
	t.Parallel()

	svc := NewService(silentLogger(), event.NewDispatcher(), nil, nil, nil, nil)
	agent := svc.newAgentLocked("agent-1")
	agent.state = agentdto.StateTurnRunning
	agent.activeTurnID = "turn-1"
	svc.registry.agents[agent.id] = agent

	ev := approvalRequestedEvent("agent-1", "turn-1")
	ev.Kind = "tool"
	handleToolApprovalRequestedEvent(svc, silentLogger(), ev)

	if agent.state != agentdto.StateAwaitingUserInput {
		t.Fatalf("agent.state = %q, want %q", agent.state, agentdto.StateAwaitingUserInput)
	}
}

func TestHandleToolApprovalResolvedEventReturnsToTurnRunning(t *testing.T) {
	t.Parallel()

	svc := NewService(silentLogger(), event.NewDispatcher(), nil, nil, nil, nil)
	agent := svc.newAgentLocked("agent-1")
	agent.state = agentdto.StateAwaitingUserInput
	agent.activeTurnID = "turn-1"
	svc.registry.agents[agent.id] = agent

	handleToolApprovalResolvedEvent(svc, silentLogger(), approvalResolvedEvent("agent-1", "turn-1"))

	if agent.state != agentdto.StateTurnRunning {
		t.Fatalf("agent.state = %q, want %q", agent.state, agentdto.StateTurnRunning)
	}
}

// Intentional: timeout/cancel resolve awaiting_user_input back to turn_running;
// approval outcome handling stays above the state machine.
func TestHandleToolApprovalResolvedEventClosesAwaitingUserInputOnTimeoutOrCancel(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name     string
		decision string
	}{
		{name: "timeout", decision: "approval timed out"},
		{name: "cancel", decision: "context canceled"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			svc := NewService(silentLogger(), event.NewDispatcher(), nil, nil, nil, nil)
			agent := svc.newAgentLocked("agent-1")
			agent.state = agentdto.StateAwaitingUserInput
			agent.activeTurnID = "turn-1"
			svc.registry.agents[agent.id] = agent

			handleToolApprovalResolvedEvent(svc, silentLogger(), approvalResolvedEventWithDecision("agent-1", "turn-1", false, tc.decision))

			if agent.state != agentdto.StateTurnRunning {
				t.Fatalf("agent.state = %q, want %q", agent.state, agentdto.StateTurnRunning)
			}
		})
	}
}

func TestForceIdleAfterCompletionErrorRecoversAwaitingUserInput(t *testing.T) {
	t.Parallel()

	svc := NewService(silentLogger(), event.NewDispatcher(), nil, nil, nil, nil)
	agent := svc.newAgentLocked("agent-1")
	agent.state = agentdto.StateAwaitingUserInput
	agent.activeTurnID = "turn-1"
	svc.registry.agents[agent.id] = agent

	recovered, err := svc.forceIdleAfterCompletionError(withEventTime(context.Background(), time.Now()), "agent-1", "turn-1", true, "")
	if err != nil {
		t.Fatalf("forceIdleAfterCompletionError() error = %v", err)
	}
	if !recovered {
		t.Fatal("forceIdleAfterCompletionError() recovered = false, want true")
	}
	if agent.state != agentdto.StateIdle {
		t.Fatalf("agent.state = %q, want %q", agent.state, agentdto.StateIdle)
	}
}

func approvalRequestedEvent(agentID, turnID string) tooldto.ToolApprovalRequested {
	return tooldto.ToolApprovalRequested{
		ToolApprovalHeader: approvalHeaderForEvent(agentID, turnID),
		Kind:               "request_user_input",
	}
}

func approvalResolvedEvent(agentID, turnID string) tooldto.ToolApprovalResolved {
	return approvalResolvedEventWithDecision(agentID, turnID, false, "")
}

func approvalResolvedEventWithDecision(agentID, turnID string, approved bool, decision string) tooldto.ToolApprovalResolved {
	return tooldto.ToolApprovalResolved{
		ToolApprovalHeader: approvalHeaderForEvent(agentID, turnID),
		Approved:           approved,
		Decision:           decision,
		Kind:               "request_user_input",
	}
}

func approvalHeaderForEvent(agentID, turnID string) shared.ToolApprovalHeader {
	return shared.ToolApprovalHeader{
		ToolCallHeader: shared.ToolCallHeader{
			TurnHeader: shared.TurnHeader{
				AgentHeader: shared.AgentHeader{
					ThreadHeader: shared.ThreadHeader{
						EventHeader: shared.EventHeader{Timestamp: time.Now()},
					},
					AgentID: agentID,
				},
				TurnIDHeader: shared.TurnIDHeader{TurnID: turnID},
			},
			CallID:   "call-1",
			ToolName: "request_user_input",
		},
		ApprovalID: "approval-1",
	}
}
