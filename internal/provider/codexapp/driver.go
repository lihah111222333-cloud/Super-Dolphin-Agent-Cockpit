package codexapp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"strings"
	"sync"
	"time"

	contract "github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
	dto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/provider"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/rpc"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/shared"
	codexprotocol "github.com/lihah111222333-cloud/super-dolphin-agent/internal/provider/codexapp/protocol"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/provider/codexapp/supportutil"
	providershared "github.com/lihah111222333-cloud/super-dolphin-agent/internal/provider/shared"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/provider/unified"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/util/ctxutil"
	pkglogger "github.com/lihah111222333-cloud/super-dolphin-agent/pkg/logger"
	"github.com/lihah111222333-cloud/super-dolphin-agent/pkg/skillmetrics"
)

var errApprovalManagerRequired = errors.New("codexapp: approval manager is required")

func requireApprovalManager(approvals *rpc.ApprovalManager) error {
	if approvals == nil {
		return errApprovalManagerRequired
	}
	return nil
}

func runtimeHooksOrZero(configured []providershared.RuntimeHooks) (providershared.RuntimeHooks, error) {
	switch len(configured) {
	case 0:
		return providershared.RuntimeHooks{}, nil
	case 1:
		return configured[0], nil
	default:
		return providershared.RuntimeHooks{}, errors.New("codexapp: exactly one runtime hooks owner is allowed")
	}
}

// DriverFactory 持有创建 Codex provider driver 所需的运行时依赖。
// tool surface 回调可在 fx 装配后注入，Create 会读取当前回调并隔离到新 driver。
type DriverFactory struct {
	contract.DriverFactory
	mu              sync.RWMutex
	logger          *slog.Logger
	logRuntime      *pkglogger.Runtime
	eventDispatcher *unified.EventDispatcher
	approvals       *rpc.ApprovalManager
	reporter        contract.RuntimeReporter
	manager         *ServerManager
	pool            *ServerPool
	listTools       func(context.Context) ([]codexprotocol.DynamicToolSchema, error)
	prepareTools    func(context.Context, contract.CodexToolSurfaceScope) ([]codexprotocol.DynamicToolSchema, error)
	bindTools       func(contract.CodexToolSurfaceScope) error
	releaseTools    func(contract.CodexToolSurfaceScope) error
	recovery        contract.SessionRecoveryReporter
	runtimeHooks    providershared.RuntimeHooks
	skillMetrics    *skillmetrics.Registry
}

type driver struct {
	logger          *slog.Logger
	logRuntime      *pkglogger.Runtime
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
	recovery        contract.SessionRecoveryReporter
	runtimeHooks    providershared.RuntimeHooks
	skillMetrics    *skillmetrics.Registry
}

var _ contract.Driver = (*driver)(nil)

// codexCapabilities 构造 Codex app 当前暴露给上层 runtime 的能力集合。
// 每次调用返回独立 map，避免 package-level mutable state 成为跨会话共享 owner。
func codexCapabilities() dto.CapabilitySet {
	return dto.CapabilitySet{
		dto.CapMessageSend:    true,
		dto.CapThreadList:     true,
		dto.CapThreadFork:     true,
		dto.CapContextCompact: true,
		dto.CapTurnOverride:   true,
		dto.CapModelSwitch:    true,
	}
}

type threadRPCResult struct {
	Thread struct {
		ID string `json:"id"`
	} `json:"thread"`
	Model          string          `json:"model"`
	ApprovalPolicy json.RawMessage `json:"approvalPolicy"`
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
	SandboxPolicy         json.RawMessage                   `json:"sandboxPolicy,omitempty"`
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
	recovery contract.SessionRecoveryReporter,
	runtimeHooks ...providershared.RuntimeHooks,
) (*DriverFactory, error) {
	hooks, err := runtimeHooksOrZero(runtimeHooks)
	if err != nil {
		return nil, err
	}
	factory := &DriverFactory{
		logger:          logger,
		eventDispatcher: dispatcher,
		approvals:       approvals,
		reporter:        reporter,
		manager:         manager,
		pool:            pool,
		recovery:        recovery,
		runtimeHooks:    hooks,
	}
	factory.DriverFactory = contract.DriverFactory{
		Name: "codex",
		Create: func() contract.Driver {
			raw := newDriver(factory.logRuntime, logger, dispatcher, approvals, reporter, manager, pool, factory.recovery, factory.currentListTools())
			if d, ok := raw.(*driver); ok {
				d.logRuntime = factory.logRuntime
				d.runtimeHooks = factory.runtimeHooks
				d.skillMetrics = factory.currentSkillMetrics()
				d.prepareTools = factory.currentPrepareTools()
				d.bindTools = factory.currentBindTools()
				d.releaseTools = factory.currentReleaseTools()
			}
			return raw
		},
		NativeTools: codexNativeToolDescriptors(),
	}

	return factory, nil
}

