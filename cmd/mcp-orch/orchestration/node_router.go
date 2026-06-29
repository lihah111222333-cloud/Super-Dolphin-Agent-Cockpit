package orchestration

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	orchmetrics "github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/orchestration/metrics"
	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/orchestration/nodeevents"
	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/orchestration/nodeexec"
	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/store/taskdag"
	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	platformdb "github.com/anthropic-ai/super-agent-v3/internal/platform/db"
	"github.com/anthropic-ai/super-agent-v3/internal/util/idgen"
	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
	"github.com/kelindar/event"
)

// NodeExecutorRouter 根据 node_type 派发 DAG wakeup。
// router 负责读节点、构造 RunContext 和必要的状态写回；executor 只执行节点，不处理 wakeup claim/retry/fail。
type NodeExecutorRouter struct {
	store            taskdag.Store
	agentExec        *nodeexec.AgentExecutor
	autoExec         *nodeexec.AutomationExecutor
	sharedFileReader nodeexec.SharedFileReader
	sharedFileWriter nodeexec.SharedFileWriter
	eventBus         *event.Dispatcher
	logger           *slog.Logger
}

var (
	errAgentReadyRunningWriteFailed = errors.New("node router: ready->running write failed")
	errRunExecutionTimeoutFramework = errors.New("node router: execution timeout framework error")
)

// NewNodeExecutorRouter 构造节点执行路由器。
// agentExec/autoExec/sharedFileReader/sharedFileWriter 可为空，但只有节点实际引用缺失能力时才转为节点级失败：
//   - executor nil → validation 失败；
//   - sharedFileReader/Writer nil → 仅在节点 cfg 引用 sharedfile 时 nodeexec 层归
//     validation；纯 inputs.from_nodes 节点不受影响。
func NewNodeExecutorRouter(
	store taskdag.Store,
	agentExec *nodeexec.AgentExecutor,
	autoExec *nodeexec.AutomationExecutor,
	sharedFileReader nodeexec.SharedFileReader,
	sharedFileWriter nodeexec.SharedFileWriter,
	logger *slog.Logger,
) *NodeExecutorRouter {
	if logger == nil {
		logger = pkglogger.Get()
	}
	return &NodeExecutorRouter{
		store:            store,
		agentExec:        agentExec,
		autoExec:         autoExec,
		sharedFileReader: sharedFileReader,
		sharedFileWriter: sharedFileWriter,
		logger:           logger,
	}
}

// WithEventBus 设置节点路由器使用的事件总线。
func (r *NodeExecutorRouter) WithEventBus(bus *event.Dispatcher) *NodeExecutorRouter {
	if r != nil {
		r.eventBus = bus
	}
	return r
}

func (r *NodeExecutorRouter) statusEventBus() *event.Dispatcher {
	if r == nil {
		return nil
	}
	return r.eventBus
}

// spawnReplanPlanner 为重规划节点构造 planner 启动请求。
func (d *WakeupDispatcher) spawnReplanPlanner(ctx context.Context, w *taskdag.Wakeup, fence wakeupFence, failure dispatchFailure, node *taskdag.Node, failFast bool) bool {
	if d == nil {
		return false
	}
	if d.launcher == nil {
		return d.failSmartRetryPrepare(ctx, w, fence, failure, fmt.Errorf("replan planner unavailable"), failFast)
	}
	nodeType, cfgRaw := "", json.RawMessage(nil)
	if node != nil {
		nodeType, cfgRaw = resolveNodeType(node.NodeType), node.Config
	}
	cwd, err := nodeexec.ValidateLaunchCWDForNodeConfig(nodeType, cfgRaw)
	if err != nil {
		return d.failSmartRetryPrepare(ctx, w, fence, failure, fmt.Errorf("replan planner cwd unavailable: %w", err), failFast)
	}
	req := LaunchRequest{
		AgentID:   idgen.NewAgentID(),
		Name:      sanitizeReplanLaunchName(w.DagKey, w.NodeKey),
		AgentKey:  replanPlannerAgentKey,
		AgentType: "agent",
		Cwd:       cwd,
		Prompt:    buildReplanPlannerPrompt(w, failure),
	}
	if err := d.launcher.LaunchAgent(ctx, req); err != nil {
		return d.failSmartRetryPrepare(ctx, w, fence, failure, fmt.Errorf("replan planner launch failed: %w", err), failFast)
	}
	return d.markLaunched(ctx, w, fence)
}

