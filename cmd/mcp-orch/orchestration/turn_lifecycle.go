package orchestration

import (
	"context"
	"errors"
	"log/slog"
	"strings"

	agentdto "github.com/anthropic-ai/super-agent-v3/internal/dto/agent"
	turndto "github.com/anthropic-ai/super-agent-v3/internal/dto/turn"
)

const (
	triggerTurnCompletionRecovered   = "turn_completion_recovered"
	triggerTurnInterruptionRecovered = "turn_interruption_recovered"
)

func handleTurnCompletedEvent(svc *service, logger *slog.Logger, ev turndto.TurnCompleted) {
	if svc == nil {
		return
	}
	ctx := withEventTime(context.Background(), ev.Timestamp)
	err := svc.CompleteTurn(ctx, ev.AgentID, ev.TurnID, ev.Success, ev.Error)
	if shouldIgnoreTurnLifecycleErr(svc, ev.AgentID, ev.TurnID, err) {
		return
	}
	recovered, recoverErr := svc.forceIdleAfterCompletionError(ctx, ev.AgentID, ev.TurnID, ev.Success, ev.Error)
	logTurnCompletionFailure(logger, ev, err, recovered, recoverErr)
}

func handleTurnInterruptedEvent(svc *service, logger *slog.Logger, ev turndto.TurnInterrupted) {
	if svc == nil {
		return
	}
	ctx := withEventTime(context.Background(), ev.Timestamp)
	err := svc.interruptTurn(ctx, ev.AgentID, ev.TurnID, ev.Reason)
	if shouldIgnoreTurnLifecycleErr(svc, ev.AgentID, ev.TurnID, err) {
		return
	}
	recovered, recoverErr := svc.forceIdleAfterInterruptionError(ctx, ev.AgentID, ev.TurnID, ev.Reason)
	logTurnInterruptedFailure(logger, ev, err, recovered, recoverErr)
}

func (s *service) interruptTurn(ctx context.Context, agentID, turnID, reason string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	agent, err := s.lookupAgentLocked(strings.TrimSpace(agentID))
	if err != nil {
		return err
	}
	turnID = strings.TrimSpace(turnID)
	if agent.activeTurnID == "" {
		return errTurnNotActive
	}
	if turnID != "" && agent.activeTurnID != turnID {
		return errTurnNotActive
	}
	agent.lastError = strings.TrimSpace(reason)
	if err := s.ensureTurnAbortableLocked(ctx, agent); err != nil {
		return err
	}
	if err := s.fireOrForceLocked(ctx, agent, agentdto.TriggerTurnAborted); err != nil {
		return err
	}
	agent.activeTurnID = ""
	return nil
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
	if !canForceIdleAfterTurnTerminal(agent, turnID) {
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

func (s *service) forceIdleAfterInterruptionError(
	ctx context.Context,
	agentID string,
	turnID string,
	reason string,
) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	agent, err := s.lookupAgentLocked(strings.TrimSpace(agentID))
	if err != nil {
		return false, err
	}
	if !canForceIdleAfterTurnTerminal(agent, turnID) {
		return false, errTurnNotActive
	}
	before := agent.state
	agent.activeTurnID = ""
	agent.lastError = strings.TrimSpace(reason)
	agent.updatedAt = resolveEventTime(ctx, agent.updatedAt)
	if err := s.recoverTurnInterruptionStateLocked(ctx, agent); err != nil {
		return false, err
	}
	if before != agent.state {
		s.publishStateChanged(agent, before, triggerTurnInterruptionRecovered)
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

func (s *service) recoverTurnInterruptionStateLocked(ctx context.Context, agent *agentRuntime) error {
	if agent == nil || agent.state == agentdto.StateIdle {
		return nil
	}
	if err := s.ensureTurnAbortableLocked(ctx, agent); err != nil {
		return err
	}
	return s.fireOrForceLocked(ctx, agent, agentdto.TriggerTurnAborted)
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

func (s *service) ensureTurnAbortableLocked(ctx context.Context, agent *agentRuntime) error {
	switch agent.state {
	case agentdto.StateTurnStarting:
		return s.fireOrForceLocked(ctx, agent, agentdto.TriggerTurnAccepted)
	case agentdto.StateTurnRunning, agentdto.StateAwaitingUserInput:
		return nil
	default:
		return formatIllegalTransitionError(ctx, agent, agent.state, agentdto.TriggerTurnAborted, errIllegalStateTransition)
	}
}

func completionRecoveryTrigger(success bool) string {
	if success {
		return agentdto.TriggerTurnCompleted
	}
	return agentdto.TriggerTurnAborted
}

func canForceIdleAfterTurnTerminal(agent *agentRuntime, turnID string) bool {
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

func shouldIgnoreTurnLifecycleErr(svc *service, agentID, turnID string, err error) bool {
	return err == nil || errors.Is(err, errTurnNotActive) && turnTerminalConverged(svc, agentID, turnID)
}

func turnTerminalConverged(svc *service, agentID, turnID string) bool {
	if svc == nil {
		return false
	}
	svc.mu.RLock()
	defer svc.mu.RUnlock()

	agent, err := svc.lookupAgentLocked(strings.TrimSpace(agentID))
	if err != nil {
		return false
	}
	return turnTerminalConvergedLocked(agent, turnID)
}

func turnTerminalConvergedLocked(agent *agentRuntime, turnID string) bool {
	if agent == nil {
		return false
	}
	if strings.TrimSpace(agent.activeTurnID) != "" {
		return false
	}
	if strings.TrimSpace(agent.state) != agentdto.StateIdle {
		return false
	}
	return strings.TrimSpace(turnID) != "" || strings.TrimSpace(agent.threadID) != ""
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

func logTurnInterruptedFailure(
	logger *slog.Logger,
	ev turndto.TurnInterrupted,
	interruptErr error,
	recovered bool,
	recoverErr error,
) {
	if logger == nil {
		logger = slog.Default()
	}
	attrs := []any{
		"agent_id", ev.AgentID,
		"turn_id", ev.TurnID,
		"error", interruptErr,
		"recovered", recovered,
	}
	if recoverErr != nil && !errors.Is(recoverErr, errTurnNotActive) && !errors.Is(recoverErr, errAgentNotFound) {
		attrs = append(attrs, "recovery_error", recoverErr)
	}
	logger.Warn("orchestration: failed to handle turn interruption", attrs...)
}
