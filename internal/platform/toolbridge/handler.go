package toolbridge

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"strings"
	"sync/atomic"
	"time"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/mcp"
	"github.com/anthropic-ai/super-agent-v3/internal/mcpserver/common"
	platformconfig "github.com/anthropic-ai/super-agent-v3/internal/platform/config"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/difftracker"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/mcpcontrol"
	"github.com/anthropic-ai/super-agent-v3/internal/provider/codexapp"
	codexprotocol "github.com/anthropic-ai/super-agent-v3/internal/provider/codexapp/protocol"
	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
)

// Handler fields are typed against the narrow ports in ports.go so
// this file has no direct dependency on internal/store/binding or
// internal/store/thread (P22 P4 S3d). Production adapters live in
// module.go where platform → store imports are legitimate (assembly
// seam).
const allowDefaultPersistentSubagentEnv = "TOOLBRIDGE_ALLOW_DEFAULT_PERSISTENT_SUBAGENT"

var persistentSubagentDefaultFallbackTotal atomic.Uint64

type Handler struct {
	registry     activePeerRegistry
	emitter      difftracker.DiffEmitter
	resolver     difftracker.WorkDirResolver
	diffFallback *diffFallbackTracker
	bindingStore agentThreadLookup
	threadStore  threadConfigOverrideStore
	preferences  uiPreferenceReader
	cfg          *platformconfig.Config
	logger       *pkglogger.Logger
}

type activePeerRegistry interface {
	FindActiveByKind(clientKind string) []*mcpcontrol.ToolInstance
}

type storedThreadRuntime struct {
	Model   string         `json:"model,omitempty"`
	Effort  string         `json:"effort,omitempty"`
	Runtime map[string]any `json:"runtime,omitempty"`
}

func NewHandler(in handlerIn) *Handler {
	logger := in.Logger
	if logger == nil {
		logger = pkglogger.Get()
	}
	return &Handler{
		registry:     in.Registry,
		emitter:      in.Emitter,
		resolver:     in.Resolver,
		diffFallback: in.DiffFallback,
		bindingStore: in.BindingStore,
		threadStore:  in.ThreadStore,
		preferences:  in.Preferences,
		cfg:          in.Config,
		logger:       logger,
	}
}

func (h *Handler) HandleToolCall(ctx context.Context, msg codexapp.RawMessage) (any, error) {
	req, err := decodeToolCallRequest(msg.Params)
	if err != nil {
		return nil, err
	}
	return h.routeToolCall(ctx, req)
}

func (h *Handler) routeToolCall(ctx context.Context, req ToolCallRequest) (*ToolCallResult, error) {
	if h == nil || h.registry == nil {
		return nil, ErrNoPeerAvailable
	}
	if blocked, err := h.spawnAgentPolicyMessage(ctx, req); err != nil {
		return nil, err
	} else if blocked != "" {
		return &ToolCallResult{
			Success: false,
			ContentItems: []ToolCallContentItem{{
				Type: "inputText",
				Text: blocked,
			}},
		}, nil
	}
	clientKind, err := resolveToolClientKind(req)
	if err != nil {
		return nil, err
	}
	peers := h.registry.FindActiveByKind(clientKind)
	if len(peers) == 0 {
		return nil, ErrNoPeerAvailable
	}
	if len(peers) > 1 {
		return nil, ErrAmbiguousPeer
	}

	callCtx, cancel := platformconfig.WithPeerTimeout(ctx, toolCallTimeout)
	defer cancel()

	peer := peers[0].Peer
	snapshot := h.beginToolDiffSnapshot(ctx, req)
	req = h.injectManagedLaunchContext(ctx, req)
	h.warnManagedLaunchConfigTrace(ctx, req)

	var resp peerToolCallResponse
	err = peer.Callback(callCtx, ProxyMethodToolsCall, map[string]any{
		"name":              req.Name,
		"arguments":         req.Arguments,
		MetadataKeyAgentID:  req.AgentID,
		MetadataKeyThreadID: req.ThreadID,
		MetadataKeyCallID:   req.CallID,
	}, &resp)
	if err != nil {
		return &ToolCallResult{
			Success: false,
			ContentItems: []ToolCallContentItem{{
				Type: "inputText",
				Text: err.Error(),
			}},
		}, nil
	}

	result := adaptMCPResponse(resp)
	h.emitToolDiff(ctx, req, snapshot)
	return result, nil
}

