// Package thread 管理 agent thread 的完整生命周期：创建、恢复、fork、归档、删除，
// 以及与 provider session、binding store、prompt assembly 之间的协调。
package thread

import (
	"context"
	"errors"
	"log/slog"
	"strings"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	"github.com/anthropic-ai/super-agent-v3/internal/module/threadprompt"
	bindingstore "github.com/anthropic-ai/super-agent-v3/internal/store/binding"
	promptstore "github.com/anthropic-ai/super-agent-v3/internal/store/prompt"
	threadstore "github.com/anthropic-ai/super-agent-v3/internal/store/thread"
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
		provideThreadServiceStorePort,
		provideBindingServiceStorePort,
		providePromptServiceCatalogPort,
		provideThreadConcreteOutputs,
		provideRuntimePromptCatalog,
		NewThreadSubscribers,
		provideCronThreadStarter,
	),
	fx.Provide(fx.Annotate(threadBusWorkersAsRunner, fx.ResultTags(`group:"runners"`))),
	fx.Provide(NewBindingRecoveryReporter, NewThreadLister, NewThreadConfigReader, NewThreadRuntimeConfigReader),
	fx.Invoke(registerThreadPromptProviders),
)

func provideThreadConcreteOutputs(svc Service) (*service, contract.ThreadStateConfigReader) {
	concrete, _ := svc.(*service)
	return concrete, concrete
}

func provideCronThreadStarter(svc Service) contract.CronThreadStarter {
	return NewCronStarterAdapter(svc)
}

type threadServiceStorePort interface {
	GetByThreadID(ctx context.Context, threadID string) (*threadstore.Thread, error)
	ListAll(ctx context.Context) ([]threadstore.Thread, error)
	ListConfigsByIDs(ctx context.Context, threadIDs []string) ([]threadstore.Thread, error)
	Upsert(ctx context.Context, params threadstore.UpsertParams) error
	SavePromptSnapshot(ctx context.Context, threadID string, snapshot threadstore.PromptSnapshot) error
	LoadPromptSnapshot(ctx context.Context, threadID string) (*threadstore.PromptSnapshot, error)
	UpdateStatus(ctx context.Context, params threadstore.UpdateStatusParams) error
	DeleteByThreadID(ctx context.Context, threadID string) error
	CountChildren(ctx context.Context, parentAgentID string) (int64, error)
	Exists(ctx context.Context, threadID string) (bool, error)
}

type bindingServiceStorePort interface {
	GetByProviderThread(ctx context.Context, provider, providerThreadID string) (*bindingstore.Binding, error)
	Upsert(ctx context.Context, params bindingstore.UpsertParams) error
	DeleteByAgentID(ctx context.Context, agentID string) error
	UpdateSessionUUID(ctx context.Context, params bindingstore.UpdateSessionUUIDParams) error
	UpdateProviderThreadID(ctx context.Context, params bindingstore.UpdateProviderThreadIDParams) error
	SetArchived(ctx context.Context, params bindingstore.SetArchivedParams) error
	GetByAgentID(ctx context.Context, agentID string) (*bindingstore.Binding, error)
	ListAgentThreadBindings(ctx context.Context) ([]bindingstore.Binding, error)
	UpdateAgentCwd(ctx context.Context, params bindingstore.UpdateAgentCwdParams) error
}

type promptServiceCatalogPort any

type threadStoreRecord = threadstore.Thread
type threadStoreStatusUpdate = threadstore.UpdateStatusParams
type bindingStoreRecord = bindingstore.Binding
type bindingStoreArchiveUpdate = bindingstore.SetArchivedParams

type threadServiceStorePortParams struct {
	fx.In
	Store threadstore.Store `optional:"true"`
}

func provideThreadServiceStorePort(params threadServiceStorePortParams) threadServiceStorePort {
	return params.Store
}

type bindingServiceStorePortParams struct {
	fx.In
	Store bindingstore.Store `optional:"true"`
}

func provideBindingServiceStorePort(params bindingServiceStorePortParams) bindingServiceStorePort {
	return params.Store
}

