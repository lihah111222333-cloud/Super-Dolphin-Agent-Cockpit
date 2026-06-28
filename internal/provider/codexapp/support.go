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

	contract "github.com/anthropic-ai/super-agent-v3/internal/contract"
	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
	platformconfig "github.com/anthropic-ai/super-agent-v3/internal/platform/config"
	codexprotocol "github.com/anthropic-ai/super-agent-v3/internal/provider/codexapp/protocol"
	"github.com/anthropic-ai/super-agent-v3/internal/provider/codexapp/supportutil"
	providershared "github.com/anthropic-ai/super-agent-v3/internal/provider/shared"
	"github.com/anthropic-ai/super-agent-v3/internal/util"
	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
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
	pkglogger.Warn("codexapp: runtime codexHome injected from initialize",
		"agent_id", s.agentID,
		"codex_home", home,
		"stage", reason)
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
func resolveSupportedCodexModel(ctx context.Context, t *transport, requested string) (string, bool, error) {
	requested = strings.TrimSpace(requested)
	raw, err := callWithTimeout(ctx, t, 10*time.Second, "model/list", map[string]any{})
	if err != nil {
		return "", false, err
	}
	models, err := supportutil.DecodeAllowedModels(raw)
	if err != nil {
		return "", false, err
	}
	preferred := supportutil.PreferredCodexModel(models)
	if requested == "" {
		return preferred, preferred != "", nil
	}
	if supportutil.CodexModelIsCodexFamily(requested) && supportutil.CodexModelListContains(models, requested) {
		return requested, false, nil
	}
	if !supportutil.CodexModelIsGenericGPT(requested) && supportutil.CodexModelListContains(models, requested) {
		return requested, false, nil
	}
	if preferred != "" {
		return preferred, !strings.EqualFold(preferred, requested), nil
	}
	if supportutil.CodexModelListContains(models, requested) {
		return requested, false, nil
	}
	return requested, false, nil
}

