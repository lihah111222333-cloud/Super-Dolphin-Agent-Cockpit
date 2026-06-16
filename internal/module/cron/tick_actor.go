package cron

import (
	"context"
	"time"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	pkglogger "github.com/anthropic-ai/super-agent-v3/internal/platform/logging"
)

// TickActor is the cron tick loop Runner. Every TickInterval it calls
// scheduler.RunTick, which claims due jobs and drives each through the
// three-phase state machine. On ctx cancel it returns ctx.Err() so
// platformrunner.RunGroup takes the shutdown signal normally.
//
// There is no anonymous goroutine inside Run — all work happens in the
// RunGroup-managed frame so panics, deadlocks and slow paths are all
// visible via the group's recover + exit channel.
//
// TickActor 是主动推进 due job 的入口。启动时先恢复旧 run，再跑新 tick，
// 避免同一窗口重复推进。
type TickActor struct {
	logger    *pkglogger.Logger
	scheduler *Scheduler
	interval  time.Duration
}

var _ contract.Runner = (*TickActor)(nil)

// NewTickActor wires a TickActor with a zero-field-ok signature. interval
// defaults to the scheduler's TickInterval when non-positive.
// NewTickActor 创建按固定间隔触发 cron 扫描的 actor。
func NewTickActor(logger *pkglogger.Logger, scheduler *Scheduler) *TickActor {
	if logger == nil {
		logger = pkglogger.Get()
	}
	interval := scheduler.cfg.TickInterval
	return &TickActor{logger: logger, scheduler: scheduler, interval: interval}
}

// Run blocks until ctx cancels. RunTick failures are logged and
// otherwise ignored; the loop keeps ticking so a transient DB error in
// one tick doesn't stop scheduling indefinitely.
// Run 启动后台循环，并在上下文取消时退出。
func (a *TickActor) Run(ctx context.Context) error {
	t := time.NewTimer(timerDelayWithJitter(a.interval))
	defer t.Stop()

	// 恢复失败不停止 tick；坏 run 会被记录，健康 job 仍要继续调度。
	if err := a.scheduler.RecoverDanglingRuns(ctx); err != nil {
		a.logger.Debug("cron: recovery failed", pkglogger.String("error", err.Error()))
	}
	if err := a.scheduler.RunTick(ctx); err != nil {
		a.logger.Debug("cron: tick failed", pkglogger.String("error", err.Error()))
	}

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-t.C:
			if err := a.scheduler.RunTick(ctx); err != nil {
				a.logger.Debug("cron: tick failed", pkglogger.String("error", err.Error()))
			}
			t.Reset(timerDelayWithJitter(a.interval))
		}
	}
}
