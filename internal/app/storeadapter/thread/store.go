package threadadapter

import (
	"context"
	"fmt"
	"maps"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	"github.com/anthropic-ai/super-agent-v3/internal/module/thread"
	bindingstore "github.com/anthropic-ai/super-agent-v3/internal/store/binding"
	threadstore "github.com/anthropic-ai/super-agent-v3/internal/store/thread"
)

var _ thread.ThreadStore = (*threadStoreAdapter)(nil)
var _ thread.BindingStore = (*threadBindingStoreAdapter)(nil)
var _ thread.ThreadPageReader = (*threadStoreCapabilitiesAdapter)(nil)
var _ thread.LoadedThreadPageReader = (*threadStoreCapabilitiesAdapter)(nil)
var _ thread.ActiveThreadCounter = (*threadStoreCapabilitiesAdapter)(nil)

type threadStoreAdapter struct {
	store threadstore.Store
}

type threadStoreCapabilitiesAdapter struct {
	*threadStoreAdapter
	pageReader       thread.ThreadPageReader
	loadedPageReader thread.LoadedThreadPageReader
	activeCounter    thread.ActiveThreadCounter
}

type threadBindingStoreAdapter struct {
	store bindingstore.Store
}

// provideThreadStoreAdapter 构造 Thread-owned 持久化端口，并保留 concrete Store 的动态分页能力。
func provideThreadStoreAdapter(store threadstore.Store) (thread.ThreadStore, error) {
	if store == nil {
		return nil, fmt.Errorf("app: thread store adapter requires thread store")
	}
	base := &threadStoreAdapter{store: store}
	pageReader, hasPage := store.(thread.ThreadPageReader)
	loadedPageReader, hasLoadedPage := store.(thread.LoadedThreadPageReader)
	activeCounter, hasActiveCount := store.(thread.ActiveThreadCounter)
	if !hasPage && !hasLoadedPage && !hasActiveCount {
		return base, nil
	}
	if !hasPage || !hasLoadedPage || !hasActiveCount {
		return nil, fmt.Errorf("app: thread store optional capabilities must be provided together")
	}
	return &threadStoreCapabilitiesAdapter{
		threadStoreAdapter: base,
		pageReader:         pageReader,
		loadedPageReader:   loadedPageReader,
		activeCounter:      activeCounter,
	}, nil
}

// provideThreadBindingStoreAdapter 构造 Thread-owned binding 持久化端口。
func provideThreadBindingStoreAdapter(store bindingstore.Store) (thread.BindingStore, error) {
	if store == nil {
		return nil, fmt.Errorf("app: thread binding adapter requires binding store")
	}
	return &threadBindingStoreAdapter{store: store}, nil
}

// GetByThreadID 读取 Store 记录并转换为 Thread-owned DTO。
func (a *threadStoreAdapter) GetByThreadID(ctx context.Context, threadID string) (*thread.ThreadRecord, error) {
	row, err := a.store.GetByThreadID(ctx, threadID)
	return threadRecordFromStore(row), err
}

// ListAll 读取全部 Store 记录并逐项转换。
func (a *threadStoreAdapter) ListAll(ctx context.Context) ([]thread.ThreadRecord, error) {
	rows, err := a.store.ListAll(ctx)
	if err != nil {
		return nil, err
	}
	return threadRecordsFromStore(rows), nil
}

// ListConfigsByIDs 批量读取指定 Thread 配置记录。
func (a *threadStoreAdapter) ListConfigsByIDs(ctx context.Context, threadIDs []string) ([]thread.ThreadRecord, error) {
	rows, err := a.store.ListConfigsByIDs(ctx, threadIDs)
	if err != nil {
		return nil, err
	}
	return threadRecordsFromStore(rows), nil
}

// Upsert 将 Thread-owned 写入 DTO 转换后持久化。
func (a *threadStoreAdapter) Upsert(ctx context.Context, params thread.ThreadUpsert) error {
	return a.store.Upsert(ctx, threadUpsertToStore(params))
}

// SavePromptSnapshot 转换并保存 prompt assembly 快照。
func (a *threadStoreAdapter) SavePromptSnapshot(ctx context.Context, threadID string, snapshot thread.PromptSnapshotRecord) error {
	return a.store.SavePromptSnapshot(ctx, threadID, promptSnapshotToStore(snapshot))
}

