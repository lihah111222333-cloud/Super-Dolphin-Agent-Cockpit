package wakeupreclaim

import (
	"context"
	"errors"
	"log/slog"
	"time"

	taskdag "github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/store/taskdag"
	platformrunner "github.com/anthropic-ai/super-agent-v3/internal/platform/runner"
	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"

	"go.uber.org/fx"
)

// wakeup lease 过期回收器独立于 dispatcher 运行。
//
// 它按 cfg.TickInterval 调用 ReclaimStaleDispatchingWakeups，把 status='dispatching'
// 且 lease_expires_at 到期的 wakeup 还原成 pending，释放 claimed_by/claimed_at/lease_expires_at。
//
// 独立 ticker 的边界是：dispatcher claim 或投递失败时，过期 lease 仍能被释放；
// 回收间隔和 dispatcher tick 间隔可以分别调整，避免一个循环的异常拖住另一个循环。
//
// store 层 fence 保证：wakeup 被回收后，旧 claim 副本再调用 MarkSent/Retry/Fail
// 会因 claimed_at 或 lease_expires_at 不匹配而 rows=0，不能覆盖新一轮调度结果。
const (
	DefaultWakeupReclaimInterval = 30 * time.Second
)

// WakeupReclaimerConfig 决定 reclaimer ticker 的间隔。
// TickInterval 为零或负数时显式采用 DefaultWakeupReclaimInterval。
type WakeupReclaimerConfig struct {
	TickInterval time.Duration
}

// ConfigOrDefaults 返回补齐默认回收间隔的副本。
func (c WakeupReclaimerConfig) ConfigOrDefaults() WakeupReclaimerConfig {
	out := c
	if out.TickInterval <= 0 {
		out.TickInterval = DefaultWakeupReclaimInterval
	}
	return out
}

// WakeupReclaimer 是 lease 过期回收后台 runner。
// Run 是阻塞主循环，调用方通过 run.Group 或 goroutine 管理其生命周期。
type WakeupReclaimer struct {
	store  taskdag.Store
	logger *slog.Logger
	cfg    WakeupReclaimerConfig
}

// NewWakeupReclaimer 构造 reclaimer。
// store 必传；logger 为 nil 时使用全局 logger；cfg 零值会补齐默认回收间隔。
func NewWakeupReclaimer(store taskdag.Store, logger *slog.Logger, cfg WakeupReclaimerConfig) (*WakeupReclaimer, error) {
	if store == nil {
		return nil, errors.New("wakeup reclaimer: store required")
	}
	if logger == nil {
		logger = pkglogger.Get()
	}
	return &WakeupReclaimer{
		store:  store,
		logger: logger,
		cfg:    cfg.ConfigOrDefaults(),
	}, nil
}

// Run 主循环：每 cfg.TickInterval 调一次 ReclaimOnce；ctx 取消时退出。
// 单 tick 失败不退出循环（DB 抖动应被下次 tick 吸收）。
func (r *WakeupReclaimer) Run(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	interval := r.cfg.TickInterval
	if interval <= 0 {
		interval = DefaultWakeupReclaimInterval
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	r.logger.Info("wakeup reclaimer: started",
		"tick_interval", interval)
	for {
		select {
		case <-ctx.Done():
			r.logger.Info("wakeup reclaimer: stopping",
				"reason", ctx.Err())
			return ctx.Err()
		case <-ticker.C:
			_, _ = r.ReclaimOnce(ctx) // ReclaimOnce 已 logWarn 错误，loop 继续
		}
	}
}

// ReclaimOnce 调一次 ReclaimStaleDispatchingWakeups，返回回收行数。
// 0 行（无过期 lease）是常态空跑，不打 info 噪声日志；>0 行打一行 info
// 让运维可以观察到 reclaim 频率。
func (r *WakeupReclaimer) ReclaimOnce(ctx context.Context) (int64, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	rows, err := r.store.ReclaimStaleDispatchingWakeups(ctx)
	if err != nil {
		r.logger.Warn("wakeup reclaimer: reclaim failed",
			"error", err)
		return 0, err
	}
	if rows > 0 {
		r.logger.Info("wakeup reclaimer: reclaimed stale dispatching wakeups",
			"rows", rows)
	}
	return rows, nil
}

// ProvideWakeupReclaimerRunnerIn 是 fx 注入 wakeup reclaimer runner 的参数结构。
// Store 允许为空，便于未装载 taskdag 模块的测试图返回 no-op runner。
type ProvideWakeupReclaimerRunnerIn struct {
	fx.In

	Store  taskdag.Store `optional:"true"`
	Logger *slog.Logger  `optional:"true"`
}

// ProvideWakeupReclaimerRunner 暴露 wakeup reclaim 后台 runner。
// 它作为 run.Group runner 接线；Store 缺失时返回 no-op，生产图则启动独立 ticker 回收过期 lease。
func ProvideWakeupReclaimerRunner(in ProvideWakeupReclaimerRunnerIn) (platformrunner.Runner, error) {
	logger := in.Logger
	if logger == nil {
		logger = pkglogger.Get()
	}
	if in.Store == nil {
		logger.Info("orchestration: wakeup reclaimer disabled (no taskdag store provided)")
		return platformrunner.NoopRunner{}, nil
	}
	return NewWakeupReclaimer(in.Store, logger, WakeupReclaimerConfig{})
}
