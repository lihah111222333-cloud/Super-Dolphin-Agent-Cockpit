package thread

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"

	bindingstore "github.com/anthropic-ai/super-agent-v3/internal/store/binding"
	"github.com/anthropic-ai/super-agent-v3/internal/util"
	"github.com/anthropic-ai/super-agent-v3/internal/util/clone"
	"github.com/anthropic-ai/super-agent-v3/internal/util/identifier"
)

// SessionStarter is an alias for contract.SessionStarter.
// Kept as a local type alias for backward compatibility within this package.
type SessionStarter = contract.SessionStarter

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

func (s *service) prepareStartRequest(ctx context.Context, req StartRequest) (StartRequest, string, func(), error) {
	callerProvidedID := strings.TrimSpace(req.AgentID) != ""
	req, agentID, err := normalizeStartRequest(req)
	if err != nil {
		return req, "", nil, err
	}
	req = s.injectParentCodexIdentityForStart(ctx, req)
	req = s.injectDefaultCodexIdentityForStart(req)
	agentID, releaseAgentID, err := s.reserveUniqueStartAgentID(ctx, req, agentID, callerProvidedID)
	if err != nil {
		return req, "", nil, err
	}
	if releaseAgentID == nil {
		return req, "", nil, errors.New("thread: reserve agent_id failed")
	}
	req.AgentID = agentID
	if err := s.prepareTaskHandoffStart(ctx, &req); err != nil {
		releaseAgentID()
		return req, "", nil, err
	}
	return req, agentID, releaseAgentID, nil
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
	displayName := resolveDisplayName(ctx, s.threadStore, agentID, req.Prompt, assembly.DisplayName)
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
	ctx = util.NonNilContext(ctx)
	req, agentID, releaseAgentID, err := s.prepareStartRequest(ctx, req)
	if err != nil {
		return StartResult{}, err
	}
	defer releaseAgentID()
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

func (s *service) Resume(ctx context.Context, req ResumeRequest) (ResumeResult, error) {
	ctx = util.NonNilContext(ctx)
	req, state, err := s.resolveResumeRequest(ctx, req)
	if err != nil {
		return ResumeResult{}, err
	}
	if reason, blocked := s.resumeLifecycleBlockReason(ctx, req.ThreadID, nil); blocked {
		return ResumeResult{}, resumeLifecycleError(req.ThreadID, reason)
	}
	req.Provider = util.FirstNonEmpty(req.Provider, state.Provider)
	req.Model = util.FirstNonEmpty(req.Model, state.Model)
	req.CWD = util.FirstNonEmpty(req.CWD, state.CWD, s.lookupBindingCWD(ctx, req.AgentID))
	displayName := resolveDisplayName(ctx, s.threadStore, req.AgentID, "", state.Prompt)
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
	providerUUID, err := requireStartedProviderUUID(session, req.Provider, agentID)
	if err != nil {
		s.stopAgent(ctx, agentID)
		return StartResult{}, err
	}
	effectiveModel, effectiveCWD, _ := enrichFromSessionConfig(session, req.Model, req.CWD)
	identity, err := resolveStartCodexIdentity(req.Config)
	if err != nil {
		s.stopAgent(ctx, agentID)
		return StartResult{}, err
	}
	codexHome := util.FirstNonEmpty(identity.Home, sessionRuntimeConfigString(session, "codexHome"))
	codexInstanceKey := util.FirstNonEmpty(identity.InstanceKey, sessionRuntimeConfigString(session, "codexInstanceKey"))
	codexModelProvider := util.FirstNonEmpty(identity.ModelProvider, sessionRuntimeConfigString(session, "codexModelProvider"))
	configOverride, err := encodeStoredThreadConfig(buildStartStoredThreadConfig(req, input, assembly, session))
	if err != nil {
		s.stopAgent(ctx, agentID)
		return StartResult{}, err
	}
	s.logStartedSessionCodexIdentity(req, agentID, codexHome, identity, session)
	rolloutPath := session.RolloutPath()
	providerThreadID := recoverableProviderThreadID(req.Provider, providerUUID, agentID, rolloutPath, codexHome)
	state := newThreadState(threadStateStartKind, threadStateFields{
		AgentID:            agentID,
		ParentAgentID:      req.ParentAgentID,
		AgentType:          req.AgentType,
		AgentMemoryScope:   req.AgentMemoryScope,
		ProviderThreadID:   providerThreadID,
		Provider:           req.Provider,
		CWD:                effectiveCWD,
		Model:              effectiveModel,
		Name:               displayName,
		Prompt:             displayName,
		RolloutPath:        rolloutPath,
		SessionUUID:        providerUUID,
		ConfigOverride:     configOverride,
		CodexHome:          codexHome,
		CodexInstanceKey:   codexInstanceKey,
		CodexModelProvider: codexModelProvider,
		CreatedAt:          time.Now().Unix(),
		AgentKey:           req.AgentKey,
		PromptVersionID:    req.PromptVersionID,
		OwnerThreadID:      req.OwnerThreadID,
	})
	publicThreadID := state.PublicThreadID
	providerThreadID = state.ProviderThreadID
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
	return newStartResult(req, publicThreadID, agentID, providerUUID, providerThreadID, effectiveModel, effectiveCWD), nil
}

func resolveStartCodexIdentity(config map[string]any) (contract.CodexIdentity, error) {
	if !startConfigHasCodexIdentity(config) {
		return contract.CodexIdentity{}, nil
	}
	return contract.ResolveCodexIdentity(config)
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
	if reason, blocked := s.resumeLifecycleBlockReason(ctx, req.ThreadID, nil); blocked {
		return nil, resumeLifecycleError(req.ThreadID, reason)
	}
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
	model := util.FirstNonEmpty(req.Model, state.Model)
	codexHome := util.FirstNonEmpty(req.CodexHome, state.CodexHome, sessionRuntimeConfigString(session, "codexHome"))
	codexInstanceKey := util.FirstNonEmpty(req.CodexInstanceKey, state.CodexInstanceKey)
	codexModelProvider := util.FirstNonEmpty(req.CodexModelProvider, state.CodexModelProvider)
	if s.logger != nil {
		s.logger.Warn("thread: persist resumed session codex identity",
			"agent_id", req.AgentID,
			"provider", req.Provider,
			"codex_home", codexHome,
			"codex_instance_key", codexInstanceKey,
			"codex_model_provider", codexModelProvider,
			"session_runtime_codex_home", sessionRuntimeConfigString(session, "codexHome"),
			"rollout_path", util.FirstNonEmpty(state.RolloutPath, session.RolloutPath()))
	}
	rolloutPath := util.FirstNonEmpty(state.RolloutPath, session.RolloutPath())
	sessionUUID := util.FirstNonEmpty(resolvedProviderUUID(session), state.SessionUUID, req.ProviderThreadID, state.ProviderThreadID)
	providerThreadID := recoverableProviderThreadID(req.Provider, sessionUUID, state.PublicThreadID, rolloutPath, codexHome)
	threadState := newThreadState(threadStateResumeKind, threadStateFields{
		RequestedThreadID:  req.ThreadID,
		PublicThreadID:     state.PublicThreadID,
		ProviderThreadID:   providerThreadID,
		AgentID:            req.AgentID,
		ParentAgentID:      state.ParentAgentID,
		AgentType:          state.AgentType,
		AgentMemoryScope:   state.AgentMemoryScope,
		Provider:           req.Provider,
		CWD:                req.CWD,
		Model:              model,
		Name:               displayName,
		Prompt:             displayName,
		RolloutPath:        rolloutPath,
		SessionUUID:        sessionUUID,
		ConfigOverride:     clone.RawMessage(state.ConfigOverrideRaw),
		CodexHome:          codexHome,
		CodexInstanceKey:   codexInstanceKey,
		CodexModelProvider: codexModelProvider,
		CreatedAt:          state.CreatedAt,
	})
	publicThreadID := threadState.PublicThreadID
	providerThreadID = threadState.ProviderThreadID
	if err := s.persistThreadState(ctx, threadState, true); err != nil {
		s.logResumePersistFailure(req.AgentID, publicThreadID, providerThreadID, err)
		// Only publish thread.Started as a fallback when the error is NOT a
		// binding conflict. Binding conflicts mean the provider_thread_id in
		// threadState belongs to another agent — publishing it would cause
		// the frontend to see a provider_mismatch and reload history with
		// the wrong UUID, resulting in empty conversation history.
		if !isBindingConflictError(err) {
			s.publishThreadStarted(threadState)
		} else {
			// Binding conflict means the codex session carries a UUID that
			// belongs to another active agent. Kill the zombie session to
			// prevent delta events from arriving on a half-alive channel,
			// and force a clean re-start on the next user interaction.
			if s.logger != nil {
				s.logger.Error("thread: binding conflict on resume — killing zombie session",
					"agent_id", req.AgentID,
					"stale_provider_thread_id", providerThreadID)
			}
			s.stopAgent(ctx, req.AgentID)
			return ResumeResult{}, fmt.Errorf("resume aborted due to binding conflict: %w", err)
		}
	}
	if promptResumeRestoreRequiresInvalidation(state.StoredCWD, req.CWD, s.cfg) {
		if err := s.invalidatePromptAssembly(ctx, contract.InvalidateResumeRestore); err != nil {
			return ResumeResult{}, err
		}
	}
	return ResumeResult{
		ThreadID:  publicThreadID,
		SessionID: util.FirstNonEmpty(providerThreadID, publicThreadID),
		Status:    "resumed",
		Model:     model,
		CWD:       req.CWD,
	}, nil
}

func (s *service) logResumePersistFailure(agentID, threadID, providerThreadID string, err error) {
	if s == nil || s.logger == nil {
		return
	}
	conflict := isBindingConflictError(err)
	s.logger.Warn("thread: resume persist failed",
		"error", err,
		"binding_conflict", conflict,
		"event_emitted", !conflict,
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
		s.logger.Debug("thread: persistThreadState binding snapshot",
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
		ConfigOverride:   clone.RawMessage(thread.ConfigOverride),
		CreatedAt:        thread.CreatedAt,
	}
}

func (s *service) stopAgent(ctx context.Context, agentID string) {
	util.LogIgnoredError(s.logger, "stop managed agent failed", s.stopManagedAgent(ctx, strings.TrimSpace(agentID), true))
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

func resolvedProviderUUID(session contract.Session) string {
	if session == nil {
		return ""
	}
	id := strings.TrimSpace(session.ThreadID())
	if identifier.LooksLikeUUID(id) {
		return id
	}
	return ""
}

func requireStartedProviderUUID(session contract.Session, provider, agentID string) (string, error) {
	id := resolvedProviderUUID(session)
	if id != "" {
		return id, nil
	}
	if allowDeferredStartedProviderUUID(session, provider, agentID) {
		return "", nil
	}
	return "", fmt.Errorf("thread: provider session UUID required to start agent %q (%s)", strings.TrimSpace(agentID), strings.TrimSpace(provider))
}

func allowDeferredStartedProviderUUID(session contract.Session, provider, agentID string) bool {
	if session == nil || !strings.EqualFold(strings.TrimSpace(provider), "claude") {
		return false
	}
	threadID := strings.TrimSpace(session.ThreadID())
	agentID = strings.TrimSpace(agentID)
	return threadID == "" || threadID == agentID || strings.HasPrefix(strings.ToLower(threadID), "agent_")
}

// isBindingConflictError reports whether err is a binding-uniqueness
// rejection (provider_thread_id or public_thread_id already belongs to a
// different agent). These errors mean the threadState carries identifiers
// that are wrong for the requesting agent, so publishing a thread.Started
// event with them would poison the frontend's loaded_provider_thread_id
// and trigger a provider_mismatch → stale-ID history reload → empty UI.
func isBindingConflictError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "already bound to agent") ||
		strings.Contains(msg, "already bound to provider") ||
		strings.Contains(msg, "already bound to public thread")
}