// Name 返回 provider 注册名。
func (d *driver) Name() string { return "codex" }

// StartSession 准备 Codex home、工具面和 runtime 后启动新线程会话。
// runtime 必须先于 start RPC 启动，否则 app-server 响应没有 reader 接收。
func (d *driver) StartSession(ctx context.Context, req dto.StartSessionRequest) (contract.Session, error) {
	// LaunchSkillRefs is a compatibility carrier; sanitize legacy Prompt bodies
	// even when callers bypass the unified client and invoke this driver directly.
	req.LaunchSkillRefs = contract.CloneProviderSkillRefsMetadataOnly(req.LaunchSkillRefs)
	if err := requireApprovalManager(d.approvals); err != nil {
		return nil, err
	}
	if err := contract.ValidateLegacyMemoryPromptSections(req.StartAssembly.ResolvedSections); err != nil {
		return nil, err
	}
	if err := contract.ValidateLegacyMemoryPromptSnapshot(req.StartAssembly.Snapshot); err != nil {
		return nil, err
	}
	if !codexPromptSnapshotBlank(req.StartAssembly.Snapshot) {
		if err := contract.ValidateResumePromptSnapshotForProvider(req.StartAssembly.Snapshot, d.Name()); err != nil {
			return nil, fmt.Errorf("codexapp: start prompt snapshot: %w", err)
		}
	}
	return d.startSession(ctx, req)
}

// startSession 在依赖校验后执行请求准备、会话构造和远端线程启动。
func (d *driver) startSession(ctx context.Context, req dto.StartSessionRequest) (contract.Session, error) {
	req, nativeContext, err := d.prepareCodexStartBoundary(ctx, req)
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
	opts = append(opts, withRuntimeHooks(d.runtimeHooks), withSkillMetrics(d.skillMetrics), withLogRuntime(d.logRuntime))
	s, err := newSessionWithOptions(ctx, d.logger, d.serverURL, req.AgentID, d.eventDispatcher, d.approvals, d.manager, opts...)
	if err != nil {
		return nil, err
	}
	s.prepareTools, s.listTools, s.releaseTools = d.prepareTools, d.listTools, d.releaseTools
	s.dynamicToolsEnabled = contract.ToolSurfaceModeUsesDynamicTools(req.ToolSurfaceMode)
	s.setNativeContext(nativeContext)
	// 先记录已校验的 snapshot，再启动 recovery worker。否则 initialize/
	// thread/start 期间若 reader 短暂断链，worker 可能在 snapshot 尚未写入
	// 时重连，绕过同一条 provider owner/version/legacy guard。
	s.setPromptSnapshot(req.StartAssembly.Snapshot)
	baseInstructions, developerInstructions, err := d.startAssemblyInstructions(req)
	if err != nil {
		cleanupFailedSession(s, "force stop failed on start prompt assembly error")
		return nil, err
	}
	// recovery 期间的 thread/resume 不再从 runtimeConfig 猜测 prompt；此前已
	// 记录校验过的 snapshot，后续每次 reconnect 重新执行 owner/version/legacy guard。
	// prepareCodexStartBoundary 已将 req.CWD canonicalize；与 runtime config 一次性 hydrate，使 recovery 的
	// validateRecoveryNativeContext 不会在 hydrate 完成前看到空 cwd。
	s.setRuntimeConfig(canonicalRuntimeConfigWithCWD(req.Config, req.CWD))
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
	startPolicy, err := codexNativeToolPolicyFromConfig(req.Config)
	if err != nil {
		cleanupFailedSession(s, "force stop failed on native tool policy error")
		return nil, err
	}
	approvalPolicy := supportutil.ResolveApprovalPolicy(req.Config)
	if startPolicy.RequiresReadOnlySandbox() {
		approvalPolicy = "never"
	}
	s.setApprovalPolicy(approvalPolicy)
	// runtime 拥有 reader、health 和恢复 goroutine；任何后续 RPC 都要求 reader 已经在线。
	// 必须在启动 worker 前先完成 runtimeConfig/cwd hydrate，否则启动阶段的 recovery 会在
	// validateRecoveryNativeContext 中读到空 cwd 而错拒合法的 native context。
	if s.runtime != nil {
		s.runtime.Start()
	}
	return d.startDynamicSession(ctx, s, req)
}

