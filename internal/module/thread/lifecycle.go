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
	providershared "github.com/anthropic-ai/super-agent-v3/internal/provider/shared"
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
	PublicThreadID     string
	ProviderThreadID   string
	OwnerThreadID      string
	AgentID            string
	ParentAgentID      string
	AgentType          string
	AgentMemoryScope   string
	Provider           string
	CWD                string
	Model              string
	Name               string
	Prompt             string
	RolloutPath        string
	SessionUUID        string
	ConfigOverride     json.RawMessage
	CodexHome          string
	CodexInstanceKey   string
	CodexModelProvider string
	CreatedAt          int64
	AgentKey           string
	PromptVersionID    *int64
	PendingLaunch      bool
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

func (s *service) prepareStartRequest(ctx context.Context, req StartRequest) (StartRequest, string, error) {
	callerProvidedID := strings.TrimSpace(req.AgentID) != ""
	req, agentID, err := normalizeStartRequest(req)
	if err != nil {
		return req, "", err
	}
	req = s.injectParentCodexIdentityForStart(ctx, req)
	req = s.injectDefaultCodexIdentityForStart(req)
	// Collision-safe agent ID: only apply when the caller didn't provide
	// an explicit ID. For child agents derive a sequential suffix from
	// the parent; for root agents verify uniqueness and retry.
	if !callerProvidedID {
		agentID, err = s.resolveUniqueAgentID(ctx, req, agentID)
		if err != nil {
			return req, "", err
		}
		req.AgentID = agentID
	}
	if err := s.prepareTaskHandoffStart(ctx, &req); err != nil {
		return req, "", err
	}
	return req, agentID, nil
}

