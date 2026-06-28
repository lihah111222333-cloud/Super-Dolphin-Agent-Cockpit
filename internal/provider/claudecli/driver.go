package claudecli

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"

	platformconfig "github.com/anthropic-ai/super-agent-v3/internal/platform/config"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/observability"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/pidregistry"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/shared"
	"github.com/anthropic-ai/super-agent-v3/internal/provider/manifestbuilder"
	providershared "github.com/anthropic-ai/super-agent-v3/internal/provider/shared"
	"github.com/anthropic-ai/super-agent-v3/internal/provider/unified"
	"github.com/anthropic-ai/super-agent-v3/internal/util/ctxutil"
	"github.com/anthropic-ai/super-agent-v3/internal/util/identifier"
	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
)

// claudeCapabilities 描述 Claude provider 当前暴露给上层 runtime 的能力。
var claudeCapabilities = dto.CapabilitySet{
	dto.CapMessageSend:  true,
	dto.CapModelSwitch:  true,
	dto.CapTurnOverride: true,
}

// driver 是 Claude CLI provider 的 Driver 实现，负责启动 CLI、维护 runtime 观测和 skill mirror。
type driver struct {
	logger          *slog.Logger
	binaryPath      string
	eventDispatcher *unified.EventDispatcher
	reporter        contract.RuntimeReporter
	pidRegistry     *pidregistry.Registry
	proxyAddrFn     func() string
	proxyTokenFn    func() string
	mirror          contract.SkillMirrorReconciler
	recovery        contract.SessionRecoveryReporter
	tracer          *observability.Service
	launchCLI       func(string, string, string, string, cliLaunchConfig, dto.MCPManifest, string) (*transport, func(), error)
	authStatus      func(context.Context, string, string, cliLaunchConfig) (claudeAuthStatus, string, error)
}

// startSpec 聚合启动或恢复会话所需的规范化参数，避免 StartSession/ResumeSession 分叉后逻辑重复。
type startSpec struct {
	agentID        string
	threadID       string
	publicThread   string
	cwd            string
	model          string
	startAssembly  contract.StartAssembly
	manifest       dto.MCPManifest
	config         cliLaunchConfig
	rawConfig      map[string]any
	historyDir     string
	configOverride dto.ThreadConfigPatch
}

// preparedStartSession 是启动 CLI 成功后的中间产物；调用方仍需等待 thread ready。
type preparedStartSession struct {
	history        *historyBackend
	requestedModel string
	launchModel    string
	launchConfig   cliLaunchConfig
	transport      *transport
	cleanup        func()
}

// restartSnapshot 保存重启前可恢复的 session 状态，失败时用于回滚 transport 与 watcher。
type restartSnapshot struct {
	transport         *transport
	cleanup           func()
	watcher           *sessionLogWatcher
	ready             chan struct{}
	readyClosed       bool
	transportModel    string
	transportConfig   cliLaunchConfig
	transportManifest dto.MCPManifest
	contextWindow     int
}

// preparedSessionRestart 保存已准备好的重启结果，等待锁内提交或回滚。
type preparedSessionRestart struct {
	transport  *transport
	cleanup    func()
	patch      dto.RawProviderEvent
	waitCtx    context.Context
	generation uint64
	snapshot   restartSnapshot
}

// proxyHTTPAddr 返回当前 toolbridge proxy 地址；未装配时保持空字符串。
func (d *driver) proxyHTTPAddr() string {
	if d == nil || d.proxyAddrFn == nil {
		return ""
	}
	return strings.TrimSpace(d.proxyAddrFn())
}

// proxyHTTPToken 返回当前 toolbridge proxy bearer token；未装配时保持空字符串。
func (d *driver) proxyHTTPToken() string {
	if d == nil || d.proxyTokenFn == nil {
		return ""
	}
	return strings.TrimSpace(d.proxyTokenFn())
}

