package uistate

import (
	"context"
	"encoding/json"
	"log/slog"
	"strings"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/observability"
	bindingstore "github.com/anthropic-ai/super-agent-v3/internal/store/binding"
	sharedfilestore "github.com/anthropic-ai/super-agent-v3/internal/store/sharedfile"
	"github.com/anthropic-ai/super-agent-v3/internal/store/uipreference"
	"github.com/anthropic-ai/super-agent-v3/internal/util/historyjsonl"
	"github.com/anthropic-ai/super-agent-v3/internal/util/identifier"
	"go.uber.org/fx"
)

type Service interface {
	GetState(ctx context.Context) (*UIState, error)
	GetSidebar(ctx context.Context) (*Sidebar, error)
	GetPreferences(ctx context.Context) (*Preferences, error)
	SetPreference(ctx context.Context, key string, value any) error
	GetProjects(ctx context.Context) (*ProjectsState, error)
	SetActiveProject(ctx context.Context, path string) (*ProjectsState, error)
	AddProject(ctx context.Context, path string) (*ProjectsState, error)
	RemoveProject(ctx context.Context, path string) (*ProjectsState, error)
}

type serviceParams struct {
	fx.In

	Logger        *slog.Logger
	ThreadLister  contract.ThreadLister         `optional:"true"`
	Agents        contract.OrchestrationService `optional:"true"`
	Preferences   preferenceStore
	Bindings      bindingstore.Store                 `optional:"true"`
	RuntimeConfig contract.ThreadRuntimeConfigReader `optional:"true"`
	Trace         *observability.Service             `optional:"true"`
}

var Module = fx.Module("uistate",
	fx.Provide(newPreferenceStoreAdapter),
	fx.Provide(newSharedFileReaderAdapter),
	fx.Provide(func(p serviceParams) (*service, Service, error) {
		var rcl runtimeConfigLookup
		if p.RuntimeConfig != nil {
			rcl = p.RuntimeConfig
		}
		return NewService(p.Logger, p.ThreadLister, p.Agents, p.Preferences, newBindingAdapter(p.Bindings), rcl, WithObservability(p.Trace))
	}),
	// 对外只发布窄 ProjectState facade，让 UI 层读取项目状态时不依赖 uistate 包内实现。
	fx.Provide(NewProjectStateFacade),
	fx.Provide(NewUIStateHandlers),
	fx.Provide(fx.Annotate(
		NewConfigHandlers,
		fx.ParamTags("", "", "", "", `optional:"true"`, ""),
	)),
	fx.Provide(NewUIStateSubscribers),
	fx.Invoke(registerInitialStateLifecycle),
)

func registerInitialStateLifecycle(lc fx.Lifecycle, svc *service) {
	lc.Append(fx.Hook{
		OnStart: func(ctx context.Context) error {
			return svc.loadInitialState(ctx)
		},
	})
}

type preferenceStoreAdapter struct {
	store uipreference.Store
}

// newPreferenceStoreAdapter 把持久化 store 收窄成 uistate 本地偏好端口。
// store DTO 到 UI 端口 DTO 的转换集中在装配边界，避免业务文件直接依赖 store 包。
func newPreferenceStoreAdapter(store uipreference.Store) preferenceStore {
	if store == nil {
		return nil
	}
	return &preferenceStoreAdapter{store: store}
}

// GetValue 转发单项偏好读取，并保留底层 store 的 not found 语义。
func (a *preferenceStoreAdapter) GetValue(ctx context.Context, cwd, key string) (json.RawMessage, error) {
	return a.store.GetValue(ctx, cwd, key)
}

// Upsert 将 uistate 本地写入 DTO 转换成 store DTO 后持久化。
func (a *preferenceStoreAdapter) Upsert(ctx context.Context, params preferenceUpsertParams) error {
	return a.store.Upsert(ctx, uipreference.UpsertParams{
		Cwd:   params.Cwd,
		Key:   params.Key,
		Value: params.Value,
	})
}

// List 将 store 偏好行转换成本地偏好行，避免 store DTO 泄露到业务文件。
func (a *preferenceStoreAdapter) List(ctx context.Context, cwd string) ([]preferenceEntry, error) {
	rows, err := a.store.List(ctx, cwd)
	if err != nil {
		return nil, err
	}
	out := make([]preferenceEntry, len(rows))
	for i, row := range rows {
		out[i] = preferenceEntry{
			Cwd:   row.Cwd,
			Key:   row.Key,
			Value: append(json.RawMessage(nil), row.Value...),
		}
	}
	return out, nil
}

