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
	PublicThreadID   string
	ProviderThreadID string
	OwnerThreadID    string
	AgentID          string
	Provider         string
	CWD              string
	Model            string
	Prompt           string
	CreatedAt        int64
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
	if err := s.launchAgent(ctx, agentID, req.CWD, req.Prompt, req.Provider, req.Model); err != nil {
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
	publicThreadID := strings.TrimSpace(agentID)
	providerThreadID := resolveProviderThreadID(session.ThreadID(), publicThreadID)
	if err := s.persistThreadState(ctx, threadState{
		PublicThreadID:   publicThreadID,
		ProviderThreadID: providerThreadID,
		AgentID:          agentID,
		Provider:         req.Provider,
		CWD:              req.CWD,
		Model:            req.Model,
		Prompt:           req.Prompt,
		CreatedAt:        time.Now().Unix(),
	}, true); err != nil {
		s.stopAgent(ctx, agentID)
		return StartResult{}, err
	}
	return StartResult{
		ThreadID:  publicThreadID,
		AgentID:   agentID,
		SessionID: firstNonEmpty(providerThreadID, publicThreadID),
		Status:    "running",
	}, nil
}
func (s *service) Resume(ctx context.Context, req ResumeRequest) (ResumeResult, error) {
	ctx = normalizeThreadContext(ctx)
	req, state, err := s.resolveResumeRequest(ctx, req)
	if err != nil {
		return ResumeResult{}, err
	}
	req.Provider = firstNonEmpty(req.Provider, state.Provider)
	req.Model = firstNonEmpty(req.Model, state.Model)
	req.CWD = firstNonEmpty(req.CWD, state.CWD, s.lookupBindingCWD(ctx, req.AgentID))
	if s.sessions != nil {
		s.sessions.RemoveSession(req.AgentID)
	}
	if err := s.launchAgent(ctx, req.AgentID, req.CWD, state.Prompt, req.Provider, req.Model); err != nil {
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
	publicThreadID := firstNonEmpty(state.PublicThreadID, req.ThreadID)
	providerThreadID := resolveProviderThreadID(session.ThreadID(), req.ThreadID)
	model := firstNonEmpty(req.Model, state.Model)
	if err := s.persistThreadState(ctx, threadState{
		PublicThreadID:   publicThreadID,
		ProviderThreadID: providerThreadID,
		AgentID:          req.AgentID,
		Provider:         req.Provider,
		CWD:              req.CWD,
		Model:            model,
		Prompt:           state.Prompt,
		CreatedAt:        firstNonZero(state.CreatedAt, time.Now().Unix()),
	}, true); err != nil {
		s.stopAgent(ctx, req.AgentID)
		return ResumeResult{}, err
	}
	return ResumeResult{
		ThreadID:  publicThreadID,
		SessionID: firstNonEmpty(providerThreadID, publicThreadID),
		Status:    "resumed",
		Model:     model,
	}, nil
}
func (s *service) Fork(ctx context.Context, threadID string) (ForkResult, error) {
	ctx = normalizeThreadContext(ctx)
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
	provider := strings.TrimSpace(binding.Provider)
	if provider == "" {
		return ForkResult{}, errors.New("fork provider is required")
	}
	agentID := newThreadID
	cwd := firstNonEmpty(meta.CWD, strings.TrimSpace(binding.Cwd))
	if err := s.launchAgent(ctx, agentID, cwd, meta.Prompt, provider, meta.Model); err != nil {
		return ForkResult{}, err
	}
	forkedSession, err := s.resumeSession(ctx, ResumeRequest{
		Provider: provider,
		AgentID:  agentID,
		ThreadID: newThreadID,
		CWD:      cwd,
		Model:    meta.Model,
	})
	if err != nil {
		s.stopAgent(ctx, agentID)
		return ForkResult{}, err
	}
	if err := s.bindSessionGeneration(ctx, agentID); err != nil {
		s.stopAgent(ctx, agentID)
		return ForkResult{}, err
	}
	providerThreadID := resolveProviderThreadID(forkedSession.ThreadID(), newThreadID)
	if err := s.persistThreadState(ctx, threadState{
		PublicThreadID:   newThreadID,
		ProviderThreadID: providerThreadID,
		OwnerThreadID:    historyTargetID(binding, threadID),
		AgentID:          agentID,
		Provider:         provider,
		CWD:              cwd,
		Model:            meta.Model,
		Prompt:           meta.Prompt,
		CreatedAt:        time.Now().Unix(),
	}, true); err != nil {
		s.stopAgent(ctx, agentID)
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
		PublicThreadID:   bindingPublicThreadID(binding, threadID),
		ProviderThreadID: resolveProviderThreadID(session.ThreadID(), historyTargetID(binding, threadID)),
		AgentID:          strings.TrimSpace(binding.AgentID),
		Provider:         strings.TrimSpace(binding.Provider),
		CWD:              firstNonEmpty(meta.CWD, strings.TrimSpace(binding.Cwd)),
		Model:            meta.Model,
		Prompt:           meta.Prompt,
		CreatedAt:        firstNonZero(meta.CreatedAt, time.Now().Unix()),
	}, true)
}

