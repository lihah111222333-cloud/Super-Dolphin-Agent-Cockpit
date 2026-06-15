package thread

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
	"github.com/anthropic-ai/super-agent-v3/internal/module/thread/startconfig"
	"github.com/anthropic-ai/super-agent-v3/internal/util"
	"github.com/anthropic-ai/super-agent-v3/internal/util/clone"
	"github.com/anthropic-ai/super-agent-v3/internal/util/idgen"
	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
)

const maxAgentIDReservationRetries = 64

func normalizeStartRequest(req StartRequest) (StartRequest, string, error) {
	req = trimStartRequest(req)
	req.Name = normalizeStartDisplayName(req.Name)
	if req.LaunchIntentID != "" && req.AgentID != "" {
		return StartRequest{}, "", errors.New("thread: agent_id cannot be provided with launch_intent_id")
	}
	if req.AgentID == "" {
		req.AgentID = idgen.NewAgentID()
	}
	req, err := resolveStartConfig(req)
	if err != nil {
		return StartRequest{}, "", err
	}
	return req, req.AgentID, nil
}

// reserveUniqueStartAgentID 为新会话预留唯一 agent id。
func (s *service) reserveUniqueStartAgentID(
	ctx context.Context,
	req StartRequest,
	candidate string,
	callerProvidedID bool,
) (string, func(), error) {
	candidate = strings.TrimSpace(candidate)
	parentID := strings.TrimSpace(req.ParentAgentID)
	if candidate == "" {
		candidate = idgen.NewAgentID()
	}
	s.agentIDMu.Lock()
	defer s.agentIDMu.Unlock()
	if s.agentIDReservations == nil {
		s.agentIDReservations = make(map[string]struct{})
	}
	if parentID != "" && !callerProvidedID {
		return s.reserveNextChildAgentIDLocked(ctx, parentID)
	}
	release, err := s.reserveAgentIDIfAvailableLocked(ctx, candidate)
	if err != nil {
		return "", nil, err
	}
	if release != nil {
		return candidate, release, nil
	}
	if parentID != "" {
		return s.reserveNextChildAgentIDLocked(ctx, parentID)
	}
	return s.reserveGeneratedRootAgentIDLocked(ctx)
}

// reserveNextChildAgentIDLocked 在锁内为子代理分配下一个 id。
func (s *service) reserveNextChildAgentIDLocked(ctx context.Context, parentID string) (string, func(), error) {
	base := int64(0)
	if s.threadStore != nil {
		if count, err := s.threadStore.CountChildren(ctx, parentID); err != nil {
			return "", nil, fmt.Errorf("thread: count child agent_ids for %q: %w", parentID, err)
		} else if count > 0 {
			base = count
		}
	}
	for i := 0; i < maxAgentIDReservationRetries; i++ {
		candidate := idgen.NewChildAgentID(parentID, int(base)+1+i)
		release, err := s.reserveAgentIDIfAvailableLocked(ctx, candidate)
		if err != nil {
			return "", nil, err
		}
		if release != nil {
			return candidate, release, nil
		}
	}
	return s.reserveGeneratedRootAgentIDLocked(ctx)
}

func (s *service) reserveGeneratedRootAgentIDLocked(ctx context.Context) (string, func(), error) {
	for i := 0; i < maxAgentIDReservationRetries; i++ {
		candidate := idgen.NewAgentID()
		release, err := s.reserveAgentIDIfAvailableLocked(ctx, candidate)
		if err != nil {
			return "", nil, err
		}
		if release != nil {
			return candidate, release, nil
		}
	}
	return "", nil, fmt.Errorf("thread: reserve generated agent_id exhausted after %d attempts", maxAgentIDReservationRetries)
}

func (s *service) reserveAgentIDIfAvailableLocked(ctx context.Context, agentID string) (func(), error) {
	agentID = strings.TrimSpace(agentID)
	if agentID == "" {
		return nil, nil
	}
	inUse, err := s.agentIDInUseLocked(ctx, agentID)
	if err != nil {
		return nil, err
	}
	if inUse {
		return nil, nil
	}
	return s.reserveAgentIDLocked(agentID), nil
}