// LoadPromptSnapshot 读取并转换 prompt assembly 快照。
func (a *threadStoreAdapter) LoadPromptSnapshot(ctx context.Context, threadID string) (*thread.PromptSnapshotRecord, error) {
	snapshot, err := a.store.LoadPromptSnapshot(ctx, threadID)
	return promptSnapshotFromStore(snapshot), err
}

// UpdateStatus 转换并写入线程状态。
func (a *threadStoreAdapter) UpdateStatus(ctx context.Context, params thread.ThreadStatusUpdate) error {
	return a.store.UpdateStatus(ctx, threadStatusUpdateToStore(params))
}

// DeleteByThreadID 删除指定线程记录。
func (a *threadStoreAdapter) DeleteByThreadID(ctx context.Context, threadID string) error {
	return a.store.DeleteByThreadID(ctx, threadID)
}

// CountChildren 统计父 agent 的子线程数量。
func (a *threadStoreAdapter) CountChildren(ctx context.Context, parentAgentID string) (int64, error) {
	return a.store.CountChildren(ctx, parentAgentID)
}

// Exists 判断指定线程是否已持久化。
func (a *threadStoreAdapter) Exists(ctx context.Context, threadID string) (bool, error) {
	return a.store.Exists(ctx, threadID)
}

// ListPage 透传底层 Store 的可选全量分页能力。
func (a *threadStoreCapabilitiesAdapter) ListPage(ctx context.Context, params contract.ThreadListPageParams) (contract.ThreadListPage, error) {
	return a.pageReader.ListPage(ctx, params)
}

// ListLoadedPage 透传底层 Store 的可选已加载分页能力。
func (a *threadStoreCapabilitiesAdapter) ListLoadedPage(ctx context.Context, params contract.ThreadListPageParams) (contract.ThreadListPage, error) {
	return a.loadedPageReader.ListLoadedPage(ctx, params)
}

// CountActive 透传底层 Store 的可选活跃统计能力。
func (a *threadStoreCapabilitiesAdapter) CountActive(ctx context.Context) (int64, error) {
	return a.activeCounter.CountActive(ctx)
}

// GetByProviderThread 读取 provider thread 对应的绑定并转换 DTO。
func (a *threadBindingStoreAdapter) GetByProviderThread(ctx context.Context, provider, providerThreadID string) (*thread.BindingRecord, error) {
	row, err := a.store.GetByProviderThread(ctx, provider, providerThreadID)
	return bindingRecordFromStore(row), err
}

// Upsert 转换并写入 provider session 绑定。
func (a *threadBindingStoreAdapter) Upsert(ctx context.Context, params thread.BindingUpsert) error {
	return a.store.Upsert(ctx, bindingUpsertToStore(params))
}

// DeleteByAgentID 删除指定 agent 的绑定。
func (a *threadBindingStoreAdapter) DeleteByAgentID(ctx context.Context, agentID string) error {
	return a.store.DeleteByAgentID(ctx, agentID)
}

// UpdateSessionUUID 转换并写入 provider session UUID。
func (a *threadBindingStoreAdapter) UpdateSessionUUID(ctx context.Context, params thread.BindingSessionUUIDUpdate) error {
	return a.store.UpdateSessionUUID(ctx, bindingSessionUUIDUpdateToStore(params))
}

// UpdateProviderThreadID 转换并写入 provider thread ID。
func (a *threadBindingStoreAdapter) UpdateProviderThreadID(ctx context.Context, params thread.BindingProviderThreadIDUpdate) error {
	return a.store.UpdateProviderThreadID(ctx, bindingProviderThreadIDUpdateToStore(params))
}

// SetArchived 转换并写入 binding 归档状态。
func (a *threadBindingStoreAdapter) SetArchived(ctx context.Context, params thread.BindingArchiveUpdate) error {
	return a.store.SetArchived(ctx, bindingArchiveUpdateToStore(params))
}

// GetByAgentID 读取 agent binding 并转换 DTO。
func (a *threadBindingStoreAdapter) GetByAgentID(ctx context.Context, agentID string) (*thread.BindingRecord, error) {
	row, err := a.store.GetByAgentID(ctx, agentID)
	return bindingRecordFromStore(row), err
}

