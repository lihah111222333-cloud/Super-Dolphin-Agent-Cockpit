package thread

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	platformdb "github.com/anthropic-ai/super-agent-v3/internal/platform/db"
	"github.com/anthropic-ai/super-agent-v3/internal/store/sqlc"
)

// querier 描述 thread store 依赖的 sqlc 查询集合。
// 测试可用窄接口替换真实 *sqlc.Queries，生产路径仍由 NewStore 注入完整查询器。
type querier interface {
	AgentThreadRunningExists(ctx context.Context, arg sqlc.AgentThreadRunningExistsParams) (int64, error)
	AgentThreadExists(ctx context.Context, arg sqlc.AgentThreadExistsParams) (int64, error)
	CountAllThreads(ctx context.Context) (int64, error)
	CountChildAgentThreads(ctx context.Context, arg sqlc.CountChildAgentThreadsParams) (int64, error)
	DeleteAgentThreadByID(ctx context.Context, arg sqlc.DeleteAgentThreadByIDParams) error
	ExpireStaleAgentThreads(ctx context.Context, arg sqlc.ExpireStaleAgentThreadsParams) (int64, error)
	GetAgentThreadByID(ctx context.Context, arg sqlc.GetAgentThreadByIDParams) (sqlc.GetAgentThreadByIDRow, error)
	GetAgentThreadByPort(ctx context.Context, arg sqlc.GetAgentThreadByPortParams) (sqlc.GetAgentThreadByPortRow, error)
	ListAgentThreadConfigsByIDs(ctx context.Context, arg sqlc.ListAgentThreadConfigsByIDsParams) ([]sqlc.ListAgentThreadConfigsByIDsRow, error)
	ListAgentThreadCwds(ctx context.Context) ([]sqlc.ListAgentThreadCwdsRow, error)
	ListAgentThreadCwdsByPrefix(ctx context.Context, arg sqlc.ListAgentThreadCwdsByPrefixParams) ([]sqlc.ListAgentThreadCwdsByPrefixRow, error)
	ListAgentThreads(ctx context.Context) ([]sqlc.ListAgentThreadsRow, error)
	ListRecoverableAgentThreads(ctx context.Context) ([]sqlc.ListRecoverableAgentThreadsRow, error)
	ListRunningAgentThreads(ctx context.Context) ([]sqlc.ListRunningAgentThreadsRow, error)
	ListRunningAgents(ctx context.Context) ([]sqlc.ListRunningAgentsRow, error)
	LoadAgentThreadPromptSnapshot(ctx context.Context, arg sqlc.LoadAgentThreadPromptSnapshotParams) (json.RawMessage, error)
	ResetRunningAgentThreads(ctx context.Context) error
	UpdateAgentThreadPromptSnapshot(ctx context.Context, arg sqlc.UpdateAgentThreadPromptSnapshotParams) (int64, error)
	UpdateAgentThreadStatus(ctx context.Context, arg sqlc.UpdateAgentThreadStatusParams) error
	UpdateAgentThreadLaunchResult(ctx context.Context, arg sqlc.UpdateAgentThreadLaunchResultParams) error
	UpsertAgentThread(ctx context.Context, arg sqlc.UpsertAgentThreadParams) error
}

// store 实现线程持久化边界，负责 SQLC 行与 domain DTO 的双向映射。
type store struct {
	q querier
}

type pagedStore struct {
	*store
}

type agentThreadPageQuerier interface {
	ListAgentThreadsPage(ctx context.Context, arg sqlc.ListAgentThreadsPageParams) ([]sqlc.ListAgentThreadsPageRow, error)
}

type loadedAgentThreadPageQuerier interface {
	ListLoadedAgentThreadsPage(ctx context.Context, arg sqlc.ListLoadedAgentThreadsPageParams) ([]sqlc.ListLoadedAgentThreadsPageRow, error)
}

type activeAgentThreadCountQuerier interface {
	CountActiveAgentThreads(ctx context.Context) (int64, error)
}

// NewStore 创建 sqlc 支撑的线程 store。
// 调用方必须传入已初始化的查询器，这里不做 nil 兜底，避免启动配置错误延后暴露。
func NewStore(q *sqlc.Queries) Store {
	return &pagedStore{store: &store{q: q}}
}