func (s *service) reserveAgentIDLocked(agentID string) func() {
	agentID = strings.TrimSpace(agentID)
	s.agentIDReservations[agentID] = struct{}{}
	return func() {
		s.agentIDMu.Lock()
		delete(s.agentIDReservations, agentID)
		s.agentIDMu.Unlock()
	}
}

// agentIDInUseLocked 在锁内判断 agent id 是否已被占用。
func (s *service) agentIDInUseLocked(ctx context.Context, agentID string) (bool, error) {
	if _, ok := s.agentIDReservations[agentID]; ok {
		return true, nil
	}
	if s.threadStore != nil {
		exists, err := s.threadStore.Exists(ctx, agentID)
		if err != nil {
			return true, fmt.Errorf("thread: check agent_id %q in thread store: %w", agentID, err)
		}
		if exists {
			return true, nil
		}
	}
	if s.bindingStore != nil {
		binding, err := s.bindingStore.GetByAgentID(ctx, agentID)
		if err == nil && binding != nil {
			return true, nil
		}
		if err != nil && !contract.IsNotFound(err) {
			return true, fmt.Errorf("thread: check agent_id %q in binding store: %w", agentID, err)
		}
	}
	return false, nil
}

func trimStartRequest(req StartRequest) StartRequest {
	req.Provider = strings.TrimSpace(req.Provider)
	req.AgentID = strings.TrimSpace(req.AgentID)
	req.ParentAgentID = strings.TrimSpace(req.ParentAgentID)
	req.AgentType = strings.TrimSpace(req.AgentType)
	req.AgentMemoryScope = strings.TrimSpace(req.AgentMemoryScope)
	req.CWD = strings.TrimSpace(req.CWD)
	req.Model = sanitizeConfigStringArtifact(req.Model)
	req.ModelProvider = sanitizeConfigStringArtifact(req.ModelProvider)
	req.Name = strings.TrimSpace(req.Name)
	req.Prompt = strings.TrimSpace(req.Prompt)
	req.OwnerThreadID = strings.TrimSpace(req.OwnerThreadID)
	req.BaseInstructions = strings.TrimSpace(req.BaseInstructions)
	req.DeveloperInstructions = strings.TrimSpace(req.DeveloperInstructions)
	req.ApprovalPolicy = strings.TrimSpace(req.ApprovalPolicy)
	req.Sandbox = trimRawJSON(req.Sandbox)
	req.Summary = strings.TrimSpace(req.Summary)
	req.Effort = sanitizeConfigStringArtifact(req.Effort)
	req.Personality = strings.TrimSpace(req.Personality)
	req.Language = strings.TrimSpace(req.Language)
	req.GitRoot = strings.TrimSpace(req.GitRoot)
	req.ToolSurfaceMode = strings.TrimSpace(req.ToolSurfaceMode)
	return req
}

// resolveStartConfig 解析启动会话需要的 provider、模型和工作目录配置。
func resolveStartConfig(req StartRequest) (StartRequest, error) {
	// ModelProvider from frontend (e.g. "claude") should drive provider selection
	// when Provider is not explicitly set.
	providerInput := req.Provider
	modelProviderInput := req.ModelProvider
	provider, err := resolveStartProvider(util.FirstNonEmpty(req.Provider, req.ModelProvider))
	if err != nil {
		return StartRequest{}, err
	}
	req.Provider = provider
	if strings.EqualFold(provider, "codex") || strings.TrimSpace(req.ModelProvider) != "" {
		pkglogger.Warn("thread/start: provider resolved",
			"provider_input", providerInput,
			"model_provider_input", modelProviderInput,
			"resolved_provider", provider,
			"cwd", req.CWD,
		)
	}
	req.CWD, err = resolveStartCWD(req.CWD)
	if err != nil {
		return StartRequest{}, err
	}
	req.ToolSurfaceMode, err = contract.NormalizeToolSurfaceMode(req.ToolSurfaceMode)
	if err != nil {
		return StartRequest{}, fmt.Errorf("thread start tool surface mode: %w", err)
	}
	req.Sandbox, err = startconfig.SanitizeSandbox(req.Sandbox)
	if err != nil {
		return StartRequest{}, err
	}
	req.ApprovalPolicy, err = resolveStartApprovalPolicy(req.ApprovalPolicy, req.Sandbox)
	if err != nil {
		return StartRequest{}, err
	}
	return req, nil
}

