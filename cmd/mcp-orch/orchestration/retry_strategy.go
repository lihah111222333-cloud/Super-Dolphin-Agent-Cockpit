package orchestration

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/orchestration/nodeexec"
	taskdag "github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/store/taskdag"
	platformconfig "github.com/anthropic-ai/super-agent-v3/internal/platform/config"
)

// 导航注（DAG v2 骨架阶段后）：
// 本文件 (`RetryPolicy / DAGSchedulePolicy / NodeExecutionPolicy`) 是 Phase 3.5
// 生产 dispatcher 路径的重试策略 (拿 DAG metadata 里的 default_retry / fail_fast)。
// DAG v2 骨架阶段加了 typed `nodeexec.OnFailureConfig` 提供智能重试。
// F12.1 后，两者收敛为 node-level override 与 fallback 的关系：dispatcher
// 支持 by_class + retry/escalate_model/append_error/replan/fail_fast；skip /
// ask_human 仅保留 enum，业务语义未落地前 fail-closed。详 ADR §2.7。
//
// Phase 3.5 / 3B · 节点失败重试策略
//
// dispatcher 在 launch 失败后判断「再 retry 还是直接 fail」时，必须能从 DAG
// metadata 拿到 default_retry / fail_fast，以及 node 级 execution.retry 覆盖。
// 把解析逻辑独立出来：
//   - 结构化字段（DAGSchedulePolicy / NodeExecutionPolicy）就地用 omitempty
//     反序列化，缺字段就走默认值，不抛错；
//   - 公开 ResolveRetryPolicy(dagMetadata, nodeConfig) → RetryPolicy 给调用
//     方使用；store 层 FailNodeAndCancelDownstream 不依赖此函数（它只接受
//     最终的 fail_fast 布尔），但 dispatcher / RPC 层接通时会经它派生。
//
// SQL 层 RetryTaskDagWakeup 仍保留 attempt_count<8 硬上限作为 paranoid 保护，
// 即使 default_retry 配得比 8 还大，也只能跑到 8。该上限和本策略并不冲突：
// 本策略给的是「软上限」，SQL 给的是「不可越过的物理上限」。

// RetryPolicy 是 dispatcher 派生出的最终决策参数。
type RetryPolicy struct {
	// MaxAttempts 是包含首发的总尝试次数。default_retry=0 → MaxAttempts=1
	// (只跑一次即终态)；default_retry=2 → MaxAttempts=3。MaxAttempts<1 视
	// 同 1，避免 0 导致永远 fail 走不通。
	MaxAttempts int
	// FailFast 决定节点 failed 后是否级联取消下游 pending 节点。来自
	// metadata.schedule.fail_fast；node 层暂无覆盖（execution.on_failure
	// 是节点级 retry/skip 策略，不是图级中断；后续 gate 再处理）。
	FailFast bool
}

// DAGSchedulePolicy 对应 DAG metadata 内 `schedule` 子对象的策略字段子集。
// 与 cmd/mcp-orch/tools/task_tools.go::DAGScheduleInput 对齐，但只取本步用
// 得到的两项；新增字段不影响反序列化。
type DAGSchedulePolicy struct {
	DefaultRetry int  `json:"default_retry,omitempty"`
	FailFast     bool `json:"fail_fast,omitempty"`
}

// dagMetadataPolicy 是 DAG metadata 的最外层（仅取 schedule 子树）。
type dagMetadataPolicy struct {
	Schedule DAGSchedulePolicy `json:"schedule"`
}

// NodeExecutionPolicy 对应 node config 内 `execution` 子对象的策略字段子集。
// HasRetry 显式区分「未设置」和「设置为 0」，以便覆盖 DAG 默认值。
type NodeExecutionPolicy struct {
	Retry    int
	HasRetry bool
}

// nodeExecutionEnvelope 是 task_dag_node.config 的 schema：execution 在
// 一个嵌套 key 下；执行时的 retry 字段允许显式 0（表示「不重试」），所以
// 用 *int 而非 int 解码，再翻成 NodeExecutionPolicy.HasRetry。
type nodeExecutionEnvelope struct {
	Execution struct {
		Retry *int `json:"retry,omitempty"`
	} `json:"execution"`
}

