package thread

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
	"github.com/anthropic-ai/super-agent-v3/internal/dto/shared"
	threaddto "github.com/anthropic-ai/super-agent-v3/internal/dto/thread"
	platformdb "github.com/anthropic-ai/super-agent-v3/internal/platform/db"
	bindingstore "github.com/anthropic-ai/super-agent-v3/internal/store/binding"
	threadstore "github.com/anthropic-ai/super-agent-v3/internal/store/thread"
)

type threadStateKind string

const (
	threadStateStartKind   threadStateKind = "start"
	threadStateResumeKind  threadStateKind = "resume"
	threadStateForkKind    threadStateKind = "fork"
	threadStateRecoverKind threadStateKind = "recover"
)

type threadStateFields struct {
	RequestedThreadID string
	PublicThreadID    string
	ProviderThreadID  string
	OwnerThreadID     string
	AgentID           string
	Provider          string
	CWD               string
	Model             string
	Prompt            string
	CreatedAt         int64
}

func newThreadState(kind threadStateKind, fields threadStateFields) threadState {
	state := threadState{
		OwnerThreadID: fields.OwnerThreadID,
		AgentID:       fields.AgentID,
		Provider:      fields.Provider,
		CWD:           fields.CWD,
		Model:         fields.Model,
		Prompt:        fields.Prompt,
	}
	switch kind {
	case threadStateStartKind:
		state.PublicThreadID = firstNonEmpty(fields.PublicThreadID, fields.AgentID)
	case threadStateForkKind:
		state.PublicThreadID = firstNonEmpty(fields.PublicThreadID, fields.ProviderThreadID, fields.AgentID)
	default:
		state.PublicThreadID = firstNonEmpty(fields.PublicThreadID, fields.RequestedThreadID, fields.AgentID)
	}
	state.ProviderThreadID = resolveProviderThreadID(
		fields.ProviderThreadID,
		state.PublicThreadID,
		fields.RequestedThreadID,
		fields.AgentID,
	)
	state.CreatedAt = firstNonZero(fields.CreatedAt)
	return state
}

func newThreadUpsertParams(thread threadstore.Thread) threadstore.UpsertParams {
	return threadstore.UpsertParams{
		ThreadID:       strings.TrimSpace(thread.ThreadID),
		Prompt:         strings.TrimSpace(thread.Prompt),
		Model:          strings.TrimSpace(thread.Model),
		Cwd:            strings.TrimSpace(thread.Cwd),
		Status:         strings.TrimSpace(thread.Status),
		Port:           thread.Port,
		PID:            thread.PID,
		CreatedAt:      thread.CreatedAt,
		UpdatedAt:      thread.UpdatedAt,
		OwnerThreadID:  strings.TrimSpace(thread.OwnerThreadID),
		ConfigOverride: thread.ConfigOverride,
	}
}

func newBindingUpsertParams(binding bindingstore.Binding) bindingstore.UpsertParams {
	return bindingstore.UpsertParams{
		AgentID:          strings.TrimSpace(binding.AgentID),
		Provider:         strings.TrimSpace(binding.Provider),
		ProviderThreadID: strings.TrimSpace(binding.ProviderThreadID),
		CodexThreadID:    strings.TrimSpace(binding.CodexThreadID),
		RolloutPath:      strings.TrimSpace(binding.RolloutPath),
		Cwd:              strings.TrimSpace(binding.Cwd),
		CreatedAt:        binding.CreatedAt,
		UpdatedAt:        binding.UpdatedAt,
	}
}

type threadEventKind string

const (
	threadEventStartedKind      threadEventKind = "started"
	threadEventStoppedKind      threadEventKind = "stopped"
	threadEventMessagesPageKind threadEventKind = "messages_page"
	threadEventCompactedKind    threadEventKind = "compacted"
)

type threadEventFields struct {
	State        threadState
	AgentID      string
	Status       string
	Reason       string
	Command      string
	TotalCount   int
	Pages        int
	BeforeTokens int
	AfterTokens  int
	Compacted    bool
	Estimated    bool
}

