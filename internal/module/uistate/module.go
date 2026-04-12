package uistate

import (
	"context"
	"log/slog"
	"strings"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	"github.com/anthropic-ai/super-agent-v3/internal/module/thread"
	bindingstore "github.com/anthropic-ai/super-agent-v3/internal/store/binding"
	"github.com/anthropic-ai/super-agent-v3/internal/store/uipreference"
	"github.com/kelindar/event"
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
	fx.Provide(NewUIStateHandlers),
	fx.Provide(NewConfigHandlers),
	fx.Invoke(registerProjections),
)

func registerProjections(lc fx.Lifecycle, dispatcher *event.Dispatcher, svc *service) {
	if svc != nil {
		svc.bindDispatcher(dispatcher)
	}
	var cancels []context.CancelFunc
	lc.Append(fx.Hook{
		OnStart: func(context.Context) error {
			cancels = registerProjectionSubscriptions(dispatcher, svc)
			return nil
		},
		OnStop: func(context.Context) error {
			for _, cancel := range cancels {
				if cancel != nil {
					cancel()
				}
			}
			cancels = nil
			return nil
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
			SessionUUID:      strings.TrimSpace(r.SessionUUID),
			Cwd:              strings.TrimSpace(r.Cwd),
		}
	}
	return out, nil
}

// enrichAgentsFromDB overwrites in-memory agent fields with DB binding
// truth.  Called outside the service mutex so DB queries are safe.
func (s *service) enrichFromDB(ctx context.Context, agents []AgentSummary, threads []ThreadSummary, runtimeMap map[string]map[string]any) {
	if s.bindings == nil {
		return
	}
	byAgent := s.loadBindingIndex(ctx)
	if len(byAgent) == 0 {
		return
	}
	for i := range agents {
		applyBindingToAgent(&agents[i], byAgent)
	}
	for _, thread := range threads {
		applyBindingToThreadRuntime(thread, byAgent, runtimeMap)
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
