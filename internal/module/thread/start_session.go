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
	return s.starter.StartSession(ctx, dto.StartSessionRequest{
		Provider:     req.Provider,
		AgentID:      agentID,
		CWD:          req.CWD,
		Model:        req.Model,
		Instructions: shared.FirstNonEmpty(req.BaseInstructions, req.Prompt),
		Config:       buildStartSessionConfig(req),
	})
}

func (s *service) resumeSession(ctx context.Context, req ResumeRequest) (contract.Session, error) {
	if s.starter == nil {
		return nil, errors.New("session starter is not configured")
	}
	return s.starter.ResumeSession(ctx, dto.ResumeSessionRequest{
		Provider:         req.Provider,
		AgentID:          req.AgentID,
		ThreadID:         req.ThreadID,
		ProviderThreadID: req.ProviderThreadID,
		Path:             req.Path,
		CWD:              req.CWD,
		Model:            req.Model,
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
	CWD              string
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
	req.Model = shared.FirstNonEmpty(req.Model, state.Model)
	req.ThreadID = state.PublicThreadID
	if req.Provider == "" {
		return ResumeRequest{}, resumeState{}, errors.New("provider is required")
	}
	if req.AgentID == "" {
		return ResumeRequest{}, resumeState{}, errors.New("agent id is required")
	}
	state.CWD = req.CWD
	state.Model = req.Model
	return req, state, nil
}

func trimResumeRequest(req ResumeRequest) (ResumeRequest, error) {
	req.Provider = strings.TrimSpace(req.Provider)
	req.AgentID = strings.TrimSpace(req.AgentID)
	req.ThreadID = strings.TrimSpace(req.ThreadID)
	req.Path = strings.TrimSpace(req.Path)
	req.CWD = strings.TrimSpace(req.CWD)
	req.Model = strings.TrimSpace(req.Model)
	if req.ThreadID == "" {
		return ResumeRequest{}, errors.New("thread id is required")
	}
	return req, nil
}

func (s *service) lookupResumeState(ctx context.Context, threadID string) resumeState {
	state := resumeState{}
	thread, err := s.getThread(ctx, threadID)
	if err == nil && thread != nil {
		state.AgentID = strings.TrimSpace(thread.AgentID)
		state.PublicThreadID = strings.TrimSpace(thread.ThreadID)
		state.Prompt = strings.TrimSpace(thread.Prompt)
		state.Model = strings.TrimSpace(thread.Model)
		state.CWD = strings.TrimSpace(thread.Cwd)
		state.CreatedAt = thread.CreatedAt
	}
	binding, err := s.resolveBinding(ctx, threadID)
	if err == nil && binding != nil {
		state.AgentID = shared.FirstNonEmpty(state.AgentID, binding.AgentID)
		state.Provider = strings.TrimSpace(binding.Provider)
		state.ProviderThreadID = shared.FirstNonEmpty(state.ProviderThreadID, binding.ProviderThreadID)
		state.PublicThreadID = shared.FirstNonEmpty(state.PublicThreadID, binding.CodexThreadID)
		state.CWD = shared.FirstNonEmpty(state.CWD, binding.Cwd)
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
