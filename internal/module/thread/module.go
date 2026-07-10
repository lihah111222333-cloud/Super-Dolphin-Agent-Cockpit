// Package thread 管理 agent thread 的完整生命周期：创建、恢复、fork、归档、删除，
// 以及与 provider session、binding store、prompt assembly 之间的协调。
package thread

import (
	"context"
	"errors"
	"log/slog"
	"strings"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	"github.com/anthropic-ai/super-agent-v3/internal/util"
	"go.uber.org/fx"
)

var _ contract.PendingLaunchSpawner = (Service)(nil)

var Module = fx.Module("thread",
	fx.Provide(
		fx.Annotate(
			NewServiceWithPromptAssemblyAndSharedFiles,
			fx.ParamTags("", `optional:"true"`, `optional:"true"`, `optional:"true"`, `optional:"true"`, `optional:"true"`, `optional:"true"`, `optional:"true"`, `optional:"true"`, `optional:"true"`, `optional:"true"`, `optional:"true"`, `optional:"true"`, `optional:"true"`, `optional:"true"`, `optional:"true"`),
			fx.As(new(Service)),
			fx.As(new(contract.PendingLaunchSpawner)),
		),
		fx.Annotate(NewThreadHandlers, fx.ParamTags("", `optional:"true"`)),
		provideThreadConcreteOutputs,
		NewThreadSubscribers,
		provideCronThreadStarter,
	),
	fx.Provide(fx.Annotate(threadBusWorkersAsRunner, fx.ResultTags(`group:"runners"`))),
	fx.Provide(NewBindingRecoveryReporter, NewThreadLister, NewThreadConfigReader, NewThreadRuntimeConfigReader),
)

func provideThreadConcreteOutputs(svc Service) (*service, contract.ThreadStateConfigReader) {
	concrete, _ := svc.(*service)
	return concrete, concrete
}

func provideCronThreadStarter(svc Service) contract.CronThreadStarter {
	return NewCronStarterAdapter(svc)
}

type threadStoreRecord = ThreadRecord
type threadStoreStatusUpdate = ThreadStatusUpdate
type bindingStoreRecord = BindingRecord
type bindingStoreArchiveUpdate = BindingArchiveUpdate
type threadBindingStoreRecord = BindingRecord
type threadConfigStoreRecord = ThreadRecord
type threadBindingStoreAdapter struct {
	store BindingStore
}

func (s *service) threadBindingStorePort() threadBindingStorePort {
	if s == nil || s.bindingStore == nil {
		return nil
	}
	return threadBindingStoreAdapter{store: s.bindingStore}
}

func newThreadBindingStorePort(store BindingStore) threadBindingStorePort {
	if store == nil {
		return nil
	}
	return threadBindingStoreAdapter{store: store}
}

// GetByProviderThread 按 provider thread 查询 store 记录，并转换为 thread 本地 binding DTO。
func (a threadBindingStoreAdapter) GetByProviderThread(ctx context.Context, provider, providerThreadID string) (*threadBindingRecord, error) {
	binding, err := a.store.GetByProviderThread(ctx, provider, providerThreadID)
	return threadBindingRecordFromStore(binding), err
}

// Upsert 将 thread 本地 binding 写入参数显式转换为 store 参数后委托持久化。
func (a threadBindingStoreAdapter) Upsert(ctx context.Context, params threadBindingUpsertParams) error {
	return a.store.Upsert(ctx, bindingUpsertParamsToStore(params))
}

// DeleteByAgentID 通过 adapter 委托删除指定 agent 的 binding，保持业务文件只依赖本地端口。
func (a threadBindingStoreAdapter) DeleteByAgentID(ctx context.Context, agentID string) error {
	return a.store.DeleteByAgentID(ctx, agentID)
}

// UpdateSessionUUID 将本地 session UUID 更新参数转换为 store 参数后写入。
func (a threadBindingStoreAdapter) UpdateSessionUUID(ctx context.Context, params threadBindingSessionUUIDUpdate) error {
	return a.store.UpdateSessionUUID(ctx, bindingSessionUUIDUpdateToStore(params))
}

