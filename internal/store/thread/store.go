package thread

import (
	"context"
	"encoding/json"
	"fmt"

	platformdb "github.com/anthropic-ai/super-agent-v3/internal/platform/db"
	"github.com/anthropic-ai/super-agent-v3/internal/store/sqlc"
)

type querier interface {
	AgentThreadRunningExists(ctx context.Context, arg sqlc.AgentThreadRunningExistsParams) (bool, error)
	AgentThreadExists(ctx context.Context, arg sqlc.AgentThreadExistsParams) (bool, error)
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
	LoadAgentThreadPromptSnapshot(ctx context.Context, arg sqlc.LoadAgentThreadPromptSnapshotParams) ([]byte, error)
	ResetRunningAgentThreads(ctx context.Context) error
	UpdateAgentThreadPromptSnapshot(ctx context.Context, arg sqlc.UpdateAgentThreadPromptSnapshotParams) (int64, error)
	UpdateAgentThreadStatus(ctx context.Context, arg sqlc.UpdateAgentThreadStatusParams) error
	UpdateAgentThreadLaunchResult(ctx context.Context, arg sqlc.UpdateAgentThreadLaunchResultParams) error
	UpsertAgentThread(ctx context.Context, arg sqlc.UpsertAgentThreadParams) error
}

type store struct {
	q querier
}

// NewStore 创建存储。
func NewStore(q *sqlc.Queries) Store {
	return &store{q: q}
}

// GetByThreadID 按线程ID读取线程存储。
func (s *store) GetByThreadID(ctx context.Context, threadID string) (*Thread, error) {
	row, err := s.q.GetAgentThreadByID(ctx, sqlc.GetAgentThreadByIDParams{ThreadID: threadID})
	if err != nil {
		return nil, wrapThreadError(err, "get_by_thread_id")
	}
	result := mapThreadByID(row)
	return &result, nil
}

// GetByPort 按port读取线程存储。
func (s *store) GetByPort(ctx context.Context, port int32) (*Thread, error) {
	row, err := s.q.GetAgentThreadByPort(ctx, sqlc.GetAgentThreadByPortParams{Port: port})
	if err != nil {
		return nil, wrapThreadError(err, "get_by_port")
	}
	result := mapThreadByPort(row)
	return &result, nil
}

// ListAll 列出all。
func (s *store) ListAll(ctx context.Context) ([]Thread, error) {
	rows, err := s.q.ListAgentThreads(ctx)
	if err != nil {
		return nil, wrapThreadError(err, "list_all")
	}
	return mapThreadList(rows), nil
}

// ListConfigsByIDs 按ids列出配置。
func (s *store) ListConfigsByIDs(ctx context.Context, threadIDs []string) ([]Thread, error) {
	rows, err := s.q.ListAgentThreadConfigsByIDs(ctx, sqlc.ListAgentThreadConfigsByIDsParams{ThreadIds: threadIDs})
	if err != nil {
		return nil, wrapThreadError(err, "list_configs_by_ids")
	}
	return mapConfigList(rows), nil
}

// ListRunning 列出running。
func (s *store) ListRunning(ctx context.Context) ([]Thread, error) {
	rows, err := s.q.ListRunningAgentThreads(ctx)
	if err != nil {
		return nil, wrapThreadError(err, "list_running")
	}
	return mapRunningThreadList(rows), nil
}

// ListRecoverable 列出recoverable。
func (s *store) ListRecoverable(ctx context.Context) ([]Thread, error) {
	rows, err := s.q.ListRecoverableAgentThreads(ctx)
	if err != nil {
		return nil, wrapThreadError(err, "list_recoverable")
	}
	return mapRecoverableThreadList(rows), nil
}

// ListRunningAgents 列出running代理。
func (s *store) ListRunningAgents(ctx context.Context) ([]RunningAgent, error) {
	rows, err := s.q.ListRunningAgents(ctx)
	if err != nil {
		return nil, wrapThreadError(err, "list_running_agents")
	}
	result := make([]RunningAgent, len(rows))
	for i, row := range rows {
		result[i] = RunningAgent{
			ThreadID: row.ThreadID,
			Port:     row.Port,
			PID:      row.Pid,
			Status:   row.Status,
		}
	}
	return result, nil
}