// newDriver 创建 Claude CLI driver，并注入可替换的启动、认证和观测依赖。
func newDriver(logger *slog.Logger, eventDispatcher *unified.EventDispatcher, reporter contract.RuntimeReporter, reg *pidregistry.Registry, proxyAddrFn func() string, proxyTokenFn func() string, mirror contract.SkillMirrorReconciler, recovery contract.SessionRecoveryReporter, tracers ...*observability.Service) contract.Driver {
	if logger == nil {
		logger = pkglogger.Get()
	}
	if proxyAddrFn == nil {
		proxyAddrFn = func() string { return "" }
	}
	if proxyTokenFn == nil {
		proxyTokenFn = func() string { return "" }
	}
	return &driver{
		logger:          logger,
		binaryPath:      resolveBinaryPath(),
		eventDispatcher: eventDispatcher,
		reporter:        reporter,
		pidRegistry:     reg,
		proxyAddrFn:     proxyAddrFn,
		proxyTokenFn:    proxyTokenFn,
		mirror:          mirror,
		recovery:        recovery,
		tracer:          firstClaudeTracer(tracers),
		launchCLI:       launchCLIWithManifest,
		authStatus:      runClaudeAuthStatus,
	}
}

// firstClaudeTracer 取第一个可用 tracer，便于 Fx 多参数注入时保持兼容。
func firstClaudeTracer(tracers []*observability.Service) *observability.Service {
	if len(tracers) == 0 {
		return nil
	}
	return tracers[0]
}

// Name 返回 provider 标识。
func (d *driver) Name() string { return "claude" }

// StartSession 启动新的 Claude CLI 会话，并为其构建 stdio-only MCP manifest。
func (d *driver) StartSession(ctx context.Context, req dto.StartSessionRequest) (contract.Session, error) {
	if err := validateClaudeSecurityConfig(req.Config); err != nil {
		return nil, fmt.Errorf("claudecli: security config: %w", err)
	}
	launchConfig := configFromMap(req.Config)
	extraBinaries, err := providershared.ConfigMCPBinaries(req.Config, "mcpConfig", "mcp_config")
	if err != nil {
		return nil, fmt.Errorf("claudecli: mcp config: %w", err)
	}
	manifest := manifestbuilder.BuildManifest(dto.ManifestContext{
		AgentID:  strings.TrimSpace(req.AgentID),
		ThreadID: strings.TrimSpace(req.AgentID),
		CWD:      strings.TrimSpace(req.CWD),
		AdditionalWorkingDirectories: providershared.ConfigStringSlice(req.Config,
			"additionalWorkingDirectories", "additional_working_directories"),
		ThreadCaps:     copyCapabilities(claudeCapabilities),
		BinaryDir:      providershared.ResolveBinaryDir(req.CWD, req.Config),
		Env:            providershared.StringMap(req.Config["env"]),
		AutoApprove:    providershared.ConfigStringSlice(req.Config, "auto_approve", "autoApprove"),
		ExtraBinaries:  extraBinaries,
		ProxyHTTPAddr:  d.proxyHTTPAddr(),
		ProxyHTTPToken: d.proxyHTTPToken(),
		TransportMode:  dto.ManifestTransportStdioOnly,
	})
	historyDir := providershared.ConfigString(req.Config, "history_dir", "claude_home", "claudeHome")
	return d.start(ctx, startSpec{
		agentID:       req.AgentID,
		cwd:           req.CWD,
		model:         req.Model,
		startAssembly: resolveStartAssembly(req, launchConfig, d.Name()),
		manifest:      manifest,
		config:        launchConfig,
		rawConfig:     cloneConfigMap(req.Config),
		publicThread:  req.AgentID,
		historyDir:    historyDir,
	})
}