func sanitizeReplanLaunchName(dagKey, nodeKey string) string {
	name := strings.TrimSpace("Replan " + strings.TrimSpace(dagKey) + "/" + strings.TrimSpace(nodeKey))
	if len(name) > 80 {
		return name[:80]
	}
	return name
}

func buildReplanPlannerPrompt(w *taskdag.Wakeup, failure dispatchFailure) string {
	var b strings.Builder
	b.WriteString("A DAG node failed and its on_failure strategy is replan.\n\n")
	fmt.Fprintf(&b, "DAG key: %s\n", strings.TrimSpace(w.DagKey))
	fmt.Fprintf(&b, "Node key: %s\n", strings.TrimSpace(w.NodeKey))
	fmt.Fprintf(&b, "Failure class: %s\n", failure.outcome.FailureClass)
	fmt.Fprintf(&b, "Error: %s\n\n", strings.TrimSpace(failure.outcome.ErrorSummary))
	b.WriteString("Inspect the DAG, decide the smallest graph change, then use task_dag_apply_ops with the current base_version. ")
	b.WriteString("Do not rerun unrelated nodes and keep the patch scoped to recovering this failed node.")
	return b.String()
}

// RouteByWakeup 是 dispatcher 调用入口：根据 wakeup 读取节点、构造 RunContext 并按 node_type 执行。
// 返回 (NodeOutcome, error)：
//   - error != nil 表示框架级失败（store 报错 / 不可恢复的内部错误）；
//   - error == nil 时 outcome.Status 反映节点执行后的态。
//
// 调用方据此决定 MarkWakeupSent / RetryWakeup / FailWakeup，以及 automation 路径是否同步推进节点终态。
func (r *NodeExecutorRouter) RouteByWakeup(ctx context.Context, w *taskdag.Wakeup) (nodeexec.NodeOutcome, error) {
	if err := validateRouteInputs(r, w); err != nil {
		return nodeexec.NodeOutcome{}, err
	}
	dagKey := strings.TrimSpace(w.DagKey)
	nodeKey := strings.TrimSpace(w.NodeKey)
	runID := routeRunID(w)
	target, err := r.lookupTargetNode(ctx, dagKey, nodeKey, runID)
	if err != nil {
		return nodeexec.NodeOutcome{}, err
	}
	if isTerminalNodeStatus(target.Status) {
		return nodeexec.NodeOutcome{Status: nodeexec.NodeStatusDone}, nil
	}
	nodeType := resolveNodeType(target.NodeType)
	if failure := validateAutomationRouteConfig(nodeType, target.Config); failure != nil {
		return *failure, nil
	}
	node := nodeexec.Node{
		DagKey:           dagKey,
		NodeKey:          nodeKey,
		NodeType:         nodeType,
		Title:            target.Title,
		Config:           append(json.RawMessage(nil), target.Config...),
		SpawningThreadID: targetRecordedSpawn(target),
	}
	runCtx, executionTimeout, runCtxErr := r.buildRunContext(ctx, dagKey, nodeKey, runID, nodeType, target.Config)
	if runCtxErr != nil {
		return runCtxErr.outcome, runCtxErr.frameworkErr
	}
	return r.dispatchByNodeType(ctx, nodeType, node, runCtx, executionTimeout, w.ID, w.AttemptCount, target.Status)
}

// validateAutomationRouteConfig 在 executor 前拦截不可执行的 automation command 配置。
// 返回 NodeOutcome 指针表示节点级 validation 失败，避免把用户配置错误升级成 dispatcher 框架错误。
func validateAutomationRouteConfig(nodeType string, raw json.RawMessage) *nodeexec.NodeOutcome {
	if nodeType != "automation" {
		return nil
	}
	validationErr := nodeexec.ValidateAutomationCommandDispatchConfig(raw)
	if validationErr == nil {
		return nil
	}
	outcome := validationOutcome("node router: automation command config invalid: " + validationErr.Error())
	return &outcome
}

