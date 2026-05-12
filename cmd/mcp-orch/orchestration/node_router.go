package orchestration

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/orchestration/nodeexec"
	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/store/taskdag"
	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
)

// NodeExecutorRouter 把 DAG-driven wakeup 按 node_type 派发到对应 NodeExecutor。
// 与 wakeup_dispatcher.go 的直接 service.LaunchAgent 路径并存：非 DAG wakeup
// (dag_key/node_key 空) 走旧路径，DAG wakeup 走本路由器。
//
// node_type 分发：
//   - "agent"      → nodeexec.AgentExecutor.Execute
//   - "automation" → nodeexec.AutomationExecutor.Execute（并在 Status=done 后
//     driving CompleteNodeAndScheduleDownstream，因为 automation
//     节点没有 child agent 在外面推动它）
//   - "hybrid"     → NodeOutcome{Status=failed, FailureClass=validation, 含 "hybrid not implemented"}
//     （F3.1 落地前的占位）
//   - 空 / 未知    → 兜底当作 "agent"（dogfood DAG 兼容；F1.0 默认 node_type=agent）
//
// NodeExecutorRouter dispatches DAG-driven wakeups to their per-node-type
// NodeExecutor (agent / automation / hybrid). Non-DAG wakeups (no dag_key)
// keep going through the legacy WakeupLauncher.LaunchAgent path.
//
// W2 (sharedfile RunContext + F1.2 inputs) 端口在 main HEAD 6e32b39e 已落地：
// RunContext 含 PrevResults / SharedFileReader / SharedFileWriter 三端口。
// router 在派发前要预填这三个端口；否则 dogfood-grade DAG (cfg.Inputs.FromNodes /
// from_sharedfiles / outputs.to_sharedfile) 走 dispatcher 路径会 fail-loud 在
// validation "PrevResults not wired" / "SharedFileReader not wired" 上。
//
// dispatcher-wiring closure：sharedFileReader / sharedFileWriter 由 fx 注入
// sharedfile_adapter.go 中的两个 adapter；prevResults 走 prefetchPrevResults
// 自 store.ListNodes 拉。
type NodeExecutorRouter struct {
	store            taskdag.Store
	agentExec        *nodeexec.AgentExecutor
	autoExec         *nodeexec.AutomationExecutor
	sharedFileReader nodeexec.SharedFileReader
	sharedFileWriter nodeexec.SharedFileWriter
	logger           *slog.Logger
}

// NewNodeExecutorRouter constructs a router. Any of agentExec/autoExec/
// sharedFileReader/sharedFileWriter may be nil — node_type-specific 失败语义：
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