func newThreadEvent(kind threadEventKind, threadID string, fields threadEventFields) any {
	header := shared.EventHeader{Timestamp: time.Now()}
	threadID = strings.TrimSpace(threadID)
	if threadID == "" {
		return nil
	}
	switch kind {
	case threadEventStartedKind:
		state := fields.State
		return threaddto.Started{
			EventHeader:      header,
			ThreadID:         threadID,
			AgentID:          strings.TrimSpace(state.AgentID),
			Provider:         strings.TrimSpace(state.Provider),
			ProviderThreadID: strings.TrimSpace(resolveProviderThreadID(state.ProviderThreadID, state.PublicThreadID)),
			CWD:              strings.TrimSpace(state.CWD),
			Model:            strings.TrimSpace(state.Model),
		}
	case threadEventStoppedKind:
		return threaddto.Stopped{
			EventHeader: header,
			ThreadID:    threadID,
			AgentID:     strings.TrimSpace(fields.AgentID),
			Status:      strings.TrimSpace(fields.Status),
			Reason:      strings.TrimSpace(fields.Reason),
		}
	case threadEventMessagesPageKind:
		return threaddto.MessagesPage{
			EventHeader: header,
			ThreadID:    threadID,
			TotalCount:  fields.TotalCount,
			Pages:       fields.Pages,
		}
	case threadEventCompactedKind:
		return threaddto.Compacted{
			EventHeader:  header,
			ThreadID:     threadID,
			Command:      strings.TrimSpace(fields.Command),
			BeforeTokens: fields.BeforeTokens,
			AfterTokens:  fields.AfterTokens,
			Compacted:    fields.Compacted,
			Estimated:    fields.Estimated,
		}
	default:
		return nil
	}
}

const (
	offlineApprovalPolicy = "on-failure"
	offlineProvider       = "codex"
	offlineToolMode       = "legacy"
	offlineToolProvider   = "openai_compatible"
)

type storedThreadConfig struct {
	Model       string `json:"model,omitempty"`
	Effort      string `json:"effort,omitempty"`
	Approvals   string `json:"approvals,omitempty"`
	Personality string `json:"personality,omitempty"`
}

type offlineConfigSnapshot struct {
	Config  dto.ThreadConfig
	Runtime map[string]any
}

func (s *service) buildOfflineConfig(
	ctx context.Context,
	threadID string,
	binding *bindingstore.Binding,
) (offlineConfigSnapshot, error) {
	id, err := normalizeThreadID(threadID)
	if err != nil {
		return offlineConfigSnapshot{}, err
	}
	thread, err := s.loadOfflineThread(ctx, id)
	if err != nil {
		return offlineConfigSnapshot{}, err
	}
	if thread == nil && binding == nil {
		return offlineConfigSnapshot{}, platformdb.ErrNotFound
	}
	return buildOfflineConfigSnapshot(id, binding, thread), nil
}

func (s *service) loadOfflineThread(
	ctx context.Context,
	threadID string,
) (*threadstore.Thread, error) {
	if s.threadStore == nil {
		return nil, nil
	}
	thread, err := s.threadStore.GetByThreadID(ctx, threadID)
	switch {
	case err == nil:
		return thread, nil
	case platformdb.IsNotFound(err):
		return nil, nil
	default:
		return nil, err
	}
}

func buildOfflineConfigSnapshot(
	threadID string,
	binding *bindingstore.Binding,
	thread *threadstore.Thread,
) offlineConfigSnapshot {
	stored := decodeStoredThreadConfig(offlineThreadConfigRaw(thread))
	provider := offlineThreadProvider(binding)
	return offlineConfigSnapshot{
		Config: dto.ThreadConfig{
			ThreadID:               threadID,
			Provider:               provider,
			SupportsThreadOverride: supportsThreadOverride(provider),
			Override: dto.ThreadConfigValues{
				Model:     stored.Model,
				Effort:    stored.Effort,
				Approvals: stored.Approvals,
			},
			Effective: dto.ThreadConfigValues{
				Model:     firstNonEmpty(stored.Model, offlineThreadModel(thread)),
				Effort:    strings.TrimSpace(stored.Effort),
				Approvals: strings.TrimSpace(stored.Approvals),
			},
		},
		Runtime: buildOfflineRuntimeConfig(stored, thread),
	}
}

func buildOfflineRuntimeConfig(stored storedThreadConfig, thread *threadstore.Thread) map[string]any {
	cfg := map[string]any{
		"approvalPolicy": offlineApprovalPolicy,
		"toolRouting": map[string]any{
			"mode":                offlineToolMode,
			"routerModel":         "",
			"routerProvider":      offlineToolProvider,
			"routerBaseURL":       "",
			"routerHasAPIKey":     false,
			"confidenceThreshold": 0.65,
			"timeoutSec":          8,
		},
	}
	if value := strings.TrimSpace(stored.Approvals); value != "" {
		cfg["approvalPolicy"] = value
	}
	if value := strings.TrimSpace(stored.Personality); value != "" {
		cfg["personality"] = value
	}
	if model := firstNonEmpty(stored.Model, offlineThreadModel(thread)); model != "" {
		cfg["model"] = model
	}
	return cfg
}