// runContextBuildErr 是 buildRunContext 的多路返值载体：
//   - frameworkErr != nil → 框架级错误（store 报错），走 RouteByWakeup error 返值，
//     让 dispatcher 走 transient retry；
//   - outcome != zero → 节点级失败（config 解析不了等），走 RouteByWakeup outcome 返值。
type runContextBuildErr struct {
	frameworkErr error
	outcome      nodeexec.NodeOutcome
}

// buildRunContext 预拼 RunContext：拍【调度上下文 ID】+ 【PrevResults 预取】+
// 【SharedFileReader/Writer 注入】。失败路径拆为两种：
//   - parse config 失败 → 节点级 validation outcome（不调 executor）；
//   - prefetch 遇 store 报错 → framework err。
func (r *NodeExecutorRouter) buildRunContext(
	ctx context.Context,
	dagKey, nodeKey string,
	runID int64,
	nodeType string,
	cfgRaw json.RawMessage,
) (nodeexec.RunContext, time.Duration, *runContextBuildErr) {
	fromNodes, parseErr := extractFromNodes(nodeType, cfgRaw)
	if parseErr != nil {
		return nodeexec.RunContext{}, 0, &runContextBuildErr{
			outcome: validationOutcome(fmt.Sprintf(
				"node router: parse %s config for prefetch failed: %v", nodeType, parseErr)),
		}
	}
	prevResults, fetchErr := r.prefetchPrevResults(ctx, dagKey, runID, fromNodes)
	if fetchErr != nil {
		return nodeexec.RunContext{}, 0, &runContextBuildErr{frameworkErr: fetchErr}
	}
	timeout, timeoutErr := r.resolveRunExecutionTimeout(ctx, dagKey, cfgRaw)
	if timeoutErr != nil {
		if errors.Is(timeoutErr, errRunExecutionTimeoutFramework) {
			return nodeexec.RunContext{}, 0, &runContextBuildErr{frameworkErr: timeoutErr}
		}
		return nodeexec.RunContext{}, 0, &runContextBuildErr{
			outcome: validationOutcome("node router: execution timeout invalid: " + timeoutErr.Error()),
		}
	}
	return nodeexec.RunContext{
		DagKey:           dagKey,
		NodeKey:          nodeKey,
		RunID:            runID,
		PrevResults:      prevResults,
		SharedFileReader: r.sharedFileReader,
		SharedFileWriter: r.sharedFileWriter,
	}, timeout, nil
}

func (r *NodeExecutorRouter) resolveRunExecutionTimeout(ctx context.Context, dagKey string, cfgRaw json.RawMessage) (time.Duration, error) {
	if r.store == nil {
		return 0, fmt.Errorf("%w: store nil", errRunExecutionTimeoutFramework)
	}
	dag, err := r.store.GetDAG(ctx, dagKey)
	if err != nil {
		return 0, fmt.Errorf("%w: load dag %s metadata: %w", errRunExecutionTimeoutFramework, dagKey, err)
	}
	var metadata json.RawMessage
	if dag != nil {
		metadata = dag.Metadata
	}
	timeout, timeoutErr := nodeexec.ResolveExecutionTimeout(metadata, cfgRaw)
	if timeoutErr != nil {
		return 0, timeoutErr
	}
	return timeout, nil
}

// extractFromNodes 拆出 cfg.Inputs.FromNodes。三种 nodeType 走 ParseNodeConfig
// 拿 Inputs 子配置；未知 nodeType 返空列表（dispatchByNodeType 会继续走 validation
// 失败分支，此处不需接管）。
func extractFromNodes(nodeType string, cfgRaw json.RawMessage) ([]string, error) {
	if len(cfgRaw) == 0 {
		return nil, nil
	}
	parsed, err := nodeexec.ParseNodeConfig(nodeType, cfgRaw)
	if err != nil {
		if errors.Is(err, nodeexec.ErrUnknownNodeType) {
			return nil, err
		}
		return nil, err
	}
	switch {
	case parsed.Agent != nil:
		return parsed.Agent.Inputs.FromNodes, nil
	case parsed.Automation != nil:
		return parsed.Automation.Inputs.FromNodes, nil
	case parsed.Hybrid != nil:
		return parsed.Hybrid.Inputs.FromNodes, nil
	default:
		return nil, nil
	}
}

