package thread

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
	shareddto "github.com/anthropic-ai/super-agent-v3/internal/dto/shared"
)

const defaultStartProvider = "codex"

func normalizeStartRequest(req StartRequest) (StartRequest, string, error) {
	req.Provider = firstNonEmpty(req.Provider, defaultStartProvider)
	req.AgentID = strings.TrimSpace(req.AgentID)
	req.CWD = strings.TrimSpace(req.CWD)
	req.Model = strings.TrimSpace(req.Model)
	req.ModelProvider = strings.TrimSpace(req.ModelProvider)
	req.Prompt = strings.TrimSpace(req.Prompt)
	req.BaseInstructions = strings.TrimSpace(req.BaseInstructions)
	req.Prompt = firstNonEmpty(req.Prompt, req.BaseInstructions)
	req.DeveloperInstructions = strings.TrimSpace(req.DeveloperInstructions)
	req.ApprovalPolicy = strings.TrimSpace(req.ApprovalPolicy)
	req.Sandbox = trimRawJSON(req.Sandbox)
	req.Summary = strings.TrimSpace(req.Summary)
	req.Effort = strings.TrimSpace(req.Effort)
	req.Personality = strings.TrimSpace(req.Personality)
	if req.AgentID == "" {
		req.AgentID = shareddto.NewID("agent")
	}
	return req, req.AgentID, nil
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
		Instructions: firstNonEmpty(req.BaseInstructions, req.Prompt),
		Config:       startSessionConfig(req),
	})
}

func (s *service) resumeSession(ctx context.Context, req ResumeRequest) (contract.Session, error) {
	if s.starter == nil {
		return nil, errors.New("session starter is not configured")
	}
	return s.starter.ResumeSession(ctx, dto.ResumeSessionRequest{
		Provider: req.Provider,
		AgentID:  req.AgentID,
		ThreadID: req.ThreadID,
		Model:    req.Model,
	})
}

func (s *service) lookupSession(agentID string) (contract.Session, error) {
	if s.sessions == nil {
		return nil, errors.New("session provider is not configured")
	}
	return s.sessions.GetSession(strings.TrimSpace(agentID))
}

type resumeState struct {
	AgentID   string
	Provider  string
	Prompt    string
	Model     string
	CWD       string
	CreatedAt int64
}

func (s *service) resolveResumeRequest(ctx context.Context, req ResumeRequest) (ResumeRequest, resumeState, error) {
	req, err := trimResumeRequest(req)
	if err != nil {
		return ResumeRequest{}, resumeState{}, err
	}
	state := s.lookupResumeState(ctx, req.ThreadID)
	req.AgentID = firstNonEmpty(req.AgentID, state.AgentID)
	req.Provider = firstNonEmpty(req.Provider, state.Provider)
	req.CWD = firstNonEmpty(req.CWD, req.Path, state.CWD)
	req.Model = firstNonEmpty(req.Model, state.Model)
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
		state.Prompt = strings.TrimSpace(thread.Prompt)
		state.Model = strings.TrimSpace(thread.Model)
		state.CWD = strings.TrimSpace(thread.Cwd)
		state.CreatedAt = thread.CreatedAt
	}
	binding, err := s.resolveBinding(ctx, threadID)
	if err == nil && binding != nil {
		state.AgentID = firstNonEmpty(state.AgentID, binding.AgentID)
		state.Provider = strings.TrimSpace(binding.Provider)
		state.CWD = firstNonEmpty(state.CWD, binding.Cwd)
	}
	return state
}

func startSessionConfig(req StartRequest) map[string]any {
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
