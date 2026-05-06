package cron

import (
	"context"
	"log/slog"
	"time"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
)

// TickActor is the cron tick loop Runner. Every TickInterval it calls
// scheduler.RunTick, which claims due jobs and drives each through the
// three-phase state machine. On ctx cancel it returns ctx.Err() so
// platformrunner.RunGroup takes the shutdown signal normally.
//
// There is no anonymous goroutine inside Run — all work happens in the
// RunGroup-managed frame so panics, deadlocks and slow paths are all
// visible via the group's recover + exit channel.
type TickActor struct {
	logger    *slog.Logger
	scheduler *Scheduler
	interval  time.Duration
}

var _ contract.Runner = (*TickActor)(nil)

// NewTickActor wires a TickActor with a zero-field-ok signature. interval
// defaults to the scheduler's TickInterval when non-positive.
func NewTickActor(logger *slog.Logger, scheduler *Scheduler) *TickActor {
	if logger == nil {
		logger = pkglogger.Get()
	}
	interval := scheduler.cfg.TickInterval
	return &TickActor{logger: logger, scheduler: scheduler, interval: interval}
}

// Run blocks until ctx cancels. RunTick failures are logged and
// otherwise ignored; the loop keeps ticking so a transient DB error in
// one tick doesn't stop scheduling indefinitely.
func (a *TickActor) Run(ctx context.Context) error {
	t := time.NewTimer(timerDelayWithJitter(a.interval))
	defer t.Stop()

	if err := a.scheduler.RecoverDanglingRuns(ctx); err != nil {
		a.logger.Debug("cron: recovery failed", slog.String("error", err.Error()))
	}
	if err := a.scheduler.RunTick(ctx); err != nil {
		a.logger.Debug("cron: tick failed", slog.String("error", err.Error()))
	}

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-t.C:
			if err := a.scheduler.RunTick(ctx); err != nil {
				a.logger.Debug("cron: tick failed", slog.String("error", err.Error()))
			}
			t.Reset(timerDelayWithJitter(a.interval))
		}
	}
}
