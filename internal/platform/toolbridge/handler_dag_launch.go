package toolbridge

import (
	"context"
	"encoding/json"
	"strings"
)

// injectManagedDAGLaunchContext 处理injectmanagedDAG启动上下文。
func (h *Handler) injectManagedDAGLaunchContext(ctx context.Context, req ToolCallRequest) ToolCallRequest {
	binding, ok := h.resolveCurrentToolCallBinding(ctx, req)
	if !ok || strings.TrimSpace(binding.AgentID) == "" {
		return req
	}
	args := decodeToolArguments(req.Arguments)
	if args == nil {
		return req
	}
	provider := managedDAGLaunchProvider(binding)
	if !injectManagedDAGLaunchArgs(args, binding, provider) {
		return req
	}
	raw, err := json.Marshal(args)
	if err != nil {
		h.warn("toolbridge: task_create_dag context injection failed",
			"agent_id", binding.AgentID,
			"error", err)
		return req
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
	return req
}

func isManagedDAGLaunchToolName(name string) bool {
	return strings.TrimSpace(name) == "task_create_dag"
}

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

// injectManagedDAGLaunchArgs 处理injectmanagedDAG启动args。
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

func isDAGAgentNode(node map[string]any) bool {
	nodeType := strings.ToLower(mapString(node, "node_type"))
	return nodeType == "" || nodeType == "agent"
}

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
