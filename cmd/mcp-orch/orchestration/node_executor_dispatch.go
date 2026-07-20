package orchestration

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/orchestration/nodeexec"
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/orchestration/sharedfilemeta"
	sharedfilestore "github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/sharedfile"
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/taskdag"
	platformconfig "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/config"
	platformdb "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/db"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/runtimesafe"
	pkglogger "github.com/lihah111222333-cloud/super-dolphin-agent/pkg/logger"
)

const (
	lifecycleHookDispatchWait     = 100 * time.Millisecond
	lifecycleHookExecutionTimeout = time.Second
)

// NodeLifecycleHooks 是注入 node executor 的生产 hook 集合。
// 命名类型让 fx 能区分生产 hook map 和测试临时 map；hook 只能观察/通知，不能决定节点状态。
type NodeLifecycleHooks map[nodeexec.HookPoint]nodeexec.HookHandler

type storeSharedFileReaderAdapter struct {
	store sharedfilestore.Reader
}

// NewStoreSharedFileReader 把 sharedfile reader 适配为节点执行器读取端口。
func NewStoreSharedFileReader(store sharedfilestore.Reader) nodeexec.SharedFileReader {
	if store == nil {
		return nil
	}
	return &storeSharedFileReaderAdapter{store: store}
}

// ReadSharedFile 把不存在的共享文件映射为 exists=false，其余 store 错误保持可见。
func (a *storeSharedFileReaderAdapter) ReadSharedFile(ctx context.Context, path string) (string, bool, error) {
	if a == nil || a.store == nil {
		return "", false, errors.New("store sharedfile reader: nil receiver")
	}
	file, err := a.store.Get(ctx, path)
	if err != nil {
		if errors.Is(err, platformdb.ErrNotFound) {
			return "", false, nil
		}
		return "", false, fmt.Errorf("store sharedfile reader: get %q: %w", path, err)
	}
	if file == nil {
		return "", false, nil
	}
	return file.Content, true, nil
}

type storeSharedFileWriterAdapter struct {
	store sharedfilestore.Store
	*sharedfilemeta.StoreWriter
}

const sharedFileWriterUpdatedBy = "node-router"

// NewStoreSharedFileWriter 把 sharedfile store 适配为节点执行器写入端口。
func NewStoreSharedFileWriter(store sharedfilestore.Store) nodeexec.SharedFileWriter {
	if store == nil {
		return nil
	}
	return &storeSharedFileWriterAdapter{store: store, StoreWriter: sharedfilemeta.NewStoreWriter(store)}
}

// WriteSharedFile 以节点执行器稳定身份写入共享文件。
func (a *storeSharedFileWriterAdapter) WriteSharedFile(ctx context.Context, path, content string) error {
	if a == nil || a.store == nil {
		return errors.New("store sharedfile writer: nil receiver")
	}
	if _, err := a.store.Upsert(ctx, sharedfilestore.UpsertParams{Path: path, Content: content, UpdatedBy: sharedFileWriterUpdatedBy}); err != nil {
		return fmt.Errorf("store sharedfile writer: upsert %q: %w", path, err)
	}
	return nil
}

type loggingNodeLifecycleHook struct {
	logger *slog.Logger
}

// ProvideNodeLifecycleHooks 提供默认的节点 lifecycle 日志 hook 集合。
func ProvideNodeLifecycleHooks(logger *slog.Logger) NodeLifecycleHooks {
	handler := loggingNodeLifecycleHook{logger: logger}
	return NodeLifecycleHooks{
		nodeexec.HookBeforeExecute: handler,
		nodeexec.HookAfterExecute:  handler,
		nodeexec.HookOnStateChange: handler,
		nodeexec.HookOnFailure:     handler,
	}
}

// ProvideAutomationExecutor 为 fx 构造带 lifecycle hooks 的 automation executor。
func ProvideAutomationExecutor(
	getter nodeexec.AutomationCommandGetter,
	runner nodeexec.AutomationCommandRunner,
	hooks NodeLifecycleHooks,
) *nodeexec.AutomationExecutor {
	return nodeexec.NewAutomationExecutor(getter, runner, nodeexec.WithAutomationHooks(map[nodeexec.HookPoint]nodeexec.HookHandler(hooks)))
}

