package toolbridge

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	platformshared "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/shared"
)

// 本文件负责把当前 provider/model/effort 上下文注入 launch_agent 与 DAG agent 节点。

// injectManagedLaunchContext 根据工具名分发 managed launch 参数注入逻辑。
func (h *Handler) injectManagedLaunchContext(ctx context.Context, req ToolCallRequest) (ToolCallRequest, error) {
	switch {
	case isManagedLaunchToolName(req.Name):
		return h.injectManagedLaunchToolContext(ctx, req)
	case isManagedDAGLaunchToolName(req.Name):
		return h.injectManagedDAGLaunchContext(ctx, req)
	default:
		return req, nil
	}
}

// injectManagedLaunchToolContext 为单个 launch_agent 调用继承当前线程的启动上下文。
// 只补缺省参数，不覆盖调用方显式传入的 provider/model/effort/cwd。
func (h *Handler) injectManagedLaunchToolContext(ctx context.Context, req ToolCallRequest) (ToolCallRequest, error) {
	binding, err := h.requireManagedLaunchParentBinding(ctx, req)
	if err != nil {
		return ToolCallRequest{}, err
	}
	args := decodeToolArguments(req.Arguments)
	if args == nil {
		args = make(map[string]any)
	}
	launchCWD := firstNonEmptyString(req.CWD, binding.CWD)
	provider, model, effort := h.resolveManagedLaunchDefaults(ctx, binding, args, launchCWD)
	changed := injectManagedLaunchArgs(args, binding, provider, model, effort)
	if !changed {
		return req, nil
	}
	raw, err := json.Marshal(args)
	if err != nil {
		return ToolCallRequest{}, fmt.Errorf("toolbridge: encode launch_agent context for parent %q: %w", binding.AgentID, err)
	}
	req.Arguments = raw
	fields := []any{
		"agent_id", binding.AgentID,
		"provider_thread_id", binding.ProviderThreadID,
		"codex_thread_id", binding.CodexThreadID,
		"injected_parent_id", mapString(args, "parent_id"),
	}
	fields = append(fields, platformshared.SafePathLogFields("args_cwd", mapString(args, "cwd"))...)
	fields = append(fields,
		"injected_provider", mapString(args, "provider"),
		"injected_model", mapString(args, "model"),
		"injected_effort", mapString(args, "effort"),
		"has_codex_home", strings.TrimSpace(binding.CodexHome) != "",
		"has_codex_instance_key", strings.TrimSpace(binding.CodexInstanceKey) != "",
		"has_codex_model_provider", strings.TrimSpace(binding.CodexModelProvider) != "",
	)
	h.warn("toolbridge: launch_agent inherited context", fields...)
	return req, nil
}

// requireManagedLaunchParentBinding 在任何子 Agent 调度前证明父 Agent 已登记且 provider thread 已建立。
func (h *Handler) requireManagedLaunchParentBinding(ctx context.Context, req ToolCallRequest) (toolCallBinding, error) {
	result := h.resolveCurrentToolCallBindingResult(ctx, req)
	if result.status == toolCallLookupFailed {
		return toolCallBinding{}, fmt.Errorf("toolbridge: parent agent binding lookup failed for %q: %w", strings.TrimSpace(req.AgentID), result.err)
	}
	if result.status != toolCallLookupFound || strings.TrimSpace(result.binding.AgentID) == "" {
		return toolCallBinding{}, fmt.Errorf("toolbridge: parent agent binding is required before %s", strings.TrimSpace(req.Name))
	}
	if strings.TrimSpace(result.binding.ProviderThreadID) == "" {
		return toolCallBinding{}, fmt.Errorf("toolbridge: parent agent %q provider_thread_id is required before %s", result.binding.AgentID, strings.TrimSpace(req.Name))
	}
	return result.binding, nil
}

// isManagedLaunchToolName 判断工具名是否为单 agent 启动入口。
func isManagedLaunchToolName(name string) bool {
	name = strings.TrimSpace(name)
	return name == "launch_agent"
}

