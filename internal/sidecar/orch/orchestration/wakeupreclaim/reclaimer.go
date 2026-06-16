package wakeupreclaim

import (
	"context"
	"errors"
	"log/slog"
	"time"

	platformrunner "github.com/anthropic-ai/super-agent-v3/internal/platform/runner"
	taskdag "github.com/anthropic-ai/super-agent-v3/internal/sidecar/orch/store/taskdag"
	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"

	"go.uber.org/fx"
)

// Phase 3.3 / 3A · wakeup lease 过期回收 cron。
//
// 与 dispatcher（3.2）解耦的独立 ticker：每 cfg.TickInterval（默认 30s）
// 调一次 ReclaimStaleDispatchingWakeups，把 status='dispatching' 且
// lease_expires_at <= NOW() 的 wakeup 还原成 pending（重置 claimed_by /
// claimed_at / lease_expires_at），让下一轮 dispatcher tick 能再 claim。
//
// 为什么要独立 cron 而不是 dispatcher tick 内顺手做：
//   - dispatcher tick 失败（DB 抖动 / panic）会暂停 claim；reclaim 必须
//     仍然推进，否则 lease 过期的 wakeup 一直死锁在 dispatching。
//   - 30s reclaim cadence 与 30s lease default 互不耦合，可以独立 tune。
//
// SQL 层 fence 保证：被 reclaim 后旧的 claim 副本 fence 失效，dispatcher
// 即使持有过时 wakeup 引用调 MarkSent / Retry / Fail 也会因 claimed_at /
// lease_expires_at 不匹配而无效（rows = 0）。
const (
	DefaultWakeupReclaimInterval = 30 * time.Second
)

// WakeupReclaimerConfig 决定 reclaimer ticker 的间隔。零值 fallback 到
// DefaultWakeupReclaimInterval（30s）。
type WakeupReclaimerConfig struct {
	TickInterval time.Duration
}

// ConfigOrDefaults 返回带默认值的副本。
func (c WakeupReclaimerConfig) ConfigOrDefaults() WakeupReclaimerConfig {
	out := c
	if out.TickInterval <= 0 {
		out.TickInterval = DefaultWakeupReclaimInterval
	}
	return out
}

// WakeupReclaimer 是 lease 过期回收 cron 的最小骨架。Run 是 blocking 主循环，
// 调用方负责 go r.Run(ctx)。
type WakeupReclaimer struct {
	store  taskdag.Store
	logger *slog.Logger
	cfg    WakeupReclaimerConfig
}

// NewWakeupReclaimer 构造 reclaimer。store 必传；logger 为 nil 时 fallback
// 到 pkglogger.Get()；cfg 零值会被 ConfigOrDefaults 填默认值。
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

// ProvideWakeupReclaimerRunnerIn lets fx inject taskdag.Store as an optional
// dependency. Same pattern as ProvideWakeupDispatcherRunnerIn (Phase 3.2).
type ProvideWakeupReclaimerRunnerIn struct {
	fx.In

	Store  taskdag.Store `optional:"true"`
	Logger *slog.Logger  `optional:"true"`
}

// ProvideWakeupReclaimerRunner (Phase 3.3) returns the wakeup reclaimer as
// a Runner for injection into run.Group via group:"runners". Independent
// ticker from the wakeup dispatcher so that dispatcher hiccups (DB blip /
// panic) cannot stall the reclaim path that frees stuck-dispatching wakeups.
//
// Wired with fx.Provide + group:"runners" from cmd/mcp-orch/fx.go so
// run.Group manages the goroutine lifecycle. taskdag.Store is optional:
// when missing the reclaimer is replaced by a no-op runner.
// ProvideWakeupReclaimerRunner 暴露 wakeup reclaim 后台 runner。
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
