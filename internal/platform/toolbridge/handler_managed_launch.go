package toolbridge

import (
	"context"
	"encoding/json"
	"strings"
)

// This file contains all logic for injecting managed launch context
// (provider / model / effort) into orchestration_launch_agent tool
// calls, including preference resolution and compatibility checks.

func (h *Handler) injectManagedLaunchContext(ctx context.Context, req ToolCallRequest) ToolCallRequest {
	switch {
	case isManagedLaunchToolName(req.Name):
		return h.injectManagedLaunchToolContext(ctx, req)
	case isManagedDAGLaunchToolName(req.Name):
		return h.injectManagedDAGLaunchContext(ctx, req)
	default:
		return req
	}
}

// injectManagedLaunchToolContext 处理injectmanaged启动工具上下文。
func (h *Handler) injectManagedLaunchToolContext(ctx context.Context, req ToolCallRequest) ToolCallRequest {
	binding, ok := h.resolveCurrentToolCallBinding(ctx, req)
	if !ok || strings.TrimSpace(binding.AgentID) == "" {
		return req
	}
	args := decodeToolArguments(req.Arguments)
	if args == nil {
		args = make(map[string]any)
	}
	launchCWD := firstNonEmptyString(req.CWD, binding.CWD)
	provider, model, effort := h.resolveManagedLaunchDefaults(ctx, binding, args, launchCWD)
	parentThreadID := managedLaunchParentThreadIDForArgs(req, binding, args)
	changed := injectManagedLaunchArgs(args, binding, parentThreadID, provider, model, effort)
	if !changed {
		return req
	}
	raw, err := json.Marshal(args)
	if err != nil {
		h.warn("toolbridge: orchestration_launch_agent context injection failed",
			"agent_id", binding.AgentID,
			"error", err)
		return req
	}
	req.Arguments = raw
	h.warn("toolbridge: orchestration_launch_agent inherited context",
		"agent_id", binding.AgentID,
		"provider_thread_id", binding.ProviderThreadID,
		"codex_thread_id", binding.CodexThreadID,
		"injected_parent_id", mapString(args, "parent_id"),
		"injected_parent_thread_id", mapString(args, "parent_thread_id"),
		"args_cwd", mapString(args, "cwd"),
		"injected_provider", mapString(args, "provider"),
		"injected_model", mapString(args, "model"),
		"injected_effort", mapString(args, "effort"),
		"has_codex_home", strings.TrimSpace(binding.CodexHome) != "",
		"has_codex_instance_key", strings.TrimSpace(binding.CodexInstanceKey) != "",
		"has_codex_model_provider", strings.TrimSpace(binding.CodexModelProvider) != "",
	)
	return req
}

// managedLaunchParentThreadIDForArgs 只给 forked 启动注入父线程 ID。
// minimal/focused 子任务不需要继承父历史，继续只传 parent_id。
func managedLaunchParentThreadIDForArgs(req ToolCallRequest, binding toolCallBinding, args map[string]any) string {
	if !strings.EqualFold(mapString(args, "context_mode"), "forked") {
		return ""
	}
	return managedLaunchParentThreadID(req, binding)
}

// managedLaunchParentThreadID 返回桌面 thread/fork 可解析的父线程 ID。
// thread/fork 先查本地 thread store；provider UUID 只作为旧数据兜底。
func managedLaunchParentThreadID(req ToolCallRequest, binding toolCallBinding) string {
	return firstNonEmptyString(binding.CodexThreadID, binding.AgentID, req.AgentID, binding.ProviderThreadID, req.ThreadID)
}

func isManagedLaunchToolName(name string) bool {
	switch strings.TrimSpace(name) {
	case "launch_agent", "orchestration_launch_agent":
		return true
	default:
		return false
	}
}

func (h *Handler) resolveManagedLaunchDefaults(ctx context.Context, binding toolCallBinding, args map[string]any, launchCWD string) (string, string, string) {
	provider, prefModel, prefEffort := h.resolveManagedLaunchDefaultsFromPreferences(ctx, binding, args, launchCWD)
	model, effort := h.resolveManagedLaunchModelEffortFromParent(ctx, binding)
	model, effort = compatibleManagedLaunchModelEffort(provider, model, effort)
	return provider, firstNonEmptyString(model, prefModel), firstNonEmptyString(effort, prefEffort)
}

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

func (h *Handler) readMergedUIPreferences(ctx context.Context, cwd string) (map[string]any, bool) {
	if h == nil || h.preferences == nil {
		return nil, false
	}
	prefs, err := h.preferences.GetMergedPreferences(ctx, strings.TrimSpace(cwd))
	if err != nil {
		h.warn("toolbridge: read UI preferences for launch defaults failed",
			"cwd", strings.TrimSpace(cwd),
			"error", err)
		return nil, false
	}
	return prefs, true
}

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

// normalizeProviderPreferenceScope 规范化providerpreference作用域。
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

func defaultProviderLaunchConfig(provider string) (string, string) {
	if normalizeProviderPreferenceScope(provider) == "claude" {
		return "sonnet", "high"
	}
	return "gpt-5.5", "xhigh"
}

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

// managedLaunchModelCompatible 处理managed启动模型compatible。
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

func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	return ""
}

func (h *Handler) warnManagedLaunchConfigTrace(ctx context.Context, req ToolCallRequest) {
	if strings.TrimSpace(req.Name) != "orchestration_launch_agent" {
		return
	}
	args := decodeToolArguments(req.Arguments)
	threadID, _ := h.resolveToolCallThreadID(ctx, req)
	stored, ok := h.readStoredThreadRuntime(ctx, threadID)
	runtime := stored.Runtime
	h.debug("toolbridge: orchestration_launch_agent config trace",
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

// readStoredThreadRuntime 读取stored线程运行时。
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

func decodeStoredThreadRuntime(raw json.RawMessage) (map[string]any, bool) {
	var stored storedThreadRuntime
	if err := json.Unmarshal(raw, &stored); err != nil || len(stored.Runtime) == 0 {
		return nil, false
	}
	return stored.Runtime, true
}
