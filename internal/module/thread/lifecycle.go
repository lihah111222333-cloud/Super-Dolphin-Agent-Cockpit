package thread

import (
	"context"
	"errors"
	"os"
	"strings"
	"time"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/shared"
	bindingstore "github.com/anthropic-ai/super-agent-v3/internal/store/binding"
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
	Name             string
	Prompt           string
	RolloutPath      string
	SessionUUID      string
	CreatedAt        int64
}

type threadMeta struct {
	Prompt    string
	Model     string
	CWD       string
	CreatedAt int64
}

func (s *service) Start(ctx context.Context, req StartRequest) (StartResult, error) {
	ctx = shared.NonNilContext(ctx)
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
	effectiveModel, effectiveCWD, _ := enrichFromSessionConfig(session, req.Model, req.CWD)
	state := newThreadState(threadStateStartKind, threadStateFields{
		AgentID:          agentID,
		ProviderThreadID: session.ThreadID(),
		Provider:         req.Provider,
		CWD:              effectiveCWD,
		Model:            effectiveModel,
		Name:             req.Name,
		Prompt:           req.Prompt,
		RolloutPath:      session.RolloutPath(),
		SessionUUID:      session.ThreadID(),
		CreatedAt:        time.Now().Unix(),
	})
	publicThreadID := state.PublicThreadID
	providerThreadID := state.ProviderThreadID
	if err := s.persistThreadState(ctx, state, true); err != nil {
		s.stopAgent(ctx, agentID)
		return StartResult{}, err
	}
	return StartResult{
		ThreadID:       publicThreadID,
		AgentID:        agentID,
		SessionID:      shared.FirstNonEmpty(providerThreadID, publicThreadID),
		Status:         "running",
		Model:          effectiveModel,
		Provider:       req.Provider,
		ModelProvider:  req.ModelProvider,
		CWD:            effectiveCWD,
		ApprovalPolicy: req.ApprovalPolicy,
	}, nil
}

func (s *service) Resume(ctx context.Context, req ResumeRequest) (ResumeResult, error) {
	ctx = shared.NonNilContext(ctx)
	req, state, err := s.resolveResumeRequest(ctx, req)
	if err != nil {
		return ResumeResult{}, err
	}
	req.Provider = shared.FirstNonEmpty(req.Provider, state.Provider)
	req.Model = shared.FirstNonEmpty(req.Model, state.Model)
	req.CWD = shared.FirstNonEmpty(req.CWD, state.CWD, s.lookupBindingCWD(ctx, req.AgentID))
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
	model := shared.FirstNonEmpty(req.Model, state.Model)
	threadState := newThreadState(threadStateResumeKind, threadStateFields{
		RequestedThreadID: req.ThreadID,
		PublicThreadID:    state.PublicThreadID,
		ProviderThreadID:  shared.FirstNonEmpty(session.ThreadID(), req.ProviderThreadID),
		AgentID:           req.AgentID,
		Provider:          req.Provider,
		CWD:               req.CWD,
		Model:             model,
		Prompt:            state.Prompt,
		RolloutPath:       shared.FirstNonEmpty(state.RolloutPath, session.RolloutPath()),
		SessionUUID:       shared.FirstNonEmpty(state.SessionUUID, session.ThreadID()),
		CreatedAt:         state.CreatedAt,
	})
	publicThreadID := threadState.PublicThreadID
	providerThreadID := threadState.ProviderThreadID
	if err := s.persistThreadState(ctx, threadState, true); err != nil {
		// Binding upsert can fail when the DB immutability constraint
		// rejects a changed provider_thread_id (e.g. Claude session UUID
		// rotation). We must NOT suppress thread.Started — without it the
		// UI never receives the correct ProviderThreadID or Model.
		if s.logger != nil {
			s.logger.Warn("thread: resume persist failed, continuing with event emission",
				"error", err,
				"agent_id", req.AgentID,
				"thread_id", publicThreadID,
				"provider_thread_id", providerThreadID,
			)
		}
	}
	s.publishThreadStarted(threadState)
	return ResumeResult{
		ThreadID:  publicThreadID,
		SessionID: shared.FirstNonEmpty(providerThreadID, publicThreadID),
		Status:    "resumed",
		Model:     model,
		CWD:       req.CWD,
	}, nil
}
func (s *service) Fork(ctx context.Context, threadID string) (ForkResult, error) {
	ctx = shared.NonNilContext(ctx)
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
	cwd := shared.FirstNonEmpty(meta.CWD, strings.TrimSpace(binding.Cwd))
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
	providerThreadID := strings.TrimSpace(forkedSession.ThreadID())
	if err := s.persistThreadState(ctx, newThreadState(threadStateForkKind, threadStateFields{
		PublicThreadID:   newThreadID,
		ProviderThreadID: providerThreadID,
		OwnerThreadID:    historyTargetID(binding, threadID),
		AgentID:          agentID,
		Provider:         provider,
		CWD:              cwd,
		Model:            meta.Model,
		Prompt:           meta.Prompt,
		RolloutPath:      forkedSession.RolloutPath(),
		SessionUUID:      forkedSession.ThreadID(),
		CreatedAt:        time.Now().Unix(),
	}), true); err != nil {
		s.stopAgent(ctx, agentID)
		return ForkResult{}, err
	}
	return ForkResult{
		NewThreadID: newThreadID,
		ForkedFrom:  bindingPublicThreadID(binding, threadID),
	}, nil
}

