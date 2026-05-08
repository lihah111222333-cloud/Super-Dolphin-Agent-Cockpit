package thread

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"

	bindingstore "github.com/anthropic-ai/super-agent-v3/internal/store/binding"
)

type bindingRegistration struct {
	AgentID            string
	Provider           string
	ProviderThreadID   string
	PublicThreadID     string
	CWD                string
	RolloutPath        string
	SessionUUID        string
	ParentAgentID      string
	AgentType          string
	AgentMemoryScope   string
	CodexHome          string
	CodexInstanceKey   string
	CodexModelProvider string
	CreatedAt          int64
}

type bindingWriteOutcome struct {
	AgentID   string
	Persisted bool
	Previous  *bindingstore.Binding
}

func normalizeThreadState(state threadState) (threadState, error) {
	state.PublicThreadID = strings.TrimSpace(state.PublicThreadID)
	state.ProviderThreadID = strings.TrimSpace(state.ProviderThreadID)
	state.OwnerThreadID = strings.TrimSpace(state.OwnerThreadID)
	state.AgentID = strings.TrimSpace(state.AgentID)
	state.ParentAgentID = strings.TrimSpace(state.ParentAgentID)
	state.AgentType = strings.TrimSpace(state.AgentType)
	state.AgentMemoryScope = strings.TrimSpace(state.AgentMemoryScope)
	state.Provider = strings.TrimSpace(state.Provider)
	state.CWD = strings.TrimSpace(state.CWD)
	state.CodexHome = strings.TrimSpace(state.CodexHome)
	state.CodexInstanceKey = strings.TrimSpace(state.CodexInstanceKey)
	state.CodexModelProvider = strings.TrimSpace(state.CodexModelProvider)
	state.Model = strings.TrimSpace(state.Model)
	state.Prompt = strings.TrimSpace(state.Prompt)
	if state.PublicThreadID == "" || state.AgentID == "" {
		return threadState{}, errors.New("thread and agent ids are required")
	}
	// provider_thread_id is left empty when the real UUID is not yet known
	// (e.g. Claude resolves it asynchronously). It will be filled in once
	// the handshake completes — no placeholder needed.
	if state.CreatedAt == 0 {
		state.CreatedAt = time.Now().Unix()
	}
	return state, nil
}

func normalizeBindingRegistration(state threadState) (bindingRegistration, error) {
	if state.AgentID == "" || state.Provider == "" || state.PublicThreadID == "" {
		return bindingRegistration{}, errors.New("binding requires agent, provider, and public thread ids")
	}
	// provider_thread_id may be empty on initial launch; it is filled
	// in later once the session handshake returns the real UUID.
	return bindingRegistration{
		AgentID:            state.AgentID,
		Provider:           state.Provider,
		ProviderThreadID:   strings.TrimSpace(state.ProviderThreadID),
		PublicThreadID:     state.PublicThreadID,
		CWD:                state.CWD,
		RolloutPath:        state.RolloutPath,
		SessionUUID:        state.SessionUUID,
		ParentAgentID:      state.ParentAgentID,
		AgentType:          state.AgentType,
		AgentMemoryScope:   state.AgentMemoryScope,
		CodexHome:          state.CodexHome,
		CodexInstanceKey:   state.CodexInstanceKey,
		CodexModelProvider: state.CodexModelProvider,
		CreatedAt:          state.CreatedAt,
	}, nil
}

func (s *service) ensurePublicThreadAvailable(ctx context.Context, state threadState) error {
	if s == nil || s.threadStore == nil {
		return nil
	}
	existing, err := s.threadStore.GetByThreadID(ctx, state.PublicThreadID)
	if err != nil {
		if contract.IsNotFound(err) {
			return nil
		}
		return err
	}
	if existing == nil {
		return nil
	}
	existingAgentID := strings.TrimSpace(existing.AgentID)
	if existingAgentID == "" {
		// Pending-launch rows (written by startPendingThread) legitimately
		// have no binding yet — SpawnIfNeeded is exactly the caller promoting
		// them. Treat that as the same-owner case so the upsert can proceed.
		if existing.PendingLaunch {
			return nil
		}
		return fmt.Errorf("public thread id %q already exists without a binding owner", state.PublicThreadID)
	}
	if existingAgentID != state.AgentID {
		return fmt.Errorf("public thread id %q is already bound to agent %q", state.PublicThreadID, existingAgentID)
	}
	return nil
}

