package thread

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"time"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
	shareddto "github.com/anthropic-ai/super-agent-v3/internal/dto/shared"
	threaddto "github.com/anthropic-ai/super-agent-v3/internal/dto/thread"
	platformbus "github.com/anthropic-ai/super-agent-v3/internal/platform/bus"
	bindingstore "github.com/anthropic-ai/super-agent-v3/internal/store/binding"
	threadstore "github.com/anthropic-ai/super-agent-v3/internal/store/thread"
	"github.com/anthropic-ai/super-agent-v3/internal/util"
	"github.com/anthropic-ai/super-agent-v3/internal/util/clone"
	"github.com/kelindar/event"
)

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
	// Provider is stashed only by startPendingThread so that SpawnIfNeeded
	// can restore the caller's provider choice on the first turn. The eager
	// path never reads Provider back out of stored config — it comes from
	// agent_provider_binding.Provider there — so leaving this unset on eager
	// rows is harmless.
	Provider string `json:"provider,omitempty"`
	// PromptKey is stashed only by startPendingThread so that SpawnIfNeeded
	// can re-pin the SystemPromptPage "set as launch prompt" preference when
	// it lazily reconstructs the StartRequest. Without this, defer_spawn
	// loses the explicit prompt_key pin and the router silently degrades to
	// the default persona on the first turn.
	PromptKey string `json:"prompt_key,omitempty"`
	// UseClassifier is stashed by startPendingThread for the same reason as
	// PromptKey: defer_spawn strands the opt-in flag otherwise, and the
	// classifier would never run on blank-thread first turns (which is the
	// whole point).
	UseClassifier bool `json:"use_classifier,omitempty"`
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
		return offlineConfigSnapshot{}, contract.ErrNotFound
	}
	stored := decodeStoredThreadConfig(offlineThreadConfigRaw(thread))
	provider := offlineThreadProvider(binding)
	provider = util.FirstNonEmpty(stored.Provider, provider)
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
				Model:     util.FirstNonEmpty(stored.Model, offlineThreadModel(thread)),
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
	case contract.IsNotFound(err):
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
	cfg = mergeRuntimeConfig(cfg, clone.RuntimeConfigMap(stored.Runtime))
	if value := strings.TrimSpace(stored.Approvals); value != "" {
		cfg["approvalPolicy"] = value
	}
	if value := strings.TrimSpace(stored.Personality); value != "" {
		cfg["personality"] = value
	}
	if model := util.FirstNonEmpty(stored.Model, offlineThreadModel(thread)); model != "" {
		cfg["model"] = model
	}
	return cfg
}

func offlineThreadProvider(binding *bindingstore.Binding) string {
	if binding == nil {
		return offlineProvider
	}
	return util.FirstNonEmpty(binding.Provider, offlineProvider)
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
	trimmed := sanitizeConfigStringArtifact(*value)
	return &trimmed
}

func threadConfigPatchValue(value *string) string {
	if value == nil {
		return ""
	}
	return sanitizeConfigStringArtifact(*value)
}

func sanitizeConfigStringArtifact(value string) string {
	value = strings.TrimSpace(value)
	switch strings.ToLower(value) {
	case "", "[object object]", "undefined", "null":
		return ""
	default:
		return value
	}
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

// emitThreadPromotedTask publishes a thread/updated event with no model /
// name payload purely to make the uistate projector rerun
// refreshThreadPatchLocked for this thread. The refresh re-evaluates
// applyTaskRuntimeToThreadRuntime, which now finds taskId / handoffFile /
// taskTitle in the persisted runtime config and pushes them into
// agentRuntimeById on the frontend. Phase 2.1: without this nudge the
// frontend would not see the new task fields until the next natural
// thread/updated or sidebar refresh.
func (s *service) emitThreadPromotedTask(threadID string) {
	if s == nil || s.emitUpdated == nil {
		return
	}
	tid := strings.TrimSpace(threadID)
	if tid == "" {
		return
	}
	s.emitUpdated(threaddto.Updated{EventHeader: shareddto.EventHeader{Timestamp: time.Now()}, ThreadID: tid})
}

func (s *service) normalizeThreadConfig(
	ctx context.Context,
	threadID string,
	binding *bindingstore.Binding,
	cfg dto.ThreadConfig,
) dto.ThreadConfig {
	cfg.ThreadID = util.FirstNonEmpty(strings.TrimSpace(cfg.ThreadID), strings.TrimSpace(threadID))
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

// NewThreadSubscribers declares thread bus subscriptions for BusModule.
func NewThreadSubscribers(svc *service) platformbus.SubscriberResult {
	return platformbus.SubscriberResult{
		Spec: contract.SubscriberSpec{
			EventType:     "thread.core",
			HandlerSymbol: "thread.registerThreadSubscriptions",
			OwnerModule:   "thread",
			CancelOwner:   "bus.SubscriberGroup",
			ShutdownClass: "bus-subscriber",
			TestFixtureID: "thread-subscribers",
			Register: func(dispatcher *event.Dispatcher) context.CancelFunc {
				if svc != nil && dispatcher != nil {
					svc.bindDispatcher(dispatcher)
				}
				cancels := registerThreadSubscriptions(svc)
				var once sync.Once
				return func() {
					once.Do(func() {
						for _, cancel := range cancels {
							if cancel != nil {
								cancel()
							}
						}
					})
				}
			},
		},
	}
}

func threadBusWorkersAsRunner(svc *service) contract.Runner {
	return contract.AsRunner(&threadBusWorkerRunner{svc: svc})
}

type threadBusWorkerRunner struct {
	svc *service
}

func (r *threadBusWorkerRunner) Start() {
	if r == nil || r.svc == nil {
		return
	}
	r.svc.startBusWorkers()
}

func (r *threadBusWorkerRunner) Stop(ctx context.Context) error {
	if r == nil || r.svc == nil {
		return nil
	}
	r.svc.stopBusWorkers(ctx)
	return nil
}
