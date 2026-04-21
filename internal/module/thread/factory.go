package thread

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
	shareddto "github.com/anthropic-ai/super-agent-v3/internal/dto/shared"
	threaddto "github.com/anthropic-ai/super-agent-v3/internal/dto/thread"
	platformdb "github.com/anthropic-ai/super-agent-v3/internal/platform/db"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/shared"
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
	ParentAgentID     string
	AgentType         string
	AgentMemoryScope  string
	Provider          string
	CWD               string
	Model             string
	Name              string
	Prompt            string
	RolloutPath       string
	SessionUUID       string
	ConfigOverride    json.RawMessage
	CreatedAt         int64
	AgentKey          string
	PromptVersionID   *int64
	PendingLaunch     bool
}

func newThreadState(kind threadStateKind, fields threadStateFields) threadState {
	displayName := strings.TrimSpace(shared.FirstNonEmpty(fields.Name, fields.Prompt))
	state := threadState{
		OwnerThreadID:    fields.OwnerThreadID,
		AgentID:          fields.AgentID,
		ParentAgentID:    strings.TrimSpace(fields.ParentAgentID),
		AgentType:        strings.TrimSpace(fields.AgentType),
		AgentMemoryScope: strings.TrimSpace(fields.AgentMemoryScope),
		Provider:         fields.Provider,
		CWD:              fields.CWD,
		Model:            fields.Model,
		Name:             displayName,
		Prompt:           displayName,
	}
	switch kind {
	case threadStateStartKind:
		state.PublicThreadID = shared.FirstNonEmpty(fields.PublicThreadID, fields.AgentID)
	case threadStateForkKind:
		state.PublicThreadID = shared.FirstNonEmpty(fields.PublicThreadID, fields.ProviderThreadID, fields.AgentID)
	default:
		state.PublicThreadID = shared.FirstNonEmpty(fields.PublicThreadID, fields.RequestedThreadID, fields.AgentID)
	}
	// Keep provider_thread_id as-is — empty when the real UUID is not
	// yet known (e.g. Claude resolves it asynchronously after launch).
	state.ProviderThreadID = strings.TrimSpace(fields.ProviderThreadID)
	state.RolloutPath = fields.RolloutPath
	state.SessionUUID = fields.SessionUUID
	state.ConfigOverride = shared.CloneRawMessage(fields.ConfigOverride)
	state.CreatedAt = firstNonZero(fields.CreatedAt)
	state.AgentKey = strings.TrimSpace(fields.AgentKey)
	state.PromptVersionID = fields.PromptVersionID
	state.PendingLaunch = fields.PendingLaunch
	return state
}

func newThreadUpsertParams(thread threadstore.Thread) threadstore.UpsertParams {
	return threadstore.UpsertParams{
		ThreadID:         strings.TrimSpace(thread.ThreadID),
		Prompt:           strings.TrimSpace(thread.Prompt),
		Model:            strings.TrimSpace(thread.Model),
		Cwd:              strings.TrimSpace(thread.Cwd),
		Status:           strings.TrimSpace(thread.Status),
		Port:             thread.Port,
		PID:              thread.PID,
		CreatedAt:        thread.CreatedAt,
		UpdatedAt:        thread.UpdatedAt,
		OwnerThreadID:    strings.TrimSpace(thread.OwnerThreadID),
		ParentAgentID:    strings.TrimSpace(thread.ParentAgentID),
		AgentType:        strings.TrimSpace(thread.AgentType),
		AgentMemoryScope: strings.TrimSpace(thread.AgentMemoryScope),
		ConfigOverride:   thread.ConfigOverride,
		AgentKey:         strings.TrimSpace(thread.AgentKey),
		PromptVersionID:  thread.PromptVersionID,
		PendingLaunch:    thread.PendingLaunch,
	}
}

