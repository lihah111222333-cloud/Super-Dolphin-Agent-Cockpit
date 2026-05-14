package orchestration

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/orchestration/nodeexec"
	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/store/taskdag"
	platformconfig "github.com/anthropic-ai/super-agent-v3/internal/platform/config"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/runtimesafe"
	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
)

const (
	lifecycleHookDispatchWait     = 100 * time.Millisecond
	lifecycleHookExecutionTimeout = time.Second
)

// NodeLifecycleHooks is the production hook set injected into node executors.
// Keeping it as a named type lets fx distinguish the lifecycle hook map from
// ad-hoc map[HookPoint]HookHandler values in tests.
type NodeLifecycleHooks map[nodeexec.HookPoint]nodeexec.HookHandler

type loggingNodeLifecycleHook struct {
	logger *slog.Logger
}

func ProvideNodeLifecycleHooks(logger *slog.Logger) NodeLifecycleHooks {
	handler := loggingNodeLifecycleHook{logger: logger}
	return NodeLifecycleHooks{
		nodeexec.HookBeforeExecute: handler,
		nodeexec.HookAfterExecute:  handler,
		nodeexec.HookOnStateChange: handler,
		nodeexec.HookOnFailure:     handler,
	}
}

func ProvideAutomationExecutor(
	getter nodeexec.AutomationCommandGetter,
	runner nodeexec.AutomationCommandRunner,
	hooks NodeLifecycleHooks,
) *nodeexec.AutomationExecutor {
	return nodeexec.NewAutomationExecutor(getter, runner, nodeexec.WithAutomationHooks(map[nodeexec.HookPoint]nodeexec.HookHandler(hooks)))
}

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

func (r *NodeExecutorRouter) invokeLifecycleHook(
	ctx context.Context,
	hooks map[nodeexec.HookPoint]nodeexec.HookHandler,
	point nodeexec.HookPoint,
	node nodeexec.Node,
	outcome nodeexec.NodeOutcome,
) {
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
