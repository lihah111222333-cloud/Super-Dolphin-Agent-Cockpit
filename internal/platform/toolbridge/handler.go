package toolbridge

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync/atomic"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/mcp"
	"github.com/anthropic-ai/super-agent-v3/internal/mcpserver/common"
	skillpkg "github.com/anthropic-ai/super-agent-v3/internal/module/skill"
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
	// hostTools 是可选依赖：当宿主进程同时含 skill module + toolbridge（即 agent-terminal）
	// 时，该字段被填充为 SkillHostTools，提供 skill_expand_body / skill_read_resource 两个
	// 本进程直跑的工具。字段保持 nil-safe：测试或未来无 HostToolRegistry 的 toolbridge
	// 图会退回 peer 路径；当前 mcp-orch / mcp-lsp standalone 不加载 toolbridge.Module。
	hostTools HostToolRegistry
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
		hostTools:    in.HostTools,
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
	if result, handled, err := h.routePrePeerToolCall(ctx, req); handled || err != nil {
		return result, err
	}
	clientKind, err := resolveToolClientKind(req)
	if err != nil {
		return nil, err
	}
	peer, err := h.selectActiveToolPeer(clientKind)
	if err != nil {
		return nil, err
	}
	return h.callPeerTool(ctx, peer.Peer, req)
}

func (h *Handler) routePrePeerToolCall(ctx context.Context, req ToolCallRequest) (*ToolCallResult, bool, error) {
	blocked, err := h.spawnAgentPolicyMessage(ctx, req)
	if err != nil {
		return nil, false, err
	}
	if blocked != "" {
		return toolCallTextResult(false, blocked), true, nil
	}
	// Host-direct 分支：当 hostTools 声明该工具名存在时，本进程同步执行，不走 peer
	// callback。dedup 优先级：hostTools 先查、命中即返回、不再查 peer——与 ListToolsForCodex
	// 的聚合顺序一致，避免同名工具双面路由产生不一致行为。
	if h.hostTools != nil && h.hostTools.HasTool(req.Name) {
		result, err := h.callHostTool(ctx, req)
		return result, true, err
	}
	return nil, false, nil
}

func (h *Handler) selectActiveToolPeer(clientKind string) (*mcpcontrol.ToolInstance, error) {
	peers := h.registry.FindActiveByKind(clientKind)
	switch len(peers) {
	case 0:
		return nil, ErrNoPeerAvailable
	case 1:
		return peers[0], nil
	default:
		return nil, ErrAmbiguousPeer
	}
}

func (h *Handler) callPeerTool(ctx context.Context, peer mcpcontrol.Peer, req ToolCallRequest) (*ToolCallResult, error) {
	callCtx, cancel := platformconfig.WithPeerTimeout(ctx, toolCallTimeout)
	defer cancel()

	snapshot := h.beginToolDiffSnapshot(ctx, req)
	req = h.injectManagedLaunchContext(ctx, req)
	h.warnManagedLaunchConfigTrace(ctx, req)

	var resp peerToolCallResponse
	err := peer.Callback(callCtx, ProxyMethodToolsCall, map[string]any{
		"name":              req.Name,
		"arguments":         req.Arguments,
		MetadataKeyAgentID:  req.AgentID,
		MetadataKeyThreadID: req.ThreadID,
		MetadataKeyCallID:   req.CallID,
	}, &resp)
	if err != nil {
		return toolCallTextResult(false, err.Error()), nil
	}

	result := adaptMCPResponse(resp)
	h.emitToolDiff(ctx, req, snapshot)
	return result, nil
}

func toolCallTextResult(success bool, text string) *ToolCallResult {
	return &ToolCallResult{
		Success: success,
		ContentItems: []ToolCallContentItem{{
			Type: "inputText",
			Text: text,
		}},
	}
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
	changed := injectManagedLaunchArgs(args, binding, provider, model, effort)
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
	provider, prefModel, prefEffort := h.resolveManagedLaunchDefaultsFromPreferences(ctx, binding, args)
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

type peerToolsListOutcome struct {
	clientKind string
	tools      []common.MCPTool
	err        error
}

func (h *Handler) listPeerToolsForCodex(ctx context.Context, kinds ...string) []peerToolsListOutcome {
	if ctx == nil {
		ctx = context.Background()
	}
	if len(kinds) == 0 {
		return nil
	}
	type indexedOutcome struct {
		index   int
		outcome peerToolsListOutcome
	}
	ch := make(chan indexedOutcome, len(kinds))
	for index, kind := range kinds {
		go func() {
			tools, err := h.listPeerTools(ctx, kind)
			ch <- indexedOutcome{
				index: index,
				outcome: peerToolsListOutcome{
					clientKind: kind,
					tools:      tools,
					err:        err,
				},
			}
		}()
	}
	out := make([]peerToolsListOutcome, len(kinds))
	for range kinds {
		result := <-ch
		out[result.index] = result.outcome
	}
	return out
}

func joinPeerToolErrors(outcomes []peerToolsListOutcome) error {
	errs := make([]error, 0, len(outcomes))
	for _, outcome := range outcomes {
		if outcome.err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", outcome.clientKind, outcome.err))
		}
	}
	return errors.Join(errs...)
}

