package cron

import (
	"context"
	"log/slog"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
	pkglogger "github.com/lihah111222333-cloud/super-dolphin-agent/pkg/logger"
)

// TickActor 是主动推进 due job 的入口。启动时先恢复旧 run，再跑新 tick，
// 避免同一窗口重复推进；Run 不再额外开 goroutine，故障会暴露给 RunGroup。
type TickActor struct {
	logger    *slog.Logger
	scheduler *Scheduler
	interval  time.Duration
}

var _ contract.Runner = (*TickActor)(nil)

// NewTickActor 创建按固定间隔触发 cron 扫描的 actor。
// interval 取自 SchedulerConfig，构造函数只负责 wiring，不自行兜底 nil scheduler。
func NewTickActor(logger *slog.Logger, scheduler *Scheduler) *TickActor {
	if logger == nil {
		logger = pkglogger.Get()
	}
	interval := scheduler.cfg.TickInterval
	return &TickActor{logger: logger, scheduler: scheduler, interval: interval}
}

// Run 启动 tick 循环，并在 ctx 取消时返回 ctx.Err()。
// 单次 recovery/tick 失败只记录日志，避免瞬时 DB 问题让后续健康 job 永久停调度。
func (a *TickActor) Run(ctx context.Context) error {
	t := time.NewTimer(timerDelayWithJitter(a.interval))
	defer t.Stop()

	// 恢复失败不停止 tick；坏 run 会被记录，健康 job 仍要继续调度。
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
