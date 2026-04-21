package thread

import (
	"context"
	"encoding/json"
	"errors"
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
	ParentAgentID    string
	AgentType        string
	AgentMemoryScope string
	Provider         string
	CWD              string
	Model            string
	Name             string
	Prompt           string
	RolloutPath      string
	SessionUUID      string
	ConfigOverride   json.RawMessage
	CreatedAt        int64
	AgentKey         string
	PromptVersionID  *int64
}

type threadMeta struct {
	Name             string
	Model            string
	CWD              string
	ParentAgentID    string
	AgentType        string
	AgentMemoryScope string
	ConfigOverride   json.RawMessage
	CreatedAt        int64
}

type sessionGenerationProvider interface {
	SessionGeneration(agentID string) uint64
}

func (s *service) bindSessionGeneration(ctx context.Context, agentID string) error {
	if s.orchestration == nil || s.sessions == nil {
		return nil
	}
	provider, ok := s.sessions.(sessionGenerationProvider)
	if !ok {
		return nil
	}
	generation := provider.SessionGeneration(agentID)
	if generation == 0 {
		return errors.New("session generation is not available")
	}
	return s.orchestration.BindSessionGeneration(ctx, strings.TrimSpace(agentID), generation)
}

func (s *service) Start(ctx context.Context, req StartRequest) (StartResult, error) {
	ctx = shared.NonNilContext(ctx)
	req, agentID, err := normalizeStartRequest(req)
	if err != nil {
		return StartResult{}, err
	}
	// Router resolution runs before prompt assembly so its output (BaseInstructions)
	// is visible to the assembly step, and its sidecar metadata (AgentKey,
	// PromptVersionID) reaches the thread Upsert via threadState.
	s.resolveRoutedPrompt(ctx, &req)
	if req.PromptAssemblyRef == nil {
		req.PromptAssemblyRef = s.promptAssembly
	}
	assemblyInput, cleanupScratchpad, err := s.buildStartAssemblyInput(req, agentID)
	if err != nil {
		return StartResult{}, err
	}
	cleanupOnFailure := true
	defer func() {
		if cleanupOnFailure && cleanupScratchpad != nil {
			cleanupScratchpad()
		}
	}()
	assembly, err := resolveStartPromptAssembly(ctx, req, assemblyInput)
	if err != nil {
		return StartResult{}, err
	}
	displayName := strings.TrimSpace(assembly.DisplayName)
	if err := s.launchAgent(
		ctx,
		agentID,
		req.CWD,
		displayName,
		req.ParentAgentID,
		req.AgentType,
		req.AgentMemoryScope,
		req.Provider,
		req.Model,
	); err != nil {
		return StartResult{}, err
	}
	session, err := s.establishStartedSession(ctx, req, assemblyInput, assembly, agentID)
	if err != nil {
		return StartResult{}, err
	}
	result, err := s.persistStartedSession(ctx, req, assemblyInput, assembly, agentID, displayName, session)
	if err != nil {
		return StartResult{}, err
	}
	cleanupOnFailure = false
	return result, nil
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
	displayName := strings.TrimSpace(state.Prompt)
	session, err := s.establishResumedSession(ctx, req, state, displayName)
	if err != nil {
		return ResumeResult{}, err
	}
	return s.persistResumedSession(ctx, req, state, displayName, session)
}

func (s *service) establishStartedSession(
	ctx context.Context,
	req StartRequest,
	input contract.StartInput,
	assembly contract.StartAssembly,
	agentID string,
) (contract.Session, error) {
	if _, err := s.startSession(ctx, req, input, assembly, agentID); err != nil {
		s.stopAgent(ctx, agentID)
		return nil, err
	}
	if err := s.bindSessionGeneration(ctx, agentID); err != nil {
		s.stopAgent(ctx, agentID)
		return nil, err
	}
	session, err := s.lookupSession(agentID)
	if err != nil {
		s.stopAgent(ctx, agentID)
		return nil, err
	}
	return session, nil
}

func (s *service) persistStartedSession(
	ctx context.Context,
	req StartRequest,
	input contract.StartInput,
	assembly contract.StartAssembly,
	agentID, displayName string,
	session contract.Session,
) (StartResult, error) {
	effectiveModel, effectiveCWD, _ := enrichFromSessionConfig(session, req.Model, req.CWD)
	configOverride, err := encodeStoredThreadConfig(buildStartStoredThreadConfig(req, input, assembly))
	if err != nil {
		s.stopAgent(ctx, agentID)
		return StartResult{}, err
	}
	state := newThreadState(threadStateStartKind, threadStateFields{
		AgentID:          agentID,
		ParentAgentID:    req.ParentAgentID,
		AgentType:        req.AgentType,
		AgentMemoryScope: req.AgentMemoryScope,
		ProviderThreadID: session.ThreadID(),
		Provider:         req.Provider,
		CWD:              effectiveCWD,
		Model:            effectiveModel,
		Name:             displayName,
		Prompt:           displayName,
		RolloutPath:      session.RolloutPath(),
		SessionUUID:      session.ThreadID(),
		ConfigOverride:   configOverride,
		CreatedAt:        time.Now().Unix(),
		AgentKey:         req.AgentKey,
		PromptVersionID:  req.PromptVersionID,
		OwnerThreadID:    req.OwnerThreadID,
	})
	publicThreadID := state.PublicThreadID
	providerThreadID := state.ProviderThreadID
	if err := s.persistThreadState(ctx, state, true); err != nil {
		s.stopAgent(ctx, agentID)
		return StartResult{}, err
	}
	if err := s.savePromptSnapshot(ctx, publicThreadID, assembly); err != nil {
		s.stopAgent(ctx, agentID)
		return StartResult{}, err
	}
	return StartResult{
		ThreadID:        publicThreadID,
		AgentID:         agentID,
		SessionID:       shared.FirstNonEmpty(providerThreadID, publicThreadID),
		Status:          "running",
		Model:           effectiveModel,
		Provider:        req.Provider,
		ModelProvider:   req.ModelProvider,
		CWD:             effectiveCWD,
		ApprovalPolicy:  req.ApprovalPolicy,
		AgentKey:        req.AgentKey,
		PromptVersionID: req.PromptVersionID,
	}, nil
}

