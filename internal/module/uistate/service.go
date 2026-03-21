package uistate

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"sync"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	uidto "github.com/anthropic-ai/super-agent-v3/internal/dto/ui"
	"github.com/anthropic-ai/super-agent-v3/internal/module/thread"
	"github.com/anthropic-ai/super-agent-v3/internal/store/uipreference"
)

type service struct {
	logger                *slog.Logger
	preferences           uipreference.Store
	mu                    sync.RWMutex
	projectsMu            sync.Mutex
	state                 UIState
	workspaceByKey        map[string]WorkspaceRunSummary
	fallbackPrefs         map[string]json.RawMessage
	patchSeq              map[string]int64
	projectionSeq         map[string]int64
	emitThreadPatch       threadPatchEmitter
	emitProjectionUpdated projectionUpdatedEmitter
	emitPreferenceChange  preferenceChangedEmitter
}

type preferenceScopeKey struct{}

var errPreferenceKeyRequired = errors.New("uistate: preference key is required")

var _ Service = (*service)(nil)

func NewService(
	logger *slog.Logger,
	threads thread.Service,
	agents contract.OrchestrationService,
	preferences uipreference.Store,
) (*service, Service, error) {
	if logger == nil {
		logger = slog.Default()
	}
	state, err := buildInitialState(context.Background(), threads, agents)
	if err != nil {
		return nil, nil, err
	}
	svc := &service{
		logger:         logger,
		preferences:    preferences,
		state:          state,
		workspaceByKey: map[string]WorkspaceRunSummary{},
		fallbackPrefs:  map[string]json.RawMessage{},
		patchSeq:       map[string]int64{},
		projectionSeq:  map[string]int64{},
	}
	return svc, svc, nil
}

func buildInitialState(ctx context.Context, threads thread.Service, agents contract.OrchestrationService) (UIState, error) {
	state := UIState{}
	if threads != nil {
		items, err := threads.List(ctx)
		if err != nil {
			return UIState{}, err
		}
		state.Threads = summarizeThreads(items)
	}
	if agents != nil {
		items, err := agents.ListAgents(ctx)
		if err != nil {
			return UIState{}, err
		}
		state.Agents = summarizeAgents(items)
	}
	for _, agent := range state.Agents {
		state.Threads = upsertThreadSummary(state.Threads, ThreadSummary{
			ID:      agent.ThreadID,
			AgentID: agent.ID,
		})
	}
	sortThreads(state.Threads)
	sortAgents(state.Agents)
	return state, nil
}

func summarizeThreads(items []thread.Ref) []ThreadSummary {
	out := make([]ThreadSummary, 0, len(items))
	for _, item := range items {
		out = append(out, ThreadSummary{
			ID:      strings.TrimSpace(item.ID),
			Name:    strings.TrimSpace(item.Name),
			AgentID: strings.TrimSpace(item.AgentID),
		})
	}
	return out
}

func summarizeAgents(items []contract.AgentSnapshot) []AgentSummary {
	out := make([]AgentSummary, 0, len(items))
	for _, item := range items {
		out = append(out, AgentSummary{
			ID:          strings.TrimSpace(item.ID),
			Name:        strings.TrimSpace(item.Name),
			ThreadID:    strings.TrimSpace(item.ThreadID),
			ParentID:    strings.TrimSpace(item.ParentID),
			State:       strings.TrimSpace(item.State),
			Provider:    strings.TrimSpace(item.Provider),
			CWD:         strings.TrimSpace(item.Cwd),
			Port:        item.Port,
			LastReport:  strings.TrimSpace(item.LastReport),
			AgentState:  strings.TrimSpace(item.State),
			LastMessage: strings.TrimSpace(item.LastReport),
		})
	}
	return out
}

func (s *service) GetState(ctx context.Context) (*UIState, error) {
	prefs, err := s.GetPreferences(ctx)
	if err != nil {
		return nil, err
	}
	snapshot := s.stateSnapshot()
	applyPreferencesToState(snapshot, prefs)
	// TODO(P8): knownDiffRevision only short-circuits unchanged snapshots here.
	// Real known diff consumption still needs projector/live patch integration.
	applyDiffStateSnapshot(ctx, snapshot, s.diffStateSnapshot(ctx))
	return snapshot, nil
}

func (s *service) GetSidebar(ctx context.Context) (*Sidebar, error) {
	prefs, err := s.GetPreferences(ctx)
	if err != nil {
		return nil, err
	}
	snapshot := s.sidebarSnapshot()
	applyPreferencesToSidebar(snapshot, prefs)
	return snapshot, nil
}

func (s *service) GetPreferences(ctx context.Context) (*Preferences, error) {
	scope := preferenceScopeFromContext(ctx)
	var (
		values map[string]any
		err    error
	)
	if s.preferences != nil {
		values, err = loadPreferencesFromStore(ctx, s.preferences, scope)
	} else {
		s.mu.RLock()
		values = s.fallbackPreferencesLocked(scope)
		s.mu.RUnlock()
	}
	if err != nil {
		return nil, err
	}
	return clonePreferences(buildPreferences(scope, values)), nil
}