// prefetchPrevResults 查 store 拿 cfg.Inputs.FromNodes 列出的上游节点 result。
//
// 范围规则：
//   - fromNodes 空 → 返 nil map（RunContext.PrevResults nil），nodeexec.inputs.go 是
//     以「InputsConfig 为空才皆跳」规则走，本层 nil/empty 都不会被误读。
//   - store ListNodes 报错 → 原样返错（framework err）。
//   - fromNodes 里某个 key 不存在于当前 DAG → 不在本层报错，交由 nodeexec
//     loadFromNodes（它会报 validation "references unknown node_key"）。
//   - 上游节点 status != done → 同上路径 （未到期：依赖未满足时 dispatcher 不
//     会 enqueue wakeup），但为保险起见过滤 ：只填 done 状态的节点 result；
//     未 done 的 key 不入 map → nodeexec 那边会报 validation，规则一致。
//   - result 为空（NULL）→ 填 empty json，让 nodeexec.loadFromNodes 走 "(empty)"
//     占位分支（上游可能未配 outputs.to_node_result）。
func (r *NodeExecutorRouter) prefetchPrevResults(
	ctx context.Context,
	dagKey string,
	runID int64,
	fromNodes []string,
) (map[string]json.RawMessage, error) {
	if len(fromNodes) == 0 {
		return nil, nil
	}
	want := make(map[string]struct{}, len(fromNodes))
	for _, k := range fromNodes {
		if key := strings.TrimSpace(k); key != "" {
			want[key] = struct{}{}
		}
	}
	if len(want) == 0 {
		return nil, nil
	}
	nodes, err := r.listRouteNodes(ctx, dagKey, runID)
	if err != nil {
		return nil, fmt.Errorf("node router: prefetch prev results list nodes %s: %w", dagKey, err)
	}
	prev := make(map[string]json.RawMessage, len(want))
	for i := range nodes {
		n := &nodes[i]
		if _, ok := want[n.NodeKey]; !ok {
			continue
		}
		if n.Status != string(nodeexec.NodeStatusDone) {
			// 未到期：dispatcher 不应在依赖未满足时 enqueue wakeup。本层不填，
			// nodeexec.loadFromNodes 会以 "unknown node_key" 报 validation，保证失败
			// 被 fail-loud 看到。
			continue
		}
		if len(n.Result) == 0 {
			prev[n.NodeKey] = nil // nodeexec 走 "(empty)" 分支
			continue
		}
		prev[n.NodeKey] = append(json.RawMessage(nil), n.Result...)
	}
	return prev, nil
}

// validateRouteInputs 拆出 RouteByWakeup 的入参防御，压住 Cyclomatic Complexity。
func validateRouteInputs(r *NodeExecutorRouter, w *taskdag.Wakeup) error {
	if r == nil {
		return errors.New("node router: nil receiver")
	}
	if w == nil {
		return errors.New("node router: nil wakeup")
	}
	if strings.TrimSpace(w.DagKey) == "" || strings.TrimSpace(w.NodeKey) == "" {
		return fmt.Errorf("node router: wakeup %d missing dag_key/node_key", w.ID)
	}
	if w.RunID == nil || *w.RunID <= 0 {
		return fmt.Errorf("node router: wakeup %d missing run_id for runtime node dispatch", w.ID)
	}
	return nil
}

// lookupTargetNode 走 store.ListNodes/ListRunNodes 拿节点列表、按 node_key 定位。
// 拆出独立函数为了让 RouteByWakeup 主高还清，压住 CC。
func (r *NodeExecutorRouter) lookupTargetNode(ctx context.Context, dagKey, nodeKey string, runID int64) (*taskdag.Node, error) {
	nodes, err := r.listRouteNodes(ctx, dagKey, runID)
	if err != nil {
		return nil, fmt.Errorf("node router: list nodes %s: %w", dagKey, err)
	}
	for i := range nodes {
		if nodes[i].NodeKey == nodeKey {
			return &nodes[i], nil
		}
	}
	return nil, fmt.Errorf("node router: node %s/%s not found", dagKey, nodeKey)
}