// GetByThreadID 按线程ID读取线程记录，并统一包装底层数据库错误。
func (s *store) GetByThreadID(ctx context.Context, threadID string) (*Thread, error) {
	row, err := s.q.GetAgentThreadByID(ctx, sqlc.GetAgentThreadByIDParams{ThreadID: threadID})
	if err != nil {
		return nil, wrapThreadError(err, "get_by_thread_id")
	}
	result := mapThreadByID(row)
	return &result, nil
}

// GetByPort 按本地端口读取线程记录，用于桌面进程恢复和端口反查。
func (s *store) GetByPort(ctx context.Context, port int32) (*Thread, error) {
	row, err := s.q.GetAgentThreadByPort(ctx, sqlc.GetAgentThreadByPortParams{Port: int64(port)})
	if err != nil {
		return nil, wrapThreadError(err, "get_by_port")
	}
	result := mapThreadByPort(row)
	return &result, nil
}

// ListAll 列出全部线程记录，保留完整 config_override 供上层自行裁剪。
func (s *store) ListAll(ctx context.Context) ([]Thread, error) {
	rows, err := s.q.ListAgentThreads(ctx)
	if err != nil {
		return nil, wrapThreadError(err, "list_all")
	}
	return mapThreadList(rows), nil
}

// ListPage 使用 keyset cursor 读取有限 thread 列表页。
func (s *pagedStore) ListPage(ctx context.Context, params contract.ThreadListPageParams) (contract.ThreadListPage, error) {
	limit, err := validateStoreThreadPageLimit(params.Limit)
	if err != nil {
		return contract.ThreadListPage{}, err
	}
	q, ok := s.q.(agentThreadPageQuerier)
	if !ok {
		return contract.ThreadListPage{}, wrapThreadError(errors.New("list page query is not configured"), "list_page")
	}
	rows, err := q.ListAgentThreadsPage(ctx, sqlc.ListAgentThreadsPageParams{
		CursorCreatedAt: params.CursorCreatedAt,
		CursorThreadID:  params.CursorThreadID,
		Limit:           int64(limit),
	})
	if err != nil {
		return contract.ThreadListPage{}, wrapThreadError(err, "list_page")
	}
	return buildThreadPage(mapThreadPageRows(rows), limit), nil
}

// ListLoadedPage 使用 SQL status 过滤读取已加载 thread 列表页。
func (s *pagedStore) ListLoadedPage(ctx context.Context, params contract.ThreadListPageParams) (contract.ThreadListPage, error) {
	limit, err := validateStoreThreadPageLimit(params.Limit)
	if err != nil {
		return contract.ThreadListPage{}, err
	}
	q, ok := s.q.(loadedAgentThreadPageQuerier)
	if !ok {
		return contract.ThreadListPage{}, wrapThreadError(errors.New("loaded list page query is not configured"), "list_loaded_page")
	}
	rows, err := q.ListLoadedAgentThreadsPage(ctx, sqlc.ListLoadedAgentThreadsPageParams{
		CursorCreatedAt: params.CursorCreatedAt,
		CursorThreadID:  params.CursorThreadID,
		Limit:           int64(limit),
	})
	if err != nil {
		return contract.ThreadListPage{}, wrapThreadError(err, "list_loaded_page")
	}
	return buildThreadPage(mapLoadedThreadPageRows(rows), limit), nil
}

// CountActive 使用数据库聚合统计仍处于 active 状态的 thread。
func (s *pagedStore) CountActive(ctx context.Context) (int64, error) {
	q, ok := s.q.(activeAgentThreadCountQuerier)
	if !ok {
		return 0, wrapThreadError(errors.New("active count query is not configured"), "count_active")
	}
	count, err := q.CountActiveAgentThreads(ctx)
	if err != nil {
		return 0, wrapThreadError(err, "count_active")
	}
	return count, nil
}

// ListConfigsByIDs 只批量读取线程配置字段，避免恢复路径拉取不需要的大行。
func (s *store) ListConfigsByIDs(ctx context.Context, threadIDs []string) ([]Thread, error) {
	rows, err := s.q.ListAgentThreadConfigsByIDs(ctx, sqlc.ListAgentThreadConfigsByIDsParams{ThreadIds: threadIDs})
	if err != nil {
		return nil, wrapThreadError(err, "list_configs_by_ids")
	}
	return mapConfigList(rows), nil
}

// ListRunning 列出仍处于运行状态的线程记录。
func (s *store) ListRunning(ctx context.Context) ([]Thread, error) {
	rows, err := s.q.ListRunningAgentThreads(ctx)
	if err != nil {
		return nil, wrapThreadError(err, "list_running")
	}
	return mapRunningThreadList(rows), nil
}

