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

	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/orchestration/launcherrors"
	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/orchestration/retrypolicy"
	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/orchestration/turncompletionretry"
	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/orchestration/wakeuptext"
	taskdag "github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/store/taskdag"
	platformconfig "github.com/anthropic-ai/super-agent-v3/internal/platform/config"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/runtimesafe"
	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
)

// wakeup dispatcher 默认值集中在这里，避免 claim、retry 和 tick 间隔在多个入口发散。
const (
	defaultWakeupClaimBatchLimit  = 10
	defaultWakeupLeaseInterval    = "00:00:30"
	defaultDispatcherTickInterval = 10 * time.Second
	defaultWakeupRetryInterval    = "00:02:00"
)

// WakeupDispatcherConfig 控制 dispatcher 单轮领取和重试节奏；零值会在启动前补齐默认值。
type WakeupDispatcherConfig struct {
	ClaimedBy     string
	LeaseInterval string
	BatchLimit    int32
	TickInterval  time.Duration
	RetryInterval string
}

// ConfigOrDefaults 填充 dispatcher 配置默认值，并为 claimed_by 生成进程内唯一标识。
func (c WakeupDispatcherConfig) ConfigOrDefaults() WakeupDispatcherConfig {
	out := c
	if out.ClaimedBy == "" {
		seq := dispatcherClaimedBySeq.Add(1)
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

var dispatcherClaimedBySeq atomic.Uint64

// WakeupLauncher 是非 DAG wakeup 投递所需的启动接口。
type WakeupLauncher interface {
	LaunchAgent(ctx context.Context, req LaunchRequest) error
}

// WakeupDispatcher 负责 claim due wakeup、投递执行、按结果 retry/fail/mark sent。
type WakeupDispatcher struct {
	store    taskdag.Store
	launcher WakeupLauncher // 非 DAG wakeup 的启动入口；DAG wakeup 优先走 nodeRouter。
	logger   *slog.Logger
	cfg      WakeupDispatcherConfig

	nodeRouter *NodeExecutorRouter

	retryAlertSink DispatchRetryAlertSink
}

// NewWakeupDispatcher 创建负责投递 DAG wakeup 的调度器。
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

// WithNodeRouter 设置 wakeup 调度器使用的节点路由器。
func (d *WakeupDispatcher) WithNodeRouter(router *NodeExecutorRouter) *WakeupDispatcher {
	if d != nil {
		d.nodeRouter = router
	}
	return d
}

// WithDispatchRetryAlertSink 设置调度重试告警输出。
func (d *WakeupDispatcher) WithDispatchRetryAlertSink(sink DispatchRetryAlertSink) *WakeupDispatcher {
	if d != nil {
		d.retryAlertSink = sink
	}
	return d
}

// ClaimedBy 返回 wakeup 当前领取者标识。
func (d *WakeupDispatcher) ClaimedBy() string { return d.cfg.ClaimedBy }

// Run 是 dispatcher 主循环，每 cfg.TickInterval 调一次 ProcessBatch。
// ctx 取消时直接返回 ctx.Err()，由 fx OnStop 或调用方等待 goroutine 收口。
//
// 单 tick 失败不退出循环（claim 失败可能是瞬态 DB 抖动；下一 tick 重试），
// ctx canceled 才停。Run 不开 goroutine —— 调用方负责 go d.Run(ctx)，让
// 调用方能 wait/cancel 该 goroutine 的启动和停止过程。
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

// ProcessBatch 是 dispatcher 的完整处理轮次：claim 一批 wakeup 后按类型投递。
// 成功会写 sent，瞬时失败会按 RetryInterval 放回 pending，永久失败会写 failed。
// launcher 为空时保留 claim-only Tick 路径，方便只验证领取和 lease 的调用方。
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
	handled := 0
	for i := range claimed {
		if d.handleClaimed(ctx, &claimed[i]) {
			handled++
		}
	}
	return handled, nil
}

// wakeupFence 把 ClaimDueWakeups 返回的 fence 字段（claimedAt/leaseAt
// 可能为 nil）展开成 time.Time 值，让后续 MarkSent/Retry/Fail 能直接用。
type wakeupFence struct {
	claimedAt time.Time
	leaseAt   time.Time
}

// extractFence 从 wakeup 行提取 CAS fence，nil 时间会保留为零值交给 store 比对。
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

// handleClaimed 推进单条已领取 wakeup，单条失败不会中断整批处理。
// 内部修复 wakeup 在本地处理，DAG wakeup 走 NodeExecutor，非 DAG wakeup 走启动接口。
func (d *WakeupDispatcher) handleClaimed(ctx context.Context, w *taskdag.Wakeup) bool {
	if w == nil {
		return false
	}
	if turncompletionretry.IsWakeup(w) {
		return d.handleTurnCompletionRetryWakeup(ctx, w)
	}
	if isDAGWakeup(w) {
		// DAG wakeup 的路由入口集中在 handleClaimedViaRouter，便于保持 sent/fail fence 一致。
		return d.handleClaimedViaRouter(ctx, w)
	}
	return d.handleClaimedViaLegacyLauncher(ctx, w)
}

// handleTurnCompletionRetryWakeup 处理 turn.completed 的修复 wakeup，不会启动 agent。
// 它只重放“完成节点 + 调度下游”，失败再按普通 retry/fail 处理。
func (d *WakeupDispatcher) handleTurnCompletionRetryWakeup(ctx context.Context, w *taskdag.Wakeup) bool {
	fence := extractFence(w)
	res := turncompletionretry.Complete(ctx, d.store, w)
	switch res.Outcome {
	case turncompletionretry.CompleteSucceeded:
		return d.markLaunched(ctx, w, fence)
	case turncompletionretry.CompleteAlreadyTerminal:
		dagSubscriberMetrics.IncIdempotentSkipped()
		return d.markLaunched(ctx, w, fence)
	case turncompletionretry.CompleteRetry:
		lastErr := truncateWakeupError("turn.completed completion retry failed: " + res.Err.Error())
		failure := dispatchFailure{lastErr: lastErr, launchErr: res.Err, outcome: failedWakeupOutcome(lastErr)}
		return d.retryWakeup(ctx, w, fence, failure)
	default:
		lastErr := truncateWakeupError("turn.completed completion retry invalid: " + res.Err.Error())
		return d.markPermanentDAGFailure(ctx, w, fence, lastErr, res.Err, true, failedWakeupOutcome(lastErr))
	}
}

// shouldRouteThroughNodeExecutor 判断 wakeup 是否应交给 NodeExecutor router。
func (d *WakeupDispatcher) shouldRouteThroughNodeExecutor(w *taskdag.Wakeup) bool {
	return d != nil && isDAGWakeup(w)
}

// handleClaimedViaLegacyLauncher 处理非 DAG wakeup 的启动投递。
// 它仍复用 wakeup fence，避免过期 claim 覆盖其它 dispatcher 的处理结果。
func (d *WakeupDispatcher) handleClaimedViaLegacyLauncher(ctx context.Context, w *taskdag.Wakeup) bool {
	fence := extractFence(w)
	req := buildLaunchRequestFromWakeup(*w)
	launchErr := d.launcher.LaunchAgent(ctx, req)
	if launchErr == nil {
		return d.markLaunched(ctx, w, fence)
	}
	lastErr := truncateWakeupError(launchErr.Error())
	if launcherrors.Classify(launchErr) == launcherrors.ClassPermanent {
		return d.markPermanentFail(ctx, w, fence, lastErr, launchErr)
	}
	return d.markTransientRetry(ctx, w, fence, dispatchFailure{lastErr: lastErr, launchErr: launchErr, outcome: failedWakeupOutcome(lastErr)})
}

// markLaunched 只把 wakeup 标成 sent，不决定节点是 running 还是 done。
// rows=0 说明这次 claim 已过期或已被别人处理。
func (d *WakeupDispatcher) markLaunched(ctx context.Context, w *taskdag.Wakeup, fence wakeupFence) bool {
	rows, err := d.store.MarkWakeupSent(ctx, taskdag.MarkWakeupSentInput{
		ID:             w.ID,
		ClaimedAt:      fence.claimedAt,
		ClaimedBy:      w.ClaimedBy,
		LeaseExpiresAt: fence.leaseAt,
	})
	if err != nil {
		d.logger.Warn("wakeup dispatcher: mark sent failed",
			"wakeup_id", w.ID, "error", err)
		return false
	}
	if rows == 0 {
		d.logger.Warn("wakeup dispatcher: mark sent fence missed",
			"wakeup_id", w.ID,
			"target_agent_id", w.TargetAgentID)
		return false
	}
	d.logger.Info("wakeup dispatcher: launched",
		"wakeup_id", w.ID,
		"target_agent_id", w.TargetAgentID,
		"attempt_count", w.AttemptCount)
	return true
}

// markPermanentFail 将非 DAG wakeup 写入 failed，并记录失败指标。
func (d *WakeupDispatcher) markPermanentFail(ctx context.Context, w *taskdag.Wakeup, fence wakeupFence, lastErr string, launchErr error) bool {
	rows, err := d.store.FailWakeup(ctx, taskdag.FailWakeupInput{
		ID:             w.ID,
		LastError:      lastErr,
		ClaimedAt:      fence.claimedAt,
		ClaimedBy:      w.ClaimedBy,
		LeaseExpiresAt: fence.leaseAt,
	})
	if err != nil {
		d.logger.Warn("wakeup dispatcher: fail-wakeup write failed",
			"wakeup_id", w.ID, "error", err)
		return false
	}
	if rows == 0 {
		d.logger.Warn("wakeup dispatcher: fail-wakeup fence missed",
			"wakeup_id", w.ID,
			"target_agent_id", w.TargetAgentID)
		return false
	}
	recordDispatchFailedMetric()
	d.logger.Warn("wakeup dispatcher: launch permanent failure → failed",
		"wakeup_id", w.ID,
		"target_agent_id", w.TargetAgentID,
		"error", launchErr)
	return true
}

// DAG wakeup 重试前先看 DAG/node 配置；到上限就失败并处理下游。
// 非 DAG wakeup 保留旧的 SQL hard cap。
func (d *WakeupDispatcher) markTransientRetry(ctx context.Context, w *taskdag.Wakeup, fence wakeupFence, failure dispatchFailure) bool {
	// DAG-driven wakeup 走 metadata/node config 决策。
	// 当前 attempt_count >= MaxAttempts 直接 fail + cascade 下游（按
	// FailFast）；attempt_count 还没到上限走旧的 RetryWakeup 路径。
	// 非 DAG wakeup（DagKey/NodeKey 空）走兼容路径，保留 SQL 8 hard cap.
	if w.DagKey != "" && w.NodeKey != "" {
		if handled, decided := d.tryDAGFailWithCascade(ctx, w, fence, failure); decided {
			return handled
		}
	}
	return d.retryWakeup(ctx, w, fence, failure)
}

// retryWakeup 把 wakeup 放回 pending，并设置 next_retry_at。
// rows=0 可能是到达上限，也可能是这次 claim 已失效。
func (d *WakeupDispatcher) retryWakeup(ctx context.Context, w *taskdag.Wakeup, fence wakeupFence, failure dispatchFailure) bool {
	rows, err := d.store.RetryWakeup(ctx, taskdag.RetryWakeupInput{
		ID:             w.ID,
		RetryInterval:  d.cfg.RetryInterval,
		LastError:      failure.lastErr,
		ClaimedAt:      fence.claimedAt,
		ClaimedBy:      w.ClaimedBy,
		LeaseExpiresAt: fence.leaseAt,
	})
	if err != nil {
		d.logger.Warn("wakeup dispatcher: retry-wakeup write failed",
			"wakeup_id", w.ID, "error", err)
		return false
	}
	if rows == 0 {
		// SQL 层 attempt_count >= 8 硬上限兜底：转 FailWakeup，避免 dispatching 死锁。
		return d.handleRetryHardCap(ctx, w, fence, failure)
	}
	d.recordRetryAccepted(ctx, w, failure)
	return true
}

// 告警异步发，不阻塞 dispatcher。
// 通知失败也不能改变 wakeup 已经做出的 retry/fail 决定。
func (d *WakeupDispatcher) emitDispatchRetryAlert(ctx context.Context, alert DispatchRetryAlert) {
	if d == nil || d.retryAlertSink == nil {
		return
	}
	sink := d.retryAlertSink
	logger := d.logger
	baseCtx := context.Background()
	if ctx != nil {
		baseCtx = context.WithoutCancel(ctx)
	}
	runtimesafe.SafeGo(baseCtx, logger, "wakeupDispatcher.retryAlert", func(runCtx context.Context) {
		alertCtx, cancel := platformconfig.WithTimeout(runCtx, 5*time.Second)
		defer cancel()
		if err := sink.AlertDispatchRetry(alertCtx, alert); err != nil {
			logger.Warn("wakeup dispatcher: retry alert enqueue failed",
				"wakeup_id", alert.WakeupID,
				"dag_key", alert.DagKey,
				"node_key", alert.NodeKey,
				"error", err)
		}
	})
}

// buildLaunchRequestFromWakeup 把非 router wakeup 行映射到 LaunchRequest。
// DAG 主路径使用 inputs.from_nodes 和 node.result 取上游上下文；UpstreamOutputs
// 只服务仍携带旧 payload 的手工 wakeup，避免影响当前 DAG dispatch。
func buildLaunchRequestFromWakeup(w taskdag.Wakeup) LaunchRequest {
	req := LaunchRequest{AgentID: strings.TrimSpace(w.TargetAgentID)}
	payload := append(json.RawMessage(nil), w.PromptPayload...)
	if len(payload) == 0 {
		return req
	}
	// DAG payload 能提供 agent 和上游输出提示时优先使用，保持旧手工 wakeup 可读。
	var dag taskdag.DownstreamWakeupPayload
	if err := json.Unmarshal(payload, &dag); err == nil && len(dag.UpstreamOutputs) > 0 {
		if strings.TrimSpace(dag.AgentID) != "" {
			req.AgentID = strings.TrimSpace(dag.AgentID)
		}
		req.Prompt = wakeuptext.RenderUpstreamPromptHint(dag.UpstreamOutputs)
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

// Tick 执行一次 claim-only batch：调 store.ClaimDueWakeups，把 status=pending 且
// 已到 next_retry_at 的 wakeup 取最多 BatchLimit 条转入 dispatching；返回这次
// 实际 claim 到的条数。无 due wakeup 时返回 (0, nil)，空跑是合法状态。
//
// 本方法不调用 launcher，claim 后只记录日志；被领取但未处理的 wakeup 依靠
// lease 过期回收，适合测试和诊断领取路径。
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

// tryDAGFailWithCascade 判断 DAG wakeup 是否已经耗尽重试预算。
// 返回 decided=false 表示仍可走普通 retry 路径；读取 retry 配置失败会显式失败。
//
// DAG metadata / runtime node config 读取失败时显式失败，保留
// node-level retry/on_failure 约定可见性。
func (d *WakeupDispatcher) tryDAGFailWithCascade(ctx context.Context, w *taskdag.Wakeup, fence wakeupFence, failure dispatchFailure) (bool, bool) {
	policy, ok, err := d.resolveDAGRetryPolicy(ctx, w.DagKey, w.NodeKey, routeRunID(w))
	if err != nil {
		return d.failSmartRetryPrepare(ctx, w, fence, failure, err, false), true
	}
	if !ok {
		return false, false
	}
	if int(w.AttemptCount) < policy.MaxAttempts {
		return false, false
	}
	lastErr := "max attempts reached: " + failure.lastErr
	if !d.markPermanentDAGFailure(ctx, w, fence, lastErr, failure.launchErr, policy.FailFast, failure.outcome) {
		return false, true
	}
	if alert, shouldAlert := recordDispatchRetryMetric(w, failure.lastErr); shouldAlert {
		d.emitDispatchRetryAlert(ctx, alert)
	}
	return true, true
}

// resolveDAGRetryPolicy 拉取 DAG metadata + node config 派生 retry policy。
// metadata/listNodes 任一报错返 error，让调用方显式失败而不是忽略
// node-level retry/on_failure 约定；node 不存在时仍仅基于 DAG 默认派生。
func (d *WakeupDispatcher) resolveDAGRetryPolicy(ctx context.Context, dagKey, nodeKey string, runID int64) (retrypolicy.RetryPolicy, bool, error) {
	retryCtx, ok, err := d.resolveDAGRetryContext(ctx, dagKey, nodeKey, runID)
	if err != nil || !ok {
		return retrypolicy.RetryPolicy{}, false, err
	}
	return retryCtx.policy, true, nil
}