// ResumeSession 按已持久化的 Codex identity 恢复远端线程。
// 恢复失败会清理新建 session 并清掉过期 provider thread 绑定，避免下一次继续读错历史。
func (d *driver) ResumeSession(ctx context.Context, req dto.ResumeSessionRequest) (contract.Session, error) {
	if err := requireApprovalManager(d.approvals); err != nil {
		return nil, err
	}
	return d.resumeSession(ctx, req)
}

// resumeSession 在依赖校验后恢复远端线程并重建动态工具面。
func (d *driver) resumeSession(ctx context.Context, req dto.ResumeSessionRequest) (contract.Session, error) {
	req, nativeContext, err := d.prepareCodexResumeBoundary(ctx, req)
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
	opts = append(opts, withRuntimeHooks(d.runtimeHooks), withSkillMetrics(d.skillMetrics), withLogRuntime(d.logRuntime))
	s, err := newSessionWithOptions(ctx, d.logger, d.serverURL, req.AgentID, d.eventDispatcher, d.approvals, d.manager, opts...)
	if err != nil {
		return nil, err
	}
	s.prepareTools, s.listTools, s.releaseTools = d.prepareTools, d.listTools, d.releaseTools
	s.dynamicToolsEnabled = contract.ToolSurfaceModeUsesDynamicTools(toolSurfaceMode)
	s.setNativeContext(nativeContext)
	// resume 请求已经通过完整 snapshot owner/version 校验；保存副本供
	// reconnect 的 thread/resume 路径继续执行同一边界检查。
	s.setPromptSnapshot(req.PromptSnapshot)
	s.setRuntimeConfig(canonicalRuntimeConfigWithCWD(req.Config, req.CWD))
	primeResumeToolScope(s, req)
	// resume RPC 的响应也依赖 runtime-owned reader；失败时 ForceStop 会幂等 drain runtime。
	// 在启动 worker 之前先写入 canonical cwd 和 provider thread ID，使得早到的 recovery 信号能通过
	// validateRecoveryNativeContext，同时保持 canonical/mismatch 校验不变。
	if s.runtime != nil {
		s.runtime.Start()
	}
	resume, err := resumeRemoteThread(ctx, s.transport, req)
	if err != nil {
		cleanupFailedSession(s, "force stop failed on resume error")
		d.clearStaleProviderThreadID(req.AgentID, "codexapp: clear stale binding failed")
		return nil, err
	}
	if err := d.rebuildResumeToolSurface(ctx, s, req, resume.threadID); err != nil {
		cleanupFailedSession(s, "force stop failed on resume tool surface error")
		return nil, err
	}
	return d.finishOrCleanupResumedSession(ctx, s, req, resume)
}

