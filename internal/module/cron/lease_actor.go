package cron

import (
	"context"
	"log/slog"
	"time"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
)

// LeaseActor 是定期续租 claim lease 的 Runner。
// 它与 TickActor 分开运行，避免慢 tick 饿死心跳，两个 actor 只共享只读 scheduler 引用。
//
// lease 只说明当前 scheduler 还在负责这个 job，不代表 turn 已完成。
// 心跳独立运行，可减少长任务被误判为丢失。
type LeaseActor struct {
	logger    *slog.Logger
	scheduler *Scheduler
	interval  time.Duration
}

// LeaseActor 满足 runner contract，用于 Fx group 注入。
var _ contract.Runner = (*LeaseActor)(nil)

// NewLeaseActor 创建 cron 租约续期 actor，并从 scheduler 配置读取心跳间隔。
func NewLeaseActor(logger *slog.Logger, scheduler *Scheduler) *LeaseActor {
	if logger == nil {
		logger = pkglogger.Get()
	}
	interval := scheduler.cfg.LeaseHeartbeat
	return &LeaseActor{logger: logger, scheduler: scheduler, interval: interval}
}

// Run 启动 lease heartbeat 循环；单次续租失败只记录，后续 claim/recovery 会继续接管。
func (a *LeaseActor) Run(ctx context.Context) error {
	t := time.NewTimer(timerDelayWithJitter(a.interval))
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-t.C:
			// 续租失败不阻断 heartbeat；下一轮 claim/recovery 仍会重新接管租约。
			if err := a.scheduler.RenewLeases(ctx); err != nil {
				a.logger.Debug("cron: renew leases failed", slog.String("error", err.Error()))
			}
			t.Reset(timerDelayWithJitter(a.interval))
		}
	}
}