// Handle 记录节点生命周期事件。
// hook 失败不会影响节点状态，因此当前实现只做诊断日志。
func (h loggingNodeLifecycleHook) Handle(_ context.Context, point nodeexec.HookPoint, node nodeexec.Node, outcome nodeexec.NodeOutcome) error {
	logger := h.logger
	if logger == nil {
		logger = pkglogger.Get()
	}
	logger.Debug("node lifecycle hook",
		"hook_point", point,
		"dag_key", node.DagKey,
		"node_key", node.NodeKey,
		"node_type", node.NodeType,
		"status", outcome.Status,
		"failure_class", outcome.FailureClass)
	return nil
}

func (r *NodeExecutorRouter) executeNodeWithLifecycleHooks(
	ctx context.Context,
	hooks map[nodeexec.HookPoint]nodeexec.HookHandler,
	exec nodeexec.NodeExecutor,
	node nodeexec.Node,
	runCtx nodeexec.RunContext,
) (nodeexec.NodeOutcome, error) {
	r.invokeLifecycleHook(ctx, hooks, nodeexec.HookBeforeExecute, node, nodeexec.NodeOutcome{})
	outcome, err := exec.Execute(ctx, node, runCtx)
	r.invokeLifecycleHook(ctx, hooks, nodeexec.HookAfterExecute, node, outcome)
	return outcome, err
}

// invokeLifecycleHook 调用节点生命周期 hook 并保留诊断信息。
func (r *NodeExecutorRouter) invokeLifecycleHook(
	ctx context.Context,
	hooks map[nodeexec.HookPoint]nodeexec.HookHandler,
	point nodeexec.HookPoint,
	node nodeexec.Node,
	outcome nodeexec.NodeOutcome,
) {
	// hook 等一小段时间就放到后台跑，别让通知卡住 dispatcher。
	handler := hooks[point]
	if handler == nil {
		return
	}
	hookCtx := context.Background()
	if ctx != nil {
		hookCtx = context.WithoutCancel(ctx)
	}
	runCtx, cancel := platformconfig.WithTimeout(hookCtx, lifecycleHookExecutionTimeout)
	done := make(chan struct{})
	runtimesafe.SafeGo(runCtx, lifecycleLogger(r), "nodeExecutor.lifecycleHook", func(runCtx context.Context) {
		defer cancel()
		defer close(done)
		if err := handler.Handle(runCtx, point, node, outcome); err != nil {
			lifecycleLogger(r).Warn("node router: lifecycle hook failed",
				"hook_point", point,
				"dag_key", node.DagKey,
				"node_key", node.NodeKey,
				"status", outcome.Status,
				"error", err)
		}
	})
	timer := time.NewTimer(lifecycleHookDispatchWait)
	defer timer.Stop()
	select {
	case <-done:
	case <-timer.C:
		lifecycleLogger(r).Warn("node router: lifecycle hook still running asynchronously",
			"hook_point", point,
			"dag_key", node.DagKey,
			"node_key", node.NodeKey,
			"status", outcome.Status)
	}
}

// invokeTerminalFailureHooksForWakeup 为唤醒失败节点触发终态失败 hook。
func (r *NodeExecutorRouter) invokeTerminalFailureHooksForWakeup(ctx context.Context, w *taskdag.Wakeup, outcome nodeexec.NodeOutcome) {
	if r == nil || w == nil {
		return
	}
	dagKey := strings.TrimSpace(w.DagKey)
	nodeKey := strings.TrimSpace(w.NodeKey)
	if dagKey == "" || nodeKey == "" {
		return
	}
	target, err := r.lookupTargetNode(ctx, dagKey, nodeKey, routeRunID(w))
	if err != nil {
		lifecycleLogger(r).Warn("node router: lookup failed for terminal failure hook",
			"dag_key", dagKey, "node_key", nodeKey, "error", err)
		return
	}
	r.invokeTerminalFailureHooksForTaskNode(ctx, target, outcome)
}

