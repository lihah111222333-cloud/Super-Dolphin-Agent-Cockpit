package thread

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	platformdb "github.com/anthropic-ai/super-agent-v3/internal/platform/db"
	bindingstore "github.com/anthropic-ai/super-agent-v3/internal/store/binding"
	"github.com/anthropic-ai/super-agent-v3/internal/util/identifier"
)

type bindingRegistration struct {
	AgentID, Provider, ProviderThreadID, PublicThreadID, CWD string
	RolloutPath, SessionUUID, ParentAgentID, AgentType       string
	AgentMemoryScope, CodexHome, CodexInstanceKey            string
	CodexModelProvider                                       string
	CreatedAt                                                int64
}

type bindingWriteOutcome struct {
	AgentID   string
	Persisted bool
	Previous  *bindingstore.Binding
}

func normalizeThreadState(state threadState) (threadState, error) {
	trim := strings.TrimSpace
	state.PublicThreadID, state.ProviderThreadID, state.OwnerThreadID = trim(state.PublicThreadID), trim(state.ProviderThreadID), trim(state.OwnerThreadID)
	state.AgentID, state.ParentAgentID, state.AgentType = trim(state.AgentID), trim(state.ParentAgentID), trim(state.AgentType)
	state.AgentMemoryScope, state.Provider, state.CWD = trim(state.AgentMemoryScope), trim(state.Provider), trim(state.CWD)
	state.CodexHome, state.CodexInstanceKey, state.CodexModelProvider = trim(state.CodexHome), trim(state.CodexInstanceKey), trim(state.CodexModelProvider)
	state.Model, state.Prompt = trim(state.Model), trim(state.Prompt)
	if state.PublicThreadID == "" || state.AgentID == "" {
		return threadState{}, errors.New("thread and agent ids are required")
	}
	if state.CreatedAt == 0 {
		state.CreatedAt = time.Now().Unix()
	}
	return state, nil
}

func normalizeBindingRegistration(state threadState) (bindingRegistration, error) {
	if state.AgentID == "" || state.Provider == "" || state.PublicThreadID == "" {
		return bindingRegistration{}, errors.New("binding requires agent, provider, and public thread ids")
	}
	providerThreadID := normalizeProviderThreadID(state.Provider, state.ProviderThreadID)
	if err := validateProviderThreadID(state.Provider, providerThreadID); err != nil {
		return bindingRegistration{}, err
	}
	return bindingRegistration{
		state.AgentID, state.Provider, providerThreadID, state.PublicThreadID, state.CWD,
		state.RolloutPath, state.SessionUUID, state.ParentAgentID, state.AgentType,
		state.AgentMemoryScope, state.CodexHome, state.CodexInstanceKey, state.CodexModelProvider, state.CreatedAt,
	}, nil
}

func normalizeProviderThreadID(provider, id string) string {
	id = strings.TrimSpace(id)
	if strings.EqualFold(strings.TrimSpace(provider), "claude") && !identifier.IsClaudeCLISessionUUID(id) {
		return ""
	}
	return id
}

func validateProviderThreadID(provider, id string) error {
	provider = strings.TrimSpace(provider)
	id = strings.TrimSpace(id)
	if !strings.EqualFold(provider, "claude") || id == "" {
		return nil
	}
	if identifier.IsClaudeCLISessionUUID(id) {
		return nil
	}
	return fmt.Errorf("claude provider_thread_id must be a session UUID")
}