// UpdateProviderThreadID 将本地 provider thread 更新参数转换为 store 参数后写入。
func (a threadBindingStoreAdapter) UpdateProviderThreadID(ctx context.Context, params threadBindingProviderThreadIDUpdate) error {
	return a.store.UpdateProviderThreadID(ctx, bindingProviderThreadIDUpdateToStore(params))
}

// GetByAgentID 按 agent 查询 store binding，并逐字段转换为 thread 本地 DTO。
func (a threadBindingStoreAdapter) GetByAgentID(ctx context.Context, agentID string) (*threadBindingRecord, error) {
	binding, err := a.store.GetByAgentID(ctx, agentID)
	return threadBindingRecordFromStore(binding), err
}

// ListAgentThreadBindings 批量读取 store binding 列表，并返回 thread 本地 DTO 切片。
func (a threadBindingStoreAdapter) ListAgentThreadBindings(ctx context.Context) ([]threadBindingRecord, error) {
	bindings, err := a.store.ListAgentThreadBindings(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]threadBindingRecord, 0, len(bindings))
	for i := range bindings {
		out = append(out, *threadBindingRecordFromStore(&bindings[i]))
	}
	return out, nil
}

// UpdateAgentCwd 将本地 CWD 更新参数转换为 store 参数后委托写入。
func (a threadBindingStoreAdapter) UpdateAgentCwd(ctx context.Context, params threadBindingCWDUpdate) error {
	return a.store.UpdateAgentCwd(ctx, bindingCWDUpdateToStore(params))
}

func threadBindingRecordFromStore(binding *BindingRecord) *threadBindingRecord {
	if binding == nil {
		return nil
	}
	return &threadBindingRecord{
		AgentID:            binding.AgentID,
		Provider:           binding.Provider,
		ProviderThreadID:   binding.ProviderThreadID,
		CodexThreadID:      binding.CodexThreadID,
		RolloutPath:        binding.RolloutPath,
		Cwd:                binding.Cwd,
		ParentAgentID:      binding.ParentAgentID,
		AgentType:          binding.AgentType,
		AgentMemoryScope:   binding.AgentMemoryScope,
		Archived:           binding.Archived,
		CreatedAt:          binding.CreatedAt,
		UpdatedAt:          binding.UpdatedAt,
		SessionUUID:        binding.SessionUUID,
		CodexHome:          binding.CodexHome,
		CodexInstanceKey:   binding.CodexInstanceKey,
		CodexModelProvider: binding.CodexModelProvider,
	}
}

// threadBindingRecordToStore 只在 module adapter 边界把本地 binding DTO 转回 store DTO。
func threadBindingRecordToStore(binding *threadBindingRecord) *BindingRecord {
	if binding == nil {
		return nil
	}
	return &BindingRecord{
		AgentID:            binding.AgentID,
		Provider:           binding.Provider,
		ProviderThreadID:   binding.ProviderThreadID,
		CodexThreadID:      binding.CodexThreadID,
		RolloutPath:        binding.RolloutPath,
		Cwd:                binding.Cwd,
		ParentAgentID:      binding.ParentAgentID,
		AgentType:          binding.AgentType,
		AgentMemoryScope:   binding.AgentMemoryScope,
		Archived:           binding.Archived,
		CreatedAt:          binding.CreatedAt,
		UpdatedAt:          binding.UpdatedAt,
		SessionUUID:        binding.SessionUUID,
		CodexHome:          binding.CodexHome,
		CodexInstanceKey:   binding.CodexInstanceKey,
		CodexModelProvider: binding.CodexModelProvider,
	}
}

func bindingRecordHasProviderHistoryForUUID(binding *threadBindingRecord, providerThreadID string) bool {
	return bindingHasProviderHistoryForUUID(threadBindingRecordToStore(binding), providerThreadID)
}

func historyTargetIDRecord(binding *threadBindingRecord, threadID string) string {
	return historyTargetID(threadBindingRecordToStore(binding), threadID)
}

