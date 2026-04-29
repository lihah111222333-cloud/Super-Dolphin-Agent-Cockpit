package orchestration

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
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
	// 10s tick：plan §3.2「mcp-orch 启动时跑 ticker（10s/次）」。低频足以让
	// 多 agent 任务的 e2e 延迟可接受，又不会把 SQL claim 路径打挤。
	defaultDispatcherTickInterval = 10 * time.Second
	// transient 重试退避：2 分钟够 provider 限流和瞬态故障窗口；超过 8 次
	// 由 SQL 层硬限制（Phase 3.5 metadata 化时回归）。
	defaultWakeupRetryInterval = "00:02:00"
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
	// TickInterval 是主循环 tick 间隔（Run 用）。<=0 时 fallback 到
	// defaultDispatcherTickInterval（10s）。
	TickInterval time.Duration
	// RetryInterval 是 transient 失败时给 RetryWakeup 的 next_retry_at
	// 偏移量（Postgres interval 字符串）。空字符串时 fallback 到
	// defaultWakeupRetryInterval（"00:02:00"）。
	RetryInterval string
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
	if out.TickInterval <= 0 {
		out.TickInterval = defaultDispatcherTickInterval
	}
	if out.RetryInterval == "" {
		out.RetryInterval = defaultWakeupRetryInterval
	}
	return out
}

var dispatcherClaimedBySeq uint64

// WakeupDispatcher 是 wakeup dispatcher 的最小骨架（Phase 3.1）。当前只承担
// "claim 一批到期 wakeup 并日志记录" 的职责；3.2 在此之上加 launch + 状态
// 推进；3.3 在此之上加 reclaim cron。
// WakeupLauncher 是 dispatcher 调起 agent 的最小接口面。生产用 *service
// 实现（service.LaunchAgent）；测试可注入 mock。
type WakeupLauncher interface {
	LaunchAgent(ctx context.Context, req LaunchRequest) error
}

type WakeupDispatcher struct {
	store    taskdag.Store
	launcher WakeupLauncher // Phase 3.2 起：claim 后真去 launch_agent
	logger   *slog.Logger
	cfg      WakeupDispatcherConfig
}

// NewWakeupDispatcher 构造 dispatcher。store 必传；launcher 可为 nil
// （Phase 3.1 只用 Tick claim 不调 launcher 时）；logger 为 nil 时
// fallback 到 pkglogger.Get()；cfg 任一字段为零值都会被 ConfigOrDefaults
// 填充。
func NewWakeupDispatcher(store taskdag.Store, launcher WakeupLauncher, logger *slog.Logger, cfg WakeupDispatcherConfig) (*WakeupDispatcher, error) {
	if store == nil {
		return nil, errors.New("wakeup dispatcher: store required")
	}
	if logger == nil {
		logger = pkglogger.Get()
	}
	return &WakeupDispatcher{
		store:    store,
		launcher: launcher,
		logger:   logger,
		cfg:      cfg.ConfigOrDefaults(),
	}, nil
}

// ClaimedBy returns the claim fence value written to task_dag_wakeups.claimed_by
// for every claim made by this dispatcher.
func (d *WakeupDispatcher) ClaimedBy() string { return d.cfg.ClaimedBy }