func offlineThreadProvider(binding *bindingstore.Binding) string {
	if binding == nil {
		return offlineProvider
	}
	return firstNonEmpty(binding.Provider, offlineProvider)
}

func supportsThreadOverride(provider string) bool {
	return strings.EqualFold(strings.TrimSpace(provider), offlineProvider)
}

func offlineThreadModel(thread *threadstore.Thread) string {
	if thread == nil {
		return ""
	}
	return strings.TrimSpace(thread.Model)
}

func offlineThreadConfigRaw(thread *threadstore.Thread) json.RawMessage {
	if thread == nil {
		return nil
	}
	return thread.ConfigOverride
}

func decodeStoredThreadConfig(raw json.RawMessage) storedThreadConfig {
	var cfg storedThreadConfig
	if len(raw) == 0 {
		return cfg
	}
	_ = json.Unmarshal(raw, &cfg)
	return cfg
}

func encodeStoredThreadConfig(cfg storedThreadConfig) (json.RawMessage, error) {
	raw, err := json.Marshal(cfg)
	if err != nil {
		return nil, err
	}
	return json.RawMessage(raw), nil
}

func applyConfigPatch(base storedThreadConfig, patch dto.ThreadConfigPatch) storedThreadConfig {
	applyConfigValue(&base.Model, patch.Model)
	applyConfigValue(&base.Effort, patch.Effort)
	applyConfigValue(&base.Approvals, patch.Approvals)
	applyConfigValue(&base.Personality, patch.Personality)
	return base
}

func applyConfigValue(dst *string, value *string) {
	if dst == nil || value == nil {
		return
	}
	*dst = strings.TrimSpace(*value)
}

func (s *service) persistThreadConfig(
	ctx context.Context,
	threadID string,
	patch dto.ThreadConfigPatch,
	effective dto.ThreadConfig,
) error {
	if s.threadStore == nil {
		return nil
	}
	thread, err := s.getThread(ctx, threadID)
	if err != nil {
		return err
	}
	stored := applyConfigPatch(decodeStoredThreadConfig(thread.ConfigOverride), patch)
	raw, err := encodeStoredThreadConfig(stored)
	if err != nil {
		return err
	}
	thread.Model = firstNonEmpty(effective.Effective.Model, effective.Override.Model)
	thread.ConfigOverride = raw
	thread.UpdatedAt = time.Now().Unix()
	return s.upsertThread(ctx, *thread)
}

func buildStartSessionConfig(req StartRequest) map[string]any {
	cfg := map[string]any{}
	putConfigString(cfg, "approvalPolicy", req.ApprovalPolicy)
	putConfigString(cfg, "approval_policy", req.ApprovalPolicy)
	putConfigString(cfg, "approvals", req.ApprovalPolicy)
	putConfigString(cfg, "modelProvider", req.ModelProvider)
	putConfigString(cfg, "developerInstructions", req.DeveloperInstructions)
	putConfigString(cfg, "developer_instructions", req.DeveloperInstructions)
	putConfigString(cfg, "summary", req.Summary)
	putConfigString(cfg, "effort", req.Effort)
	putConfigString(cfg, "personality", req.Personality)
	putConfigJSON(cfg, "sandbox", req.Sandbox)
	for key, value := range req.Config {
		if _, exists := cfg[key]; !exists {
			cfg[key] = value
		}
	}
	if len(cfg) == 0 {
		return nil
	}
	return cfg
}

func putConfigString(cfg map[string]any, key, value string) {
	if value = strings.TrimSpace(value); value != "" {
		cfg[key] = value
	}
}

func putConfigJSON(cfg map[string]any, key string, raw json.RawMessage) {
	raw = trimRawJSON(raw)
	if len(raw) == 0 {
		return
	}
	var value any
	if err := json.Unmarshal(raw, &value); err == nil {
		cfg[key] = value
	}
}