func bindingProvider(binding *BindingRecord) string {
	return bindingRecordProvider(threadBindingRecordFromStore(binding))
}

func (s *service) resolveBindingChain(ctx context.Context, threadID string) (*BindingRecord, error) {
	binding, err := s.resolveBindingChainRecord(ctx, threadID)
	return threadBindingRecordToStore(binding), err
}

// resolveThreadBindingRecord 将既有 binding 解析结果转换为本地 DTO，供本 lane 的 event 路径使用。
func (s *service) resolveThreadBindingRecord(ctx context.Context, threadID string) (*threadBindingRecord, error) {
	binding, err := s.resolveBinding(ctx, threadID)
	return threadBindingRecordFromStore(binding), err
}

func bindingUpsertParamsToStore(params threadBindingUpsertParams) BindingUpsert {
	return BindingUpsert{
		AgentID:            params.AgentID,
		Provider:           params.Provider,
		ProviderThreadID:   params.ProviderThreadID,
		CodexThreadID:      params.CodexThreadID,
		RolloutPath:        params.RolloutPath,
		SessionUUID:        params.SessionUUID,
		Cwd:                params.Cwd,
		ParentAgentID:      params.ParentAgentID,
		AgentType:          params.AgentType,
		AgentMemoryScope:   params.AgentMemoryScope,
		CreatedAt:          params.CreatedAt,
		UpdatedAt:          params.UpdatedAt,
		CodexHome:          params.CodexHome,
		CodexInstanceKey:   params.CodexInstanceKey,
		CodexModelProvider: params.CodexModelProvider,
	}
}

func bindingSessionUUIDUpdateToStore(params threadBindingSessionUUIDUpdate) BindingSessionUUIDUpdate {
	return BindingSessionUUIDUpdate{
		SessionUUID: params.SessionUUID,
		UpdatedAt:   params.UpdatedAt,
		AgentID:     params.AgentID,
	}
}

func bindingProviderThreadIDUpdateToStore(params threadBindingProviderThreadIDUpdate) BindingProviderThreadIDUpdate {
	return BindingProviderThreadIDUpdate{
		ProviderThreadID: params.ProviderThreadID,
		UpdatedAt:        params.UpdatedAt,
		AgentID:          params.AgentID,
	}
}

func bindingCWDUpdateToStore(params threadBindingCWDUpdate) BindingCWDUpdate {
	return BindingCWDUpdate{
		AgentID:   params.AgentID,
		Cwd:       params.Cwd,
		UpdatedAt: params.UpdatedAt,
	}
}

type threadConfigStoreAdapter struct {
	store ThreadStore
}

func (s *service) threadConfigStorePort() threadConfigStorePort {
	if s == nil || s.threadStore == nil {
		return nil
	}
	return threadConfigStoreAdapter{store: s.threadStore}
}

// GetByThreadID 按 thread id 查询 store 配置，并转换为 history 使用的本地 thread DTO。
func (a threadConfigStoreAdapter) GetByThreadID(ctx context.Context, threadID string) (*threadConfigRecord, error) {
	thread, err := a.store.GetByThreadID(ctx, threadID)
	return threadConfigRecordFromStore(thread), err
}

// ListConfigsByIDs 批量查询 store thread 配置，并逐项转换为本地 DTO。
func (a threadConfigStoreAdapter) ListConfigsByIDs(ctx context.Context, threadIDs []string) ([]threadConfigRecord, error) {
	threads, err := a.store.ListConfigsByIDs(ctx, threadIDs)
	if err != nil {
		return nil, err
	}
	out := make([]threadConfigRecord, 0, len(threads))
	for i := range threads {
		out = append(out, *threadConfigRecordFromStore(&threads[i]))
	}
	return out, nil
}

