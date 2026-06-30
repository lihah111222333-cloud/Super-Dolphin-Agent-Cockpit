package codexapp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"os"
	"strings"
	"sync"
	"time"

	contract "github.com/anthropic-ai/super-agent-v3/internal/contract"
	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/rpc"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/shared"
	codexprotocol "github.com/anthropic-ai/super-agent-v3/internal/provider/codexapp/protocol"
	"github.com/anthropic-ai/super-agent-v3/internal/provider/codexapp/supportutil"
	providershared "github.com/anthropic-ai/super-agent-v3/internal/provider/shared"
	"github.com/anthropic-ai/super-agent-v3/internal/provider/unified"
	"github.com/anthropic-ai/super-agent-v3/internal/util/ctxutil"
	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
)

type DriverFactory struct {
	contract.DriverFactory
	mu              sync.RWMutex
	logger          *slog.Logger
	eventDispatcher *unified.EventDispatcher
	approvals       *rpc.ApprovalManager
	reporter        contract.RuntimeReporter
	manager         *ServerManager
	pool            *ServerPool
	listTools       func(context.Context) ([]codexprotocol.DynamicToolSchema, error)
	prepareTools    func(context.Context, contract.CodexToolSurfaceScope) ([]codexprotocol.DynamicToolSchema, error)
	bindTools       func(contract.CodexToolSurfaceScope) error
	releaseTools    func(contract.CodexToolSurfaceScope) error
	mirror          contract.SkillMirrorReconciler
	recovery        contract.SessionRecoveryReporter
}

type driver struct {
	logger          *slog.Logger
	serverURL       string
	eventDispatcher *unified.EventDispatcher
	approvals       *rpc.ApprovalManager
	reporter        contract.RuntimeReporter
	manager         *ServerManager
	pool            *ServerPool
	listTools       func(context.Context) ([]codexprotocol.DynamicToolSchema, error)
	prepareTools    func(context.Context, contract.CodexToolSurfaceScope) ([]codexprotocol.DynamicToolSchema, error)
	bindTools       func(contract.CodexToolSurfaceScope) error
	releaseTools    func(contract.CodexToolSurfaceScope) error
	mirror          contract.SkillMirrorReconciler
	recovery        contract.SessionRecoveryReporter
}

var _ contract.Driver = (*driver)(nil)

var codexCapabilities = dto.CapabilitySet{
	dto.CapMessageSend:    true,
	dto.CapThreadList:     true,
	dto.CapThreadFork:     true,
	dto.CapContextCompact: true,
	dto.CapTurnOverride:   true,
	dto.CapModelSwitch:    true,
}

type threadRPCResult struct {
	Thread struct {
		ID string `json:"id"`
	} `json:"thread"`
	Model string `json:"model"`
}

type threadStartParams struct {
	Cwd                   string                            `json:"cwd,omitempty"`
	Model                 string                            `json:"model,omitempty"`
	ModelProvider         string                            `json:"modelProvider,omitempty"`
	BaseInstructions      string                            `json:"baseInstructions,omitempty"`
	DeveloperInstructions string                            `json:"developerInstructions,omitempty"`
	ApprovalPolicy        string                            `json:"approvalPolicy,omitempty"`
	Personality           string                            `json:"personality,omitempty"`
	Summary               string                            `json:"summary,omitempty"`
	Effort                string                            `json:"effort,omitempty"`
	Sandbox               json.RawMessage                   `json:"sandbox,omitempty"`
	MCPConfig             json.RawMessage                   `json:"mcpConfig,omitempty"`
	DynamicTools          []codexprotocol.DynamicToolSchema `json:"dynamicTools,omitempty"`
}

type threadResumeParams struct {
	ThreadID              string `json:"threadId"`
	Cwd                   string `json:"cwd,omitempty"`
	Model                 string `json:"model,omitempty"`
	BaseInstructions      string `json:"baseInstructions,omitempty"`
	ApprovalPolicy        string `json:"approvalPolicy,omitempty"`
	DeveloperInstructions string `json:"developerInstructions,omitempty"`
	Sandbox               string `json:"sandbox,omitempty"`
	Summary               string `json:"summary,omitempty"`
	Effort                string `json:"effort,omitempty"`
	Personality           string `json:"personality,omitempty"`
}

