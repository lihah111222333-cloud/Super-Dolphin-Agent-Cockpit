package cron

import (
	"context"
	"time"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
)

// LeaseActor is the heartbeat Runner that bumps claim leases every
// LeaseHeartbeat. Separate from TickActor because the P1b plan mandates
// two independent actors so a slow tick never starves heartbeats, and
// vice versa; both live in runner.actors and share only the read-only
// scheduler reference.
//
// lease 只说明当前 scheduler 还在负责这个 job，不代表 turn 已完成。
// 心跳独立运行，可减少长任务被误判为丢失。
type LeaseActor struct {
	logger    *pkglogger.Logger
	scheduler *Scheduler
	interval  time.Duration
}

var _ contract.Runner = (*LeaseActor)(nil)

// NewLeaseActor 创建 cron 租约续期 actor。
func NewLeaseActor(logger *pkglogger.Logger, scheduler *Scheduler) *LeaseActor {
	if logger == nil {
		logger = pkglogger.Get()
	}
	interval := scheduler.cfg.LeaseHeartbeat
	return &LeaseActor{logger: logger, scheduler: scheduler, interval: interval}
}

// Run 启动后台循环，并在上下文取消时退出。
func (a *LeaseActor) Run(ctx context.Context) error {
	t := time.NewTimer(timerDelayWithJitter(a.interval))
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-t.C:
			// 续租失败只记录，下一轮 claim 或 recovery 会处理。
			if err := a.scheduler.RenewLeases(ctx); err != nil {
				a.logger.Debug("cron: renew leases failed", pkglogger.String("error", err.Error()))
			}
			t.Reset(timerDelayWithJitter(a.interval))
		}
	}
}