// ResumeSession 基于已持久化的 prompt snapshot 和 provider thread 恢复 Claude CLI 会话。
func (d *driver) ResumeSession(ctx context.Context, req dto.ResumeSessionRequest) (contract.Session, error) {
	snapshot := req.PromptSnapshot
	rawConfig := resumeSessionRuntimeConfig(req)
	if err := validateClaudeSecurityConfig(rawConfig); err != nil {
		return nil, fmt.Errorf("claudecli: security config: %w", err)
	}
	launchConfig := configFromMap(rawConfig)
	launchConfig.Effort = strings.TrimSpace(req.Effort)
	launchConfig.PromptSnapshot = snapshot
	extraBinaries, err := providershared.ConfigMCPBinaries(rawConfig, "mcpConfig", "mcp_config")
	if err != nil {
		return nil, fmt.Errorf("claudecli: mcp config: %w", err)
	}
	manifest := manifestbuilder.BuildManifest(dto.ManifestContext{
		AgentID:  strings.TrimSpace(req.AgentID),
		ThreadID: strings.TrimSpace(req.ThreadID),
		CWD:      strings.TrimSpace(req.CWD),
		AdditionalWorkingDirectories: providershared.ConfigStringSlice(rawConfig,
			"additionalWorkingDirectories", "additional_working_directories"),
		ThreadCaps:     copyCapabilities(claudeCapabilities),
		BinaryDir:      providershared.ResolveBinaryDir(req.CWD, rawConfig),
		Env:            providershared.StringMap(rawConfig["env"]),
		AutoApprove:    providershared.ConfigStringSlice(rawConfig, "auto_approve", "autoApprove"),
		ExtraBinaries:  extraBinaries,
		ProxyHTTPAddr:  d.proxyHTTPAddr(),
		ProxyHTTPToken: d.proxyHTTPToken(),
		TransportMode:  dto.ManifestTransportStdioOnly,
	})
	return d.start(ctx, startSpec{
		agentID:      req.AgentID,
		threadID:     shared.FirstNonEmpty(req.ProviderThreadID, req.ThreadID),
		publicThread: req.ThreadID,
		cwd:          req.CWD,
		model:        req.Model,
		startAssembly: contract.StartAssembly{
			DisplayName:           strings.TrimSpace(snapshot.DisplayName),
			BaseInstructions:      strings.TrimSpace(snapshot.BaseInstructions),
			DeveloperInstructions: strings.TrimSpace(snapshot.DeveloperInstructions),
			Snapshot:              snapshot,
		},
		manifest:       manifest,
		config:         launchConfig,
		historyDir:     req.ClaudeHome,
		configOverride: req.ConfigOverride,
		rawConfig:      rawConfig,
	})
}

// resumeSessionRuntimeConfig 合并恢复请求 runtime 配置，并确保 cwd 能被后续启动链路读取。
func resumeSessionRuntimeConfig(req dto.ResumeSessionRequest) map[string]any {
	cfg := cloneConfigMap(req.Config)
	if cfg == nil {
		cfg = map[string]any{}
	}
	putRuntimeConfigStringIfMissing(cfg, "cwd", req.CWD)
	if len(cfg) == 0 {
		return nil
	}
	return cfg
}

// start 串起 provider home、skill mirror、CLI 启动、thread ready 等关键步骤。
// 任一阶段失败都会直接返回错误，不发布半启动 session。
func (d *driver) start(ctx context.Context, spec startSpec) (session contract.Session, err error) {
	traceStarted := time.Now()
	defer func() {
		d.recordDriverTrace(ctx, claudeSessionEvent("provider.session.acquire", spec, time.Since(traceStarted), err))
		if err == nil && session != nil {
			d.recordDriverTrace(ctx, claudeSessionEvent("provider.session.ready", spec, time.Since(traceStarted), nil))
		}
	}()
	if err := shared.CheckCtx(ctx); err != nil {
		return nil, err
	}
	if err := validateStartCWD(spec.cwd); err != nil {
		return nil, err
	}
	spec, err = d.prepareProviderHomeAndMirrors(ctx, spec)
	if err != nil {
		return nil, err
	}
	started, err := d.prepareSessionStart(ctx, spec)
	if err != nil {
		return nil, err
	}
	s := d.newStartedSession(spec, started)
	if err := d.awaitStartedSession(ctx, s, started.transport); err != nil {
		return nil, err
	}
	d.dispatchStartEvents(s, started.launchModel)
	d.reportRuntime(s.agentID)
	return s, nil
}

