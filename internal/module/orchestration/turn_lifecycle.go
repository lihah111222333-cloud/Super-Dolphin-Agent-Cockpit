package orchestration

import (
	"context"
	"errors"
	"log/slog"
	"strings"

	agentdto "github.com/anthropic-ai/super-agent-v3/internal/dto/agent"
	turndto "github.com/anthropic-ai/super-agent-v3/internal/dto/turn"
)

const triggerTurnCompletionRecovered = "turn_completion_recovered"

func handleTurnCompletedEvent(svc *service, logger *slog.Logger, ev turndto.TurnCompleted) {
	if svc == nil {
		return
	}
	ctx := withEventTime(context.Background(), ev.Timestamp)
	err := svc.CompleteTurn(ctx, ev.AgentID, ev.TurnID, ev.Success, ev.Error)
	if err == nil {
		return
	}
	recovered, recoverErr := svc.forceIdleAfterCompletionError(ctx, ev.AgentID, ev.TurnID, ev.Success, ev.Error)
	logTurnCompletionFailure(logger, ev, err, recovered, recoverErr)
}

func (s *service) forceIdleAfterCompletionError(
	ctx context.Context,
	agentID string,
	turnID string,
	success bool,
	errMsg string,
) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	agent, err := s.lookupAgentLocked(strings.TrimSpace(agentID))
	if err != nil {
		return false, err
	}
	if !canForceIdleAfterCompletion(agent, turnID) {
		return false, errTurnNotActive
	}
	before := agent.state
	agent.activeTurnID = ""
	if success {
		agent.lastError = ""
	} else {
		agent.lastError = strings.TrimSpace(errMsg)
	}
	agent.updatedAt = resolveEventTime(ctx, agent.updatedAt)
	if err := s.recoverTurnCompletionStateLocked(ctx, agent, success); err != nil {
		return false, err
	}
	if before != agent.state {
		s.publishStateChanged(agent, before, triggerTurnCompletionRecovered)
	}
	return true, nil
}

func (s *service) recoverTurnCompletionStateLocked(ctx context.Context, agent *agentRuntime, success bool) error {
	if agent == nil || agent.state == agentdto.StateIdle {
		return nil
	}
	if err := s.normalizeTurnCompletionStateLocked(ctx, agent, success); err != nil {
		return err
	}
	return s.fireOrForceLocked(ctx, agent, completionRecoveryTrigger(success))
}

func (s *service) normalizeTurnCompletionStateLocked(ctx context.Context, agent *agentRuntime, success bool) error {
	switch agent.state {
	case agentdto.StateTurnStarting:
		if success {
			return nil
		}
		return s.fireOrForceLocked(ctx, agent, agentdto.TriggerTurnAccepted)
	case agentdto.StateAwaitingUserInput:
		if !success {
			return nil
		}
		return s.fireOrForceLocked(ctx, agent, agentdto.TriggerUserInputResolved)
	default:
		return nil
	}
}

func completionRecoveryTrigger(success bool) string {
	if success {
		return agentdto.TriggerTurnCompleted
	}
	return agentdto.TriggerTurnAborted
}

func canForceIdleAfterCompletion(agent *agentRuntime, turnID string) bool {
	if agent == nil {
		return false
	}
	turnID = strings.TrimSpace(turnID)
	activeTurnID := strings.TrimSpace(agent.activeTurnID)
	if turnID != "" && activeTurnID != "" && activeTurnID != turnID {
		return false
	}
	if activeTurnID != "" {
		return true
	}
	switch strings.TrimSpace(agent.state) {
	case agentdto.StateTurnStarting, agentdto.StateTurnRunning, agentdto.StateAwaitingUserInput:
		return true
	default:
		return false
	}
}

func logTurnCompletionFailure(
	logger *slog.Logger,
	ev turndto.TurnCompleted,
	completeErr error,
	recovered bool,
	recoverErr error,
) {
	if logger == nil {
		logger = slog.Default()
	}
	attrs := []any{
		"agent_id", ev.AgentID,
		"turn_id", ev.TurnID,
		"error", completeErr,
		"recovered", recovered,
	}
	if recoverErr != nil && !errors.Is(recoverErr, errTurnNotActive) && !errors.Is(recoverErr, errAgentNotFound) {
		attrs = append(attrs, "recovery_error", recoverErr)
	}
	logger.Warn("orchestration: failed to handle turn completion", attrs...)
}