// NewDriverFactory 构造 Codex provider 的 DriverFactory。
// factory 持有可热替换的动态工具回调；每次 Create 都复制当前回调，避免会话间共享可变工具面。
func NewDriverFactory(
	logger *slog.Logger,
	dispatcher *unified.EventDispatcher,
	approvals *rpc.ApprovalManager,
	reporter contract.RuntimeReporter,
	manager *ServerManager,
	pool *ServerPool,
	mirror contract.SkillMirrorReconciler,
	recovery contract.SessionRecoveryReporter,
) *DriverFactory {
	factory := &DriverFactory{
		logger:          logger,
		eventDispatcher: dispatcher,
		approvals:       approvals,
		reporter:        reporter,
		manager:         manager,
		pool:            pool,
		mirror:          mirror,
		recovery:        recovery,
	}
	factory.DriverFactory = contract.DriverFactory{
		Name: "codex",
		Create: func() contract.Driver {
			raw := newDriver(logger, dispatcher, approvals, reporter, manager, pool, factory.mirror, factory.recovery, factory.currentListTools())
			if d, ok := raw.(*driver); ok {
				d.prepareTools = factory.currentPrepareTools()
				d.bindTools = factory.currentBindTools()
				d.releaseTools = factory.currentReleaseTools()
			}
			return raw
		},
		NativeTools: []contract.NativeToolDescriptor{
			{ID: contract.CodexNativeToolReadFile, Label: "直接读项目文件", Description: "绕过项目文件工具直接读取文件。", DefaultDisabled: true, Provider: "codex", FilterMode: contract.NativeToolFilterModeSoft},
			{ID: contract.CodexNativeToolWriteNewFile, Label: "直接新建文件", Description: "绕过项目文件编辑链路直接创建文件。", DefaultDisabled: true, Provider: "codex", FilterMode: contract.NativeToolFilterModeSoft},
			{ID: contract.CodexNativeToolApplyPatch, Label: "直接改文件", Description: "绕过项目文件编辑链路直接修改文件。", DefaultDisabled: true, Provider: "codex", FilterMode: contract.NativeToolFilterModeSoft},
			{ID: contract.CodexNativeToolShell, Label: "直接执行命令", Description: "绕过项目命令治理直接执行本地命令。", DefaultDisabled: true, Provider: "codex", FilterMode: contract.NativeToolFilterModeSoft},
			{ID: contract.CodexNativeToolListDir, Label: "直接列目录", Description: "绕过项目文件工具直接读取目录。", DefaultDisabled: true, Provider: "codex", FilterMode: contract.NativeToolFilterModeSoft},
			{ID: contract.CodexNativeToolMultiAgent, Label: "自行编排子任务", Description: "让 Codex 自己创建和管理子任务；本项目已有任务编排。", DefaultDisabled: true, Provider: "codex", FilterMode: contract.NativeToolFilterModeSoft},
			{ID: contract.CodexNativeToolMultiToolParallel, Label: "同时使用多个工具", Description: "让 Codex 一次使用多个内置工具；本项目已有工具调度。", DefaultDisabled: true, Provider: "codex", FilterMode: contract.NativeToolFilterModeSoft},
			{ID: contract.CodexNativeToolSpawnAgent, Label: "创建子任务", Description: "让 Codex 自己创建子任务；本项目已有任务编排。", DefaultDisabled: true, Provider: "codex", FilterMode: contract.NativeToolFilterModeSoft},
			{ID: contract.CodexNativeToolSendInput, Label: "给子任务发消息", Description: "绕过项目任务消息流直接给子任务发送输入。", DefaultDisabled: true, Provider: "codex", FilterMode: contract.NativeToolFilterModeSoft},
			{ID: contract.CodexNativeToolResumeAgent, Label: "恢复子任务", Description: "绕过项目任务生命周期直接恢复子任务。", DefaultDisabled: true, Provider: "codex", FilterMode: contract.NativeToolFilterModeSoft},
			{ID: contract.CodexNativeToolWaitAgent, Label: "等待子任务", Description: "绕过项目任务状态流直接等待子任务。", DefaultDisabled: true, Provider: "codex", FilterMode: contract.NativeToolFilterModeSoft},
			{ID: contract.CodexNativeToolCloseAgent, Label: "关闭子任务", Description: "绕过项目任务生命周期直接关闭子任务。", DefaultDisabled: true, Provider: "codex", FilterMode: contract.NativeToolFilterModeSoft},
			{ID: contract.CodexNativeToolToolSearch, Label: "自行发现工具", Description: "绕过项目工具清单自行发现可用工具。", DefaultDisabled: true, Provider: "codex", FilterMode: contract.NativeToolFilterModeSoft},
			{ID: contract.CodexNativeToolWebSearch, Label: "网页搜索", Description: "让模型自行搜索网页。", DefaultDisabled: false, Provider: "codex", FilterMode: contract.NativeToolFilterModeSoft},
			{ID: contract.CodexNativeToolImageGen, Label: "生成图片", Description: "让模型自行生成图片。", DefaultDisabled: false, Provider: "codex", FilterMode: contract.NativeToolFilterModeSoft},
			{ID: contract.CodexNativeToolViewImage, Label: "查看图片", Description: "让模型自行查看本地图片。", DefaultDisabled: false, Provider: "codex", FilterMode: contract.NativeToolFilterModeSoft},
			{ID: contract.CodexNativeToolRequestInput, Label: "向用户提问", Description: "绕过项目对话流直接向用户发起提问。", DefaultDisabled: true, Provider: "codex", FilterMode: contract.NativeToolFilterModeSoft},
			{ID: contract.CodexNativeToolRequestPerms, Label: "请求放行权限", Description: "绕过项目审批入口直接请求放行。", DefaultDisabled: true, Provider: "codex", FilterMode: contract.NativeToolFilterModeSoft},
			{ID: contract.CodexNativeToolPluginInstall, Label: "请求安装插件", Description: "绕过项目插件管理入口请求安装插件。", DefaultDisabled: true, Provider: "codex", FilterMode: contract.NativeToolFilterModeSoft},
			{ID: contract.CodexNativeToolListMCPResources, Label: "列出外部资源", Description: "绕过项目工具面直接读取外部资源列表。", DefaultDisabled: true, Provider: "codex", FilterMode: contract.NativeToolFilterModeSoft},
			{ID: contract.CodexNativeToolListMCPResourceTemplates, Label: "列出资源模板", Description: "绕过项目工具面直接读取外部资源模板。", DefaultDisabled: true, Provider: "codex", FilterMode: contract.NativeToolFilterModeSoft},
			{ID: contract.CodexNativeToolReadMCPResource, Label: "读取外部资源", Description: "绕过项目工具面直接读取外部资源内容。", DefaultDisabled: true, Provider: "codex", FilterMode: contract.NativeToolFilterModeSoft},
			{ID: contract.CodexNativeToolBrowserUse, Label: "操作内置浏览器", Description: "让模型自行操作内置浏览器。", DefaultDisabled: false, Provider: "codex", FilterMode: contract.NativeToolFilterModeSoft},
			{ID: contract.CodexNativeToolBrowserUseExternal, Label: "操作外部浏览器", Description: "让模型自行操作外部浏览器。", DefaultDisabled: false, Provider: "codex", FilterMode: contract.NativeToolFilterModeSoft},
			{ID: contract.CodexNativeToolComputerUse, Label: "操作本机应用", Description: "让模型自行操作本机应用。", DefaultDisabled: false, Provider: "codex", FilterMode: contract.NativeToolFilterModeSoft},
			{ID: contract.CodexNativeToolWorkspaceDeps, Label: "读取运行环境", Description: "绕过项目环境入口直接读取工作区运行环境。", DefaultDisabled: true, Provider: "codex", FilterMode: contract.NativeToolFilterModeSoft},
			{ID: contract.CodexNativeToolApps, Label: "使用连接器", Description: "绕过项目连接器管理直接使用连接器。", DefaultDisabled: true, Provider: "codex", FilterMode: contract.NativeToolFilterModeSoft},
			{ID: contract.CodexNativeToolPlugins, Label: "使用插件", Description: "绕过项目插件管理直接使用插件。", DefaultDisabled: true, Provider: "codex", FilterMode: contract.NativeToolFilterModeSoft},
			{ID: contract.CodexNativeToolGoals, Label: "自行管理目标", Description: "绕过项目任务视图自行管理目标。", DefaultDisabled: true, Provider: "codex", FilterMode: contract.NativeToolFilterModeSoft},
			{ID: contract.CodexNativeToolUpdatePlan, Label: "自行更新计划", Description: "绕过项目计划和任务视图自行更新计划。", DefaultDisabled: true, Provider: "codex", FilterMode: contract.NativeToolFilterModeSoft},
		},
	}

	return factory
}

