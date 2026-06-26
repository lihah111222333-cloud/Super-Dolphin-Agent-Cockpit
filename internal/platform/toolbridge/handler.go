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

// Handler 字段只依赖 ports.go 中的窄接口，避免 toolbridge 反向导入具体 store。
// 生产适配器统一放在 module.go 装配，保证平台层与持久化层的依赖方向清晰。
const allowDefaultPersistentSubagentEnv = "TOOLBRIDGE_ALLOW_DEFAULT_PERSISTENT_SUBAGENT"
const subAgentDelegationDepthLimitMessage = "Sub-agents are not allowed to spawn further agents (delegation depth limit)."

// persistentSubagentDefaultFallbackTotal 统计兼容 fallback 被触发的次数，便于后续移除旧路径。
var persistentSubagentDefaultFallbackTotal atomic.Uint64

// Handler 负责把模型侧 tool call 路由到 host-direct、Codex surface 或外部 MCP peer。
// 结构体持有的 store、registry 与 hostTools 都是可选边界，调用路径必须显式处理缺失依赖。
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

// activePeerRegistry 是 toolbridge 选择活跃 MCP peer 所需的最小注册表接口。
type activePeerRegistry interface {
	FindActiveByKind(clientKind string) []*mcpcontrol.ToolInstance
}

// scopedActivePeerRegistry 支持按 agent/thread/call 作用域选择 peer，避免同类 peer 冲突。
type scopedActivePeerRegistry interface {
	FindActiveForScope(scope mcpcontrol.ToolScope) []*mcpcontrol.ToolInstance
}

// storedThreadRuntime 是 thread config override 中保存的运行时片段。
type storedThreadRuntime struct {
	Model   string         `json:"model,omitempty"`
	Effort  string         `json:"effort,omitempty"`
	Runtime map[string]any `json:"runtime,omitempty"`
}

// NewHandler 创建 toolbridge 路由器，并为可注入的 stdio client factory 设置默认实现。
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

// HandleToolCall 是 JSON-RPC tool call 的入口。
// 它先建立 trace，再按 Codex surface、host-direct、peer 的顺序路由，defer 统一记录结束事件。
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

// routeToolCall 是非 Codex surface 调用进入 toolbridge 后的分流边界。
// host-direct 保留工具先在本进程执行，已下线 skill 工具返回明确失败；其余调用经过
// 本地策略拦截后才选择 scoped peer。proxy 入口和直接入口共用这里的错误语义，
// peer 错误转模型文本、edit diff 归因则交给 callPeerTool 统一处理。
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

// routeReservedHostOnlyToolCall 兜住必须由 host-direct 执行的保留工具名。
// 这些工具不能被 peer shadow，否则 stale peer 可能绕过本地权限、cwd 或 trace 边界。
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

// routeHostOnlyToolCall 调用 host-direct 工具；未装配 registry 时返回结构化不可用错误。
func (h *Handler) routeHostOnlyToolCall(ctx context.Context, req ToolCallRequest, errCode string) (*ToolCallResult, error) {
	if h != nil && h.hostTools != nil && h.hostTools.HasTool(req.Name) {
		return h.callHostTool(ctx, req)
	}
	return hostToolErrorResult(req, contract.NewAgentMemoryError(errCode, fmt.Errorf("%s is not configured", strings.TrimSpace(req.Name)))), nil
}

// routePrePeerToolCall 在进入 peer 前执行本地策略和 host-direct 去重。
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

// selectActiveToolPeer 选择唯一活跃 peer；0 个或多个都按错误返回，避免隐式降级。
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

// findActiveToolPeers 优先使用 scoped registry，旧 registry 只按 client family 选择。
func (h *Handler) findActiveToolPeers(scope mcpcontrol.ToolScope) []*mcpcontrol.ToolInstance {
	if scoped, ok := h.registry.(scopedActivePeerRegistry); ok {
		return scoped.FindActiveForScope(scope)
	}
	return h.registry.FindActiveByKind(scope.Family)
}

// callPeerTool 向选中的 MCP peer 发起 tools/call，并在 edit 调用前后记录 diff。
// 这里会注入 managed launch 上下文与解析后的 cwd，保证 peer 收到的是完整调用元数据。
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

// toolCallTextResult 构造文本型 ToolCallResult，供本地拦截和错误分支复用。
func toolCallTextResult(success bool, text string) *ToolCallResult {
	return &ToolCallResult{
		Success: success,
		ContentItems: []ToolCallContentItem{{
			Type: "inputText",
			Text: text,
		}},
	}
}

// toolCallErrorResult 构造失败文本结果，保持 peer callback 错误不向 JSON-RPC 外层冒泡。
func toolCallErrorResult(text string) *ToolCallResult {
	return toolCallTextResult(false, text)
}

// managed launch 参数注入相关 helper 位于 handler_managed_launch.go。

// resolveCurrentToolCallBinding 从 agentID、threadID 和 provider thread 中恢复当前 tool call 绑定。
// 该绑定用于继承 managed launch 上下文；解析不到时调用方按无绑定路径继续。
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

