package orchestration

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	agentdto "github.com/anthropic-ai/super-agent-v3/internal/dto/agent"
	tooldto "github.com/anthropic-ai/super-agent-v3/internal/dto/tool"
)

func (s *service) markAwaitingUserInput(ctx context.Context, agentID, turnID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	agent, err := s.lookupAgentLocked(strings.TrimSpace(agentID))
	if err != nil {
		return err
	}
	if !userInputMatchesActiveTurn(agent, turnID) {
		return errTurnNotActive
	}
	if err := s.ensureTurnRunningForUserInputLocked(ctx, agent); err != nil {
		return err
	}
	if agent.state == agentdto.StateAwaitingUserInput {
		return nil
	}
	return s.fireOrForceLocked(ctx, agent, agentdto.TriggerUserInputRequested)
}

func (s *service) resolveAwaitingUserInput(ctx context.Context, agentID, turnID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	agent, err := s.lookupAgentLocked(strings.TrimSpace(agentID))
	if err != nil {
		return err
	}
	if !userInputMatchesActiveTurn(agent, turnID) {
		return errTurnNotActive
	}
	switch agent.state {
	case agentdto.StateAwaitingUserInput:
		return s.fireOrForceLocked(ctx, agent, agentdto.TriggerUserInputResolved)
	case agentdto.StateTurnRunning:
		return nil
	default:
		return fmt.Errorf("%w for agent %q: state=%s trigger=%s", errIllegalStateTransition, agent.id, agent.state, agentdto.TriggerUserInputResolved)
	}
}

func (s *service) ensureTurnRunningForUserInputLocked(ctx context.Context, agent *agentRuntime) error {
	switch agent.state {
	case agentdto.StateTurnStarting:
		return s.fireOrForceLocked(ctx, agent, agentdto.TriggerTurnAccepted)
	case agentdto.StateTurnRunning, agentdto.StateAwaitingUserInput:
		return nil
	default:
		return fmt.Errorf("%w for agent %q: state=%s trigger=%s", errIllegalStateTransition, agent.id, agent.state, agentdto.TriggerUserInputRequested)
	}
}

func userInputMatchesActiveTurn(agent *agentRuntime, turnID string) bool {
	if agent == nil {
		return false
	}
	turnID = strings.TrimSpace(turnID)
	activeTurnID := strings.TrimSpace(agent.activeTurnID)
	if activeTurnID == "" {
		return turnID == ""
	}
	return turnID == "" || turnID == activeTurnID
}

func handleToolApprovalRequestedEvent(svc *service, logger *slog.Logger, ev tooldto.ToolApprovalRequested) {
	if svc == nil || !isRequestUserInputEvent(ev.Kind) {
		return
	}
	if err := svc.markAwaitingUserInput(withEventTime(context.Background(), ev.Timestamp), ev.AgentID, ev.TurnID); shouldIgnoreUserInputErr(err) {
		return
	} else if err != nil {
		userInputLogger(logger).Warn("orchestration: failed to mark awaiting user input", "agent_id", ev.AgentID, "turn_id", ev.TurnID, "error", err)
	}
}

func handleToolApprovalResolvedEvent(svc *service, logger *slog.Logger, ev tooldto.ToolApprovalResolved) {
	if svc == nil || !isRequestUserInputEvent(ev.Kind) {
		return
	}
	if err := svc.resolveAwaitingUserInput(withEventTime(context.Background(), ev.Timestamp), ev.AgentID, ev.TurnID); shouldIgnoreUserInputErr(err) {
		return
	} else if err != nil {
		userInputLogger(logger).Warn("orchestration: failed to resolve awaiting user input", "agent_id", ev.AgentID, "turn_id", ev.TurnID, "error", err)
	}
}

func isRequestUserInputEvent(kind string) bool {
	return strings.EqualFold(strings.TrimSpace(kind), "request_user_input")
}

func shouldIgnoreUserInputErr(err error) bool {
	return err == nil || errors.Is(err, errAgentNotFound) || errors.Is(err, errTurnNotActive)
}

func userInputLogger(logger *slog.Logger) *slog.Logger {
	if logger != nil {
		return logger
	}
	return slog.Default()
}
