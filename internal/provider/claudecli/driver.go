package claudecli

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
	"github.com/anthropic-ai/super-agent-v3/internal/module/cliadapter"
	"github.com/anthropic-ai/super-agent-v3/internal/module/nativefilter"
	"github.com/anthropic-ai/super-agent-v3/internal/module/skilllibrary"
	platformconfig "github.com/anthropic-ai/super-agent-v3/internal/platform/config"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/pidregistry"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/shared"
	"github.com/anthropic-ai/super-agent-v3/internal/provider/manifestbuilder"
	providershared "github.com/anthropic-ai/super-agent-v3/internal/provider/shared"
	"github.com/anthropic-ai/super-agent-v3/internal/provider/unified"
	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
)

var claudeCapabilities = dto.CapabilitySet{
	dto.CapMessageSend:  true,
	dto.CapModelSwitch:  true,
	dto.CapTurnOverride: true,
}

type driver struct {
	logger           *slog.Logger
	binaryPath       string
	eventDispatcher  *unified.EventDispatcher
	reporter         contract.RuntimeReporter
	pidRegistry      *pidregistry.Registry
	proxyAddrFn      func() string
	skillCacheDir    string
	skillStore       *skilllibrary.Store
	nativeFilterPath string
}

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

type preparedStartSession struct {
	history        *historyBackend
	requestedModel string
	launchModel    string
	launchConfig   cliLaunchConfig
	transport      *transport
	cleanup        func()
}

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

type preparedSessionRestart struct {
	transport  *transport
	cleanup    func()
	patch      dto.RawProviderEvent
	waitCtx    context.Context
	generation uint64
	snapshot   restartSnapshot
}

func (d *driver) proxyHTTPAddr() string {
	if d == nil || d.proxyAddrFn == nil {
		return ""
	}
	return strings.TrimSpace(d.proxyAddrFn())
}

func newDriver(logger *slog.Logger, eventDispatcher *unified.EventDispatcher, reporter contract.RuntimeReporter, reg *pidregistry.Registry, proxyAddrFn func() string, skillCacheDir string, skillStore *skilllibrary.Store, nativeFilterPath string) contract.Driver {
	if logger == nil {
		logger = pkglogger.Get()
	}
	if proxyAddrFn == nil {
		proxyAddrFn = func() string { return "" }
	}
	return &driver{
		logger:           logger,
		binaryPath:       resolveBinaryPath(),
		eventDispatcher:  eventDispatcher,
		reporter:         reporter,
		pidRegistry:      reg,
		proxyAddrFn:      proxyAddrFn,
		skillCacheDir:    skillCacheDir,
		skillStore:       skillStore,
		nativeFilterPath: nativeFilterPath,
	}
}

func (d *driver) Name() string { return "claude" }

func (d *driver) StartSession(ctx context.Context, req dto.StartSessionRequest) (contract.Session, error) {
	launchConfig := configFromMap(req.Config)
	manifest := manifestbuilder.BuildManifest(dto.ManifestContext{
		AgentID:       strings.TrimSpace(req.AgentID),
		ThreadID:      strings.TrimSpace(req.AgentID),
		CWD:           strings.TrimSpace(req.CWD),
		ThreadCaps:    copyCapabilities(claudeCapabilities),
		BinaryDir:     providershared.ResolveBinaryDir(req.CWD, req.Config),
		Env:           providershared.StringMap(req.Config["env"]),
		AutoApprove:   providershared.ConfigStringSlice(req.Config, "auto_approve", "autoApprove"),
		ProxyHTTPAddr: d.proxyHTTPAddr(),
	})
	return d.start(ctx, startSpec{
		agentID:       req.AgentID,
		cwd:           req.CWD,
		model:         req.Model,
		startAssembly: resolveStartAssembly(req, launchConfig, d.Name()),
		manifest:      manifest,
		config:        launchConfig,
		rawConfig:     cloneConfigMap(req.Config),
		publicThread:  req.AgentID,
		historyDir:    providershared.ConfigString(req.Config, "history_dir", "claude_home"),
	})
}