// ResolveRetryPolicy 综合 DAG metadata + node config 解出最终 RetryPolicy。
// 解析失败的字段安静走默认值（DefaultRetry=0 / FailFast=false / 无 node 覆
// 盖），不返回 error：dispatcher 不应因为元数据 JSON 异常就把任务卡死，
// 应当退化到「不再重试」让节点尽快终态。
func ResolveRetryPolicy(dagMetadata, nodeConfig json.RawMessage) RetryPolicy {
	dagPolicy := decodeDAGSchedulePolicy(dagMetadata)
	nodePolicy := decodeNodeExecutionPolicy(nodeConfig)
	retryCount := dagPolicy.DefaultRetry
	if nodePolicy.HasRetry {
		retryCount = nodePolicy.Retry
	}
	maxAttempts := retryCount + 1
	if maxAttempts < 1 {
		maxAttempts = 1
	}
	return RetryPolicy{MaxAttempts: maxAttempts, FailFast: dagPolicy.FailFast}
}

func decodeDAGSchedulePolicy(raw json.RawMessage) DAGSchedulePolicy {
	if len(raw) == 0 {
		return DAGSchedulePolicy{}
	}
	var envelope dagMetadataPolicy
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return DAGSchedulePolicy{}
	}
	return envelope.Schedule
}

func decodeNodeExecutionPolicy(raw json.RawMessage) NodeExecutionPolicy {
	if len(raw) == 0 {
		return NodeExecutionPolicy{}
	}
	var envelope nodeExecutionEnvelope
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return NodeExecutionPolicy{}
	}
	if envelope.Execution.Retry == nil {
		return NodeExecutionPolicy{}
	}
	return NodeExecutionPolicy{Retry: *envelope.Execution.Retry, HasRetry: true}
}

// failureClassPermanent reports whether a failed NodeOutcome should bypass the
// basic bounded retry path. F1.4 keeps this intentionally small: AgentExecutor
// transient/quota/validation failures all use the existing RetryPolicy attempt
// budget. F12.1 owns smarter by_class actions such as append_error,
// escalate_model, and replan.
func failureClassPermanent(class nodeexec.FailureClass) bool {
	switch class {
	case nodeexec.FailureClassHard,
		nodeexec.FailureClassNeedsHuman:
		return true
	}
	return false
}

func failureOutcomePermanent(outcome nodeexec.NodeOutcome) bool {
	if failureClassPermanent(outcome.FailureClass) {
		return true
	}
	if outcome.FailureClass == nodeexec.FailureClassValidation {
		return !retryableValidationOutcome(outcome)
	}
	return false
}

func retryableValidationOutcome(outcome nodeexec.NodeOutcome) bool {
	return strings.HasPrefix(outcome.ErrorSummary, "launch agent:")
}

type dispatchFailure struct {
	lastErr   string
	launchErr error
	outcome   nodeexec.NodeOutcome
}

func dispatchFailureFrom(lastErr string, launchErr error, outcome nodeexec.NodeOutcome) dispatchFailure {
	return dispatchFailure{lastErr: lastErr, launchErr: launchErr, outcome: outcome}
}

func failedWakeupOutcome(summary string) nodeexec.NodeOutcome {
	return nodeexec.NodeOutcome{Status: nodeexec.NodeStatusFailed, ErrorSummary: summary}
}

func withDispatchRetryAlertTimeout(ctx context.Context) (context.Context, context.CancelFunc) {
	return platformconfig.WithTimeout(ctx, 5*time.Second)
}

func (d *WakeupDispatcher) failDAGNodeAndCancelDownstream(ctx context.Context, w *taskdag.Wakeup, lastErr string, failFast bool, outcome nodeexec.NodeOutcome) {
	if w == nil || strings.TrimSpace(w.DagKey) == "" || strings.TrimSpace(w.NodeKey) == "" {
		return
	}
	flow, ok := d.store.(taskdag.NodeFlowStore)
	if !ok {
		d.logger.Warn("wakeup dispatcher: store missing NodeFlowStore, skip cascade",
			"wakeup_id", w.ID, "dag_key", w.DagKey, "node_key", w.NodeKey)
		return
	}
	res, err := flow.FailNodeAndCancelDownstream(ctx, taskdag.FailNodeInput{
		DagKey:   w.DagKey,
		NodeKey:  w.NodeKey,
		RunID:    routeRunID(w),
		Reason:   lastErr,
		FailFast: failFast,
	})
	if err != nil {
		d.logger.Warn("wakeup dispatcher: fail-node cascade write failed",
			"wakeup_id", w.ID, "dag_key", w.DagKey, "node_key", w.NodeKey, "error", err)
		return
	}
	d.logger.Warn("wakeup dispatcher: DAG node failed",
		"wakeup_id", w.ID,
		"dag_key", w.DagKey,
		"node_key", w.NodeKey,
		"attempt_count", w.AttemptCount,
		"fail_fast", failFast,
		"canceled_downstream", len(res.CanceledDownstream))
	if d.nodeRouter != nil {
		if outcome.Status == "" {
			outcome.Status = nodeexec.NodeStatusFailed
		}
		if outcome.ErrorSummary == "" {
			outcome.ErrorSummary = lastErr
		}
		d.nodeRouter.invokeTerminalFailureHooksForWakeup(ctx, w, outcome)
	}
}

