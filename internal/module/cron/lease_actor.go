package cron

import (
	"context"
	"log/slog"
	"time"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
)

// LeaseActor is the heartbeat Runner that bumps claim leases every
// LeaseHeartbeat. Separate from TickActor because the P1b plan mandates
// two independent actors so a slow tick never starves heartbeats, and
// vice versa; both live in runner.actors and share only the read-only
// scheduler reference.
type LeaseActor struct {
	logger    *slog.Logger
	scheduler *Scheduler
	interval  time.Duration
}

var _ contract.Runner = (*LeaseActor)(nil)

func NewLeaseActor(logger *slog.Logger, scheduler *Scheduler) *LeaseActor {
	if logger == nil {
		logger = pkglogger.Get()
	}
	interval := scheduler.cfg.LeaseHeartbeat
	return &LeaseActor{logger: logger, scheduler: scheduler, interval: interval}
}

func (a *LeaseActor) Run(ctx context.Context) error {
	t := time.NewTimer(timerDelayWithJitter(a.interval))
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-t.C:
			if err := a.scheduler.RenewLeases(ctx); err != nil {
				a.logger.Debug("cron: renew leases failed", slog.String("error", err.Error()))
			}
			t.Reset(timerDelayWithJitter(a.interval))
		}
	}
}
