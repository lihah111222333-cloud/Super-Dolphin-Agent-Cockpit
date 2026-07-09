package codexapp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"maps"
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

// DriverFactory 持有创建 Codex provider driver 所需的运行时依赖。
// tool surface 回调可在 fx 装配后注入，Create 会读取当前回调并隔离到新 driver。
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
		NativeTools: codexNativeToolDescriptors(),
	}

	return factory
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
	startPolicy, err := codexNativeToolPolicyFromConfig(req.Config)
	if err != nil {
		return nil, err
	}
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
	return d.finishOrCleanupResumedSession(ctx, s, req, threadID)
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
