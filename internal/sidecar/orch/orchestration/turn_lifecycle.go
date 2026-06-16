package orchestration

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	agentdto "github.com/anthropic-ai/super-agent-v3/internal/dto/agent"
	tooldto "github.com/anthropic-ai/super-agent-v3/internal/dto/tool"
	turndto "github.com/anthropic-ai/super-agent-v3/internal/dto/turn"
	"github.com/anthropic-ai/super-agent-v3/internal/sidecar/orch/orchestration/launcherrors"
	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
)

// removed triggers

func handleTurnCompletedEvent(svc *service, logger *slog.Logger, ev turndto.TurnCompleted) {
	handleTurnCompletedEventWithCtx(svc, logger, ev, context.Background())
}

// handleTurnCompletedEventWithCtx 处理带ctx的turncompleted事件。
func handleTurnCompletedEventWithCtx(svc *service, logger *slog.Logger, ev turndto.TurnCompleted, parent context.Context) {
	if svc == nil {
		return
	}
	if parent == nil {
		parent = context.Background()
	}
	if parent.Err() != nil {
		return
	}
	startedAt := time.Now()
	ctx := withEventTime(parent, ev.Timestamp)
	logger = userInputLogger(logger)
	logTurnCompletedEventReceived(logger, ev)
	err := svc.CompleteTurn(ctx, ev.AgentID, ev.TurnID, ev.Success, ev.Error)
	if shouldIgnoreTurnLifecycleErr(svc, ev.AgentID, ev.TurnID, err) {
		logTurnTerminalProgress(logger, "orchestration: turn completed event settled",
			ev.AgentID, ev.ThreadID, ev.TurnID, startedAt, nil)
		if detail := turnCompletedReportText(ev); !errors.Is(err, errAgentNotFound) && !ev.Success && detail != "" && launcherrors.Classify(errors.New(detail)) == launcherrors.ClassPermanent {
			svc.stopAgentAfterPermanentTurnFailure(ev.AgentID, ev.ThreadID, "turn_completed_permanent")
		}
		return
	}
	if ctx.Err() != nil {
		return
	}
	recovered, recoverErr := svc.forceIdleAfterCompletionError(ctx, ev.AgentID, ev.TurnID, ev.Success, ev.Error)
	logTurnTerminalProgress(logger, "orchestration: turn completed event recovery attempted",
		ev.AgentID, ev.ThreadID, ev.TurnID, startedAt, recoverErr)
	logTurnCompletionFailure(logger, ev, err, recovered, recoverErr)
}

func handleTurnInterruptedEvent(svc *service, logger *slog.Logger, ev turndto.TurnInterrupted) {
	handleTurnInterruptedEventWithCtx(svc, logger, ev, context.Background())
}

// handleTurnInterruptedEventWithCtx 处理带ctx的turninterrupted事件。
func handleTurnInterruptedEventWithCtx(svc *service, logger *slog.Logger, ev turndto.TurnInterrupted, parent context.Context) {
	if svc == nil {
		return
	}
	if parent == nil {
		parent = context.Background()
	}
	if parent.Err() != nil {
		return
	}
	startedAt := time.Now()
	ctx := withEventTime(parent, ev.Timestamp)
	logger = userInputLogger(logger)
	logTurnInterruptedEventReceived(logger, ev)
	err := svc.interruptTurn(ctx, ev.AgentID, ev.TurnID, ev.Reason)
	if shouldIgnoreTurnLifecycleErr(svc, ev.AgentID, ev.TurnID, err) {
		logTurnTerminalProgress(logger, "orchestration: turn interrupted event settled",
			ev.AgentID, ev.ThreadID, ev.TurnID, startedAt, nil)
		if reason := strings.TrimSpace(ev.Reason); !errors.Is(err, errAgentNotFound) && reason != "" && launcherrors.Classify(errors.New(reason)) == launcherrors.ClassPermanent {
			svc.stopAgentAfterPermanentTurnFailure(ev.AgentID, ev.ThreadID, "turn_interrupted_permanent")
		}
		return
	}
	if ctx.Err() != nil {
		return
	}
	recovered, recoverErr := svc.forceIdleAfterInterruptionError(ctx, ev.AgentID, ev.TurnID, ev.Reason)
	logTurnTerminalProgress(logger, "orchestration: turn interrupted event recovery attempted",
		ev.AgentID, ev.ThreadID, ev.TurnID, startedAt, recoverErr)
	logTurnInterruptedFailure(logger, ev, err, recovered, recoverErr)
}

func (s *service) markAwaitingUserInput(ctx context.Context, agentID, turnID string) error {
	return s.withAgentLocked(agentID, func(agent *agentRuntime) error {
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
	})
}

// resolveAwaitingUserInput 解析awaitinguserinput。
func (s *service) resolveAwaitingUserInput(ctx context.Context, agentID, turnID, reason string) error {
	reason = strings.TrimSpace(reason)
	if reason == "" {
		reason = "reject"
	}
	logger := userInputLogger(s.logger)
	return s.withAgentLocked(agentID, func(agent *agentRuntime) error {
		if !userInputMatchesActiveTurn(agent, turnID) {
			return errTurnNotActive
		}
		resolvedTurnID := strings.TrimSpace(turnID)
		if resolvedTurnID == "" {
			resolvedTurnID = strings.TrimSpace(agent.activeTurnID)
		}
		switch agent.state {
		case agentdto.StateAwaitingUserInput:
			if err := s.fireOrForceLocked(ctx, agent, agentdto.TriggerUserInputResolved); err != nil {
				return err
			}
			logger.Info("orchestration: resolved awaiting user input", "agent_id", agent.id, "turn_id", resolvedTurnID, "reason", reason)
			return nil
		case agentdto.StateTurnRunning:
			logger.Info("orchestration: awaiting user input already resolved", "agent_id", agent.id, "turn_id", resolvedTurnID, "reason", reason)
			return nil
		default:
			return fmt.Errorf("%w for agent %q: state=%s trigger=%s", errIllegalStateTransition, agent.id, agent.state, agentdto.TriggerUserInputResolved)
		}
	})
}