func (r *NodeExecutorRouter) invokeTerminalFailureHooksForTaskNode(ctx context.Context, target *taskdag.Node, outcome nodeexec.NodeOutcome) {
	if outcome.Status == "" {
		outcome.Status = nodeexec.NodeStatusFailed
	}
	r.invokeStateChangeHooksForTaskNode(ctx, target, outcome)
	r.invokeFailureHookForTaskNode(ctx, target, outcome)
}

func (r *NodeExecutorRouter) invokeStateChangeHooksForTaskNode(ctx context.Context, target *taskdag.Node, outcome nodeexec.NodeOutcome) {
	if target == nil {
		return
	}
	nodeType := resolveNodeType(target.NodeType)
	exec := r.executorForNodeType(nodeType)
	if exec == nil {
		return
	}
	hooks := exec.Hooks()
	node := nodeFromTaskNode(target, nodeType)
	r.invokeLifecycleHook(ctx, hooks, nodeexec.HookOnStateChange, node, outcome)
}

func (r *NodeExecutorRouter) invokeFailureHookForTaskNode(ctx context.Context, target *taskdag.Node, outcome nodeexec.NodeOutcome) {
	if target == nil {
		return
	}
	nodeType := resolveNodeType(target.NodeType)
	exec := r.executorForNodeType(nodeType)
	if exec == nil {
		return
	}
	hooks := exec.Hooks()
	node := nodeFromTaskNode(target, nodeType)
	r.invokeLifecycleHook(ctx, hooks, nodeexec.HookOnFailure, node, outcome)
}

func currentAgentModel(node *taskdag.Node) string {
	if node == nil {
		return ""
	}
	cfg, err := nodeexec.ParseAgentConfig(node.Config)
	if err != nil || cfg == nil {
		return ""
	}
	return strings.TrimSpace(cfg.Exec.Model)
}

func patchAgentExecModel(raw json.RawMessage, model string) (json.RawMessage, error) {
	root, err := rawJSONObject(raw)
	if err != nil {
		return nil, err
	}
	execObj, err := nestedJSONObject(root, "exec")
	if err != nil {
		return nil, err
	}
	modelBytes, err := json.Marshal(strings.TrimSpace(model))
	if err != nil {
		return nil, err
	}
	execObj["model"] = modelBytes
	execBytes, err := json.Marshal(execObj)
	if err != nil {
		return nil, err
	}
	root["exec"] = execBytes
	return json.Marshal(root)
}

// appendAgentValidationDiagnostic 把上一次 validation 错误追加到 agent first_turn。
// 用于 retry/diagnostic 路径，避免覆盖用户原始提示。
func appendAgentValidationDiagnostic(raw json.RawMessage, summary string) (json.RawMessage, error) {
	root, err := rawJSONObject(raw)
	if err != nil {
		return nil, err
	}
	firstTurn := ""
	if rawFirst, ok := root["first_turn"]; ok && len(rawFirst) > 0 {
		if err := json.Unmarshal(rawFirst, &firstTurn); err != nil {
			return nil, fmt.Errorf("parse first_turn: %w", err)
		}
	}
	diagnostic := "Previous validation error:\n" + strings.TrimSpace(summary)
	if strings.TrimSpace(firstTurn) != "" {
		firstTurn = strings.TrimSpace(firstTurn) + "\n\n" + diagnostic
	} else {
		firstTurn = diagnostic
	}
	firstBytes, err := json.Marshal(firstTurn)
	if err != nil {
		return nil, err
	}
	root["first_turn"] = firstBytes
	return json.Marshal(root)
}

func rawJSONObject(raw json.RawMessage) (map[string]json.RawMessage, error) {
	if len(raw) == 0 {
		return map[string]json.RawMessage{}, nil
	}
	var root map[string]json.RawMessage
	if err := json.Unmarshal(raw, &root); err != nil {
		return nil, fmt.Errorf("parse node config object: %w", err)
	}
	if root == nil {
		root = map[string]json.RawMessage{}
	}
	return root, nil
}

func nestedJSONObject(root map[string]json.RawMessage, key string) (map[string]json.RawMessage, error) {
	var obj map[string]json.RawMessage
	if raw, ok := root[key]; ok && len(raw) > 0 {
		if err := json.Unmarshal(raw, &obj); err != nil {
			return nil, fmt.Errorf("parse node config %s object: %w", key, err)
		}
	}
	if obj == nil {
		obj = map[string]json.RawMessage{}
	}
	return obj, nil
}