func (s *service) registerThreadBinding(ctx context.Context, state threadState) (bindingWriteOutcome, error) {
	if s == nil || s.bindingStore == nil {
		s.logBindingSkipped(state.AgentID, "no binding store")
		return bindingWriteOutcome{}, nil
	}
	registration, err := normalizeBindingRegistration(state)
	if err != nil {
		return bindingWriteOutcome{}, err
	}
	if err := s.ensureProviderThreadAvailable(ctx, registration); err != nil {
		return bindingWriteOutcome{}, err
	}
	existing, outcome, err := s.prepareBindingWrite(ctx, registration)
	if err != nil {
		return bindingWriteOutcome{}, err
	}
	if err := validateBindingRegistration(existing, registration); err != nil {
		return outcome, err
	}
	if !shouldPersistBinding(existing, registration) {
		s.logBindingSkipped(registration.AgentID, "no change needed")
		return outcome, nil
	}
	if err := s.persistRegisteredBinding(ctx, registration); err != nil {
		s.logBindingPersistFailed(registration, err)
		return outcome, err
	}
	outcome.Persisted = true
	s.logBindingPersisted(registration)
	return outcome, s.verifyOrRollbackThreadBinding(ctx, registration, outcome)
}

func (s *service) logBindingSkipped(agentID, reason string) {
	if s == nil || s.logger == nil {
		return
	}
	s.logger.Warn("thread: registerThreadBinding skipped",
		"agent_id", agentID, "reason", reason)
}

func (s *service) logBindingPersistFailed(r bindingRegistration, err error) {
	if s == nil || s.logger == nil {
		return
	}
	s.logger.Warn("thread: registerThreadBinding FAILED to persist",
		"agent_id", r.AgentID,
		"provider_thread_id", r.ProviderThreadID,
		"error", err)
}

func (s *service) logBindingPersisted(r bindingRegistration) {
	if s == nil || s.logger == nil {
		return
	}
	s.logger.Warn("thread: registerThreadBinding persisted OK",
		"agent_id", r.AgentID,
		"provider", r.Provider,
		"provider_thread_id", r.ProviderThreadID,
		"public_thread_id", r.PublicThreadID,
		"rollout_path", r.RolloutPath,
		"session_uuid", r.SessionUUID)
}

func (s *service) ensureProviderThreadAvailable(ctx context.Context, registration bindingRegistration) error {
	if s == nil || s.bindingStore == nil {
		return nil
	}
	// An empty provider_thread_id means the real UUID has not been resolved
	// yet (e.g. Claude CLI resolves it asynchronously after the first user
	// message). Empty strings are not meaningful unique identifiers and must
	// not trigger binding conflicts.
	if strings.TrimSpace(registration.ProviderThreadID) == "" {
		return nil
	}
	existing, err := s.bindingStore.GetByProviderThread(ctx, registration.Provider, registration.ProviderThreadID)
	if err != nil {
		if contract.IsNotFound(err) {
			return nil
		}
		return err
	}
	if existing == nil {
		return nil
	}
	if existingAgentID := strings.TrimSpace(existing.AgentID); existingAgentID != registration.AgentID {
		return fmt.Errorf("provider thread %q is already bound to agent %q", registration.ProviderThreadID, existingAgentID)
	}
	return nil
}

func validateBindingRegistration(existing *bindingstore.Binding, registration bindingRegistration) error {
	if existing == nil {
		return nil
	}
	for _, validate := range []func(*bindingstore.Binding, bindingRegistration) error{
		validateBindingProvider,
		validateBindingProviderThread,
		validateBindingPublicThread,
		validateBindingCWD,
		validateBindingParentAgentID,
		validateBindingAgentType,
		validateBindingMemoryScope,
	} {
		if err := validate(existing, registration); err != nil {
			return err
		}
	}
	return nil
}

func shouldPersistBinding(existing *bindingstore.Binding, registration bindingRegistration) bool {
	if existing == nil {
		return true
	}
	return bindingNeedsProviderThreadUpdate(existing, registration) ||
		bindingNeedsInitialValue(strings.TrimSpace(existing.CodexThreadID), registration.PublicThreadID) ||
		bindingNeedsInitialValue(strings.TrimSpace(existing.Cwd), registration.CWD) ||
		bindingNeedsInitialValue(strings.TrimSpace(existing.ParentAgentID), registration.ParentAgentID) ||
		bindingNeedsInitialValue(strings.TrimSpace(existing.AgentType), registration.AgentType) ||
		bindingNeedsInitialValue(strings.TrimSpace(existing.AgentMemoryScope), registration.AgentMemoryScope) ||
		bindingNeedsInitialValue(strings.TrimSpace(existing.CodexHome), registration.CodexHome) ||
		bindingNeedsInitialValue(strings.TrimSpace(existing.CodexInstanceKey), registration.CodexInstanceKey) ||
		bindingNeedsInitialValue(strings.TrimSpace(existing.CodexModelProvider), registration.CodexModelProvider)
}