// threadConfigRecordFromStore 将 store thread DTO 逐字段复制为 thread 本地 DTO，避免业务路径继续透传 store 类型。
func threadConfigRecordFromStore(thread *ThreadRecord) *threadConfigRecord {
	if thread == nil {
		return nil
	}
	return &threadConfigRecord{
		ThreadID:         thread.ThreadID,
		AgentID:          thread.AgentID,
		ParentAgentID:    thread.ParentAgentID,
		AgentType:        thread.AgentType,
		AgentMemoryScope: thread.AgentMemoryScope,
		Name:             thread.Name,
		Prompt:           thread.Prompt,
		Model:            thread.Model,
		Cwd:              thread.Cwd,
		Status:           thread.Status,
		Port:             thread.Port,
		PID:              thread.PID,
		CreatedAt:        thread.CreatedAt,
		UpdatedAt:        thread.UpdatedAt,
		FinishedAt:       thread.FinishedAt,
		LastEventType:    thread.LastEventType,
		ErrorMessage:     thread.ErrorMessage,
		WorkspaceRunKey:  thread.WorkspaceRunKey,
		OwnerThreadID:    thread.OwnerThreadID,
		ConfigOverride:   thread.ConfigOverride,
		AgentKey:         thread.AgentKey,
		PromptVersionID:  thread.PromptVersionID,
		PendingLaunch:    thread.PendingLaunch,
		ManuallyRenamed:  thread.ManuallyRenamed,
	}
}

func (s *service) buildOfflineConfig(ctx context.Context, threadID string, binding *BindingRecord) (offlineConfigSnapshot, error) {
	return s.buildOfflineConfigRecord(ctx, threadID, threadBindingRecordFromStore(binding))
}

func (s *service) offlineRuntimeConfigForMissingSession(ctx context.Context, threadID string, binding *BindingRecord, resolveErr error) (map[string]any, bool, error) {
	return s.offlineRuntimeConfigForMissingSessionRecord(ctx, threadID, threadBindingRecordFromStore(binding), resolveErr)
}

func (s *service) cleanupThreadScratchpad(ctx context.Context, threadID string, binding *BindingRecord) {
	s.cleanupThreadScratchpadRecord(ctx, threadID, threadBindingRecordFromStore(binding))
}

func newThreadUpsertParams(thread ThreadRecord) ThreadUpsert {
	return ThreadUpsert{
		ThreadID:         strings.TrimSpace(thread.ThreadID),
		Name:             strings.TrimSpace(util.FirstNonEmpty(thread.Name, thread.Prompt)),
		Prompt:           strings.TrimSpace(thread.Prompt),
		Model:            strings.TrimSpace(thread.Model),
		Cwd:              strings.TrimSpace(thread.Cwd),
		Status:           strings.TrimSpace(thread.Status),
		Port:             thread.Port,
		PID:              thread.PID,
		CreatedAt:        thread.CreatedAt,
		UpdatedAt:        thread.UpdatedAt,
		OwnerThreadID:    strings.TrimSpace(thread.OwnerThreadID),
		ParentAgentID:    strings.TrimSpace(thread.ParentAgentID),
		AgentType:        strings.TrimSpace(thread.AgentType),
		AgentMemoryScope: strings.TrimSpace(thread.AgentMemoryScope),
		ConfigOverride:   thread.ConfigOverride,
		AgentKey:         strings.TrimSpace(thread.AgentKey),
		PromptVersionID:  thread.PromptVersionID,
		PendingLaunch:    thread.PendingLaunch,
		ManuallyRenamed:  thread.ManuallyRenamed,
	}
}

// NewBindingRecoveryReporter 创建会话恢复时回写 binding 的 reporter。
func NewBindingRecoveryReporter(store BindingStore, logger *slog.Logger) contract.SessionRecoveryReporter {
	return &bindingRecoveryReporter{store: newThreadBindingStorePort(store), logger: logger}
}

// savePromptSnapshot 在 assembly 边界把 thread 本地 snapshot DTO 转成 store DTO 后保存。
// snapshot 为空或 thread_id/store 缺失就报错，否则后续 resume/fork 没法稳定恢复。
func (s *service) savePromptSnapshot(ctx context.Context, threadID string, assembly contract.StartAssembly) error {
	threadID = strings.TrimSpace(threadID)
	if s == nil {
		return errors.New("thread: service is not configured")
	}
	if s.threadStore == nil {
		return errors.New("thread: thread store is not configured")
	}
	if threadID == "" {
		return errors.New("thread: prompt snapshot thread_id is required")
	}
	if promptSnapshotBlank(assembly.Snapshot) {
		return errors.New("thread: prompt snapshot is empty")
	}
	return s.threadStore.SavePromptSnapshot(ctx, threadID, promptSnapshotRecordToStore(toStoredPromptSnapshot(assembly.Snapshot)))
}