// ListRecoverable 列出可恢复线程，供启动后的会话恢复扫描使用。
func (s *store) ListRecoverable(ctx context.Context) ([]Thread, error) {
	rows, err := s.q.ListRecoverableAgentThreads(ctx)
	if err != nil {
		return nil, wrapThreadError(err, "list_recoverable")
	}
	return mapRecoverableThreadList(rows), nil
}

// ListRunningAgents 列出运行中 agent 的轻量状态，用于 UI 和生命周期计数。
func (s *store) ListRunningAgents(ctx context.Context) ([]RunningAgent, error) {
	rows, err := s.q.ListRunningAgents(ctx)
	if err != nil {
		return nil, wrapThreadError(err, "list_running_agents")
	}
	result := make([]RunningAgent, len(rows))
	for i, row := range rows {
		result[i] = RunningAgent{
			ThreadID: row.ThreadID,
			Port:     int32(row.Port),
			PID:      int32(row.Pid),
			Status:   row.Status,
		}
	}
	return result, nil
}

// Upsert 写入或刷新线程记录；bool 字段在 SQLite 层按 0/1 存储。
func (s *store) Upsert(ctx context.Context, params UpsertParams) error {
	return wrapThreadError(s.q.UpsertAgentThread(ctx, sqlc.UpsertAgentThreadParams{
		ThreadID:         params.ThreadID,
		Name:             params.Name,
		Prompt:           params.Prompt,
		Model:            params.Model,
		CWD:              params.Cwd,
		Status:           params.Status,
		Port:             int64(params.Port),
		Pid:              int64(params.PID),
		CreatedAt:        params.CreatedAt,
		UpdatedAt:        params.UpdatedAt,
		OwnerThreadID:    params.OwnerThreadID,
		ParentAgentID:    params.ParentAgentID,
		AgentType:        params.AgentType,
		AgentMemoryScope: params.AgentMemoryScope,
		ConfigOverride:   params.ConfigOverride,
		AgentKey:         params.AgentKey,
		PromptVersionID:  params.PromptVersionID,
		PendingLaunch:    boolToInt64(params.PendingLaunch),
		ManuallyRenamed:  boolToInt64(params.ManuallyRenamed),
	}), "upsert")
}

// SavePromptSnapshot 保存线程的 prompt 快照，线程不存在时返回 store not found 错误。
func (s *store) SavePromptSnapshot(ctx context.Context, threadID string, snapshot PromptSnapshot) error {
	if snapshot.SectionSnapshot == nil {
		snapshot.SectionSnapshot = map[string]string{}
	}
	payload, err := json.Marshal(snapshot)
	if err != nil {
		return wrapThreadError(err, "save_prompt_snapshot")
	}
	rows, err := s.q.UpdateAgentThreadPromptSnapshot(ctx, sqlc.UpdateAgentThreadPromptSnapshotParams{
		ThreadID:       threadID,
		PromptSnapshot: payload,
	})
	if err != nil {
		return wrapThreadError(err, "save_prompt_snapshot")
	}
	if rows == 0 {
		return wrapThreadError(platformdb.ErrNotFound, "save_prompt_snapshot")
	}
	return nil
}

// LoadPromptSnapshot 读取 prompt 快照，空载荷和 JSON null 都按没有快照处理。
func (s *store) LoadPromptSnapshot(ctx context.Context, threadID string) (*PromptSnapshot, error) {
	payload, err := s.q.LoadAgentThreadPromptSnapshot(ctx, sqlc.LoadAgentThreadPromptSnapshotParams{ThreadID: threadID})
	if err != nil {
		return nil, wrapThreadError(err, "load_prompt_snapshot")
	}
	if len(payload) == 0 {
		return nil, nil
	}
	var snapshot *PromptSnapshot
	if err := json.Unmarshal(payload, &snapshot); err != nil {
		return nil, wrapThreadError(err, "load_prompt_snapshot")
	}
	if snapshot == nil {
		return nil, nil
	}
	if snapshot.SectionSnapshot == nil {
		snapshot.SectionSnapshot = map[string]string{}
	}
	return snapshot, nil
}

