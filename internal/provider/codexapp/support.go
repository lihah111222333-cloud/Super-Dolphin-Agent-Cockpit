package codexapp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"strings"
	"time"

	contract "github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
	dto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/provider"
	platformconfig "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/config"
	platformshared "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/shared"
	codexprotocol "github.com/lihah111222333-cloud/super-dolphin-agent/internal/provider/codexapp/protocol"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/provider/codexapp/supportutil"
	providershared "github.com/lihah111222333-cloud/super-dolphin-agent/internal/provider/shared"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/util"
	pkglogger "github.com/lihah111222333-cloud/super-dolphin-agent/pkg/logger"
)

func withTimeout(ctx context.Context, d time.Duration) (context.Context, context.CancelFunc) {
	return platformconfig.WithTimeout(ctx, d)
}

func (s *PeerSupervisor) waitForPeerAfterCancel(name string, h peerHandle, waitCh <-chan error) {
	timeout := 5 * (s.stopGrace + 2*s.killGrace)
	select {
	case <-waitCh:
	case <-time.After(timeout):
		s.logger.Warn("peer_supervisor: peer wait did not return after ctx cancel", "peer", name, "pid", h.PID(), "timeout", timeout)
	}
}

func mustJSON(v any) json.RawMessage {
	raw, err := json.Marshal(v)
	if err != nil {
		slog.Error("codexapp: mustJSON marshal failed", "error", err)
		return json.RawMessage("null")
	}
	return raw
}

func initializeParams() map[string]any {
	return map[string]any{"clientInfo": map[string]any{"name": "super-agent-v3", "version": "1.0"}, "capabilities": map[string]any{"experimentalApi": true}}
}

func normalizeCodexAppEffort(effort string) string {
	effort = strings.TrimSpace(effort)
	if strings.EqualFold(effort, "minimal") {
		return "low"
	}
	return effort
}

func newTurnHandle(localID, providerID string) *turnHandle {
	return &turnHandle{localID: strings.TrimSpace(localID), providerID: strings.TrimSpace(providerID), done: make(chan struct{})}
}

// LocalID 返回宿主侧用于追踪本轮 turn 的本地 ID。
func (h *turnHandle) LocalID() string { return h.localID }

// Done 在 turn 完成或失败时关闭，调用方用它等待结果落定。
func (h *turnHandle) Done() <-chan struct{} { return h.done }

// ProviderID 返回 Codex app-server 分配的 turn ID，读取时保持锁保护。
func (h *turnHandle) ProviderID() string {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.providerID
}

// Err 返回 turn 结束时记录的错误，必须和完成信号使用同一把锁读取。
func (h *turnHandle) Err() error {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.err
}

func (h *turnHandle) setProviderID(providerID string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.providerID = strings.TrimSpace(providerID)
}

func (h *turnHandle) complete(err error) {
	h.once.Do(func() {
		h.mu.Lock()
		h.err = err
		h.mu.Unlock()
		close(h.done)
	})
}

func cloneCaps(src dto.CapabilitySet) dto.CapabilitySet {
	out := make(dto.CapabilitySet, len(src))
	maps.Copy(out, src)
	return out
}

// ThreadID 返回当前 session 绑定的 Codex provider thread ID。
func (s *session) ThreadID() string {
	if s == nil {
		return ""
	}
	threadID, _ := s.threadID.Load().(string)
	return strings.TrimSpace(threadID)
}

// RolloutPath 根据当前 thread ID 找到 Codex 本地 rollout 文件路径。
func (s *session) RolloutPath() string {
	tid := s.ThreadID()
	if tid == "" {
		return ""
	}
	// codexHome 来自会话 runtimeConfig，用于把 rollout 查找限制在当前 provider home。
	// 为空时沿用默认 Codex home，兼容单 provider 的历史本地文件布局。
	path, err := findRolloutPath(tid, s.runtimeConfigString("codexHome"))
	if err != nil {
		return ""
	}
	return path
}

func (s *session) setThreadID(threadID string) {
	if s == nil {
		return
	}
	s.threadID.Store(strings.TrimSpace(threadID))
}

