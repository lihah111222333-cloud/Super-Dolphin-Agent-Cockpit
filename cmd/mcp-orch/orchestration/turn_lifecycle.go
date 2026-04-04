package orchestration

import (
	"context"
	"errors"
	"log/slog"
	"strings"

	agentdto "github.com/anthropic-ai/super-agent-v3/internal/dto/agent"
	turndto "github.com/anthropic-ai/super-agent-v3/internal/dto/turn"
	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
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
	return s.withAgentLocked(agentID, func(agent *agentRuntime) error {
		if err := s.ensureTurnAbortableLocked(ctx, agent); err != nil {
			return err
		}
		return s.finalizeActiveTurnLocked(ctx, agent, turnID, activeTurnFinalizationKind{
			trigger:   agentdto.TriggerTurnAborted,
			errorText: reason,
		})
	})
}

func (s *service) forceIdleAfterCompletionError(
	ctx context.Context,
	agentID string,
	turnID string,
	success bool,
	errMsg string,
) (bool, error) {
	recovered := false
	err := s.withAgentLocked(agentID, func(agent *agentRuntime) error {
		var recoverErr error
		recovered, recoverErr = s.forceIdleAfterTurnTerminalLocked(ctx, agent, turnID, activeTurnRecoveryKind{
			recoveredTrigger: triggerTurnCompletionRecovered,
			errorText:        errMsg,
			clearError:       success,
			recover: func(ctx context.Context, svc *service, agent *agentRuntime) error {
				return svc.recoverTurnCompletionStateLocked(ctx, agent, success)
			},
		})
		return recoverErr
	})
	return recovered, err
}

func (s *service) forceIdleAfterInterruptionError(
	ctx context.Context,
	agentID string,
	turnID string,
	reason string,
) (bool, error) {
	recovered := false
	err := s.withAgentLocked(agentID, func(agent *agentRuntime) error {
		var recoverErr error
		recovered, recoverErr = s.forceIdleAfterTurnTerminalLocked(ctx, agent, turnID, activeTurnRecoveryKind{
			recoveredTrigger: triggerTurnInterruptionRecovered,
			errorText:        reason,
			recover: func(ctx context.Context, svc *service, agent *agentRuntime) error {
				return svc.recoverTurnInterruptionStateLocked(ctx, agent)
			},
		})
		return recoverErr
	})
	return recovered, err
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
		return s.ensureTurnStartedLocked(ctx, agent, agentdto.TriggerTurnCompleted, agentdto.StateTurnRunning, agentdto.StateAwaitingUserInput)
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
	return s.ensureTurnStartedLocked(ctx, agent, agentdto.TriggerTurnAborted, agentdto.StateTurnRunning, agentdto.StateAwaitingUserInput)
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
	converged := false
	if err := svc.withAgentReadLocked(agentID, func(agent *agentRuntime) error {
		converged = turnTerminalConvergedLocked(agent, turnID)
		return nil
	}); err != nil {
		return false
	}
	return converged
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
		logger = pkglogger.Get()
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
		logger = pkglogger.Get()
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