func (r *NodeExecutorRouter) listRouteNodes(ctx context.Context, dagKey string, runID int64) ([]taskdag.Node, error) {
	if runID <= 0 {
		return nil, fmt.Errorf("run_id required for runtime node dispatch")
	}
	runReader, ok := any(r.store).(taskdag.RunNodeReadStore)
	if !ok {
		return nil, fmt.Errorf("store does not implement RunNodeReadStore for run_id=%d", runID)
	}
	return runReader.ListRunNodes(ctx, dagKey, runID)
}

// DAG wakeup 必须带 run_id；没有就直接失败。
// 不要退回模板节点读取。
func routeRunID(w *taskdag.Wakeup) int64 {
	if w == nil || w.RunID == nil {
		return 0
	}
	return *w.RunID
}

func taskNodeRunID(node *taskdag.Node) int64 {
	if node == nil || node.RunID == nil {
		return 0
	}
	return *node.RunID
}

// resolveNodeType 归一化 node_type。
// 空串按 agent 处理以兼容旧 DAG；未知值保留原样，由 dispatchByNodeType 返回 validation failure。
func resolveNodeType(raw string) string {
	t := strings.TrimSpace(raw)
	if t == "" {
		return "agent"
	}
	return t
}

// dispatchByNodeType 是 node_type 到 executor 的唯一生产路由表。
// 拆出独立函数以保持 RouteByWakeup 的状态读取和执行派发边界清晰。
//
// wakeupID 是本轮 dispatcher 绑定的 wakeup row id，仅 dispatchAgent 用于
// ready→running 推进；其他路径不需要。
func (r *NodeExecutorRouter) dispatchByNodeType(ctx context.Context, nodeType string, node nodeexec.Node, runCtx nodeexec.RunContext, executionTimeout time.Duration, wakeupID int64, wakeupAttempt int32, oldStatus string) (nodeexec.NodeOutcome, error) {
	switch nodeType {
	case "agent":
		return r.dispatchAgent(ctx, node, runCtx, wakeupID, oldStatus)
	case "automation":
		return r.dispatchAutomation(ctx, node, runCtx, executionTimeout, wakeupID, wakeupAttempt, oldStatus)
	case "hybrid":
		return nodeexec.NodeOutcome{
			Status:       nodeexec.NodeStatusFailed,
			FailureClass: nodeexec.FailureClassValidation,
			ErrorSummary: "node router: hybrid node lifecycle not yet implemented (F3.1)",
		}, nil
	default:
		return validationOutcome(fmt.Sprintf("node router: unsupported node_type %q", nodeType)), nil
	}
}

// dispatchAgent 只负责启动 child agent 并把节点推进到 running。
// child agent 的 done/failed 由 TurnCompleted subscriber 推进；如果 subscriber 已先写终态，running 写会被视为幂等 race。
func (r *NodeExecutorRouter) dispatchAgent(ctx context.Context, node nodeexec.Node, runCtx nodeexec.RunContext, wakeupID int64, oldStatus string) (nodeexec.NodeOutcome, error) {
	var hooks map[nodeexec.HookPoint]nodeexec.HookHandler
	if r.agentExec != nil {
		hooks = r.agentExec.Hooks()
	}
	if strings.TrimSpace(node.SpawningThreadID) != "" {
		return r.dispatchRecordedAgentSpawn(ctx, hooks, node, runCtx, wakeupID, oldStatus)
	}
	if r.agentExec == nil {
		return validationOutcome("node router: agent executor not wired"), nil
	}
	if !r.agentExec.HasSpawnRecorder() {
		return nodeexec.NodeOutcome{
			Status:       nodeexec.NodeStatusFailed,
			FailureClass: nodeexec.FailureClassHard,
			ErrorSummary: "node router: agent spawn recorder not wired",
		}, nil
	}
	outcome, err := r.executeNodeWithLifecycleHooks(ctx, hooks, r.agentExec, node, runCtx)
	if err != nil || outcome.Status == nodeexec.NodeStatusFailed {
		// launch 失败 / executor 框架错：不写 running；dispatcher 会走 retry / fail 路径。
		return outcome, err
	}
	// launch 成功：推 ready→running，让 subscriber 后续推 done/failed。
	advanced, advanceErr := r.advanceAgentNodeToRunning(ctx, node.DagKey, node.NodeKey, runCtx.RunID, wakeupID, oldStatus)
	if advanceErr != nil {
		return outcome, advanceErr
	}
	if advanced {
		r.invokeLifecycleHook(ctx, hooks, nodeexec.HookOnStateChange, node, nodeexec.NodeOutcome{
			Status: nodeexec.NodeStatusRunning,
		})
	}
	return outcome, nil
}

