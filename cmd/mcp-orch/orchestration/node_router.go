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
//                   driving CompleteNodeAndScheduleDownstream，因为 automation
//                   节点没有 child agent 在外面推动它）
//   - "hybrid"     → NodeOutcome{Status=failed, FailureClass=validation, 含 "hybrid not implemented"}
//                   （F3.1 落地前的占位）
//   - 空 / 未知    → 兜底当作 "agent"（dogfood DAG 兼容；F1.0 默认 node_type=agent）
//
// NodeExecutorRouter dispatches DAG-driven wakeups to their per-node-type
// NodeExecutor (agent / automation / hybrid). Non-DAG wakeups (no dag_key)
// keep going through the legacy WakeupLauncher.LaunchAgent path.
//
// W2 (sharedfile RunContext + F1.2 inputs) 在 main 分支仍在收敛中，本 worktree
// 的 RunContext 还是 F1.0 三字段版本。router 现阶段只填 DagKey/NodeKey，
// PrevResults / sharedfile reader/writer 字段位待 main 合并后再接通；当前在
// 路由器结构上不预留 nil 字段，避免编译失败。
type NodeExecutorRouter struct {
	store     taskdag.Store
	agentExec *nodeexec.AgentExecutor
	autoExec  *nodeexec.AutomationExecutor
	logger    *slog.Logger
}

// NewNodeExecutorRouter constructs a router. Any of agentExec/autoExec may be
// nil — the corresponding node_type falls back to a validation-class failure.
func NewNodeExecutorRouter(
	store taskdag.Store,
	agentExec *nodeexec.AgentExecutor,
	autoExec *nodeexec.AutomationExecutor,
	logger *slog.Logger,
) *NodeExecutorRouter {
	if logger == nil {
		logger = pkglogger.Get()
	}
	return &NodeExecutorRouter{
		store:     store,
		agentExec: agentExec,
		autoExec:  autoExec,
		logger:    logger,
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
	if r == nil {
		return nodeexec.NodeOutcome{}, errors.New("node router: nil receiver")
	}
	if w == nil {
		return nodeexec.NodeOutcome{}, errors.New("node router: nil wakeup")
	}
	dagKey := strings.TrimSpace(w.DagKey)
	nodeKey := strings.TrimSpace(w.NodeKey)
	if dagKey == "" || nodeKey == "" {
		return nodeexec.NodeOutcome{}, fmt.Errorf("node router: wakeup %d missing dag_key/node_key", w.ID)
	}
	nodes, err := r.store.ListNodes(ctx, dagKey)
	if err != nil {
		return nodeexec.NodeOutcome{}, fmt.Errorf("node router: list nodes %s: %w", dagKey, err)
	}
	var target *taskdag.Node
	for i := range nodes {
		if nodes[i].NodeKey == nodeKey {
			target = &nodes[i]
			break
		}
	}
	if target == nil {
		return nodeexec.NodeOutcome{}, fmt.Errorf("node router: node %s/%s not found", dagKey, nodeKey)
	}

	// node_type 兜底：空字符串 / 未识别 → "agent"（dogfood DAG 兼容，F1.0 默认）
	nodeType := strings.TrimSpace(target.NodeType)
	if nodeType == "" {
		nodeType = "agent"
	}

	runCtx := nodeexec.RunContext{
		DagKey:  dagKey,
		NodeKey: nodeKey,
	}
	execNode := nodeexec.Node{
		DagKey:   dagKey,
		NodeKey:  nodeKey,
		NodeType: nodeType,
		Title:    target.Title,
		Config:   append(json.RawMessage(nil), target.Config...),
	}

	switch nodeType {
	case "agent":
		if r.agentExec == nil {
			return validationOutcome("node router: agent executor not wired"), nil
		}
		return r.agentExec.Execute(ctx, execNode, runCtx)
	case "automation":
		if r.autoExec == nil {
			return validationOutcome("node router: automation executor not wired"), nil
		}
		outcome, execErr := r.autoExec.Execute(ctx, execNode, runCtx)
		if execErr != nil {
			return outcome, execErr
		}
		// Automation 节点没有 child agent 在外面驱动 CompleteNode；
		// 路由器代为推进 node.status / schedule downstream。
		if outcome.Status == nodeexec.NodeStatusDone {
			if err := r.completeAutomationNode(ctx, dagKey, nodeKey, outcome.Result); err != nil {
				r.logger.Warn("node router: automation complete propagate failed",
					"dag_key", dagKey, "node_key", nodeKey, "error", err)
			}
		}
		return outcome, nil
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
