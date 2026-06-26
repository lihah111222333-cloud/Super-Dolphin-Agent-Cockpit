package uistate

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	uidto "github.com/anthropic-ai/super-agent-v3/internal/dto/ui"
	"github.com/anthropic-ai/super-agent-v3/internal/module/uistate/timeline"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/observability"
	"github.com/anthropic-ai/super-agent-v3/internal/store/uipreference"
	"github.com/anthropic-ai/super-agent-v3/internal/util/clone"
	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
)

type service struct {
	logger                *slog.Logger
	threads               contract.ThreadLister
	agents                contract.OrchestrationService
	preferences           uipreference.Store
	bindings              bindingLookup
	runtimeConfig         runtimeConfigLookup
	mu                    sync.RWMutex
	projectsMu            sync.Mutex
	state                 UIState
	workspaceByKey        map[string]WorkspaceRunSummary
	activityByThread      map[string]*threadActivity
	overlayExpiryByThread map[string]time.Time
	fallbackPrefs         map[string]json.RawMessage
	timeline              timeline.Service
	timelinePatchByThread map[string]threadTimelinePatchState
	patchSeq              map[string]int64
	projectionSeq         map[string]int64
	emitThreadPatch       threadPatchEmitter
	emitProjectionUpdated projectionUpdatedEmitter
	emitPreferenceChange  preferenceChangedEmitter
	trace                 *observability.Service
}

type bindingLookup interface {
	ListAgentThreadBindings(ctx context.Context) ([]bindingEntry, error)
}

type runtimeConfigLookup interface {
	ReadRuntimeConfig(ctx context.Context, threadID string) (map[string]any, error)
}

type bindingEntry struct {
	AgentID          string
	Provider         string
	ProviderThreadID string
	CodexThreadID    string
	RolloutPath      string
	SessionUUID      string
	CodexHome        string
	Cwd              string
}

type preferenceScopeKey struct{}

var errPreferenceKeyRequired = errors.New("uistate: preference key is required")

var _ Service = (*service)(nil)

type ServiceOption func(*service)

// WithObservability 注入可选观测服务，用于记录 UI patch 和 timeline trace。
func WithObservability(trace *observability.Service) ServiceOption {
	return func(s *service) { s.trace = trace }
}

// NewService 创建 uistate 服务，并初始化内存投影、fallback 偏好和 timeline 状态。
func NewService(
	logger *slog.Logger,
	threads contract.ThreadLister,
	agents contract.OrchestrationService,
	preferences uipreference.Store,
	bindings bindingLookup,
	runtimeCfg runtimeConfigLookup,
	options ...ServiceOption,
) (*service, Service, error) {
	if logger == nil {
		logger = pkglogger.Get()
	}
	svc := &service{
		logger:                logger,
		threads:               threads,
		agents:                agents,
		preferences:           preferences,
		bindings:              bindings,
		runtimeConfig:         runtimeCfg,
		state:                 UIState{},
		workspaceByKey:        map[string]WorkspaceRunSummary{},
		activityByThread:      map[string]*threadActivity{},
		overlayExpiryByThread: map[string]time.Time{},
		fallbackPrefs:         map[string]json.RawMessage{},
		timeline:              timeline.New(logger, nil, 0),
		timelinePatchByThread: map[string]threadTimelinePatchState{},
		patchSeq:              map[string]int64{},
		projectionSeq:         map[string]int64{},
	}
	for _, option := range options {
		if option != nil {
			option(svc)
		}
	}
	return svc, svc, nil
}

func (s *service) loadInitialState(ctx context.Context) error {
	if s == nil {
		return nil
	}
	state, err := buildInitialState(ctx, s.threads, s.agents)
	if err != nil {
		return err
	}
	s.mu.Lock()
	s.state.Threads = state.Threads
	s.state.Agents = state.Agents
	s.mu.Unlock()
	return nil
}

// buildInitialState 从 thread 和 orchestration 模块读取首屏快照。
// 任一依赖读取失败都会返回错误，避免用不完整初始状态启动 UI 投影。
func buildInitialState(ctx context.Context, threads contract.ThreadLister, agents contract.OrchestrationService) (UIState, error) {
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
			ID:        agent.ThreadID,
			AgentID:   agent.ID,
			CreatedAt: clone.Time(agent.CreatedAt),
			UpdatedAt: clone.Time(agent.UpdatedAt),
		})
	}
	sortThreads(state.Threads)
	sortAgents(state.Agents)
	return state, nil
}
func summarizeThreads(items []contract.ThreadRef) []ThreadSummary {
	out := make([]ThreadSummary, 0, len(items))
	for _, item := range items {
		status := strings.TrimSpace(item.Status)
		out = append(out, ThreadSummary{
			ID:              strings.TrimSpace(item.ID),
			Name:            strings.TrimSpace(item.Name),
			AgentID:         strings.TrimSpace(item.AgentID),
			CreatedAt:       nonZeroTimePtr(contract.NormalizeUnixTime(item.CreatedAt)),
			UpdatedAt:       nonZeroTimePtr(contract.NormalizeUnixTime(item.UpdatedAt)),
			LifecycleStatus: status,
			State:           status,
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
			CreatedAt:   nonZeroTimePtr(item.CreatedAt),
			UpdatedAt:   nonZeroTimePtr(item.UpdatedAt),
			LastReport:  strings.TrimSpace(item.LastReport),
			AgentState:  strings.TrimSpace(item.State),
			LastMessage: strings.TrimSpace(item.LastReport),
		})
	}
	return out
}