// UpdateStatus 更新线程状态和更新时间。
func (s *store) UpdateStatus(ctx context.Context, params UpdateStatusParams) error {
	return wrapThreadError(s.q.UpdateAgentThreadStatus(ctx, sqlc.UpdateAgentThreadStatusParams{
		ThreadID:  params.ThreadID,
		Status:    params.Status,
		UpdatedAt: params.UpdatedAt,
	}), "update_status")
}

// UpdateLaunchResult 回写启动后生成的 agent key 与 prompt 版本。
func (s *store) UpdateLaunchResult(ctx context.Context, params UpdateLaunchResultParams) error {
	return wrapThreadError(s.q.UpdateAgentThreadLaunchResult(ctx, sqlc.UpdateAgentThreadLaunchResultParams{
		ThreadID:        params.ThreadID,
		AgentKey:        params.AgentKey,
		PromptVersionID: params.PromptVersionID,
		UpdatedAt:       params.UpdatedAt,
	}), "update_launch_result")
}

// DeleteByThreadID 按线程ID删除线程记录。
func (s *store) DeleteByThreadID(ctx context.Context, threadID string) error {
	return wrapThreadError(s.q.DeleteAgentThreadByID(ctx, sqlc.DeleteAgentThreadByIDParams{ThreadID: threadID}), "delete_by_thread_id")
}

// ResetRunning 在进程启动恢复前清理遗留运行态。
func (s *store) ResetRunning(ctx context.Context) error {
	return wrapThreadError(s.q.ResetRunningAgentThreads(ctx), "reset_running")
}

// ExpireStale 将超过 cutoff 的 pending/running 线程批量过期。
func (s *store) ExpireStale(ctx context.Context, params ExpireStaleParams) (int64, error) {
	count, err := s.q.ExpireStaleAgentThreads(ctx, sqlc.ExpireStaleAgentThreadsParams{
		UpdatedAt:   params.UpdatedAt,
		UpdatedAt_2: params.Cutoff,
	})
	if err != nil {
		return 0, wrapThreadError(err, "expire_stale")
	}
	return count, nil
}

// RunningExists 判断指定线程是否仍处于运行态。
func (s *store) RunningExists(ctx context.Context, threadID string) (bool, error) {
	v, err := s.q.AgentThreadRunningExists(ctx, sqlc.AgentThreadRunningExistsParams{ThreadID: threadID})
	if err != nil {
		return false, wrapThreadError(err, "running_exists")
	}
	return v != 0, nil
}

// ListCwds 列出线程工作目录，用于 UI 项目范围和历史入口。
func (s *store) ListCwds(ctx context.Context) ([]ThreadCwd, error) {
	rows, err := s.q.ListAgentThreadCwds(ctx)
	if err != nil {
		return nil, wrapThreadError(err, "list_cwds")
	}
	return mapThreadCwds(rows), nil
}

// ListCwdsByPrefix 按目录前缀筛选线程工作目录。
func (s *store) ListCwdsByPrefix(ctx context.Context, prefix string) ([]ThreadCwd, error) {
	rows, err := s.q.ListAgentThreadCwdsByPrefix(ctx, sqlc.ListAgentThreadCwdsByPrefixParams{Column1: &prefix})
	if err != nil {
		return nil, wrapThreadError(err, "list_cwds_by_prefix")
	}
	return mapThreadCwdsByPrefix(rows), nil
}

// CountChildren 统计指定父 agent 派生出的子线程数量。
func (s *store) CountChildren(ctx context.Context, parentAgentID string) (int64, error) {
	count, err := s.q.CountChildAgentThreads(ctx, sqlc.CountChildAgentThreadsParams{ParentAgentID: parentAgentID})
	if err != nil {
		return 0, wrapThreadError(err, "count_children")
	}
	return count, nil
}

// Exists 判断线程记录是否存在。
func (s *store) Exists(ctx context.Context, threadID string) (bool, error) {
	v, err := s.q.AgentThreadExists(ctx, sqlc.AgentThreadExistsParams{ThreadID: threadID})
	if err != nil {
		return false, wrapThreadError(err, "exists")
	}
	return v != 0, nil
}

// CountAll 统计全部线程记录数量。
func (s *store) CountAll(ctx context.Context) (int64, error) {
	count, err := s.q.CountAllThreads(ctx)
	if err != nil {
		return 0, wrapThreadError(err, "count_all")
	}
	return count, nil
}

// wrapThreadError 统一给 thread store 错误补充操作名和存储名。
func wrapThreadError(err error, operation string) error {
	return platformdb.WrapStoreError(err, operation, "thread")
}

