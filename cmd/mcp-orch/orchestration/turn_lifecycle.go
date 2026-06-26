package orchestration

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/orchestration/launcherrors"
	agentdto "github.com/anthropic-ai/super-agent-v3/internal/dto/agent"
	tooldto "github.com/anthropic-ai/super-agent-v3/internal/dto/tool"
	turndto "github.com/anthropic-ai/super-agent-v3/internal/dto/turn"
	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
)

// turn lifecycle 事件处理函数。

// handleTurnCompletedEvent 使用 background context 处理 turn.completed 事件。
func handleTurnCompletedEvent(svc *service, logger *slog.Logger, ev turndto.TurnCompleted) {
	handleTurnCompletedEventWithCtx(svc, logger, ev, context.Background())
}

// handleTurnCompletedEventWithCtx 处理 turn.completed，并在常规完成失败时尝试强制收口状态。
func handleTurnCompletedEventWithCtx(svc *service, logger *slog.Logger, ev turndto.TurnCompleted, parent context.Context) {
	if svc == nil {
		return
	}
	ctx, logger, startedAt, ok := prepareTurnCompletedEvent(parent, logger, ev)
	if !ok {
		return
	}
	err := svc.CompleteTurn(ctx, ev.AgentID, ev.TurnID, ev.Success, ev.Error)
	if settleIgnoredTurnCompletion(svc, logger, ev, startedAt, err) {
		return
	}
	if ctx.Err() != nil {
		return
	}
	if settleProviderTurnCompletionMismatch(svc, logger, ev, startedAt, ctx) {
		return
	}
	recovered, recoverErr := svc.forceIdleAfterCompletionError(ctx, ev.AgentID, ev.TurnID, ev.Success, ev.Error)
	logTurnTerminalProgress(logger, "orchestration: turn completed event recovery attempted",
		ev.AgentID, ev.ThreadID, ev.TurnID, startedAt, recoverErr)
	logTurnCompletionFailure(logger, ev, err, recovered, recoverErr)
}

// prepareTurnCompletedEvent 统一 completion 事件的上下文、时间和日志入口。
func prepareTurnCompletedEvent(parent context.Context, logger *slog.Logger, ev turndto.TurnCompleted) (context.Context, *slog.Logger, time.Time, bool) {
	if parent == nil {
		parent = context.Background()
	}
	if parent.Err() != nil {
		return nil, nil, time.Time{}, false
	}
	startedAt := time.Now()
	ctx := withEventTime(parent, ev.Timestamp)
	logger = userInputLogger(logger)
	logTurnCompletedEventReceived(logger, ev)
	return ctx, logger, startedAt, true
}

// settleIgnoredTurnCompletion 处理已经收口过的 completion，保持终态事件幂等。
func settleIgnoredTurnCompletion(svc *service, logger *slog.Logger, ev turndto.TurnCompleted, startedAt time.Time, err error) bool {
	if !shouldIgnoreTurnLifecycleErr(svc, ev.AgentID, ev.TurnID, err) {
		return false
	}
	logTurnTerminalProgress(logger, "orchestration: turn completed event settled",
		ev.AgentID, ev.ThreadID, ev.TurnID, startedAt, nil)
	if detail := turnCompletedReportText(ev); !errors.Is(err, errAgentNotFound) && !ev.Success && detail != "" && launcherrors.Classify(errors.New(detail)) == launcherrors.ClassPermanent {
		svc.stopAgentAfterPermanentTurnFailure(ev.AgentID, ev.ThreadID, "turn_completed_permanent")
	}
	return true
}

// settleProviderTurnCompletionMismatch 处理 provider turn id 与本地 active turn id 不一致但线程匹配的完成事件。
func settleProviderTurnCompletionMismatch(svc *service, logger *slog.Logger, ev turndto.TurnCompleted, startedAt time.Time, ctx context.Context) bool {
	recovered, recoverErr := svc.forceIdleAfterProviderTurnCompletion(ctx, ev)
	if recoverErr != nil || !recovered {
		return false
	}
	logTurnTerminalProgress(logger, "orchestration: turn completed event settled",
		ev.AgentID, ev.ThreadID, ev.TurnID, startedAt, nil)
	if detail := turnCompletedReportText(ev); !ev.Success && detail != "" && launcherrors.Classify(errors.New(detail)) == launcherrors.ClassPermanent {
		svc.stopAgentAfterPermanentTurnFailure(ev.AgentID, ev.ThreadID, "turn_completed_permanent")
	}
	return true
}