// prepareSessionStart 校验 cwd、规范化模型配置、完成认证预检并启动 CLI transport。
func (d *driver) prepareSessionStart(ctx context.Context, spec startSpec) (preparedStartSession, error) {
	if err := validateStartCWD(spec.cwd); err != nil {
		return preparedStartSession{}, err
	}
	history := &historyBackend{sessionDir: spec.historyDir}
	requestedModel, requestedConfig := resolveRequestedStartConfig(spec)
	requestedConfig.DeveloperInstructions = promptDeveloperInstructions(cliLaunchConfig{
		DeveloperInstructions: requestedConfig.DeveloperInstructions,
		PromptSnapshot:        spec.startAssembly.Snapshot,
	})
	requestedConfig.PromptSnapshot = spec.startAssembly.Snapshot
	launchModel := claudeLaunchDisplayModel(requestedModel, history)
	launchConfig := canonicalizeClaudeLaunchConfig(launchModel, requestedConfig)
	if launchConfig.ClaudeHome == "" {
		launchConfig.ClaudeHome = strings.TrimSpace(spec.historyDir)
	}
	if err := d.preflightClaudeAuth(ctx, d.binaryPath, spec.cwd, launchConfig); err != nil {
		return preparedStartSession{}, err
	}
	tr, cleanup, err := d.launchCLI(
		d.binaryPath,
		spec.cwd,
		requestedModel,
		promptBaseInstructions(spec.startAssembly.BaseInstructions, launchConfig.PromptSnapshot),
		launchConfig,
		spec.manifest,
		spec.threadID,
	)
	if err != nil {
		return preparedStartSession{}, err
	}
	return preparedStartSession{
		history:        history,
		requestedModel: requestedModel,
		launchModel:    launchModel,
		launchConfig:   launchConfig,
		transport:      tr,
		cleanup:        cleanup,
	}, nil
}

// prepareProviderHomeAndMirrors 准备 Claude home 并同步 provider-native skill mirror。
// mirror 是启动前硬依赖，冲突会 fail-fast，避免 CLI 读取到过期或重复技能。
func (d *driver) prepareProviderHomeAndMirrors(ctx context.Context, spec startSpec) (startSpec, error) {
	requestedHome := strings.TrimSpace(spec.historyDir)
	mirrorHome := ""
	if requestedHome != "" {
		home, err := providershared.EnsureProviderHome(providershared.ProviderClaude, requestedHome)
		if err != nil {
			return spec, err
		}
		spec.historyDir = home
		mirrorHome = home
	}
	if d == nil || d.mirror == nil {
		return spec, errors.New("claude skill mirror reconciler is required")
	}
	targets, err := providershared.ProviderMirrorTargets(providershared.ProviderClaude, spec.cwd, mirrorHome)
	if err != nil {
		return spec, err
	}
	report, err := d.mirror.ReconcileProviderMirrors(ctx, spec.cwd, targets)
	if err != nil {
		return spec, err
	}
	if err := providershared.EnsureNoSkillMirrorConflicts(report); err != nil {
		return spec, err
	}
	return spec, nil
}