func resolveStartProvider(provider string) (string, error) {
	normalized := strings.ToLower(strings.TrimSpace(provider))
	if normalized == "" {
		return "", errors.New("provider is required")
	}
	switch normalized {
	case "codex":
		return normalized, nil
	default:
		return "", fmt.Errorf("invalid provider %q", strings.TrimSpace(provider))
	}
}

func resolveStartCWD(cwd string) (string, error) {
	if cwd = strings.TrimSpace(cwd); cwd != "" {
		if cwd == "." {
			return "", errors.New("thread start cwd must be explicit; got dot")
		}
		return cwd, nil
	}
	return "", errors.New("thread start cwd is required")
}

func resolveStartApprovalPolicy(policy string, sandbox json.RawMessage) (string, error) {
	raw := strings.TrimSpace(policy)
	if raw == "" {
		dangerFullAccess, err := startconfig.IsDangerFullAccessSandbox(sandbox)
		if err != nil {
			return "", err
		}
		if dangerFullAccess {
			return "never", nil
		}
		return "", nil
	}
	switch strings.ToLower(raw) {
	case "always", "never", "auto", "on-request", "on-failure", "untrusted":
		return strings.ToLower(raw), nil
	default:
		return "", fmt.Errorf("invalid approval policy %q", raw)
	}
}

// startSession 把 start 结果交给 provider。
// prompt 已经组好，snapshot 也在 assembly 里；这里只检查 cwd/starter 并启动 session。
func (s *service) startSession(ctx context.Context, req StartRequest, input contract.StartInput, assembly contract.StartAssembly, agentID string) (contract.Session, error) {
	if s.starter == nil {
		return nil, errors.New("session starter is not configured")
	}
	cwd, err := resolveStartCWD(req.CWD)
	if err != nil {
		return nil, err
	}
	config := buildStartSessionConfig(req, input, assembly)
	pkglogger.Debug("thread/start: provider session config trace",
		"agent_id", agentID,
		"provider", req.Provider,
		"req_model", req.Model,
		"req_effort", req.Effort,
		"input_model", input.Model,
		"config_model", configTraceString(config, "model"),
		"config_effort", configTraceString(config, "effort"),
	)
	logStartProviderSessionIdentity(agentID, req, config)
	sessionCtx := context.WithoutCancel(ctx)
	return s.starter.StartSession(sessionCtx, dto.StartSessionRequest{
		Provider:        req.Provider,
		AgentID:         agentID,
		CWD:             cwd,
		Model:           req.Model,
		Instructions:    assembly.BaseInstructions,
		StartAssembly:   toProviderStartAssembly(assembly),
		Config:          config,
		ToolSurfaceMode: req.ToolSurfaceMode,
		// Legacy additive carrier only. Current skill runtime is resolved via
		// canonical skills -> provider-native mirrors before provider launch.
		LaunchSkillNames:  append([]string(nil), req.LaunchSkillNames...),
		LaunchSkillRefs:   cloneProviderSkillRefs(req.LaunchSkillRefs),
		ForceLaunchSkills: req.ForceLaunchSkills,
	})
}

func cloneProviderSkillRefs(refs []dto.SkillRef) []dto.SkillRef {
	if len(refs) == 0 {
		return nil
	}
	out := make([]dto.SkillRef, len(refs))
	copy(out, refs)
	return out
}