func (s *service) establishResumedSession(
	ctx context.Context,
	req ResumeRequest,
	state resumeState,
	displayName string,
) (contract.Session, error) {
	if s.sessions != nil {
		s.sessions.RemoveSession(req.AgentID)
	}
	if err := s.launchAgent(
		ctx,
		req.AgentID,
		req.CWD,
		displayName,
		state.ParentAgentID,
		state.AgentType,
		state.AgentMemoryScope,
		req.Provider,
		req.Model,
	); err != nil {
		return nil, err
	}
	if _, err := s.resumeSession(ctx, req); err != nil {
		s.stopAgent(ctx, req.AgentID)
		return nil, err
	}
	if err := s.bindSessionGeneration(ctx, req.AgentID); err != nil {
		s.stopAgent(ctx, req.AgentID)
		return nil, err
	}
	session, err := s.lookupSession(req.AgentID)
	if err != nil {
		s.stopAgent(ctx, req.AgentID)
		return nil, err
	}
	return session, nil
}

func (s *service) persistResumedSession(
	ctx context.Context,
	req ResumeRequest,
	state resumeState,
	displayName string,
	session contract.Session,
) (ResumeResult, error) {
	model := shared.FirstNonEmpty(req.Model, state.Model)
	threadState := newThreadState(threadStateResumeKind, threadStateFields{
		RequestedThreadID: req.ThreadID,
		PublicThreadID:    state.PublicThreadID,
		ProviderThreadID:  shared.FirstNonEmpty(req.ProviderThreadID, session.ThreadID()),
		AgentID:           req.AgentID,
		ParentAgentID:     state.ParentAgentID,
		AgentType:         state.AgentType,
		AgentMemoryScope:  state.AgentMemoryScope,
		Provider:          req.Provider,
		CWD:               req.CWD,
		Model:             model,
		Name:              displayName,
		Prompt:            displayName,
		RolloutPath:       shared.FirstNonEmpty(state.RolloutPath, session.RolloutPath()),
		SessionUUID:       shared.FirstNonEmpty(state.SessionUUID, session.ThreadID()),
		ConfigOverride:    shared.CloneRawMessage(state.ConfigOverrideRaw),
		CreatedAt:         state.CreatedAt,
	})
	publicThreadID := threadState.PublicThreadID
	providerThreadID := threadState.ProviderThreadID
	if err := s.persistThreadState(ctx, threadState, true); err != nil {
		s.logResumePersistFailure(req.AgentID, publicThreadID, providerThreadID, err)
		s.publishThreadStarted(threadState)
	}
	if promptResumeRestoreRequiresInvalidation(state.StoredCWD, req.CWD, s.cfg) {
		if err := s.invalidatePromptAssembly(ctx, contract.InvalidateResumeRestore); err != nil {
			return ResumeResult{}, err
		}
	}
	return ResumeResult{
		ThreadID:  publicThreadID,
		SessionID: shared.FirstNonEmpty(providerThreadID, publicThreadID),
		Status:    "resumed",
		Model:     model,
		CWD:       req.CWD,
	}, nil
}

func (s *service) logResumePersistFailure(agentID, threadID, providerThreadID string, err error) {
	if s == nil || s.logger == nil {
		return
	}
	s.logger.Warn("thread: resume persist failed, continuing with event emission",
		"error", err,
		"agent_id", agentID,
		"thread_id", threadID,
		"provider_thread_id", providerThreadID,
	)
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
			"parent_agent_id", state.ParentAgentID,
			"agent_type", state.AgentType,
			"agent_memory_scope", state.AgentMemoryScope,
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
	return threadMeta{
		Name:             strings.TrimSpace(thread.Prompt),
		Model:            strings.TrimSpace(thread.Model),
		CWD:              strings.TrimSpace(thread.Cwd),
		ParentAgentID:    strings.TrimSpace(thread.ParentAgentID),
		AgentType:        strings.TrimSpace(thread.AgentType),
		AgentMemoryScope: strings.TrimSpace(thread.AgentMemoryScope),
		ConfigOverride:   shared.CloneRawMessage(thread.ConfigOverride),
		CreatedAt:        thread.CreatedAt,
	}
}

func (s *service) stopAgent(ctx context.Context, agentID string) {
	shared.LogIgnoredError(s.logger, "stop managed agent failed", s.stopManagedAgent(ctx, strings.TrimSpace(agentID), true))
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