func (h *Handler) ListToolsForCodex(ctx context.Context) ([]codexprotocol.DynamicToolSchema, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	// Host-direct 工具优先加入列表：dedup 遵守“先加入者胜出”原则，同名 peer 工具会被忽略。
	// 这保证调用阶段 hostTools.HasTool 命中与列表阶段优先级一致。
	var hostTools []common.MCPTool
	if h != nil && h.hostTools != nil {
		hostTools = h.hostTools.ListHostTools()
	}
	merged := append([]common.MCPTool(nil), hostTools...)
	peerSucceeded := false
	outcomes := h.listPeerToolsForCodex(ctx, dto.ClientKindOrch, dto.ClientKindLSP)
	for _, outcome := range outcomes {
		if outcome.err != nil {
			if h != nil {
				h.warn("toolbridge dynamic tools peer degraded", "client_kind", outcome.clientKind, "error", outcome.err)
			}
			continue
		}
		peerSucceeded = true
		merged = append(merged, outcome.tools...)
	}
	if len(merged) == 0 && !peerSucceeded {
		if err := joinPeerToolErrors(outcomes); err != nil {
			return nil, fmt.Errorf("toolbridge: no dynamic tools available: %w", err)
		}
		return nil, ErrNoPeerAvailable
	}
	return toCodexDynamicTools(dedupToolsByName(merged)), nil
}

// dedupToolsByName 按 name 去重，保留首次出现的入口（所以调用时要把 host-direct
// 放在列表最前）。补 host_tools 的 dedup 优先级语义。
func dedupToolsByName(in []common.MCPTool) []common.MCPTool {
	seen := make(map[string]struct{}, len(in))
	out := make([]common.MCPTool, 0, len(in))
	for _, t := range in {
		if _, ok := seen[t.Name]; ok {
			continue
		}
		seen[t.Name] = struct{}{}
		out = append(out, t)
	}
	return out
}

// callHostTool 是 routeToolCall 的 host-direct 分支：在调用 hostTools.CallHostTool 之前
// 从 agentID 解析 cwd，打包返回值为 ToolCallResult，路径上与 peer 分支对齐。
func (h *Handler) callHostTool(ctx context.Context, req ToolCallRequest) (*ToolCallResult, error) {
	cwd := h.resolveAgentCWD(ctx, req.AgentID)
	result, err := h.hostTools.CallHostTool(ctx, HostToolCall{
		Name:      req.Name,
		Arguments: req.Arguments,
		CWD:       cwd,
		AgentID:   strings.TrimSpace(req.AgentID),
		ThreadID:  strings.TrimSpace(req.ThreadID),
		TurnID:    strings.TrimSpace(req.TurnID),
		CallID:    strings.TrimSpace(req.CallID),
	})
	if err != nil {
		return hostToolErrorResult(req, err), nil
	}
	payload, mErr := json.Marshal(result)
	if mErr != nil {
		return nil, mErr
	}
	return &ToolCallResult{
		Success: true,
		ContentItems: []ToolCallContentItem{{
			Type: "inputText",
			Text: string(payload),
		}},
	}, nil
}

func hostToolErrorResult(req ToolCallRequest, err error) *ToolCallResult {
	envelope := map[string]any{
		"kind":  "host_tool_error",
		"tool":  strings.TrimSpace(req.Name),
		"error": err.Error(),
	}
	var required skillpkg.SkillApprovalRequiredError
	switch {
	case errors.As(err, &required):
		envelope["kind"] = "approval_required"
		envelope["approval"] = approvalRequestEnvelope(required.Request)
	case isSkillApprovalDenied(err):
		envelope["kind"] = "approval_denied"
	}
	payload, marshalErr := json.Marshal(envelope)
	if marshalErr != nil {
		payload = []byte(err.Error())
	}
	return &ToolCallResult{
		Success: false,
		ContentItems: []ToolCallContentItem{{
			Type: "inputText",
			Text: string(payload),
		}},
	}
}

type skillApprovalDeniedMarker interface {
	SkillApprovalDenied() bool
}

func isSkillApprovalDenied(err error) bool {
	var marker skillApprovalDeniedMarker
	return errors.As(err, &marker) && marker.SkillApprovalDenied()
}

func approvalRequestEnvelope(req contract.ApprovalRequest) map[string]any {
	return map[string]any{
		"callId":       strings.TrimSpace(req.CallID),
		"approvalId":   strings.TrimSpace(req.ApprovalID),
		"toolName":     strings.TrimSpace(req.ToolName),
		"agentId":      strings.TrimSpace(req.AgentID),
		"threadId":     strings.TrimSpace(req.ThreadID),
		"turnId":       strings.TrimSpace(req.TurnID),
		"reason":       strings.TrimSpace(req.Reason),
		"kind":         strings.TrimSpace(req.Kind),
		"sourceMethod": strings.TrimSpace(req.SourceMethod),
		"payload":      req.Payload,
	}
}

// resolveAgentCWD 包装 WorkDirResolver 调用，失败时返回空串（下游 service 会返 ErrMissingCWD，
// 该错误会被 callHostTool 打包成带说明的失败 ToolCallResult）。resolver 为 nil 同理空串。
func (h *Handler) resolveAgentCWD(ctx context.Context, agentID string) string {
	if h == nil || h.resolver == nil {
		return ""
	}
	cwd, err := h.resolver.ResolveAgentCWD(ctx, agentID)
	if err != nil {
		return ""
	}
	return cwd
}
