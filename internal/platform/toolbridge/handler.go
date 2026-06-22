package toolbridge

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	providerdto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
	platformconfig "github.com/anthropic-ai/super-agent-v3/internal/platform/config"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/difftracker"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/mcpcontrol"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/observability"
	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
	"github.com/kelindar/event"
)

// Handler fields are typed against the narrow ports in ports.go so
// this file has no direct dependency on internal/store/binding or
// internal/store/thread (P22 P4 S3d). Production adapters live in
// module.go where platform → store imports are legitimate (assembly
// seam).
const allowDefaultPersistentSubagentEnv = "TOOLBRIDGE_ALLOW_DEFAULT_PERSISTENT_SUBAGENT"
const subAgentDelegationDepthLimitMessage = "Sub-agents are not allowed to spawn further agents (delegation depth limit)."

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
	tracer       *observability.Service
	dispatcher   *event.Dispatcher
	// hostTools 是可选依赖：agent-terminal 生产图只装配 memory_read / memory_write
	// host-direct 工具。字段保持 nil-safe：测试或未来无 HostToolRegistry 的
	// toolbridge 图会退回 peer 路径；当前 mcp-orch / mcp-lsp standalone 不加载
	// toolbridge.Module。
	hostTools          HostToolRegistry
	skillTools         contract.SkillToolProvider
	surfaceMu          sync.Mutex
	surfaces           map[string]*codexToolSurface
	proxyAuthToken     string
	stdioClientFactory func(context.Context, providerdto.MCPBinary) (mcpClient, error)
}

type activePeerRegistry interface {
	FindActiveByKind(clientKind string) []*mcpcontrol.ToolInstance
}

type scopedActivePeerRegistry interface {
	FindActiveForScope(scope mcpcontrol.ToolScope) []*mcpcontrol.ToolInstance
}

type storedThreadRuntime struct {
	Model   string         `json:"model,omitempty"`
	Effort  string         `json:"effort,omitempty"`
	Runtime map[string]any `json:"runtime,omitempty"`
}

// NewHandler 创建处理器。
func NewHandler(in handlerIn) *Handler {
	logger := in.Logger
	if logger == nil {
		logger = pkglogger.Get()
	}
	handler := &Handler{
		registry:       in.Registry,
		emitter:        in.Emitter,
		resolver:       in.Resolver,
		diffFallback:   in.DiffFallback,
		bindingStore:   in.BindingStore,
		threadStore:    in.ThreadStore,
		preferences:    in.Preferences,
		cfg:            in.Config,
		logger:         logger,
		tracer:         in.Tracer,
		dispatcher:     in.Dispatcher,
		hostTools:      in.HostTools,
		skillTools:     in.SkillTools,
		surfaces:       make(map[string]*codexToolSurface),
		proxyAuthToken: newProxyAuthToken(),
	}
	handler.stdioClientFactory = handler.defaultStdioClientFactory
	return handler
}

// HandleToolCall 处理工具call。
func (h *Handler) HandleToolCall(ctx context.Context, msg contract.ToolCallRawMessage) (result any, err error) {
	req, err := decodeToolCallRequest(msg.Params)
	if err != nil {
		return nil, err
	}
	traceReq := req
	if strings.TrimSpace(traceReq.CallID) == "" {
		traceReq.CallID = callIDFromRawJSONRPCID(msg.ID)
	}
	traceReq = normalizeToolCallRequest(traceReq)
	ctx = beginToolTraceContext(ctx)
	started := time.Now()
	h.recordToolTrace(ctx, toolTraceBeginEvent(traceReq))
	defer func() {
		h.recordToolTrace(ctx, toolTraceEndEvent(traceReq, result, err, time.Since(started), 0))
	}()
	surfaceReq := traceReq
	if result, handled, routeErr := h.routeCodexSurfaceToolCall(ctx, surfaceReq); handled || routeErr != nil {
		return result, routeErr
	}
	return h.routeToolCall(ctx, req)
}

// routeToolCall 处理route工具call。
func (h *Handler) routeToolCall(ctx context.Context, req ToolCallRequest) (*ToolCallResult, error) {
	req = normalizeToolCallRequest(req)
	if result, handled, err := h.routeReservedHostOnlyToolCall(ctx, req); handled || err != nil {
		return result, err
	}
	switch strings.TrimSpace(req.Name) {
	case ToolNameReadSection, ToolNameLegacySkillExpandBody, ToolNameLegacySkillReadResource:
		return removedSkillToolResult(req.Name), nil
	}
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
	peer, err := h.selectActiveToolPeer(mcpcontrol.ToolScope{
		AgentID:  req.AgentID,
		ThreadID: req.ThreadID,
		TurnID:   req.TurnID,
		CallID:   req.CallID,
		CWD:      req.CWD,
		Family:   clientKind,
	})
	if err != nil {
		return nil, err
	}
	return h.callPeerTool(ctx, peer.Peer, req)
}

func (h *Handler) routeReservedHostOnlyToolCall(ctx context.Context, req ToolCallRequest) (*ToolCallResult, bool, error) {
	toolName, reserved := reservedHostOnlyToolCanonicalName(req.Name)
	if !reserved {
		return nil, false, nil
	}
	req.Name = toolName
	switch toolName {
	case ToolNameMemoryRead:
		result, err := h.routeHostOnlyToolCall(ctx, req, "reader_unavailable")
		return result, true, err
	case ToolNameMemoryWrite:
		result, err := h.routeHostOnlyToolCall(ctx, req, "writer_unavailable")
		return result, true, err
	case ToolNameObservabilityTraceGet:
		result, err := h.routeHostOnlyToolCall(ctx, req, "trace_unavailable")
		return result, true, err
	default:
		return nil, false, nil
	}
}