type promptServiceCatalogPortParams struct {
	fx.In
	Catalog promptstore.RuntimePromptCatalog `optional:"true"`
}

func providePromptServiceCatalogPort(params promptServiceCatalogPortParams) promptServiceCatalogPort {
	return params.Catalog
}

type threadPromptProviderParams struct {
	fx.In
	Registrar     contract.DynamicSectionRegistrar `optional:"true"`
	PromptStore   promptstore.Store                `optional:"true"`
	Builtin       contract.BuiltinPromptRegistry   `optional:"true"`
	PromptCatalog promptstore.RuntimePromptCatalog `optional:"true"`
}

func registerThreadPromptProviders(params threadPromptProviderParams) error {
	catalog := params.PromptCatalog
	if catalog == nil {
		catalog = threadprompt.NewRuntimeCatalog(params.PromptStore, params.Builtin)
	}
	return threadprompt.RegisterProviders(params.Registrar, catalog)
}

type runtimePromptCatalogParams struct {
	fx.In
	PromptStore promptstore.Store              `optional:"true"`
	Builtin     contract.BuiltinPromptRegistry `optional:"true"`
}

func provideRuntimePromptCatalog(params runtimePromptCatalogParams) promptstore.RuntimePromptCatalog {
	return threadprompt.NewRuntimeCatalog(params.PromptStore, params.Builtin)
}

type threadBindingStoreRecord = bindingstore.Binding
type threadConfigStoreRecord = threadstore.Thread
type threadBindingStoreAdapter struct {
	store bindingServiceStorePort
}

func (s *service) threadBindingStorePort() threadBindingStorePort {
	if s == nil || s.bindingStore == nil {
		return nil
	}
	return threadBindingStoreAdapter{store: s.bindingStore}
}