// Upsert 新增或更新记录。
func (s *store) Upsert(ctx context.Context, params UpsertParams) error {
	return wrapThreadError(s.q.UpsertAgentThread(ctx, sqlc.UpsertAgentThreadParams{
		ThreadID:         params.ThreadID,
		Name:             params.Name,
		Prompt:           params.Prompt,
		Model:            params.Model,
		CWD:              params.Cwd,
		Status:           params.Status,
		Port:             params.Port,
		Pid:              params.PID,
		CreatedAt:        params.CreatedAt,
		UpdatedAt:        params.UpdatedAt,
		OwnerThreadID:    params.OwnerThreadID,
		ParentAgentID:    params.ParentAgentID,
		AgentType:        params.AgentType,
		AgentMemoryScope: params.AgentMemoryScope,
		ConfigOverride:   params.ConfigOverride,
		AgentKey:         params.AgentKey,
		PromptVersionID:  params.PromptVersionID,
		PendingLaunch:    params.PendingLaunch,
		ManuallyRenamed:  params.ManuallyRenamed,
	}), "upsert")
}

// SavePromptSnapshot 保存prompt快照。
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

// LoadPromptSnapshot 加载prompt快照。
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

// UpdateStatus 更新状态。
func (s *store) UpdateStatus(ctx context.Context, params UpdateStatusParams) error {
	return wrapThreadError(s.q.UpdateAgentThreadStatus(ctx, sqlc.UpdateAgentThreadStatusParams{
		ThreadID:  params.ThreadID,
		Status:    params.Status,
		UpdatedAt: params.UpdatedAt,
	}), "update_status")
}

// UpdateLaunchResult 更新启动结果。
func (s *store) UpdateLaunchResult(ctx context.Context, params UpdateLaunchResultParams) error {
	return wrapThreadError(s.q.UpdateAgentThreadLaunchResult(ctx, sqlc.UpdateAgentThreadLaunchResultParams{
		ThreadID:        params.ThreadID,
		AgentKey:        params.AgentKey,
		PromptVersionID: params.PromptVersionID,
		UpdatedAt:       params.UpdatedAt,
	}), "update_launch_result")
}

// DeleteByThreadID 按线程ID删除线程存储。
func (s *store) DeleteByThreadID(ctx context.Context, threadID string) error {
	return wrapThreadError(s.q.DeleteAgentThreadByID(ctx, sqlc.DeleteAgentThreadByIDParams{ThreadID: threadID}), "delete_by_thread_id")
}

// ResetRunning 重置running。
func (s *store) ResetRunning(ctx context.Context) error {
	return wrapThreadError(s.q.ResetRunningAgentThreads(ctx), "reset_running")
}

// ExpireStale 处理expirestale。
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

// RunningExists 处理runningexists。
func (s *store) RunningExists(ctx context.Context, threadID string) (bool, error) {
	exists, err := s.q.AgentThreadRunningExists(ctx, sqlc.AgentThreadRunningExistsParams{ThreadID: threadID})
	if err != nil {
		return false, wrapThreadError(err, "running_exists")
	}
	return exists, nil
}

// ListCwds 列出cwds。
func (s *store) ListCwds(ctx context.Context) ([]ThreadCwd, error) {
	rows, err := s.q.ListAgentThreadCwds(ctx)
	if err != nil {
		return nil, wrapThreadError(err, "list_cwds")
	}
	return mapThreadCwds(rows), nil
}

// ListCwdsByPrefix 按prefix列出cwds。
func (s *store) ListCwdsByPrefix(ctx context.Context, prefix string) ([]ThreadCwd, error) {
	rows, err := s.q.ListAgentThreadCwdsByPrefix(ctx, sqlc.ListAgentThreadCwdsByPrefixParams{Column1: &prefix})
	if err != nil {
		return nil, wrapThreadError(err, "list_cwds_by_prefix")
	}
	return mapThreadCwdsByPrefix(rows), nil
}

// CountChildren 统计children。
func (s *store) CountChildren(ctx context.Context, parentAgentID string) (int64, error) {
	count, err := s.q.CountChildAgentThreads(ctx, sqlc.CountChildAgentThreadsParams{ParentAgentID: parentAgentID})
	if err != nil {
		return 0, wrapThreadError(err, "count_children")
	}
	return count, nil
}

// Exists 判断线程存储是否可用。
func (s *store) Exists(ctx context.Context, threadID string) (bool, error) {
	exists, err := s.q.AgentThreadExists(ctx, sqlc.AgentThreadExistsParams{ThreadID: threadID})
	if err != nil {
		return false, wrapThreadError(err, "exists")
	}
	return exists, nil
}