func (s *session) configureThread(ctx context.Context, patch dto.ThreadConfigPatch) error {
	threadID, err := requireThreadID(s)
	if err != nil {
		return err
	}
	if err := s.applyConfigSet(ctx, threadID, patch); err != nil {
		return err
	}
	if err := s.applyConfigSlashCommands(ctx, threadID, patch); err != nil {
		return err
	}
	s.updateRuntimeConfigFromPatch(patch)
	return nil
}

func (s *session) applyConfigSet(_ context.Context, _ string, patch dto.ThreadConfigPatch) error {
	// Codex app-server 没有 thread/config/set RPC，model/effort 先保存在会话本地。
	// 下一次 turn/start 通过 turnStartParams.Model/Effort 传给 provider。
	if patch.Model != nil {
		s.setRuntimeConfigValue("model", strings.TrimSpace(*patch.Model))
	}
	if patch.Effort != nil {
		s.setRuntimeConfigValue("effort", strings.TrimSpace(*patch.Effort))
	}
	return nil
}

func (s *session) applyConfigSlashCommands(ctx context.Context, threadID string, patch dto.ThreadConfigPatch) error {
	if err := s.applySlashConfig(ctx, threadID, "thread/personality/set", "personality", patch.Personality); err != nil {
		return err
	}
	return s.applySlashConfig(ctx, threadID, "thread/approvals/set", "policy", patch.Approvals)
}

func (s *session) updateRuntimeConfigFromPatch(patch dto.ThreadConfigPatch) {
	if patch.Approvals != nil {
		approval := strings.TrimSpace(*patch.Approvals)
		s.setApprovalPolicy(approval)
		s.setRuntimeConfigValue("approvalPolicy", approval)
		s.setRuntimeConfigValue("approval_policy", approval)
		s.setRuntimeConfigValue("approvals", approval)
	}
	if patch.Personality != nil {
		s.setRuntimeConfigValue("personality", strings.TrimSpace(*patch.Personality))
	}
}

func (s *session) applySlashConfig(ctx context.Context, threadID, method, key string, value *string) error {
	if value == nil {
		return nil
	}
	arg := strings.TrimSpace(*value)
	if arg == "" {
		return nil
	}
	_, err := callWithTimeout(ctx, callTargetFunc(s.callTransport), 10*time.Second, method, map[string]any{"threadId": threadID, key: arg, "args": arg})
	return err
}

func (s *session) runtimeConfigString(key string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.runtimeConfig == nil {
		return ""
	}
	v, _ := s.runtimeConfig[key].(string)
	return supportutil.SanitizeConfigStringArtifact(v)
}

// runtimeConfigJSON 把 runtimeConfig 中的 JSON 风格值序列化成 RawMessage。
// 权限类配置不能静默丢弃；如果值无法 JSON 编码，调用方会收到错误并阻断 turn/start。
func (s *session) runtimeConfigJSON(key string) (json.RawMessage, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.runtimeConfig == nil {
		return nil, nil
	}
	value, ok := s.runtimeConfig[key]
	if !ok || value == nil {
		return nil, nil
	}
	switch typed := value.(type) {
	case json.RawMessage:
		if len(typed) == 0 {
			return nil, nil
		}
		return append(json.RawMessage(nil), typed...), nil
	case []byte:
		if len(typed) == 0 {
			return nil, nil
		}
		return append(json.RawMessage(nil), typed...), nil
	default:
		raw, err := json.Marshal(value)
		if err != nil {
			return nil, fmt.Errorf("codexapp: runtime config %s must be JSON serializable: %w", key, err)
		}
		return raw, nil
	}
}

func (s *session) runtimeConfigStringSlice(keys ...string) []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.runtimeConfig == nil {
		return nil
	}
	return providershared.ConfigStringSlice(s.runtimeConfig, keys...)
}

