package orchestration

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"sync/atomic"
	"time"

	taskdag "github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/store/taskdag"
	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
)

// Phase 3.1 / 3A · wakeup dispatcher 接线（仅 tick 单次，goroutine 主循环
// 在 3.2 加；reclaim cron 在 3.3 加）。
//
// 复用既有 store/taskdag 的 ClaimDueWakeups（FOR UPDATE SKIP LOCKED 在 SQL
// 层已就位，本步不重复造）。Dispatcher.tick 一次调用 = 一次 batch claim：
// 取最多 BatchLimit 条到期 wakeup（status=pending && next_retry_at<=NOW），
// 把它们转入 dispatching 状态并写入 claimed_by/lease_expires_at 形成 fence。
// 真正的 launch + MarkSent/Retry/Fail 在 3.2 接入；本步只把声明 + 日志做完
// 让 fx 生命周期早一些联通。
const (
	defaultWakeupClaimBatchLimit = 10
	// 30s lease：足够 launchAgentViaLauncher 的 retry 链 (max 4s+) + provider
	// 启动 + MarkWakeupSent；过期由 Phase 3.3 reclaim cron 兜底回收。
	defaultWakeupLeaseInterval = "00:00:30"
)

// WakeupDispatcherConfig 决定 dispatcher 一次 tick 的行为参数。
// 默认值见 ConfigOrDefaults；外部传 zero-value 时也能跑。
type WakeupDispatcherConfig struct {
	// ClaimedBy 写到 task_dag_wakeups.claimed_by，用作 lease fence 与多实例
	// 调试。空字符串时 ConfigOrDefaults 会派一个进程级唯一名字。
	ClaimedBy string
	// LeaseInterval 是 Postgres interval 字符串（如 "00:00:30"）。空字符串
	// 时 fallback 到 defaultWakeupLeaseInterval。
	LeaseInterval string
	// BatchLimit 是单次 tick claim 的最大条数。<=0 时 fallback 到 default。
	BatchLimit int32
}

// ConfigOrDefaults 返回带默认值的副本，零值字段被填上 dispatcher 的 default。
func (c WakeupDispatcherConfig) ConfigOrDefaults() WakeupDispatcherConfig {
	out := c
	if out.ClaimedBy == "" {
		// 进程内严格递增，给同进程内多 dispatcher（暂不会有，但留扩展）做区分。
		seq := atomic.AddUint64(&dispatcherClaimedBySeq, 1)
		out.ClaimedBy = "mcp-orch-dispatcher-" + strconv.FormatUint(seq, 10)
	}
	if out.LeaseInterval == "" {
		out.LeaseInterval = defaultWakeupLeaseInterval
	}
	if out.BatchLimit <= 0 {
		out.BatchLimit = defaultWakeupClaimBatchLimit
	}
	return out
}

var dispatcherClaimedBySeq uint64

// WakeupDispatcher 是 wakeup dispatcher 的最小骨架（Phase 3.1）。当前只承担
// "claim 一批到期 wakeup 并日志记录" 的职责；3.2 在此之上加 launch + 状态
// 推进；3.3 在此之上加 reclaim cron。
type WakeupDispatcher struct {
	store  taskdag.Store
	logger *slog.Logger
	cfg    WakeupDispatcherConfig
}

// NewWakeupDispatcher 构造 dispatcher。store 必传；logger 为 nil 时 fallback 到
// pkglogger.Get()；cfg 任一字段为零值都会被 ConfigOrDefaults 填充。
func NewWakeupDispatcher(store taskdag.Store, logger *slog.Logger, cfg WakeupDispatcherConfig) (*WakeupDispatcher, error) {
	if store == nil {
		return nil, errors.New("wakeup dispatcher: store required")
	}
	if logger == nil {
		logger = pkglogger.Get()
	}
	return &WakeupDispatcher{
		store:  store,
		logger: logger,
		cfg:    cfg.ConfigOrDefaults(),
	}, nil
}

// ClaimedBy returns the claim fence value written to task_dag_wakeups.claimed_by
// for every claim made by this dispatcher.
func (d *WakeupDispatcher) ClaimedBy() string { return d.cfg.ClaimedBy }

// Tick 执行一次 batch claim：调 store.ClaimDueWakeups，把 status=pending 且
// 已到 next_retry_at 的 wakeup 取最多 BatchLimit 条转入 dispatching；返回这次
// 实际 claim 到的条数。无 due wakeup 时返回 (0, nil) —— "空跑" 是合法状态，
// 不应被视作错误（与 plan §3.1 验证「无 due wakeup 空跑」对齐）。
//
// Phase 3.1 不在本方法内调用 launcher：claim 后只打 info 日志，wakeup 会停
// 在 dispatching 状态直到 3.3 reclaim cron 把 lease 过期的回收成 pending，
// 或 3.2 主循环接管处理。这是有意的——3.1 单独可 ship + 单测覆盖 claim 行为。
func (d *WakeupDispatcher) Tick(ctx context.Context) (int, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	claimed, err := d.store.ClaimDueWakeups(ctx, taskdag.ClaimDueWakeupsInput{
		ClaimedBy:     d.cfg.ClaimedBy,
		LeaseInterval: d.cfg.LeaseInterval,
		Limit:         d.cfg.BatchLimit,
	})
	if err != nil {
		d.logger.Warn("wakeup dispatcher: claim failed",
			"claimed_by", d.cfg.ClaimedBy,
			"batch_limit", d.cfg.BatchLimit,
			"error", err)
		return 0, fmt.Errorf("wakeup dispatcher: claim due wakeups: %w", err)
	}
	if len(claimed) == 0 {
		// 空跑：不打 info 噪声日志，调用方按需要做计数。
		return 0, nil
	}
	for i := range claimed {
		w := &claimed[i]
		// claim 完一定带 lease；若驱动返回 nil 说明 store 实现违约，记 warn
		// 让这种回归可见但不让 dispatcher 崩。
		var leaseAt time.Time
		if w.LeaseExpiresAt != nil {
			leaseAt = *w.LeaseExpiresAt
		}
		d.logger.Info("wakeup dispatcher: claimed wakeup",
			"wakeup_id", w.ID,
			"dag_key", w.DagKey,
			"node_key", w.NodeKey,
			"target_agent_id", w.TargetAgentID,
			"attempt_count", w.AttemptCount,
			"claimed_by", w.ClaimedBy,
			"lease_expires_at", leaseAt)
	}
	return len(claimed), nil
}