// logStartProviderSessionIdentity 记录启动后的 provider session 身份。
func logStartProviderSessionIdentity(agentID string, req StartRequest, config map[string]any) {
	if !strings.EqualFold(strings.TrimSpace(req.Provider), "codex") &&
		strings.TrimSpace(req.ModelProvider) == "" &&
		configTraceString(config, "provider") == "" &&
		configTraceString(config, "modelProvider") == "" &&
		configTraceString(config, "codexModelProvider") == "" {
		return
	}
	pkglogger.Warn("thread/start: provider session identity trace",
		"agent_id", agentID,
		"provider", req.Provider,
		"req_model_provider", req.ModelProvider,
		"req_model", req.Model,
		"req_effort", req.Effort,
		"config_provider", configTraceString(config, "provider"),
		"config_model_provider", configTraceString(config, "modelProvider"),
		"config_codex_model_provider", configTraceString(config, "codexModelProvider"),
		"config_model", configTraceString(config, "model"),
		"config_effort", configTraceString(config, "effort"),
	)
}

// resumeSession 恢复用户已有的 thread。
// 它先从 store/binding 取回 config 和 prompt snapshot，再交给 provider。
func (s *service) resumeSession(ctx context.Context, req ResumeRequest) (contract.Session, error) {
	resolvedReq, err := s.hydrateResumeSessionRequest(ctx, req)
	if err != nil {
		return nil, err
	}
	return s.resumeResolvedSession(ctx, resolvedReq)
}

// resumeForkSession 用在 fork 后的新 provider thread。
// 调用方已经给了 snapshot、provider 和 agent id，这里只做必要检查。
func (s *service) resumeForkSession(ctx context.Context, req ResumeRequest) (contract.Session, error) {
	resolvedReq, err := trimResumeRequest(req)
	if err != nil {
		return nil, err
	}
	if resolvedReq.Provider == "" {
		return nil, errors.New("provider is required")
	}
	if resolvedReq.AgentID == "" {
		return nil, errors.New("agent id is required")
	}
	return s.resumeResolvedSession(ctx, resolvedReq)
}

// resumeResolvedSession 把已整理好的 resume 请求发给 provider。
// 这里不再查 store，只确认 starter 和 cwd 能用。
func (s *service) resumeResolvedSession(ctx context.Context, resolvedReq ResumeRequest) (contract.Session, error) {
	if s.starter == nil {
		return nil, errors.New("session starter is not configured")
	}
	cwd := strings.TrimSpace(resolvedReq.CWD)
	if cwd == "" || cwd == "." {
		return nil, errors.New("thread resume cwd is required")
	}
	return s.starter.ResumeSession(context.WithoutCancel(ctx), dto.ResumeSessionRequest{
		Provider:                 resolvedReq.Provider,
		AgentID:                  resolvedReq.AgentID,
		ThreadID:                 resolvedReq.ThreadID,
		ProviderThreadID:         resolvedReq.ProviderThreadID,
		Path:                     resolvedReq.Path,
		CWD:                      cwd,
		Model:                    resolvedReq.Model,
		Effort:                   resolvedReq.Effort,
		Config:                   clone.RuntimeConfigMap(resolvedReq.Config),
		PromptSnapshot:           toProviderPromptSnapshot(resolvedReq.PromptSnapshot),
		ConfigOverride:           resolvedReq.ConfigOverride,
		ClaudeHome:               resolvedReq.ClaudeHome,
		CodexHome:                resolvedReq.CodexHome,
		CodexInstanceKey:         resolvedReq.CodexInstanceKey,
		CodexModelProvider:       resolvedReq.CodexModelProvider,
		CodexDisabledNativeTools: append([]string(nil), resolvedReq.CodexDisabledNativeTools...),
	})
}

func (s *service) lookupSession(agentID string) (contract.Session, error) {
	if s.sessions == nil {
		return nil, errors.New("session provider is not configured")
	}
	return s.sessions.GetSession(strings.TrimSpace(agentID))
}