func (s *session) ensureRuntimeCodexHomeFromInitialize(reason string) {
	if s == nil || s.transport == nil {
		return
	}
	if existing := s.runtimeConfigString("codexHome"); existing != "" {
		return
	}
	home := strings.TrimSpace(s.transport.InitializeCodexHome())
	if home == "" {
		pkglogger.Warn("codexapp: runtime codexHome injection skipped",
			"agent_id", s.agentID,
			"reason", "initialize_missing_codex_home",
			"stage", reason)
		return
	}
	s.setRuntimeConfigValue("codexHome", home)
	fields := []any{
		"agent_id", s.agentID,
		"stage", reason,
	}
	fields = append(fields, platformshared.SafePathLogFields("codex_home", home)...)
	pkglogger.Warn("codexapp: runtime codexHome injected from initialize", fields...)
}

func (s *session) setRuntimeConfigValue(key string, value any) {
	if strings.TrimSpace(key) == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.runtimeConfig == nil {
		s.runtimeConfig = map[string]any{}
	}
	s.runtimeConfig[key] = value
}

// resolveSupportedCodexModel 查询 app-server 支持的模型，并在需要时选一个可用默认值。
type codexModelResolution struct {
	model    string
	replaced bool
}

type codexModelResolver struct {
	target callTarget
}

func newCodexModelResolver(target callTarget) codexModelResolver {
	return codexModelResolver{target: target}
}

// Resolve 通过 model/list 把默认模型解析成当前账号真实可用的模型。
// 这是 thread/start 与 turn/start 的共同入口；列表不可用必须 fail-fast。
func (r codexModelResolver) Resolve(ctx context.Context, requested string) (codexModelResolution, error) {
	requested = strings.TrimSpace(requested)
	models, err := r.listModels(ctx, requested)
	if err != nil {
		return codexModelResolution{}, err
	}
	return selectCodexModel(requested, models)
}

// listModels 调用 provider 的 model/list，并把空列表归类为必需模型解析失败。
func (r codexModelResolver) listModels(ctx context.Context, requested string) ([]string, error) {
	if r.target == nil {
		return nil, supportutil.NewModelResolutionRequiredError(requested, errors.New("model/list transport is not configured"))
	}
	raw, err := callWithTimeout(ctx, r.target, 10*time.Second, "model/list", map[string]any{})
	if err != nil {
		return nil, supportutil.NewModelResolutionRequiredError(requested, err)
	}
	models, err := supportutil.DecodeAllowedModels(raw)
	if err != nil {
		return nil, supportutil.NewModelResolutionRequiredError(requested, err)
	}
	return models, nil
}

// selectCodexModel 从已验证的 model/list 结果中选出要发送给 app-server 的模型。
func selectCodexModel(requested string, models []string) (codexModelResolution, error) {
	preferred := supportutil.PreferredCodexModel(models)
	if requested == "" {
		if preferred == "" {
			return codexModelResolution{}, supportutil.NewModelResolutionRequiredError(requested, nil)
		}
		return codexModelResolution{model: preferred, replaced: true}, nil
	}
	if requestedCodexModelAllowed(requested, models) {
		return codexModelResolution{model: requested}, nil
	}
	if preferred != "" {
		return codexModelResolution{model: preferred, replaced: !strings.EqualFold(preferred, requested)}, nil
	}
	if supportutil.CodexModelListContains(models, requested) {
		return codexModelResolution{model: requested}, nil
	}
	return codexModelResolution{model: requested}, nil
}

// requestedCodexModelAllowed 保留显式可用模型；泛化 GPT 默认值则交给 preferred 选择。
func requestedCodexModelAllowed(requested string, models []string) bool {
	if supportutil.CodexModelIsCodexFamily(requested) {
		return supportutil.CodexModelListContains(models, requested)
	}
	return !supportutil.CodexModelIsGenericGPT(requested) && supportutil.CodexModelListContains(models, requested)
}

// resolveSupportedCodexModel 保留旧调用名，但内部统一走 typed resolver。
func resolveSupportedCodexModel(ctx context.Context, t callTarget, requested string) (codexModelResolution, error) {
	return newCodexModelResolver(t).Resolve(ctx, requested)
}

