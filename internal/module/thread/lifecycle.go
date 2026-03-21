package thread

import (
	"context"
	"errors"
	"os"
	"strings"
	"time"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
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
	BindSessionGeneration(ctx context.Context, agentID string, generation uint64) error
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
	if err := s.bindSessionGeneration(ctx, agentID); err != nil {
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
func (s *service) Resume(ctx context.Context, req ResumeRequest) (ResumeResult, error) {
	ctx = normalizeThreadContext(ctx)
	req, state, err := s.resolveResumeRequest(ctx, req)
	if err != nil {
		return ResumeResult{}, err
	}
	cwd := firstNonEmpty(req.CWD, s.lookupBindingCWD(ctx, req.AgentID))
	if s.sessions != nil {
		s.sessions.RemoveSession(req.AgentID)
	}
	if err := s.launchAgent(ctx, req.AgentID, cwd, state.Prompt); err != nil {
		return ResumeResult{}, err
	}
	if _, err := s.resumeSession(ctx, req); err != nil {
		s.stopAgent(ctx, req.AgentID)
		return ResumeResult{}, err
	}
	if err := s.bindSessionGeneration(ctx, req.AgentID); err != nil {
		s.stopAgent(ctx, req.AgentID)
		return ResumeResult{}, err
	}
	session, err := s.lookupSession(req.AgentID)
	if err != nil {
		s.stopAgent(ctx, req.AgentID)
		return ResumeResult{}, err
	}
	threadID := resolveStartedThreadID(session.ThreadID(), req.ThreadID)
	model := firstNonEmpty(req.Model, state.Model)
	if err := s.persistThreadState(ctx, threadState{
		ThreadID:  threadID,
		AgentID:   req.AgentID,
		Provider:  req.Provider,
		CWD:       cwd,
		Model:     model,
		Prompt:    state.Prompt,
		CreatedAt: firstNonZero(state.CreatedAt, time.Now().Unix()),
	}, true); err != nil {
		return ResumeResult{}, err
	}
	return ResumeResult{ThreadID: threadID, Status: "resumed", Model: model}, nil
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
		if err := s.bindSessionGeneration(ctx, binding.AgentID); err != nil {
			s.stopAgent(ctx, binding.AgentID)
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
		s.publishThreadStarted(state)
		return nil
	}
	if err := s.bindingStore.Upsert(ctx, bindingstore.UpsertParams{
		AgentID:          state.AgentID,
		Provider:         state.Provider,
		ProviderThreadID: state.ThreadID,
		CodexThreadID:    state.ThreadID,
		Cwd:              state.CWD,
		CreatedAt:        state.CreatedAt,
		UpdatedAt:        time.Now().Unix(),
	}); err != nil {
		return err
	}
	s.publishThreadStarted(state)
	return nil
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
	if s.threadAgents == nil {
		s.threadAgents = make(map[string]string)
	}
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