// GetState 返回完整 UIState 快照，并叠加偏好、diff state 和 timeline 投影。
// store 回填只补展示字段，不覆盖事件流已经投影出的运行时状态。
func (s *service) GetState(ctx context.Context) (*UIState, error) {
	prefs, err := s.GetPreferences(ctx)
	if err != nil {
		return nil, err
	}
	snapshot := s.stateSnapshot(ctx)
	applyPreferencesToState(snapshot, prefs)
	applyDiffStateSnapshot(ctx, snapshot, s.diffStateSnapshot(ctx))
	if s.timeline != nil {
		snapshot.TimelineByThread = s.timeline.Snapshot()
	}
	// Snapshot 先来自事件流投影，再用 store 做展示层回填。
	// enrichFromDB 只补空 provider，不会覆盖 runtimeMap 中已有的上游权威值。
	s.enrichFromDB(ctx, snapshot.Agents, snapshot.Threads, snapshot.AgentRuntimeByID)
	return snapshot, nil
}

// GetSidebar 返回 sidebar 专用快照，并记录偏好读取、内存快照和 DB 回填耗时。
func (s *service) GetSidebar(ctx context.Context) (*Sidebar, error) {
	t0 := time.Now()
	prefs, err := s.GetPreferences(ctx)
	if err != nil {
		return nil, err
	}
	t1 := time.Now()
	snapshot := s.sidebarSnapshot()
	t2 := time.Now()
	s.enrichFromDB(ctx, snapshot.Agents, snapshot.Threads, snapshot.AgentRuntimeByID)
	t3 := time.Now()
	applyPreferencesToSidebar(snapshot, prefs)

	s.logger.Info("ui.sidebar.get.duration",
		"total_ms", time.Since(t0).Milliseconds(),
		"get_prefs_ms", t1.Sub(t0).Milliseconds(),
		"snapshot_ms", t2.Sub(t1).Milliseconds(),
		"enrich_db_ms", t3.Sub(t2).Milliseconds(),
	)

	return snapshot, nil
}

// GetPreferences 读取当前 scope 的 UI 偏好。
// 当持久化 store 未注入时使用内存 fallback，供测试或轻量启动路径保持同一 wire 形态。
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

