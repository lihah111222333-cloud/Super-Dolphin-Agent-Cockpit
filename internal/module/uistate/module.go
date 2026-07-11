package uistate

import (
	"context"
	"log/slog"
	"strings"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/observability"
	"github.com/anthropic-ai/super-agent-v3/internal/util/historyjsonl"
	"github.com/anthropic-ai/super-agent-v3/internal/util/identifier"
	"go.uber.org/fx"
)

// Service 定义 UI 状态投影、偏好和项目列表的读写入口。
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
	ThreadLister  contract.ThreadLister `optional:"true"`
	Agents        AgentLister           `optional:"true"`
	Preferences   PreferenceStore
	Bindings      BindingLookup                      `optional:"true"`
	RuntimeConfig contract.ThreadRuntimeConfigReader `optional:"true"`
	Trace         *observability.Service             `optional:"true"`
}

var Module = fx.Module("uistate",
	fx.Provide(func(p serviceParams) (*service, Service, error) {
		var rcl runtimeConfigLookup
		if p.RuntimeConfig != nil {
			rcl = p.RuntimeConfig
		}
		return NewService(p.Logger, p.ThreadLister, p.Agents, p.Preferences, p.Bindings, rcl, WithObservability(p.Trace))
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
	var byAgent map[string]BindingEntry
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
func applyBindingToThreadRuntime(thread ThreadSummary, idx map[string]BindingEntry, runtimeMap map[string]map[string]any) {
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

func ensureThreadRuntime(thread ThreadSummary, entry BindingEntry, runtimeMap map[string]map[string]any) map[string]any {
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

func (s *service) loadBindingIndex(ctx context.Context) map[string]BindingEntry {
	entries, err := s.bindings.ListAgentThreadBindings(ctx)
	if err != nil {
		return nil
	}
	idx := make(map[string]BindingEntry, len(entries))
	for _, e := range entries {
		idx[strings.TrimSpace(e.AgentID)] = e
	}
	return idx
}

func applyBindingToAgent(agent *AgentSummary, idx map[string]BindingEntry) {
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

func resolveProviderThreadID(b BindingEntry) string {
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