// resolveManagedLaunchDefaults 按 UI 偏好和父线程 runtime 计算子 agent 默认启动参数。
func (h *Handler) resolveManagedLaunchDefaults(ctx context.Context, binding toolCallBinding, args map[string]any, launchCWD string) (string, string, string) {
	provider, prefModel, prefEffort := h.resolveManagedLaunchDefaultsFromPreferences(ctx, binding, args, launchCWD)
	model, effort := h.resolveManagedLaunchModelEffortFromParent(ctx, binding)
	model, effort = compatibleManagedLaunchModelEffort(provider, model, effort)
	return provider, firstNonEmptyString(model, prefModel), firstNonEmptyString(effort, prefEffort)
}

// resolveManagedLaunchModelEffortFromParent 从父线程 runtime 中读取已使用的模型与 effort。
func (h *Handler) resolveManagedLaunchModelEffortFromParent(ctx context.Context, binding toolCallBinding) (string, string) {
	for _, threadID := range []string{binding.AgentID, binding.CodexThreadID, binding.ProviderThreadID} {
		stored, ok := h.readStoredThreadRuntime(ctx, threadID)
		if !ok {
			continue
		}
		runtime := stored.Runtime
		model := firstNonEmptyString(stored.Model, mapString(runtime, "model"))
		effort := firstNonEmptyString(stored.Effort, mapString(runtime, "effort"))
		if model != "" || effort != "" {
			return model, effort
		}
	}
	return "", ""
}

// resolveManagedLaunchDefaultsFromPreferences 读取 UI provider 偏好作为新 agent 的默认值。
func (h *Handler) resolveManagedLaunchDefaultsFromPreferences(ctx context.Context, binding toolCallBinding, args map[string]any, launchCWD string) (string, string, string) {
	prefs, ok := h.readMergedUIPreferences(ctx, firstNonEmptyString(mapString(args, "cwd"), launchCWD, binding.CWD))
	if !ok {
		return "", "", ""
	}
	provider := normalizeProviderPreferenceScope(firstNonEmptyString(
		mapString(args, "provider"),
		preferenceString(prefs, "settings.provider.active"),
		binding.Provider,
	))
	model := preferenceString(prefs, "settings.provider."+provider+".model")
	effort := preferenceString(prefs, "settings.provider."+provider+".effort")
	defaultModel, defaultEffort := defaultProviderLaunchConfig(provider)
	return provider, firstNonEmptyString(model, defaultModel), firstNonEmptyString(effort, defaultEffort)
}

// readMergedUIPreferences 读取指定 cwd 的合并 UI 偏好；失败只记录告警并允许后续来源接管。
func (h *Handler) readMergedUIPreferences(ctx context.Context, cwd string) (map[string]any, bool) {
	if h == nil || h.preferences == nil {
		return nil, false
	}
	prefs, err := h.preferences.GetMergedPreferences(ctx, strings.TrimSpace(cwd))
	if err != nil {
		fields := []any{"error", err}
		fields = append(fields, platformshared.SafePathLogFields("cwd", strings.TrimSpace(cwd))...)
		h.warn("toolbridge: read UI preferences for launch defaults failed", fields...)
		return nil, false
	}
	return prefs, true
}

// preferenceString 从偏好 map 中读取字符串值。
func preferenceString(values map[string]any, key string) string {
	if len(values) == 0 {
		return ""
	}
	value, ok := values[key]
	if !ok {
		return ""
	}
	text, ok := value.(string)
	if !ok {
		return ""
	}
	return strings.TrimSpace(text)
}

// normalizeProviderPreferenceScope 统一 provider 偏好作用域名称，兼容 Claude/Codex 的别名。
func normalizeProviderPreferenceScope(provider string) string {
	normalized := strings.ToLower(strings.TrimSpace(provider))
	switch {
	case normalized == "claude" || strings.Contains(normalized, "claude"):
		return "claude"
	case normalized == "codex" || normalized == "openai" || normalized == "":
		return "codex"
	default:
		return normalized
	}
}

