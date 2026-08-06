package orchestration

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/orchestration/nodeevents"
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/orchestration/nodeexec"
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/orchestration/retrypolicy"
	taskdag "github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/taskdag"
)

// dispatchFailure 汇总一次 wakeup 调度失败的错误文本、原始错误和节点执行结果。
type dispatchFailure struct {
	lastErr   string
	launchErr error
	outcome   nodeexec.NodeOutcome
}

// failedWakeupOutcome 为非 DAG 启动路径失败合成节点失败 outcome。
func failedWakeupOutcome(summary string) nodeexec.NodeOutcome {
	return nodeexec.NodeOutcome{Status: nodeexec.NodeStatusFailed, ErrorSummary: summary}
}

// failureClassPermanent 判断 FailureClass 是否应直接终止，不再自动重试。
func failureClassPermanent(class nodeexec.FailureClass) bool {
	switch class {
	case nodeexec.FailureClassHard,
		nodeexec.FailureClassNeedsHuman:
		return true
	}
	return false
}

// failureOutcomePermanent 判断完整执行结果是否属于永久失败。
// validation 中的 launch agent 前缀是启动瞬态错误，仍允许 retry。
func failureOutcomePermanent(outcome nodeexec.NodeOutcome) bool {
	if failureClassPermanent(outcome.FailureClass) {
		return true
	}
	if outcome.FailureClass == nodeexec.FailureClassValidation {
		return !strings.HasPrefix(outcome.ErrorSummary, "launch agent:")
	}
	return false
}

// nonRetryableValidationFailure 判断 validation 失败是否不能由重试修复。
func nonRetryableValidationFailure(outcome nodeexec.NodeOutcome) bool {
	return outcome.FailureClass == nodeexec.FailureClassValidation && !strings.HasPrefix(outcome.ErrorSummary, "launch agent:")
}

// DAG wakeup 最终失败时，wakeup 和节点要一起写失败。
// 不要拆开写，否则崩溃后可能只失败了 wakeup，节点还在跑。
func (d *WakeupDispatcher) markPermanentDAGFailure(ctx context.Context, w *taskdag.Wakeup, fence wakeupFence, lastErr string, launchErr error, failFast bool, outcome nodeexec.NodeOutcome) bool {
	runID := routeRunID(w)
	if runID <= 0 {
		d.logger.Warn("wakeup dispatcher: skip DAG node failure without run_id", "wakeup_id", w.ID, "dag_key", w.DagKey, "node_key", w.NodeKey)
		return false
	}
	atomicStore, ok := d.store.(taskdag.WakeupNodeFailureStore)
	if !ok {
		d.logger.Warn("wakeup dispatcher: store missing atomic DAG wakeup failure path", "wakeup_id", w.ID, "dag_key", w.DagKey, "node_key", w.NodeKey)
		return false
	}
	rows, res, err := atomicStore.FailWakeupAndFailNodeAndCancelDownstream(ctx, taskdag.FailWakeupInput{
		ID:             w.ID,
		LastError:      lastErr,
		ClaimedAt:      fence.claimedAt,
		ClaimedBy:      w.ClaimedBy,
		LeaseExpiresAt: fence.leaseAt,
	}, taskdag.FailNodeInput{DagKey: w.DagKey, NodeKey: w.NodeKey, RunID: runID, Reason: lastErr, FailFast: failFast})
	if err != nil {
		d.logger.Warn("wakeup dispatcher: permanent DAG failure transaction failed", "wakeup_id", w.ID, "dag_key", w.DagKey, "node_key", w.NodeKey, "error", err)
		return false
	}
	if rows == 0 {
		d.logger.Warn("wakeup dispatcher: fail-wakeup fence missed", "wakeup_id", w.ID, "target_agent_id", w.TargetAgentID)
		return false
	}
	d.recordDispatchFailedMetric()
	d.logger.Warn("wakeup dispatcher: DAG wakeup and node failed", "wakeup_id", w.ID, "target_agent_id", w.TargetAgentID, "error", launchErr)
	d.publishDAGNodeFailure(ctx, w, lastErr, failFast, outcome, res)
	return true
}