// buildThreadStartParams 汇总 thread/start 参数，并保留模型来源供后续解析区分默认值与显式指定。
func (d *driver) buildThreadStartParams(req dto.StartSessionRequest) (threadStartParams, error) {
	baseInstructions, developerInstructions, err := d.startAssemblyInstructions(req)
	if err != nil {
		return threadStartParams{}, err
	}
	model := strings.TrimSpace(req.Model)
	if model == "" {
		model = supportutil.ConfigString(req.Config, "model")
	}
	params := threadStartParams{
		Cwd:                   strings.TrimSpace(req.CWD),
		Model:                 model,
		ModelProvider:         supportutil.FirstConfigString(req.Config, contract.CodexModelProviderKey, "modelProvider", "model_provider"),
		BaseInstructions:      baseInstructions,
		DeveloperInstructions: developerInstructions,
		ApprovalPolicy:        supportutil.ResolveApprovalPolicy(req.Config),
		Personality:           supportutil.ConfigString(req.Config, "personality"),
		Summary:               supportutil.ConfigString(req.Config, "summary"),
		Effort:                normalizeCodexAppEffort(supportutil.ConfigString(req.Config, "effort")),
		Sandbox:               codexSandboxWireJSON(supportutil.ConfigJSON(req.Config, "sandbox")),
		SandboxPolicy:         codexSandboxPolicyWireJSON(supportutil.ConfigJSON(req.Config, "sandbox")),
		MCPConfig:             supportutil.ConfigJSON(req.Config, "mcpConfig"),
	}
	policy, err := codexNativeToolPolicyFromConfig(req.Config)
	if err != nil {
		return threadStartParams{}, err
	}
	policy.ApplyThreadStartParams(&params)
	return params, nil
}

func (d *driver) startDynamicSession(ctx context.Context, s *session, req dto.StartSessionRequest) (contract.Session, error) {
	tools, err := d.prepareStartDynamicTools(ctx, s, req)
	if err != nil {
		cleanupFailedSession(s, "force stop failed on dynamic tools preparation error")
		return nil, err
	}
	result, err := d.startRemoteThreadWithDynamicTools(ctx, s.transport, req, tools)
	if err != nil {
		pkglogger.Warn("codexapp: startDynamicSession failed before cleanup", "agent_id", strings.TrimSpace(req.AgentID), "error", err)
		cleanupFailedSession(s, "force stop failed on start error")
		return nil, err
	}
	if err := d.bindStartedToolSurface(s, req, result.threadID); err != nil {
		cleanupFailedSession(s, "force stop failed on start tool surface bind error")
		return nil, err
	}
	started, err := d.finishStartedSession(s, req, result)
	if err != nil {
		cleanupFailedSession(s, "force stop failed on start runtime report error")
		return nil, err
	}
	return started, nil
}

// prepareStartDynamicTools 为 thread/start 准备需要交给 Codex 的动态工具声明。
func (d *driver) prepareStartDynamicTools(ctx context.Context, s *session, req dto.StartSessionRequest) ([]codexprotocol.DynamicToolSchema, error) {
	if !contract.ToolSurfaceModeUsesDynamicTools(req.ToolSurfaceMode) {
		return nil, nil
	}
	if d == nil {
		return nil, errors.New("codexapp: dynamic tools provider is not configured")
	}
	if d.prepareTools != nil {
		scope, err := d.codexToolSurfaceScope(req.AgentID, "", "", req.CWD, req.Config)
		if err != nil {
			return nil, err
		}
		scope.SurfaceID = s.ensureToolSurfaceID()
		return d.prepareTools(ctx, scope)
	}
	if d.listTools == nil {
		return nil, errors.New("codexapp: dynamic tools provider is not configured")
	}
	tools, err := d.listTools(ctx)
	if err != nil {
		return nil, fmt.Errorf("dynamic tools list: %w", err)
	}
	return tools, nil
}