func (s *service) SetPreference(ctx context.Context, key string, value any) error {
	key = normalizePreferenceKey(key)
	if key == "" {
		return errPreferenceKeyRequired
	}
	scope := preferenceScopeFromContext(ctx)
	var projectionUpdates []uidto.UIProjectionUpdated
	if s.preferences != nil {
		if err := storePreference(ctx, s.preferences, scope, key, value); err != nil {
			return err
		}
		s.mu.Lock()
		s.applyRuntimePreferenceLocked(key, value)
		projectionUpdates = s.preferenceProjectionUpdatesLocked(key)
		s.mu.Unlock()
		s.emitPreferenceChangedEvent(scope, key, value)
		s.emitProjectionUpdatedEvents(projectionUpdates...)
		return nil
	}
	raw, err := marshalPreferenceValue(value)
	if err != nil {
		return err
	}
	s.mu.Lock()
	s.fallbackPrefs[fallbackPreferenceKey(scope, key)] = raw
	s.applyRuntimePreferenceLocked(key, value)
	projectionUpdates = s.preferenceProjectionUpdatesLocked(key)
	s.mu.Unlock()
	s.emitPreferenceChangedEvent(scope, key, value)
	s.emitProjectionUpdatedEvents(projectionUpdates...)
	return nil
}

func (s *service) sidebarLocked() Sidebar {
	sidebar := Sidebar{
		Threads:     cloneThreads(s.state.Threads),
		Agents:      cloneAgents(s.state.Agents),
		ActiveTurn:  cloneTurn(s.state.ActiveTurn),
		RecentTurns: cloneTurns(s.state.RecentTurns),
		Workspace:   WorkspacePanel{Runs: s.workspaceRunsLocked()},
		TokenUsage:  s.state.TokenUsage,
	}
	s.fillSidebarDerivedLocked(&sidebar)
	return sidebar
}

func (s *service) workspaceRunsLocked() []WorkspaceRunSummary {
	items := make([]WorkspaceRunSummary, 0, len(s.workspaceByKey))
	for _, item := range s.workspaceByKey {
		items = append(items, item)
	}
	sortWorkspaceRuns(items)
	return cloneWorkspaceRuns(items)
}

func (s *service) fallbackPreferencesLocked(scope string) map[string]any {
	values := map[string]any{}
	for rawKey, value := range s.fallbackPrefs {
		cwd, key, ok := splitFallbackPreferenceKey(rawKey)
		if !ok {
			continue
		}
		if cwd == "" {
			values[key] = decodePreferenceValue(value)
		}
	}
	if scope == "" {
		return values
	}
	for rawKey, value := range s.fallbackPrefs {
		cwd, key, ok := splitFallbackPreferenceKey(rawKey)
		if ok && cwd == scope {
			values[key] = decodePreferenceValue(value)
		}
	}
	return values
}

func loadPreferencesFromStore(ctx context.Context, store uipreference.Store, scope string) (map[string]any, error) {
	rows, err := store.List(ctx, scope)
	if err != nil {
		return nil, err
	}
	values := map[string]any{}
	for _, row := range rows {
		values[normalizePreferenceKey(row.Key)] = decodePreferenceValue(row.Value)
	}
	return values, nil
}

func storePreference(ctx context.Context, store uipreference.Store, scope, key string, value any) error {
	raw, err := marshalPreferenceValue(value)
	if err != nil {
		return err
	}
	return store.Upsert(ctx, uipreference.UpsertParams{
		Cwd:   scope,
		Key:   key,
		Value: raw,
	})
}

func decodePreferenceValue(raw json.RawMessage) any {
	var value any
	if err := json.Unmarshal(raw, &value); err == nil {
		return value
	}
	return strings.TrimSpace(string(raw))
}

func marshalPreferenceValue(value any) (json.RawMessage, error) {
	return json.Marshal(value)
}

func (s *service) stateSnapshot() *UIState {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return cloneState(s.state)
}

func (s *service) sidebarSnapshot() *Sidebar {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return cloneSidebar(s.sidebarLocked())
}

func withPreferenceScope(ctx context.Context, cwd string) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, preferenceScopeKey{}, strings.TrimSpace(cwd))
}

func preferenceScopeFromContext(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	value, _ := ctx.Value(preferenceScopeKey{}).(string)
	return strings.TrimSpace(value)
}

func fallbackPreferenceKey(scope, key string) string {
	return scope + "\x1f" + strings.TrimSpace(key)
}

func splitFallbackPreferenceKey(raw string) (string, string, bool) {
	scope, key, ok := strings.Cut(raw, "\x1f")
	key = strings.TrimSpace(key)
	return scope, key, ok && key != ""
}