func (d *driver) ResumeSession(ctx context.Context, req dto.ResumeSessionRequest) (contract.Session, error) {
	snapshot := req.PromptSnapshot
	manifest := manifestbuilder.BuildManifest(dto.ManifestContext{
		AgentID:       strings.TrimSpace(req.AgentID),
		ThreadID:      strings.TrimSpace(req.ThreadID),
		CWD:           strings.TrimSpace(req.CWD),
		ThreadCaps:    copyCapabilities(claudeCapabilities),
		BinaryDir:     providershared.ResolveBinaryDir(req.CWD, nil),
		ProxyHTTPAddr: d.proxyHTTPAddr(),
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
		manifest: manifest,
		config: cliLaunchConfig{
			Effort:         strings.TrimSpace(req.Effort),
			PromptSnapshot: snapshot,
		},
		configOverride: req.ConfigOverride,
	})
}

func (d *driver) start(ctx context.Context, spec startSpec) (contract.Session, error) {
	if err := shared.CheckCtx(ctx); err != nil {
		return nil, err
	}
	started, err := d.prepareSessionStart(spec)
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

func (d *driver) prepareSessionStart(spec startSpec) (preparedStartSession, error) {
	history := &historyBackend{sessionDir: spec.historyDir}
	requestedModel, requestedConfig := resolveRequestedStartConfig(spec)
	requestedConfig.DeveloperInstructions = promptDeveloperInstructions(cliLaunchConfig{
		DeveloperInstructions: requestedConfig.DeveloperInstructions,
		PromptSnapshot:        spec.startAssembly.Snapshot,
	})
	requestedConfig.PromptSnapshot = spec.startAssembly.Snapshot
	launchModel := claudeLaunchDisplayModel(requestedModel, history)
	launchConfig := canonicalizeClaudeLaunchConfig(launchModel, requestedConfig)
	// Before launchCLI, mount the shared skill cache into the workspace so
	// Claude CLI's native discovery picks up our skills.
	if d.skillCacheDir != "" && spec.cwd != "" {
		if err := cliadapter.SetupWorkspaceSkills(spec.cwd, d.skillCacheDir); err != nil {
			// fail-open: log and continue. Skill discovery failure should not
			// block the user's main session.
			if d.logger != nil {
				d.logger.Warn("workspace skill symlink setup failed",
					"cwd", spec.cwd, "cache", d.skillCacheDir, "err", err)
			}
		}
	}
	// spec §8 native-filter settings: aggregate active skills + base config
	// into <workspace>/.claude/settings.local.json. fail-open——任何一步失败都
	// 不阻塞 session 启动；P5b 阶段文件能否真被 Claude CLI 拾取仍待 spec §8.3
	// 实测验证。
	if spec.cwd != "" {
		d.applyNativeFilter(spec.cwd)
	}
	tr, cleanup, err := launchCLI(
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

func resolveRequestedStartConfig(spec startSpec) (string, cliLaunchConfig) {
	requestedModel := strings.TrimSpace(spec.model)
	if requestedModel == "" && spec.configOverride.Model != nil {
		requestedModel = strings.TrimSpace(*spec.configOverride.Model)
	}
	requestedConfig := spec.config
	if strings.TrimSpace(requestedConfig.Effort) == "" && spec.configOverride.Effort != nil {
		requestedConfig.Effort = strings.TrimSpace(*spec.configOverride.Effort)
	}
	return requestedModel, requestedConfig
}

func (d *driver) newStartedSession(spec startSpec, started preparedStartSession) *session {
	initialThreadID := fallbackThreadID(spec.agentID, spec.threadID)
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
		suppressedTurns:   map[string]struct{}{},
	}
	s.applyConfiguredOverridesLocked(spec.configOverride, false)
	if shouldMarkThreadReady(spec.threadID, publicThreadID) {
		s.markThreadReady()
	}
	s.startReadLoop(started.transport)
	return s
}

func (d *driver) awaitStartedSession(ctx context.Context, s *session, tr *transport) error {
	if err := s.awaitResolvedThreadID(ctx); err != nil {
		shared.LogIgnoredError(d.logger, "stop failed on start error", s.stop(true))
		return err
	}
	registerTransportPID(d.pidRegistry, tr, s.agentID)
	return nil
}

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

func restartFailureStatus(err error) (string, string, string) {
	if errors.Is(err, context.Canceled) {
		return "idle", "等待指示", ""
	}
	return "error", "Claude 重启失败", strings.TrimSpace(err.Error())
}

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

func (s *session) dispatchRestartPatch(prepared preparedSessionRestart) {
	s.mu.Unlock()
	if prepared.snapshot.watcher != nil {
		prepared.snapshot.watcher.stopAndWait()
	}
	s.dispatch(prepared.patch)
	s.mu.Lock()
}

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

func (s *session) restartResumeIDLocked() string {
	resumeID := strings.TrimSpace(shared.FirstNonEmpty(s.sessionID, s.threadID))
	if requiresResolvedThreadID(resumeID) {
		return ""
	}
	return resumeID
}

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

	// NOTE: Claude CLI is stdio-backed today, so runtime reports intentionally
	// omit a control port until the provider exposes a stable side channel.
	if err := d.reporter.ReportRuntime(ctx, contract.RuntimeReport{
		AgentID:  agentID,
		Provider: d.Name(),
	}); err != nil {
		d.logger.Warn("claudecli: report runtime failed", "agent_id", agentID, "error", err)
	}
}

var _ contract.Driver = (*driver)(nil)

// applyNativeFilter 把 active skills + base config 聚合写到
// <workspace>/.claude/settings.local.json（spec §8）。
//
// 设计：fail-open。任何一步失败仅 warn 日志，不返回错误、不阻塞 session 启动。
// 理由：spec §8.3 实测虽证实 deny 路径生效（appendix），但 settings 写入仍是
// best-effort 加固层；fail-closed 反而会因 base config 读取失败 / store 列出
// 失败而拒绝整个 session 启动，得不偿失。
//
// **Scope limitation（已知）**：当前只扫 user-level skilllibrary
// （`~/.multi-agent/skills-library/`），不扫项目级 `<cwd>/.agent/skills`。
// 由 spec §8.3 appendix "Known scope limitation" 明确：项目 skill 当前不在
// native filter 收紧范围内，因为团队规范不要求项目 skill 声明 allowed_tools /
// replaces_native；如未来需要把项目 skill 纳入 P5b 强制限制，参考 P5e 决策
// 三问（Q1/Q2/Q3）重启评估。
//
// skillStore 为 nil（fx optional 注入未提供）时整段 no-op，便于测试 fixture。
func (d *driver) applyNativeFilter(workspace string) {
	if d.skillStore == nil {
		return
	}
	entries, err := d.skillStore.List()
	if err != nil {
		if d.logger != nil {
			d.logger.Warn("native filter: skill list failed", "err", err)
		}
		return
	}
	summaries := make([]nativefilter.SkillSummary, 0, len(entries))
	for _, e := range entries {
		if e.Meta == nil {
			continue
		}
		summaries = append(summaries, nativefilter.SkillSummary{
			Name:           e.Meta.Name,
			Disabled:       e.Meta.Disabled,
			AllowedTools:   e.Meta.AllowedTools,
			ReplacesNative: e.Meta.ReplacesNative,
		})
	}
	base, err := nativefilter.LoadBaseConfig(d.nativeFilterPath)
	if err != nil {
		// 读 base config 失败时仍以空 base 继续 —— skill-side 决议（包含
		// AllowedTools / ReplacesNative）仍然有价值，没必要因为基线文件坏掉
		// 就完全放弃过滤。
		if d.logger != nil {
			d.logger.Warn("native filter: base config load failed",
				"path", d.nativeFilterPath, "err", err)
		}
	}
	settings := nativefilter.AggregateClaude(base.Claude, summaries)
	if err := cliadapter.WriteClaudeSettingsLocal(workspace, settings); err != nil {
		if d.logger != nil {
			d.logger.Warn("native filter: settings write failed",
				"workspace", workspace, "err", err)
		}
	}
}

// DefaultNativeFilterPath 返回 spec §8.1 约定的基线配置路径
// `~/.multi-agent/native-cli-filter.json`。
// HOME 拿不到时返回 ""，调用方用空路径触发 LoadBaseConfig 的 fail-open 分支。
func DefaultNativeFilterPath() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ""
	}
	return filepath.Join(home, ".multi-agent", "native-cli-filter.json")
}