func (s *service) Recover(ctx context.Context, threadID string) (RecoverResult, error) {
	ctx = shared.NonNilContext(ctx)
	binding, err := s.resolveBinding(ctx, threadID)
	if err != nil {
		return RecoverResult{}, err
	}
	meta := s.lookupThreadMeta(ctx, threadID)
	agentID := strings.TrimSpace(binding.AgentID)
	provider := strings.TrimSpace(binding.Provider)
	publicThreadID := bindingPublicThreadID(binding, threadID)
	providerThreadID := historyTargetID(binding, threadID)
	mode := "restore_launch"
	if err := s.recoverAgent(ctx, strings.TrimSpace(binding.AgentID), shared.FirstNonEmpty(meta.CWD, strings.TrimSpace(binding.Cwd)), meta.Prompt); err != nil {
		return RecoverResult{}, err
	}
	if _, err := s.lookupSession(agentID); err != nil {
		mode = "relaunch_resume"
		if _, err := s.resumeSession(ctx, ResumeRequest{
			Provider:         provider,
			AgentID:          agentID,
			ThreadID:         publicThreadID,
			ProviderThreadID: providerThreadID,
		}); err != nil {
			return RecoverResult{}, err
		}
		if err := s.bindSessionGeneration(ctx, binding.AgentID); err != nil {
			s.stopAgent(ctx, binding.AgentID)
			return RecoverResult{}, err
		}
	}
	session, err := s.lookupSession(agentID)
	if err != nil {
		return RecoverResult{}, err
	}
	if err := s.persistThreadState(ctx, newThreadState(threadStateRecoverKind, threadStateFields{
		RequestedThreadID: threadID,
		PublicThreadID:    publicThreadID,
		ProviderThreadID:  shared.FirstNonEmpty(session.ThreadID(), providerThreadID),
		AgentID:           agentID,
		Provider:          provider,
		CWD:               shared.FirstNonEmpty(meta.CWD, strings.TrimSpace(binding.Cwd)),
		Model:             meta.Model,
		Prompt:            meta.Prompt,
		RolloutPath:       shared.FirstNonEmpty(binding.RolloutPath, session.RolloutPath()),
		SessionUUID:       shared.FirstNonEmpty(binding.SessionUUID, session.ThreadID()),
		CreatedAt:         meta.CreatedAt,
	}), true); err != nil {
		return RecoverResult{}, err
	}
	s.publishThreadStarted(threadState{PublicThreadID: publicThreadID, AgentID: agentID, Provider: provider})
	return RecoverResult{
		ThreadID:  publicThreadID,
		Status:    "recovering",
		Recovered: true,
		Mode:      mode,
	}, nil
}