// boolToInt64 将 Go bool 转为 SQLite 约定的 0/1。
func boolToInt64(b bool) int64 {
	if b {
		return 1
	}
	return 0
}

// mapThreadByID 映射按 thread_id 查询返回的完整线程行。
func mapThreadByID(row sqlc.GetAgentThreadByIDRow) Thread {
	return Thread{
		ThreadID:         row.ThreadID,
		AgentID:          stringFromAny(row.AgentID),
		ParentAgentID:    row.ParentAgentID,
		AgentType:        row.AgentType,
		AgentMemoryScope: row.AgentMemoryScope,
		Name:             row.Name,
		Prompt:           row.Prompt,
		Model:            row.Model,
		Cwd:              row.CWD,
		Status:           row.Status,
		Port:             int32(row.Port),
		PID:              int32(row.Pid),
		CreatedAt:        row.CreatedAt,
		UpdatedAt:        row.UpdatedAt,
		FinishedAt:       row.FinishedAt,
		LastEventType:    row.LastEventType,
		ErrorMessage:     row.ErrorMessage,
		WorkspaceRunKey:  row.WorkspaceRunKey,
		OwnerThreadID:    row.OwnerThreadID,
		ConfigOverride:   json.RawMessage(row.ConfigOverride),
		AgentKey:         row.AgentKey,
		PromptVersionID:  row.PromptVersionID,
		PendingLaunch:    row.PendingLaunch != 0,
		ManuallyRenamed:  row.ManuallyRenamed != 0,
	}
}

// mapThreadByPort 映射按端口查询返回的完整线程行。
func mapThreadByPort(row sqlc.GetAgentThreadByPortRow) Thread {
	return Thread{
		ThreadID:         row.ThreadID,
		AgentID:          stringFromAny(row.AgentID),
		ParentAgentID:    row.ParentAgentID,
		AgentType:        row.AgentType,
		AgentMemoryScope: row.AgentMemoryScope,
		Name:             row.Name,
		Prompt:           row.Prompt,
		Model:            row.Model,
		Cwd:              row.CWD,
		Status:           row.Status,
		Port:             int32(row.Port),
		PID:              int32(row.Pid),
		CreatedAt:        row.CreatedAt,
		UpdatedAt:        row.UpdatedAt,
		FinishedAt:       row.FinishedAt,
		LastEventType:    row.LastEventType,
		ErrorMessage:     row.ErrorMessage,
		WorkspaceRunKey:  row.WorkspaceRunKey,
		OwnerThreadID:    row.OwnerThreadID,
		ConfigOverride:   json.RawMessage(row.ConfigOverride),
		AgentKey:         row.AgentKey,
		PromptVersionID:  row.PromptVersionID,
		PendingLaunch:    row.PendingLaunch != 0,
		ManuallyRenamed:  row.ManuallyRenamed != 0,
	}
}

// mapConfigList 映射批量配置查询结果，只填充调用方需要的配置字段。
func mapConfigList(rows []sqlc.ListAgentThreadConfigsByIDsRow) []Thread {
	result := make([]Thread, len(rows))
	for i, row := range rows {
		result[i] = Thread{
			ThreadID:       row.ThreadID,
			Model:          row.Model,
			ConfigOverride: json.RawMessage(row.ConfigOverride),
		}
	}
	return result
}

// mapThreadList 映射全量线程列表查询结果。
func mapThreadList(rows []sqlc.ListAgentThreadsRow) []Thread {
	result := make([]Thread, len(rows))
	for i, row := range rows {
		result[i] = Thread{
			ThreadID:         row.ThreadID,
			AgentID:          stringFromAny(row.AgentID),
			ParentAgentID:    row.ParentAgentID,
			AgentType:        row.AgentType,
			AgentMemoryScope: row.AgentMemoryScope,
			Name:             row.Name,
			Prompt:           row.Prompt,
			Model:            row.Model,
			Cwd:              row.CWD,
			Status:           row.Status,
			Port:             int32(row.Port),
			PID:              int32(row.Pid),
			CreatedAt:        row.CreatedAt,
			UpdatedAt:        row.UpdatedAt,
			FinishedAt:       row.FinishedAt,
			LastEventType:    row.LastEventType,
			ErrorMessage:     row.ErrorMessage,
			WorkspaceRunKey:  row.WorkspaceRunKey,
			OwnerThreadID:    row.OwnerThreadID,
			ConfigOverride:   json.RawMessage(row.ConfigOverride),
			AgentKey:         row.AgentKey,
			PromptVersionID:  row.PromptVersionID,
			PendingLaunch:    row.PendingLaunch != 0,
			ManuallyRenamed:  row.ManuallyRenamed != 0,
		}
	}
	return result
}

