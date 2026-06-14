package codexapp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
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

const fallbackBaseInstructions = "You are a helpful assistant."

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
		ID  string `json:"id"`
		Cwd string `json:"cwd"`
	} `json:"thread"`
	Model         string `json:"model"`
	ModelProvider string `json:"modelProvider"`
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

func (f *DriverFactory) SetListTools(fn func(context.Context) ([]codexprotocol.DynamicToolSchema, error)) {
	if f == nil {
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.listTools = fn
}

func (f *DriverFactory) SetPrepareTools(fn func(context.Context, contract.CodexToolSurfaceScope) ([]codexprotocol.DynamicToolSchema, error)) {
	if f == nil {
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.prepareTools = fn
}

func (f *DriverFactory) SetReleaseTools(fn func(contract.CodexToolSurfaceScope) error) {
	if f == nil {
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.releaseTools = fn
}

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

func (d *driver) Name() string { return "codex" }

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
	s.releaseTools = d.releaseTools
	// P22 P1c: explicit runtime start. newSession no longer spawns
	// reader / health goroutines, so StartSession is the sole production
	// launch point for this session's runtime handle. Start BEFORE any
	// subsequent transport.Call (startDynamicSession dispatches RPCs whose
	// responses require the runtime-owned reader to be live).
	if s.runtime != nil {
		s.runtime.Start()
	}
	baseInstructions, developerInstructions := d.startAssemblyInstructions(req)
	s.setRuntimeConfig(canonicalStartRuntimeConfig(req.Config))
	s.ensureRuntimeCodexHomeFromInitialize("start")
	s.setRuntimeConfigValue("baseInstructions", baseInstructions)
	if developerInstructions != "" {
		s.setRuntimeConfigValue("developerInstructions", developerInstructions)
	}
	startPolicy := codexNativeToolPolicyFromConfig(req.Config)
	approvalPolicy := supportutil.ResolveApprovalPolicy(req.Config)
	if startPolicy.RequiresReadOnlySandbox() {
		approvalPolicy = "never"
	}
	s.setApprovalPolicy(approvalPolicy)
	return d.startDynamicSession(ctx, s, req)
}

func (d *driver) ResumeSession(ctx context.Context, req dto.ResumeSessionRequest) (contract.Session, error) {
	var err error
	req, err = d.prepareResumeSessionRequest(ctx, req)
	if err != nil {
		return nil, err
	}
	opts, err := d.resolveResumeOptions(ctx, req)
	if err != nil {
		return nil, err
	}
	s, err := newSessionWithOptions(ctx, d.logger, d.serverURL, req.AgentID, d.eventDispatcher, d.approvals, d.manager, opts...)
	if err != nil {
		return nil, err
	}
	s.releaseTools = d.releaseTools
	// P22 P1c: explicit runtime start BEFORE resumeRemoteThread; the latter
	// issues a thread/resume RPC whose response lands via the runtime-owned
	// reader. If resume fails below, cleanupFailedSession → ForceStop →
	// runtime.Stop idempotently drains the runtime.
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
	config := cloneCodexConfigMap(req.Config)
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
	resumeID := shared.FirstNonEmpty(req.ProviderThreadID, req.ThreadID)
	params := buildThreadResumeParams(req)
	params.ThreadID = strings.TrimSpace(resumeID)
	raw, err := callWithTimeout(ctx, t, 30*time.Second, "thread/resume", params)
	if err != nil {
		return "", err
	}
	return decodeThreadID(raw, resumeID)
}

func (d *driver) startAssemblyInstructions(req dto.StartSessionRequest) (string, string) {
	base := strings.TrimSpace(shared.FirstNonEmpty(
		req.StartAssembly.BaseInstructions,
		req.StartAssembly.Snapshot.BaseInstructions,
		req.Instructions,
	))
	developer := strings.TrimSpace(shared.FirstNonEmpty(
		req.StartAssembly.DeveloperInstructions,
		req.StartAssembly.Snapshot.DeveloperInstructions,
		supportutil.ConfigString(req.Config, "developerInstructions"),
		supportutil.ConfigString(req.Config, "developer_instructions"),
	))
	if base == "" {
		base = fallbackBaseInstructions
	}
	base = contract.AppendStartRuntimeContext(base, req.StartAssembly)
	return base, developer
}

func promptSnapshotInstructions(snapshot dto.PromptAssemblySnapshot) (string, string) {
	return strings.TrimSpace(snapshot.BaseInstructions), strings.TrimSpace(snapshot.DeveloperInstructions)
}

func buildThreadResumeParams(req dto.ResumeSessionRequest) threadResumeParams {
	baseInstructions, developerInstructions := promptSnapshotInstructions(req.PromptSnapshot)
	params := threadResumeParams{
		Cwd:                   strings.TrimSpace(req.CWD),
		Model:                 strings.TrimSpace(req.Model),
		BaseInstructions:      baseInstructions,
		DeveloperInstructions: developerInstructions,
		Effort:                strings.TrimSpace(req.Effort),
	}
	codexNativeToolPolicyFromDisabled(req.CodexDisabledNativeTools).ApplyThreadResumeParams(&params)
	return params
}

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