func normalizeThreadConfigPatch(
	ctx context.Context,
	session contract.Session,
	provider string,
	patch dto.ThreadConfigPatch,
) (dto.ThreadConfigPatch, error) {
	patch.Model = trimThreadConfigPatchValue(patch.Model)
	patch.Effort = trimThreadConfigPatchValue(patch.Effort)
	patch.Personality = trimThreadConfigPatchValue(patch.Personality)
	patch.Approvals = trimThreadConfigPatchValue(patch.Approvals)
	if model := threadConfigPatchValue(patch.Model); model != "" {
		validated, err := validateModelName(model)
		if err != nil {
			return dto.ThreadConfigPatch{}, err
		}
		patch.Model = &validated
		if err := ensureAllowedModel(ctx, session, validated, provider); err != nil {
			return dto.ThreadConfigPatch{}, err
		}
	}
	if err := validateThreadConfigEffort(threadConfigPatchValue(patch.Effort)); err != nil {
		return dto.ThreadConfigPatch{}, err
	}
	return patch, nil
}

func threadConfigPatchNoop(patch dto.ThreadConfigPatch) bool {
	return patch.Model == nil &&
		patch.Effort == nil &&
		patch.Personality == nil &&
		patch.Approvals == nil
}

func wrapThreadConfigPatchError(err error, provider string, patch dto.ThreadConfigPatch) error {
	if patch.Model != nil {
		return wrapFriendlyCapabilityError(
			err,
			dto.CapModelSwitch,
			provider,
			errRuntimeModelSwitchUnsupported,
		)
	}
	return err
}

func trimThreadConfigPatchValue(value *string) *string {
	if value == nil {
		return nil
	}
	trimmed := strings.TrimSpace(*value)
	return &trimmed
}

func threadConfigPatchValue(value *string) string {
	if value == nil {
		return ""
	}
	return strings.TrimSpace(*value)
}

func (s *service) normalizeThreadConfig(
	ctx context.Context,
	threadID string,
	binding *bindingstore.Binding,
	cfg dto.ThreadConfig,
) dto.ThreadConfig {
	cfg.ThreadID = firstNonEmpty(strings.TrimSpace(cfg.ThreadID), strings.TrimSpace(threadID))
	if binding != nil && strings.TrimSpace(cfg.Provider) == "" {
		cfg.Provider = strings.TrimSpace(binding.Provider)
	}
	if !cfg.SupportsThreadOverride && supportsThreadOverride(cfg.Provider) {
		cfg.SupportsThreadOverride = true
	}
	if cfg.Effective.Model == "" {
		cfg.Effective.Model = s.storedThreadModel(ctx, threadID)
	}
	return cfg
}

func decodeLegacyParams[T any](raw []byte, target *T, legacyFn func([]byte, *T) error) error {
	if err := json.Unmarshal(raw, target); err != nil {
		return err
	}
	if legacyFn == nil {
		return nil
	}
	return legacyFn(raw, target)
}

func (s *service) resolveBindingChain(ctx context.Context, threadID string) (*bindingstore.Binding, error) {
	binding, err := s.bindingByAgentID(ctx, threadID)
	if binding != nil || err == nil {
		return binding, err
	}
	for _, lookup := range []func(context.Context, string) *bindingstore.Binding{
		s.bindingByPersistedThreadAgent,
		s.bindingByRememberedThreadAgent,
		s.bindingByProviderThreadID,
	} {
		if binding := lookup(ctx, threadID); binding != nil {
			return binding, nil
		}
	}
	return nil, err
}

func (s *service) bindingByAgentID(ctx context.Context, agentID string) (*bindingstore.Binding, error) {
	binding, err := s.bindingStore.GetByAgentID(ctx, agentID)
	if err == nil {
		s.rememberBinding(binding)
	}
	return binding, err
}

func (s *service) bindingByPersistedThreadAgent(ctx context.Context, threadID string) *bindingstore.Binding {
	agentID := s.lookupPersistedAgentID(ctx, threadID)
	return s.bindingByResolvedAgentID(ctx, threadID, agentID)
}

func (s *service) bindingByRememberedThreadAgent(ctx context.Context, threadID string) *bindingstore.Binding {
	agentID := s.lookupThreadAgent(threadID)
	return s.bindingByResolvedAgentID(ctx, threadID, agentID)
}

func (s *service) bindingByResolvedAgentID(ctx context.Context, threadID, agentID string) *bindingstore.Binding {
	if agentID == "" || agentID == threadID {
		return nil
	}
	binding, err := s.bindingByAgentID(ctx, agentID)
	if err != nil {
		return nil
	}
	return binding
}

func (s *service) bindingByProviderThreadID(ctx context.Context, threadID string) *bindingstore.Binding {
	for _, provider := range []string{"codex", "claude"} {
		binding, err := s.bindingStore.GetByProviderThread(ctx, provider, threadID)
		if err == nil {
			s.rememberBinding(binding)
			return binding
		}
	}
	return nil
}