// bindStartedToolSurface 把已启动的 provider thread 绑定到本地工具面。
func (d *driver) bindStartedToolSurface(s *session, req dto.StartSessionRequest, providerThreadID string) error {
	if d == nil || d.prepareTools == nil {
		return nil
	}
	if !contract.ToolSurfaceModeUsesDynamicTools(req.ToolSurfaceMode) {
		return nil
	}
	if d.bindTools == nil {
		return errors.New("codexapp: dynamic tools binder is not configured")
	}
	if strings.TrimSpace(providerThreadID) == "" {
		return errors.New("codexapp: provider thread id is required for tool surface bind")
	}
	scope, err := d.codexToolSurfaceScope(req.AgentID, "", providerThreadID, req.CWD, req.Config)
	if err != nil {
		return err
	}
	scope.SurfaceID = s.currentToolSurfaceID()
	if err = d.bindTools(scope); err != nil {
		return fmt.Errorf("dynamic tools start surface bind: %w", err)
	}
	return nil
}

func (d *driver) rebuildResumeToolSurface(ctx context.Context, s *session, req dto.ResumeSessionRequest, providerThreadID string) error {
	if d == nil || d.prepareTools == nil {
		return nil
	}
	scope, err := d.codexToolSurfaceScope(req.AgentID, req.ThreadID, providerThreadID, req.CWD, req.Config)
	if err != nil {
		return err
	}
	scope.SurfaceID = s.ensureToolSurfaceID()
	if _, err = d.prepareTools(ctx, scope); err != nil {
		return fmt.Errorf("dynamic tools resume surface: %w", err)
	}
	return nil
}

// codexToolSurfaceScope 组装 Codex 动态工具面所需的可信上下文，并把 mcpConfig 转成可拉取 tools 的 manifest。
func (d *driver) codexToolSurfaceScope(agentID, localThreadID, providerThreadID, cwd string, cfg map[string]any) (contract.CodexToolSurfaceScope, error) {
	cwd = strings.TrimSpace(cwd)
	workspaceRoots := trustedWorkspaceRoots(cwd, providershared.ConfigStringSlice(cfg, contract.RuntimeConfigAdditionalWorkingDirectories.Keys()...))
	additionalRoots := workspaceRoots[min(len(workspaceRoots), 1):]
	extraBinaries, err := providershared.ConfigMCPBinaries(cfg, "mcpConfig", "mcp_config")
	if err != nil {
		return contract.CodexToolSurfaceScope{}, fmt.Errorf("codexapp: dynamic tools mcpConfig: %w", err)
	}
	disabledTools, err := codexDisabledToolsFromConfig(cfg)
	if err != nil {
		return contract.CodexToolSurfaceScope{}, fmt.Errorf("codexapp: %w", err)
	}
	return contract.CodexToolSurfaceScope{
		AgentID:          strings.TrimSpace(agentID),
		UIThreadID:       strings.TrimSpace(localThreadID),
		LocalThreadID:    strings.TrimSpace(localThreadID),
		ProviderThreadID: strings.TrimSpace(providerThreadID),
		CWD:              cwd,
		WorkspaceRoots:   append([]string(nil), workspaceRoots...),
		DisabledTools:    disabledTools,
		Manifest: contract.BuildManifest(dto.ManifestContext{
			AgentID:                      strings.TrimSpace(agentID),
			ThreadID:                     strings.TrimSpace(util.FirstNonEmpty(providerThreadID, localThreadID, agentID)),
			CWD:                          cwd,
			AdditionalWorkingDirectories: additionalRoots,
			ThreadCaps:                   cloneCaps(codexCapabilities),
			BinaryDir:                    providershared.ResolveBinaryDir(cwd, cfg),
			Env:                          providershared.StringMap(cfg[contract.RuntimeConfigEnv.Canonical]),
			AutoApprove:                  providershared.ConfigStringSlice(cfg, contract.RuntimeConfigAutoApprove.Keys()...),
			ExtraBinaries:                extraBinaries,
			TransportMode:                dto.ManifestTransportStdioOnly,
		}),
	}, nil
}

