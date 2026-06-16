package thread

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
	shareddto "github.com/anthropic-ai/super-agent-v3/internal/dto/shared"
	threaddto "github.com/anthropic-ai/super-agent-v3/internal/dto/thread"
	platformbus "github.com/anthropic-ai/super-agent-v3/internal/platform/bus"
	platformdb "github.com/anthropic-ai/super-agent-v3/internal/platform/db"
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
	// AgentKey is also stashed for pending-launch intake threads. The thread
	// can have a visible name and a pinned agent persona before the user sends
	// the first real requirement; SpawnIfNeeded restores the pin for routing.
	AgentKey string `json:"agent_key,omitempty"`
	// ToolSurfaceMode preserves the caller-selected chat/auto/agent surface
	// through pending_launch so the lazy first turn does not re-enable tools.
	ToolSurfaceMode string `json:"tool_surface_mode,omitempty"`
}

type offlineConfigSnapshot struct {
	Config  dto.ThreadConfig
	Runtime map[string]any
}

// buildOfflineConfig 构建offline配置。
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
	stored, err := decodeStoredThreadConfig(offlineThreadConfigRaw(thread))
	if err != nil {
		return offlineConfigSnapshot{}, err
	}
	provider := util.FirstNonEmpty(stored.Provider, offlineThreadProvider(binding))
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
		Runtime: buildOfflineRuntimeConfig(stored, thread, binding),
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

// buildOfflineRuntimeConfig 构建offline运行时配置。
func buildOfflineRuntimeConfig(stored storedThreadConfig, thread *threadstore.Thread, binding *bindingstore.Binding) map[string]any {
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
	if thread != nil && strings.TrimSpace(thread.Cwd) != "" {
		cfg["cwd"] = strings.TrimSpace(thread.Cwd)
	} else if binding != nil && strings.TrimSpace(binding.Cwd) != "" {
		cfg["cwd"] = strings.TrimSpace(binding.Cwd)
	}
	if value := strings.TrimSpace(stored.Approvals); value != "" {
		cfg["approvalPolicy"] = value
	}
	if value := strings.TrimSpace(stored.Personality); value != "" {
		cfg["personality"] = value
	}
	if value := strings.TrimSpace(stored.PromptKey); value != "" {
		cfg["promptKey"] = value
		cfg["prompt_key"] = value
	}
	if model := util.FirstNonEmpty(stored.Model, offlineThreadModel(thread)); model != "" {
		cfg["model"] = model
	}
	return cfg
}

func resolveResumeCodexDisabledNativeTools(current []string, runtime map[string]any) []string {
	if len(current) > 0 {
		return append([]string(nil), current...)
	}
	return codexDisabledNativeToolsFromRuntime(runtime)
}

func codexDisabledNativeToolsFromRuntime(runtime map[string]any) []string {
	if len(runtime) == 0 {
		return nil
	}
	return cleanResumeStringList(runtime["codexDisabledNativeTools"])
}

// cleanResumeStringList 处理clean恢复stringlist。
func cleanResumeStringList(value any) []string {
	var raw []string
	switch typed := value.(type) {
	case []string:
		raw = typed
	case []any:
		raw = make([]string, 0, len(typed))
		for _, item := range typed {
			if text, ok := item.(string); ok {
				raw = append(raw, text)
			}
		}
	default:
		return nil
	}
	seen := make(map[string]struct{}, len(raw))
	for _, item := range raw {
		text := strings.TrimSpace(item)
		if text != "" {
			seen[text] = struct{}{}
		}
	}
	out := make([]string, 0, len(seen))
	for item := range seen {
		out = append(out, item)
	}
	sort.Strings(out)
	return out
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

func decodeStoredThreadConfig(raw json.RawMessage) (storedThreadConfig, error) {
	var cfg storedThreadConfig
	if len(raw) == 0 {
		return cfg, nil
	}
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return storedThreadConfig{}, fmt.Errorf("decode thread config override: %w", err)
	}
	return cfg, nil
}

func encodeStoredThreadConfig(cfg storedThreadConfig) (json.RawMessage, error) {
	raw, err := json.Marshal(cfg)
	if err != nil {
		return nil, err
	}
	return json.RawMessage(raw), nil
}

// canonicalizeResumeStoredThreadConfig 在 resume 写回前同步 Codex 身份；runtime 显式字段优先。
func canonicalizeResumeStoredThreadConfig(provider string, raw json.RawMessage, home, instanceKey, modelProvider string, resolved bool, runtimeIdentity map[string]any, hasRuntimeIdentity bool) (json.RawMessage, contract.CodexIdentity, bool, error) {
	if !isCodexResumeProvider(provider) {
		return clone.RawMessage(raw), contract.CodexIdentity{}, false, nil
	}
	identity, ok, err := resolveResumeWritebackCodexIdentity(provider, home, instanceKey, modelProvider, resolved, runtimeIdentity, hasRuntimeIdentity)
	if err != nil || !ok {
		return clone.RawMessage(raw), identity, ok, err
	}
	cfg, err := decodeStoredThreadConfig(raw)
	if err != nil {
		return resumeConfigDecodeFailed(err)
	}
	if cfg.Runtime == nil {
		cfg.Runtime = map[string]any{}
	}
	cfg.Runtime[contract.CodexHomeKey], cfg.Runtime[contract.CodexInstanceKeyKey], cfg.Runtime[contract.CodexModelProviderKey] = identity.Home, identity.InstanceKey, identity.ModelProvider
	raw, err = encodeStoredThreadConfig(cfg)
	return raw, identity, true, err
}

