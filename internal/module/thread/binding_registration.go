package thread

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"strings"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
	platformdb "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/db"
	platformshared "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/shared"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/util/identifier"
)

// bindingRegistration 是写入 binding store 前的规范化线程绑定请求。
// 构造时已完成 provider thread id 校验，后续持久化逻辑不要再改写这些字段。
type bindingRegistration struct {
	AgentID, Provider, ProviderThreadID, PublicThreadID, CWD string
	RolloutPath, SessionUUID, ParentAgentID, AgentType       string
	AgentMemoryScope, CodexHome, CodexInstanceKey            string
	CodexModelProvider                                       string
	CreatedAt                                                int64
}

// bindingWriteOutcome 描述一次 binding 写入是否真正落库。
// Previous 保留旧记录，供调用方在校验失败时返回可解释的冲突信息。
type bindingWriteOutcome struct {
	AgentID   string
	Persisted bool
	Previous  *threadBindingRecord
}

// normalizeThreadState 清理线程状态中的可变输入，并补齐创建时间。
// 公开线程 ID 和 agent ID 是持久化主键边界，缺失时必须直接返回错误。
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
		state.CreatedAt = time.Now().UnixMilli()
	}
	return state, nil
}

// normalizeBindingRegistration 将 threadState 收敛为 binding store 可写入的字段集合。
// provider thread id 会按 provider 规则重新规范化，非法值在进入 store 前阻断。
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

// normalizeProviderThreadID 清理 provider 侧线程 ID。
// Claude 只接受真实 CLI session UUID，非 UUID 值会被清空以避免错误绑定。
func normalizeProviderThreadID(provider, id string) string {
	id = strings.TrimSpace(id)
	if strings.EqualFold(strings.TrimSpace(provider), "claude") && !identifier.IsClaudeCLISessionUUID(id) {
		return ""
	}
	return id
}

// validateProviderThreadID 校验 provider thread id 是否满足 provider 自身格式。
// 目前只有 Claude 需要 session UUID；其他 provider 允许空值或自有格式。
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