func (s *service) persistThreadState(ctx context.Context, state threadState, updateBinding bool) error {
	var err error
	var bindingOutcome bindingWriteOutcome
	if state, err = normalizeThreadState(state); err != nil {
		return err
	}
	if err := s.ensurePublicThreadAvailable(ctx, state); err != nil {
		return err
	}
	if updateBinding && s.bindingStore != nil {
		if bindingOutcome, err = s.registerThreadBinding(ctx, state); err != nil {
			return err
		}
	}
	if state.PublicThreadID == "" || state.AgentID == "" {
		return errors.New("thread and agent ids are required")
	}
	if s.threadStore != nil {
		if err := s.threadStore.Upsert(ctx, threadstore.UpsertParams{
			ThreadID:      state.PublicThreadID,
			Prompt:        state.Prompt,
			Model:         state.Model,
			Cwd:           state.CWD,
			Status:        statusCreated,
			CreatedAt:     state.CreatedAt,
			UpdatedAt:     time.Now().Unix(),
			OwnerThreadID: state.OwnerThreadID,
		}); err != nil {
			if rollbackErr := s.rollbackThreadBinding(ctx, bindingOutcome); rollbackErr != nil {
				return errors.Join(err, rollbackErr)
			}
			return err
		}
	}
	s.rememberThreadAgent(state.PublicThreadID, state.AgentID)
	s.rememberThreadAgent(state.ProviderThreadID, state.AgentID)
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

func (s *service) launchAgent(ctx context.Context, agentID, cwd, name, provider, model string) error {
	if s.orchestration == nil {
		return nil
	}
	req, err := buildLaunchRequest(agentID, cwd, name, provider, model)
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
	return s.launchAgent(ctx, agentID, cwd, name, "", "")
}

func buildLaunchRequest(agentID, cwd, name, provider, model string) (LaunchAgentRequest, error) {
	exe, err := os.Executable()
	if err != nil {
		return LaunchAgentRequest{}, err
	}
	return LaunchAgentRequest{
		AgentID: strings.TrimSpace(agentID),
		Name:    strings.TrimSpace(name),
		Cwd:     strings.TrimSpace(cwd),
		Command: []string{exe},
		Env:     launchConfigEnv(provider, model),
	}, nil
}

func launchConfigEnv(provider, model string) []string {
	env := make([]string, 0, 2)
	if provider = strings.TrimSpace(provider); provider != "" {
		env = append(env, "AGENT_PROVIDER="+provider)
	}
	if model = strings.TrimSpace(model); model != "" {
		env = append(env, "AGENT_MODEL="+model)
	}
	if len(env) == 0 {
		return nil
	}
	return env
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