func newBindingUpsertParams(binding bindingstore.Binding) bindingstore.UpsertParams {
	return bindingstore.UpsertParams{
		AgentID:          strings.TrimSpace(binding.AgentID),
		Provider:         strings.TrimSpace(binding.Provider),
		ProviderThreadID: strings.TrimSpace(binding.ProviderThreadID),
		CodexThreadID:    strings.TrimSpace(binding.CodexThreadID),
		RolloutPath:      strings.TrimSpace(binding.RolloutPath),
		SessionUUID:      strings.TrimSpace(binding.SessionUUID),
		Cwd:              strings.TrimSpace(binding.Cwd),
		ParentAgentID:    strings.TrimSpace(binding.ParentAgentID),
		AgentType:        strings.TrimSpace(binding.AgentType),
		AgentMemoryScope: strings.TrimSpace(binding.AgentMemoryScope),
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
	threadEventLaunchedKind     threadEventKind = "launched"
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
	header := shareddto.EventHeader{Timestamp: time.Now()}
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
			ProviderThreadID: strings.TrimSpace(state.ProviderThreadID),
			CWD:              strings.TrimSpace(state.CWD),
			Model:            strings.TrimSpace(state.Model),
			Name:             strings.TrimSpace(state.Name),
			PendingLaunch:    state.PendingLaunch,
		}
	case threadEventLaunchedKind:
		state := fields.State
		return threaddto.Launched{
			EventHeader:      header,
			ThreadID:         threadID,
			AgentID:          strings.TrimSpace(state.AgentID),
			Provider:         strings.TrimSpace(state.Provider),
			ProviderThreadID: strings.TrimSpace(state.ProviderThreadID),
			CWD:              strings.TrimSpace(state.CWD),
			Model:            strings.TrimSpace(state.Model),
			Name:             strings.TrimSpace(state.Name),
			AgentKey:         strings.TrimSpace(state.AgentKey),
			PromptVersionID:  state.PromptVersionID,
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
	Model       string         `json:"model,omitempty"`
	Effort      string         `json:"effort,omitempty"`
	Approvals   string         `json:"approvals,omitempty"`
	Personality string         `json:"personality,omitempty"`
	Runtime     map[string]any `json:"runtime,omitempty"`
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
	stored := decodeStoredThreadConfig(offlineThreadConfigRaw(thread))
	provider := offlineThreadProvider(binding)
	return offlineConfigSnapshot{
		Config: dto.ThreadConfig{
			ThreadID:               id,
			Provider:               provider,
			SupportsThreadOverride: supportsThreadOverride(provider),
			Override: dto.ThreadConfigValues{
				Model:     stored.Model,
				Effort:    stored.Effort,
				Approvals: stored.Approvals,
			},
			Effective: dto.ThreadConfigValues{
				Model:     shared.FirstNonEmpty(stored.Model, offlineThreadModel(thread)),
				Effort:    strings.TrimSpace(stored.Effort),
				Approvals: strings.TrimSpace(stored.Approvals),
			},
		},
		Runtime: buildOfflineRuntimeConfig(stored, thread),
	}, nil
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
	cfg = mergeRuntimeConfig(cfg, shared.CloneRuntimeConfigMap(stored.Runtime))
	if value := strings.TrimSpace(stored.Approvals); value != "" {
		cfg["approvalPolicy"] = value
	}
	if value := strings.TrimSpace(stored.Personality); value != "" {
		cfg["personality"] = value
	}
	if model := shared.FirstNonEmpty(stored.Model, offlineThreadModel(thread)); model != "" {
		cfg["model"] = model
	}
	return cfg
}

func offlineThreadProvider(binding *bindingstore.Binding) string {
	if binding == nil {
		return offlineProvider
	}
	return shared.FirstNonEmpty(binding.Provider, offlineProvider)
}

func supportsThreadOverride(provider string) bool {
	switch strings.ToLower(strings.TrimSpace(provider)) {
	case offlineProvider, "claude":
		return true
	default:
		return false
	}
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
	_ dto.ThreadConfig,
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
	if patch.Model != nil {
		thread.Model = strings.TrimSpace(*patch.Model)
	}
	thread.ConfigOverride = raw
	thread.UpdatedAt = time.Now().Unix()
	return s.upsertThread(ctx, *thread)
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

func applyThreadConfigReturnPatch(cfg dto.ThreadConfig, patch dto.ThreadConfigPatch) dto.ThreadConfig {
	if patch.Model != nil {
		cfg.Override.Model = threadConfigPatchValue(patch.Model)
	}
	if patch.Effort != nil {
		cfg.Override.Effort = threadConfigPatchValue(patch.Effort)
	}
	return cfg
}

func (s *service) emitThreadModelUpdated(threadID string, model *string) {
	if s == nil || s.emitUpdated == nil || model == nil {
		return
	}
	s.emitUpdated(threaddto.Updated{EventHeader: shareddto.EventHeader{Timestamp: time.Now()}, ThreadID: strings.TrimSpace(threadID), Model: model})
}

func (s *service) normalizeThreadConfig(
	ctx context.Context,
	threadID string,
	binding *bindingstore.Binding,
	cfg dto.ThreadConfig,
) dto.ThreadConfig {
	cfg.ThreadID = shared.FirstNonEmpty(strings.TrimSpace(cfg.ThreadID), strings.TrimSpace(threadID))
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