func validateStoreThreadPageLimit(limit int) (int, error) {
	if limit <= 0 {
		return 0, wrapThreadError(errors.New("thread page limit is required"), "list_page")
	}
	return limit, nil
}

func buildThreadPage(threads []contract.ThreadListRecord, limit int) contract.ThreadListPage {
	page := contract.ThreadListPage{Threads: threads}
	if len(threads) > limit {
		page.HasMore = true
		page.Threads = threads[:limit]
	}
	if len(page.Threads) > 0 {
		last := page.Threads[len(page.Threads)-1]
		page.NextCursorCreatedAt = last.CreatedAt
		page.NextCursorThreadID = last.ThreadID
	}
	return page
}

func mapThreadPageRows(rows []sqlc.ListAgentThreadsPageRow) []contract.ThreadListRecord {
	mapped := make([]sqlc.ListAgentThreadsRow, len(rows))
	for i, row := range rows {
		mapped[i] = listAgentThreadsRowFromPage(row)
	}
	return mapThreadListRecords(mapThreadList(mapped))
}

func mapLoadedThreadPageRows(rows []sqlc.ListLoadedAgentThreadsPageRow) []contract.ThreadListRecord {
	mapped := make([]sqlc.ListAgentThreadsRow, len(rows))
	for i, row := range rows {
		mapped[i] = listAgentThreadsRowFromLoadedPage(row)
	}
	return mapThreadListRecords(mapThreadList(mapped))
}