// ResolveResumeSessionIdentity 为恢复请求补齐 Codex home、instance key 和 model provider。
// 恢复路径不能退回默认身份；任何类型错误或 home 解析失败都会阻断，避免读错历史线程。
func (d *driver) ResolveResumeSessionIdentity(_ context.Context, req dto.ResumeSessionRequest) (dto.ResumeSessionRequest, error) {
	if err := validateCodexProviderNativeSkillsConfig(req.Config); err != nil {
		return req, err
	}
	if err := validateStartCodexIdentityShape(req.Config); err != nil {
		return req, err
	}
	config := resumeCodexIdentityConfig(req)
	requestedHome := supportutil.ConfigString(config, contract.CodexHomeKey)
	providerHome, err := selectCodexProviderHome(requestedHome)
	if err != nil {
		return req, err
	}
	home, err := ensureResolvedCodexProviderHome(providerHome)
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

type resumeResult struct {
	threadID       string
	approvalPolicy string
}

func resumeRemoteThread(ctx context.Context, t *transport, req dto.ResumeSessionRequest) (resumeResult, error) {
	resumeID, err := requireProviderResumeThreadID("codexapp", req.ProviderThreadID)
	if err != nil {
		return resumeResult{}, err
	}
	params, err := buildThreadResumeParams(req)
	if err != nil {
		return resumeResult{}, err
	}
	params.ThreadID = resumeID
	raw, err := callWithTimeout(ctx, t, 30*time.Second, "thread/resume", params)
	if err != nil {
		return resumeResult{}, err
	}
	return decodeResumeResult(raw)
}

func requireProviderResumeThreadID(component, providerThreadID string) (string, error) {
	providerThreadID = strings.TrimSpace(providerThreadID)
	if providerThreadID == "" {
		return "", fmt.Errorf("%s: provider thread id is required", component)
	}
	return providerThreadID, nil
}

// startAssemblyInstructions 校验启动快照并组装 base/developer 指令；旧 memory 槽位
// 必须在 thread/start 之前拒绝，避免恢复时再次穿透 provider-native 边界。
func (d *driver) startAssemblyInstructions(req dto.StartSessionRequest) (string, string, error) {
	if err := contract.ValidateLegacyMemoryPromptSections(req.StartAssembly.ResolvedSections); err != nil {
		return "", "", err
	}
	if err := contract.ValidateLegacyMemoryPromptSnapshot(req.StartAssembly.Snapshot); err != nil {
		return "", "", err
	}
	if !codexPromptSnapshotBlank(req.StartAssembly.Snapshot) {
		if err := contract.ValidateResumePromptSnapshotForProvider(req.StartAssembly.Snapshot, d.Name()); err != nil {
			return "", "", fmt.Errorf("codexapp: start prompt snapshot: %w", err)
		}
	}
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
	if err := contract.ValidateLegacyMemoryPromptSnapshot(req.PromptSnapshot); err != nil {
		return threadResumeParams{}, err
	}
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
	policy, err := codexNativeToolPolicyFromDisabled(req.CodexDisabledNativeTools)
	if err != nil {
		return threadResumeParams{}, err
	}
	policy.ApplyThreadResumeParams(&params)
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

func decodeThreadID(raw json.RawMessage) (string, error) {
	resp, err := decodeThreadRPCResult(raw)
	if err != nil {
		return "", err
	}
	id := strings.TrimSpace(resp.Thread.ID)
	if id == "" {
		return "", errors.New("codexapp: empty thread id")
	}
	return id, nil
}

func decodeResumeResult(raw json.RawMessage) (resumeResult, error) {
	resp, err := decodeThreadRPCResult(raw)
	if err != nil {
		return resumeResult{}, err
	}
	id := strings.TrimSpace(resp.Thread.ID)
	if id == "" {
		return resumeResult{}, errors.New("codexapp: empty thread id")
	}
	approvalPolicy, err := decodeResumeApprovalPolicy(resp.ApprovalPolicy)
	if err != nil {
		return resumeResult{}, err
	}
	return resumeResult{threadID: id, approvalPolicy: approvalPolicy}, nil
}

// decodeResumeApprovalPolicy 校验 thread/resume 返回的权威审批策略，并保留 granular 对象的规范 JSON。
func decodeResumeApprovalPolicy(raw json.RawMessage) (string, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return "", errors.New("codexapp: thread/resume approvalPolicy is required")
	}
	var policy string
	if err := json.Unmarshal(raw, &policy); err == nil {
		policy = strings.TrimSpace(policy)
		switch policy {
		case "untrusted", "on-request", "never":
			return policy, nil
		default:
			return "", fmt.Errorf("codexapp: thread/resume contains unknown approvalPolicy %q", policy)
		}
	}
	return decodeGranularResumeApprovalPolicy(raw)
}

// decodeGranularResumeApprovalPolicy 严格校验 granular 审批对象的必需布尔字段。
func decodeGranularResumeApprovalPolicy(raw json.RawMessage) (string, error) {
	var granular struct {
		Granular *struct {
			MCPElicitations *bool `json:"mcp_elicitations"`
			Rules           *bool `json:"rules"`
			SandboxApproval *bool `json:"sandbox_approval"`
		} `json:"granular"`
	}
	if err := json.Unmarshal(raw, &granular); err != nil || granular.Granular == nil {
		return "", errors.New("codexapp: thread/resume approvalPolicy has invalid shape")
	}
	if granular.Granular.MCPElicitations == nil || granular.Granular.Rules == nil || granular.Granular.SandboxApproval == nil {
		return "", errors.New("codexapp: thread/resume granular approvalPolicy is incomplete")
	}
	var compact bytes.Buffer
	if err := json.Compact(&compact, raw); err != nil {
		return "", errors.New("codexapp: thread/resume granular approvalPolicy is invalid")
	}
	return compact.String(), nil
}
