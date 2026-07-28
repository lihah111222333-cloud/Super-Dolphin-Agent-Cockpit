package toolbridge

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// injectManagedDAGLaunchContext 为 task_create_dag 的 agent 节点注入当前 Codex 启动上下文。
// 父绑定或 provider thread 不完整时阻断整个 DAG，避免 agent 节点脱离父子拓扑。
func (h *Handler) injectManagedDAGLaunchContext(ctx context.Context, req ToolCallRequest) (ToolCallRequest, error) {
	binding, err := h.requireManagedLaunchParentBinding(ctx, req)
	if err != nil {
		return ToolCallRequest{}, err
	}
	args := decodeToolArguments(req.Arguments)
	if args == nil {
		return req, nil
	}
	provider := managedDAGLaunchProvider(binding)
	if !injectManagedDAGLaunchArgs(args, binding, provider) {
		return req, nil
	}
	raw, err := json.Marshal(args)
	if err != nil {
		return ToolCallRequest{}, fmt.Errorf("toolbridge: encode task_create_dag context for parent %q: %w", binding.AgentID, err)
	}
	req.Arguments = raw
	h.warn("toolbridge: task_create_dag inherited launch context",
		"agent_id", binding.AgentID,
		"provider_thread_id", binding.ProviderThreadID,
		"injected_provider", provider,
		"has_codex_home", strings.TrimSpace(binding.CodexHome) != "",
		"has_codex_instance_key", strings.TrimSpace(binding.CodexInstanceKey) != "",
		"has_codex_model_provider", strings.TrimSpace(binding.CodexModelProvider) != "",
	)
	return req, nil
}

// isManagedDAGLaunchToolName 判断工具名是否为 DAG 创建入口。
func isManagedDAGLaunchToolName(name string) bool {
	return strings.TrimSpace(name) == "task_create_dag"
}

// managedDAGLaunchProvider 根据当前 binding 推断 DAG agent 默认 provider。
func managedDAGLaunchProvider(binding toolCallBinding) string {
	provider := firstNonEmptyString(binding.Provider)
	if provider == "" && strings.TrimSpace(binding.CodexModelProvider) != "" {
		provider = "codex"
	}
	if provider == "" {
		return ""
	}
	return normalizeProviderPreferenceScope(provider)
}

// injectManagedDAGLaunchArgs 只改写 DAG 中的 agent 节点。
// 已显式设置的 exec 字段不会被覆盖，避免父线程策略吞掉调用方指定的启动参数。
func injectManagedDAGLaunchArgs(args map[string]any, binding toolCallBinding, provider string) bool {
	nodes, ok := args["nodes"].([]any)
	if !ok {
		return false
	}
	changed := false
	for _, raw := range nodes {
		node, ok := raw.(map[string]any)
		if !ok || !isDAGAgentNode(node) {
			continue
		}
		exec := dagNodeExecMap(node)
		if exec == nil {
			continue
		}
		if injectManagedDAGAgentExec(exec, binding, provider) {
			changed = true
		}
	}
	return changed
}

// isDAGAgentNode 判断节点是否应按 agent 节点注入上下文；缺省 node_type 兼容旧模板。
func isDAGAgentNode(node map[string]any) bool {
	nodeType := strings.ToLower(mapString(node, "node_type"))
	return nodeType == "" || nodeType == "agent"
}

// dagNodeExecMap 读取 DAG 节点的 config.exec 对象；结构不匹配时跳过该节点。
func dagNodeExecMap(node map[string]any) map[string]any {
	config, ok := node["config"].(map[string]any)
	if !ok {
		return nil
	}
	exec, ok := config["exec"].(map[string]any)
	if !ok {
		return nil
	}
	return exec
}

// injectManagedDAGAgentExec 注入单个 agent 节点的 provider/Codex home/instance 信息。
func injectManagedDAGAgentExec(exec map[string]any, binding toolCallBinding, provider string) bool {
	changed := setArgStringIfMissing(exec, "provider", provider)
	if normalizeProviderPreferenceScope(mapString(exec, "provider")) != "codex" {
		return changed
	}
	if setArgStringIfMissing(exec, "codex_home", binding.CodexHome) {
		changed = true
	}
	if setArgStringIfMissing(exec, "codex_instance_key", binding.CodexInstanceKey) {
		changed = true
	}
	if setArgStringIfMissing(exec, "codex_model_provider", binding.CodexModelProvider) {
		changed = true
	}
	return changed
}
