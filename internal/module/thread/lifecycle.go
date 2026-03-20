package thread

import (
	"context"
	"errors"
	"os"
	"strings"
	"time"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
	shareddto "github.com/anthropic-ai/super-agent-v3/internal/dto/shared"
	bindingstore "github.com/anthropic-ai/super-agent-v3/internal/store/binding"
	threadstore "github.com/anthropic-ai/super-agent-v3/internal/store/thread"
)

type SessionStarter interface {
	StartSession(ctx context.Context, req dto.StartSessionRequest) (contract.Session, error)
	ResumeSession(ctx context.Context, req dto.ResumeSessionRequest) (contract.Session, error)
}

type OrchestrationFacade interface {
	LaunchAgent(ctx context.Context, req LaunchAgentRequest) error
	StopAgent(ctx context.Context, agentID string) error
	Recover(ctx context.Context, agentID string) error
}
type threadState struct {
	ThreadID      string
	OwnerThreadID string
	AgentID       string
	Provider      string
	CWD           string
	Model         string
	Prompt        string
	CreatedAt     int64
}
type threadMeta struct {
	Prompt    string
	Model     string
	CWD       string
	CreatedAt int64
}

func (s *service) Start(ctx context.Context, req StartRequest) (StartResult, error) {
	ctx = normalizeThreadContext(ctx)
	req, agentID, err := normalizeStartRequest(req)
	if err != nil {
		return StartResult{}, err
	}
	if err := s.launchAgent(ctx, agentID, req.CWD, req.Prompt); err != nil {
		return StartResult{}, err
	}
	if _, err := s.startSession(ctx, req, agentID); err != nil {
		s.stopAgent(ctx, agentID)
		return StartResult{}, err
	}
	session, err := s.lookupSession(agentID)
	if err != nil {
		s.stopAgent(ctx, agentID)
		return StartResult{}, err
	}
	threadID := resolveStartedThreadID(session.ThreadID(), agentID)
	if err := s.persistThreadState(ctx, threadState{
		ThreadID:  threadID,
		AgentID:   agentID,
		Provider:  req.Provider,
		CWD:       req.CWD,
		Model:     req.Model,
		Prompt:    req.Prompt,
		CreatedAt: time.Now().Unix(),
	}, true); err != nil {
		s.stopAgent(ctx, agentID)
		return StartResult{}, err
	}
	return StartResult{ThreadID: threadID, AgentID: agentID}, nil
}
func (s *service) Resume(ctx context.Context, req ResumeRequest) error {
	ctx = normalizeThreadContext(ctx)
	req, err := normalizeResumeRequest(req)
	if err != nil {
		return err
	}
	meta := s.lookupThreadMeta(ctx, req.ThreadID)
	cwd := firstNonEmpty(meta.CWD, s.lookupBindingCWD(ctx, req.AgentID))
	if err := s.launchAgent(ctx, req.AgentID, cwd, meta.Prompt); err != nil {
		return err
	}
	if _, err := s.resumeSession(ctx, req); err != nil {
		s.stopAgent(ctx, req.AgentID)
		return err
	}
	session, err := s.lookupSession(req.AgentID)
	if err != nil {
		s.stopAgent(ctx, req.AgentID)
		return err
	}
	threadID := resolveStartedThreadID(session.ThreadID(), req.ThreadID)
	return s.persistThreadState(ctx, threadState{
		ThreadID:  threadID,
		AgentID:   req.AgentID,
		Provider:  req.Provider,
		CWD:       cwd,
		Model:     meta.Model,
		Prompt:    meta.Prompt,
		CreatedAt: firstNonZero(meta.CreatedAt, time.Now().Unix()),
	}, true)
}
func (s *service) Fork(ctx context.Context, threadID string) (ForkResult, error) {
	session, binding, err := s.resolveSession(ctx, threadID)
	if err != nil {
		return ForkResult{}, err
	}
	result, err := session.ForkThread(ctx, dto.ForkRequest{ThreadID: historyTargetID(binding, threadID)})
	if err != nil {
		return ForkResult{}, err
	}
	meta := s.lookupThreadMeta(ctx, threadID)
	newThreadID := strings.TrimSpace(result.NewThreadID)
	if newThreadID == "" {
		return ForkResult{}, errors.New("fork thread id is required")
	}
	if err := s.persistThreadState(ctx, threadState{
		ThreadID:      newThreadID,
		OwnerThreadID: historyTargetID(binding, threadID),
		AgentID:       strings.TrimSpace(binding.AgentID),
		Provider:      strings.TrimSpace(binding.Provider),
		CWD:           firstNonEmpty(meta.CWD, strings.TrimSpace(binding.Cwd)),
		Model:         meta.Model,
		Prompt:        meta.Prompt,
		CreatedAt:     time.Now().Unix(),
	}, false); err != nil {
		return ForkResult{}, err
	}
	return ForkResult{NewThreadID: newThreadID}, nil
}