// 只有 DB 写成功后才发事件和 hook。
// hook 失败不能把已经终态的节点拉回 retry。
func (d *WakeupDispatcher) publishDAGNodeFailure(ctx context.Context, w *taskdag.Wakeup, lastErr string, failFast bool, outcome nodeexec.NodeOutcome, res *taskdag.FailNodeResult) {
	d.logger.Warn("wakeup dispatcher: DAG node failed", "wakeup_id", w.ID, "dag_key", w.DagKey, "node_key", w.NodeKey, "attempt_count", w.AttemptCount, "fail_fast", failFast, "canceled_downstream", len(res.CanceledDownstream))
	if d.nodeRouter != nil {
		nodeevents.PublishFail(d.nodeRouter.statusEventBus(), "", res)
		if outcome.Status == "" {
			outcome.Status = nodeexec.NodeStatusFailed
		}
		if outcome.ErrorSummary == "" {
			outcome.ErrorSummary = lastErr
		}
		d.nodeRouter.invokeTerminalFailureHooksForWakeup(ctx, w, outcome)
	}
}

// handleRetryHardCap 在 SQL 或策略层达到重试上限时将 wakeup 转为终态失败。
func (d *WakeupDispatcher) handleRetryHardCap(ctx context.Context, w *taskdag.Wakeup, fence wakeupFence, failure dispatchFailure) bool {
	exhaustedErr := "retry attempts exhausted: " + failure.lastErr
	handled := false
	if isDAGWakeup(w) {
		failFast, resolvedErr := d.retryPolicyFailFast(ctx, w, exhaustedErr, "hard-cap failure")
		handled = d.markPermanentDAGFailure(ctx, w, fence, resolvedErr, failure.launchErr, failFast, failure.outcome)
	} else {
		handled = d.markPermanentFail(ctx, w, fence, exhaustedErr, failure.launchErr)
	}
	if handled {
		if alert, shouldAlert := d.recordDispatchRetryMetric(w, failure.lastErr); shouldAlert {
			d.emitDispatchRetryAlert(ctx, alert)
		}
	}
	d.logger.Warn("wakeup dispatcher: retry attempts exhausted → failed",
		"wakeup_id", w.ID,
		"target_agent_id", w.TargetAgentID)
	return handled
}

// executor 已经返回失败 outcome 时走这里。
// 按 FailureClass 和 on_failure 决定重试、智能修复，还是直接失败。
func (d *WakeupDispatcher) handleFailedRouterOutcome(ctx context.Context, w *taskdag.Wakeup, fence wakeupFence, outcome nodeexec.NodeOutcome) bool {
	synthErr := fmt.Errorf("%s: %s", outcome.FailureClass, outcome.ErrorSummary)
	lastErr := truncateWakeupError(synthErr.Error())
	failure := dispatchFailure{lastErr: lastErr, launchErr: synthErr, outcome: outcome}
	if nonRetryableValidationFailure(outcome) {
		return d.recordPermanentRouterFailure(ctx, w, fence, lastErr, synthErr, outcome)
	}
	if tried, ok := d.trySmartDAGRetry(ctx, w, fence, failure); tried {
		return ok
	}
	if !failureOutcomePermanent(outcome) {
		return d.markTransientRetry(ctx, w, fence, failure)
	}
	return d.recordPermanentRouterFailure(ctx, w, fence, lastErr, synthErr, outcome)
}

// recordPermanentRouterFailure 记录 node executor 返回的永久失败，并按策略处理 fail-fast。
func (d *WakeupDispatcher) recordPermanentRouterFailure(ctx context.Context, w *taskdag.Wakeup, fence wakeupFence, lastErr string, launchErr error, outcome nodeexec.NodeOutcome) bool {
	if w.AttemptCount >= 3 {
		if alert, shouldAlert := d.recordDispatchRetryMetric(w, lastErr); shouldAlert {
			d.emitDispatchRetryAlert(ctx, alert)
		}
	}
	failFast, lastErr := d.retryPolicyFailFast(ctx, w, lastErr, "permanent router failure")
	return d.markPermanentDAGFailure(ctx, w, fence, lastErr, launchErr, failFast, outcome)
}

// replanPlannerAgentKey 是 replan 策略使用的规划 agent key。
const replanPlannerAgentKey = "dag_designer"

// dagRetryContext 聚合一次 smart retry 决策需要的 DAG、节点和 on_failure 配置。
type dagRetryContext struct {
	policy    retrypolicy.RetryPolicy
	node      *taskdag.Node
	onFailure *nodeexec.OnFailureConfig
}

