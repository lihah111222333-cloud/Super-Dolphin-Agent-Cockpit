package cron

import (
	"context"
	"log/slog"
	"strings"

	"github.com/kelindar/event"

	turndto "github.com/anthropic-ai/super-agent-v3/internal/dto/turn"
	platformbus "github.com/anthropic-ai/super-agent-v3/internal/platform/bus"
	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
)

func subscribeCronProgress(dispatcher *event.Dispatcher, scheduler *Scheduler, logger *pkglogger.Logger) context.CancelFunc {
	if logger == nil {
		logger = pkglogger.Get()
	}
	return platformbus.ResilientSubscribe(dispatcher, func(ev turndto.ItemCompleted) {
		if err := scheduler.ExtendClaimForTurnProgress(context.Background(), ev.TurnID); err != nil {
			logger.Debug("cron: extend claim for turn progress failed",
				slog.String("turn_id", ev.TurnID),
				slog.String("error", err.Error()),
			)
		}
	}, logger)
}

func subscribeCronTerminalEvents(dispatcher *event.Dispatcher, scheduler *Scheduler, logger *pkglogger.Logger) context.CancelFunc {
	if logger == nil {
		logger = pkglogger.Get()
	}
	cancelCompleted := platformbus.ResilientSubscribe(dispatcher, func(ev turndto.TurnCompleted) {
		if err := scheduler.CompleteTurn(context.Background(), ev.TurnID, ev.Success, terminalErrorText(ev)); err != nil {
			logger.Debug("cron: complete turn from terminal event failed",
				slog.String("turn_id", ev.TurnID),
				slog.String("error", err.Error()),
			)
		}
	}, logger)
	cancelInterrupted := platformbus.ResilientSubscribe(dispatcher, func(ev turndto.TurnInterrupted) {
		if err := scheduler.CompleteTurn(context.Background(), ev.TurnID, false, "turn interrupted: "+ev.Reason); err != nil {
			logger.Debug("cron: fail turn from interrupted event failed",
				slog.String("turn_id", ev.TurnID),
				slog.String("error", err.Error()),
			)
		}
	}, logger)
	return func() {
		if cancelCompleted != nil {
			cancelCompleted()
		}
		if cancelInterrupted != nil {
			cancelInterrupted()
		}
	}
}

func terminalErrorText(ev turndto.TurnCompleted) string {
	if ev.Success {
		return ""
	}
	for _, value := range []string{ev.Error, ev.Reason, ev.Status, ev.StopReason, ev.Message} {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return "cron: turn completed unsuccessfully"
}