type sharedFileReaderAdapter struct {
	reader sharedfilestore.Reader
}

// newSharedFileReaderAdapter 把 shared-file store 收窄成 LSP prompt hint 只读端口。
func newSharedFileReaderAdapter(reader sharedfilestore.Reader) sharedFileReader {
	if reader == nil {
		return nil
	}
	return &sharedFileReaderAdapter{reader: reader}
}

// Get 读取 shared-file 并只暴露 uistate 需要的最小字段。
func (a *sharedFileReaderAdapter) Get(ctx context.Context, path string) (*sharedFile, error) {
	file, err := a.reader.Get(ctx, path)
	if err != nil {
		return nil, err
	}
	if file == nil {
		return nil, nil
	}
	return &sharedFile{Path: file.Path, Content: file.Content}, nil
}

// bindingAdapter adapts binding.Store to the minimal bindingLookup interface.
type bindingAdapter struct {
	store bindingstore.Store
}

func newBindingAdapter(store bindingstore.Store) bindingLookup {
	if store == nil {
		return nil
	}
	return &bindingAdapter{store: store}
}

// ListAgentThreadBindings 将持久化 binding 行转换成 uistate 需要的最小字段集合。
// 这里统一 trim 跨模块 wire 字段，避免 sidebar runtime 回填继续传播空白 ID。
func (a *bindingAdapter) ListAgentThreadBindings(ctx context.Context) ([]bindingEntry, error) {
	rows, err := a.store.ListAgentThreadBindings(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]bindingEntry, len(rows))
	for i, r := range rows {
		out[i] = bindingEntry{
			AgentID:          strings.TrimSpace(r.AgentID),
			Provider:         strings.TrimSpace(r.Provider),
			ProviderThreadID: strings.TrimSpace(r.ProviderThreadID),
			CodexThreadID:    strings.TrimSpace(r.CodexThreadID),
			RolloutPath:      strings.TrimSpace(r.RolloutPath),
			SessionUUID:      strings.TrimSpace(r.SessionUUID),
			CodexHome:        strings.TrimSpace(r.CodexHome),
			Cwd:              strings.TrimSpace(r.Cwd),
		}
	}
	return out, nil
}

// loadBatchConfigs 批量读取 thread runtime 配置，返回 bool 表示是否已覆盖本轮读取路径。
// 读取失败只影响 UI runtime 补充字段，调用方会记录告警并保持内存投影可用。
func (s *service) loadBatchConfigs(ctx context.Context, threads []ThreadSummary) (map[string]map[string]any, bool) {
	if s.runtimeConfig == nil {
		return nil, false
	}
	bulkReader, ok := s.runtimeConfig.(contract.ThreadRuntimeConfigReader)
	if !ok {
		return nil, false
	}
	var threadIDs []string
	for _, thread := range threads {
		if id := strings.TrimSpace(thread.ID); id != "" {
			threadIDs = append(threadIDs, id)
		}
	}
	if len(threadIDs) == 0 {
		return nil, true
	}
	batchConfigs, err := bulkReader.ReadRuntimeConfigs(ctx, threadIDs)
	if err != nil {
		s.logger.WarnContext(ctx, "uistate: ReadRuntimeConfigs failed; skipping per-thread fallback", "err", err)
		return nil, true
	}
	return batchConfigs, true
}

// enrichFromDB 用 binding 和 runtime 配置补齐内存快照里的 provider/thread 运行时字段。
// 该步骤只做展示层回填，不改变 store 中的状态；读取失败不会覆盖事件流投影结果。
func (s *service) enrichFromDB(ctx context.Context, agents []AgentSummary, threads []ThreadSummary, runtimeMap map[string]map[string]any) {
	var byAgent map[string]bindingEntry
	if s.bindings != nil {
		byAgent = s.loadBindingIndex(ctx)
	}

	batchConfigs, batchRuntimeRead := s.loadBatchConfigs(ctx, threads)

	for _, thread := range threads {
		if len(byAgent) > 0 {
			applyBindingToThreadRuntime(thread, byAgent, runtimeMap)
		}

		threadID := strings.TrimSpace(thread.ID)
		if threadID == "" {
			continue
		}

		var cfg map[string]any
		if batchRuntimeRead {
			cfg = batchConfigs[threadID]
		} else if s.runtimeConfig != nil {
			var rcErr error
			cfg, rcErr = s.runtimeConfig.ReadRuntimeConfig(ctx, threadID)
			if rcErr != nil {
				s.logger.WarnContext(ctx, "uistate: ReadRuntimeConfig failed", "threadID", threadID, "err", rcErr)
			}
		}

		applyTaskRuntimeToThreadRuntimeConfig(threadID, cfg, runtimeMap)
	}
	if len(byAgent) == 0 {
		return
	}
	for i := range agents {
		applyBindingToAgent(&agents[i], byAgent)
	}
}