// validateStartCWD 校验 Claude CLI 必须在存在的目录内启动。
func validateStartCWD(cwd string) error {
	cwd = strings.TrimSpace(cwd)
	if cwd == "" {
		return errors.New("claudecli: cwd is required")
	}
	info, err := os.Stat(cwd)
	if err != nil {
		return fmt.Errorf("claudecli: cwd stat %q: %w", cwd, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("claudecli: cwd %q is not a directory", cwd)
	}
	return nil
}

// resolveRequestedStartConfig 将请求模型和 pending override 合并为本次启动配置。
func resolveRequestedStartConfig(spec startSpec) (string, cliLaunchConfig) {
	requestedModel := sanitizeClaudeModel(spec.model)
	if requestedModel == "" && spec.configOverride.Model != nil {
		requestedModel = sanitizeClaudeModel(*spec.configOverride.Model)
	}
	requestedConfig := spec.config
	if strings.TrimSpace(requestedConfig.Effort) == "" && spec.configOverride.Effort != nil {
		requestedConfig.Effort = strings.TrimSpace(*spec.configOverride.Effort)
	}
	return requestedModel, requestedConfig
}

// newStartedSession 创建 session 对象并启动读循环。
// 新会话会先标记 thread ready，以允许第一条用户消息触发 Claude CLI 返回真实 session_id。
func (d *driver) newStartedSession(spec startSpec, started preparedStartSession) *session {
	initialThreadID := fallbackThreadID(spec.threadID)
	publicThreadID := shared.FirstNonEmpty(spec.publicThread, spec.agentID, initialThreadID)
	baseInstructions := promptBaseInstructions(spec.startAssembly.BaseInstructions, started.launchConfig.PromptSnapshot)
	s := &session{
		agentID:           strings.TrimSpace(spec.agentID),
		threadID:          initialThreadID,
		publicThreadID:    strings.TrimSpace(publicThreadID),
		sessionID:         initialThreadID,
		threadReady:       make(chan struct{}),
		transport:         started.transport,
		caps:              copyCapabilities(claudeCapabilities),
		history:           started.history,
		logger:            d.logger,
		eventDispatcher:   d.eventDispatcher,
		binaryPath:        d.binaryPath,
		cwd:               resolveAbsCWD(spec.cwd),
		launchCLI:         d.launchCLI,
		model:             started.requestedModel,
		transportModel:    started.launchModel,
		transportConfig:   started.launchConfig,
		transportManifest: spec.manifest,
		instructions:      strings.TrimSpace(baseInstructions),
		config:            started.launchConfig,
		rawConfig:         cloneConfigMap(spec.rawConfig),
		manifest:          spec.manifest,
		cleanup:           started.cleanup,
		pidRegistry:       d.pidRegistry,
		recovery:          d.recovery,
		tracer:            d.tracer,
		suppressedTurns:   map[string]struct{}{},
		imageTracker:      newImageHashTracker(),
		settleTransport:   defaultSettleInterruptedTransport,
	}
	s.applyConfiguredOverridesLocked(spec.configOverride, false)
	// Claude CLI v2.1+ 只有收到首条用户消息后才发送携带真实 session_id 的 system:init。
	// 因此新会话先标记 ready，允许 StartTurn 发送首条消息；真实 threadID 稍后仍由
	// system:init 异步回填。恢复会话已有持久化 UUID，重启路径也有独立的 resumeID 逻辑。
	s.markThreadReady()
	s.startReadLoop(started.transport)
	return s
}

// awaitStartedSession 等待 provider threadID 可用并登记 transport 进程。
// 若等待失败，会停止刚创建的 session 并清理可能残留的 provider thread 绑定。
func (d *driver) awaitStartedSession(ctx context.Context, s *session, tr *transport) error {
	if err := s.awaitResolvedThreadID(ctx); err != nil {
		shared.LogIgnoredError(d.logger, "stop failed on start error", s.stop(true))
		d.clearStaleProviderThreadID(s.agentID, "claudecli: clear stale binding failed")
		return err
	}
	if err := registerTransportPID(d.pidRegistry, tr, s.agentID); err != nil {
		shared.LogIgnoredError(d.logger, "stop failed on pid registry registration error", s.stop(true))
		d.clearStaleProviderThreadID(s.agentID, "claudecli: clear stale binding failed")
		return err
	}
	return nil
}

// clearStaleProviderThreadID 清除启动失败后可能遗留的 provider thread 绑定。
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

// dispatchStartEvents 发布 agent:launched 和 idle 状态事件，通知上层会话已进入可交互态。
func (d *driver) dispatchStartEvents(s *session, launchModel string) {
	resolvedThreadID := s.ThreadID()
	eventThreadID := s.EventThreadID()
	now := time.Now().Format(time.RFC3339Nano)
	s.dispatch(dto.RawProviderEvent{
		EventType: "agent:launched",
		Data: map[string]any{
			"agent_id":   s.agentID,
			"thread_id":  eventThreadID,
			"session_id": resolvedThreadID,
			"timestamp":  now,
			"cwd":        s.cwd,
			"model":      launchModel,
		},
	})
	s.dispatch(dto.RawProviderEvent{
		EventType: "agent:state_changed",
		Data: map[string]any{
			"agent_id":   s.agentID,
			"thread_id":  eventThreadID,
			"session_id": resolvedThreadID,
			"new_state":  "idle",
			"timestamp":  now,
		},
	})
}

// restartStatusDetails 将重启原因转换为 UI 可展示的状态细节。
func restartStatusDetails(reason string) string {
	switch strings.TrimSpace(reason) {
	case "settings_changed":
		return "正在应用新的 Claude 配置"
	case "transport_unavailable":
		return "正在重新连接 Claude CLI"
	default:
		return "正在重启 Claude CLI"
	}
}

// restartFailureStatus 将重启失败转换为 UI 状态三元组。
func restartFailureStatus(err error) (string, string, string) {
	if errors.Is(err, context.Canceled) {
		return "idle", "等待指示", ""
	}
	return "error", "Claude 重启失败", strings.TrimSpace(err.Error())
}

// statusPatchRawEventLocked 在 session 锁内构造重启状态 patch 事件。
func (s *session) statusPatchRawEventLocked(status, header, details string) dto.RawProviderEvent {
	base := s.rawBaseLocked()
	data := buildEventData(base, base.SessionID, time.Now().Format(time.RFC3339Nano), map[string]any{
		"status":         strings.TrimSpace(status),
		"status_header":  strings.TrimSpace(header),
		"status_details": strings.TrimSpace(details),
		"source":         "claude/restart",
		"partial":        true,
	})
	return dto.RawProviderEvent{EventType: "agent:status_patch", Data: data}
}

// dispatchRestartPatch 临时释放 session 锁以停止旧 watcher 并发布重启状态。
func (s *session) dispatchRestartPatch(prepared preparedSessionRestart) {
	s.mu.Unlock()
	if prepared.snapshot.watcher != nil {
		prepared.snapshot.watcher.stopAndWait()
	}
	s.dispatch(prepared.patch)
	s.mu.Lock()
}

// restoreRestartSnapshotLocked 在重启失败时回滚 transport、ready channel 和上下文窗口。
func (s *session) restoreRestartSnapshotLocked(snapshot restartSnapshot) {
	s.transport = snapshot.transport
	s.cleanup = snapshot.cleanup
	s.transportModel = snapshot.transportModel
	s.transportConfig = snapshot.transportConfig
	s.transportManifest = snapshot.transportManifest
	s.sessionContextWindow = snapshot.contextWindow
	if snapshot.readyClosed {
		s.resetThreadReadyLocked()
		s.markThreadReadyLocked()
		return
	}
	s.threadReady = snapshot.ready
}

// commitRestartSuccessLocked 在 session 锁内提交成功重启后的模型、配置和 manifest。
// 已应用的 pending override 会被清空，configDirty 只保留仍未应用的变更。
func (s *session) commitRestartSuccessLocked(next stagedSessionState) {
	s.model = next.model
	s.config = next.config
	s.manifest = next.manifest
	s.transportModel = next.displayModel
	s.transportConfig = next.config
	s.transportManifest = next.manifest
	if next.appliedPendingModel {
		s.overrideModel = next.appliedPendingModelText
		s.overrideModelSet = true
	}
	if next.appliedPendingEffort {
		s.overrideEffort = next.appliedPendingEffortText
		s.overrideEffortSet = true
	}
	if next.appliedPendingModel && s.pendingModel != nil && strings.TrimSpace(*s.pendingModel) == next.appliedPendingModelText {
		s.pendingModel = nil
	}
	if next.appliedPendingEffort && s.pendingEffort != nil && strings.TrimSpace(*s.pendingEffort) == next.appliedPendingEffortText {
		s.pendingEffort = nil
	}
	s.configDirty = s.pendingModel != nil || s.pendingEffort != nil
}

// restartResumeIDLocked 返回可用于 Claude resume 的真实 session UUID。
func (s *session) restartResumeIDLocked() string {
	resumeID := strings.TrimSpace(shared.FirstNonEmpty(s.sessionID, s.threadID))
	if !identifier.IsClaudeCLISessionUUID(resumeID) {
		return ""
	}
	return resumeID
}

// reportRuntime 向 runtime reporter 上报 Claude provider 已启动；当前 stdio 模式不暴露控制端口。
func (d *driver) reportRuntime(agentID string) {
	if d == nil || d.reporter == nil {
		return
	}
	agentID = strings.TrimSpace(agentID)
	if agentID == "" {
		return
	}
	ctx, cancel := platformconfig.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	// Claude CLI 当前只走 stdio transport；在 provider 提供稳定控制通道前，
	// runtime report 故意不填 control port。
	if err := d.reporter.ReportRuntime(ctx, contract.RuntimeReport{
		AgentID:  agentID,
		Provider: d.Name(),
	}); err != nil {
		d.logger.Warn("claudecli: report runtime failed", "agent_id", agentID, "error", err)
	}
}

var _ contract.Driver = (*driver)(nil)