type resumeState struct {
	AgentID            string
	ParentAgentID      string
	OwnerThreadID      string
	AgentType          string
	AgentMemoryScope   string
	Provider           string
	ProviderThreadID   string
	PublicThreadID     string
	Prompt             string
	Model              string
	Effort             string
	ConfigOverride     storedThreadConfig
	ConfigOverrideRaw  json.RawMessage
	CWD                string
	StoredCWD          string
	RolloutPath        string
	SessionUUID        string
	ClaudeHome         string
	CodexHome          string
	CodexInstanceKey   string
	CodexModelProvider string
	CreatedAt          int64
}

// resolveResumeRequest 整理 service.Resume 要用的状态。
// thread row、binding 和 runtime config 会合并到这里，UI 传来的旧值不能覆盖它们。
func (s *service) resolveResumeRequest(ctx context.Context, req ResumeRequest) (ResumeRequest, resumeState, error) {
	req, err := trimResumeRequest(req)
	if err != nil {
		return ResumeRequest{}, resumeState{}, err
	}
	requestedThreadID := req.ThreadID
	state, err := s.lookupResumeState(ctx, requestedThreadID)
	if err != nil {
		return ResumeRequest{}, resumeState{}, err
	}
	state.PublicThreadID = util.FirstNonEmpty(state.PublicThreadID, requestedThreadID)
	req.AgentID = util.FirstNonEmpty(req.AgentID, state.AgentID)
	req.Provider = util.FirstNonEmpty(req.Provider, state.Provider)
	req.ProviderThreadID = normalizeProviderThreadID(req.Provider, util.FirstNonEmpty(req.ProviderThreadID, state.ProviderThreadID))

	req.CWD, err = resolveAuthoritativeResumeCWD(req, state)
	if err != nil {
		return ResumeRequest{}, resumeState{}, err
	}
	req.ClaudeHome = util.FirstNonEmpty(req.ClaudeHome, state.ClaudeHome, resumeRuntimeConfigString(state.ConfigOverride.Runtime, "claudeHome", "claude_home", "history_dir"))
	req = hydrateResumeCodexIdentity(req, state)
	req.CodexDisabledNativeTools = resolveResumeCodexDisabledNativeTools(req.CodexDisabledNativeTools, state.ConfigOverride.Runtime)
	req.Config = mergeRuntimeConfig(clone.RuntimeConfigMap(state.ConfigOverride.Runtime), req.Config)
	req, err = s.injectDefaultCodexIdentityForResume(req)
	if err != nil {
		return ResumeRequest{}, resumeState{}, err
	}
	req.ConfigOverride = resolveResumeConfigOverride(req, state)
	req.Model = resolveResumeModel(req, state)
	req.Effort = resolveResumeEffort(req, state)
	req.ThreadID = state.PublicThreadID
	if req.Provider == "" {
		return ResumeRequest{}, resumeState{}, errors.New("provider is required")
	}
	if req.AgentID == "" {
		return ResumeRequest{}, resumeState{}, errors.New("agent id is required")
	}
	state.CWD = req.CWD
	state.Model = req.Model
	state.Effort = req.Effort
	state.ClaudeHome = util.FirstNonEmpty(state.ClaudeHome, req.ClaudeHome)
	state.CodexHome = util.FirstNonEmpty(state.CodexHome, req.CodexHome)
	state.CodexInstanceKey = util.FirstNonEmpty(state.CodexInstanceKey, req.CodexInstanceKey)
	state.CodexModelProvider = util.FirstNonEmpty(state.CodexModelProvider, req.CodexModelProvider)
	return req, state, nil
}

func hydrateResumeCodexIdentity(req ResumeRequest, state resumeState) ResumeRequest {
	if !strings.EqualFold(strings.TrimSpace(req.Provider), "codex") {
		return req
	}
	runtime := state.ConfigOverride.Runtime
	req.CodexHome = util.FirstNonEmpty(
		req.CodexHome,
		state.CodexHome,
		resumeRuntimeConfigString(runtime, contract.CodexHomeKey, "codex_home"),
	)
	req.CodexInstanceKey = util.FirstNonEmpty(
		req.CodexInstanceKey,
		state.CodexInstanceKey,
		resumeRuntimeConfigString(runtime, contract.CodexInstanceKeyKey, "codex_instance_key"),
	)
	req.CodexModelProvider = util.FirstNonEmpty(
		req.CodexModelProvider,
		state.CodexModelProvider,
		resumeRuntimeConfigString(runtime, contract.CodexModelProviderKey, "codex_model_provider"),
	)
	return req
}