// retryPolicyFailFast 解析当前 DAG/node 的 fail-fast 策略；解析失败时保守写入错误文本。
func (d *WakeupDispatcher) retryPolicyFailFast(ctx context.Context, w *taskdag.Wakeup, lastErr, action string) (bool, string) {
	policy, ok, err := d.resolveDAGRetryPolicy(ctx, w.DagKey, w.NodeKey, routeRunID(w))
	if err == nil {
		return ok && policy.FailFast, lastErr
	}
	d.logger.Warn("wakeup dispatcher: retry policy invalid during "+action,
		"wakeup_id", w.ID, "dag_key", w.DagKey, "node_key", w.NodeKey, "error", err)
	return false, truncateWakeupError("retry policy invalid: " + err.Error() + ": " + lastErr)
}

// 这里只判断 on_failure 是否要接管。
// 真正写 retry/fail 仍交给后面的 store 路径，别在这里直接改 DB。
func (d *WakeupDispatcher) trySmartDAGRetry(ctx context.Context, w *taskdag.Wakeup, fence wakeupFence, failure dispatchFailure) (bool, bool) {
	if !canSmartRetry(d, w) {
		return false, false
	}
	retryCtx, ok, err := d.resolveDAGRetryContext(ctx, w.DagKey, w.NodeKey, routeRunID(w))
	if err != nil {
		return true, d.failSmartRetryPrepare(ctx, w, fence, failure, err, false)
	}
	if !ok || retryCtx.node == nil || retryCtx.onFailure == nil {
		return false, false
	}
	strategy, ok := smartRetryStrategyFor(retryCtx.onFailure, failure.outcome.FailureClass)
	if !ok {
		return false, false
	}
	preflight := strategy == nodeexec.OnFailureFailFast ||
		!smartRetryStrategyImplemented(strategy) ||
		int(w.AttemptCount) >= nodeexec.MaxAttemptsFor(retryCtx.onFailure)
	if preflight {
		return true, d.handleSmartRetryPreflight(ctx, w, fence, failure, retryCtx, strategy)
	}
	return true, d.dispatchSmartRetryAction(ctx, w, fence, failure, retryCtx, strategy)
}

// smartRetryStrategyFor 根据 FailureClass 解析可执行的 on_failure 策略。
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

// smartRetryStrategyResolution 记录策略来源，区分默认策略和按 FailureClass 覆盖。
type smartRetryStrategyResolution struct {
	strategy   nodeexec.OnFailureStrategy
	configured bool
	byClass    bool
}

// configuredOnFailureStrategy 解析节点失败后的重试或终止策略。
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

// permanentClassStrategyAllowed 限制永久失败只能走显式允许且不会重新运行节点的策略。
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

// handleSmartRetryPreflight 在执行 smart retry 前拦截 fail_fast、未实现策略和次数上限。
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
		return d.failSmartRetry(ctx, w, fence, failure, failure.lastErr, true)
	case !smartRetryStrategyImplemented(strategy):
		return d.failUnsupportedSmartRetryStrategy(ctx, w, fence, failure, strategy, retryCtx.policy.FailFast)
	case int(w.AttemptCount) >= nodeexec.MaxAttemptsFor(retryCtx.onFailure):
		reason := "max attempts reached: " + failure.lastErr
		return d.failSmartRetry(ctx, w, fence, failure, reason, retryCtx.policy.FailFast)
	default:
		return false
	}
}

// smartRetryStrategyRerunsNode 判断策略是否会再次执行原节点。
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

// smartRetryStrategyImplemented 判断策略是否已有 dispatcher 实现。
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

// canSmartRetry 判断当前 wakeup 是否具备 DAG/node 维度，非 DAG wakeup 不走 smart retry。
func canSmartRetry(d *WakeupDispatcher, w *taskdag.Wakeup) bool {
	return d != nil && w != nil &&
		strings.TrimSpace(w.DagKey) != "" &&
		strings.TrimSpace(w.NodeKey) != ""
}

// dispatchSmartRetryAction 根据策略执行 retry、模型升级、追加错误或 replan。
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
		return d.retryWakeup(ctx, w, fence, failure)
	case nodeexec.OnFailureEscalateModel:
		return d.escalateModelAndRetry(ctx, w, fence, failure, retryCtx.node, retryCtx.onFailure, retryCtx.policy.FailFast)
	case nodeexec.OnFailureAppendError:
		return d.appendValidationErrorAndRetry(ctx, w, fence, failure, retryCtx.node, retryCtx.policy.FailFast)
	case nodeexec.OnFailureReplan:
		return d.spawnReplanPlanner(ctx, w, fence, failure, retryCtx.node, retryCtx.policy.FailFast)
	default:
		return false
	}
}

