package uistate

import (
	"context"
	"log/slog"
	"strings"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	"github.com/anthropic-ai/super-agent-v3/internal/module/thread"
	bindingstore "github.com/anthropic-ai/super-agent-v3/internal/store/binding"
	"github.com/anthropic-ai/super-agent-v3/internal/store/uipreference"
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

	Logger      *slog.Logger
	Threads     thread.Service
	Agents      contract.OrchestrationService `optional:"true"`
	Preferences uipreference.Store
	Bindings    bindingstore.Store `optional:"true"`
}

var Module = fx.Options(
	fx.Provide(func(p serviceParams) (*service, Service, error) {
		return NewService(p.Logger, p.Threads, p.Agents, p.Preferences, newBindingAdapter(p.Bindings))
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
)

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
			SessionUUID:      strings.TrimSpace(r.SessionUUID),
			Cwd:              strings.TrimSpace(r.Cwd),
		}
	}
	return out, nil
}

// enrichAgentsFromDB overwrites in-memory agent fields with DB binding
// truth.  Called outside the service mutex so DB queries are safe.
func (s *service) enrichFromDB(ctx context.Context, agents []AgentSummary, threads []ThreadSummary, runtimeMap map[string]map[string]any) {
	var byAgent map[string]bindingEntry
	if s.bindings != nil {
		byAgent = s.loadBindingIndex(ctx)
	}
	for _, thread := range threads {
		if len(byAgent) > 0 {
			applyBindingToThreadRuntime(thread, byAgent, runtimeMap)
		}
		s.applyTaskRuntimeToThreadRuntime(ctx, thread, runtimeMap)
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
}

func ensureThreadRuntime(thread ThreadSummary, entry bindingEntry, runtimeMap map[string]map[string]any) map[string]any {
	rt := runtimeMap[thread.ID]
	if rt != nil {
		return rt
	}
	rt = map[string]any{
		"agentId":          thread.AgentID,
		"state":            "idle",
		"providerThreadId": entry.ProviderThreadID,
	}
	runtimeMap[thread.ID] = rt
	return rt
}

func (s *service) applyTaskRuntimeToThreadRuntime(ctx context.Context, thread ThreadSummary, runtimeMap map[string]map[string]any) {
	if s == nil || s.runtimeConfig == nil {
		return
	}
	threadID := strings.TrimSpace(thread.ID)
	if threadID == "" {
		return
	}
	cfg, err := s.runtimeConfig.ReadRuntimeConfig(ctx, threadID)
	if err != nil || len(cfg) == 0 {
		return
	}
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

func runtimeConfigReader(threads thread.Service) runtimeConfigLookup {
	if reader, ok := threads.(runtimeConfigLookup); ok {
		return reader
	}
	return nil
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
	ptid := strings.TrimSpace(b.ProviderThreadID)
	if su := strings.TrimSpace(b.SessionUUID); su != "" && su != ptid {
		return su
	}
	return ptid
}