// SetListTools 更新动态工具列表回调。
// 回调在锁内替换，后续创建的 driver 才会读取新值，已启动会话不被原地改写。
func (f *DriverFactory) SetListTools(fn func(context.Context) ([]codexprotocol.DynamicToolSchema, error)) {
	if f == nil {
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.listTools = fn
}

// SetPrepareTools 更新会话级动态工具准备回调。
// 该回调参与 Start/Resume 的工具面绑定，nil 表示当前运行环境不提供动态工具。
func (f *DriverFactory) SetPrepareTools(fn func(context.Context, contract.CodexToolSurfaceScope) ([]codexprotocol.DynamicToolSchema, error)) {
	if f == nil {
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.prepareTools = fn
}

// SetReleaseTools 更新工具面释放回调。
// session 关闭路径会调用它归还作用域资源，替换动作需持锁防止与 Create 竞态。
func (f *DriverFactory) SetReleaseTools(fn func(contract.CodexToolSurfaceScope) error) {
	if f == nil {
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.releaseTools = fn
}

// SetBindTools 更新恢复路径的工具面绑定回调。
// Resume 会用它把既有 thread scope 重新绑定到新 session。
func (f *DriverFactory) SetBindTools(fn func(contract.CodexToolSurfaceScope) error) {
	if f == nil {
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.bindTools = fn
}

func (f *DriverFactory) currentListTools() func(context.Context) ([]codexprotocol.DynamicToolSchema, error) {
	if f == nil {
		return nil
	}
	f.mu.RLock()
	defer f.mu.RUnlock()
	return f.listTools
}

func (f *DriverFactory) currentPrepareTools() func(context.Context, contract.CodexToolSurfaceScope) ([]codexprotocol.DynamicToolSchema, error) {
	if f == nil {
		return nil
	}
	f.mu.RLock()
	defer f.mu.RUnlock()
	return f.prepareTools
}

func (f *DriverFactory) currentBindTools() func(contract.CodexToolSurfaceScope) error {
	if f == nil {
		return nil
	}
	f.mu.RLock()
	defer f.mu.RUnlock()
	return f.bindTools
}

func (f *DriverFactory) currentReleaseTools() func(contract.CodexToolSurfaceScope) error {
	if f == nil {
		return nil
	}
	f.mu.RLock()
	defer f.mu.RUnlock()
	return f.releaseTools
}

// newDriver 创建单个 Codex driver 实例。
// 环境变量里的 app-server URL 优先；否则只在 legacy ServerManager 已运行时复用共享地址。
func newDriver(logger *slog.Logger, eventDispatcher *unified.EventDispatcher, approvals *rpc.ApprovalManager, reporter contract.RuntimeReporter, manager *ServerManager, pool *ServerPool, mirror contract.SkillMirrorReconciler, recovery contract.SessionRecoveryReporter, listTools ...func(context.Context) ([]codexprotocol.DynamicToolSchema, error)) contract.Driver {
	if logger == nil {
		logger = pkglogger.Get()
	}
	serverURL := strings.TrimSpace(os.Getenv("CODEX_APP_SERVER_URL"))
	if serverURL == "" && manager != nil && manager.Running() {
		serverURL = manager.ServerURL()
	}
	var listToolsFn func(context.Context) ([]codexprotocol.DynamicToolSchema, error)
	if len(listTools) != 0 {
		listToolsFn = listTools[0]
	}
	return &driver{
		logger:          logger,
		serverURL:       serverURL,
		eventDispatcher: eventDispatcher,
		approvals:       approvals,
		reporter:        reporter,
		manager:         manager,
		pool:            pool,
		listTools:       listToolsFn,
		mirror:          mirror,
		recovery:        recovery,
	}
}

// Name 返回 provider 注册名。
func (d *driver) Name() string { return "codex" }

// StartSession 准备 Codex home、工具面和 runtime 后启动新线程会话。
// runtime 必须先于 start RPC 启动，否则 app-server 响应没有 reader 接收。
func (d *driver) StartSession(ctx context.Context, req dto.StartSessionRequest) (contract.Session, error) {
	var err error
	req, err = d.prepareStartSessionRequest(ctx, req)
	if err != nil {
		return nil, err
	}
	req.ToolSurfaceMode, err = contract.NormalizeToolSurfaceMode(req.ToolSurfaceMode)
	if err != nil {
		return nil, fmt.Errorf("codexapp: tool surface mode: %w", err)
	}
	opts, err := d.resolveSessionOptions(ctx, req)
	if err != nil {
		return nil, err
	}
	s, err := newSessionWithOptions(ctx, d.logger, d.serverURL, req.AgentID, d.eventDispatcher, d.approvals, d.manager, opts...)
	if err != nil {
		return nil, err
	}
	s.prepareTools, s.listTools, s.releaseTools = d.prepareTools, d.listTools, d.releaseTools
	s.dynamicToolsEnabled = contract.ToolSurfaceModeUsesDynamicTools(req.ToolSurfaceMode)
	// runtime 拥有 reader、health 和恢复 goroutine；任何后续 RPC 都要求 reader 已经在线。
	if s.runtime != nil {
		s.runtime.Start()
	}
	baseInstructions, developerInstructions, err := d.startAssemblyInstructions(req)
	if err != nil {
		cleanupFailedSession(s, "force stop failed on start prompt assembly error")
		return nil, err
	}
	s.setRuntimeConfig(canonicalStartRuntimeConfig(req.Config))
	s.ensureRuntimeCodexHomeFromInitialize("start")
	s.setRuntimeConfigValue("baseInstructions", baseInstructions)
	if developerInstructions != "" {
		s.setRuntimeConfigValue("developerInstructions", developerInstructions)
	}
	pkglogger.Debug("codexapp: start prompt prefix shape",
		"agent_id", req.AgentID,
		"prefix_hash", req.StartAssembly.PrefixShape.Hash,
		"static_sections", req.StartAssembly.PrefixShape.StaticSectionNames,
		"dynamic_sections", req.StartAssembly.PrefixShape.DynamicSectionNames,
		"cached_prefix_bytes", req.StartAssembly.PrefixShape.CachedPrefixBytes,
		"uncached_tail_bytes", req.StartAssembly.PrefixShape.UncachedTailBytes,
	)
	startPolicy := codexNativeToolPolicyFromConfig(req.Config)
	approvalPolicy := supportutil.ResolveApprovalPolicy(req.Config)
	if startPolicy.RequiresReadOnlySandbox() {
		approvalPolicy = "never"
	}
	s.setApprovalPolicy(approvalPolicy)
	return d.startDynamicSession(ctx, s, req)
}

// ResumeSession 按已持久化的 Codex identity 恢复远端线程。
// 恢复失败会清理新建 session 并清掉过期 provider thread 绑定，避免下一次继续读错历史。
func (d *driver) ResumeSession(ctx context.Context, req dto.ResumeSessionRequest) (contract.Session, error) {
	var err error
	req.ProviderThreadID, err = requireProviderResumeThreadID("codexapp", req.ProviderThreadID)
	if err != nil {
		return nil, err
	}
	if err := contract.ValidateResumePromptSnapshot(req.PromptSnapshot); err != nil {
		return nil, fmt.Errorf("codexapp: %w", err)
	}
	req, err = d.prepareResumeSessionRequest(ctx, req)
	if err != nil {
		return nil, err
	}
	toolSurfaceMode, err := contract.NormalizeToolSurfaceMode(supportutil.ConfigString(req.Config, "toolSurfaceMode", "tool_surface_mode"))
	if err != nil {
		return nil, fmt.Errorf("codexapp: tool surface mode: %w", err)
	}
	opts, err := d.resolveResumeOptions(ctx, req)
	if err != nil {
		return nil, err
	}
	s, err := newSessionWithOptions(ctx, d.logger, d.serverURL, req.AgentID, d.eventDispatcher, d.approvals, d.manager, opts...)
	if err != nil {
		return nil, err
	}
	s.prepareTools, s.listTools, s.releaseTools = d.prepareTools, d.listTools, d.releaseTools
	s.dynamicToolsEnabled = contract.ToolSurfaceModeUsesDynamicTools(toolSurfaceMode)
	// resume RPC 的响应也依赖 runtime-owned reader；失败时 ForceStop 会幂等 drain runtime。
	if s.runtime != nil {
		s.runtime.Start()
	}
	s.setRuntimeConfig(canonicalStartRuntimeConfig(req.Config))
	primeResumeToolScope(s, req)
	threadID, err := resumeRemoteThread(ctx, s.transport, req)
	if err != nil {
		cleanupFailedSession(s, "force stop failed on resume error")
		d.clearStaleProviderThreadID(req.AgentID, "codexapp: clear stale binding failed")
		return nil, err
	}
	if err := d.rebuildResumeToolSurface(ctx, s, req, threadID); err != nil {
		cleanupFailedSession(s, "force stop failed on resume tool surface error")
		return nil, err
	}
	return d.finishResumedSession(ctx, s, req, threadID), nil
}

// ResolveResumeSessionIdentity 为恢复请求补齐 Codex home、instance key 和 model provider。
// 恢复路径不能退回默认身份；任何类型错误或 home 解析失败都会阻断，避免读错历史线程。
func (d *driver) ResolveResumeSessionIdentity(_ context.Context, req dto.ResumeSessionRequest) (dto.ResumeSessionRequest, error) {
	if err := validateStartCodexIdentityShape(req.Config); err != nil {
		return req, err
	}
	config := resumeCodexIdentityConfig(req)
	requestedHome := supportutil.ConfigString(config, contract.CodexHomeKey)
	providerHome, err := selectCodexProviderHome(requestedHome)
	if err != nil {
		return req, err
	}
	home, _, err := ensureResolvedCodexProviderHome(providerHome)
	if err != nil {
		return req, err
	}
	config, err = withDefaultCodexIdentity(config, home, defaultCodexModelProviderForHome(providerHome))
	if err != nil {
		return req, err
	}
	identity, err := providershared.ResolveCodexIdentity(config)
	if err != nil {
		return req, err
	}
	req.CodexHome = identity.Home
	req.CodexInstanceKey = identity.InstanceKey
	req.CodexModelProvider = identity.ModelProvider
	req.Config = config
	return req, nil
}

func resumeCodexIdentityConfig(req dto.ResumeSessionRequest) map[string]any {
	config := maps.Clone(req.Config)
	if config == nil {
		config = make(map[string]any, 3)
	}
	if value := strings.TrimSpace(req.CodexHome); value != "" {
		config[contract.CodexHomeKey] = value
	}
	if value := strings.TrimSpace(req.CodexInstanceKey); value != "" {
		config[contract.CodexInstanceKeyKey] = value
	}
	if value := strings.TrimSpace(req.CodexModelProvider); value != "" {
		config[contract.CodexModelProviderKey] = value
	}
	return config
}

func (d *driver) clearStaleProviderThreadID(agentID, message string) {
	if d == nil || d.recovery == nil {
		return
	}
	cleanCtx, cancel := ctxutil.WithSessionCloseTimeout(context.Background())
	defer cancel()
	if err := d.recovery.ClearStaleProviderThreadID(cleanCtx, agentID); err != nil && d.logger != nil {
		d.logger.Warn(message, "agent_id", strings.TrimSpace(agentID), "error", err)
	}
}

// AllowedModels 从 app-server 查询当前 Codex provider 可用模型。
// 解码失败会直接返回错误，调用方不能拿空列表当作成功降级。
func (s *session) AllowedModels(ctx context.Context) ([]string, error) {
	raw, err := callWithTimeout(ctx, callTargetFunc(s.callTransport), 10*time.Second, "model/list", map[string]any{})
	if err != nil {
		return nil, err
	}
	models, err := supportutil.DecodeAllowedModels(raw)
	if err != nil {
		return nil, err
	}
	return models, nil
}

type startResult struct {
	threadID string
	model    string
}

func resumeRemoteThread(ctx context.Context, t *transport, req dto.ResumeSessionRequest) (string, error) {
	resumeID, err := requireProviderResumeThreadID("codexapp", req.ProviderThreadID)
	if err != nil {
		return "", err
	}
	params, err := buildThreadResumeParams(req)
	if err != nil {
		return "", err
	}
	params.ThreadID = resumeID
	raw, err := callWithTimeout(ctx, t, 30*time.Second, "thread/resume", params)
	if err != nil {
		return "", err
	}
	return decodeThreadID(raw, resumeID)
}

func requireProviderResumeThreadID(component, providerThreadID string) (string, error) {
	providerThreadID = strings.TrimSpace(providerThreadID)
	if providerThreadID == "" {
		return "", fmt.Errorf("%s: provider thread id is required", component)
	}
	return providerThreadID, nil
}

func (d *driver) startAssemblyInstructions(req dto.StartSessionRequest) (string, string, error) {
	base := strings.TrimSpace(shared.FirstNonEmpty(
		req.StartAssembly.BaseInstructions,
		promptSnapshotBaseInstructions(req.StartAssembly.Snapshot),
		req.Instructions,
	))
	developer := strings.TrimSpace(shared.FirstNonEmpty(
		req.StartAssembly.DeveloperInstructions,
		req.StartAssembly.Snapshot.DeveloperInstructions,
		supportutil.ConfigString(req.Config, "developerInstructions"),
		supportutil.ConfigString(req.Config, "developer_instructions"),
	))
	if base == "" {
		return "", "", errors.New("codexapp: start prompt assembly is empty: base instructions or prompt snapshot are required")
	}
	base = contract.AppendStartRuntimeContext(base, req.StartAssembly)
	return base, developer, nil
}

func promptSnapshotBaseInstructions(snapshot dto.PromptAssemblySnapshot) string {
	if boundary := normalizePromptBoundary(snapshot.Boundary); boundary != nil {
		return joinPromptBlocks(boundary.CachedPrefix, boundary.UncachedTail)
	}
	return strings.TrimSpace(snapshot.BaseInstructions)
}

func promptSnapshotInstructions(snapshot dto.PromptAssemblySnapshot) (string, string) {
	return promptSnapshotBaseInstructions(snapshot), strings.TrimSpace(snapshot.DeveloperInstructions)
}

func buildThreadResumeParams(req dto.ResumeSessionRequest) (threadResumeParams, error) {
	baseInstructions, developerInstructions := promptSnapshotInstructions(req.PromptSnapshot)
	if strings.TrimSpace(baseInstructions) == "" {
		return threadResumeParams{}, errors.New("codexapp: resume prompt snapshot has empty base instructions")
	}
	params := threadResumeParams{
		Cwd:                   strings.TrimSpace(req.CWD),
		Model:                 strings.TrimSpace(req.Model),
		BaseInstructions:      baseInstructions,
		DeveloperInstructions: developerInstructions,
		Effort:                strings.TrimSpace(req.Effort),
	}
	codexNativeToolPolicyFromDisabled(req.CodexDisabledNativeTools).ApplyThreadResumeParams(&params)
	return params, nil
}

func normalizePromptBoundary(boundary *dto.PromptAssemblyBoundary) *dto.PromptAssemblyBoundary {
	if boundary == nil {
		return nil
	}
	out := &dto.PromptAssemblyBoundary{
		CachedPrefix: strings.TrimSpace(boundary.CachedPrefix),
		UncachedTail: strings.TrimSpace(boundary.UncachedTail),
	}
	if out.CachedPrefix == "" && out.UncachedTail == "" {
		return nil
	}
	return out
}

func joinPromptBlocks(values ...string) string {
	blocks := make([]string, 0, len(values))
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			blocks = append(blocks, value)
		}
	}
	return strings.Join(blocks, "\n\n")
}

// codexSandboxWireJSON 将 Codex sandbox wire 值规整成 app-server 接受的 JSON。
// 字符串和对象两种历史形态都兼容；无法识别时原样返回，让下游按真实输入报错。
func codexSandboxWireJSON(raw json.RawMessage) json.RawMessage {
	raw = json.RawMessage(strings.TrimSpace(string(raw)))
	if len(raw) == 0 {
		return nil
	}
	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		if mode := canonicalCodexSandboxMode(text); mode != "" {
			return mustJSON(mode)
		}
		return raw
	}
	var obj map[string]any
	if err := json.Unmarshal(raw, &obj); err != nil {
		return raw
	}
	if mode := sandboxModeObjectValue(obj); mode != "" {
		return mustJSON(mode)
	}
	if len(obj) == 1 {
		for key := range obj {
			if mode := canonicalCodexSandboxMode(key); mode != "" {
				return mustJSON(mode)
			}
		}
	}
	return raw
}

func sandboxModeObjectValue(obj map[string]any) string {
	for _, key := range []string{"mode", "type"} {
		value, _ := obj[key].(string)
		if mode := canonicalCodexSandboxMode(value); mode != "" {
			return mode
		}
	}
	return ""
}

func canonicalCodexSandboxMode(value string) string {
	key := strings.NewReplacer("-", "", "_", "").Replace(strings.ToLower(strings.TrimSpace(value)))
	switch key {
	case "readonly":
		return "read-only"
	case "workspacewrite":
		return "workspace-write"
	case "dangerfullaccess":
		return "danger-full-access"
	default:
		return ""
	}
}

func hasAnyConfigKey(cfg map[string]any, keys ...string) bool {
	return hasAnyKey(cfg, keys...)
}

func decodeStartResult(raw json.RawMessage) (startResult, error) {
	resp, err := decodeThreadRPCResult(raw)
	if err != nil {
		return startResult{}, err
	}
	id := strings.TrimSpace(resp.Thread.ID)
	if id == "" {
		return startResult{}, errors.New("codexapp: empty thread id")
	}
	return startResult{
		threadID: id,
		model:    strings.TrimSpace(resp.Model),
	}, nil
}

func decodeThreadID(raw json.RawMessage, fallback string) (string, error) {
	if resp, err := decodeThreadRPCResult(raw); err == nil {
		if id := strings.TrimSpace(resp.Thread.ID); id != "" {
			return id, nil
		}
	}
	if fallback = strings.TrimSpace(fallback); fallback != "" {
		return fallback, nil
	}
	return "", errors.New("codexapp: empty thread id")
}