// ensurePublicThreadAvailable 确认公开线程 ID 没有被其他 agent 占用。
// 已存在但仍处于 pending launch 的记录允许继续绑定，避免启动过程误报冲突。
func (s *service) ensurePublicThreadAvailable(ctx context.Context, state threadState) error {
	store := s.threadConfigStorePort()
	if store == nil {
		return nil
	}
	existing, err := store.GetByThreadID(ctx, state.PublicThreadID)
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

// registerThreadBinding 在校验 provider 和公开线程占用后持久化线程绑定。
func (s *service) registerThreadBinding(ctx context.Context, state threadState) (bindingWriteOutcome, error) {
	store := s.threadBindingStorePort()
	if store == nil {
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
	s.logger.Warn("thread: registerThreadBinding skipped", "agent_id", agentID, "reason", reason)
}
func (s *service) logBindingPersistFailed(r bindingRegistration, err error) {
	if s == nil || s.logger == nil {
		return
	}
	s.logger.Warn("thread: registerThreadBinding FAILED to persist", "agent_id", r.AgentID, "provider_thread_id", r.ProviderThreadID, "error", err)
}
func (s *service) logBindingPersisted(r bindingRegistration) {
	if s == nil || s.logger == nil {
		return
	}
	fields := []any{"agent_id", r.AgentID, "provider", r.Provider, "provider_thread_id", r.ProviderThreadID, "public_thread_id", r.PublicThreadID, "session_uuid", r.SessionUUID}
	fields = append(fields, platformshared.SafePathLogFields("rollout_path", r.RolloutPath)...)
	s.logger.Warn("thread: registerThreadBinding persisted OK", fields...)
}

// ensureProviderThreadAvailable 确认 provider_thread_id 没有被其他存活 agent 占用。
// 写入前先查冲突，只有旧 agent 已不存活时才驱逐 stale binding，避免同一个 provider 会话被两个线程恢复。
func (s *service) ensureProviderThreadAvailable(ctx context.Context, registration bindingRegistration) error {
	store := s.threadBindingStorePort()
	if store == nil {
		return nil
	}
	if strings.TrimSpace(registration.ProviderThreadID) == "" {
		return nil
	}
	existing, err := store.GetByProviderThread(ctx, registration.Provider, registration.ProviderThreadID)
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
		s.logger.Warn("thread: evicting stale provider_thread_id binding", "provider_thread_id", registration.ProviderThreadID, "stale_agent_id", existingAgentID, "new_agent_id", registration.AgentID)
	}
	store := s.threadBindingStorePort()
	if store == nil {
		return nil
	}
	if err := store.UpdateProviderThreadID(ctx, threadBindingProviderThreadIDUpdate{AgentID: existingAgentID, ProviderThreadID: "", UpdatedAt: time.Now().UnixMilli()}); err != nil {
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
func validateBindingRegistration(existing *threadBindingRecord, registration bindingRegistration) error {
	if existing == nil {
		return nil
	}
	for _, validate := range []func(*threadBindingRecord, bindingRegistration) error{validateBindingProvider, validateBindingProviderThread, validateBindingPublicThread, validateBindingCWD, validateBindingParentAgentID, validateBindingAgentType, validateBindingMemoryScope, validateBindingCodexIdentity} {
		if err := validate(existing, registration); err != nil {
			return err
		}
	}
	return nil
}
func shouldPersistBinding(existing *threadBindingRecord, registration bindingRegistration) bool {
	if existing == nil {
		return true
	}
	return bindingNeedsProviderThreadUpdate(existing, registration) ||
		bindingNeedsThreadMetadataUpdate(existing, registration) ||
		bindingNeedsCodexIdentityUpdate(existing, registration)
}

// verifyThreadBinding 验证线程binding。
func (s *service) verifyThreadBinding(ctx context.Context, registration bindingRegistration) error {
	store := s.threadBindingStorePort()
	if store == nil {
		return nil
	}
	binding, err := store.GetByAgentID(ctx, registration.AgentID)
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
func (s *service) prepareBindingWrite(ctx context.Context, registration bindingRegistration) (*threadBindingRecord, bindingWriteOutcome, error) {
	store := s.threadBindingStorePort()
	if store == nil {
		return nil, bindingWriteOutcome{}, nil
	}
	existing, err := store.GetByAgentID(ctx, registration.AgentID)
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
	store := s.threadBindingStorePort()
	if store == nil {
		return nil
	}
	return store.Upsert(ctx, newBindingUpsertParams(threadBindingRecord{
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
		UpdatedAt:          time.Now().UnixMilli(),
	}))
}
func (s *service) verifyOrRollbackThreadBinding(ctx context.Context, registration bindingRegistration, outcome bindingWriteOutcome) error {
	if err := s.verifyThreadBinding(ctx, registration); err != nil {
		if rollbackErr := s.rollbackThreadBinding(ctx, outcome); rollbackErr != nil {
			return errors.Join(err, rollbackErr)
		}
		return err
	}
	return nil
}
func validateBindingProvider(existing *threadBindingRecord, registration bindingRegistration) error {
	if provider := strings.TrimSpace(existing.Provider); provider != "" && provider != registration.Provider {
		return fmt.Errorf("agent %q is already bound to provider %q", registration.AgentID, provider)
	}
	return nil
}
func validateBindingProviderThread(existing *threadBindingRecord, registration bindingRegistration) error {
	existingID := strings.TrimSpace(existing.ProviderThreadID)
	if existingID == "" {
		return nil
	}
	if existingID == registration.ProviderThreadID {
		return nil
	}
	if existingID == strings.TrimSpace(existing.AgentID) {
		return nil
	}
	return fmt.Errorf("agent %q provider_thread_id is immutable (existing=%q, new=%q)",
		registration.AgentID, existingID, registration.ProviderThreadID)
}
func validateBindingPublicThread(existing *threadBindingRecord, registration bindingRegistration) error {
	if publicThreadID := strings.TrimSpace(existing.CodexThreadID); publicThreadID != "" && publicThreadID != registration.PublicThreadID {
		return fmt.Errorf("agent %q is already bound to public thread %q", registration.AgentID, publicThreadID)
	}
	return nil
}
func validateBindingCWD(existing *threadBindingRecord, registration bindingRegistration) error {
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
func bindingNeedsProviderThreadUpdate(existing *threadBindingRecord, registration bindingRegistration) bool {
	return strings.TrimSpace(existing.ProviderThreadID) != registration.ProviderThreadID &&
		registration.ProviderThreadID != ""
}
func bindingNeedsSessionUUIDUpdate(existing *threadBindingRecord, registration bindingRegistration) bool {
	return bindingRequiresSessionUUID(existing, registration) &&
		strings.TrimSpace(existing.SessionUUID) != registration.SessionUUID
}
func bindingRequiresSessionUUID(existing *threadBindingRecord, registration bindingRegistration) bool {
	if registration.SessionUUID == "" {
		return false
	}
	providerThreadID := strings.TrimSpace(existing.ProviderThreadID)
	return providerThreadID == "" || providerThreadID == strings.TrimSpace(existing.AgentID)
}

// bindingNeedsThreadMetadataUpdate 判断是否需要补写线程元数据。
// 只补齐空字段或允许更新的 session UUID，不覆盖已经持久化的不可变信息。
func bindingNeedsThreadMetadataUpdate(existing *threadBindingRecord, registration bindingRegistration) bool {
	return bindingNeedsInitialValue(strings.TrimSpace(existing.CodexThreadID), registration.PublicThreadID) ||
		bindingNeedsSessionUUIDUpdate(existing, registration) ||
		bindingNeedsInitialValue(strings.TrimSpace(existing.Cwd), registration.CWD) ||
		bindingNeedsInitialValue(strings.TrimSpace(existing.ParentAgentID), registration.ParentAgentID) ||
		bindingNeedsInitialValue(strings.TrimSpace(existing.AgentType), registration.AgentType) ||
		bindingNeedsInitialValue(strings.TrimSpace(existing.AgentMemoryScope), registration.AgentMemoryScope)
}

// bindingNeedsCodexIdentityUpdate 只在补齐空身份或修正已验证的 CodexHome alias 时触发写回。
func bindingNeedsCodexIdentityUpdate(existing *threadBindingRecord, registration bindingRegistration) bool {
	existingHome := strings.TrimSpace(existing.CodexHome)
	return bindingNeedsInitialValue(existingHome, registration.CodexHome) ||
		bindingNeedsInitialValue(strings.TrimSpace(existing.CodexInstanceKey), registration.CodexInstanceKey) ||
		bindingNeedsInitialValue(strings.TrimSpace(existing.CodexModelProvider), registration.CodexModelProvider) ||
		(existingHome != "" && registration.CodexHome != "" && existingHome != registration.CodexHome && registration.CodexInstanceKey != "" && registration.CodexModelProvider != "")
}
func bindingNeedsInitialValue(existing, incoming string) bool {
	return existing == "" && incoming != ""
}
func validateBindingParentAgentID(existing *threadBindingRecord, registration bindingRegistration) error {
	return validateBindingImmutableField(registration.AgentID, "parent_agent_id", existing.ParentAgentID, registration.ParentAgentID)
}
func validateBindingAgentType(existing *threadBindingRecord, registration bindingRegistration) error {
	return validateBindingImmutableField(registration.AgentID, "agent_type", existing.AgentType, registration.AgentType)
}
func validateBindingMemoryScope(existing *threadBindingRecord, registration bindingRegistration) error {
	return validateBindingImmutableField(registration.AgentID, "agent_memory_scope", existing.AgentMemoryScope, registration.AgentMemoryScope)
}
func validateBindingImmutableField(agentID, label, old, next string) error {
	old, next = strings.TrimSpace(old), strings.TrimSpace(next)
	if old != "" && next != "" && old != next {
		return fmt.Errorf("agent %q %s is immutable", agentID, label)
	}
	return nil
}

// validateBindingCodexIdentity 只允许补齐空字段或把同一路径的旧 alias 修正成 canonical home，非空 tuple 不可覆盖。
func validateBindingCodexIdentity(existing *threadBindingRecord, registration bindingRegistration) error {
	if err := validateCodexIdentityTupleField(registration.AgentID, "codex instance key", existing.CodexInstanceKey, registration.CodexInstanceKey); err != nil {
		return err
	}
	if err := validateCodexIdentityTupleField(registration.AgentID, "codex model provider", existing.CodexModelProvider, registration.CodexModelProvider); err != nil {
		return err
	}
	return validateCodexHomeAliasRepair(existing, registration)
}
func validateCodexIdentityTupleField(agentID, label, old, next string) error {
	old, next = strings.TrimSpace(old), strings.TrimSpace(next)
	if old == "" || next == "" || old == next {
		return nil
	}
	return fmt.Errorf("agent %q %s is immutable (existing=%q, new=%q)", agentID, label, old, next)
}

// validateCodexHomeAliasRepair 允许 clean/canonical alias 修复，其他非空 CodexHome 变更都 fail-fast。
func validateCodexHomeAliasRepair(existing *threadBindingRecord, registration bindingRegistration) error {
	old, next := strings.TrimSpace(existing.CodexHome), strings.TrimSpace(registration.CodexHome)
	if old == "" || next == "" || old == next {
		return nil
	}
	if registration.CodexInstanceKey == "" || registration.CodexModelProvider == "" {
		return fmt.Errorf("agent %q codex home is immutable (existing=%q, new=%q): complete incoming codex identity is required", registration.AgentID, old, next)
	}
	if filepath.IsAbs(next) && filepath.Clean(old) == next {
		return nil
	}
	canonical, err := contract.CanonicalizeCodexHome(old)
	switch {
	case err != nil:
		return fmt.Errorf("agent %q codex home is immutable (existing=%q, new=%q): canonicalize existing home: %w", registration.AgentID, old, next, err)
	case canonical != next:
		return fmt.Errorf("agent %q codex home is immutable (existing=%q, new=%q)", registration.AgentID, old, next)
	default:
		return nil
	}
}

// rollbackThreadBinding 回滚本次启动流程新写入或更新的 binding。
// 原先不存在则删除新记录；原先存在则恢复快照，ctx 已取消时改用后台 context 完成补偿。
func (s *service) rollbackThreadBinding(ctx context.Context, outcome bindingWriteOutcome) error {
	store := s.threadBindingStorePort()
	if store == nil || !outcome.Persisted {
		return nil
	}
	if ctx == nil || ctx.Err() != nil {
		ctx = context.Background()
	}
	if outcome.Previous == nil {
		return store.DeleteByAgentID(ctx, outcome.AgentID)
	}
	return store.Upsert(ctx, newBindingUpsertParams(threadBindingRecord{
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
		UpdatedAt:        time.Now().UnixMilli(),
	}))
}
func cloneBinding(binding *threadBindingRecord) *threadBindingRecord {
	if binding == nil {
		return nil
	}
	copy := *binding
	return &copy
}

// lookupPersistedAgentID 从 thread store 查找已经持久化的 agent ID。
// thread 不存在时返回带 ErrNotFound 的错误，供恢复路径区分缺记录和存储故障。
func (s *service) lookupPersistedAgentID(ctx context.Context, threadID string) (string, bool, error) {
	store := s.threadConfigStorePort()
	if store == nil {
		return "", false, nil
	}
	thread, err := store.GetByThreadID(ctx, threadID)
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
	store  threadBindingStorePort
	logger *slog.Logger
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
	if err := r.store.UpdateProviderThreadID(ctx, threadBindingProviderThreadIDUpdate{AgentID: agentID, ProviderThreadID: "", UpdatedAt: time.Now().UnixMilli()}); err != nil {
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
	updatedAt := time.Now().UnixMilli()
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
	return r.store.UpdateSessionUUID(ctx, threadBindingSessionUUIDUpdate{AgentID: agentID, SessionUUID: sessionUUID, UpdatedAt: updatedAt})
}

// recordProviderThreadID 记录provider线程ID。
func (r *bindingRecoveryReporter) recordProviderThreadID(ctx context.Context, binding *threadBindingRecord, agentID, sessionUUID string, updatedAt int64) error {
	current := strings.TrimSpace(binding.ProviderThreadID)
	if current != "" && current != agentID && identifier.LooksLikeUUID(current) {
		return nil
	}
	if !bindingRecordHasProviderHistoryForUUID(binding, sessionUUID) {
		if r.logger != nil {
			fields := []any{"agent_id", agentID, "session_uuid", sessionUUID}
			fields = append(fields, platformshared.SafePathLogFields("rollout_path", binding.RolloutPath)...)
			r.logger.Info("thread: provider session uuid is not recoverable", fields...)
		}
		return nil
	}
	return r.store.UpdateProviderThreadID(ctx, threadBindingProviderThreadIDUpdate{AgentID: agentID, ProviderThreadID: sessionUUID, UpdatedAt: updatedAt})
}