func (s *service) verifyThreadBinding(ctx context.Context, registration bindingRegistration) error {
	if s == nil || s.bindingStore == nil {
		return nil
	}
	binding, err := s.bindingStore.GetByAgentID(ctx, registration.AgentID)
	if err != nil {
		return err
	}
	if binding == nil {
		return fmt.Errorf("binding verification failed for agent %q", registration.AgentID)
	}
	for _, check := range []bindingVerification{
		{label: "provider", actual: strings.TrimSpace(binding.Provider), expected: registration.Provider},
		{label: "provider thread", actual: strings.TrimSpace(binding.ProviderThreadID), expected: registration.ProviderThreadID},
		{label: "public thread", actual: strings.TrimSpace(binding.CodexThreadID), expected: registration.PublicThreadID},
		{label: "cwd", actual: strings.TrimSpace(binding.Cwd), expected: registration.CWD, optional: true},
		{label: "parent agent", actual: strings.TrimSpace(binding.ParentAgentID), expected: registration.ParentAgentID, optional: true},
		{label: "agent type", actual: strings.TrimSpace(binding.AgentType), expected: registration.AgentType, optional: true},
		{label: "memory scope", actual: strings.TrimSpace(binding.AgentMemoryScope), expected: registration.AgentMemoryScope, optional: true},
		{label: "codex home", actual: strings.TrimSpace(binding.CodexHome), expected: registration.CodexHome, optional: true},
		{label: "codex instance key", actual: strings.TrimSpace(binding.CodexInstanceKey), expected: registration.CodexInstanceKey, optional: true},
		{label: "codex model provider", actual: strings.TrimSpace(binding.CodexModelProvider), expected: registration.CodexModelProvider, optional: true},
	} {
		if check.mismatch() {
			return fmt.Errorf("binding verification failed: %s mismatch for agent %q", check.label, registration.AgentID)
		}
	}
	return nil
}

func (s *service) prepareBindingWrite(
	ctx context.Context,
	registration bindingRegistration,
) (*bindingstore.Binding, bindingWriteOutcome, error) {
	existing, err := s.bindingStore.GetByAgentID(ctx, registration.AgentID)
	if err != nil {
		if contract.IsNotFound(err) {
			return nil, bindingWriteOutcome{AgentID: registration.AgentID}, nil
		}
		return nil, bindingWriteOutcome{}, err
	}
	return existing, bindingWriteOutcome{
		AgentID:  registration.AgentID,
		Previous: cloneBinding(existing),
	}, nil
}

func (s *service) persistRegisteredBinding(ctx context.Context, registration bindingRegistration) error {
	return s.bindingStore.Upsert(ctx, newBindingUpsertParams(bindingstore.Binding{
		AgentID:            registration.AgentID,
		Provider:           registration.Provider,
		ProviderThreadID:   registration.ProviderThreadID,
		CodexThreadID:      registration.PublicThreadID,
		RolloutPath:        registration.RolloutPath,
		SessionUUID:        registration.SessionUUID,
		Cwd:                registration.CWD,
		ParentAgentID:      registration.ParentAgentID,
		AgentType:          registration.AgentType,
		AgentMemoryScope:   registration.AgentMemoryScope,
		CodexHome:          registration.CodexHome,
		CodexInstanceKey:   registration.CodexInstanceKey,
		CodexModelProvider: registration.CodexModelProvider,
		CreatedAt:          registration.CreatedAt,
		UpdatedAt:          time.Now().Unix(),
	}))
}

func (s *service) verifyOrRollbackThreadBinding(
	ctx context.Context,
	registration bindingRegistration,
	outcome bindingWriteOutcome,
) error {
	if err := s.verifyThreadBinding(ctx, registration); err != nil {
		if rollbackErr := s.rollbackThreadBinding(ctx, outcome); rollbackErr != nil {
			return errors.Join(err, rollbackErr)
		}
		return err
	}
	return nil
}

func validateBindingProvider(existing *bindingstore.Binding, registration bindingRegistration) error {
	if provider := strings.TrimSpace(existing.Provider); provider != "" && provider != registration.Provider {
		return fmt.Errorf("agent %q is already bound to provider %q", registration.AgentID, provider)
	}
	return nil
}

func validateBindingProviderThread(existing *bindingstore.Binding, registration bindingRegistration) error {
	existingID := strings.TrimSpace(existing.ProviderThreadID)
	// Allow empty → non-empty (first fill after async handshake).
	if existingID == "" {
		return nil
	}
	// Same value — no conflict.
	if existingID == registration.ProviderThreadID {
		return nil
	}
	// Allow placeholder agent_id → real UUID correction.
	if existingID == strings.TrimSpace(existing.AgentID) {
		return nil
	}
	// UUID already set and caller tries to change it — reject.
	return fmt.Errorf("agent %q provider_thread_id is immutable (existing=%q, new=%q)",
		registration.AgentID, existingID, registration.ProviderThreadID)
}