// RouteByWakeup 是 dispatcher 调用入口：根据 wakeup 拿到的 dag/node 信息读取
// 节点 row、构造 RunContext、按 node_type 派发执行。返回 (NodeOutcome, error)：
//   - error != nil 表示框架级失败（store 报错 / 不可恢复的内部错误）；
//   - error == nil 时 outcome.Status 反映节点执行后的态。
//
// 调用方（dispatcher）据此决定 MarkWakeupSent / RetryWakeup / FailWakeup，
// 以及 (automation 路径) 是否需要同步推进节点 status 到终态。
//
// RouteByWakeup is the dispatcher entrypoint. It fetches the node row,
// builds a RunContext, and dispatches by node_type. Returned error signals a
// framework-level fault; NodeOutcome carries node-level success/failure.
func (r *NodeExecutorRouter) RouteByWakeup(ctx context.Context, w *taskdag.Wakeup) (nodeexec.NodeOutcome, error) {
	if err := validateRouteInputs(r, w); err != nil {
		return nodeexec.NodeOutcome{}, err
	}
	dagKey := strings.TrimSpace(w.DagKey)
	nodeKey := strings.TrimSpace(w.NodeKey)
	target, err := r.lookupTargetNode(ctx, dagKey, nodeKey)
	if err != nil {
		return nodeexec.NodeOutcome{}, err
	}
	nodeType := resolveNodeType(target.NodeType)
	node := nodeexec.Node{
		DagKey:   dagKey,
		NodeKey:  nodeKey,
		NodeType: nodeType,
		Title:    target.Title,
		Config:   append(json.RawMessage(nil), target.Config...),
	}
	runCtx, runCtxErr := r.buildRunContext(ctx, dagKey, nodeKey, nodeType, target.Config)
	if runCtxErr != nil {
		return runCtxErr.outcome, runCtxErr.frameworkErr
	}
	return r.dispatchByNodeType(ctx, nodeType, node, runCtx)
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
	dagKey, nodeKey, nodeType string,
	cfgRaw json.RawMessage,
) (nodeexec.RunContext, *runContextBuildErr) {
	fromNodes, parseErr := extractFromNodes(nodeType, cfgRaw)
	if parseErr != nil {
		return nodeexec.RunContext{}, &runContextBuildErr{
			outcome: validationOutcome(fmt.Sprintf(
				"node router: parse %s config for prefetch failed: %v", nodeType, parseErr)),
		}
	}
	prevResults, fetchErr := r.prefetchPrevResults(ctx, dagKey, fromNodes)
	if fetchErr != nil {
		return nodeexec.RunContext{}, &runContextBuildErr{frameworkErr: fetchErr}
	}
	return nodeexec.RunContext{
		DagKey:           dagKey,
		NodeKey:          nodeKey,
		PrevResults:      prevResults,
		SharedFileReader: r.sharedFileReader,
		SharedFileWriter: r.sharedFileWriter,
	}, nil
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
			// 未知 nodeType 不是 prefetch 职责；让 dispatchByNodeType 去报。
			return nil, nil
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
// 边界语义：
//   - fromNodes 空 → 返 nil map（RunContext.PrevResults nil），nodeexec.inputs.go 是
//     以「InputsConfig 为空才皆跳」语义走，本层 nil/empty 都不会被误读。
//   - store ListNodes 报错 → 原样返错（framework err）。
//   - fromNodes 里某个 key 不存在于当前 DAG → 不在本层报错，交由 nodeexec
//     loadFromNodes（它会报 validation "references unknown node_key"）。
//   - 上游节点 status != done → 同上路径 （未到期：依赖未满足时 dispatcher 不
//     会 enqueue wakeup），但为保险起见过滤 ：只填 done 状态的节点 result；
//     未 done 的 key 不入 map → nodeexec 那边会报 validation，语义一致。
//   - result 为空（NULL）→ 填 empty json，让 nodeexec.loadFromNodes 走 "(empty)"
//     占位分支（上游可能未配 outputs.to_node_result）。
func (r *NodeExecutorRouter) prefetchPrevResults(
	ctx context.Context,
	dagKey string,
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
	nodes, err := r.store.ListNodes(ctx, dagKey)
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
	return nil
}

// lookupTargetNode 走 store.ListNodes 拿节点列表、按 node_key 定位。
// 拆出独立函数为了让 RouteByWakeup 主高还清，压住 CC。
func (r *NodeExecutorRouter) lookupTargetNode(ctx context.Context, dagKey, nodeKey string) (*taskdag.Node, error) {
	nodes, err := r.store.ListNodes(ctx, dagKey)
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

// resolveNodeType 处理 nodeType 兜底逻辑：空串/未识别 → "agent" (F1.0 默认)。
func resolveNodeType(raw string) string {
	t := strings.TrimSpace(raw)
	if t == "" {
		return "agent"
	}
	return t
}

// dispatchByNodeType 是本文件里别的「真」路由表：按 node_type 调对应 executor。
// 拆出独立函数以满足 code-size guard 的 CC 阈值。
func (r *NodeExecutorRouter) dispatchByNodeType(ctx context.Context, nodeType string, node nodeexec.Node, runCtx nodeexec.RunContext) (nodeexec.NodeOutcome, error) {
	switch nodeType {
	case "agent":
		return r.dispatchAgent(ctx, node, runCtx)
	case "automation":
		return r.dispatchAutomation(ctx, node, runCtx)
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

func (r *NodeExecutorRouter) dispatchAgent(ctx context.Context, node nodeexec.Node, runCtx nodeexec.RunContext) (nodeexec.NodeOutcome, error) {
	if r.agentExec == nil {
		return validationOutcome("node router: agent executor not wired"), nil
	}
	return r.agentExec.Execute(ctx, node, runCtx)
}

func (r *NodeExecutorRouter) dispatchAutomation(ctx context.Context, node nodeexec.Node, runCtx nodeexec.RunContext) (nodeexec.NodeOutcome, error) {
	if r.autoExec == nil {
		return validationOutcome("node router: automation executor not wired"), nil
	}
	outcome, execErr := r.autoExec.Execute(ctx, node, runCtx)
	if execErr != nil {
		return outcome, execErr
	}
	// Automation 节点没有 child agent 在外面驱动 CompleteNode；路由器代为推进。
	if outcome.Status == nodeexec.NodeStatusDone {
		if err := r.completeAutomationNode(ctx, node.DagKey, node.NodeKey, outcome.Result); err != nil {
			r.logger.Warn("node router: automation complete propagate failed",
				"dag_key", node.DagKey, "node_key", node.NodeKey, "error", err)
		}
	}
	return outcome, nil
}

// completeAutomationNode 在 automation 节点 Execute 成功后同步推进 status=done
// + 调度下游。失败仅 logWarn 不阻塞主流（dispatcher 仍会 MarkWakeupSent，
// 后续 reclaim cron / 重试可补救）。
func (r *NodeExecutorRouter) completeAutomationNode(ctx context.Context, dagKey, nodeKey string, result json.RawMessage) error {
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
	if _, err := flow.CompleteNodeAndScheduleDownstream(ctx, taskdag.CompleteNodeInput{
		Status:  "done",
		Result:  resBytes,
		DagKey:  dagKey,
		NodeKey: nodeKey,
	}); err != nil {
		return err
	}
	return nil
}

func validationOutcome(summary string) nodeexec.NodeOutcome {
	return nodeexec.NodeOutcome{
		Status:       nodeexec.NodeStatusFailed,
		FailureClass: nodeexec.FailureClassValidation,
		ErrorSummary: summary,
	}
}

// serviceAgentLauncher 把 service.LaunchAgentSnapshot 适配成
// nodeexec.AgentLauncher 接口 (返 threadID + error)。
//
// serviceAgentLauncher adapts service.LaunchAgentSnapshot to satisfy
// nodeexec.AgentLauncher (returns thread_id + error).
type serviceAgentLauncher struct {
	svc *service
}

// NewServiceAgentLauncher exposes the adapter to the fx layer.
func NewServiceAgentLauncher(svc *service) nodeexec.AgentLauncher {
	return &serviceAgentLauncher{svc: svc}
}

func (a *serviceAgentLauncher) LaunchAgent(ctx context.Context, req contract.LaunchRequest) (string, error) {
	if a == nil || a.svc == nil {
		return "", errors.New("service agent launcher: nil receiver")
	}
	snap, err := a.svc.LaunchAgentSnapshot(ctx, req)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(snap.ThreadID), nil
}

// storeNodeSpawnRecorderAdapter 把 store/taskdag.NodeSpawnRecorderStore 的宽接口
// 适配成 nodeexec.NodeSpawnRecorder 的窄接口。生产 binding 是同一 *store；
// W1 (F1.5) 已经把 store 实现 NodeSpawnRecorderStore 编译期断言进去，本适配器
// 只做接口面收窄 + 入参重排。
type storeNodeSpawnRecorderAdapter struct {
	store taskdag.NodeSpawnRecorderStore
}

// NewStoreNodeSpawnRecorder exposes the adapter to the fx layer.
func NewStoreNodeSpawnRecorder(store taskdag.NodeSpawnRecorderStore) nodeexec.NodeSpawnRecorder {
	if store == nil {
		return nil
	}
	return &storeNodeSpawnRecorderAdapter{store: store}
}

func (a *storeNodeSpawnRecorderAdapter) RecordNodeSpawn(ctx context.Context, dagKey, nodeKey, threadID string) error {
	if a == nil || a.store == nil {
		return errors.New("store node spawn recorder: nil receiver")
	}
	_, err := a.store.RecordNodeSpawn(ctx, taskdag.RecordNodeSpawnInput{
		DagKey:   dagKey,
		NodeKey:  nodeKey,
		ThreadID: threadID,
	})
	return err
}

// ProvideAgentExecutor 是 fx 用的 wiring 适配器：消费已经被 fx 容器装好的
// AgentLauncher + NodeSpawnRecorder，按 W2 端口收敛后的 functional options
// 形式构造 AgentExecutor。
//
// round-3 合并 follow-up：W1 worker 在落 fx wiring 时使用的是 W2 端口收敛
// 之前的 NewAgentExecutor(launcher, recorder) 旧签名（直接把 NewAgentExecutor
// 当 provider 用）；W2 把它折叠为 NewAgentExecutor(launcher, opts ...Option) +
// WithRecorder(NodeSpawnRecorder) 之后，旧 wiring 只会注入 launcher，recorder
// 被静默丢弃 → F1.5 spawning_thread_id 写回链在 dispatcher 路径上失效。本
// provider 显式串接两者，确保 recorder 真正落到 executor.
//
// ProvideAgentExecutor wires the AgentLauncher + NodeSpawnRecorder into an
// *AgentExecutor with WithRecorder() so the F1.5 write-back stays active
// after W2's functional-options refactor.
func ProvideAgentExecutor(launcher nodeexec.AgentLauncher, recorder nodeexec.NodeSpawnRecorder) *nodeexec.AgentExecutor {
	return nodeexec.NewAgentExecutor(launcher, nodeexec.WithRecorder(recorder))
}
