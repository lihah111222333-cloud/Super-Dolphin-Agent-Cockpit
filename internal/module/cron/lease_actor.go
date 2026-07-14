package cron

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
	pkglogger "github.com/lihah111222333-cloud/super-dolphin-agent/pkg/logger"
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

// Run 启动 lease heartbeat 循环；续租失败只在当前 lease 安全预算内重试。
func (a *LeaseActor) Run(ctx context.Context) error {
	t := time.NewTimer(timerDelayWithJitter(a.interval))
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-t.C:
			if err := a.scheduler.RenewLeases(ctx); err != nil {
				if err := a.retryRenewLeases(ctx, err); err != nil {
					return err
				}
			}
			t.Reset(timerDelayWithJitter(a.interval))
		}
	}
}

// retryRenewLeases 在最早 lease 安全期限前有界重试，耗尽后中断失租 active turn。
func (a *LeaseActor) retryRenewLeases(ctx context.Context, initial error) error {
	var initialRenewalErr *leaseRenewalError
	if !errors.As(initial, &initialRenewalErr) {
		return initial
	}
	err := initial
	deadline := leaseRenewRetryDeadline(err, time.Now(), a.scheduler.cfg)
	for err != nil {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			var renewalErr *leaseRenewalError
			if errors.As(err, &renewalErr) {
				return errors.Join(err, a.scheduler.cancelLeaseFailures(ctx, renewalErr.failures))
			}
			return err
		}
		delay := min(a.interval/2, remaining)
		if delay <= 0 {
			delay = remaining
		}
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
		err = a.scheduler.RenewLeases(ctx)
		if err != nil {
			a.logger.Debug("cron: renew leases retry failed", slog.String("error", err.Error()))
		}
	}
	return nil
}

// leaseRenewRetryDeadline 取配置预算与失败 job lease 截止时间中的最早安全点。
func leaseRenewRetryDeadline(err error, now time.Time, cfg SchedulerConfig) time.Time {
	budget := cfg.LeaseTTL - cfg.LeaseHeartbeat
	if budget <= 0 {
		budget = cfg.LeaseHeartbeat
	}
	deadline := now.Add(budget)
	var renewalErr *leaseRenewalError
	if !errors.As(err, &renewalErr) {
		return deadline
	}
	for _, failure := range renewalErr.failures {
		if failure.job.LeaseExpiresAt.IsZero() {
			continue
		}
		candidate := failure.job.LeaseExpiresAt.Add(-cfg.LeaseHeartbeat)
		if candidate.Before(deadline) {
			deadline = candidate
		}
	}
	return deadline
}
