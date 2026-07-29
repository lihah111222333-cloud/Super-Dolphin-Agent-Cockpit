package uistate

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/observability"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/util/providerrecovery"
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
func (s *service) enrichFromDB(ctx context.Context, agents []AgentSummary, threads []ThreadSummary, runtimeMap map[string]map[string]any) error {
	var byAgent map[string]BindingEntry
	if s.bindings != nil {
		var err error
		byAgent, err = s.loadBindingIndex(ctx)
		if err != nil {
			return err
		}
	}
	providerThreads, err := resolveBindingProviderThreads(byAgent, resolveProviderThreadID)
	if err != nil {
		return err
	}

	batchConfigs, batchRuntimeRead := s.loadBatchConfigs(ctx, threads)

	for _, thread := range threads {
		s.enrichThreadFromDB(ctx, thread, byAgent, providerThreads, runtimeMap, batchConfigs, batchRuntimeRead)
	}
	if len(byAgent) == 0 {
		return nil
	}
	for i := range agents {
		applyBindingToAgent(&agents[i], byAgent, providerThreads)
	}
	return nil
}

// enrichThreadFromDB 回填单条线程的 binding 和运行时配置。
func (s *service) enrichThreadFromDB(
	ctx context.Context,
	thread ThreadSummary,
	byAgent map[string]BindingEntry,
	providerThreads map[string]string,
	runtimeMap map[string]map[string]any,
	batchConfigs map[string]map[string]any,
	batchRuntimeRead bool,
) {
	if len(byAgent) > 0 {
		applyBindingToThreadRuntime(thread, byAgent, providerThreads, runtimeMap)
	}
	threadID := strings.TrimSpace(thread.ID)
	if threadID == "" {
		return
	}
	cfg := s.threadRuntimeConfig(ctx, threadID, batchConfigs, batchRuntimeRead)
	applyTaskRuntimeToThreadRuntimeConfig(threadID, cfg, runtimeMap)
}

// threadRuntimeConfig 读取单条线程的批量或单项运行时配置。
func (s *service) threadRuntimeConfig(
	ctx context.Context,
	threadID string,
	batchConfigs map[string]map[string]any,
	batchRuntimeRead bool,
) map[string]any {
	if batchRuntimeRead {
		return batchConfigs[threadID]
	}
	if s.runtimeConfig == nil {
		return nil
	}
	cfg, err := s.runtimeConfig.ReadRuntimeConfig(ctx, threadID)
	if err != nil {
		s.logger.WarnContext(ctx, "uistate: ReadRuntimeConfig failed", "threadID", threadID, "err", err)
	}
	return cfg
}

// applyBindingToThreadRuntime 将 agent binding 回填到线程 runtime map。
// 该路径只填补缺失字段；已有 provider/threadID 视为上游权威值，不能被历史 binding 覆盖。
func applyBindingToThreadRuntime(
	thread ThreadSummary,
	idx map[string]BindingEntry,
	providerThreads map[string]string,
	runtimeMap map[string]map[string]any,
) {
	if thread.AgentID == "" {
		return
	}
	entry, ok := idx[thread.AgentID]
	if !ok || entry.Provider == "" {
		return
	}
	providerThreadID := providerThreads[strings.TrimSpace(thread.AgentID)]
	rt := ensureThreadRuntime(thread, providerThreadID, runtimeMap)
	applyBindingRuntimeFields(rt, entry, providerThreadID)
}

// applyBindingRuntimeFields 只回填缺失的展示字段。
func applyBindingRuntimeFields(rt map[string]any, entry BindingEntry, providerThreadID string) {
	// 这里是展示层回填路径，只在 runtimeMap 缺 provider 时填充。
	// 已存在 provider 可能来自实时事件或 thread runtime 配置，必须由上游权威来源纠正。
	if rt["provider"] == nil || rt["provider"] == "" {
		rt["provider"] = entry.Provider
	}
	if providerThreadID != "" && runtimeProviderThreadIDNeedsBackfill(rt["providerThreadId"]) {
		rt["providerThreadId"] = providerThreadID
	}
	if entry.Cwd != "" && runtimeString(rt["cwd"]) == "" {
		rt["cwd"] = entry.Cwd
	}
}

func ensureThreadRuntime(thread ThreadSummary, providerThreadID string, runtimeMap map[string]map[string]any) map[string]any {
	rt := runtimeMap[thread.ID]
	if rt != nil {
		return rt
	}
	rt = map[string]any{
		"agentId":          thread.AgentID,
		"state":            "idle",
		"providerThreadId": providerThreadID,
	}
	runtimeMap[thread.ID] = rt
	return rt
}

func applyTaskRuntimeToThreadRuntimeConfig(threadID string, cfg map[string]any, runtimeMap map[string]map[string]any) {
	_ = threadID
	_ = cfg
	_ = runtimeMap
}

func (s *service) loadBindingIndex(ctx context.Context) (map[string]BindingEntry, error) {
	entries, err := s.bindings.ListAgentThreadBindings(ctx)
	if err != nil {
		return nil, err
	}
	idx := make(map[string]BindingEntry, len(entries))
	for _, e := range entries {
		idx[strings.TrimSpace(e.AgentID)] = e
	}
	return idx, nil
}

func applyBindingToAgent(agent *AgentSummary, idx map[string]BindingEntry, providerThreads map[string]string) {
	agentID := strings.TrimSpace(agent.ID)
	b, ok := idx[agentID]
	if !ok {
		return
	}
	if b.Provider != "" {
		agent.Provider = b.Provider
	}
	ptid := providerThreads[agentID]
	if ptid != "" {
		agent.ProviderThreadID = ptid
	}
	if b.Cwd != "" {
		agent.CWD = b.Cwd
	}
}

// resolveBindingProviderThreads 为每个 binding 只解析一次 provider identity。
func resolveBindingProviderThreads(
	idx map[string]BindingEntry,
	resolve func(BindingEntry) (string, error),
) (map[string]string, error) {
	if resolve == nil {
		return nil, errors.New("uistate: provider recovery resolver is required")
	}
	resolved := make(map[string]string, len(idx))
	for agentID, binding := range idx {
		providerThreadID, err := resolve(binding)
		if err != nil {
			return nil, fmt.Errorf("uistate: recover provider identity for agent %q: %w", agentID, err)
		}
		resolved[strings.TrimSpace(agentID)] = providerThreadID
	}
	return resolved, nil
}

// resolveProviderThreadID 通过唯一 recovery port 解析展示所需的精确 provider identity。
func resolveProviderThreadID(b BindingEntry) (string, error) {
	result, err := providerrecovery.ResolveOptional(providerRecoveryRequestFromUIBinding(b))
	if providerrecovery.IsKind(err, providerrecovery.ErrorKindNotFound) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return result.ProviderThreadID, nil
}

// providerRecoveryRequestFromUIBinding 将 uistate binding 映射到唯一 recovery port。
func providerRecoveryRequestFromUIBinding(binding BindingEntry) providerrecovery.Request {
	return providerrecovery.Request{
		Provider:         binding.Provider,
		RolloutPath:      binding.RolloutPath,
		ProviderThreadID: binding.ProviderThreadID,
		SessionUUID:      binding.SessionUUID,
		CodexHome:        binding.CodexHome,
	}
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