func validateBindingPublicThread(existing *bindingstore.Binding, registration bindingRegistration) error {
	if publicThreadID := strings.TrimSpace(existing.CodexThreadID); publicThreadID != "" && publicThreadID != registration.PublicThreadID {
		return fmt.Errorf("agent %q is already bound to public thread %q", registration.AgentID, publicThreadID)
	}
	return nil
}

func validateBindingCWD(existing *bindingstore.Binding, registration bindingRegistration) error {
	if cwd := strings.TrimSpace(existing.Cwd); cwd != "" && registration.CWD != "" && cwd != registration.CWD {
		return fmt.Errorf("agent %q binding cwd mismatch", registration.AgentID)
	}
	return nil
}

type bindingVerification struct {
	label    string
	actual   string
	expected string
	optional bool
}

func (v bindingVerification) mismatch() bool {
	if v.optional && v.expected == "" {
		return false
	}
	return v.actual != v.expected
}

func bindingNeedsProviderThreadUpdate(existing *bindingstore.Binding, registration bindingRegistration) bool {
	return strings.TrimSpace(existing.ProviderThreadID) != registration.ProviderThreadID &&
		registration.ProviderThreadID != ""
}

func bindingNeedsInitialValue(existing, incoming string) bool {
	return existing == "" && incoming != ""
}

func validateBindingParentAgentID(existing *bindingstore.Binding, registration bindingRegistration) error {
	if parentID := strings.TrimSpace(existing.ParentAgentID); parentID != "" &&
		registration.ParentAgentID != "" &&
		parentID != registration.ParentAgentID {
		return fmt.Errorf("agent %q parent_agent_id is immutable", registration.AgentID)
	}
	return nil
}

func validateBindingAgentType(existing *bindingstore.Binding, registration bindingRegistration) error {
	if agentType := strings.TrimSpace(existing.AgentType); agentType != "" &&
		registration.AgentType != "" &&
		agentType != registration.AgentType {
		return fmt.Errorf("agent %q agent_type is immutable", registration.AgentID)
	}
	return nil
}

func validateBindingMemoryScope(existing *bindingstore.Binding, registration bindingRegistration) error {
	if scope := strings.TrimSpace(existing.AgentMemoryScope); scope != "" &&
		registration.AgentMemoryScope != "" &&
		scope != registration.AgentMemoryScope {
		return fmt.Errorf("agent %q agent_memory_scope is immutable", registration.AgentID)
	}
	return nil
}

func (s *service) rollbackThreadBinding(ctx context.Context, outcome bindingWriteOutcome) error {
	if s == nil || s.bindingStore == nil || !outcome.Persisted {
		return nil
	}
	if ctx == nil || ctx.Err() != nil {
		ctx = context.Background()
	}
	if outcome.Previous == nil {
		return s.bindingStore.DeleteByAgentID(ctx, outcome.AgentID)
	}
	return s.bindingStore.Upsert(ctx, newBindingUpsertParams(bindingstore.Binding{
		AgentID:          strings.TrimSpace(outcome.Previous.AgentID),
		Provider:         strings.TrimSpace(outcome.Previous.Provider),
		ProviderThreadID: strings.TrimSpace(outcome.Previous.ProviderThreadID),
		CodexThreadID:    strings.TrimSpace(outcome.Previous.CodexThreadID),
		RolloutPath:      strings.TrimSpace(outcome.Previous.RolloutPath),
		Cwd:              strings.TrimSpace(outcome.Previous.Cwd),
		ParentAgentID:    strings.TrimSpace(outcome.Previous.ParentAgentID),
		AgentType:        strings.TrimSpace(outcome.Previous.AgentType),
		AgentMemoryScope: strings.TrimSpace(outcome.Previous.AgentMemoryScope),
		CreatedAt:        outcome.Previous.CreatedAt,
		UpdatedAt:        time.Now().Unix(),
	}))
}

func cloneBinding(binding *bindingstore.Binding) *bindingstore.Binding {
	if binding == nil {
		return nil
	}
	copy := *binding
	return &copy
}

func (s *service) lookupPersistedAgentID(ctx context.Context, threadID string) string {
	if s == nil || s.threadStore == nil {
		return ""
	}
	thread, err := s.threadStore.GetByThreadID(ctx, threadID)
	if err != nil || thread == nil {
		return ""
	}
	return strings.TrimSpace(thread.AgentID)
}