// CountAll 统计all。
func (s *store) CountAll(ctx context.Context) (int64, error) {
	count, err := s.q.CountAllThreads(ctx)
	if err != nil {
		return 0, wrapThreadError(err, "count_all")
	}
	return count, nil
}

func wrapThreadError(err error, operation string) error {
	return platformdb.WrapStoreError(err, operation, "thread")
}

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
		Port:             row.Port,
		PID:              row.Pid,
		CreatedAt:        row.CreatedAt,
		UpdatedAt:        row.UpdatedAt,
		FinishedAt:       row.FinishedAt,
		LastEventType:    row.LastEventType,
		ErrorMessage:     row.ErrorMessage,
		WorkspaceRunKey:  row.WorkspaceRunKey,
		OwnerThreadID:    row.OwnerThreadID,
		ConfigOverride:   row.ConfigOverride,
		AgentKey:         row.AgentKey,
		PromptVersionID:  row.PromptVersionID,
		PendingLaunch:    row.PendingLaunch,
		ManuallyRenamed:  row.ManuallyRenamed,
	}
}

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
		Port:             row.Port,
		PID:              row.Pid,
		CreatedAt:        row.CreatedAt,
		UpdatedAt:        row.UpdatedAt,
		FinishedAt:       row.FinishedAt,
		LastEventType:    row.LastEventType,
		ErrorMessage:     row.ErrorMessage,
		WorkspaceRunKey:  row.WorkspaceRunKey,
		OwnerThreadID:    row.OwnerThreadID,
		ConfigOverride:   row.ConfigOverride,
		AgentKey:         row.AgentKey,
		PromptVersionID:  row.PromptVersionID,
		PendingLaunch:    row.PendingLaunch,
		ManuallyRenamed:  row.ManuallyRenamed,
	}
}

func mapConfigList(rows []sqlc.ListAgentThreadConfigsByIDsRow) []Thread {
	result := make([]Thread, len(rows))
	for i, row := range rows {
		result[i] = Thread{
			ThreadID:       row.ThreadID,
			Model:          row.Model,
			ConfigOverride: row.ConfigOverride,
		}
	}
	return result
}

// mapThreadList 映射线程list。
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
			Port:             row.Port,
			PID:              row.Pid,
			CreatedAt:        row.CreatedAt,
			UpdatedAt:        row.UpdatedAt,
			FinishedAt:       row.FinishedAt,
			LastEventType:    row.LastEventType,
			ErrorMessage:     row.ErrorMessage,
			WorkspaceRunKey:  row.WorkspaceRunKey,
			OwnerThreadID:    row.OwnerThreadID,
			ConfigOverride:   row.ConfigOverride,
			AgentKey:         row.AgentKey,
			PromptVersionID:  row.PromptVersionID,
			PendingLaunch:    row.PendingLaunch,
			ManuallyRenamed:  row.ManuallyRenamed,
		}
	}
	return result
}

// mapRunningThreadList 映射running线程list。
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
			Port:             row.Port,
			PID:              row.Pid,
			CreatedAt:        row.CreatedAt,
			UpdatedAt:        row.UpdatedAt,
			FinishedAt:       row.FinishedAt,
			LastEventType:    row.LastEventType,
			ErrorMessage:     row.ErrorMessage,
			WorkspaceRunKey:  row.WorkspaceRunKey,
			OwnerThreadID:    row.OwnerThreadID,
			ConfigOverride:   row.ConfigOverride,
			AgentKey:         row.AgentKey,
			PromptVersionID:  row.PromptVersionID,
			PendingLaunch:    row.PendingLaunch,
			ManuallyRenamed:  row.ManuallyRenamed,
		}
	}
	return result
}

// mapRecoverableThreadList 映射recoverable线程list。
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
			Port:             row.Port,
			PID:              row.Pid,
			CreatedAt:        row.CreatedAt,
			UpdatedAt:        row.UpdatedAt,
			FinishedAt:       row.FinishedAt,
			LastEventType:    row.LastEventType,
			ErrorMessage:     row.ErrorMessage,
			WorkspaceRunKey:  row.WorkspaceRunKey,
			OwnerThreadID:    row.OwnerThreadID,
			ConfigOverride:   row.ConfigOverride,
			AgentKey:         row.AgentKey,
			PromptVersionID:  row.PromptVersionID,
			PendingLaunch:    row.PendingLaunch,
			ManuallyRenamed:  row.ManuallyRenamed,
		}
	}
	return result
}

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
