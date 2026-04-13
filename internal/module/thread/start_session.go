package thread

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/shared"
)

const defaultStartProvider = "codex"

func normalizeStartRequest(req StartRequest) (StartRequest, string, error) {
	req = trimStartRequest(req)
	req.Prompt = shared.FirstNonEmpty(req.Prompt, req.BaseInstructions)
	if req.AgentID == "" {
		req.AgentID = shared.NewID("agent")
	}
	req, err := resolveStartConfig(req)
	if err != nil {
		return StartRequest{}, "", err
	}
	return req, req.AgentID, nil
}

func trimStartRequest(req StartRequest) StartRequest {
	req.Provider = strings.TrimSpace(req.Provider)
	req.AgentID = strings.TrimSpace(req.AgentID)
	req.CWD = strings.TrimSpace(req.CWD)
	req.Model = strings.TrimSpace(req.Model)
	req.ModelProvider = strings.TrimSpace(req.ModelProvider)
	req.Prompt = strings.TrimSpace(req.Prompt)
	req.BaseInstructions = strings.TrimSpace(req.BaseInstructions)
	req.DeveloperInstructions = strings.TrimSpace(req.DeveloperInstructions)
	req.ApprovalPolicy = strings.TrimSpace(req.ApprovalPolicy)
	req.Sandbox = trimRawJSON(req.Sandbox)
	req.Summary = strings.TrimSpace(req.Summary)
	req.Effort = strings.TrimSpace(req.Effort)
	req.Personality = strings.TrimSpace(req.Personality)
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
	switch strings.ToLower(strings.TrimSpace(shared.FirstNonEmpty(provider, defaultStartProvider))) {
	case "codex", "claude":
		return strings.ToLower(strings.TrimSpace(shared.FirstNonEmpty(provider, defaultStartProvider))), nil
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

func (s *service) startSession(ctx context.Context, req StartRequest, agentID string) (contract.Session, error) {
	if s.starter == nil {
		return nil, errors.New("session starter is not configured")
	}
	cwd := strings.TrimSpace(req.CWD)
	if cwd == "" || cwd == "." {
		if abs, err := os.Getwd(); err == nil {
			cwd = abs
		}
	}
	return s.starter.StartSession(ctx, dto.StartSessionRequest{
		Provider:     req.Provider,
		AgentID:      agentID,
		CWD:          cwd,
		Model:        req.Model,
		Instructions: shared.FirstNonEmpty(req.BaseInstructions, req.Prompt),
		Config:       buildStartSessionConfig(req),
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
	return s.starter.ResumeSession(ctx, dto.ResumeSessionRequest{
		Provider:         resolvedReq.Provider,
		AgentID:          resolvedReq.AgentID,
		ThreadID:         resolvedReq.ThreadID,
		ProviderThreadID: resolvedReq.ProviderThreadID,
		Path:             resolvedReq.Path,
		CWD:              cwd,
		Model:            resolvedReq.Model,
		Effort:           resolvedReq.Effort,
		ConfigOverride:   resolvedReq.ConfigOverride,
	})
}

func (s *service) lookupSession(agentID string) (contract.Session, error) {
	if s.sessions == nil {
		return nil, errors.New("session provider is not configured")
	}
	return s.sessions.GetSession(strings.TrimSpace(agentID))
}

type resumeState struct {
	AgentID          string
	Provider         string
	ProviderThreadID string
	PublicThreadID   string
	Prompt           string
	Model            string
	Effort           string
	ConfigOverride   storedThreadConfig
	CWD              string
	RolloutPath      string
	SessionUUID      string
	CreatedAt        int64
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
		state.PublicThreadID = strings.TrimSpace(thread.ThreadID)
		state.Prompt = strings.TrimSpace(thread.Prompt)
		state.Model = strings.TrimSpace(thread.Model)
		state.ConfigOverride = decodeStoredThreadConfig(thread.ConfigOverride)
		state.Effort = strings.TrimSpace(state.ConfigOverride.Effort)
		state.CWD = strings.TrimSpace(thread.Cwd)
		state.CreatedAt = thread.CreatedAt
	}
	binding, err := s.resolveBinding(ctx, threadID)
	if err == nil && binding != nil {
		state.AgentID = shared.FirstNonEmpty(state.AgentID, binding.AgentID)
		state.Provider = strings.TrimSpace(binding.Provider)
		state.ProviderThreadID = shared.FirstNonEmpty(state.ProviderThreadID, binding.ProviderThreadID)
		state.PublicThreadID = shared.FirstNonEmpty(state.PublicThreadID, binding.CodexThreadID)
		state.RolloutPath = strings.TrimSpace(binding.RolloutPath)
		state.SessionUUID = strings.TrimSpace(binding.SessionUUID)
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
	return state
}

func buildStartSessionConfig(req StartRequest) map[string]any {
	cfg := map[string]any{}
	putConfigString(cfg, "approvalPolicy", req.ApprovalPolicy)
	putConfigString(cfg, "approval_policy", req.ApprovalPolicy)
	putConfigString(cfg, "approvals", req.ApprovalPolicy)
	putConfigString(cfg, "modelProvider", req.ModelProvider)
	putConfigString(cfg, "developerInstructions", req.DeveloperInstructions)
	putConfigString(cfg, "developer_instructions", req.DeveloperInstructions)
	putConfigString(cfg, "summary", req.Summary)
	putConfigString(cfg, "effort", req.Effort)
	putConfigString(cfg, "personality", req.Personality)
	putConfigJSON(cfg, "sandbox", req.Sandbox)
	for key, value := range req.Config {
		if _, exists := cfg[key]; !exists {
			cfg[key] = value
		}
	}
	if len(cfg) == 0 {
		return nil
	}
	return cfg
}

func putConfigString(cfg map[string]any, key, value string) {
	if value = strings.TrimSpace(value); value != "" {
		cfg[key] = value
	}
}

func putConfigJSON(cfg map[string]any, key string, raw json.RawMessage) {
	raw = trimRawJSON(raw)
	if len(raw) == 0 {
		return
	}
	var value any
	if err := json.Unmarshal(raw, &value); err == nil {
		cfg[key] = value
	}
}

func trimRawJSON(raw json.RawMessage) json.RawMessage {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 {
		return nil
	}
	return raw
}

// looksLikeUUID returns true when s resembles a UUID (hex-and-dashes, 32+ hex chars).
// It rejects agent_id placeholders like "agent_17754..." that are not valid provider UUIDs.
func looksLikeUUID(s string) bool {
	s = strings.TrimSpace(s)
	if len(s) < 32 {
		return false
	}
	hex := 0
	for _, c := range s {
		switch {
		case (c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F'):
			hex++
		case c == '-':
			// ok
		default:
			return false
		}
	}
	return hex >= 32
}
