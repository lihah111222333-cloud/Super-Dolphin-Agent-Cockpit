package uistate

import (
	"context"
	"log/slog"
	"strings"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	bindingstore "github.com/anthropic-ai/super-agent-v3/internal/store/binding"
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
	Preferences   uipreference.Store
	Bindings      bindingstore.Store                 `optional:"true"`
	RuntimeConfig contract.ThreadRuntimeConfigReader `optional:"true"`
}

var Module = fx.Module("uistate",
	fx.Provide(func(p serviceParams) (*service, Service, error) {
		var rcl runtimeConfigLookup
		if p.RuntimeConfig != nil {
			rcl = p.RuntimeConfig
		}
		return NewService(p.Logger, p.ThreadLister, p.Agents, p.Preferences, newBindingAdapter(p.Bindings), rcl)
	}),
	// P22 P4 S1b: publish the narrow contract.UIProjectStateFacade so
	// ui/wails and other frontends can consume GetProjects without
	// importing this package.
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

func (s *service) loadBatchConfigs(ctx context.Context, threads []ThreadSummary) map[string]map[string]any {
	if s.runtimeConfig == nil {
		return nil
	}
	bulkReader, ok := s.runtimeConfig.(contract.ThreadRuntimeConfigReader)
	if !ok {
		return nil
	}
	var threadIDs []string
	for _, thread := range threads {
		if id := strings.TrimSpace(thread.ID); id != "" {
			threadIDs = append(threadIDs, id)
		}
	}
	if len(threadIDs) == 0 {
		return nil
	}
	batchConfigs, _ := bulkReader.ReadRuntimeConfigs(ctx, threadIDs)
	return batchConfigs
}

func (s *service) enrichFromDB(ctx context.Context, agents []AgentSummary, threads []ThreadSummary, runtimeMap map[string]map[string]any) {
	var byAgent map[string]bindingEntry
	if s.bindings != nil {
		byAgent = s.loadBindingIndex(ctx)
	}

	batchConfigs := s.loadBatchConfigs(ctx, threads)

	for _, thread := range threads {
		if len(byAgent) > 0 {
			applyBindingToThreadRuntime(thread, byAgent, runtimeMap)
		}

		threadID := strings.TrimSpace(thread.ID)
		if threadID == "" {
			continue
		}

		var cfg map[string]any
		if batchConfigs != nil {
			cfg = batchConfigs[threadID]
		} else if s.runtimeConfig != nil {
			cfg, _ = s.runtimeConfig.ReadRuntimeConfig(ctx, threadID)
		}

		if len(cfg) > 0 {
			applyTaskRuntimeToThreadRuntimeConfig(threadID, cfg, runtimeMap)
		}
	}
	if len(byAgent) == 0 {
		return
	}
	for i := range agents {
		applyBindingToAgent(&agents[i], byAgent)
	}
}

func applyBindingToThreadRuntime(thread ThreadSummary, idx map[string]bindingEntry, runtimeMap map[string]map[string]any) {
	if thread.AgentID == "" {
		return
	}
	entry, ok := idx[thread.AgentID]
	if !ok || entry.Provider == "" {
		return
	}
	rt := ensureThreadRuntime(thread, entry, runtimeMap)
	// Defensive note: this is a backfill-only path. We intentionally fill the
	// provider only when runtimeMap is missing it; if a non-empty provider here
	// is stale, correction must come from the upstream authoritative sources.
	// The known pending-launch default-to-codex source was closed in the B2+
	// thread fix (see internal/module/thread/factory.go:266).
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
	rt := runtimeMap[threadID]
	if rt == nil {
		rt = map[string]any{}
		runtimeMap[threadID] = rt
	}
	setRuntimeStringField(rt, cfg, "taskId", "taskId", "task_id")
	setRuntimeStringField(rt, cfg, "taskTitle", "taskTitle", "task_title")
	setRuntimeStringField(rt, cfg, "handoffFile", "handoffFile", "handoff_file")
	setRuntimeStringField(rt, cfg, "ownerThreadId", "ownerThreadId", "owner_thread_id")
	setRuntimeStringField(rt, cfg, "rootTaskId", "rootTaskId", "root_task_id")
}

func setRuntimeStringField(rt map[string]any, cfg map[string]any, field string, keys ...string) {
	if value := runtimeConfigStringValue(cfg, keys...); value != "" {
		rt[field] = value
	}
}

func runtimeConfigStringValue(cfg map[string]any, keys ...string) string {
	for _, key := range keys {
		value, ok := cfg[key]
		if !ok {
			continue
		}
		text, ok := value.(string)
		if ok {
			if text = strings.TrimSpace(text); text != "" {
				return text
			}
		}
	}
	return ""
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