func (h *Handler) injectManagedLaunchContext(ctx context.Context, req ToolCallRequest) ToolCallRequest {
	if strings.TrimSpace(req.Name) != "orchestration_launch_agent" {
		return req
	}
	binding, ok := h.resolveCurrentToolCallBinding(ctx, req)
	if !ok || strings.TrimSpace(binding.AgentID) == "" {
		return req
	}
	args := decodeToolArguments(req.Arguments)
	if args == nil {
		args = make(map[string]any)
	}
	provider, model, effort := h.resolveManagedLaunchDefaults(ctx, binding, args)
	changed := setArgStringIfMissing(args, "parent_id", binding.AgentID)
	changed = setArgStringIfMissing(args, "cwd", binding.CWD) || changed
	changed = setArgStringIfMissing(args, "provider", provider) || changed
	changed = setArgStringIfMissing(args, "model", model) || changed
	changed = setArgStringIfMissing(args, "effort", effort) || changed
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
		"injected_parent_id", mapString(args, "parent_id"),
		"injected_cwd", mapString(args, "cwd"),
		"injected_provider", mapString(args, "provider"),
		"injected_model", mapString(args, "model"),
		"injected_effort", mapString(args, "effort"),
		"has_codex_home", strings.TrimSpace(binding.CodexHome) != "",
		"has_codex_instance_key", strings.TrimSpace(binding.CodexInstanceKey) != "",
		"has_codex_model_provider", strings.TrimSpace(binding.CodexModelProvider) != "",
	)
	return req
}