// ensurePublicThreadAvailable 确保public线程available。
func (s *service) ensurePublicThreadAvailable(ctx context.Context, state threadState) error {
	if s == nil || s.threadStore == nil {
		return nil
	}
	existing, err := s.threadStore.GetByThreadID(ctx, state.PublicThreadID)
	if err != nil && !contract.IsNotFound(err) {
		return err
	}
	if contract.IsNotFound(err) {
		existing = nil
	}
	if existing == nil {
		return nil
	}
	existingAgentID := strings.TrimSpace(existing.AgentID)
	if existingAgentID == "" {
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

// registerThreadBinding 注册线程binding。
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
	s.logger.Warn("thread: registerThreadBinding persisted OK", "agent_id", r.AgentID, "provider", r.Provider,
		"provider_thread_id", r.ProviderThreadID, "public_thread_id", r.PublicThreadID, "rollout_path", r.RolloutPath, "session_uuid", r.SessionUUID)
}

// ensureProviderThreadAvailable 确保provider线程available。
func (s *service) ensureProviderThreadAvailable(ctx context.Context, registration bindingRegistration) error {
	if s == nil || s.bindingStore == nil {
		return nil
	}
	if strings.TrimSpace(registration.ProviderThreadID) == "" {
		return nil
	}
	existing, err := s.bindingStore.GetByProviderThread(ctx, registration.Provider, registration.ProviderThreadID)
	if err != nil && !contract.IsNotFound(err) {
		return err
	}
	if contract.IsNotFound(err) {
		existing = nil
	}
	if existing == nil {
		return nil
	}
	existingAgentID := strings.TrimSpace(existing.AgentID)
	if existingAgentID == registration.AgentID {
		return nil
	}
	return s.resolveProviderThreadConflict(ctx, registration, existingAgentID)
}

func (s *service) resolveProviderThreadConflict(ctx context.Context, registration bindingRegistration, existingAgentID string) error {
	if s.isSessionAlive(existingAgentID) {
		return fmt.Errorf("provider thread %q is already bound to agent %q (session active)", registration.ProviderThreadID, existingAgentID)
	}
	if s.logger != nil {
		s.logger.Warn("thread: evicting stale provider_thread_id binding",
			"provider_thread_id", registration.ProviderThreadID,
			"stale_agent_id", existingAgentID,
			"new_agent_id", registration.AgentID)
	}
	if err := s.bindingStore.UpdateProviderThreadID(ctx, bindingstore.UpdateProviderThreadIDParams{
		AgentID:          existingAgentID,
		ProviderThreadID: "",
		UpdatedAt:        time.Now().Unix(),
	}); err != nil {
		return fmt.Errorf("evict stale provider_thread_id binding: %w", err)
	}
	return nil
}

func (s *service) isSessionAlive(agentID string) bool {
	if s == nil || s.sessions == nil {
		return false
	}
	session, err := s.sessions.GetSession(strings.TrimSpace(agentID))
	return err == nil && session != nil
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
		bindingNeedsThreadMetadataUpdate(existing, registration) ||
		bindingNeedsCodexIdentityUpdate(existing, registration)
}

// verifyThreadBinding 验证线程binding。
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
	if bindingRequiresSessionUUID(binding, registration) &&
		strings.TrimSpace(binding.SessionUUID) != registration.SessionUUID {
		return fmt.Errorf("binding verification failed: session uuid mismatch for agent %q", registration.AgentID)
	}
	return nil
}