// 已有 spawning_thread_id 说明 agent 已经启动过。
// 这种 retry/reclaim 只补状态，不要再启动一个 child agent。
func targetRecordedSpawn(target *taskdag.Node) string {
	if target == nil || target.SpawningThreadID == nil {
		return ""
	}
	return strings.TrimSpace(*target.SpawningThreadID)
}

// launch 成功但 wakeup 还没标 sent 时，恢复会走这里。
// child thread 已经落库，只重放状态推进，别重复 launch。
func (r *NodeExecutorRouter) dispatchRecordedAgentSpawn(
	ctx context.Context,
	hooks map[nodeexec.HookPoint]nodeexec.HookHandler,
	node nodeexec.Node,
	runCtx nodeexec.RunContext,
	wakeupID int64,
	oldStatus string,
) (nodeexec.NodeOutcome, error) {
	outcome := nodeexec.NodeOutcome{Status: nodeexec.NodeStatusDone}
	advanced, advanceErr := r.advanceAgentNodeToRunning(ctx, node.DagKey, node.NodeKey, runCtx.RunID, wakeupID, oldStatus)
	if advanceErr != nil {
		return outcome, advanceErr
	}
	if advanced {
		r.invokeLifecycleHook(ctx, hooks, nodeexec.HookOnStateChange, node, nodeexec.NodeOutcome{
			Status: nodeexec.NodeStatusRunning,
		})
	}
	return outcome, nil
}

// advanceAgentNodeToRunning 调 UpdateRunningNodeStatus（SQL 白名单 IN ('pending','ready')）
// 推 ready→running。除已被 subscriber 推到终态的 race 外，写入失败必须返回
// framework error 让 dispatcher 重试，不能只记日志后把 wakeup 标成功。
func (r *NodeExecutorRouter) advanceAgentNodeToRunning(ctx context.Context, dagKey, nodeKey string, runID int64, wakeupID int64, oldStatus string) (bool, error) {
	if r.store == nil {
		err := errors.New("node router: store nil for ready->running write")
		r.logger.Warn("node router: store nil, ready->running write failed",
			"dag_key", dagKey, "node_key", nodeKey)
		return false, err
	}
	runStore, ok := any(r.store).(taskdag.RunningNodeStore)
	if !ok {
		err := errors.New("node router: store does not implement RunningNodeStore")
		r.logger.Warn("node router: ready->running write unsupported",
			"dag_key", dagKey, "node_key", nodeKey)
		return false, err
	}
	node, updateErr := runStore.UpdateRunningNodeStatus(ctx, taskdag.RunningNodeStatusUpdate{
		Status:   "running",
		Result:   json.RawMessage(`{}`),
		WakeupID: wakeupID,
		DagKey:   dagKey,
		NodeKey:  nodeKey,
		RunID:    runID,
	})
	switch {
	case updateErr == nil:
		nodeevents.Publish(r.eventBus, oldStatus, node)
		orchmetrics.IncDispatchAgentRunningWritten()
		return true, nil
	case errors.Is(updateErr, sql.ErrNoRows) || platformdb.IsNotFound(updateErr):
		// race window D：subscriber 已推终态，不在白名单 IN ('pending','ready')。
		orchmetrics.IncDispatchAgentRunningSkippedAlreadyTerminal()
		r.logger.Debug("node router: ready->running skipped, node already terminal",
			"dag_key", dagKey, "node_key", nodeKey)
		return false, nil
	default:
		orchmetrics.IncDispatchAgentRunningWriteFailed()
		r.logger.Warn("node router: ready->running write failed",
			"dag_key", dagKey, "node_key", nodeKey, "error", updateErr)
		return false, fmt.Errorf("%w: %w", errAgentReadyRunningWriteFailed, updateErr)
	}
}