func (d *WakeupDispatcher) handleRetryHardCap(ctx context.Context, w *taskdag.Wakeup, fence wakeupFence, failure dispatchFailure) {
	exhaustedErr := "retry attempts exhausted: " + failure.lastErr
	if d.markPermanentFail(ctx, w, fence, exhaustedErr, failure.launchErr) {
		if alert, shouldAlert := recordDispatchRetryMetric(w, failure.lastErr); shouldAlert {
			d.emitDispatchRetryAlert(ctx, alert)
		}
		d.failDAGNodeForRetryHardCap(ctx, w, exhaustedErr, failure)
	}
	d.logger.Warn("wakeup dispatcher: retry attempts exhausted → failed",
		"wakeup_id", w.ID,
		"target_agent_id", w.TargetAgentID)
}

func (d *WakeupDispatcher) failDAGNodeForRetryHardCap(ctx context.Context, w *taskdag.Wakeup, lastErr string, failure dispatchFailure) {
	failFast := false
	if policy, ok := d.resolveDAGRetryPolicy(ctx, w.DagKey, w.NodeKey, routeRunID(w)); ok {
		failFast = policy.FailFast
	}
	d.failDAGNodeAndCancelDownstream(ctx, w, lastErr, failFast, failure.outcome)
}

func (d *WakeupDispatcher) handleFailedRouterOutcome(ctx context.Context, w *taskdag.Wakeup, fence wakeupFence, outcome nodeexec.NodeOutcome) {
	synthErr := fmt.Errorf("%s: %s", outcome.FailureClass, outcome.ErrorSummary)
	lastErr := truncateWakeupError(synthErr.Error())
	failure := dispatchFailureFrom(lastErr, synthErr, outcome)
	if d.trySmartDAGRetry(ctx, w, fence, failure) {
		return
	}
	if !failureOutcomePermanent(outcome) {
		d.markTransientRetry(ctx, w, fence, failure)
		return
	}
	if !d.markPermanentFail(ctx, w, fence, lastErr, synthErr) {
		return
	}
	d.recordPermanentRouterFailure(ctx, w, lastErr, outcome)
}

func (d *WakeupDispatcher) recordPermanentRouterFailure(ctx context.Context, w *taskdag.Wakeup, lastErr string, outcome nodeexec.NodeOutcome) {
	if w.AttemptCount >= 3 {
		if alert, shouldAlert := recordDispatchRetryMetric(w, lastErr); shouldAlert {
			d.emitDispatchRetryAlert(ctx, alert)
		}
	}
	failFast := false
	if policy, ok := d.resolveDAGRetryPolicy(ctx, w.DagKey, w.NodeKey, routeRunID(w)); ok {
		failFast = policy.FailFast
	}
	d.failDAGNodeAndCancelDownstream(ctx, w, lastErr, failFast, outcome)
}

const replanPlannerAgentKey = "dag_designer"

type dagRetryContext struct {
	policy    RetryPolicy
	node      *taskdag.Node
	onFailure *nodeexec.OnFailureConfig
}

func (d *WakeupDispatcher) trySmartDAGRetry(ctx context.Context, w *taskdag.Wakeup, fence wakeupFence, failure dispatchFailure) bool {
	if !canSmartRetry(d, w) {
		return false
	}
	retryCtx, ok := d.resolveDAGRetryContext(ctx, w.DagKey, w.NodeKey, routeRunID(w))
	if !ok || retryCtx.node == nil || retryCtx.onFailure == nil {
		return false
	}
	strategy, ok := smartRetryStrategyFor(retryCtx.onFailure, failure.outcome.FailureClass)
	if !ok {
		return false
	}
	if d.handleSmartRetryPreflight(ctx, w, fence, failure, retryCtx, strategy) {
		return true
	}
	return d.dispatchSmartRetryAction(ctx, w, fence, failure, retryCtx, strategy)
}