// SetPreference 校验并保存 UI 偏好，同时发出偏好变更和受影响投影的更新事件。
// 偏好 key 为空或值类型不合法会立即返回错误，避免静默写入不可消费配置。
func (s *service) SetPreference(ctx context.Context, key string, value any) error {
	key = normalizePreferenceKey(key)
	if key == "" {
		return errPreferenceKeyRequired
	}
	if err := validatePreferenceValue(key, value); err != nil {
		return err
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
	s.applyThreadOverlaysLocked(sidebar.Threads, time.Now())
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

// fallbackPreferencesLocked 合并全局和当前 scope 的内存偏好。
// scope 内配置覆盖全局配置，保持与持久化 store 的读取顺序一致。
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

func marshalPreferenceValue(value any) (json.RawMessage, error) { return json.Marshal(value) }

// stateSnapshot 在读锁内复制 UIState，并补齐 sidebar 派生字段。
// 返回值是可修改副本，调用方可以继续叠加偏好或 diff state 而不污染内存投影。
func (s *service) stateSnapshot(ctx context.Context) *UIState {
	s.mu.RLock()
	defer s.mu.RUnlock()
	snapshot := cloneState(s.state)
	s.applyThreadOverlaysLocked(snapshot.Threads, time.Now())
	sidebar := s.sidebarLocked()
	recentByThread := latestTurnsByThread(snapshot.ActiveTurn, snapshot.RecentTurns)
	snapshot.Statuses, snapshot.InterruptibleByThread = cloneStringMap(sidebar.Statuses), cloneBoolMap(sidebar.InterruptibleByThread)
	snapshot.StatusHeadersByThread, snapshot.StatusDetailsByThread = cloneStringMap(sidebar.StatusHeadersByThread), cloneStringMap(sidebar.StatusDetailsByThread)
	snapshot.AgentRuntimeByID, snapshot.MainAgentState, snapshot.AgentMetaByID = cloneRuntimeMap(sidebar.AgentRuntimeByID), s.mainAgentStateLocked(), map[string]map[string]any{}
	for _, thread := range snapshot.Threads {
		id, name := strings.TrimSpace(thread.ID), strings.TrimSpace(thread.Name)
		if id == "" {
			continue
		}
		if name != "" {
			snapshot.AgentMetaByID[id] = map[string]any{"alias": name}
		}
		if turn, ok := recentByThread[id]; ok {
			if ts := recentTurnTime(turn); !ts.IsZero() {
				if snapshot.AgentMetaByID[id] == nil {
					snapshot.AgentMetaByID[id] = map[string]any{}
				}
				snapshot.AgentMetaByID[id]["lastActiveAt"] = ts.UTC().Format(time.RFC3339Nano)
			}
		}
	}
	if requestedThreadID := firstNonEmptyString(diffStateRequestFromContext(ctx).threadID, snapshot.ActiveThreadID, snapshot.ActiveCmdThreadID); requestedThreadID != "" {
		snapshot.AlertsByThread = map[string][]uidto.PatchAlert{requestedThreadID: {}}
		if tu, ok := snapshot.TokenUsages[requestedThreadID]; ok {
			usage := &uidto.ThreadPatchTokenUsage{UsedTokens: tu.TotalTokens, ContextWindowTokens: tu.ContextWindowTokens}
			if usage.ContextWindowTokens > 0 {
				usage.UsedPercent = float64(usage.UsedTokens) * 100 / float64(usage.ContextWindowTokens)
			}
			snapshot.TokenUsageByThread = map[string]*uidto.ThreadPatchTokenUsage{requestedThreadID: usage}
		} else {
			snapshot.TokenUsageByThread = map[string]*uidto.ThreadPatchTokenUsage{}
		}
	}
	return snapshot
}
func (s *service) sidebarSnapshot() *Sidebar {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return cloneSidebar(s.sidebarLocked())
}
func (s *service) applyThreadOverlaysLocked(threads []ThreadSummary, now time.Time) {
	for i := range threads {
		threads[i] = s.effectiveThreadSummaryLocked(threads[i], now)
	}
}

// overlayActiveLocked 判断线程 overlay 是否仍在 TTL 内。
// 没有过期时间的 overlay 视为手动清理型，直到 clearThreadOverlayLocked 移除。
func (s *service) overlayActiveLocked(thread ThreadSummary, now time.Time) bool {
	threadID := strings.TrimSpace(thread.ID)
	if threadID == "" {
		return false
	}
	if strings.TrimSpace(thread.OverlayType) == "" && strings.TrimSpace(thread.OverlayText) == "" {
		return false
	}
	deadline, ok := s.overlayExpiryByThread[threadID]
	return !ok || deadline.IsZero() || !deadline.Before(now)
}

func (s *service) effectiveThreadSummaryLocked(thread ThreadSummary, now time.Time) ThreadSummary {
	if !s.overlayActiveLocked(thread, now) {
		clearThreadOverlay(&thread)
		return thread
	}
	thread.OverlayText = overlayHeaderText(thread.OverlayType, thread.OverlayText)
	if status := overlayStatus(thread.OverlayType); status != "" {
		thread.State = status
		thread.ThreadStatus = status
	}
	return thread
}

// setThreadOverlayLocked 写入线程 overlay，并按 priority/TTL 控制可见性。
// 已存在更高优先级 overlay 时不覆盖，避免低优先级提示打断错误或停止状态。
func (s *service) setThreadOverlayLocked(threadID, overlayType, text string, priority int, ttl time.Duration) {
	threadID = strings.TrimSpace(threadID)
	overlayType = strings.TrimSpace(overlayType)
	if threadID == "" || overlayType == "" {
		return
	}
	now := time.Now()
	if current, ok := s.threadSummaryLocked(threadID); ok && s.overlayActiveLocked(current, now) && current.OverlayPriority > priority {
		return
	}
	s.state.Threads = upsertThreadSummary(s.state.Threads, ThreadSummary{
		ID:              threadID,
		OverlayText:     overlayHeaderText(overlayType, text),
		OverlayType:     overlayType,
		OverlayPriority: priority,
	})
	if ttl <= 0 {
		delete(s.overlayExpiryByThread, threadID)
		return
	}
	if s.overlayExpiryByThread == nil {
		s.overlayExpiryByThread = map[string]time.Time{}
	}
	s.overlayExpiryByThread[threadID] = now.Add(ttl)
}

// clearThreadOverlayLocked 清理指定线程 overlay；overlayType 非空时只清理匹配类型。
func (s *service) clearThreadOverlayLocked(threadID, overlayType string) {
	threadID = strings.TrimSpace(threadID)
	overlayType = strings.TrimSpace(overlayType)
	if threadID == "" {
		return
	}
	for i := range s.state.Threads {
		if s.state.Threads[i].ID != threadID {
			continue
		}
		if overlayType != "" && s.state.Threads[i].OverlayType != overlayType {
			return
		}
		clearThreadOverlay(&s.state.Threads[i])
		delete(s.overlayExpiryByThread, threadID)
		return
	}
	if overlayType == "" {
		delete(s.overlayExpiryByThread, threadID)
	}
}