// defaultProviderLaunchConfig 返回 provider 对应的默认模型与 effort。
func defaultProviderLaunchConfig(provider string) (string, string) {
	if normalizeProviderPreferenceScope(provider) == "claude" {
		return "sonnet", "high"
	}
	return "gpt-5.5", "xhigh"
}

// compatibleManagedLaunchModelEffort 过滤与目标 provider 不兼容的模型和 effort。
func compatibleManagedLaunchModelEffort(provider, model, effort string) (string, string) {
	provider = normalizeProviderPreferenceScope(provider)
	model = strings.TrimSpace(model)
	effort = strings.TrimSpace(effort)
	if model != "" && !managedLaunchModelCompatible(provider, model) {
		return "", ""
	}
	if effort != "" && !managedLaunchEffortCompatible(provider, effort) {
		effort = ""
	}
	return model, effort
}

// managedLaunchModelCompatible 判断模型名是否能用于目标 provider。
func managedLaunchModelCompatible(provider, model string) bool {
	model = strings.ToLower(strings.TrimSpace(model))
	if model == "" {
		return true
	}
	if normalizeProviderPreferenceScope(provider) == "claude" {
		return model == "best" || model == "opus" || model == "opus[1m]" ||
			model == "sonnet" || model == "sonnet[1m]" || model == "haiku" ||
			strings.HasPrefix(model, "claude-")
	}
	return strings.HasPrefix(model, "gpt-")
}

// managedLaunchEffortCompatible 判断 effort 是否能用于目标 provider。
func managedLaunchEffortCompatible(provider, effort string) bool {
	switch strings.ToLower(strings.TrimSpace(effort)) {
	case "high", "medium", "low":
		return true
	case "max":
		return normalizeProviderPreferenceScope(provider) == "claude"
	case "xhigh", "minimal", "none":
		return normalizeProviderPreferenceScope(provider) != "claude"
	default:
		return false
	}
}

// firstNonEmptyString 返回第一个清理后非空的字符串。
func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	return ""
}

// warnManagedLaunchConfigTrace 记录 launch_agent 参数继承的调试信息。
func (h *Handler) warnManagedLaunchConfigTrace(ctx context.Context, req ToolCallRequest) {
	if strings.TrimSpace(req.Name) != "launch_agent" {
		return
	}
	args := decodeToolArguments(req.Arguments)
	threadID, _ := h.resolveToolCallThreadID(ctx, req)
	stored, ok := h.readStoredThreadRuntime(ctx, threadID)
	runtime := stored.Runtime
	h.debug("toolbridge: launch_agent config trace",
		"agent_id", strings.TrimSpace(req.AgentID),
		"thread_id", threadID,
		"args_provider", mapString(args, "provider"),
		"args_model", mapString(args, "model"),
		"args_effort", mapString(args, "effort"),
		"stored_found", ok,
		"stored_model", strings.TrimSpace(stored.Model),
		"stored_effort", strings.TrimSpace(stored.Effort),
		"runtime_model", mapString(runtime, "model"),
		"runtime_effort", mapString(runtime, "effort"),
	)
}

// readStoredThreadRuntime 读取 thread config override 中的模型、effort 与 runtime。
func (h *Handler) readStoredThreadRuntime(ctx context.Context, threadID string) (storedThreadRuntime, bool) {
	if h == nil || h.threadStore == nil || strings.TrimSpace(threadID) == "" {
		return storedThreadRuntime{}, false
	}
	raw, err := h.threadStore.GetConfigOverride(ctx, strings.TrimSpace(threadID))
	if err != nil || len(raw) == 0 {
		return storedThreadRuntime{}, false
	}
	var stored storedThreadRuntime
	if err := json.Unmarshal(raw, &stored); err != nil {
		return storedThreadRuntime{}, false
	}
	return stored, true
}