func newThreadBindingStorePort(store bindingstore.Store) threadBindingStorePort {
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

func threadBindingRecordFromStore(binding *bindingstore.Binding) *threadBindingRecord {
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
func threadBindingRecordToStore(binding *threadBindingRecord) *bindingstore.Binding {
	if binding == nil {
		return nil
	}
	return &bindingstore.Binding{
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

func bindingProvider(binding *bindingstore.Binding) string {
	return bindingRecordProvider(threadBindingRecordFromStore(binding))
}

func (s *service) resolveBindingChain(ctx context.Context, threadID string) (*bindingstore.Binding, error) {
	binding, err := s.resolveBindingChainRecord(ctx, threadID)
	return threadBindingRecordToStore(binding), err
}

// resolveThreadBindingRecord 将既有 binding 解析结果转换为本地 DTO，供本 lane 的 event 路径使用。
func (s *service) resolveThreadBindingRecord(ctx context.Context, threadID string) (*threadBindingRecord, error) {
	binding, err := s.resolveBinding(ctx, threadID)
	return threadBindingRecordFromStore(binding), err
}

func bindingUpsertParamsToStore(params threadBindingUpsertParams) bindingstore.UpsertParams {
	return bindingstore.UpsertParams{
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

func bindingSessionUUIDUpdateToStore(params threadBindingSessionUUIDUpdate) bindingstore.UpdateSessionUUIDParams {
	return bindingstore.UpdateSessionUUIDParams{
		SessionUUID: params.SessionUUID,
		UpdatedAt:   params.UpdatedAt,
		AgentID:     params.AgentID,
	}
}

func bindingProviderThreadIDUpdateToStore(params threadBindingProviderThreadIDUpdate) bindingstore.UpdateProviderThreadIDParams {
	return bindingstore.UpdateProviderThreadIDParams{
		ProviderThreadID: params.ProviderThreadID,
		UpdatedAt:        params.UpdatedAt,
		AgentID:          params.AgentID,
	}
}

func bindingCWDUpdateToStore(params threadBindingCWDUpdate) bindingstore.UpdateAgentCwdParams {
	return bindingstore.UpdateAgentCwdParams{
		AgentID:   params.AgentID,
		Cwd:       params.Cwd,
		UpdatedAt: params.UpdatedAt,
	}
}

type threadConfigStoreAdapter struct {
	store threadServiceStorePort
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
func threadConfigRecordFromStore(thread *threadstore.Thread) *threadConfigRecord {
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

func (s *service) buildOfflineConfig(ctx context.Context, threadID string, binding *bindingstore.Binding) (offlineConfigSnapshot, error) {
	return s.buildOfflineConfigRecord(ctx, threadID, threadBindingRecordFromStore(binding))
}

func (s *service) offlineRuntimeConfigForMissingSession(ctx context.Context, threadID string, binding *bindingstore.Binding, resolveErr error) (map[string]any, bool, error) {
	return s.offlineRuntimeConfigForMissingSessionRecord(ctx, threadID, threadBindingRecordFromStore(binding), resolveErr)
}

func (s *service) cleanupThreadScratchpad(ctx context.Context, threadID string, binding *bindingstore.Binding) {
	s.cleanupThreadScratchpadRecord(ctx, threadID, threadBindingRecordFromStore(binding))
}

func newThreadUpsertParams(thread threadstore.Thread) threadstore.UpsertParams {
	return threadstore.UpsertParams{
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
func NewBindingRecoveryReporter(store bindingstore.Store, logger *slog.Logger) contract.SessionRecoveryReporter {
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

func promptSnapshotRecordToStore(record promptSnapshotRecord) threadstore.PromptSnapshot {
	return threadstore.PromptSnapshot{
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

func promptBoundaryRecordToStore(boundary *promptBoundaryRecord) *threadstore.PromptBoundary {
	if boundary == nil {
		return nil
	}
	return &threadstore.PromptBoundary{
		CachedPrefix: boundary.CachedPrefix,
		UncachedTail: boundary.UncachedTail,
	}
}

func promptSnapshotRecordFromStore(snapshot *threadstore.PromptSnapshot) *promptSnapshotRecord {
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

func promptBoundaryRecordFromStore(boundary *threadstore.PromptBoundary) *promptBoundaryRecord {
	if boundary == nil {
		return nil
	}
	return &promptBoundaryRecord{
		CachedPrefix: boundary.CachedPrefix,
		UncachedTail: boundary.UncachedTail,
	}
}

func resolveDisplayName(ctx context.Context, store threadServiceStorePort, agentID, _ string, currentName string) string {
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

// runtimePromptCatalog 把构造层注入的 prompt catalog 收窄成本包本地端口。
// 路由文件只接触 runtimePromptCatalog，真实 store DTO 在这个 adapter 边界显式复制。
func (s *service) runtimePromptCatalog() runtimePromptCatalog {
	if s == nil || s.promptCatalog == nil {
		return nil
	}
	catalog, ok := s.promptCatalog.(promptstore.RuntimePromptCatalog)
	if !ok {
		return nil
	}
	return promptStoreRuntimeCatalogAdapter{catalog: catalog}
}

type promptStoreRuntimeCatalogAdapter struct {
	catalog promptstore.RuntimePromptCatalog
}

// ListTemplates 读取运行时模板并转换成 thread 本地 DTO。
func (a promptStoreRuntimeCatalogAdapter) ListTemplates(ctx context.Context, filter runtimePromptListFilter) ([]runtimePromptTemplate, error) {
	rows, err := a.catalog.ListTemplates(ctx, promptstore.RuntimeListFilter{
		AgentKey: filter.AgentKey,
		Keyword:  filter.Keyword,
		CWD:      filter.CWD,
		Limit:    filter.Limit,
	})
	if err != nil {
		return nil, err
	}
	return runtimePromptTemplatesFromStore(rows), nil
}

// ListSectionsByTemplateID 读取模板 section 并转换成 thread 本地 DTO。
func (a promptStoreRuntimeCatalogAdapter) ListSectionsByTemplateID(ctx context.Context, templateID int64) ([]runtimePromptTemplateSection, error) {
	rows, err := a.catalog.ListSectionsByTemplateID(ctx, templateID)
	if err != nil {
		return nil, err
	}
	return runtimePromptSectionsFromStore(rows), nil
}

// InsertVersion 将 thread 本地版本 DTO 转回 prompt catalog 写入边界。
func (a promptStoreRuntimeCatalogAdapter) InsertVersion(ctx context.Context, version runtimePromptTemplateVersion) (int64, error) {
	return a.catalog.InsertVersion(ctx, runtimePromptVersionToStore(version))
}

// CanInsertPromptVersion 透传底层 catalog 的写能力标记，缺省保持可写以兼容旧实现。
func (a promptStoreRuntimeCatalogAdapter) CanInsertPromptVersion() bool {
	checker, ok := a.catalog.(promptVersionInsertCapability)
	return !ok || checker.CanInsertPromptVersion()
}

func runtimePromptTemplatesFromStore(rows []promptstore.PromptTemplate) []runtimePromptTemplate {
	out := make([]runtimePromptTemplate, 0, len(rows))
	for _, row := range rows {
		out = append(out, runtimePromptTemplateFromStore(row))
	}
	return out
}

func runtimePromptTemplateFromStore(row promptstore.PromptTemplate) runtimePromptTemplate {
	return runtimePromptTemplate{
		ID:             row.ID,
		PromptKey:      row.PromptKey,
		Title:          row.Title,
		AgentKey:       row.AgentKey,
		ToolName:       row.ToolName,
		PromptText:     row.PromptText,
		WhenToUse:      row.WhenToUse,
		Variables:      cloneRuntimePromptJSON(row.Variables),
		Tags:           cloneRuntimePromptJSON(row.Tags),
		Enabled:        row.Enabled,
		ManuallyEdited: row.ManuallyEdited,
		MatchWhen:      cloneRuntimePromptJSON(row.MatchWhen),
		Priority:       row.Priority,
		CreatedBy:      row.CreatedBy,
		UpdatedBy:      row.UpdatedBy,
		CreatedAt:      row.CreatedAt,
		UpdatedAt:      row.UpdatedAt,
		Description:    row.Description,
	}
}

func runtimePromptSectionsFromStore(rows []promptstore.PromptTemplateSection) []runtimePromptTemplateSection {
	out := make([]runtimePromptTemplateSection, 0, len(rows))
	for _, row := range rows {
		out = append(out, runtimePromptSectionFromStore(row))
	}
	return out
}

func runtimePromptSectionFromStore(row promptstore.PromptTemplateSection) runtimePromptTemplateSection {
	return runtimePromptTemplateSection{
		ID:                  row.ID,
		TemplateID:          row.TemplateID,
		SectionKey:          row.SectionKey,
		Region:              row.Region,
		Ordinal:             row.Ordinal,
		Body:                row.Body,
		EnableWhen:          cloneRuntimePromptJSON(row.EnableWhen),
		Enabled:             row.Enabled,
		TriggerType:         row.TriggerType,
		RecallTopic:         row.RecallTopic,
		TemplatePromptKey:   row.TemplatePromptKey,
		TemplateTitle:       row.TemplateTitle,
		TemplateDescription: row.TemplateDescription,
		TemplateWhenToUse:   row.TemplateWhenToUse,
		TemplateTags:        cloneRuntimePromptJSON(row.TemplateTags),
		CreatedAt:           row.CreatedAt,
		UpdatedAt:           row.UpdatedAt,
	}
}

func runtimePromptVersionToStore(row runtimePromptTemplateVersion) promptstore.PromptTemplateVersion {
	return promptstore.PromptTemplateVersion{
		ID:              row.ID,
		PromptKey:       row.PromptKey,
		Title:           row.Title,
		AgentKey:        row.AgentKey,
		ToolName:        row.ToolName,
		PromptText:      row.PromptText,
		Variables:       cloneRuntimePromptJSON(row.Variables),
		Tags:            cloneRuntimePromptJSON(row.Tags),
		Description:     row.Description,
		Enabled:         row.Enabled,
		CreatedBy:       row.CreatedBy,
		UpdatedBy:       row.UpdatedBy,
		SourceUpdatedAt: row.SourceUpdatedAt,
		CreatedAt:       row.CreatedAt,
		ArchivedAt:      row.ArchivedAt,
	}
}

func cloneRuntimePromptJSON(raw []byte) []byte {
	if len(raw) == 0 {
		return nil
	}
	return append([]byte(nil), raw...)
}