// codexDisabledToolsFromConfig 严格读取 Codex session config 中的禁用工具列表。
// 安全相关列表只接受字符串、csv 字符串或字符串数组，类型错误会阻断启动。
func codexDisabledToolsFromConfig(cfg map[string]any) ([]string, error) {
	for _, key := range []string{"disallowed_tools", "disallowedTools"} {
		raw, ok := cfg[key]
		if !ok {
			continue
		}
		return normalizeCodexDisabledToolsConfig(key, raw)
	}
	return nil, nil
}

// normalizeCodexDisabledToolsConfig 校验并规范化 Codex 禁用工具配置。
// 这里不能吞掉对象或混合数组，否则 read-only 子 agent 会误获得写工具。
func normalizeCodexDisabledToolsConfig(key string, raw any) ([]string, error) {
	switch typed := raw.(type) {
	case string:
		return providershared.SplitConfigStringSlice(typed), nil
	case []string:
		return providershared.TrimStrings(typed), nil
	case []any:
		for i, value := range typed {
			if _, ok := value.(string); !ok {
				return nil, fmt.Errorf("%s[%d] must be string", key, i)
			}
		}
		return providershared.TrimConfigStringValues(typed), nil
	default:
		return nil, fmt.Errorf("%s must be string or string array", key)
	}
}

func (d *driver) finishStartedSession(s *session, req dto.StartSessionRequest, result startResult) (contract.Session, error) {
	s.setThreadID(result.threadID)
	if result.model != "" {
		s.setRuntimeConfigValue("model", result.model)
	}
	if cwd := strings.TrimSpace(req.CWD); cwd != "" {
		s.setRuntimeConfigValue("cwd", cwd)
	}
	if port := parsePortFromURL(s.transport.serverURL); port > 0 {
		s.setRuntimeConfigValue("port", port)
	}
	if err := d.reportRuntime(s.agentID, s.transport.serverURL); err != nil {
		return nil, err
	}
	return s, nil
}

func primeResumeToolScope(s *session, req dto.ResumeSessionRequest) {
	if resumeID := strings.TrimSpace(req.ProviderThreadID); resumeID != "" {
		s.setThreadID(resumeID)
	}
	if cwd := strings.TrimSpace(req.CWD); cwd != "" {
		s.setRuntimeConfigValue("cwd", cwd)
	}
}

// finishOrCleanupResumedSession 统一 resume 收尾失败的 session 清理，避免主流程堆高复杂度。
func (d *driver) finishOrCleanupResumedSession(ctx context.Context, s *session, req dto.ResumeSessionRequest, threadID string) (contract.Session, error) {
	resumed, err := d.finishResumedSession(ctx, s, req, threadID)
	if err != nil {
		cleanupFailedSession(s, "force stop failed on resume finalization error")
		return nil, err
	}
	return resumed, nil
}

// finishResumedSession 恢复 resume 后本地 session 需要继续使用的运行配置。
func (d *driver) finishResumedSession(ctx context.Context, s *session, req dto.ResumeSessionRequest, threadID string) (contract.Session, error) {
	s.setThreadID(threadID)
	s.ensureRuntimeCodexHomeFromInitialize("resume")
	if cwd := strings.TrimSpace(req.CWD); cwd != "" {
		s.setRuntimeConfigValue("cwd", cwd)
	}
	if m := strings.TrimSpace(req.Model); m != "" {
		s.setRuntimeConfigValue("model", m)
	}
	baseInstructions, developerInstructions := promptSnapshotInstructions(req.PromptSnapshot)
	if baseInstructions != "" {
		s.setRuntimeConfigValue("baseInstructions", baseInstructions)
	}
	if developerInstructions != "" {
		s.setRuntimeConfigValue("developerInstructions", developerInstructions)
	}
	if len(req.CodexDisabledNativeTools) > 0 {
		s.setRuntimeConfigValue("codexDisabledNativeTools", append([]string(nil), req.CodexDisabledNativeTools...))
	}
	s.approvalPolicyVerified.Store(false)
	if err := d.restoreApprovalPolicy(ctx, s, threadID); err != nil {
		return nil, err
	}
	if err := applyResumeNativeToolRuntimePolicy(s, req.CodexDisabledNativeTools); err != nil {
		return nil, err
	}
	if err := d.reportRuntime(s.agentID, s.transport.serverURL); err != nil {
		return nil, err
	}
	return s, nil
}