// mapThreadListRecords 把 store 内部 Thread DTO 投影为 contract 层列表行。
func mapThreadListRecords(threads []Thread) []contract.ThreadListRecord {
	result := make([]contract.ThreadListRecord, len(threads))
	for i, thread := range threads {
		result[i] = contract.ThreadListRecord{
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
	return result
}

func listAgentThreadsRowFromPage(row sqlc.ListAgentThreadsPageRow) sqlc.ListAgentThreadsRow {
	return sqlc.ListAgentThreadsRow{
		ThreadID:         row.ThreadID,
		Name:             row.Name,
		Prompt:           row.Prompt,
		Model:            row.Model,
		CWD:              row.CWD,
		Status:           row.Status,
		Port:             row.Port,
		Pid:              row.Pid,
		CreatedAt:        row.CreatedAt,
		UpdatedAt:        row.UpdatedAt,
		FinishedAt:       row.FinishedAt,
		LastEventType:    row.LastEventType,
		ErrorMessage:     row.ErrorMessage,
		WorkspaceRunKey:  row.WorkspaceRunKey,
		OwnerThreadID:    row.OwnerThreadID,
		ParentAgentID:    row.ParentAgentID,
		AgentType:        row.AgentType,
		AgentMemoryScope: row.AgentMemoryScope,
		ConfigOverride:   row.ConfigOverride,
		AgentKey:         row.AgentKey,
		PromptVersionID:  row.PromptVersionID,
		PendingLaunch:    row.PendingLaunch,
		ManuallyRenamed:  row.ManuallyRenamed,
		AgentID:          row.AgentID,
	}
}

func listAgentThreadsRowFromLoadedPage(row sqlc.ListLoadedAgentThreadsPageRow) sqlc.ListAgentThreadsRow {
	return sqlc.ListAgentThreadsRow{
		ThreadID:         row.ThreadID,
		Name:             row.Name,
		Prompt:           row.Prompt,
		Model:            row.Model,
		CWD:              row.CWD,
		Status:           row.Status,
		Port:             row.Port,
		Pid:              row.Pid,
		CreatedAt:        row.CreatedAt,
		UpdatedAt:        row.UpdatedAt,
		FinishedAt:       row.FinishedAt,
		LastEventType:    row.LastEventType,
		ErrorMessage:     row.ErrorMessage,
		WorkspaceRunKey:  row.WorkspaceRunKey,
		OwnerThreadID:    row.OwnerThreadID,
		ParentAgentID:    row.ParentAgentID,
		AgentType:        row.AgentType,
		AgentMemoryScope: row.AgentMemoryScope,
		ConfigOverride:   row.ConfigOverride,
		AgentKey:         row.AgentKey,
		PromptVersionID:  row.PromptVersionID,
		PendingLaunch:    row.PendingLaunch,
		ManuallyRenamed:  row.ManuallyRenamed,
		AgentID:          row.AgentID,
	}
}

// mapRunningThreadList 映射运行中线程列表查询结果。
func mapRunningThreadList(rows []sqlc.ListRunningAgentThreadsRow) []Thread {
	result := make([]Thread, len(rows))
	for i, row := range rows {
		result[i] = Thread{
			ThreadID:         row.ThreadID,
			AgentID:          stringFromAny(row.AgentID),
			ParentAgentID:    row.ParentAgentID,
			AgentType:        row.AgentType,
			AgentMemoryScope: row.AgentMemoryScope,
			Name:             row.Name,
			Prompt:           row.Prompt,
			Model:            row.Model,
			Cwd:              row.CWD,
			Status:           row.Status,
			Port:             int32(row.Port),
			PID:              int32(row.Pid),
			CreatedAt:        row.CreatedAt,
			UpdatedAt:        row.UpdatedAt,
			FinishedAt:       row.FinishedAt,
			LastEventType:    row.LastEventType,
			ErrorMessage:     row.ErrorMessage,
			WorkspaceRunKey:  row.WorkspaceRunKey,
			OwnerThreadID:    row.OwnerThreadID,
			ConfigOverride:   json.RawMessage(row.ConfigOverride),
			AgentKey:         row.AgentKey,
			PromptVersionID:  row.PromptVersionID,
			PendingLaunch:    row.PendingLaunch != 0,
			ManuallyRenamed:  row.ManuallyRenamed != 0,
		}
	}
	return result
}

// mapRecoverableThreadList 映射可恢复线程列表查询结果。
func mapRecoverableThreadList(rows []sqlc.ListRecoverableAgentThreadsRow) []Thread {
	result := make([]Thread, len(rows))
	for i, row := range rows {
		result[i] = Thread{
			ThreadID:         row.ThreadID,
			AgentID:          stringFromAny(row.AgentID),
			ParentAgentID:    row.ParentAgentID,
			AgentType:        row.AgentType,
			AgentMemoryScope: row.AgentMemoryScope,
			Name:             row.Name,
			Prompt:           row.Prompt,
			Model:            row.Model,
			Cwd:              row.CWD,
			Status:           row.Status,
			Port:             int32(row.Port),
			PID:              int32(row.Pid),
			CreatedAt:        row.CreatedAt,
			UpdatedAt:        row.UpdatedAt,
			FinishedAt:       row.FinishedAt,
			LastEventType:    row.LastEventType,
			ErrorMessage:     row.ErrorMessage,
			WorkspaceRunKey:  row.WorkspaceRunKey,
			OwnerThreadID:    row.OwnerThreadID,
			ConfigOverride:   json.RawMessage(row.ConfigOverride),
			AgentKey:         row.AgentKey,
			PromptVersionID:  row.PromptVersionID,
			PendingLaunch:    row.PendingLaunch != 0,
			ManuallyRenamed:  row.ManuallyRenamed != 0,
		}
	}
	return result
}

// mapThreadCwds 映射线程工作目录列表。
func mapThreadCwds(rows []sqlc.ListAgentThreadCwdsRow) []ThreadCwd {
	result := make([]ThreadCwd, len(rows))
	for i, row := range rows {
		result[i] = ThreadCwd{
			ThreadID: row.ThreadID,
			Cwd:      row.CWD,
		}
	}
	return result
}

// mapThreadCwdsByPrefix 映射按前缀查询出的线程工作目录列表。
func mapThreadCwdsByPrefix(rows []sqlc.ListAgentThreadCwdsByPrefixRow) []ThreadCwd {
	result := make([]ThreadCwd, len(rows))
	for i, row := range rows {
		result[i] = ThreadCwd{
			ThreadID: row.ThreadID,
			Cwd:      row.CWD,
		}
	}
	return result
}

// stringFromAny 兼容不同驱动返回的 nullable text 形态。
func stringFromAny(value any) string {
	if value == nil {
		return ""
	}
	switch typed := value.(type) {
	case string:
		return typed
	case *string:
		if typed == nil {
			return ""
		}
		return *typed
	default:
		return fmt.Sprint(typed)
	}
}