func (s *service) persistThreadState(ctx context.Context, state threadState, updateBinding bool) error {
	state, err := normalizeThreadState(state)
	if err != nil {
		return err
	}
	if err := s.ensurePublicThreadAvailable(ctx, state); err != nil {
		return err
	}
	if state.PublicThreadID == "" || state.AgentID == "" {
		return errors.New("thread and agent ids are required")
	}
	if s.logger != nil {
		s.logger.Warn("thread: persistThreadState binding snapshot",
			"agent_id", state.AgentID,
			"provider", state.Provider,
			"provider_thread_id", state.ProviderThreadID,
			"public_thread_id", state.PublicThreadID,
			"rollout_path", state.RolloutPath,
			"session_uuid", state.SessionUUID,
			"update_binding", updateBinding,
		)
	}
	bindingOutcome, err := s.maybeRegisterThreadBinding(ctx, state, updateBinding)
	if err != nil {
		return err
	}
	return s.persistStartedThread(ctx, state, bindingOutcome)
}

func (s *service) lookupThreadMeta(ctx context.Context, threadID string) threadMeta {
	thread, err := s.getThread(ctx, threadID)
	if err != nil || thread == nil {
		return threadMeta{}
	}
	return threadMeta{Prompt: strings.TrimSpace(thread.Prompt), Model: strings.TrimSpace(thread.Model), CWD: strings.TrimSpace(thread.Cwd), CreatedAt: thread.CreatedAt}
}

func (s *service) lookupBindingCWD(ctx context.Context, agentID string) string {
	if s.bindingStore == nil {
		return ""
	}
	binding, err := s.bindingStore.GetByAgentID(ctx, strings.TrimSpace(agentID))
	if err != nil || binding == nil {
		return ""
	}
	s.rememberBinding(binding); return strings.TrimSpace(binding.Cwd)
}

func (s *service) launchAgent(ctx context.Context, agentID, cwd, name, provider, model string) error {
	if s.orchestration == nil {
		return nil
	}
	req, err := buildLaunchRequest(agentID, cwd, name, provider, model)
	if err != nil {
		return err
	}; return s.orchestration.LaunchAgent(ctx, req)
}

func (s *service) stopAgent(ctx context.Context, agentID string) {
	shared.LogIgnoredError(s.logger, "stop managed agent failed", s.stopManagedAgent(ctx, strings.TrimSpace(agentID), true))
}

func (s *service) recoverAgent(ctx context.Context, agentID, cwd, name string) error {
	if s.orchestration == nil {
		return nil
	}
	agentID = strings.TrimSpace(agentID)
	if err := s.orchestration.Recover(ctx, agentID); err == nil {
		return nil
	}; return s.launchAgent(ctx, agentID, cwd, name, "", "")
}

func buildLaunchRequest(agentID, cwd, name, provider, model string) (LaunchAgentRequest, error) {
	exe, err := os.Executable()
	if err != nil {
		return LaunchAgentRequest{}, err
	}
	return LaunchAgentRequest{AgentID: strings.TrimSpace(agentID), Name: strings.TrimSpace(name), Cwd: strings.TrimSpace(cwd), Command: []string{exe}, Env: launchConfigEnv(provider, model)}, nil
}

func launchConfigEnv(provider, model string) []string {
	var env []string
	if provider = strings.TrimSpace(provider); provider != "" {
		env = append(env, "AGENT_PROVIDER="+provider)
	}
	if model = strings.TrimSpace(model); model != "" {
		env = append(env, "AGENT_MODEL="+model)
	}
	return env
}
func (s *service) rememberBinding(binding *bindingstore.Binding) {
	if binding == nil {
		return
	}
	agentID := strings.TrimSpace(binding.AgentID)
	for _, tid := range []string{binding.ProviderThreadID, binding.CodexThreadID, binding.AgentID} {
		s.rememberThreadAgent(tid, agentID)
	}
}

func (s *service) rememberThreadAgent(threadID, agentID string) {
	threadID, agentID = strings.TrimSpace(threadID), strings.TrimSpace(agentID)
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
	if threadID = strings.TrimSpace(threadID); threadID == "" {
		return ""
	}
	s.threadAgentsMu.RLock()
	defer s.threadAgentsMu.RUnlock()
	return s.threadAgents[threadID]
}
func (s *service) forgetThreadAgent(threadID string) {
	if threadID = strings.TrimSpace(threadID); threadID == "" {
		return
	}
	s.threadAgentsMu.Lock()
	defer s.threadAgentsMu.Unlock()
	delete(s.threadAgents, threadID)
}