func (d *driver) startRemoteThreadWithDynamicTools(ctx context.Context, t *transport, req dto.StartSessionRequest, tools []codexprotocol.DynamicToolSchema) (startResult, error) {
	params, err := d.buildThreadStartParams(req)
	if err != nil {
		return startResult{}, err
	}
	params.DynamicTools = tools
	// dynamicTools schema 已由 codex app-server 暴露给模型，developerInstructions 不再重复塞工具名。
	// 这避免把完整工具目录再次写入上下文，保留预算给真实任务内容。
	return startRemoteThreadWithParams(ctx, t, req, params)
}

// startRemoteThreadWithParams 发送 thread/start，并在发送前补齐模型选择和诊断日志。
func startRemoteThreadWithParams(ctx context.Context, t *transport, req dto.StartSessionRequest, params threadStartParams) (startResult, error) {
	configKeys := supportutil.SortedConfigKeys(req.Config)
	if supportutil.CodexModelNeedsListResolutionForSource(params.Model, threadStartModelResolutionSource(req)) {
		requestedModel := strings.TrimSpace(params.Model)
		resolution, err := resolveSupportedCodexModel(ctx, t, requestedModel)
		if err != nil {
			return startResult{}, err
		}
		if resolution.model != "" && resolution.replaced {
			params.Model = resolution.model
			pkglogger.Info("codexapp: thread/start selected supported model from model/list",
				"agent_id", strings.TrimSpace(req.AgentID),
				"requested_model", requestedModel,
				"model", resolution.model,
			)
		}
	}
	if strings.TrimSpace(params.Model) == "" || strings.TrimSpace(params.Effort) == "" {
		pkglogger.Warn("codexapp: thread/start config trace",
			"agent_id", strings.TrimSpace(req.AgentID),
			"req_model", strings.TrimSpace(req.Model),
			"config_model", supportutil.ConfigString(req.Config, "model"),
			"params_model", params.Model,
			"config_effort", supportutil.ConfigString(req.Config, "effort"),
			"params_effort", params.Effort,
			"config_keys", configKeys,
		)
	}
	if strings.TrimSpace(params.Effort) == "" {
		fields := []any{
			"agent_id", strings.TrimSpace(req.AgentID),
			"model", params.Model,
			"approval_policy", params.ApprovalPolicy,
			"config_keys", configKeys,
			"expected_config_key", "effort",
		}
		fields = append(fields, platformshared.SafePathLogFields("cwd", params.Cwd)...)
		pkglogger.Warn("codexapp: thread/start effort missing", fields...)
	}
	requestFields := []any{
		"agent_id", strings.TrimSpace(req.AgentID),
		"model", params.Model,
		"effort", params.Effort,
		"approval_policy", params.ApprovalPolicy,
		"config_keys", configKeys,
		"has_env", hasAnyConfigKey(req.Config, "env"),
		"has_mcp", hasAnyConfigKey(req.Config, "mcp", "mcpConfig", "mcp_config", "mcpServers", "mcp_servers"),
		"has_hooks", hasAnyConfigKey(req.Config, "hooks", "hookConfig", "hook_config"),
	}
	requestFields = append(requestFields, platformshared.SafePathLogFields("cwd", params.Cwd)...)
	pkglogger.Info("codexapp: thread/start request", requestFields...)
	logThreadStartIdentityTrace("codexapp: thread/start identity trace", t.serverURL, req, params, nil)
	if len(params.DynamicTools) > 0 {
		firstTool, _ := json.Marshal(params.DynamicTools[0])
		fields := []any{
			"dynamic_tools_count", len(params.DynamicTools),
		}
		fields = append(fields, platformshared.SafePayloadLogFields("first_tool_json", firstTool)...)
		pkglogger.Info("codexapp: thread/start payload debug", fields...)
	}
	raw, err := callWithTimeout(ctx, t, 30*time.Second, "thread/start", params)
	if err != nil {
		logThreadStartIdentityTrace("codexapp: thread/start request failed", t.serverURL, req, params, err)
		return startResult{}, supportutil.WrapCodexModelUnsupportedError(err, params.Model)
	}
	return decodeStartResult(raw)
}