// ListAgentThreadBindings 列出并转换所有 agent-thread 绑定。
func (a *threadBindingStoreAdapter) ListAgentThreadBindings(ctx context.Context) ([]thread.BindingRecord, error) {
	rows, err := a.store.ListAgentThreadBindings(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]thread.BindingRecord, 0, len(rows))
	for i := range rows {
		out = append(out, *bindingRecordFromStore(&rows[i]))
	}
	return out, nil
}

// UpdateAgentCwd 转换并写入绑定工作目录。
func (a *threadBindingStoreAdapter) UpdateAgentCwd(ctx context.Context, params thread.BindingCWDUpdate) error {
	return a.store.UpdateAgentCwd(ctx, bindingCWDUpdateToStore(params))
}

func threadRecordsFromStore(rows []threadstore.Thread) []thread.ThreadRecord {
	out := make([]thread.ThreadRecord, 0, len(rows))
	for i := range rows {
		out = append(out, *threadRecordFromStore(&rows[i]))
	}
	return out
}

func threadRecordFromStore(row *threadstore.Thread) *thread.ThreadRecord {
	if row == nil {
		return nil
	}
	return &thread.ThreadRecord{
		ThreadID: row.ThreadID, AgentID: row.AgentID, ParentAgentID: row.ParentAgentID,
		AgentType: row.AgentType, AgentMemoryScope: row.AgentMemoryScope, Name: row.Name,
		Prompt: row.Prompt, Model: row.Model, Cwd: row.Cwd, Status: row.Status, Port: row.Port,
		PID: row.PID, CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt, FinishedAt: cloneAdapterInt64Ptr(row.FinishedAt),
		LastEventType: row.LastEventType, ErrorMessage: row.ErrorMessage, WorkspaceRunKey: row.WorkspaceRunKey,
		OwnerThreadID: row.OwnerThreadID, ConfigOverride: cloneAdapterJSON(row.ConfigOverride),
		AgentKey: row.AgentKey, PromptVersionID: cloneAdapterInt64Ptr(row.PromptVersionID), PendingLaunch: row.PendingLaunch,
		ManuallyRenamed: row.ManuallyRenamed,
	}
}

func threadUpsertToStore(row thread.ThreadUpsert) threadstore.UpsertParams {
	return threadstore.UpsertParams{
		ThreadID: row.ThreadID, Name: row.Name, Prompt: row.Prompt, Model: row.Model, Cwd: row.Cwd,
		Status: row.Status, Port: row.Port, PID: row.PID, CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt,
		OwnerThreadID: row.OwnerThreadID, ParentAgentID: row.ParentAgentID, AgentType: row.AgentType,
		AgentMemoryScope: row.AgentMemoryScope, ConfigOverride: cloneAdapterJSON(row.ConfigOverride),
		AgentKey: row.AgentKey, PromptVersionID: row.PromptVersionID, PendingLaunch: row.PendingLaunch,
		ManuallyRenamed: row.ManuallyRenamed,
	}
}

func threadStatusUpdateToStore(row thread.ThreadStatusUpdate) threadstore.UpdateStatusParams {
	return threadstore.UpdateStatusParams{ThreadID: row.ThreadID, Status: row.Status, UpdatedAt: row.UpdatedAt}
}

func promptSnapshotToStore(row thread.PromptSnapshotRecord) threadstore.PromptSnapshot {
	return threadstore.PromptSnapshot{
		DisplayName: row.DisplayName, BaseInstructions: row.BaseInstructions,
		Boundary: promptBoundaryToStore(row.Boundary), DeveloperInstructions: row.DeveloperInstructions,
		Provider: row.Provider, Version: row.Version, Hash: row.Hash,
		SectionSnapshot: cloneAdapterStringMap(row.SectionSnapshot), Generation: row.Generation,
	}
}