// executorForNodeType 根据节点类型选择对应执行器。
func (r *NodeExecutorRouter) executorForNodeType(nodeType string) nodeexec.NodeExecutor {
	if r == nil {
		return nil
	}
	switch nodeType {
	case "agent":
		if r.agentExec == nil {
			return nil
		}
		return r.agentExec
	case "automation":
		if r.autoExec == nil {
			return nil
		}
		return r.autoExec
	default:
		return nil
	}
}

func lifecycleLogger(r *NodeExecutorRouter) *slog.Logger {
	if r != nil && r.logger != nil {
		return r.logger
	}
	return pkglogger.Get()
}

func nodeFromTaskNode(target *taskdag.Node, nodeType string) nodeexec.Node {
	return nodeexec.Node{
		DagKey:   target.DagKey,
		NodeKey:  target.NodeKey,
		NodeType: nodeType,
		Title:    target.Title,
		Config:   append(json.RawMessage(nil), target.Config...),
	}
}

func isDAGWakeup(w *taskdag.Wakeup) bool {
	return w != nil &&
		strings.TrimSpace(w.DagKey) != "" && strings.TrimSpace(w.NodeKey) != ""
}

// handleClaimedViaRouter 走 dispatcher wiring batch 的 NodeExecutor 抽象路由。
// 映射表：
//   - router 返错（framework fault） → markTransientRetry，下轮 tick 重试。
//   - outcome.Status == done                 → markLaunched。
//   - outcome.Status == failed                → 按 FailureClass 拆分 permanent / retry。
//   - 其他 (skipped / waiting_human 等暂不纳入 dispatcher 判定)→ markLaunched
//     (设计上该多状态是 node.status 的事实，wakeup 只负责代推一下)。
//
// agent 节点启动后只到 running，等 turn.completed 再收尾。
// automation 没有外部 agent，所以 router 直接完成节点。
func (d *WakeupDispatcher) handleClaimedViaRouter(ctx context.Context, w *taskdag.Wakeup) bool {
	fence := extractFence(w)
	if routeRunID(w) <= 0 {
		lastErr := "dag wakeup missing run_id for runtime node dispatch"
		d.logger.Warn("wakeup dispatcher: dag wakeup missing run_id → failed",
			"wakeup_id", w.ID, "dag_key", w.DagKey, "node_key", w.NodeKey)
		return d.markPermanentFail(ctx, w, fence, lastErr, errors.New(lastErr))
	}
	if d.nodeRouter == nil {
		lastErr := "dag wakeup missing node router for runtime node dispatch"
		err := errors.New(lastErr)
		d.logger.Warn("wakeup dispatcher: dag wakeup missing node router → retry",
			"wakeup_id", w.ID, "dag_key", w.DagKey, "node_key", w.NodeKey)
		return d.markTransientRetry(ctx, w, fence, dispatchFailure{lastErr: lastErr, launchErr: err, outcome: failedWakeupOutcome(lastErr)})
	}
	outcome, err := d.nodeRouter.RouteByWakeup(ctx, w)
	if err != nil {
		lastErr := truncateWakeupError(err.Error())
		d.logger.Warn("wakeup dispatcher: router framework error → retry",
			"wakeup_id", w.ID, "dag_key", w.DagKey, "node_key", w.NodeKey,
			"error", err)
		failure := dispatchFailure{lastErr: lastErr, launchErr: err, outcome: failedWakeupOutcome(lastErr)}
		if errors.Is(err, errAgentReadyRunningWriteFailed) {
			return d.retryWakeup(ctx, w, fence, failure)
		}
		return d.markTransientRetry(ctx, w, fence, failure)
	}
	switch outcome.Status {
	case nodeexec.NodeStatusFailed:
		return d.handleFailedRouterOutcome(ctx, w, fence, outcome)
	default:
		// done、skipped、waiting_human 和零值都表示本次 wakeup 已由 router 收敛。
		return d.markLaunched(ctx, w, fence)
	}
}