// loadStoredPromptSnapshot 从 store 读取 snapshot，并在 assembly 边界转回 thread 本地 DTO。
func (s *service) loadStoredPromptSnapshot(ctx context.Context, threadID string) (contract.PromptAssemblySnapshot, error) {
	threadID = strings.TrimSpace(threadID)
	if s == nil {
		return contract.PromptAssemblySnapshot{}, errors.New("thread: service is not configured")
	}
	if s.threadStore == nil {
		return contract.PromptAssemblySnapshot{}, errors.New("thread: thread store is not configured")
	}
	if threadID == "" {
		return contract.PromptAssemblySnapshot{}, errors.New("thread: prompt snapshot thread_id is required")
	}
	snapshot, err := s.threadStore.LoadPromptSnapshot(ctx, threadID)
	if err != nil || snapshot == nil {
		return contract.PromptAssemblySnapshot{}, err
	}
	return fromStoredPromptSnapshot(promptSnapshotRecordFromStore(snapshot)), nil
}

func promptSnapshotRecordToStore(record promptSnapshotRecord) PromptSnapshotRecord {
	return PromptSnapshotRecord{
		DisplayName:           record.DisplayName,
		BaseInstructions:      record.BaseInstructions,
		Boundary:              promptBoundaryRecordToStore(record.Boundary),
		DeveloperInstructions: record.DeveloperInstructions,
		Provider:              record.Provider,
		Version:               record.Version,
		Hash:                  record.Hash,
		SectionSnapshot:       clonePromptSectionMap(record.SectionSnapshot),
		Generation:            record.Generation,
	}
}

func promptBoundaryRecordToStore(boundary *promptBoundaryRecord) *PromptBoundaryRecord {
	if boundary == nil {
		return nil
	}
	return &PromptBoundaryRecord{
		CachedPrefix: boundary.CachedPrefix,
		UncachedTail: boundary.UncachedTail,
	}
}

func promptSnapshotRecordFromStore(snapshot *PromptSnapshotRecord) *promptSnapshotRecord {
	if snapshot == nil {
		return nil
	}
	return &promptSnapshotRecord{
		DisplayName:           snapshot.DisplayName,
		BaseInstructions:      snapshot.BaseInstructions,
		Boundary:              promptBoundaryRecordFromStore(snapshot.Boundary),
		DeveloperInstructions: snapshot.DeveloperInstructions,
		Provider:              snapshot.Provider,
		Version:               snapshot.Version,
		Hash:                  snapshot.Hash,
		SectionSnapshot:       clonePromptSectionMap(snapshot.SectionSnapshot),
		Generation:            snapshot.Generation,
	}
}

func promptBoundaryRecordFromStore(boundary *PromptBoundaryRecord) *promptBoundaryRecord {
	if boundary == nil {
		return nil
	}
	return &promptBoundaryRecord{
		CachedPrefix: boundary.CachedPrefix,
		UncachedTail: boundary.UncachedTail,
	}
}

func resolveDisplayName(ctx context.Context, store ThreadStore, agentID, _ string, currentName string) string {
	name := strings.TrimSpace(currentName)
	if name == defaultThreadName() {
		name = ""
	}
	if store != nil {
		existing, err := store.GetByThreadID(ctx, agentID)
		if err == nil && existing.ManuallyRenamed {
			return strings.TrimSpace(existing.Name)
		}
	}
	return name
}

// PromptCatalog 返回构造层注入的 Thread-owned prompt 端口。
func (s *service) promptCatalogPort() PromptCatalog {
	if s == nil || s.promptCatalog == nil {
		return nil
	}
	return s.promptCatalog
}
