package orchestration

import (
	"errors"
	"log/slog"
	"strings"
	"time"

	turndto "github.com/anthropic-ai/super-agent-v3/internal/dto/turn"
	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
)

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