// handleTurnInterruptedEvent 使用 background context 处理 turn.interrupted 事件。
func handleTurnInterruptedEvent(svc *service, logger *slog.Logger, ev turndto.TurnInterrupted) {
	handleTurnInterruptedEventWithCtx(svc, logger, ev, context.Background())
}

// handleTurnInterruptedEventWithCtx 处理 turn.interrupted，并在状态漂移时尝试强制 idle。
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

// markAwaitingUserInput 将 active turn 推进到 awaiting_user_input。
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

// resolveAwaitingUserInput 将 awaiting_user_input 恢复为 turn_running，重复 resolved 视为幂等。
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

// ensureTurnRunningForUserInputLocked 确保 agent 已进入可请求用户输入的运行态。
func (s *service) ensureTurnRunningForUserInputLocked(ctx context.Context, agent *agentRuntime) error {
	return s.ensureTurnStartedLocked(ctx, agent, agentdto.TriggerUserInputRequested, agentdto.StateTurnRunning, agentdto.StateAwaitingUserInput)
}

// userInputMatchesActiveTurn 判断 approval 事件是否匹配当前 active turn。
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

// handleToolApprovalRequestedEvent 将 request_user_input/tool approval 事件映射为 awaiting_user_input。
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

// handleToolApprovalResolvedEvent 将 approval resolved 事件映射为用户输入已解决。
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

// approvalResolveReason 将 approval 决策归一为 approve、timeout、cancel 或 reject。
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

// isRequestUserInputEvent 判断 tool approval 事件是否应驱动 user-input 状态。
func isRequestUserInputEvent(kind string) bool {
	// 普通 tool approval 进入 orchestration 时 kind 会被规范化为 "tool"，也需要驱动等待用户输入状态。
	return strings.EqualFold(strings.TrimSpace(kind), "request_user_input") || strings.EqualFold(strings.TrimSpace(kind), "tool")
}

// shouldIgnoreUserInputErr 判断 user-input 状态事件是否已经幂等收口。
func shouldIgnoreUserInputErr(err error) bool {
	return err == nil || errors.Is(err, errAgentNotFound) || errors.Is(err, errTurnNotActive)
}

// userInputLogger 返回非 nil logger，避免事件路径因 logger 缺失 panic。
func userInputLogger(logger *slog.Logger) *slog.Logger {
	if logger != nil {
		return logger
	}
	return pkglogger.Get()
}

// interruptTurn 在 service 锁内中止当前 active turn。
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

// forceIdleAfterCompletionError 在完成事件处理失败时尝试把状态推进到终态。
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

// forceIdleAfterProviderTurnCompletion 在确认 agent/thread 匹配后落 report 并强制收口当前 turn。
func (s *service) forceIdleAfterProviderTurnCompletion(ctx context.Context, ev turndto.TurnCompleted) (bool, error) {
	recovered := false
	err := s.withAgentLocked(ev.AgentID, func(agent *agentRuntime) error {
		if !canRecoverProviderTurnCompletion(agent, ev) {
			return errTurnNotActive
		}
		if _, err := s.applyReportEventLocked(
			ctx,
			agent,
			"turn/completed",
			mustMarshalHookReportEvent(ev),
			turnCompletedReportText(ev),
		); err != nil {
			return err
		}
		var recoverErr error
		recovered, recoverErr = s.forceIdleAfterTurnTerminalLocked(ctx, agent, "", activeTurnRecoveryKind{
			recoveredTrigger: string(completionRecoveryTrigger(ev.Success)),
			errorText:        ev.Error,
			clearError:       ev.Success,
			recover: func(ctx context.Context, svc *service, agent *agentRuntime) error {
				return svc.recoverTurnCompletionStateLocked(ctx, agent, ev.Success)
			},
		})
		return recoverErr
	})
	return recovered, err
}

// forceIdleAfterInterruptionError 在中断事件处理失败时尝试把状态推进到 idle。
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

// recoverTurnCompletionStateLocked 修正完成事件到达时可能残留的中间状态。
func (s *service) recoverTurnCompletionStateLocked(ctx context.Context, agent *agentRuntime, success bool) error {
	if agent == nil || agent.state == agentdto.StateIdle {
		return nil
	}
	if err := s.normalizeTurnCompletionStateLocked(ctx, agent, success); err != nil {
		return err
	}
	return s.fireOrForceLocked(ctx, agent, completionRecoveryTrigger(success))
}