// dispatchAutomation 执行 automation 节点并在同一 wakeup attempt 内推进完成态。
// ready->running 和完成写回都必须带 fence，避免过期 wakeup 或重复调度覆盖新状态。
func (r *NodeExecutorRouter) dispatchAutomation(ctx context.Context, node nodeexec.Node, runCtx nodeexec.RunContext, executionTimeout time.Duration, wakeupID int64, wakeupAttempt int32, oldStatus string) (nodeexec.NodeOutcome, error) {
	if r.autoExec == nil {
		return validationOutcome("node router: automation executor not wired"), nil
	}
	hooks := r.autoExec.Hooks()
	advanced, advanceErr := r.advanceAgentNodeToRunning(ctx, node.DagKey, node.NodeKey, runCtx.RunID, wakeupID, oldStatus)
	if advanceErr != nil {
		return nodeexec.NodeOutcome{}, advanceErr
	}
	if !advanced {
		return nodeexec.NodeOutcome{}, fmt.Errorf("node router: automation ready->running fence missed for %s/%s/%d", node.DagKey, node.NodeKey, runCtx.RunID)
	}
	execCtx := nodeexec.WithExecutionTimeout(ctx, executionTimeout)
	outcome, execErr := r.executeNodeWithLifecycleHooks(execCtx, hooks, r.autoExec, node, runCtx)
	if execErr != nil {
		return outcome, execErr
	}
	// Automation 节点没有 child agent 在外面驱动 CompleteNode；路由器代为推进。
	if outcome.Status == nodeexec.NodeStatusDone {
		if err := r.completeAutomationNode(ctx, node.DagKey, node.NodeKey, runCtx.RunID, wakeupID, wakeupAttempt, outcome.Result, oldStatus); err != nil {
			r.logger.Warn("node router: automation complete propagate failed",
				"dag_key", node.DagKey, "node_key", node.NodeKey, "error", err)
			return outcome, fmt.Errorf("node router: automation complete propagate failed: %w", err)
		} else {
			r.invokeLifecycleHook(ctx, hooks, nodeexec.HookOnStateChange, node, nodeexec.NodeOutcome{
				Status: nodeexec.NodeStatusDone,
				Result: outcome.Result,
			})
		}
	}
	return outcome, nil
}

// completeAutomationNode 在 automation 节点 Execute 成功后同步推进 status=done
// + 调度下游。失败必须作为 framework error 返回，让 dispatcher 重试，避免
// wakeup 被标记 sent 后隐藏持久化失败。
func (r *NodeExecutorRouter) completeAutomationNode(ctx context.Context, dagKey, nodeKey string, runID int64, wakeupID int64, wakeupAttempt int32, result json.RawMessage, oldStatus string) error {
	if r.store == nil {
		return errors.New("node router: store nil, cannot complete automation node")
	}
	// CompleteNodeAndScheduleDownstream 通过 NodeFlowStore 类型断言；
	// taskdag.Store 编译期保证嵌入。
	flow, ok := any(r.store).(taskdag.NodeFlowStore)
	if !ok {
		return errors.New("node router: store does not implement NodeFlowStore")
	}
	resBytes := result
	if len(resBytes) == 0 {
		resBytes = json.RawMessage(`{}`)
	}
	res, err := flow.CompleteNodeAndScheduleDownstream(ctx, taskdag.CompleteNodeInput{
		Status:        "done",
		Result:        resBytes,
		DagKey:        dagKey,
		NodeKey:       nodeKey,
		RunID:         runID,
		WakeupID:      wakeupID,
		WakeupAttempt: wakeupAttempt,
	})
	if err != nil {
		return err
	}
	nodeevents.PublishComplete(r.eventBus, oldStatus, res)
	return nil
}