func (s *service) Recover(ctx context.Context, threadID string) error {
	ctx = normalizeThreadContext(ctx)
	binding, err := s.resolveBinding(ctx, threadID)
	if err != nil {
		return err
	}
	meta := s.lookupThreadMeta(ctx, threadID)
	if err := s.recoverAgent(ctx, strings.TrimSpace(binding.AgentID), firstNonEmpty(meta.CWD, strings.TrimSpace(binding.Cwd)), meta.Prompt); err != nil {
		return err
	}
	if _, err := s.lookupSession(strings.TrimSpace(binding.AgentID)); err != nil {
		if _, err := s.resumeSession(ctx, ResumeRequest{
			Provider: strings.TrimSpace(binding.Provider),
			AgentID:  strings.TrimSpace(binding.AgentID),
			ThreadID: historyTargetID(binding, threadID),
		}); err != nil {
			return err
		}
	}
	session, err := s.lookupSession(strings.TrimSpace(binding.AgentID))
	if err != nil {
		return err
	}
	return s.persistThreadState(ctx, threadState{
		ThreadID:  resolveStartedThreadID(session.ThreadID(), historyTargetID(binding, threadID)),
		AgentID:   strings.TrimSpace(binding.AgentID),
		Provider:  strings.TrimSpace(binding.Provider),
		CWD:       firstNonEmpty(meta.CWD, strings.TrimSpace(binding.Cwd)),
		Model:     meta.Model,
		Prompt:    meta.Prompt,
		CreatedAt: firstNonZero(meta.CreatedAt, time.Now().Unix()),
	}, true)
}

func normalizeStartRequest(req StartRequest) (StartRequest, string, error) {
	req.Provider = strings.TrimSpace(req.Provider)
	req.AgentID = strings.TrimSpace(req.AgentID)
	req.CWD = strings.TrimSpace(req.CWD)
	req.Model = strings.TrimSpace(req.Model)
	req.Prompt = strings.TrimSpace(req.Prompt)
	req.ApprovalPolicy = strings.TrimSpace(req.ApprovalPolicy)
	req.Instructions = strings.TrimSpace(req.Instructions)
	req.Effort = strings.TrimSpace(req.Effort)
	req.Personality = strings.TrimSpace(req.Personality)
	if req.Provider == "" {
		return StartRequest{}, "", errors.New("provider is required")
	}
	if req.AgentID == "" {
		req.AgentID = shareddto.NewID("agent")
	}
	return req, req.AgentID, nil
}
func normalizeResumeRequest(req ResumeRequest) (ResumeRequest, error) {
	req.Provider = strings.TrimSpace(req.Provider)
	req.AgentID = strings.TrimSpace(req.AgentID)
	req.ThreadID = strings.TrimSpace(req.ThreadID)
	switch {
	case req.Provider == "":
		return ResumeRequest{}, errors.New("provider is required")
	case req.AgentID == "":
		return ResumeRequest{}, errors.New("agent id is required")
	case req.ThreadID == "":
		return ResumeRequest{}, errors.New("thread id is required")
	default:
		return req, nil
	}
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
		Instructions: firstNonEmpty(req.Instructions, req.Prompt),
		Config: map[string]any{
			"approvalPolicy": req.ApprovalPolicy,
			"effort":         req.Effort,
			"personality":    req.Personality,
		},
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
	})
}
func (s *service) lookupSession(agentID string) (contract.Session, error) {
	if s.sessions == nil {
		return nil, errors.New("session provider is not configured")
	}
	return s.sessions.GetSession(strings.TrimSpace(agentID))
}