func (h *Handler) resolveManagedLaunchDefaults(ctx context.Context, binding toolCallBinding, args map[string]any) (string, string, string) {
	model, effort := h.resolveManagedLaunchModelEffortFromParent(ctx, binding)
	provider, prefModel, prefEffort := h.resolveManagedLaunchDefaultsFromPreferences(ctx, binding, args)
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

func (h *Handler) resolveManagedLaunchDefaultsFromPreferences(ctx context.Context, binding toolCallBinding, args map[string]any) (string, string, string) {
	prefs, ok := h.readMergedUIPreferences(ctx, firstNonEmptyString(mapString(args, "cwd"), binding.CWD))
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

func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	return ""
}

func (h *Handler) resolveCurrentToolCallBinding(ctx context.Context, req ToolCallRequest) (toolCallBinding, bool) {
	if h == nil || h.bindingStore == nil {
		return toolCallBinding{}, false
	}
	lookup, ok := h.bindingStore.(toolCallBindingLookup)
	if !ok {
		return toolCallBinding{}, false
	}
	if agentID := strings.TrimSpace(req.AgentID); agentID != "" {
		if binding, ok := lookupToolCallBindingByAgent(ctx, lookup, agentID); ok {
			return binding, true
		}
	}
	threadID := strings.TrimSpace(req.ThreadID)
	if threadID == "" {
		return toolCallBinding{}, false
	}
	if binding, ok := lookupToolCallBindingByAgent(ctx, lookup, threadID); ok {
		return binding, true
	}
	if binding, ok := lookupToolCallBindingByProviderThread(ctx, lookup, "codex", threadID); ok {
		return binding, true
	}
	return toolCallBinding{}, false
}

func lookupToolCallBindingByAgent(ctx context.Context, lookup toolCallBindingLookup, agentID string) (toolCallBinding, bool) {
	binding, err := lookup.GetBindingByAgent(ctx, strings.TrimSpace(agentID))
	if err != nil {
		return toolCallBinding{}, false
	}
	return binding, strings.TrimSpace(binding.AgentID) != ""
}

func lookupToolCallBindingByProviderThread(ctx context.Context, lookup toolCallBindingLookup, provider, threadID string) (toolCallBinding, bool) {
	binding, err := lookup.GetBindingByProviderThread(ctx, strings.TrimSpace(provider), strings.TrimSpace(threadID))
	if err != nil {
		return toolCallBinding{}, false
	}
	return binding, strings.TrimSpace(binding.AgentID) != ""
}

func (h *Handler) spawnAgentPolicyMessage(ctx context.Context, req ToolCallRequest) (string, error) {
	if strings.TrimSpace(req.Name) != "spawn_agent" {
		return "", nil
	}
	required, err := h.persistentSubagentRequired(ctx, req)
	if err != nil {
		return "", err
	}
	if !required {
		return "", nil
	}
	return "当前会话启用了 persistent_subagent_default：禁止使用 `spawn_agent` 创建临时子 agent。请改用 `orchestration_launch_agent` 创建持续化 UI 子 agent。", nil
}

func (h *Handler) persistentSubagentRequired(ctx context.Context, req ToolCallRequest) (bool, error) {
	runtime, err := h.requireToolCallRuntimeConfig(ctx, req)
	if err != nil {
		return false, err
	}
	required, present := persistentSubagentFlagFromRuntime(runtime)
	if !present {
		if !allowDefaultPersistentSubagentFallback() {
			return false, contract.ErrPersistentSubagentFlagRequired
		}
		required = h.cfg != nil && h.cfg.Agent.PersistentSubagentDefault
		persistentSubagentDefaultFallbackTotal.Add(1)
		h.warn("compatibility-only: persistent subagent default fallback", "env", allowDefaultPersistentSubagentEnv, "required", required)
	}
	if !required {
		return false, nil
	}
	managedAvailable, known := runtimeHasTool(runtime, "orchestration_launch_agent")
	if known {
		return managedAvailable, nil
	}
	return true, nil
}

func (h *Handler) requireToolCallRuntimeConfig(ctx context.Context, req ToolCallRequest) (map[string]any, error) {
	threadID, err := h.requireToolCallThreadID(ctx, req)
	if err != nil {
		return nil, err
	}
	return h.requireToolCallRuntime(ctx, threadID)
}

func (h *Handler) toolCallRuntimeConfig(ctx context.Context, req ToolCallRequest) (map[string]any, bool) {
	runtime, err := h.requireToolCallRuntimeConfig(ctx, req)
	if err != nil {
		return nil, false
	}
	return runtime, true
}

func (h *Handler) requireToolCallThreadID(ctx context.Context, req ToolCallRequest) (string, error) {
	threadID, ok := h.resolveToolCallThreadID(ctx, req)
	if !ok {
		return "", contract.ErrThreadRuntimeRequired
	}
	return threadID, nil
}

func (h *Handler) requireToolCallRuntime(ctx context.Context, threadID string) (map[string]any, error) {
	runtime, ok := h.readToolCallRuntime(ctx, threadID)
	if !ok {
		return nil, contract.ErrPersistentSubagentRuntimeRequired
	}
	return runtime, nil
}

func (h *Handler) resolveToolCallThreadID(ctx context.Context, req ToolCallRequest) (string, bool) {
	if threadID, ok := resolveToolCallThreadIDFromRequest(req); ok {
		return threadID, true
	}
	return h.resolveToolCallThreadIDFromAgent(ctx, req)
}

func resolveToolCallThreadIDFromRequest(req ToolCallRequest) (string, bool) {
	threadID := strings.TrimSpace(req.ThreadID)
	if threadID == "" {
		return "", false
	}
	return threadID, true
}

func (h *Handler) resolveToolCallThreadIDFromAgent(ctx context.Context, req ToolCallRequest) (string, bool) {
	if h == nil || h.bindingStore == nil {
		return "", false
	}
	agentID := strings.TrimSpace(req.AgentID)
	if agentID == "" {
		return "", false
	}
	threadID, err := h.bindingStore.GetThreadByAgent(ctx, agentID)
	if err != nil {
		return "", false
	}
	threadID = strings.TrimSpace(threadID)
	if threadID == "" {
		return "", false
	}
	return threadID, true
}

func (h *Handler) readToolCallRuntime(ctx context.Context, threadID string) (map[string]any, bool) {
	if h == nil || h.threadStore == nil {
		return nil, false
	}
	raw, err := h.threadStore.GetConfigOverride(ctx, threadID)
	if err != nil || len(raw) == 0 {
		return nil, false
	}
	return decodeStoredThreadRuntime(raw)
}

func (h *Handler) warnManagedLaunchConfigTrace(ctx context.Context, req ToolCallRequest) {
	if strings.TrimSpace(req.Name) != "orchestration_launch_agent" {
		return
	}
	args := decodeToolArguments(req.Arguments)
	threadID, _ := h.resolveToolCallThreadID(ctx, req)
	stored, ok := h.readStoredThreadRuntime(ctx, threadID)
	runtime := stored.Runtime
	h.warn("toolbridge: orchestration_launch_agent config trace",
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

func decodeToolArguments(raw json.RawMessage) map[string]any {
	if len(bytes.TrimSpace(raw)) == 0 {
		return nil
	}
	var args map[string]any
	if err := json.Unmarshal(raw, &args); err != nil {
		return nil
	}
	return args
}

func mapString(values map[string]any, key string) string {
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

func setArgStringIfMissing(values map[string]any, key, value string) bool {
	if mapString(values, key) != "" {
		return false
	}
	value = strings.TrimSpace(value)
	if value == "" {
		return false
	}
	values[key] = value
	return true
}

func allowDefaultPersistentSubagentFallback() bool {
	return os.Getenv(allowDefaultPersistentSubagentEnv) == "1"
}

func persistentSubagentDefaultFallbackCount() uint64 {
	return persistentSubagentDefaultFallbackTotal.Load()
}

func persistentSubagentFlagFromRuntime(runtime map[string]any) (bool, bool) {
	for _, key := range []string{"sessionFlags", "session_flags"} {
		raw, ok := runtime[key]
		if !ok {
			continue
		}
		flags, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		for name, value := range flags {
			switch normalizeToolbridgeSessionFlagName(name) {
			case "persistentsubagentdefault", "managedsubagentdefault", "uipersistentsubagentdefault":
				boolean, ok := value.(bool)
				if ok {
					return boolean, true
				}
			}
		}
	}
	return false, false
}

func runtimeHasTool(runtime map[string]any, want string) (bool, bool) {
	for _, key := range []string{"enabledTools", "enabled_tools", "tools"} {
		raw, ok := runtime[key]
		if !ok {
			continue
		}
		switch typed := raw.(type) {
		case []string:
			for _, value := range typed {
				if strings.TrimSpace(value) == want {
					return true, true
				}
			}
			return false, true
		case []any:
			for _, value := range typed {
				text, _ := value.(string)
				if strings.TrimSpace(text) == want {
					return true, true
				}
			}
			return false, true
		}
	}
	return false, false
}

func normalizeToolbridgeSessionFlagName(name string) string {
	name = strings.ToLower(strings.TrimSpace(name))
	replacer := strings.NewReplacer("_", "", "-", "", " ", "")
	return replacer.Replace(name)
}

func (h *Handler) ListToolsForCodex(ctx context.Context) ([]codexprotocol.DynamicToolSchema, error) {
	orchTools, err := h.listPeerTools(ctx, dto.ClientKindOrch)
	if err != nil {
		return nil, err
	}
	lspTools, err := h.listPeerTools(ctx, dto.ClientKindLSP)
	if err != nil {
		return nil, err
	}
	merged := append(append([]common.MCPTool(nil), orchTools...), lspTools...)
	return toCodexDynamicTools(merged), nil
}

// peerReadyTimeout is the max time to wait for a peer to register after startup.
const peerReadyTimeout = 10 * time.Second
const peerPollInterval = 300 * time.Millisecond

func (h *Handler) listPeerTools(ctx context.Context, clientKind string) ([]common.MCPTool, error) {
	if h == nil || h.registry == nil {
		return nil, ErrNoPeerAvailable
	}
	// Peer processes may still be starting up. Poll with a short timeout.
	peers, err := h.waitForPeer(ctx, clientKind)
	if err != nil {
		return nil, err
	}

	// Bootstrap peer callback returns {"tools":[...]} wrapper.
	var result peerToolsListResult
	if err := peers[0].Peer.Callback(ctx, "tools/list", nil, &result); err != nil {
		return nil, err
	}
	return result.Tools, nil
}

func (h *Handler) waitForPeer(ctx context.Context, clientKind string) ([]*mcpcontrol.ToolInstance, error) {
	deadline := time.Now().Add(peerReadyTimeout)
	for {
		peers := h.registry.FindActiveByKind(clientKind)
		if len(peers) >= 1 {
			// Use the first active peer. Multiple peers can exist when the
			// codex app-server also spawns MCP sidecars from a resumed thread.
			return peers[:1], nil
		}
		if time.Now().After(deadline) {
			return nil, ErrNoPeerAvailable
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(peerPollInterval):
		}
	}
}

func adaptMCPResponse(resp peerToolCallResponse) *ToolCallResult {
	items := make([]ToolCallContentItem, 0, len(resp.Content))
	for _, item := range resp.Content {
		items = append(items, ToolCallContentItem{
			Type: "inputText",
			Text: strings.TrimSpace(item.Text),
		})
	}
	return &ToolCallResult{ContentItems: items, Success: true}
}

func toCodexDynamicTools(tools []common.MCPTool) []codexprotocol.DynamicToolSchema {
	out := make([]codexprotocol.DynamicToolSchema, 0, len(tools))
	for _, tool := range tools {
		schema := codexprotocol.DynamicToolSchema{
			Name:        tool.Name,
			Description: tool.Description,
			InputSchema: tool.InputSchema,
		}
		setDynamicToolDeferLoading(&schema, toolDeferLoading(tool))
		out = append(out, schema)
	}
	return out
}

func decodeToolCallRequest(params json.RawMessage) (ToolCallRequest, error) {
	if len(bytes.TrimSpace(params)) == 0 {
		return ToolCallRequest{}, fmt.Errorf("toolbridge: missing tool call params")
	}

	var payload map[string]json.RawMessage
	if err := json.Unmarshal(params, &payload); err != nil {
		return ToolCallRequest{}, fmt.Errorf("toolbridge: decode tool call params: %w", err)
	}

	req := ToolCallRequest{
		Name:       firstString(payload, "name", "tool", "toolName", "tool_name"),
		Arguments:  firstRaw(payload, "arguments", "args"),
		AgentID:    firstString(payload, "agentId", "agent_id"),
		ThreadID:   firstString(payload, "threadId", "thread_id"),
		CallID:     firstString(payload, "callId", "call_id"),
		ClientKind: firstString(payload, "clientKind", "client_kind", "family"),
	}
	if req.Name == "" {
		req.Name = nestedString(payload, "item", "name", "tool", "toolName")
	}
	if req.ThreadID == "" {
		req.ThreadID = nestedString(payload, "thread", "id")
	}
	if req.CallID == "" {
		req.CallID = nestedString(payload, "item", "callId", "call_id")
	}
	if len(bytes.TrimSpace(req.Arguments)) == 0 {
		req.Arguments = json.RawMessage(`{}`)
	}
	if strings.TrimSpace(req.Name) == "" {
		return ToolCallRequest{}, fmt.Errorf("toolbridge: missing tool name")
	}
	return req, nil
}

func firstString(payload map[string]json.RawMessage, keys ...string) string {
	for _, key := range keys {
		if value := decodeString(payload[key]); value != "" {
			return value
		}
	}
	return ""
}

func firstRaw(payload map[string]json.RawMessage, keys ...string) json.RawMessage {
	for _, key := range keys {
		if value := bytes.TrimSpace(payload[key]); len(value) != 0 {
			return value
		}
	}
	return nil
}

func nestedString(payload map[string]json.RawMessage, field string, keys ...string) string {
	raw := bytes.TrimSpace(payload[field])
	if len(raw) == 0 {
		return ""
	}
	var nested map[string]json.RawMessage
	if err := json.Unmarshal(raw, &nested); err != nil {
		return ""
	}
	return firstString(nested, keys...)
}

func decodeString(raw json.RawMessage) string {
	if len(bytes.TrimSpace(raw)) == 0 {
		return ""
	}
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		return ""
	}
	return strings.TrimSpace(value)
}

func toolDeferLoading(tool common.MCPTool) bool {
	value := reflect.ValueOf(tool)
	field := value.FieldByName("DeferLoading")
	return field.IsValid() && field.Kind() == reflect.Bool && field.Bool()
}

func setDynamicToolDeferLoading(schema *codexprotocol.DynamicToolSchema, enabled bool) {
	if schema == nil {
		return
	}
	value := reflect.ValueOf(schema)
	if value.Kind() == reflect.Pointer {
		value = value.Elem()
	}
	field := value.FieldByName("DeferLoading")
	if field.IsValid() && field.CanSet() && field.Kind() == reflect.Bool {
		field.SetBool(enabled)
	}
}

func lspEditAction(arguments json.RawMessage) string {
	if len(bytes.TrimSpace(arguments)) == 0 {
		return ""
	}
	var payload struct {
		Action string `json:"action"`
	}
	if err := json.Unmarshal(arguments, &payload); err != nil {
		return ""
	}
	return strings.TrimSpace(payload.Action)
}

func (h *Handler) warn(msg string, args ...any) {
	logger := h.logger
	if logger == nil {
		logger = pkglogger.Get()
	}
	logger.Warn(msg, args...)
}

func (h *Handler) debug(msg string, args ...any) {
	logger := h.logger
	if logger == nil {
		logger = pkglogger.Get()
	}
	logger.Debug(msg, args...)
}