func smartRetryStrategyFor(cfg *nodeexec.OnFailureConfig, class nodeexec.FailureClass) (nodeexec.OnFailureStrategy, bool) {
	resolved := configuredOnFailureStrategy(cfg, class)
	if failureClassPermanent(class) && !permanentClassStrategyAllowed(resolved) {
		return "", false
	}
	if !resolved.configured {
		resolved.strategy = nodeexec.OnFailureRetry
	}
	return resolved.strategy, true
}

type smartRetryStrategyResolution struct {
	strategy   nodeexec.OnFailureStrategy
	configured bool
	byClass    bool
}

func configuredOnFailureStrategy(cfg *nodeexec.OnFailureConfig, class nodeexec.FailureClass) smartRetryStrategyResolution {
	if cfg == nil {
		return smartRetryStrategyResolution{}
	}
	if class != "" {
		if strategy, ok := cfg.ByClass[class]; ok && strategy != "" {
			return smartRetryStrategyResolution{strategy: strategy, configured: true, byClass: true}
		}
	}
	if cfg.Default != "" {
		return smartRetryStrategyResolution{strategy: cfg.Default, configured: true}
	}
	return smartRetryStrategyResolution{}
}

func permanentClassStrategyAllowed(resolved smartRetryStrategyResolution) bool {
	switch {
	case !resolved.configured:
		return false
	case resolved.byClass:
		return !smartRetryStrategyRerunsNode(resolved.strategy)
	default:
		return resolved.strategy == nodeexec.OnFailureFailFast
	}
}

func (d *WakeupDispatcher) handleSmartRetryPreflight(
	ctx context.Context,
	w *taskdag.Wakeup,
	fence wakeupFence,
	failure dispatchFailure,
	retryCtx dagRetryContext,
	strategy nodeexec.OnFailureStrategy,
) bool {
	switch {
	case strategy == nodeexec.OnFailureFailFast:
		d.failSmartRetry(ctx, w, fence, failure, failure.lastErr, true)
	case !smartRetryStrategyImplemented(strategy):
		d.failUnsupportedSmartRetryStrategy(ctx, w, fence, failure, strategy, retryCtx.policy.FailFast)
	case int(w.AttemptCount) >= nodeexec.MaxAttemptsFor(retryCtx.onFailure):
		reason := "max attempts reached: " + failure.lastErr
		d.failSmartRetry(ctx, w, fence, failure, reason, retryCtx.policy.FailFast)
	default:
		return false
	}
	return true
}

func smartRetryStrategyRerunsNode(strategy nodeexec.OnFailureStrategy) bool {
	switch strategy {
	case nodeexec.OnFailureRetry,
		nodeexec.OnFailureEscalateModel,
		nodeexec.OnFailureAppendError:
		return true
	default:
		return false
	}
}

func smartRetryStrategyImplemented(strategy nodeexec.OnFailureStrategy) bool {
	switch strategy {
	case nodeexec.OnFailureRetry,
		nodeexec.OnFailureEscalateModel,
		nodeexec.OnFailureAppendError,
		nodeexec.OnFailureReplan,
		nodeexec.OnFailureFailFast:
		return true
	default:
		return false
	}
}

func canSmartRetry(d *WakeupDispatcher, w *taskdag.Wakeup) bool {
	return d != nil && w != nil &&
		strings.TrimSpace(w.DagKey) != "" &&
		strings.TrimSpace(w.NodeKey) != ""
}

func (d *WakeupDispatcher) dispatchSmartRetryAction(
	ctx context.Context,
	w *taskdag.Wakeup,
	fence wakeupFence,
	failure dispatchFailure,
	retryCtx dagRetryContext,
	strategy nodeexec.OnFailureStrategy,
) bool {
	switch strategy {
	case nodeexec.OnFailureRetry:
		d.retryWakeup(ctx, w, fence, failure)
	case nodeexec.OnFailureEscalateModel:
		d.escalateModelAndRetry(ctx, w, fence, failure, retryCtx.node, retryCtx.onFailure, retryCtx.policy.FailFast)
	case nodeexec.OnFailureAppendError:
		d.appendValidationErrorAndRetry(ctx, w, fence, failure, retryCtx.node, retryCtx.policy.FailFast)
	case nodeexec.OnFailureReplan:
		d.spawnReplanPlanner(ctx, w, fence, failure, retryCtx.policy.FailFast)
	default:
		return false
	}
	return true
}