func (s *service) ensureTurnRunningForUserInputLocked(ctx context.Context, agent *agentRuntime) error {
	return s.ensureTurnStartedLocked(ctx, agent, agentdto.TriggerUserInputRequested, agentdto.StateTurnRunning, agentdto.StateAwaitingUserInput)
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
	if err := svc.resolveAwaitingUserInput(withEventTime(context.Background(), ev.Timestamp), ev.AgentID, ev.TurnID, approvalResolveReason(ev)); shouldIgnoreUserInputErr(err) {
		return
	} else if err != nil {
		userInputLogger(logger).Warn("orchestration: failed to resolve awaiting user input", "agent_id", ev.AgentID, "turn_id", ev.TurnID, "error", err)
	}
}

func approvalResolveReason(ev tooldto.ToolApprovalResolved) string {
	if ev.Approved {
		return "approve"
	}
	decision := strings.ToLower(strings.TrimSpace(ev.Decision))
	switch {
	case strings.Contains(decision, "timed out"), strings.Contains(decision, "timeout"):
		return "timeout"
	case strings.Contains(decision, "cancel"):
		return "cancel"
	default:
		return "reject"
	}
}

func isRequestUserInputEvent(kind string) bool {
	// Ordinary tool approvals reach orchestration as kind "tool" because
	// rpc.RequestApproval normalizes an empty approval kind to that live value.
	return strings.EqualFold(strings.TrimSpace(kind), "request_user_input") || strings.EqualFold(strings.TrimSpace(kind), "tool")
}

func shouldIgnoreUserInputErr(err error) bool {
	return err == nil || errors.Is(err, errAgentNotFound) || errors.Is(err, errTurnNotActive)
}

func userInputLogger(logger *slog.Logger) *slog.Logger {
	if logger != nil {
		return logger
	}
	return pkglogger.Get()
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
			recoveredTrigger: string(completionRecoveryTrigger(success)),
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
			recoveredTrigger: string(agentdto.TriggerTurnAborted),
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

func completionRecoveryTrigger(success bool) agentdto.AgentTrigger {
	if success {
		return agentdto.TriggerTurnCompleted
	}
	return agentdto.TriggerTurnAborted
}

// canForceIdleAfterTurnTerminal 判断强制idle后置turnterminal是否可用。
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
	switch agent.state {
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
	if agent.state != agentdto.StateIdle {
		return false
	}
	return strings.TrimSpace(turnID) != "" || strings.TrimSpace(agent.threadID) != ""
}

func logTurnCompletedEventReceived(logger *slog.Logger, ev turndto.TurnCompleted) {
	logger = userInputLogger(logger)
	logger.Info("orchestration: turn completed event received",
		pkglogger.String(pkglogger.FieldAgentID, ev.AgentID),
		pkglogger.String(pkglogger.FieldThreadID, ev.ThreadID),
		pkglogger.String(pkglogger.FieldTurnID, ev.TurnID),
		pkglogger.String(pkglogger.FieldStatus, strings.TrimSpace(ev.Status)),
		pkglogger.Any("success", ev.Success),
		pkglogger.Int64("result_len", int64(len(strings.TrimSpace(turnCompletedReportText(ev))))))
}

func logTurnInterruptedEventReceived(logger *slog.Logger, ev turndto.TurnInterrupted) {
	logger = userInputLogger(logger)
	logger.Info("orchestration: turn interrupted event received",
		pkglogger.String(pkglogger.FieldAgentID, ev.AgentID),
		pkglogger.String(pkglogger.FieldThreadID, ev.ThreadID),
		pkglogger.String(pkglogger.FieldTurnID, ev.TurnID),
		pkglogger.String(pkglogger.FieldStatus, "interrupted"),
		pkglogger.String("reason", strings.TrimSpace(ev.Reason)))
}

func logTurnTerminalProgress(
	logger *slog.Logger,
	message string,
	agentID string,
	threadID string,
	turnID string,
	startedAt time.Time,
	err error,
) {
	logger = userInputLogger(logger)
	elapsed := time.Since(startedAt)
	attrs := []any{
		pkglogger.String(pkglogger.FieldAgentID, agentID),
		pkglogger.String(pkglogger.FieldThreadID, threadID),
		pkglogger.String(pkglogger.FieldTurnID, turnID),
		pkglogger.Int64(pkglogger.FieldDurationMS, elapsed.Milliseconds()),
	}
	if err != nil {
		attrs = append(attrs, pkglogger.String(pkglogger.FieldError, err.Error()))
		logger.Warn(message, attrs...)
		return
	}
	if elapsed >= longWaitLogThreshold {
		logger.Warn(message, attrs...)
		return
	}
	logger.Info(message, attrs...)
}

// logTurnCompletionFailure 处理日志turn补全failure。
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
	if errors.Is(completeErr, errAgentNotFound) || errors.Is(completeErr, errTurnNotActive) {
		logger.Debug("orchestration: failed to handle turn completion", attrs...)
		return
	}
	logger.Warn("orchestration: failed to handle turn completion", attrs...)
}

// logTurnInterruptedFailure 处理日志turninterruptedfailure。
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
	if errors.Is(interruptErr, errAgentNotFound) || errors.Is(interruptErr, errTurnNotActive) {
		logger.Debug("orchestration: failed to handle turn interruption", attrs...)
		return
	}
	logger.Warn("orchestration: failed to handle turn interruption", attrs...)
}