func (s *service) prepareBindingWrite(
	ctx context.Context,
	registration bindingRegistration,
) (*bindingstore.Binding, bindingWriteOutcome, error) {
	existing, err := s.bindingStore.GetByAgentID(ctx, registration.AgentID)
	notFound := contract.IsNotFound(err)
	if err != nil && !notFound {
		return nil, bindingWriteOutcome{}, err
	}
	if notFound {
		return nil, bindingWriteOutcome{AgentID: registration.AgentID}, nil
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

func bindingNeedsSessionUUIDUpdate(existing *bindingstore.Binding, registration bindingRegistration) bool {
	return bindingRequiresSessionUUID(existing, registration) &&
		strings.TrimSpace(existing.SessionUUID) != registration.SessionUUID
}

func bindingRequiresSessionUUID(existing *bindingstore.Binding, registration bindingRegistration) bool {
	if registration.SessionUUID == "" {
		return false
	}
	providerThreadID := strings.TrimSpace(existing.ProviderThreadID)
	return providerThreadID == "" || providerThreadID == strings.TrimSpace(existing.AgentID)
}

// bindingNeedsThreadMetadataUpdate 处理bindingneeds线程元数据更新。
func bindingNeedsThreadMetadataUpdate(existing *bindingstore.Binding, registration bindingRegistration) bool {
	return bindingNeedsInitialValue(strings.TrimSpace(existing.CodexThreadID), registration.PublicThreadID) ||
		bindingNeedsSessionUUIDUpdate(existing, registration) ||
		bindingNeedsInitialValue(strings.TrimSpace(existing.Cwd), registration.CWD) ||
		bindingNeedsInitialValue(strings.TrimSpace(existing.ParentAgentID), registration.ParentAgentID) ||
		bindingNeedsInitialValue(strings.TrimSpace(existing.AgentType), registration.AgentType) ||
		bindingNeedsInitialValue(strings.TrimSpace(existing.AgentMemoryScope), registration.AgentMemoryScope)
}

func bindingNeedsCodexIdentityUpdate(existing *bindingstore.Binding, registration bindingRegistration) bool {
	return bindingNeedsInitialValue(strings.TrimSpace(existing.CodexHome), registration.CodexHome) ||
		bindingNeedsInitialValue(strings.TrimSpace(existing.CodexInstanceKey), registration.CodexInstanceKey) ||
		bindingNeedsInitialValue(strings.TrimSpace(existing.CodexModelProvider), registration.CodexModelProvider)
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

// rollbackThreadBinding 处理rollback线程binding。
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

// lookupPersistedAgentID 处理lookuppersisted代理ID。
func (s *service) lookupPersistedAgentID(ctx context.Context, threadID string) (string, bool, error) {
	if s == nil || s.threadStore == nil {
		return "", false, nil
	}
	thread, err := s.threadStore.GetByThreadID(ctx, threadID)
	if err != nil {
		if platformdb.IsNotFound(err) {
			return "", false, fmt.Errorf("thread %q not found: %w", strings.TrimSpace(threadID), contract.ErrNotFound)
		}
		return "", false, err
	}
	if thread == nil {
		return "", false, nil
	}
	return strings.TrimSpace(thread.AgentID), true, nil
}

type bindingRecoveryReporter struct {
	store  bindingstore.Store
	logger *slog.Logger
}

// NewBindingRecoveryReporter 创建bindingrecoveryreporter。
func NewBindingRecoveryReporter(store bindingstore.Store, logger *slog.Logger) contract.SessionRecoveryReporter {
	return &bindingRecoveryReporter{store: store, logger: logger}
}

// ClearStaleProviderThreadID 清理staleprovider线程ID。
func (r *bindingRecoveryReporter) ClearStaleProviderThreadID(ctx context.Context, agentID string) error {
	if r == nil || r.store == nil {
		return nil
	}
	agentID = strings.TrimSpace(agentID)
	if agentID == "" {
		return nil
	}
	binding, err := r.store.GetByAgentID(ctx, agentID)
	if err != nil || binding == nil {
		return err
	}
	current := strings.TrimSpace(binding.ProviderThreadID)
	if current == "" || identifier.LooksLikeUUID(current) {
		return nil
	}
	if err := r.store.UpdateProviderThreadID(ctx, bindingstore.UpdateProviderThreadIDParams{
		AgentID:          agentID,
		ProviderThreadID: "",
		UpdatedAt:        time.Now().Unix(),
	}); err != nil {
		return err
	}
	if r.logger != nil {
		r.logger.Info("thread: cleared stale provider_thread_id", "agent_id", agentID, "old_provider_thread_id", current)
	}
	return nil
}

// RecordProviderSessionUUID 记录provider会话UUID。
func (r *bindingRecoveryReporter) RecordProviderSessionUUID(ctx context.Context, agentID, sessionUUID string) error {
	if r == nil || r.store == nil {
		return nil
	}
	agentID = strings.TrimSpace(agentID)
	sessionUUID = strings.TrimSpace(sessionUUID)
	if agentID == "" || !identifier.LooksLikeUUID(sessionUUID) {
		return nil
	}
	binding, err := r.store.GetByAgentID(ctx, agentID)
	if err != nil || binding == nil {
		return err
	}
	updatedAt := time.Now().Unix()
	if err := r.recordSessionUUID(ctx, agentID, sessionUUID, updatedAt); err != nil {
		return err
	}
	if err := r.recordProviderThreadID(ctx, binding, agentID, sessionUUID, updatedAt); err != nil {
		return err
	}
	if r.logger != nil {
		r.logger.Info("thread: recorded provider session uuid", "agent_id", agentID, "session_uuid", sessionUUID)
	}
	return nil
}

func (r *bindingRecoveryReporter) recordSessionUUID(ctx context.Context, agentID, sessionUUID string, updatedAt int64) error {
	return r.store.UpdateSessionUUID(ctx, bindingstore.UpdateSessionUUIDParams{
		AgentID:     agentID,
		SessionUUID: sessionUUID,
		UpdatedAt:   updatedAt,
	})
}

// recordProviderThreadID 记录provider线程ID。
func (r *bindingRecoveryReporter) recordProviderThreadID(ctx context.Context, binding *bindingstore.Binding, agentID, sessionUUID string, updatedAt int64) error {
	current := strings.TrimSpace(binding.ProviderThreadID)
	if current != "" && current != agentID && identifier.LooksLikeUUID(current) {
		return nil
	}
	if !bindingHasProviderHistoryForUUID(binding, sessionUUID) {
		if r.logger != nil {
			r.logger.Info("thread: provider session uuid is not recoverable",
				"agent_id", agentID,
				"session_uuid", sessionUUID,
				"rollout_path", binding.RolloutPath)
		}
		return nil
	}
	return r.store.UpdateProviderThreadID(ctx, bindingstore.UpdateProviderThreadIDParams{
		AgentID:          agentID,
		ProviderThreadID: sessionUUID,
		UpdatedAt:        updatedAt,
	})
}