func (d *WakeupDispatcher) resolveDAGRetryContext(ctx context.Context, dagKey, nodeKey string, runID int64) (dagRetryContext, bool) {
	dag, err := d.store.GetDAG(ctx, dagKey)
	if err != nil || dag == nil {
		return dagRetryContext{}, false
	}
	nodes, err := listDispatcherNodesForRun(ctx, d.store, dagKey, runID)
	if err != nil {
		return dagRetryContext{policy: ResolveRetryPolicy(dag.Metadata, nil)}, true
	}
	target, nodeConfig := findRetryNode(nodes, nodeKey)
	return dagRetryContext{
		policy:    ResolveRetryPolicy(dag.Metadata, nodeConfig),
		node:      target,
		onFailure: nodeOnFailureConfig(target),
	}, true
}

func listDispatcherNodesForRun(ctx context.Context, store taskdag.Store, dagKey string, runID int64) ([]taskdag.Node, error) {
	if runID == 0 {
		return store.ListNodes(ctx, dagKey)
	}
	runReader, ok := any(store).(taskdag.RunNodeReadStore)
	if !ok {
		return nil, fmt.Errorf("store does not implement RunNodeReadStore for run_id=%d", runID)
	}
	return runReader.ListRunNodes(ctx, dagKey, runID)
}

func findRetryNode(nodes []taskdag.Node, nodeKey string) (*taskdag.Node, json.RawMessage) {
	for i := range nodes {
		if nodes[i].NodeKey != nodeKey {
			continue
		}
		node := nodes[i]
		return &node, node.Config
	}
	return nil, nil
}

func nodeOnFailureConfig(node *taskdag.Node) *nodeexec.OnFailureConfig {
	if node == nil {
		return nil
	}
	parsed, err := nodeexec.ParseNodeConfig(resolveNodeType(node.NodeType), node.Config)
	if err != nil || parsed == nil {
		return nil
	}
	switch {
	case parsed.Agent != nil:
		return parsed.Agent.Exec.OnFailure
	case parsed.Automation != nil:
		return parsed.Automation.Exec.OnFailure
	default:
		return nil
	}
}

func (d *WakeupDispatcher) escalateModelAndRetry(
	ctx context.Context,
	w *taskdag.Wakeup,
	fence wakeupFence,
	failure dispatchFailure,
	node *taskdag.Node,
	cfg *nodeexec.OnFailureConfig,
	failFast bool,
) {
	if resolveNodeType(node.NodeType) != "agent" {
		err := fmt.Errorf("escalate_model unsupported for node_type %q", node.NodeType)
		d.failSmartRetryPrepare(ctx, w, fence, failure, err, failFast)
		return
	}
	current := currentAgentModel(node)
	next, ok := nodeexec.EscalationModelFor(cfg, current)
	if !ok {
		reason := "escalation chain exhausted: " + failure.lastErr
		d.failSmartRetry(ctx, w, fence, failure, reason, failFast)
		return
	}
	updated, err := patchAgentExecModel(node.Config, next)
	if err != nil {
		d.failSmartRetryPrepare(ctx, w, fence, failure, err, failFast)
		return
	}
	failure.lastErr = truncateWakeupError(fmt.Sprintf("strategy=escalate_model model=%s: %s", next, failure.lastErr))
	d.retryWakeupWithSmartRetryConfig(ctx, w, fence, failure, node, updated, failFast)
}

func (d *WakeupDispatcher) appendValidationErrorAndRetry(
	ctx context.Context,
	w *taskdag.Wakeup,
	fence wakeupFence,
	failure dispatchFailure,
	node *taskdag.Node,
	failFast bool,
) {
	if resolveNodeType(node.NodeType) != "agent" {
		err := fmt.Errorf("append_error unsupported for node_type %q", node.NodeType)
		d.failSmartRetryPrepare(ctx, w, fence, failure, err, failFast)
		return
	}
	updated, err := appendAgentValidationDiagnostic(node.Config, failure.outcome.ErrorSummary)
	if err != nil {
		d.failSmartRetryPrepare(ctx, w, fence, failure, err, failFast)
		return
	}
	failure.lastErr = truncateWakeupError("strategy=append_error: " + failure.lastErr)
	d.retryWakeupWithSmartRetryConfig(ctx, w, fence, failure, node, updated, failFast)
}