// applyBindingToThreadRuntime 将 agent binding 回填到线程 runtime map。
// 该路径只填补缺失字段；已有 provider/threadID 视为上游权威值，不能被历史 binding 覆盖。
func applyBindingToThreadRuntime(thread ThreadSummary, idx map[string]bindingEntry, runtimeMap map[string]map[string]any) {
	if thread.AgentID == "" {
		return
	}
	entry, ok := idx[thread.AgentID]
	if !ok || entry.Provider == "" {
		return
	}
	rt := ensureThreadRuntime(thread, entry, runtimeMap)
	// 这里是展示层回填路径，只在 runtimeMap 缺 provider 时填充。
	// 已存在 provider 可能来自实时事件或 thread runtime 配置，必须由上游权威来源纠正。
	if rt["provider"] == nil || rt["provider"] == "" {
		rt["provider"] = entry.Provider
	}
	if providerThreadID := resolveProviderThreadID(entry); providerThreadID != "" && runtimeProviderThreadIDNeedsBackfill(rt["providerThreadId"]) {
		rt["providerThreadId"] = providerThreadID
	}
	if entry.Cwd != "" && runtimeString(rt["cwd"]) == "" {
		rt["cwd"] = entry.Cwd
	}
}

func ensureThreadRuntime(thread ThreadSummary, entry bindingEntry, runtimeMap map[string]map[string]any) map[string]any {
	rt := runtimeMap[thread.ID]
	if rt != nil {
		return rt
	}
	rt = map[string]any{
		"agentId":          thread.AgentID,
		"state":            "idle",
		"providerThreadId": resolveProviderThreadID(entry),
	}
	runtimeMap[thread.ID] = rt
	return rt
}

func applyTaskRuntimeToThreadRuntimeConfig(threadID string, cfg map[string]any, runtimeMap map[string]map[string]any) {
	_ = threadID
	_ = cfg
	_ = runtimeMap
}

func (s *service) loadBindingIndex(ctx context.Context) map[string]bindingEntry {
	entries, err := s.bindings.ListAgentThreadBindings(ctx)
	if err != nil {
		return nil
	}
	idx := make(map[string]bindingEntry, len(entries))
	for _, e := range entries {
		idx[strings.TrimSpace(e.AgentID)] = e
	}
	return idx
}

func applyBindingToAgent(agent *AgentSummary, idx map[string]bindingEntry) {
	b, ok := idx[strings.TrimSpace(agent.ID)]
	if !ok {
		return
	}
	if b.Provider != "" {
		agent.Provider = b.Provider
	}
	ptid := resolveProviderThreadID(b)
	if ptid != "" {
		agent.ProviderThreadID = ptid
	}
	if b.Cwd != "" {
		agent.CWD = b.Cwd
	}
}

func resolveProviderThreadID(b bindingEntry) string {
	for _, candidate := range []string{b.ProviderThreadID, b.SessionUUID} {
		ptid := strings.TrimSpace(candidate)
		if !identifier.LooksLikeUUID(ptid) {
			continue
		}
		if _, err := historyjsonl.ExistingProviderPath(historyjsonl.ReadRequest{
			Provider:         b.Provider,
			RolloutPath:      b.RolloutPath,
			ThreadID:         b.CodexThreadID,
			ProviderThreadID: ptid,
			SessionUUID:      ptid,
			CodexHome:        b.CodexHome,
		}); err == nil {
			return ptid
		}
	}
	return ""
}

func runtimeProviderThreadIDNeedsBackfill(value any) bool {
	text := runtimeString(value)
	return text == "" || strings.HasPrefix(text, "agent_")
}

func runtimeString(value any) string {
	text, ok := value.(string)
	if !ok {
		return ""
	}
	return strings.TrimSpace(text)
}
