package thread

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
	platformdb "github.com/anthropic-ai/super-agent-v3/internal/platform/db"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/shared"
	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
)

const defaultStartProvider = "codex"
const maxAgentIDReservationRetries = 64

func normalizeStartRequest(req StartRequest) (StartRequest, string, error) {
	req = trimStartRequest(req)
	req.Name = normalizeStartDisplayName(req.Name)
	if req.AgentID == "" {
		// Root agent: timestamp-only ID. Collision is checked at
		// the service layer which retries with a fresh timestamp.
		req.AgentID = shared.NewAgentID()
	}
	req, err := resolveStartConfig(req)
	if err != nil {
		return StartRequest{}, "", err
	}
	return req, req.AgentID, nil
}

func (s *service) reserveUniqueStartAgentID(
	ctx context.Context,
	req StartRequest,
	candidate string,
	callerProvidedID bool,
) (string, func(), error) {
	candidate = strings.TrimSpace(candidate)
	parentID := strings.TrimSpace(req.ParentAgentID)
	if candidate == "" {
		candidate = shared.NewAgentID()
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
		candidate := shared.NewChildAgentID(parentID, int(base)+1+i)
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
		candidate := shared.NewAgentID()
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
		if err != nil && !platformdb.IsNotFound(err) {
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
	req.Model = strings.TrimSpace(req.Model)
	req.ModelProvider = strings.TrimSpace(req.ModelProvider)
	req.Name = strings.TrimSpace(req.Name)
	req.Prompt = strings.TrimSpace(req.Prompt)
	req.OwnerThreadID = strings.TrimSpace(req.OwnerThreadID)
	req.BaseInstructions = strings.TrimSpace(req.BaseInstructions)
	req.DeveloperInstructions = strings.TrimSpace(req.DeveloperInstructions)
	req.ApprovalPolicy = strings.TrimSpace(req.ApprovalPolicy)
	req.Sandbox = trimRawJSON(req.Sandbox)
	req.Summary = strings.TrimSpace(req.Summary)
	req.Effort = strings.TrimSpace(req.Effort)
	req.Personality = strings.TrimSpace(req.Personality)
	req.Language = strings.TrimSpace(req.Language)
	req.GitRoot = strings.TrimSpace(req.GitRoot)
	return req
}

func resolveStartConfig(req StartRequest) (StartRequest, error) {
	// ModelProvider from frontend (e.g. "claude") should drive provider selection
	// when Provider is not explicitly set.
	provider, err := resolveStartProvider(shared.FirstNonEmpty(req.Provider, req.ModelProvider))
	if err != nil {
		return StartRequest{}, err
	}
	req.Provider = provider
	req.CWD = resolveStartCWD(req.CWD)
	req.Sandbox = sanitizeStartSandbox(req.Sandbox)
	req.ApprovalPolicy, err = resolveStartApprovalPolicy(req.ApprovalPolicy, req.Sandbox)
	if err != nil {
		return StartRequest{}, err
	}
	return req, nil
}

func resolveStartProvider(provider string) (string, error) {
	normalized := strings.ToLower(strings.TrimSpace(provider))
	if normalized == "" {
		return defaultStartProvider, nil
	}
	switch normalized {
	case "codex", "claude":
		return normalized, nil
	default:
		return "", fmt.Errorf("invalid provider %q", strings.TrimSpace(provider))
	}
}

func resolveStartCWD(cwd string) string {
	if cwd = strings.TrimSpace(cwd); cwd != "" {
		return cwd
	}
	if wd, err := os.Getwd(); err == nil {
		if wd = strings.TrimSpace(wd); wd != "" {
			return wd
		}
	}
	return "."
}

func resolveStartApprovalPolicy(policy string, sandbox json.RawMessage) (string, error) {
	raw := strings.TrimSpace(policy)
	if raw == "" {
		if isDangerFullAccessSandbox(sandbox) {
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

func sanitizeStartSandbox(raw json.RawMessage) json.RawMessage {
	raw = trimRawJSON(raw)
	if len(raw) == 0 || !json.Valid(raw) {
		return nil
	}
	return raw
}

func isDangerFullAccessSandbox(raw json.RawMessage) bool {
	raw = sanitizeStartSandbox(raw)
	if len(raw) == 0 {
		return false
	}
	if raw[0] == '{' {
		var payload struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal(raw, &payload); err == nil {
			return isDangerFullAccessValue(payload.Type)
		}
	}
	var value string
	if err := json.Unmarshal(raw, &value); err == nil {
		return isDangerFullAccessValue(value)
	}
	return false
}

func isDangerFullAccessValue(value string) bool {
	normalized := strings.ToLower(strings.TrimSpace(value))
	normalized = strings.ReplaceAll(normalized, "-", "")
	return normalized == "dangerfullaccess"
}

func (s *service) startSession(ctx context.Context, req StartRequest, input contract.StartInput, assembly contract.StartAssembly, agentID string) (contract.Session, error) {
	if s.starter == nil {
		return nil, errors.New("session starter is not configured")
	}
	cwd := strings.TrimSpace(req.CWD)
	if cwd == "" || cwd == "." {
		if abs, err := os.Getwd(); err == nil {
			cwd = abs
		}
	}
	config := buildStartSessionConfig(req, input, assembly)
	pkglogger.Warn("thread/start: provider session config trace",
		"agent_id", agentID,
		"provider", req.Provider,
		"req_model", req.Model,
		"req_effort", req.Effort,
		"input_model", input.Model,
		"config_model", configTraceString(config, "model"),
		"config_effort", configTraceString(config, "effort"),
	)
	sessionCtx := context.WithoutCancel(ctx)
	return s.starter.StartSession(sessionCtx, dto.StartSessionRequest{
		Provider:      req.Provider,
		AgentID:       agentID,
		CWD:           cwd,
		Model:         req.Model,
		Instructions:  assembly.BaseInstructions,
		StartAssembly: toProviderStartAssembly(assembly),
		Config:        config,
		// p20.3 §4.3：additive optional carrier。nil/false 时整个代码路径
		// 等同于旧 payload；p20.4 / p20.7 消费时再他者在 snapshot / manifest
		// 层面施工，本单仍不涉及。
		LaunchSkillNames:  append([]string(nil), req.LaunchSkillNames...),
		ForceLaunchSkills: req.ForceLaunchSkills,
	})
}

func (s *service) resumeSession(ctx context.Context, req ResumeRequest) (contract.Session, error) {
	if s.starter == nil {
		return nil, errors.New("session starter is not configured")
	}
	resolvedReq, err := s.hydrateResumeSessionRequest(ctx, req)
	if err != nil {
		return nil, err
	}
	cwd := strings.TrimSpace(resolvedReq.CWD)
	if cwd == "" || cwd == "." {
		if abs, err := os.Getwd(); err == nil {
			cwd = abs
		}
	}
	sessionCtx := context.WithoutCancel(ctx)
	return s.starter.ResumeSession(sessionCtx, dto.ResumeSessionRequest{
		Provider:           resolvedReq.Provider,
		AgentID:            resolvedReq.AgentID,
		ThreadID:           resolvedReq.ThreadID,
		ProviderThreadID:   resolvedReq.ProviderThreadID,
		Path:               resolvedReq.Path,
		CWD:                cwd,
		Model:              resolvedReq.Model,
		Effort:             resolvedReq.Effort,
		PromptSnapshot:     toProviderPromptSnapshot(resolvedReq.PromptSnapshot),
		ConfigOverride:     resolvedReq.ConfigOverride,
		CodexHome:          resolvedReq.CodexHome,
		CodexInstanceKey:   resolvedReq.CodexInstanceKey,
		CodexModelProvider: resolvedReq.CodexModelProvider,
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
	CodexHome          string
	CodexInstanceKey   string
	CodexModelProvider string
	CreatedAt          int64
}

func (s *service) resolveResumeRequest(ctx context.Context, req ResumeRequest) (ResumeRequest, resumeState, error) {
	req, err := trimResumeRequest(req)
	if err != nil {
		return ResumeRequest{}, resumeState{}, err
	}
	requestedThreadID := req.ThreadID
	state := s.lookupResumeState(ctx, requestedThreadID)
	state.PublicThreadID = shared.FirstNonEmpty(state.PublicThreadID, requestedThreadID)
	state.ProviderThreadID = shared.FirstNonEmpty(state.ProviderThreadID, requestedThreadID)
	req.AgentID = shared.FirstNonEmpty(req.AgentID, state.AgentID)
	req.Provider = shared.FirstNonEmpty(req.Provider, state.Provider)
	req.ProviderThreadID = shared.FirstNonEmpty(req.ProviderThreadID, state.ProviderThreadID)
	req.CWD = shared.FirstNonEmpty(req.CWD, req.Path, state.CWD)
	req.CodexHome = shared.FirstNonEmpty(req.CodexHome, state.CodexHome)
	req.CodexInstanceKey = shared.FirstNonEmpty(req.CodexInstanceKey, state.CodexInstanceKey)
	req.CodexModelProvider = shared.FirstNonEmpty(req.CodexModelProvider, state.CodexModelProvider)
	req = s.injectDefaultCodexIdentityForResume(req)
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
	state.CodexHome = shared.FirstNonEmpty(state.CodexHome, req.CodexHome)
	state.CodexInstanceKey = shared.FirstNonEmpty(state.CodexInstanceKey, req.CodexInstanceKey)
	state.CodexModelProvider = shared.FirstNonEmpty(state.CodexModelProvider, req.CodexModelProvider)
	return req, state, nil
}

func trimResumeRequest(req ResumeRequest) (ResumeRequest, error) {
	req.Provider = strings.TrimSpace(req.Provider)
	req.AgentID = strings.TrimSpace(req.AgentID)
	req.ThreadID = strings.TrimSpace(req.ThreadID)
	req.Path = strings.TrimSpace(req.Path)
	req.CWD = strings.TrimSpace(req.CWD)
	req.Model = strings.TrimSpace(req.Model)
	req.Effort = strings.TrimSpace(req.Effort)
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

func (s *service) hydrateResumeSessionRequest(ctx context.Context, req ResumeRequest) (ResumeRequest, error) {
	req, err := trimResumeRequest(req)
	if err != nil {
		return ResumeRequest{}, err
	}
	state := s.lookupResumeState(ctx, req.ThreadID)
	state.PublicThreadID = shared.FirstNonEmpty(state.PublicThreadID, req.ThreadID)
	state.ProviderThreadID = shared.FirstNonEmpty(state.ProviderThreadID, req.ThreadID)
	req.AgentID = shared.FirstNonEmpty(req.AgentID, state.AgentID)
	req.Provider = shared.FirstNonEmpty(req.Provider, state.Provider)
	req.ProviderThreadID = shared.FirstNonEmpty(req.ProviderThreadID, state.ProviderThreadID)
	req.CWD = shared.FirstNonEmpty(req.CWD, req.Path, state.CWD)
	req.CodexHome = shared.FirstNonEmpty(req.CodexHome, state.CodexHome)
	req.CodexInstanceKey = shared.FirstNonEmpty(req.CodexInstanceKey, state.CodexInstanceKey)
	req.CodexModelProvider = shared.FirstNonEmpty(req.CodexModelProvider, state.CodexModelProvider)
	req = s.injectDefaultCodexIdentityForResume(req)
	req.PromptSnapshot = s.resolveResumePromptSnapshot(ctx, req, state)
	if req.ConfigOverride.Model == nil {
		if value := strings.TrimSpace(state.ConfigOverride.Model); value != "" {
			req.ConfigOverride.Model = &value
		}
	}
	if req.ConfigOverride.Effort == nil {
		if value := strings.TrimSpace(state.ConfigOverride.Effort); value != "" {
			req.ConfigOverride.Effort = &value
		}
	}
	if req.Model == "" {
		req.Model = resolveResumeModel(req, state)
	}
	if req.Effort == "" {
		req.Effort = resolveResumeEffort(req, state)
	}
	if req.Provider == "" {
		return ResumeRequest{}, errors.New("provider is required")
	}
	if req.AgentID == "" {
		return ResumeRequest{}, errors.New("agent id is required")
	}
	req.ThreadID = state.PublicThreadID
	return req, nil
}

func resolveResumeConfigOverride(req ResumeRequest, state resumeState) dto.ThreadConfigPatch {
	patch := dto.ThreadConfigPatch{
		Model:       trimThreadConfigPatchValue(req.ConfigOverride.Model),
		Effort:      trimThreadConfigPatchValue(req.ConfigOverride.Effort),
		Personality: nil,
		Approvals:   nil,
	}
	if patch.Model == nil {
		if value := strings.TrimSpace(req.Model); value != "" {
			patch.Model = &value
		} else if value := strings.TrimSpace(state.ConfigOverride.Model); value != "" {
			patch.Model = &value
		}
	}
	if patch.Effort == nil {
		if value := strings.TrimSpace(req.Effort); value != "" {
			patch.Effort = &value
		} else if value := strings.TrimSpace(state.ConfigOverride.Effort); value != "" {
			patch.Effort = &value
		}
	}
	return patch
}

func resolveResumeModel(req ResumeRequest, state resumeState) string {
	if req.ConfigOverride.Model != nil {
		if value := threadConfigPatchValue(req.ConfigOverride.Model); value != "" {
			return value
		}
		if value := strings.TrimSpace(req.Model); value != "" {
			return value
		}
		return strings.TrimSpace(state.Model)
	}
	if value := strings.TrimSpace(req.Model); value != "" {
		return value
	}
	if value := strings.TrimSpace(state.ConfigOverride.Model); value != "" {
		return value
	}
	return strings.TrimSpace(state.Model)
}

func resolveResumeEffort(req ResumeRequest, state resumeState) string {
	if req.ConfigOverride.Effort != nil {
		if value := threadConfigPatchValue(req.ConfigOverride.Effort); value != "" {
			return value
		}
		return strings.TrimSpace(req.Effort)
	}
	if value := strings.TrimSpace(req.Effort); value != "" {
		return value
	}
	return strings.TrimSpace(state.ConfigOverride.Effort)
}

func (s *service) lookupResumeState(ctx context.Context, threadID string) resumeState {
	state := resumeState{}
	thread, err := s.getThread(ctx, threadID)
	if err == nil && thread != nil {
		state.AgentID = strings.TrimSpace(thread.AgentID)
		state.ParentAgentID = strings.TrimSpace(thread.ParentAgentID)
		state.AgentType = strings.TrimSpace(thread.AgentType)
		state.AgentMemoryScope = strings.TrimSpace(thread.AgentMemoryScope)
		state.PublicThreadID = strings.TrimSpace(thread.ThreadID)
		state.Prompt = strings.TrimSpace(thread.Prompt)
		state.Model = strings.TrimSpace(thread.Model)
		state.ConfigOverrideRaw = shared.CloneRawMessage(thread.ConfigOverride)
		state.ConfigOverride = decodeStoredThreadConfig(thread.ConfigOverride)
		state.Effort = strings.TrimSpace(state.ConfigOverride.Effort)
		state.CWD = strings.TrimSpace(thread.Cwd)
		state.CreatedAt = thread.CreatedAt
	}
	binding, err := s.resolveBinding(ctx, threadID)
	if err == nil && binding != nil {
		state.AgentID = shared.FirstNonEmpty(state.AgentID, binding.AgentID)
		state.ParentAgentID = shared.FirstNonEmpty(state.ParentAgentID, strings.TrimSpace(binding.ParentAgentID))
		state.AgentType = shared.FirstNonEmpty(state.AgentType, strings.TrimSpace(binding.AgentType))
		state.AgentMemoryScope = shared.FirstNonEmpty(state.AgentMemoryScope, strings.TrimSpace(binding.AgentMemoryScope))
		state.Provider = strings.TrimSpace(binding.Provider)
		state.ProviderThreadID = shared.FirstNonEmpty(state.ProviderThreadID, binding.ProviderThreadID)
		state.PublicThreadID = shared.FirstNonEmpty(state.PublicThreadID, binding.CodexThreadID)
		state.RolloutPath = strings.TrimSpace(binding.RolloutPath)
		state.SessionUUID = strings.TrimSpace(binding.SessionUUID)
		state.CodexHome = strings.TrimSpace(binding.CodexHome)
		state.CodexInstanceKey = strings.TrimSpace(binding.CodexInstanceKey)
		state.CodexModelProvider = strings.TrimSpace(binding.CodexModelProvider)
		state.CWD = shared.FirstNonEmpty(state.CWD, binding.Cwd)
		// SessionUUID is updated asynchronously by onAgentLaunched when the
		// real provider UUID arrives (e.g. claude system:init).  If it
		// differs from ProviderThreadID the latter is stale — prefer
		// SessionUUID so resume uses the correct provider session.
		// However, SessionUUID itself can be an agent_id placeholder
		// (e.g. "agent_17754...") that is NOT a valid provider UUID.
		// Only override when SessionUUID looks like a real UUID.
		if state.SessionUUID != "" &&
			state.SessionUUID != state.ProviderThreadID &&
			looksLikeUUID(state.SessionUUID) {
			state.ProviderThreadID = state.SessionUUID
		}
	}
	state.StoredCWD = state.CWD
	return state
}