func (s *service) completeStart(ctx context.Context, req StartRequest, agentID string) (StartResult, error) {
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
	defer runScratchpadCleanup(&cleanupOnFailure, cleanupScratchpad)
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

func (s *service) Start(ctx context.Context, req StartRequest) (StartResult, error) {
	ctx = shared.NonNilContext(ctx)
	req, agentID, err := s.prepareStartRequest(ctx, req)
	if err != nil {
		return StartResult{}, err
	}
	// C1 — when the caller has nothing to classify yet (empty composer) we
	// defer the provider-CLI fork to the first turn. startPendingThread writes
	// a placeholder agent_threads row with pending_launch=true and returns so
	// the UI can show the card immediately; the real spawn happens in
	// SpawnIfNeeded once turn/start arrives with real user input.
	if isPendingLaunchIntent(req) {
		return s.startPendingThread(ctx, req, agentID)
	}
	return s.completeStart(ctx, req, agentID)
}

// resolveUniqueAgentID ensures the agent ID won't collide with an existing
// thread_id in the database. For child agents (ParentAgentID is set) it
// generates a sequential suffix: {parentID}-1, {parentID}-2, …
// For root agents it verifies uniqueness and regenerates on collision.
func (s *service) resolveUniqueAgentID(ctx context.Context, req StartRequest, candidate string) (string, error) {
	parentID := strings.TrimSpace(req.ParentAgentID)
	if parentID != "" {
		return s.nextChildAgentID(ctx, parentID)
	}
	return s.ensureUniqueRootAgentID(ctx, candidate)
}

// nextChildAgentID generates a child agent ID: {parentID}-{N+1} where N is
// the current count of children for that parent. On collision (unlikely but
// possible under concurrent sub-agent creation) it increments the counter.
func (s *service) nextChildAgentID(ctx context.Context, parentID string) (string, error) {
	if s.threadStore == nil {
		return shared.NewChildAgentID(parentID, 1), nil
	}
	count, err := s.threadStore.CountChildren(ctx, parentID)
	if err != nil {
		// DB error → fall back to timestamp-based to avoid blocking start.
		return shared.NewAgentID(), nil
	}
	const maxRetries = 5
	for i := 0; i < maxRetries; i++ {
		candidate := shared.NewChildAgentID(parentID, int(count)+1+i)
		exists, err := s.threadStore.Exists(ctx, candidate)
		if err != nil {
			return candidate, nil // DB read error → use as-is
		}
		if !exists {
			return candidate, nil
		}
	}
	// Exhausted retries — extremely unlikely. Fall back to timestamp.
	return shared.NewAgentID(), nil
}

// ensureUniqueRootAgentID checks the database for a collision and regenerates
// the ID if the timestamp-only ID already exists.
func (s *service) ensureUniqueRootAgentID(ctx context.Context, candidate string) (string, error) {
	if s.threadStore == nil {
		return candidate, nil
	}
	const maxRetries = 3
	for i := 0; i < maxRetries; i++ {
		exists, err := s.threadStore.Exists(ctx, candidate)
		if err != nil {
			return candidate, nil // DB error → use as-is
		}
		if !exists {
			return candidate, nil
		}
		// Collision: regenerate with a new timestamp.
		candidate = shared.NewAgentID()
	}
	return candidate, nil
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
	identity, err := resolveStartCodexIdentity(req.Config)
	if err != nil {
		s.stopAgent(ctx, agentID)
		return StartResult{}, err
	}
	codexHome := shared.FirstNonEmpty(identity.Home, sessionRuntimeConfigString(session, "codexHome"))
	s.logStartedSessionCodexIdentity(req, agentID, codexHome, identity, session)
	state := newThreadState(threadStateStartKind, threadStateFields{
		AgentID:            agentID,
		ParentAgentID:      req.ParentAgentID,
		AgentType:          req.AgentType,
		AgentMemoryScope:   req.AgentMemoryScope,
		ProviderThreadID:   session.ThreadID(),
		Provider:           req.Provider,
		CWD:                effectiveCWD,
		Model:              effectiveModel,
		Name:               displayName,
		Prompt:             displayName,
		RolloutPath:        session.RolloutPath(),
		SessionUUID:        session.ThreadID(),
		ConfigOverride:     configOverride,
		CodexHome:          codexHome,
		CodexInstanceKey:   identity.InstanceKey,
		CodexModelProvider: identity.ModelProvider,
		CreatedAt:          time.Now().Unix(),
		AgentKey:           req.AgentKey,
		PromptVersionID:    req.PromptVersionID,
		OwnerThreadID:      req.OwnerThreadID,
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
	s.logIgnoredTaskHandoffError("refresh task handoff on start", publicThreadID, s.refreshTaskHandoffFromThread(ctx, publicThreadID, taskHandoffRenderSeed{
		SourceThreadID: publicThreadID,
		Status:         "running",
	}))
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
		AgentTitle:      req.AgentTitle,
		PromptKey:       req.PromptKey,
		PromptVersionID: req.PromptVersionID,
		TaskID:          firstConfigString(req.Config, taskConfigKeyID, taskConfigKeyIDSnake),
		HandoffFile:     firstConfigString(req.Config, taskConfigKeyHandoffFile, taskConfigKeyHandoffFileSnake),
	}, nil
}

func resolveStartCodexIdentity(config map[string]any) (providershared.CodexIdentity, error) {
	if !startConfigHasCodexIdentity(config) {
		return providershared.CodexIdentity{}, nil
	}
	return providershared.ResolveCodexIdentity(config)
}

func startConfigHasCodexIdentity(config map[string]any) bool {
	if len(config) == 0 {
		return false
	}
	for _, key := range []string{"codexHome", "codexInstanceKey", "codexModelProvider"} {
		if _, ok := config[key]; ok {
			return true
		}
	}
	return false
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
	codexHome := shared.FirstNonEmpty(state.CodexHome, sessionRuntimeConfigString(session, "codexHome"))
	if s.logger != nil {
		s.logger.Warn("thread: persist resumed session codex identity",
			"agent_id", req.AgentID,
			"provider", req.Provider,
			"codex_home", codexHome,
			"session_runtime_codex_home", sessionRuntimeConfigString(session, "codexHome"),
			"rollout_path", shared.FirstNonEmpty(state.RolloutPath, session.RolloutPath()))
	}
	threadState := newThreadState(threadStateResumeKind, threadStateFields{
		RequestedThreadID:  req.ThreadID,
		PublicThreadID:     state.PublicThreadID,
		ProviderThreadID:   shared.FirstNonEmpty(req.ProviderThreadID, session.ThreadID()),
		AgentID:            req.AgentID,
		ParentAgentID:      state.ParentAgentID,
		AgentType:          state.AgentType,
		AgentMemoryScope:   state.AgentMemoryScope,
		Provider:           req.Provider,
		CWD:                req.CWD,
		Model:              model,
		Name:               displayName,
		Prompt:             displayName,
		RolloutPath:        shared.FirstNonEmpty(state.RolloutPath, session.RolloutPath()),
		SessionUUID:        shared.FirstNonEmpty(state.SessionUUID, session.ThreadID()),
		ConfigOverride:     shared.CloneRawMessage(state.ConfigOverrideRaw),
		CodexHome:          codexHome,
		CodexInstanceKey:   state.CodexInstanceKey,
		CodexModelProvider: state.CodexModelProvider,
		CreatedAt:          state.CreatedAt,
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