func (d *driver) buildThreadStartParams(req dto.StartSessionRequest) threadStartParams {
	baseInstructions, developerInstructions := d.startAssemblyInstructions(req)
	params := threadStartParams{
		Cwd:                   strings.TrimSpace(req.CWD),
		Model:                 strings.TrimSpace(req.Model),
		ModelProvider:         supportutil.FirstConfigString(req.Config, contract.CodexModelProviderKey, "modelProvider", "model_provider"),
		BaseInstructions:      baseInstructions,
		DeveloperInstructions: developerInstructions,
		ApprovalPolicy:        supportutil.ResolveApprovalPolicy(req.Config),
		Personality:           supportutil.ConfigString(req.Config, "personality"),
		Summary:               supportutil.ConfigString(req.Config, "summary"),
		Effort:                normalizeCodexAppEffort(supportutil.ConfigString(req.Config, "effort")),
		Sandbox:               codexSandboxWireJSON(supportutil.ConfigJSON(req.Config, "sandbox")),
		MCPConfig:             supportutil.ConfigJSON(req.Config, "mcpConfig"),
	}
	codexNativeToolPolicyFromConfig(req.Config).ApplyThreadStartParams(&params)
	return params
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
	return d.finishStartedSession(s, req, result), nil
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
	return contract.CodexToolSurfaceScope{
		AgentID:          strings.TrimSpace(agentID),
		UIThreadID:       strings.TrimSpace(localThreadID),
		LocalThreadID:    strings.TrimSpace(localThreadID),
		ProviderThreadID: strings.TrimSpace(providerThreadID),
		CWD:              cwd,
		WorkspaceRoots:   append([]string(nil), workspaceRoots...),
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

func (d *driver) finishStartedSession(s *session, req dto.StartSessionRequest, result startResult) contract.Session {
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
	d.reportRuntime(s.agentID, s.transport.serverURL)
	return s
}

func primeResumeToolScope(s *session, req dto.ResumeSessionRequest) {
	if resumeID := strings.TrimSpace(req.ProviderThreadID); resumeID != "" {
		s.setThreadID(resumeID)
	}
	if cwd := strings.TrimSpace(req.CWD); cwd != "" {
		s.setRuntimeConfigValue("cwd", cwd)
	}
}

// finishResumedSession 恢复 resume 后本地 session 需要继续使用的运行配置。
func (d *driver) finishResumedSession(ctx context.Context, s *session, req dto.ResumeSessionRequest, threadID string) contract.Session {
	s.setThreadID(threadID)
	s.ensureRuntimeCodexHomeFromInitialize("resume")
	if cwd := strings.TrimSpace(req.CWD); cwd != "" {
		s.setRuntimeConfigValue("cwd", cwd)
	}
	if m := strings.TrimSpace(req.Model); m != "" {
		s.setRuntimeConfigValue("model", m)
	}
	baseInstructions, developerInstructions := promptSnapshotInstructions(req.PromptSnapshot)
	if baseInstructions == "" {
		baseInstructions = fallbackBaseInstructions
	}
	s.setRuntimeConfigValue("baseInstructions", baseInstructions)
	if developerInstructions != "" {
		s.setRuntimeConfigValue("developerInstructions", developerInstructions)
	}
	if len(req.CodexDisabledNativeTools) > 0 {
		s.setRuntimeConfigValue("codexDisabledNativeTools", append([]string(nil), req.CodexDisabledNativeTools...))
	}
	d.restoreApprovalPolicy(ctx, s, threadID)
	applyResumeNativeToolRuntimePolicy(s, req.CodexDisabledNativeTools)
	d.reportRuntime(s.agentID, s.transport.serverURL)
	return s
}

func (d *driver) startRemoteThreadWithDynamicTools(ctx context.Context, t *transport, req dto.StartSessionRequest, tools []codexprotocol.DynamicToolSchema) (startResult, error) {
	params := d.buildThreadStartParams(req)
	params.DynamicTools = tools
	// dynamicTools schema 已由 codex app-server 暴露给模型，developerInstructions 不再重复塞工具名。
	// 这避免把完整工具目录再次写入上下文，保留预算给真实任务内容。
	return startRemoteThreadWithParams(ctx, t, req, params)
}

// startRemoteThreadWithParams 发送 thread/start，并在发送前补齐模型选择和诊断日志。
func startRemoteThreadWithParams(ctx context.Context, t *transport, req dto.StartSessionRequest, params threadStartParams) (startResult, error) {
	configKeys := supportutil.SortedConfigKeys(req.Config)
	if supportutil.CodexModelNeedsListResolution(params.Model) {
		requestedModel := strings.TrimSpace(params.Model)
		model, replaced, err := resolveSupportedCodexModel(ctx, t, requestedModel)
		if err != nil {
			pkglogger.Warn("codexapp: model/list default selection failed",
				"agent_id", strings.TrimSpace(req.AgentID),
				"cwd", params.Cwd,
				"requested_model", requestedModel,
				"error", err,
			)
		} else if model != "" && replaced {
			params.Model = model
			pkglogger.Info("codexapp: thread/start selected supported model from model/list",
				"agent_id", strings.TrimSpace(req.AgentID),
				"requested_model", requestedModel,
				"model", model,
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
		pkglogger.Warn("codexapp: thread/start effort missing",
			"agent_id", strings.TrimSpace(req.AgentID),
			"cwd", params.Cwd,
			"model", params.Model,
			"approval_policy", params.ApprovalPolicy,
			"config_keys", configKeys,
			"expected_config_key", "effort",
		)
	}
	pkglogger.Info("codexapp: thread/start request",
		"agent_id", strings.TrimSpace(req.AgentID),
		"cwd", params.Cwd,
		"model", params.Model,
		"effort", params.Effort,
		"approval_policy", params.ApprovalPolicy,
		"config_keys", configKeys,
		"has_env", hasAnyConfigKey(req.Config, "env"),
		"has_mcp", hasAnyConfigKey(req.Config, "mcp", "mcpConfig", "mcp_config", "mcpServers", "mcp_servers"),
		"has_hooks", hasAnyConfigKey(req.Config, "hooks", "hookConfig", "hook_config"),
	)
	logThreadStartIdentityTrace("codexapp: thread/start identity trace", t.serverURL, req, params, nil)
	if len(params.DynamicTools) > 0 {
		firstTool, _ := json.Marshal(params.DynamicTools[0])
		pkglogger.Info("codexapp: thread/start payload debug",
			"dynamic_tools_count", len(params.DynamicTools),
			"first_tool_json", string(firstTool),
		)
	}
	raw, err := callWithTimeout(ctx, t, 30*time.Second, "thread/start", params)
	if err != nil {
		logThreadStartIdentityTrace("codexapp: thread/start request failed", t.serverURL, req, params, err)
		return startResult{}, supportutil.WrapCodexModelUnsupportedError(err, params.Model)
	}
	return decodeStartResult(raw)
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
		"cwd", strings.TrimSpace(params.Cwd),
		"config_keys", supportutil.SortedConfigKeys(req.Config),
	}
	if err != nil {
		fields = append(fields, "error", err)
	}
	pkglogger.Warn(msg, fields...)
}

// restoreApprovalPolicy 从远端线程配置恢复审批策略，失败时保留本地已有状态。
func (d *driver) restoreApprovalPolicy(ctx context.Context, s *session, threadID string) {
	if d == nil || s == nil {
		return
	}
	result, err := s.transport.Call(ctx, "thread/config/get", map[string]any{
		"threadId": threadID,
	})
	if err != nil {
		// RPC not available – fall back to local state.
		s.setRuntimeConfigValue("approvalPolicy", s.approvalPolicyValue())
		return
	}
	var resp map[string]any
	if err := json.Unmarshal(result, &resp); err != nil {
		s.setRuntimeConfigValue("approvalPolicy", s.approvalPolicyValue())
		return
	}
	effective, _ := resp["effective"].(map[string]any)
	if effective == nil {
		s.setRuntimeConfigValue("approvalPolicy", s.approvalPolicyValue())
		return
	}
	if approval, ok := effective["approvals"].(string); ok && strings.TrimSpace(approval) != "" {
		s.setApprovalPolicy(strings.TrimSpace(approval))
		s.setRuntimeConfigValue("approvalPolicy", strings.TrimSpace(approval))
		return
	}
	s.setRuntimeConfigValue("approvalPolicy", s.approvalPolicyValue())
}

func (d *driver) reportRuntime(agentID, serverURL string) {
	if d == nil || d.reporter == nil {
		return
	}
	agentID = strings.TrimSpace(agentID)
	if agentID == "" {
		return
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
		d.logger.Warn("codexapp: report runtime failed", "agent_id", agentID, "error", err)
	}
}