func validationOutcome(summary string) nodeexec.NodeOutcome {
	return nodeexec.NodeOutcome{
		Status:       nodeexec.NodeStatusFailed,
		FailureClass: nodeexec.FailureClassValidation,
		ErrorSummary: summary,
	}
}

type serviceAgentLauncher struct {
	svc *service
}

// NewServiceAgentLauncher 创建通过 thread service 启动 DAG agent 的适配器。
func NewServiceAgentLauncher(svc *service) nodeexec.AgentLauncher {
	return &serviceAgentLauncher{svc: svc}
}

// LaunchAgent 启动代理线程并返回 provider thread_id。
func (a *serviceAgentLauncher) LaunchAgent(ctx context.Context, req contract.LaunchRequest) (string, error) {
	return a.LaunchAgentWithSpawnRecord(ctx, req, nil)
}

// LaunchAgentWithSpawnRecord 启动代理线程并保留 spawn 记录。
func (a *serviceAgentLauncher) LaunchAgentWithSpawnRecord(ctx context.Context, req contract.LaunchRequest, record func(threadID string) error) (string, error) {
	if a == nil || a.svc == nil {
		return "", errors.New("service agent launcher: nil receiver")
	}
	var launchedThreadID string
	snap, err := a.svc.launchAgentSnapshot(ctx, req, func(_ string, result LaunchResult) error {
		launchedThreadID = strings.TrimSpace(result.ThreadID)
		if record == nil {
			return nil
		}
		return record(launchedThreadID)
	})
	if err != nil {
		return "", err
	}
	if threadID := strings.TrimSpace(snap.ThreadID); threadID != "" {
		return threadID, nil
	}
	return launchedThreadID, nil
}

// ValidateDAGAgentLaunch 校验 DAG agent 是否具备远端 launcher 写回能力。
// 本地 launcher 无法返回稳定 thread_id，必须在启动前 fail-fast。
func (a *serviceAgentLauncher) ValidateDAGAgentLaunch(_ context.Context, _ contract.LaunchRequest, dagKey, nodeKey string) error {
	if a == nil || a.svc == nil {
		return errors.New("service agent launcher: nil receiver")
	}
	if a.svc.launcher != nil {
		if _, ok := a.svc.launcher.(*localLauncher); !ok {
			return nil
		}
	}
	return fmt.Errorf("%w: dag_key=%s node_key=%s; local/standalone launcher does not provide the DAG agent command/thread_id/spawning_thread_id write-back contract; fix: start mcp-orch with the desktop remote launcher RPC address before running DAG agent nodes",
		nodeexec.ErrDAGAgentRequiresRemoteLauncher, strings.TrimSpace(dagKey), strings.TrimSpace(nodeKey))
}

// StopLaunchedThread 停止由 DAG 启动的线程。
func (a *serviceAgentLauncher) StopLaunchedThread(ctx context.Context, threadID string) error {
	if a == nil || a.svc == nil {
		return errors.New("service agent launcher: nil receiver")
	}
	result, err := StopSpawnedAgent(ctx, a.svc.agentThreads, a.svc, threadID)
	if terminateStopResultError(result, err) {
		if err == nil {
			err = fmt.Errorf("spawned agent stop result %s", result)
		}
		return err
	}
	return nil
}

// ProvideAgentExecutor 为 fx 提供带 spawn recorder 和 lifecycle hooks 的 agent executor。
func ProvideAgentExecutor(launcher nodeexec.AgentLauncher, recorder nodeexec.NodeSpawnRecorder, hooks NodeLifecycleHooks) (*nodeexec.AgentExecutor, error) {
	if recorder == nil {
		return nil, errors.New("node router: agent executor requires node spawn recorder")
	}
	return nodeexec.NewAgentExecutor(launcher, nodeexec.WithRecorder(recorder), nodeexec.WithHooks(map[nodeexec.HookPoint]nodeexec.HookHandler(hooks))), nil
}