func (d *WakeupDispatcher) retryWakeupWithSmartRetryConfig(
	ctx context.Context,
	w *taskdag.Wakeup,
	fence wakeupFence,
	failure dispatchFailure,
	node *taskdag.Node,
	config json.RawMessage,
	failFast bool,
) {
	if d == nil {
		return
	}
	if d.store == nil || node == nil {
		d.failSmartRetryPrepare(ctx, w, fence, failure, fmt.Errorf("smart retry config patch: missing dispatcher store or node"), failFast)
		return
	}
	patcher, ok := any(d.store).(taskdag.SmartRetryConfigStore)
	if !ok {
		d.failSmartRetryPrepare(ctx, w, fence, failure, fmt.Errorf("smart retry config patch: store missing SmartRetryConfigStore"), failFast)
		return
	}
	rows, err := patcher.RetryWakeupWithNodeConfigPatch(ctx, taskdag.RetryWakeupWithNodeConfigPatchInput{
		RetryWakeup: taskdag.RetryWakeupInput{
			ID:             w.ID,
			RetryInterval:  d.cfg.RetryInterval,
			LastError:      failure.lastErr,
			ClaimedAt:      fence.claimedAt,
			ClaimedBy:      w.ClaimedBy,
			LeaseExpiresAt: fence.leaseAt,
		},
		NodeConfig: taskdag.NodeConfigPatchInput{
			DagKey:         node.DagKey,
			NodeKey:        node.NodeKey,
			RunID:          taskNodeRunID(node),
			PreviousConfig: node.Config,
			Config:         append(json.RawMessage(nil), config...),
		},
	})
	if err != nil {
		d.failSmartRetryPrepare(ctx, w, fence, failure, err, failFast)
		return
	}
	if rows == 0 {
		d.handleRetryHardCap(ctx, w, fence, failure)
		return
	}
	d.recordRetryAccepted(ctx, w, failure)
}

func (d *WakeupDispatcher) failSmartRetryPrepare(ctx context.Context, w *taskdag.Wakeup, fence wakeupFence, failure dispatchFailure, err error, failFast bool) {
	reason := truncateWakeupError("smart retry prepare failed: " + err.Error())
	d.failSmartRetry(ctx, w, fence, failure, reason, failFast)
}

func (d *WakeupDispatcher) failUnsupportedSmartRetryStrategy(
	ctx context.Context,
	w *taskdag.Wakeup,
	fence wakeupFence,
	failure dispatchFailure,
	strategy nodeexec.OnFailureStrategy,
	failFast bool,
) {
	reason := truncateWakeupError(fmt.Sprintf("unsupported smart retry strategy %q: %s", strategy, failure.lastErr))
	d.failSmartRetry(ctx, w, fence, failure, reason, failFast)
}

func (d *WakeupDispatcher) failSmartRetry(ctx context.Context, w *taskdag.Wakeup, fence wakeupFence, failure dispatchFailure, reason string, failFast bool) {
	if !d.markPermanentFail(ctx, w, fence, reason, failure.launchErr) {
		return
	}
	if alert, shouldAlert := recordDispatchRetryMetric(w, failure.lastErr); shouldAlert {
		d.emitDispatchRetryAlert(ctx, alert)
	}
	d.failDAGNodeAndCancelDownstream(ctx, w, reason, failFast, failure.outcome)
}

func (d *WakeupDispatcher) recordRetryAccepted(ctx context.Context, w *taskdag.Wakeup, failure dispatchFailure) {
	if alert, shouldAlert := recordDispatchRetryMetric(w, failure.lastErr); shouldAlert {
		d.emitDispatchRetryAlert(ctx, alert)
	}
	d.logger.Info("wakeup dispatcher: launch transient failure → retry",
		"wakeup_id", w.ID, "target_agent_id", w.TargetAgentID, "retry_interval", d.cfg.RetryInterval, "error", failure.launchErr)
}
