package thread

import (
	"context"
	"encoding/json"
	"errors"
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
	// Provider 只为 pending_launch 线程落库，首轮 SpawnIfNeeded 用它恢复调用方选择的 provider。
	// 立即启动路径以 agent_provider_binding.Provider 为准，因此 eager 记录为空不会影响运行时路由。
	Provider string `json:"provider,omitempty"`
	// PromptKey 保存 pending_launch intake 时的显式 prompt_key pin。
	// 懒启动重建 StartRequest 时必须带回它，否则首轮路由会退回默认 persona。
	PromptKey string `json:"prompt_key,omitempty"`
	// AgentKey 保存 pending_launch 线程的 agent persona pin，供首轮真正启动时恢复路由目标。
	AgentKey string `json:"agent_key,omitempty"`
	// ToolSurfaceMode 保存调用方选择的 chat/auto/agent surface，避免懒启动首轮重新打开工具面。
	ToolSurfaceMode string `json:"tool_surface_mode,omitempty"`
}

type offlineConfigSnapshot struct {
	Config  dto.ThreadConfig
	Runtime map[string]any
}

// buildOfflineConfigRecord 读取持久化线程配置和 binding，拼出无活跃 session 时 UI 可展示的配置快照。
// 线程和 binding 都不存在时返回 not found；配置 JSON 损坏会直接报错，避免展示静默兜底值。
func (s *service) buildOfflineConfigRecord(
	ctx context.Context,
	threadID string,
	binding *threadBindingRecord,
) (offlineConfigSnapshot, error) {
	id, err := normalizeThreadID(threadID)
	if err != nil {
		return offlineConfigSnapshot{}, err
	}
	thread, err := s.loadOfflineThreadRecord(ctx, id)
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

func (s *service) loadOfflineThreadRecord(
	ctx context.Context,
	threadID string,
) (*threadConfigRecord, error) {
	store := s.threadConfigStorePort()
	if store == nil {
		return nil, nil
	}
	thread, err := store.GetByThreadID(ctx, threadID)
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
func buildOfflineRuntimeConfig(stored storedThreadConfig, thread *threadConfigRecord, binding *threadBindingRecord) map[string]any {
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

// cleanResumeStringList 清洗 resume runtime 中的字符串列表。
// 只接受 []string 或 JSON 解码后的 []any，元素会 trim、去空、去重并排序，避免恢复配置因重复工具名产生不稳定 diff。
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

func offlineThreadProvider(binding *threadBindingRecord) string {
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

func offlineThreadModel(thread *threadConfigRecord) string {
	if thread == nil {
		return ""
	}
	return strings.TrimSpace(thread.Model)
}

func offlineThreadConfigRaw(thread *threadConfigRecord) json.RawMessage {
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
		return nil, contract.CodexIdentity{}, false, err
	}
	if cfg.Runtime == nil {
		cfg.Runtime = map[string]any{}
	}
	cfg.Runtime[contract.CodexHomeKey], cfg.Runtime[contract.CodexInstanceKeyKey], cfg.Runtime[contract.CodexModelProviderKey] = identity.Home, identity.InstanceKey, identity.ModelProvider
	raw, err = encodeStoredThreadConfig(cfg)
	return raw, identity, true, err
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
	if s == nil || s.threadStore == nil {
		return errors.New("thread: thread store is not configured")
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
	binding *threadBindingRecord,
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

// resolveBindingChain 按多路索引恢复线程 binding。
// 顺序是直接 agent_id、持久化/进程记忆的 agent_id、provider_thread_id；只有确认线程记录也缺失时才返回原始 not found。
func (s *service) resolveBindingChainRecord(ctx context.Context, threadID string) (*threadBindingRecord, error) {
	store := s.threadBindingStorePort()
	if store == nil {
		return nil, errors.New("binding store is not configured")
	}
	binding, err := store.GetByAgentID(ctx, threadID)
	switch {
	case err == nil:
		return s.rememberBindingRecord(binding), err
	case !platformdb.IsNotFound(err):
		return nil, err
	}
	binding, threadMissing, missingErr, lookupErr := s.bindingByPersistedOrRememberedAgentRecord(ctx, threadID)
	if binding != nil || lookupErr != nil {
		return binding, lookupErr
	}
	if binding, lookupErr := s.bindingByProviderThreadIDRecord(ctx, threadID); binding != nil || lookupErr != nil {
		return binding, lookupErr
	}
	if threadMissing {
		return nil, missingErr
	}
	return nil, err
}

// bindingByPersistedOrRememberedAgent 优先用持久化 agent 绑定查找，缺失时回退到内存记忆的 agent。
func (s *service) bindingByPersistedOrRememberedAgentRecord(ctx context.Context, threadID string) (*threadBindingRecord, bool, error, error) {
	persistedAgentID, persistedFound, missingErr := s.lookupPersistedAgentID(ctx, threadID)
	if missingErr != nil && !platformdb.IsNotFound(missingErr) {
		return nil, false, nil, missingErr
	}
	if persistedFound {
		if binding, lookupErr := s.bindingByResolvedAgentIDRecord(ctx, threadID, persistedAgentID); binding != nil || lookupErr != nil {
			return binding, false, nil, lookupErr
		}
	}
	binding, lookupErr := s.bindingByResolvedAgentIDRecord(ctx, threadID, s.lookupThreadAgent(threadID))
	if platformdb.IsNotFound(missingErr) && platformdb.IsNotFound(lookupErr) {
		lookupErr = nil
	}
	return binding, platformdb.IsNotFound(missingErr), missingErr, lookupErr
}

// bindingByResolvedAgentIDRecord 用解析出的 agent id 查 binding；查不到要返回带 thread 上下文的错误。
func (s *service) bindingByResolvedAgentIDRecord(ctx context.Context, threadID, agentID string) (*threadBindingRecord, error) {
	if agentID == "" || agentID == threadID {
		return nil, nil
	}
	store := s.threadBindingStorePort()
	if store == nil {
		return nil, errors.New("binding store is not configured")
	}
	binding, err := store.GetByAgentID(ctx, agentID)
	switch {
	case err == nil:
		return s.rememberBindingRecord(binding), nil
	case platformdb.IsNotFound(err):
		return nil, fmt.Errorf("thread %q binding for resolved agent_id %q not found: %w", threadID, agentID, contract.ErrNotFound)
	default:
		return nil, err
	}
}

func (s *service) bindingByProviderThreadIDRecord(ctx context.Context, threadID string) (*threadBindingRecord, error) {
	store := s.threadBindingStorePort()
	if store == nil {
		return nil, errors.New("binding store is not configured")
	}
	for _, provider := range []string{"codex", "claude"} {
		binding, err := store.GetByProviderThread(ctx, provider, threadID)
		switch {
		case err == nil:
			return s.rememberBindingRecord(binding), nil
		case !platformdb.IsNotFound(err):
			return nil, err
		}
	}
	return nil, nil
}

func (s *service) rememberBindingRecord(binding *threadBindingRecord) *threadBindingRecord {
	if binding != nil {
		agentID := strings.TrimSpace(binding.AgentID)
		for _, tid := range []string{binding.ProviderThreadID, binding.CodexThreadID, binding.AgentID} {
			s.rememberThreadAgent(tid, agentID)
		}
	}
	return binding
}

// NewThreadSubscribers 声明 thread 模块在总线上的订阅入口。
// 订阅只注册事件处理器元数据，实际生命周期由 BusModule 的 SubscriberGroup 管理。
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

// Start 接入 thread 模块的总线后台 worker 生命周期。
// 这里只接管事件慢路径 worker 的生命周期，不创建业务 thread 或 provider session。
func (r *threadBusWorkerRunner) Start() {
	if r == nil || r.svc == nil {
		return
	}
	r.svc.startBusWorkers()
}

// Stop 收束 thread 模块的总线后台 worker 生命周期。
// 关闭时只收束队列和恢复 goroutine，业务 thread 的停止仍由 Stop/Delete 等显式入口处理。
func (r *threadBusWorkerRunner) Stop(ctx context.Context) error {
	if r == nil || r.svc == nil {
		return nil
	}
	r.svc.stopBusWorkers(ctx)
	return nil
}