func threadStartModelResolutionSource(req dto.StartSessionRequest) supportutil.CodexModelResolutionSource {
	if strings.TrimSpace(req.Model) != "" {
		return supportutil.CodexModelResolutionSourceExplicit
	}
	return supportutil.CodexModelResolutionSourceDefault
}

// logThreadStartIdentityTrace 输出身份路由相关字段，方便排查 provider 选错的问题。
func logThreadStartIdentityTrace(msg, serverURL string, req dto.StartSessionRequest, params threadStartParams, err error) {
	provider := strings.TrimSpace(req.Provider)
	configModelProvider := supportutil.ConfigString(req.Config, "modelProvider")
	configCodexModelProvider := supportutil.ConfigString(req.Config, "codexModelProvider")
	if !strings.EqualFold(provider, "codex") &&
		strings.TrimSpace(params.ModelProvider) == "" &&
		configModelProvider == "" &&
		configCodexModelProvider == "" {
		return
	}
	fields := []any{
		"agent_id", strings.TrimSpace(req.AgentID),
		"provider", provider,
		"server_url", strings.TrimSpace(serverURL),
		"params_model_provider", strings.TrimSpace(params.ModelProvider),
		"config_model_provider", configModelProvider,
		"config_codex_model_provider", configCodexModelProvider,
		"model", strings.TrimSpace(params.Model),
		"effort", strings.TrimSpace(params.Effort),
		"approval_policy", strings.TrimSpace(params.ApprovalPolicy),
		"config_keys", supportutil.SortedConfigKeys(req.Config),
	}
	fields = append(fields, platformshared.SafePathLogFields("cwd", strings.TrimSpace(params.Cwd))...)
	if err != nil {
		fields = append(fields, "error", err)
	}
	pkglogger.Warn(msg, fields...)
}

// restoreApprovalPolicy 从远端线程配置恢复已验证的审批策略。
func (d *driver) restoreApprovalPolicy(ctx context.Context, s *session, threadID string) error {
	if d == nil {
		return errors.New("codexapp: approval policy restore requires driver")
	}
	if s == nil || s.transport == nil {
		return errors.New("codexapp: approval policy restore requires session transport")
	}
	result, err := s.transport.Call(ctx, "thread/config/get", map[string]any{
		"threadId": threadID,
	})
	if err != nil {
		return fmt.Errorf("codexapp: approval policy remote verification failed: %w", err)
	}
	var resp map[string]any
	if err := json.Unmarshal(result, &resp); err != nil {
		return fmt.Errorf("codexapp: approval policy response decode failed: %w", err)
	}
	effective, ok := resp["effective"].(map[string]any)
	if !ok || effective == nil {
		return errors.New("codexapp: approval policy response effective object is required")
	}
	approval, ok := effective["approvals"].(string)
	if !ok {
		return errors.New("codexapp: approval policy response approvals string is required")
	}
	approval = strings.TrimSpace(approval)
	switch approval {
	case "untrusted", "on-failure", "on-request", "never":
	default:
		return fmt.Errorf("codexapp: approval policy response contains unknown policy %q", approval)
	}
	s.setApprovalPolicy(approval)
	s.setRuntimeConfigValue("approvalPolicy", approval)
	return nil
}

func (d *driver) reportRuntime(agentID, serverURL string) error {
	if d == nil || d.reporter == nil {
		return nil
	}
	agentID = strings.TrimSpace(agentID)
	if agentID == "" {
		return nil
	}
	ctx, cancel := platformconfig.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	// Runtime reports use the configured app-server endpoint port until the
	// Codex App protocol exposes a provider-reported control/runtime port.
	if err := d.reporter.ReportRuntime(ctx, contract.RuntimeReport{
		AgentID:  agentID,
		Port:     parsePortFromURL(serverURL),
		Provider: d.Name(),
	}); err != nil {
		return err
	}
	return nil
}