func trimResumeRequest(req ResumeRequest) (ResumeRequest, error) {
	req.Provider = strings.TrimSpace(req.Provider)
	req.AgentID = strings.TrimSpace(req.AgentID)
	req.ThreadID = strings.TrimSpace(req.ThreadID)
	req.Path = strings.TrimSpace(req.Path)
	req.CWD = strings.TrimSpace(req.CWD)
	req.Model = sanitizeConfigStringArtifact(req.Model)
	req.Effort = sanitizeConfigStringArtifact(req.Effort)
	req.ClaudeHome = strings.TrimSpace(req.ClaudeHome)
	req.CodexHome = strings.TrimSpace(req.CodexHome)
	req.CodexInstanceKey = strings.TrimSpace(req.CodexInstanceKey)
	req.CodexModelProvider = strings.TrimSpace(req.CodexModelProvider)
	req.ConfigOverride.Model = trimThreadConfigPatchValue(req.ConfigOverride.Model)
	req.ConfigOverride.Effort = trimThreadConfigPatchValue(req.ConfigOverride.Effort)
	req.ConfigOverride.Personality = nil
	req.ConfigOverride.Approvals = nil
	if req.ThreadID == "" {
		return ResumeRequest{}, errors.New("thread id is required")
	}
	return req, nil
}

// resolveResumeConfigOverride 解析恢复会话时允许覆盖的配置。
func resolveResumeConfigOverride(req ResumeRequest, state resumeState) dto.ThreadConfigPatch {
	patch := dto.ThreadConfigPatch{
		Model:       trimThreadConfigPatchValue(req.ConfigOverride.Model),
		Effort:      trimThreadConfigPatchValue(req.ConfigOverride.Effort),
		Personality: nil,
		Approvals:   nil,
	}
	if patch.Model == nil {
		if value := sanitizeConfigStringArtifact(req.Model); value != "" {
			patch.Model = &value
		} else if value := sanitizeConfigStringArtifact(state.ConfigOverride.Model); value != "" {
			patch.Model = &value
		}
	}
	if patch.Effort == nil {
		if value := sanitizeConfigStringArtifact(req.Effort); value != "" {
			patch.Effort = &value
		} else if value := sanitizeConfigStringArtifact(state.ConfigOverride.Effort); value != "" {
			patch.Effort = &value
		}
	}
	return patch
}

// resolveResumeModel 解析恢复会话时使用的模型。
func resolveResumeModel(req ResumeRequest, state resumeState) string {
	if req.ConfigOverride.Model != nil {
		if value := threadConfigPatchValue(req.ConfigOverride.Model); value != "" {
			return value
		}
		if value := sanitizeConfigStringArtifact(req.Model); value != "" {
			return value
		}
		return sanitizeConfigStringArtifact(state.Model)
	}
	if value := sanitizeConfigStringArtifact(req.Model); value != "" {
		return value
	}
	if value := sanitizeConfigStringArtifact(state.ConfigOverride.Model); value != "" {
		return value
	}
	return sanitizeConfigStringArtifact(state.Model)
}

func resolveResumeEffort(req ResumeRequest, state resumeState) string {
	if req.ConfigOverride.Effort != nil {
		if value := threadConfigPatchValue(req.ConfigOverride.Effort); value != "" {
			return value
		}
		return sanitizeConfigStringArtifact(req.Effort)
	}
	if value := sanitizeConfigStringArtifact(req.Effort); value != "" {
		return value
	}
	return sanitizeConfigStringArtifact(state.ConfigOverride.Effort)
}
func resumeRuntimeConfigString(runtime map[string]any, keys ...string) string {
	for _, key := range keys {
		value, _ := runtime[strings.TrimSpace(key)].(string)
		if text := strings.TrimSpace(value); text != "" {
			return text
		}
	}
	return ""
}
