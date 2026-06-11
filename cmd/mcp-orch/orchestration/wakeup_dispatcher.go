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

// Wakeup dispatcher defaults and wakeup kind handling live here.
const (
	defaultWakeupClaimBatchLimit  = 10
	defaultWakeupLeaseInterval    = "00:00:30"
	defaultDispatcherTickInterval = 10 * time.Second
	defaultWakeupRetryInterval    = "00:02:00"
)

// WakeupDispatcherConfig decides one dispatcher tick; zero values use defaults.
type WakeupDispatcherConfig struct {
	ClaimedBy     string
	LeaseInterval string
	BatchLimit    int32
	TickInterval  time.Duration
	RetryInterval string
}

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

type WakeupLauncher interface {
	LaunchAgent(ctx context.Context, req LaunchRequest) error
}

type WakeupDispatcher struct {
	store    taskdag.Store
	launcher WakeupLauncher // Phase 3.2 起：claim 后真去 launch_agent
	logger   *slog.Logger
	cfg      WakeupDispatcherConfig

	nodeRouter *NodeExecutorRouter

	retryAlertSink DispatchRetryAlertSink
}

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

func (d *WakeupDispatcher) WithNodeRouter(router *NodeExecutorRouter) *WakeupDispatcher {
	if d != nil {
		d.nodeRouter = router
	}
	return d
}

func (d *WakeupDispatcher) WithDispatchRetryAlertSink(sink DispatchRetryAlertSink) *WakeupDispatcher {
	if d != nil {
		d.retryAlertSink = sink
	}
	return d
}

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

// handleClaimed advances one claimed wakeup without aborting the rest of the
// batch: internal repair wakeups are handled locally, DAG wakeups route through
// NodeExecutor, and non-DAG wakeups use the legacy launcher.
func (d *WakeupDispatcher) handleClaimed(ctx context.Context, w *taskdag.Wakeup) bool {
	if w == nil {
		return false
	}
	if turncompletionretry.IsWakeup(w) {
		return d.handleTurnCompletionRetryWakeup(ctx, w)
	}
	if isDAGWakeup(w) {
		// Router wiring marker: handleClaimedViaRouter calls d.nodeRouter.RouteByWakeup.
		return d.handleClaimedViaRouter(ctx, w)
	}
	return d.handleClaimedViaLegacyLauncher(ctx, w)
}

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

func (d *WakeupDispatcher) shouldRouteThroughNodeExecutor(w *taskdag.Wakeup) bool {
	return d != nil && isDAGWakeup(w)
}

// handleClaimedViaLegacyLauncher 是 wiring batch 前原造逻辑，保留不动。
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

// buildLaunchRequestFromWakeup 把 legacy / non-router wakeup 行映射到 LaunchRequest。
// Router-driven DAG wakeups no longer depend on UpstreamOutputs prompt hints;
// main-path upstream context uses explicit inputs.from_nodes plus node.result
// envelopes. The DownstreamWakeupPayload.UpstreamOutputs branch remains only
// for old/manual wakeups that still carry the legacy payload.
func buildLaunchRequestFromWakeup(w taskdag.Wakeup) LaunchRequest {
	req := LaunchRequest{AgentID: strings.TrimSpace(w.TargetAgentID)}
	payload := append(json.RawMessage(nil), w.PromptPayload...)
	if len(payload) == 0 {
		return req
	}
	// Phase 3.9: DAG-driven payload 优先。
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

// tryDAGFailWithCascade decides whether a DAG wakeup has exhausted retries.
// It returns decided=false when the normal retry path may still run.
//
// DAG metadata / runtime node config 读取失败时显式失败，保留
// node-level retry/on_failure 契约可见性。
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
// node-level retry/on_failure 契约；node 不存在时仍仅基于 DAG 默认派生。
func (d *WakeupDispatcher) resolveDAGRetryPolicy(ctx context.Context, dagKey, nodeKey string, runID int64) (retrypolicy.RetryPolicy, bool, error) {
	retryCtx, ok, err := d.resolveDAGRetryContext(ctx, dagKey, nodeKey, runID)
	if err != nil || !ok {
		return retrypolicy.RetryPolicy{}, false, err
	}
	return retryCtx.policy, true, nil
}