func promptSnapshotFromStore(row *threadstore.PromptSnapshot) *thread.PromptSnapshotRecord {
	if row == nil {
		return nil
	}
	return &thread.PromptSnapshotRecord{
		DisplayName: row.DisplayName, BaseInstructions: row.BaseInstructions,
		Boundary: promptBoundaryFromStore(row.Boundary), DeveloperInstructions: row.DeveloperInstructions,
		Provider: row.Provider, Version: row.Version, Hash: row.Hash,
		SectionSnapshot: cloneAdapterStringMap(row.SectionSnapshot), Generation: row.Generation,
	}
}

func promptBoundaryToStore(row *thread.PromptBoundaryRecord) *threadstore.PromptBoundary {
	if row == nil {
		return nil
	}
	return &threadstore.PromptBoundary{CachedPrefix: row.CachedPrefix, UncachedTail: row.UncachedTail}
}

func promptBoundaryFromStore(row *threadstore.PromptBoundary) *thread.PromptBoundaryRecord {
	if row == nil {
		return nil
	}
	return &thread.PromptBoundaryRecord{CachedPrefix: row.CachedPrefix, UncachedTail: row.UncachedTail}
}

func bindingRecordFromStore(row *bindingstore.Binding) *thread.BindingRecord {
	if row == nil {
		return nil
	}
	return &thread.BindingRecord{
		AgentID: row.AgentID, Provider: row.Provider, ProviderThreadID: row.ProviderThreadID,
		CodexThreadID: row.CodexThreadID, RolloutPath: row.RolloutPath, Cwd: row.Cwd,
		ParentAgentID: row.ParentAgentID, AgentType: row.AgentType, AgentMemoryScope: row.AgentMemoryScope,
		Archived: row.Archived, CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt, SessionUUID: row.SessionUUID,
		CodexHome: row.CodexHome, CodexInstanceKey: row.CodexInstanceKey,
		CodexModelProvider: row.CodexModelProvider,
	}
}

func bindingUpsertToStore(row thread.BindingUpsert) bindingstore.UpsertParams {
	return bindingstore.UpsertParams{
		AgentID: row.AgentID, Provider: row.Provider, ProviderThreadID: row.ProviderThreadID,
		CodexThreadID: row.CodexThreadID, RolloutPath: row.RolloutPath, SessionUUID: row.SessionUUID,
		Cwd: row.Cwd, ParentAgentID: row.ParentAgentID, AgentType: row.AgentType,
		AgentMemoryScope: row.AgentMemoryScope, CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt,
		CodexHome: row.CodexHome, CodexInstanceKey: row.CodexInstanceKey,
		CodexModelProvider: row.CodexModelProvider,
	}
}

func bindingSessionUUIDUpdateToStore(row thread.BindingSessionUUIDUpdate) bindingstore.UpdateSessionUUIDParams {
	return bindingstore.UpdateSessionUUIDParams{SessionUUID: row.SessionUUID, UpdatedAt: row.UpdatedAt, AgentID: row.AgentID}
}

func bindingProviderThreadIDUpdateToStore(row thread.BindingProviderThreadIDUpdate) bindingstore.UpdateProviderThreadIDParams {
	return bindingstore.UpdateProviderThreadIDParams{ProviderThreadID: row.ProviderThreadID, UpdatedAt: row.UpdatedAt, AgentID: row.AgentID}
}

func bindingArchiveUpdateToStore(row thread.BindingArchiveUpdate) bindingstore.SetArchivedParams {
	return bindingstore.SetArchivedParams{AgentID: row.AgentID, Archived: row.Archived, UpdatedAt: row.UpdatedAt}
}

func bindingCWDUpdateToStore(row thread.BindingCWDUpdate) bindingstore.UpdateAgentCwdParams {
	return bindingstore.UpdateAgentCwdParams{AgentID: row.AgentID, Cwd: row.Cwd, UpdatedAt: row.UpdatedAt}
}

func cloneAdapterJSON(raw []byte) []byte {
	if len(raw) == 0 {
		return nil
	}
	return append([]byte(nil), raw...)
}

// cloneAdapterInt64Ptr 复制可选整数值，避免 Store 与领域 DTO 共享可变指针。
func cloneAdapterInt64Ptr(value *int64) *int64 {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

func cloneAdapterStringMap(values map[string]string) map[string]string {
	if values == nil {
		return nil
	}
	out := make(map[string]string, len(values))
	maps.Copy(out, values)
	return out
}