// resolveDAGRetryContext 读取 DAG 重试所需的节点、运行和唤醒上下文。
func (d *WakeupDispatcher) resolveDAGRetryContext(ctx context.Context, dagKey, nodeKey string, runID int64) (dagRetryContext, bool, error) {
	if runID <= 0 {
		return dagRetryContext{}, false, nil
	}
	dag, err := d.store.GetDAG(ctx, dagKey)
	if err != nil {
		return dagRetryContext{}, false, fmt.Errorf("resolve retry policy dag %s: %w", dagKey, err)
	}
	if dag == nil {
		return dagRetryContext{}, false, fmt.Errorf("resolve retry policy dag %s: not found", dagKey)
	}
	nodes, err := listDispatcherNodesForRun(ctx, d.store, dagKey, runID)
	if err != nil {
		return dagRetryContext{}, false, fmt.Errorf("list run nodes for retry policy dag %s run_id=%d: %w", dagKey, runID, err)
	}
	target, nodeConfig := findRetryNode(nodes, nodeKey)
	policy, policyErr := retrypolicy.ResolveRetryPolicy(dag.Metadata, nodeConfig)
	if policyErr != nil {
		return dagRetryContext{}, false, policyErr
	}
	return dagRetryContext{
		policy:    policy,
		node:      target,
		onFailure: nodeOnFailureConfig(target),
	}, true, nil
}

// listDispatcherNodesForRun 读取当前 run 的 runtime node，避免误用 DAG 模板节点。
func listDispatcherNodesForRun(ctx context.Context, store taskdag.WakeupDispatchStore, dagKey string, runID int64) ([]taskdag.Node, error) {
	if runID <= 0 {
		return nil, fmt.Errorf("run_id required for dispatcher node lookup")
	}
	runReader, ok := any(store).(taskdag.RunNodeReadStore)
	if !ok {
		return nil, fmt.Errorf("store does not implement RunNodeReadStore for run_id=%d", runID)
	}
	return runReader.ListRunNodes(ctx, dagKey, runID)
}

// findRetryNode 在 runtime 节点集合中找到当前失败节点，并返回它的原始配置。
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

// nodeOnFailureConfig 从 agent/automation 节点配置中提取 on_failure 策略。
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

// escalateModelAndRetry 将 agent 节点的模型提升到下一档后原子 patch config 并重试。
func (d *WakeupDispatcher) escalateModelAndRetry(
	ctx context.Context,
	w *taskdag.Wakeup,
	fence wakeupFence,
	failure dispatchFailure,
	node *taskdag.Node,
	cfg *nodeexec.OnFailureConfig,
	failFast bool,
) bool {
	if resolveNodeType(node.NodeType) != "agent" {
		err := fmt.Errorf("escalate_model unsupported for node_type %q", node.NodeType)
		return d.failSmartRetryPrepare(ctx, w, fence, failure, err, failFast)
	}
	current := currentAgentModel(node)
	next, ok := nodeexec.EscalationModelFor(cfg, current)
	if !ok {
		reason := "escalation chain exhausted: " + failure.lastErr
		return d.failSmartRetry(ctx, w, fence, failure, reason, failFast)
	}
	updated, err := patchAgentExecModel(node.Config, next)
	if err != nil {
		return d.failSmartRetryPrepare(ctx, w, fence, failure, err, failFast)
	}
	failure.lastErr = truncateWakeupError(fmt.Sprintf("strategy=escalate_model model=%s: %s", next, failure.lastErr))
	return d.retryWakeupWithSmartRetryConfig(ctx, w, fence, failure, node, updated, failFast)
}