func (s *service) persistThreadState(ctx context.Context, state threadState, updateBinding bool) error {
	state.ThreadID = strings.TrimSpace(state.ThreadID)
	state.AgentID = strings.TrimSpace(state.AgentID)
	if state.ThreadID == "" || state.AgentID == "" {
		return errors.New("thread and agent ids are required")
	}
	s.rememberThreadAgent(state.ThreadID, state.AgentID)
	if s.threadStore != nil {
		if err := s.threadStore.Upsert(ctx, threadstore.UpsertParams{
			ThreadID:      state.ThreadID,
			Prompt:        state.Prompt,
			Model:         state.Model,
			Cwd:           state.CWD,
			Status:        statusCreated,
			CreatedAt:     state.CreatedAt,
			UpdatedAt:     time.Now().Unix(),
			OwnerThreadID: state.OwnerThreadID,
		}); err != nil {
			return err
		}
	}
	if !updateBinding || s.bindingStore == nil {
		return nil
	}
	return s.bindingStore.Upsert(ctx, bindingstore.UpsertParams{
		AgentID:          state.AgentID,
		Provider:         state.Provider,
		ProviderThreadID: state.ThreadID,
		CodexThreadID:    state.ThreadID,
		Cwd:              state.CWD,
		CreatedAt:        state.CreatedAt,
		UpdatedAt:        time.Now().Unix(),
	})
}

func (s *service) lookupThreadMeta(ctx context.Context, threadID string) threadMeta {
	thread, err := s.getThread(ctx, threadID)
	if err != nil || thread == nil {
		return threadMeta{}
	}
	return threadMeta{
		Prompt:    strings.TrimSpace(thread.Prompt),
		Model:     strings.TrimSpace(thread.Model),
		CWD:       strings.TrimSpace(thread.Cwd),
		CreatedAt: thread.CreatedAt,
	}
}

func (s *service) lookupBindingCWD(ctx context.Context, agentID string) string {
	if s.bindingStore == nil {
		return ""
	}
	binding, err := s.bindingStore.GetByAgentID(ctx, strings.TrimSpace(agentID))
	if err != nil || binding == nil {
		return ""
	}
	s.rememberBinding(binding)
	return strings.TrimSpace(binding.Cwd)
}

func (s *service) launchAgent(ctx context.Context, agentID, cwd, name string) error {
	if s.orchestration == nil {
		return nil
	}
	req, err := buildLaunchRequest(agentID, cwd, name)
	if err != nil {
		return err
	}
	return s.orchestration.LaunchAgent(ctx, req)
}

func (s *service) stopAgent(ctx context.Context, agentID string) {
	if s.orchestration != nil {
		_ = s.orchestration.StopAgent(ctx, strings.TrimSpace(agentID))
	}
}

func (s *service) recoverAgent(ctx context.Context, agentID, cwd, name string) error {
	if s.orchestration == nil {
		return nil
	}
	agentID = strings.TrimSpace(agentID)
	if err := s.orchestration.Recover(ctx, agentID); err == nil {
		return nil
	}
	return s.launchAgent(ctx, agentID, cwd, name)
}

func buildLaunchRequest(agentID, cwd, name string) (LaunchAgentRequest, error) {
	exe, err := os.Executable()
	if err != nil {
		return LaunchAgentRequest{}, err
	}
	return LaunchAgentRequest{
		AgentID: strings.TrimSpace(agentID),
		Name:    strings.TrimSpace(name),
		Cwd:     strings.TrimSpace(cwd),
		Command: []string{exe},
	}, nil
}
func resolveStartedThreadID(threadID, fallback string) string {
	return firstNonEmpty(threadID, fallback)
}
func normalizeThreadContext(ctx context.Context) context.Context {
	if ctx == nil {
		return context.Background()
	}
	return ctx
}
func firstNonZero(values ...int64) int64 {
	for _, value := range values {
		if value > 0 {
			return value
		}
	}
	return time.Now().Unix()
}
func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	return ""
}
func (s *service) rememberBinding(binding *bindingstore.Binding) {
	if binding == nil {
		return
	}
	agentID := strings.TrimSpace(binding.AgentID)
	for _, threadID := range []string{binding.ProviderThreadID, binding.CodexThreadID, binding.AgentID} {
		s.rememberThreadAgent(threadID, agentID)
	}
}
func (s *service) rememberThreadAgent(threadID, agentID string) {
	threadID = strings.TrimSpace(threadID)
	agentID = strings.TrimSpace(agentID)
	if threadID == "" || agentID == "" {
		return
	}
	s.threadAgentsMu.Lock()
	defer s.threadAgentsMu.Unlock()
	s.threadAgents[threadID] = agentID
}
func (s *service) lookupThreadAgent(threadID string) string {
	threadID = strings.TrimSpace(threadID)
	if threadID == "" {
		return ""
	}
	s.threadAgentsMu.RLock()
	defer s.threadAgentsMu.RUnlock()
	return s.threadAgents[threadID]
}
func (s *service) forgetThreadAgent(threadID string) {
	threadID = strings.TrimSpace(threadID)
	if threadID == "" {
		return
	}
	s.threadAgentsMu.Lock()
	defer s.threadAgentsMu.Unlock()
	delete(s.threadAgents, threadID)
}
