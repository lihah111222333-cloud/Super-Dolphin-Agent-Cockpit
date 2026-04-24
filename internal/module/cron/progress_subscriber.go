package cron

import (
	"context"
	"log/slog"

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