// appendValidationErrorAndRetry 把 validation 错误追加进 agent 节点配置后重试。
func (d *WakeupDispatcher) appendValidationErrorAndRetry(
	ctx context.Context,
	w *taskdag.Wakeup,
	fence wakeupFence,
	failure dispatchFailure,
	node *taskdag.Node,
	failFast bool,
) bool {
	if resolveNodeType(node.NodeType) != "agent" {
		err := fmt.Errorf("append_error unsupported for node_type %q", node.NodeType)
		return d.failSmartRetryPrepare(ctx, w, fence, failure, err, failFast)
	}
	updated, err := appendAgentValidationDiagnostic(node.Config, failure.outcome.ErrorSummary)
	if err != nil {
		return d.failSmartRetryPrepare(ctx, w, fence, failure, err, failFast)
	}
	failure.lastErr = truncateWakeupError("strategy=append_error: " + failure.lastErr)
	return d.retryWakeupWithSmartRetryConfig(ctx, w, fence, failure, node, updated, failFast)
}

// retryWakeupWithSmartRetryConfig 按智能重试配置重新投递唤醒任务。
func (d *WakeupDispatcher) retryWakeupWithSmartRetryConfig(
	ctx context.Context,
	w *taskdag.Wakeup,
	fence wakeupFence,
	failure dispatchFailure,
	node *taskdag.Node,
	config json.RawMessage,
	failFast bool,
) bool {
	// wakeup retry 和 node.config patch 必须一起成功。
	// 任一步失败都要显式失败，别让下一轮用旧配置重跑。
	if d == nil {
		return false
	}
	if d.store == nil || node == nil {
		return d.failSmartRetryPrepare(ctx, w, fence, failure, fmt.Errorf("smart retry config patch: missing dispatcher store or node"), failFast)
	}
	patcher, ok := any(d.store).(taskdag.SmartRetryConfigStore)
	if !ok {
		return d.failSmartRetryPrepare(ctx, w, fence, failure, fmt.Errorf("smart retry config patch: store missing SmartRetryConfigStore"), failFast)
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
		return d.failSmartRetryPrepare(ctx, w, fence, failure, err, failFast)
	}
	if rows == 0 {
		return d.handleRetryHardCap(ctx, w, fence, failure)
	}
	d.recordRetryAccepted(ctx, w, failure)
	return true
}

// failSmartRetryPrepare 将 smart retry 准备阶段错误转换为 DAG 节点终态失败。
func (d *WakeupDispatcher) failSmartRetryPrepare(ctx context.Context, w *taskdag.Wakeup, fence wakeupFence, failure dispatchFailure, err error, failFast bool) bool {
	reason := truncateWakeupError("smart retry prepare failed: " + err.Error())
	return d.failSmartRetry(ctx, w, fence, failure, reason, failFast)
}

// failUnsupportedSmartRetryStrategy 将未知 on_failure 策略显式失败，避免静默回退到普通 retry。
func (d *WakeupDispatcher) failUnsupportedSmartRetryStrategy(
	ctx context.Context,
	w *taskdag.Wakeup,
	fence wakeupFence,
	failure dispatchFailure,
	strategy nodeexec.OnFailureStrategy,
	failFast bool,
) bool {
	reason := truncateWakeupError(fmt.Sprintf("unsupported smart retry strategy %q: %s", strategy, failure.lastErr))
	return d.failSmartRetry(ctx, w, fence, failure, reason, failFast)
}

// failSmartRetry 按 smart retry 策略将 DAG wakeup 和节点一起写入失败。
func (d *WakeupDispatcher) failSmartRetry(ctx context.Context, w *taskdag.Wakeup, fence wakeupFence, failure dispatchFailure, reason string, failFast bool) bool {
	if !d.markPermanentDAGFailure(ctx, w, fence, reason, failure.launchErr, failFast, failure.outcome) {
		return false
	}
	if alert, shouldAlert := d.recordDispatchRetryMetric(w, failure.lastErr); shouldAlert {
		d.emitDispatchRetryAlert(ctx, alert)
	}
	return true
}

// recordRetryAccepted 记录 retry 已接受，并在达到告警条件时异步发出告警。
func (d *WakeupDispatcher) recordRetryAccepted(ctx context.Context, w *taskdag.Wakeup, failure dispatchFailure) {
	if alert, shouldAlert := d.recordDispatchRetryMetric(w, failure.lastErr); shouldAlert {
		d.emitDispatchRetryAlert(ctx, alert)
	}
	d.logger.Info("wakeup dispatcher: launch transient failure → retry",
		"wakeup_id", w.ID, "target_agent_id", w.TargetAgentID, "retry_interval", d.cfg.RetryInterval, "error", failure.launchErr)
}