// lookupToolCallBindingByAgent 读取 agent 绑定，并要求返回值仍带有有效 agentID。
func lookupToolCallBindingByAgent(ctx context.Context, lookup toolCallBindingLookup, agentID string) (toolCallBinding, bool) {
	binding, err := lookup.GetBindingByAgent(ctx, strings.TrimSpace(agentID))
	if err != nil {
		return toolCallBinding{}, false
	}
	return binding, strings.TrimSpace(binding.AgentID) != ""
}

// lookupToolCallBindingByProviderThread 用 provider thread 兜住旧数据中的绑定关系。
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

// persistentSubagentRequired 判断当前 runtime 是否强制要求 persistent subagent。
// 缺失 session flag 默认 fail-fast；只有显式环境变量打开时才使用旧配置兼容路径。
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

// runtimeHasManagedLaunchTool 判断 runtime 工具列表是否显式暴露持久化子 agent 启动工具。
func runtimeHasManagedLaunchTool(runtime map[string]any) (bool, bool) {
	shortAvailable, shortKnown := runtimeHasTool(runtime, "launch_agent")
	legacyAvailable, legacyKnown := runtimeHasTool(runtime, "orchestration_launch_agent")
	if shortKnown || legacyKnown {
		return shortAvailable || legacyAvailable, true
	}
	return false, false
}

// requireToolCallRuntimeConfig 读取当前 tool call 的 thread runtime；缺失 threadID 或 runtime 都返回错误。
func (h *Handler) requireToolCallRuntimeConfig(ctx context.Context, req ToolCallRequest) (map[string]any, error) {
	threadID, err := h.requireToolCallThreadID(ctx, req)
	if err != nil {
		return nil, err
	}
	return h.requireToolCallRuntime(ctx, threadID)
}

// toolCallRuntimeConfig 是允许静默缺失的读取入口，仅供非强制策略探测使用。
func (h *Handler) toolCallRuntimeConfig(ctx context.Context, req ToolCallRequest) (map[string]any, bool) {
	runtime, err := h.requireToolCallRuntimeConfig(ctx, req)
	if err != nil {
		return nil, false
	}
	return runtime, true
}

// requireToolCallThreadID 要求工具调用能解析到 threadID，否则策略无法读取 runtime。
func (h *Handler) requireToolCallThreadID(ctx context.Context, req ToolCallRequest) (string, error) {
	threadID, ok := h.resolveToolCallThreadID(ctx, req)
	if !ok {
		return "", contract.ErrThreadRuntimeRequired
	}
	return threadID, nil
}

// requireToolCallRuntime 要求指定 thread 的 runtime 存在，缺失时阻断策略判断。
func (h *Handler) requireToolCallRuntime(ctx context.Context, threadID string) (map[string]any, error) {
	runtime, ok := h.readToolCallRuntime(ctx, threadID)
	if !ok {
		return nil, contract.ErrPersistentSubagentRuntimeRequired
	}
	return runtime, nil
}

// resolveToolCallThreadID 优先使用请求中的 threadID，缺失时再从 agent 绑定反查。
func (h *Handler) resolveToolCallThreadID(ctx context.Context, req ToolCallRequest) (string, bool) {
	if threadID, ok := resolveToolCallThreadIDFromRequest(req); ok {
		return threadID, true
	}
	return h.resolveToolCallThreadIDFromAgent(ctx, req)
}

// resolveToolCallThreadIDFromRequest 从请求体中读取显式 threadID。
func resolveToolCallThreadIDFromRequest(req ToolCallRequest) (string, bool) {
	threadID := strings.TrimSpace(req.ThreadID)
	if threadID == "" {
		return "", false
	}
	return threadID, true
}

// resolveToolCallThreadIDFromAgent 通过 agentID 反查 threadID，供旧调用或缺省参数路径使用。
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

// readToolCallRuntime 读取 thread config override 中的 runtime 段。
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

// decodeToolArguments 将 tool call arguments 解为对象；空参数或非对象都返回 nil。
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

// mapString 从 map 中读取并清理字符串字段，类型不符时返回空字符串。
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

// setArgStringIfMissing 只在目标字段为空且新值非空时写入参数 map。
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

// allowDefaultPersistentSubagentFallback 判断是否允许使用旧的全局 persistent subagent 默认值。
func allowDefaultPersistentSubagentFallback() bool {
	return os.Getenv(allowDefaultPersistentSubagentEnv) == "1"
}

// persistentSubagentDefaultFallbackCount 返回兼容 fallback 次数，主要用于测试和观测。
func persistentSubagentDefaultFallbackCount() uint64 {
	return persistentSubagentDefaultFallbackTotal.Load()
}

// persistentSubagentFlagFromRuntime 从 runtime session flags 中读取 persistent subagent 开关。
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

// runtimeHasTool 判断 runtime 工具列表是否已知且包含目标工具名。
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

// normalizeToolbridgeSessionFlagName 折叠 session flag 名称差异，兼容 camel/snake/kebab 写法。
func normalizeToolbridgeSessionFlagName(name string) string {
	name = strings.ToLower(strings.TrimSpace(name))
	replacer := strings.NewReplacer("_", "", "-", "", " ", "")
	return replacer.Replace(name)
}

// Peer-list/decode helpers live in handler_peer_decode.go.
// Host-direct tool listing/call helpers live in handler_host_tools.go.