// Run 是 Phase 3.2 dispatcher 主循环。每 cfg.TickInterval（默认 10s）调
// 一次 ProcessBatch；ctx 取消（fx OnStop 触发）时优雅返回 ctx.Err()。
//
// 单 tick 失败不退出循环（claim 失败可能是瞬态 DB 抖动；下一 tick 重试），
// ctx canceled 才停。Run 不开 goroutine —— 调用方负责 go d.Run(ctx)，让
// 调用方能 wait/cancel 该 goroutine 的生命周期。
func (d *WakeupDispatcher) Run(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	interval := d.cfg.TickInterval
	if interval <= 0 {
		interval = defaultDispatcherTickInterval
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	d.logger.Info("wakeup dispatcher: started",
		"claimed_by", d.cfg.ClaimedBy,
		"tick_interval", interval,
		"batch_limit", d.cfg.BatchLimit)
	for {
		select {
		case <-ctx.Done():
			d.logger.Info("wakeup dispatcher: stopping",
				"claimed_by", d.cfg.ClaimedBy,
				"reason", ctx.Err())
			return ctx.Err()
		case <-ticker.C:
			if _, err := d.ProcessBatch(ctx); err != nil {
				// processBatch 已经 logWarn 过；这里只是不退出循环。
				continue
			}
		}
	}
}

// ProcessBatch 是 dispatcher 一轮处理：claim 一批 wakeup，对每条调
// launcher.LaunchAgent；按结果推进状态：成功 MarkWakeupSent，transient
// 失败 RetryWakeup（next_retry_at = now + RetryInterval），permanent 失败
// FailWakeup。返回处理过的条数（含成功/失败）。Phase 3.1 的 Tick 仅做
// claim + log，本方法是 3.2 的真处理路径。当 launcher 为 nil 时退化到
// Tick 行为以兼容 3.1 单元路径。
func (d *WakeupDispatcher) ProcessBatch(ctx context.Context) (int, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if d.launcher == nil {
		return d.Tick(ctx)
	}
	claimed, err := d.store.ClaimDueWakeups(ctx, taskdag.ClaimDueWakeupsInput{
		ClaimedBy:     d.cfg.ClaimedBy,
		LeaseInterval: d.cfg.LeaseInterval,
		Limit:         d.cfg.BatchLimit,
	})
	if err != nil {
		d.logger.Warn("wakeup dispatcher: claim failed",
			"claimed_by", d.cfg.ClaimedBy,
			"error", err)
		return 0, fmt.Errorf("wakeup dispatcher: claim due wakeups: %w", err)
	}
	for i := range claimed {
		d.handleClaimed(ctx, &claimed[i])
	}
	return len(claimed), nil
}

// wakeupFence 把 ClaimDueWakeups 返回的 fence 字段（claimedAt/leaseAt
// 可能为 nil）展开成 time.Time 值，让后续 MarkSent/Retry/Fail 能直接用。
type wakeupFence struct {
	claimedAt time.Time
	leaseAt   time.Time
}

func extractFence(w *taskdag.Wakeup) wakeupFence {
	var f wakeupFence
	if w.ClaimedAt != nil {
		f.claimedAt = *w.ClaimedAt
	}
	if w.LeaseExpiresAt != nil {
		f.leaseAt = *w.LeaseExpiresAt
	}
	return f
}

// handleClaimed 调 launcher.LaunchAgent 并按结果推进 wakeup 状态。任何
// 子步骤失败都 logWarn 但不返回错误：dispatcher 对单条 wakeup 的失败
// 不应中断同 batch 其余条目。fence（claimedAt/claimedBy/leaseExpiresAt）
// 必须原样回传给 store，让 SQL 层防止过期 lease 错乱状态。
func (d *WakeupDispatcher) handleClaimed(ctx context.Context, w *taskdag.Wakeup) {
	if w == nil {
		return
	}
	fence := extractFence(w)
	req := buildLaunchRequestFromWakeup(*w)
	launchErr := d.launcher.LaunchAgent(ctx, req)
	if launchErr == nil {
		d.markLaunched(ctx, w, fence)
		return
	}
	lastErr := truncateWakeupError(launchErr.Error())
	if classifyLaunchError(launchErr) == launchClassPermanent {
		d.markPermanentFail(ctx, w, fence, lastErr, launchErr)
		return
	}
	d.markTransientRetry(ctx, w, fence, lastErr, launchErr)
}

func (d *WakeupDispatcher) markLaunched(ctx context.Context, w *taskdag.Wakeup, fence wakeupFence) {
	if _, err := d.store.MarkWakeupSent(ctx, taskdag.MarkWakeupSentInput{
		ID:             w.ID,
		ClaimedAt:      fence.claimedAt,
		ClaimedBy:      w.ClaimedBy,
		LeaseExpiresAt: fence.leaseAt,
	}); err != nil {
		d.logger.Warn("wakeup dispatcher: mark sent failed",
			"wakeup_id", w.ID, "error", err)
		return
	}
	d.logger.Info("wakeup dispatcher: launched",
		"wakeup_id", w.ID,
		"target_agent_id", w.TargetAgentID,
		"attempt_count", w.AttemptCount)
}

func (d *WakeupDispatcher) markPermanentFail(ctx context.Context, w *taskdag.Wakeup, fence wakeupFence, lastErr string, launchErr error) {
	if _, err := d.store.FailWakeup(ctx, taskdag.FailWakeupInput{
		ID:             w.ID,
		LastError:      lastErr,
		ClaimedAt:      fence.claimedAt,
		ClaimedBy:      w.ClaimedBy,
		LeaseExpiresAt: fence.leaseAt,
	}); err != nil {
		d.logger.Warn("wakeup dispatcher: fail-wakeup write failed",
			"wakeup_id", w.ID, "error", err)
		return
	}
	d.logger.Warn("wakeup dispatcher: launch permanent failure → failed",
		"wakeup_id", w.ID,
		"target_agent_id", w.TargetAgentID,
		"error", launchErr)
}

func (d *WakeupDispatcher) markTransientRetry(ctx context.Context, w *taskdag.Wakeup, fence wakeupFence, lastErr string, launchErr error) {
	rows, err := d.store.RetryWakeup(ctx, taskdag.RetryWakeupInput{
		ID:             w.ID,
		RetryInterval:  d.cfg.RetryInterval,
		LastError:      lastErr,
		ClaimedAt:      fence.claimedAt,
		ClaimedBy:      w.ClaimedBy,
		LeaseExpiresAt: fence.leaseAt,
	})
	if err != nil {
		d.logger.Warn("wakeup dispatcher: retry-wakeup write failed",
			"wakeup_id", w.ID, "error", err)
		return
	}
	if rows == 0 {
		// SQL 层 attempt_count >= 8 硬上限达到（Phase 3.5 metadata 化前的
		// 兜底）。直接转 FailWakeup 避免 wakeup 一直停在 dispatching 死锁。
		d.markPermanentFail(ctx, w, fence, "retry attempts exhausted: "+lastErr, launchErr)
		d.logger.Warn("wakeup dispatcher: retry attempts exhausted → failed",
			"wakeup_id", w.ID,
			"target_agent_id", w.TargetAgentID)
		return
	}
	d.logger.Info("wakeup dispatcher: launch transient failure → retry",
		"wakeup_id", w.ID,
		"target_agent_id", w.TargetAgentID,
		"retry_interval", d.cfg.RetryInterval,
		"error", launchErr)
}

// buildLaunchRequestFromWakeup 把 wakeup 行映射到 LaunchRequest。
// PromptPayload 是 JSON：尝试解码成 LaunchRequest 形状（fields 非空才用），
// 失败则只用 wakeup.TargetAgentID 启个最小请求。Phase 3.4 enqueue 路径
// 还没接通时，生产环境不会真有 wakeup 流入；本函数主要给单测和 3.4 之后
// 的真实数据形状预留入口。
func buildLaunchRequestFromWakeup(w taskdag.Wakeup) LaunchRequest {
	req := LaunchRequest{AgentID: strings.TrimSpace(w.TargetAgentID)}
	payload := append(json.RawMessage(nil), w.PromptPayload...)
	if len(payload) == 0 {
		return req
	}
	var parsed LaunchRequest
	if err := json.Unmarshal(payload, &parsed); err != nil {
		return req
	}
	if strings.TrimSpace(parsed.AgentID) == "" {
		parsed.AgentID = req.AgentID
	}
	return parsed
}

// truncateWakeupError 截短 last_error 字段长度，避免极长 stack 撑爆 DB
// 列。640 字符够大多数 provider error 摘要，又远低于 text 列上限。
func truncateWakeupError(s string) string {
	const limit = 640
	if len(s) <= limit {
		return s
	}
	return s[:limit] + "…(truncated)"
}

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