func (h *Handler) routeHostOnlyToolCall(ctx context.Context, req ToolCallRequest, errCode string) (*ToolCallResult, error) {
	if h != nil && h.hostTools != nil && h.hostTools.HasTool(req.Name) {
		return h.callHostTool(ctx, req)
	}
	return hostToolErrorResult(req, contract.NewAgentMemoryError(errCode, fmt.Errorf("%s is not configured", strings.TrimSpace(req.Name)))), nil
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

func (h *Handler) selectActiveToolPeer(scope mcpcontrol.ToolScope) (*mcpcontrol.ToolInstance, error) {
	peers := h.findActiveToolPeers(scope)
	switch len(peers) {
	case 0:
		return nil, ErrNoPeerAvailable
	case 1:
		return peers[0], nil
	default:
		return nil, ErrAmbiguousPeer
	}
}

func (h *Handler) findActiveToolPeers(scope mcpcontrol.ToolScope) []*mcpcontrol.ToolInstance {
	if scoped, ok := h.registry.(scopedActivePeerRegistry); ok {
		return scoped.FindActiveForScope(scope)
	}
	return h.registry.FindActiveByKind(scope.Family)
}

func (h *Handler) callPeerTool(ctx context.Context, peer mcpcontrol.Peer, req ToolCallRequest) (*ToolCallResult, error) {
	callCtx, cancel := platformconfig.WithPeerTimeout(ctx, toolCallTimeout)
	defer cancel()

	snapshot := h.beginToolDiffSnapshot(ctx, req)
	req = h.injectManagedLaunchContext(ctx, req)
	h.warnManagedLaunchConfigTrace(ctx, req)
	cwd := h.resolveAndWarnCurrentToolCallCWD(ctx, req)
	var resp peerToolCallResponse
	err := peer.Callback(callCtx, ProxyMethodToolsCall, map[string]any{
		"name":                    req.Name,
		"arguments":               req.Arguments,
		MetadataKeyAgentID:        req.AgentID,
		MetadataKeyThreadID:       req.ThreadID,
		MetadataKeyCallID:         req.CallID,
		MetadataKeyCWD:            cwd,
		MetadataKeyWorkspaceRoots: append([]string(nil), req.WorkspaceRoots...),
	}, &resp)
	if err != nil {
		return toolCallErrorResult(err.Error()), nil
	}

	result, err := adaptMCPResponse(resp)
	if err != nil {
		return nil, err
	}
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

func toolCallErrorResult(text string) *ToolCallResult {
	return toolCallTextResult(false, text)
}

// Managed launch context injection helpers live in handler_managed_launch.go.

// resolveCurrentToolCallBinding 解析当前工具callbinding。
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

// spawnAgentPolicyMessage 在工具转发前决定是否阻断原生 spawn_agent。
// 这里同时覆盖子 agent 再委派和 persistent_subagent_default 的旧策略。
func (h *Handler) spawnAgentPolicyMessage(ctx context.Context, req ToolCallRequest) (string, error) {
	if strings.TrimSpace(req.Name) != "spawn_agent" {
		return "", nil
	}
	if blocked, err := h.childAgentDelegationPolicyMessage(ctx, req); err != nil || blocked != "" {
		return blocked, err
	}
	required, err := h.persistentSubagentRequired(ctx, req)
	if err != nil {
		return "", err
	}
	if !required {
		return "", nil
	}
	return "当前会话启用了 persistent_subagent_default：禁止使用 `spawn_agent` 创建临时子 agent。请改用 `launch_agent` 创建持续化 UI 子 agent；等待单个子 agent 用 `get_agent_report(wait=true)`，等待多个子 agent 用 `get_agent_reports(wait=true)`。", nil
}

// childAgentDelegationPolicyMessage 拦截子 agent 的原生 spawn_agent 再委派。
// binding 里有 parent_agent_id 才说明当前调用者已经是子 agent；没有绑定投影时维持后续 runtime policy。
func (h *Handler) childAgentDelegationPolicyMessage(ctx context.Context, req ToolCallRequest) (string, error) {
	binding, ok := h.resolveCurrentToolCallBinding(ctx, req)
	if !ok || strings.TrimSpace(binding.ParentAgentID) == "" {
		return "", nil
	}
	return subAgentDelegationDepthLimitMessage, nil
}

// persistentSubagentRequired 处理persistentsubagent必需。
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
	managedAvailable, known := runtimeHasManagedLaunchTool(runtime)
	if known {
		return managedAvailable, nil
	}
	return true, nil
}

func runtimeHasManagedLaunchTool(runtime map[string]any) (bool, bool) {
	shortAvailable, shortKnown := runtimeHasTool(runtime, "launch_agent")
	legacyAvailable, legacyKnown := runtimeHasTool(runtime, "orchestration_launch_agent")
	if shortKnown || legacyKnown {
		return shortAvailable || legacyAvailable, true
	}
	return false, false
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

// resolveToolCallThreadIDFromAgent 从代理解析工具call线程ID。
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

// persistentSubagentFlagFromRuntime 从运行时处理persistentsubagentflag。
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

// runtimeHasTool 处理运行时has工具。
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

// Peer-list/decode helpers live in handler_peer_decode.go.
// Host-direct tool listing/call helpers live in handler_host_tools.go.