func resumeConfigDecodeFailed(err error) (json.RawMessage, contract.CodexIdentity, bool, error) {
	return nil, contract.CodexIdentity{}, false, err
}

// resolveResumeWritebackCodexIdentity 选择 resume 写回用的 Codex 身份；runtime 一旦显式出现就不再回退。
func resolveResumeWritebackCodexIdentity(provider, home, instanceKey, modelProvider string, resolved bool, runtimeIdentity map[string]any, hasRuntimeIdentity bool) (contract.CodexIdentity, bool, error) {
	if hasRuntimeIdentity {
		identity, err := contract.ResolveCodexIdentity(runtimeIdentity)
		return identity, err == nil, err
	}
	if strings.TrimSpace(home) == "" && strings.TrimSpace(instanceKey) == "" && strings.TrimSpace(modelProvider) == "" {
		return contract.CodexIdentity{}, false, nil
	}
	if resolved {
		return contract.CodexIdentity{Home: home, InstanceKey: instanceKey, ModelProvider: modelProvider}, true, nil
	}
	identity, ok, err := canonicalizeCodexIdentityFields(provider, home, instanceKey, modelProvider)
	if err == nil && !ok {
		err = collectResumeCodexIdentityValues(ResumeRequest{CodexHome: home, CodexInstanceKey: instanceKey, CodexModelProvider: modelProvider}, nil).validateCompleteStrings()
	}
	return identity, ok, err
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

// persistThreadConfig 持久化线程配置。
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
	stored, err := decodeStoredThreadConfig(thread.ConfigOverride)
	if err != nil {
		return err
	}
	stored = applyConfigPatch(stored, patch)
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

// normalizeThreadConfig 规范化线程配置。
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

// resolveBindingChain 解析bindingchain。
func (s *service) resolveBindingChain(ctx context.Context, threadID string) (*bindingstore.Binding, error) {
	binding, err := s.bindingStore.GetByAgentID(ctx, threadID)
	switch {
	case err == nil:
		return s.rememberBinding(binding), err
	case !platformdb.IsNotFound(err):
		return nil, err
	}
	binding, threadMissing, missingErr, lookupErr := s.bindingByPersistedOrRememberedAgent(ctx, threadID)
	if binding != nil || lookupErr != nil {
		return binding, lookupErr
	}
	if binding, lookupErr := s.bindingByProviderThreadID(ctx, threadID); binding != nil || lookupErr != nil {
		return binding, lookupErr
	}
	if threadMissing {
		return nil, missingErr
	}
	return nil, err
}

// bindingByPersistedOrRememberedAgent 按persistedremembered代理处理binding。
func (s *service) bindingByPersistedOrRememberedAgent(ctx context.Context, threadID string) (*bindingstore.Binding, bool, error, error) {
	persistedAgentID, persistedFound, missingErr := s.lookupPersistedAgentID(ctx, threadID)
	if missingErr != nil && !platformdb.IsNotFound(missingErr) {
		return nil, false, nil, missingErr
	}
	if persistedFound {
		if binding, lookupErr := s.bindingByResolvedAgentID(ctx, threadID, persistedAgentID); binding != nil || lookupErr != nil {
			return binding, false, nil, lookupErr
		}
	}
	binding, lookupErr := s.bindingByResolvedAgentID(ctx, threadID, s.lookupThreadAgent(threadID))
	if platformdb.IsNotFound(missingErr) && platformdb.IsNotFound(lookupErr) {
		lookupErr = nil
	}
	return binding, platformdb.IsNotFound(missingErr), missingErr, lookupErr
}

func (s *service) bindingByResolvedAgentID(ctx context.Context, threadID, agentID string) (*bindingstore.Binding, error) {
	if agentID == "" || agentID == threadID {
		return nil, nil
	}
	binding, err := s.bindingStore.GetByAgentID(ctx, agentID)
	switch {
	case err == nil:
		return s.rememberBinding(binding), nil
	case platformdb.IsNotFound(err):
		return nil, fmt.Errorf("thread %q binding for resolved agent_id %q not found: %w", threadID, agentID, contract.ErrNotFound)
	default:
		return nil, err
	}
}

func (s *service) bindingByProviderThreadID(ctx context.Context, threadID string) (*bindingstore.Binding, error) {
	for _, provider := range []string{"codex", "claude"} {
		binding, err := s.bindingStore.GetByProviderThread(ctx, provider, threadID)
		switch {
		case err == nil:
			return s.rememberBinding(binding), nil
		case !platformdb.IsNotFound(err):
			return nil, err
		}
	}
	return nil, nil
}

// NewThreadSubscribers declares thread bus subscriptions for BusModule.
// NewThreadSubscribers 创建线程subscribers。
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

// Start 启动线程流程。
func (r *threadBusWorkerRunner) Start() {
	if r == nil || r.svc == nil {
		return
	}
	r.svc.startBusWorkers()
}

// Stop 停止线程流程。
func (r *threadBusWorkerRunner) Stop(ctx context.Context) error {
	if r == nil || r.svc == nil {
		return nil
	}
	r.svc.stopBusWorkers(ctx)
	return nil
}