// recoverTurnInterruptionStateLocked 修正中断事件到达时可能残留的中间状态。
func (s *service) recoverTurnInterruptionStateLocked(ctx context.Context, agent *agentRuntime) error {
	if agent == nil || agent.state == agentdto.StateIdle {
		return nil
	}
	if err := s.ensureTurnAbortableLocked(ctx, agent); err != nil {
		return err
	}
	return s.fireOrForceLocked(ctx, agent, agentdto.TriggerTurnAborted)
}

// normalizeTurnCompletionStateLocked 在完成触发前补齐状态机要求的前置状态。
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

// ensureTurnAbortableLocked 确保 agent 已进入可被中止的 turn 运行态。
func (s *service) ensureTurnAbortableLocked(ctx context.Context, agent *agentRuntime) error {
	return s.ensureTurnStartedLocked(ctx, agent, agentdto.TriggerTurnAborted, agentdto.StateTurnRunning, agentdto.StateAwaitingUserInput)
}

// completionRecoveryTrigger 根据 completion 成功与否选择完成或中止触发。
func completionRecoveryTrigger(success bool) agentdto.AgentTrigger {
	if success {
		return agentdto.TriggerTurnCompleted
	}
	return agentdto.TriggerTurnAborted
}

// canForceIdleAfterTurnTerminal 判断终态事件失败后是否允许强制收口 active turn。
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

// canRecoverProviderTurnCompletion 限定只恢复同一 agent/thread 的 provider turn id mismatch。
func canRecoverProviderTurnCompletion(agent *agentRuntime, ev turndto.TurnCompleted) bool {
	if agent == nil {
		return false
	}
	eventTurnID := strings.TrimSpace(ev.TurnID)
	activeTurnID := strings.TrimSpace(agent.activeTurnID)
	if eventTurnID == "" || activeTurnID == "" || eventTurnID == activeTurnID {
		return false
	}
	if !agentStateMatches(agent.state, agentdto.StateTurnStarting, agentdto.StateTurnRunning, agentdto.StateAwaitingUserInput) {
		return false
	}
	return turnCompletedEventMatchesAgentThread(agent, ev.ThreadID)
}

// turnCompletedEventMatchesAgentThread 判断 provider 完成事件是否属于当前 agent/thread。
func turnCompletedEventMatchesAgentThread(agent *agentRuntime, threadID string) bool {
	threadID = strings.TrimSpace(threadID)
	if threadID == "" {
		return false
	}
	for _, candidate := range []string{agent.threadID, agent.remoteThreadID, agent.id} {
		if strings.EqualFold(threadID, strings.TrimSpace(candidate)) {
			return true
		}
	}
	return false
}

// shouldIgnoreTurnLifecycleErr 判断终态事件错误是否可以视为已幂等收口。
func shouldIgnoreTurnLifecycleErr(svc *service, agentID, turnID string, err error) bool {
	return err == nil || errors.Is(err, errTurnNotActive) && turnTerminalConverged(svc, agentID, turnID)
}

// turnTerminalConverged 检查 agent 是否已经没有 active turn 且回到 idle。
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

// turnTerminalConvergedLocked 在持锁读取路径判断 turn 终态是否已收敛。
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

// logTurnCompletedEventReceived 记录收到的 completion 事件及 report 长度。
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

// logTurnInterruptedEventReceived 记录收到的 interruption 事件和原因。
func logTurnInterruptedEventReceived(logger *slog.Logger, ev turndto.TurnInterrupted) {
	logger = userInputLogger(logger)
	logger.Info("orchestration: turn interrupted event received",
		pkglogger.String(pkglogger.FieldAgentID, ev.AgentID),
		pkglogger.String(pkglogger.FieldThreadID, ev.ThreadID),
		pkglogger.String(pkglogger.FieldTurnID, ev.TurnID),
		pkglogger.String(pkglogger.FieldStatus, "interrupted"),
		pkglogger.String("reason", strings.TrimSpace(ev.Reason)))
}

// logTurnTerminalProgress 根据耗时和错误选择 info/warn 记录终态处理进度。
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

// logTurnCompletionFailure 记录 completion 处理失败；已收敛缺失只打 debug。
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

// logTurnInterruptedFailure 记录 interruption 处理失败；已收敛缺失只打 debug。
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
