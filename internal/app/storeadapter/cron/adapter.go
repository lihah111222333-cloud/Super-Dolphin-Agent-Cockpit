package cronadapter

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/app/internal/storeguard"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/module/cron"
	cronstore "github.com/lihah111222333-cloud/super-dolphin-agent/internal/store/cron"
)

type cronStoreAdapter struct {
	jobs      *cronJobStoreAdapter
	scheduler *cronSchedulerStoreAdapter
}

type cronJobStoreAdapter struct {
	store cronstore.Store
}

type cronSchedulerStoreAdapter struct {
	*cronJobStoreAdapter
	*cronSchedulerClaimAdapter
	*cronSchedulerRunAdapter
	*cronSubmitAdapter
}

type cronSchedulerClaimAdapter struct {
	store cronstore.Store
}

type cronSchedulerRunAdapter struct {
	store cronstore.Store
}

type cronSubmitAdapter struct {
	store cronstore.Store
}

var _ cron.Store = (*cronJobStoreAdapter)(nil)
var _ cron.SchedulerStore = (*cronSchedulerStoreAdapter)(nil)

var errCronStoreAdapterMissing = errors.New("cron: store adapter missing required Store")

// newCronStoreAdapter 在 App 组合边界构造共享 root adapter；cron Store 是 required 依赖。
func newCronStoreAdapter(store cronstore.Store) (*cronStoreAdapter, error) {
	if storeguard.IsNil(store) {
		return nil, errCronStoreAdapterMissing
	}
	jobs := &cronJobStoreAdapter{store: store}
	return &cronStoreAdapter{
		jobs: jobs,
		scheduler: &cronSchedulerStoreAdapter{
			cronJobStoreAdapter:       jobs,
			cronSchedulerClaimAdapter: &cronSchedulerClaimAdapter{store: store},
			cronSchedulerRunAdapter:   &cronSchedulerRunAdapter{store: store},
			cronSubmitAdapter:         &cronSubmitAdapter{store: store},
		},
	}, nil
}

// provideCronStore 将共享 root adapter 投影为 cron service CRUD 端口。
func provideCronStore(adapter *cronStoreAdapter) (cron.Store, error) {
	if adapter == nil || adapter.jobs == nil {
		return nil, errCronStoreAdapterMissing
	}
	return adapter.jobs, nil
}

// provideCronSchedulerStore 将同一 root adapter 投影为 cron scheduler 状态机端口。
func provideCronSchedulerStore(adapter *cronStoreAdapter) (cron.SchedulerStore, error) {
	if adapter == nil || adapter.scheduler == nil {
		return nil, errCronStoreAdapterMissing
	}
	return adapter.scheduler, nil
}

// CreateJob 把 service 的创建参数转换为 store 入参，并把返回行转成本包记录。
func (a *cronJobStoreAdapter) CreateJob(ctx context.Context, p cron.CreateJobParams) (cron.JobRecord, error) {
	row, err := a.store.CreateJob(ctx, toStoreCronCreateJobParams(p))
	return fromStoreJob(row), mapCronStoreError(err)
}

// GetJobByID 按 ID 读取任务，并屏蔽 store 层的 DTO 和错误类型。
func (a *cronJobStoreAdapter) GetJobByID(ctx context.Context, id string) (cron.JobRecord, error) {
	row, err := a.store.GetJobByID(ctx, id)
	return fromStoreJob(row), mapCronStoreError(err)
}

// ListJobsPage 显式转换分页 DTO，避免把 store 类型泄漏到 cron 模块。
func (a *cronJobStoreAdapter) ListJobsPage(ctx context.Context, p cron.ListJobsPageParams) (cron.JobRecordPage, error) {
	page, err := a.store.ListJobsPage(ctx, cronstore.ListJobsPageParams{Limit: p.Limit, Cursor: p.Cursor})
	if err != nil {
		if errors.Is(err, cronstore.ErrInvalidListCursor) {
			return cron.JobRecordPage{}, fmt.Errorf("%w: %v", cron.ErrStoreInvalidListCursor, err)
		}
		return cron.JobRecordPage{}, mapCronStoreError(err)
	}
	return cron.JobRecordPage{Jobs: fromStoreJobs(page.Jobs), NextCursor: page.NextCursor, HasMore: page.HasMore}, nil
}

// DeleteJob 删除任务，并把 not found 等 store 错误映射到本包错误。
func (a *cronJobStoreAdapter) DeleteJob(ctx context.Context, id string) error {
	return mapCronStoreError(a.store.DeleteJob(ctx, id))
}

// UpdateJobSchedule 覆盖任务调度配置，避免 service 直接构造 store 入参。
func (a *cronJobStoreAdapter) UpdateJobSchedule(ctx context.Context, p cron.UpdateJobScheduleParams) error {
	return mapCronStoreError(a.store.UpdateJobSchedule(ctx, toStoreCronUpdateJobScheduleParams(p)))
}

// SetJobEnabled 切换任务启停状态，并统一转换 store 层错误。
func (a *cronJobStoreAdapter) SetJobEnabled(ctx context.Context, id string, enabled bool, now time.Time) error {
	return mapCronStoreError(a.store.SetJobEnabled(ctx, id, enabled, now))
}

// PatchNextRunAt 更新下一次运行时间，供手动触发和调度推进复用。
func (a *cronJobStoreAdapter) PatchNextRunAt(ctx context.Context, id string, nextRunAt time.Time, now time.Time) error {
	return mapCronStoreError(a.store.PatchNextRunAt(ctx, id, nextRunAt, now))
}

// ListRunsByJob 查询任务运行记录，并返回本包 cron.RunRecord 切片。
func (a *cronJobStoreAdapter) ListRunsByJob(ctx context.Context, jobID string, limit int32) ([]cron.RunRecord, error) {
	rows, err := a.store.ListRunsByJob(ctx, jobID, limit)
	if err != nil {
		return nil, mapCronStoreError(err)
	}
	return fromStoreRuns(rows), nil
}

// ClaimDueJobsForUpdate 认领到期任务，并把 claim 入参限制在 scheduler 所需字段内。
func (a *cronSchedulerClaimAdapter) ClaimDueJobsForUpdate(ctx context.Context, p cron.ClaimDueJobsForUpdateParams) ([]cron.JobRecord, error) {
	rows, err := a.store.ClaimDueJobsForUpdate(ctx, toStoreCronClaimDueJobsForUpdateParams(p))
	if err != nil {
		return nil, mapCronStoreError(err)
	}
	return fromStoreJobs(rows), nil
}

// RenewLease 延长当前 worker 持有的 job 租约。
func (a *cronSchedulerClaimAdapter) RenewLease(ctx context.Context, p cron.LeaseParams) error {
	return mapCronStoreError(a.store.RenewLease(ctx, toStoreCronLeaseParams(p)))
}

// ExtendClaim 处理 turn progress 触发的租约延长，失败时保留本包错误语义。
func (a *cronSchedulerClaimAdapter) ExtendClaim(ctx context.Context, p cron.LeaseParams) error {
	return mapCronStoreError(a.store.ExtendClaim(ctx, toStoreCronLeaseParams(p)))
}

// MarkFinished 按 claim 栅栏标记任务成功完成，并写入下一次运行时间。
func (a *cronSchedulerClaimAdapter) MarkFinished(ctx context.Context, p cron.MarkFinishedParams) error {
	return mapCronStoreError(a.store.MarkFinished(ctx, toStoreCronMarkFinishedParams(p)))
}

// MarkFailed 按 claim 栅栏记录失败或 observe_lost 状态。
func (a *cronSchedulerClaimAdapter) MarkFailed(ctx context.Context, p cron.MarkFailedParams) error {
	return mapCronStoreError(a.store.MarkFailed(ctx, toStoreCronMarkFailedParams(p)))
}

// FinalizeRecoveredRun 将恢复期 run/job 终态请求映射到同一 store 事务。
func (a *cronSchedulerClaimAdapter) FinalizeRecoveredRun(ctx context.Context, p cron.FinalizeRecoveredRunParams) error {
	return mapCronStoreError(a.store.FinalizeRecoveredRun(ctx, toStoreCronFinalizeRecoveredRunParams(p)))
}

// SetActiveTurn 绑定 job 当前活跃 turn，并保留 thread/agent 身份信息。
func (a *cronSchedulerClaimAdapter) SetActiveTurn(ctx context.Context, p cron.SetActiveTurnParams) error {
	return mapCronStoreError(a.store.SetActiveTurn(ctx, toStoreCronSetActiveTurnParams(p)))
}

// SubmitRunWithActiveTurn 原子保存 run turn、job active turn 和 submitted 状态。
func (a *cronSubmitAdapter) SubmitRunWithActiveTurn(ctx context.Context, p cron.SubmitRunWithActiveTurnParams) error {
	return mapCronStoreError(a.store.SubmitRunWithActiveTurn(ctx, toStoreCronSubmitRunWithActiveTurnParams(p)))
}

// InsertRun 创建一次 run 记录，返回值会转成本包 cron.RunRecord。
func (a *cronSchedulerRunAdapter) InsertRun(ctx context.Context, p cron.InsertRunParams) (cron.RunRecord, error) {
	row, err := a.store.InsertRun(ctx, toStoreCronInsertRunParams(p))
	return fromStoreRun(row), mapCronStoreError(err)
}

// CASRunStatus 执行 run 状态比较交换，避免 scheduler 依赖 store 参数类型。
func (a *cronSchedulerRunAdapter) CASRunStatus(ctx context.Context, p cron.CASRunStatusParams) error {
	return mapCronStoreError(a.store.CASRunStatus(ctx, toStoreCronCASRunStatusParams(p)))
}

// SetRunTurn 记录 run 对应的实际 turn 信息。
func (a *cronSchedulerRunAdapter) SetRunTurn(ctx context.Context, p cron.SetRunTurnParams) error {
	return mapCronStoreError(a.store.SetRunTurn(ctx, toStoreCronSetRunTurnParams(p)))
}

// GetRunByID 读取恢复冲突复核所需的精确 run 状态。
func (a *cronSchedulerRunAdapter) GetRunByID(ctx context.Context, id string) (cron.RunRecord, error) {
	row, err := a.store.GetRunByID(ctx, id)
	return fromStoreRun(row), mapCronStoreError(err)
}

// IsTurnOwned 使用 Store 的原子判域查询，避免 App adapter 拆分读取 run 与 job。
func (a *cronSubmitAdapter) IsTurnOwned(ctx context.Context, turnID string) (bool, error) {
	owned, err := a.store.IsTurnOwned(ctx, turnID)
	return owned, mapCronStoreError(err)
}

// GetRunningRunByTurnID 查找当前 running run，用于终态事件收尾。
func (a *cronSchedulerRunAdapter) GetRunningRunByTurnID(ctx context.Context, turnID string) (cron.RunRecord, error) {
	row, err := a.store.GetRunningRunByTurnID(ctx, turnID)
	return fromStoreRun(row), mapCronStoreError(err)
}

// GetSubmittedOrRunningRunByTurnID 查找可由终态事件收尾的 submitted/running run。
func (a *cronSubmitAdapter) GetSubmittedOrRunningRunByTurnID(ctx context.Context, turnID string) (cron.RunRecord, error) {
	row, err := a.store.GetSubmittedOrRunningRunByTurnID(ctx, turnID)
	return fromStoreRun(row), mapCronStoreError(err)
}

// ListUnresolvedRuns 列出恢复流程需要接管的 run。
func (a *cronSchedulerRunAdapter) ListUnresolvedRuns(ctx context.Context) ([]cron.RunRecord, error) {
	rows, err := a.store.ListUnresolvedRuns(ctx)
	if err != nil {
		return nil, mapCronStoreError(err)
	}
	return fromStoreRuns(rows), nil
}

// ListUnresolvedRunsPage 分页列出恢复流程需要接管的 run。
func (a *cronSubmitAdapter) ListUnresolvedRunsPage(ctx context.Context, limit int32, cursor string) ([]cron.RunRecord, error) {
	rows, err := a.store.ListUnresolvedRunsPage(ctx, limit, cursor)
	if err != nil {
		return nil, mapCronStoreError(err)
	}
	return fromStoreRuns(rows), nil
}

// ListJobsClaimedBy 查询当前调度身份仍持有的 job，用于续租和 progress 延长。
func (a *cronSchedulerClaimAdapter) ListJobsClaimedBy(ctx context.Context, claimedBy string) ([]cron.JobRecord, error) {
	rows, err := a.store.ListJobsClaimedBy(ctx, claimedBy)
	if err != nil {
		return nil, mapCronStoreError(err)
	}
	return fromStoreJobs(rows), nil
}

func toStoreCronCreateJobParams(p cron.CreateJobParams) cronstore.CreateJobParams {
	return cronstore.CreateJobParams{
		ID: p.ID, Name: p.Name, Prompt: p.Prompt, ScheduleType: p.ScheduleType, ScheduleExpr: p.ScheduleExpr,
		Timezone: p.Timezone, Provider: p.Provider, Model: p.Model, CWD: p.CWD,
		Config: cloneCronStoreJSON(p.Config), Skills: cloneCronStoreJSON(p.Skills), NotifyChannel: p.NotifyChannel,
		Enabled: p.Enabled, NextRunAt: p.NextRunAt, MaxAttempts: p.MaxAttempts, CreatedAt: p.CreatedAt, UpdatedAt: p.UpdatedAt,
	}
}

func toStoreCronUpdateJobScheduleParams(p cron.UpdateJobScheduleParams) cronstore.UpdateJobScheduleParams {
	return cronstore.UpdateJobScheduleParams{
		ID: p.ID, Name: p.Name, Prompt: p.Prompt, ScheduleType: p.ScheduleType, ScheduleExpr: p.ScheduleExpr,
		Timezone: p.Timezone, Provider: p.Provider, Model: p.Model, CWD: p.CWD,
		Config: cloneCronStoreJSON(p.Config), Skills: cloneCronStoreJSON(p.Skills), NotifyChannel: p.NotifyChannel,
		Enabled: p.Enabled, NextRunAt: p.NextRunAt, MaxAttempts: p.MaxAttempts, UpdatedAt: p.UpdatedAt,
	}
}

func toStoreCronClaimDueJobsForUpdateParams(p cron.ClaimDueJobsForUpdateParams) cronstore.ClaimDueJobsForUpdateParams {
	return cronstore.ClaimDueJobsForUpdateParams{
		Now: p.Now, ClaimedBy: p.ClaimedBy, LeaseExpiresAt: p.LeaseExpiresAt, ClaimToken: p.ClaimToken, MaxClaim: p.MaxClaim,
	}
}

func toStoreCronLeaseParams(p cron.LeaseParams) cronstore.LeaseParams {
	return cronstore.LeaseParams{ID: p.ID, ClaimToken: p.ClaimToken, LeaseExpiresAt: p.LeaseExpiresAt, Now: p.Now}
}

func toStoreCronMarkFinishedParams(p cron.MarkFinishedParams) cronstore.MarkFinishedParams {
	return cronstore.MarkFinishedParams{
		ID: p.ID, ClaimToken: p.ClaimToken, RunID: p.RunID, ExpectedActiveTurnID: p.ExpectedActiveTurnID,
		LastRunAt: p.LastRunAt, LastTurnID: p.LastTurnID, NextRunAt: p.NextRunAt, Now: p.Now,
	}
}

func toStoreCronMarkFailedParams(p cron.MarkFailedParams) cronstore.MarkFailedParams {
	return cronstore.MarkFailedParams{
		ID: p.ID, ClaimToken: p.ClaimToken, RunID: p.RunID, ExpectedActiveTurnID: p.ExpectedActiveTurnID,
		LastRunAt: p.LastRunAt, LastTurnID: p.LastTurnID, LastStatus: p.LastStatus,
		LastErrorAt: p.LastErrorAt, LastError: p.LastError, NextRunAt: p.NextRunAt,
		NextRetryAt: p.NextRetryAt, Now: p.Now,
	}
}

func toStoreCronFinalizeRecoveredRunParams(p cron.FinalizeRecoveredRunParams) cronstore.FinalizeRecoveredRunParams {
	return cronstore.FinalizeRecoveredRunParams{
		MarkFailedParams:  toStoreCronMarkFailedParams(p.MarkFailedParams),
		ExpectedRunStatus: p.ExpectedRunStatus,
	}
}

func toStoreCronSetActiveTurnParams(p cron.SetActiveTurnParams) cronstore.SetActiveTurnParams {
	return cronstore.SetActiveTurnParams{
		ID: p.ID, ClaimToken: p.ClaimToken, ActiveTurnID: p.ActiveTurnID,
		ThreadID: p.ThreadID, AgentID: p.AgentID, Now: p.Now,
	}
}

func toStoreCronSubmitRunWithActiveTurnParams(
	p cron.SubmitRunWithActiveTurnParams,
) cronstore.SubmitRunWithActiveTurnParams {
	return cronstore.SubmitRunWithActiveTurnParams{
		RunID: p.RunID, JobID: p.JobID, ClaimToken: p.ClaimToken, ActiveTurnID: p.ActiveTurnID,
		ThreadID: p.ThreadID, AgentID: p.AgentID, SubmittedAt: p.SubmittedAt, Now: p.Now,
	}
}

func toStoreCronInsertRunParams(p cron.InsertRunParams) cronstore.InsertRunParams {
	return cronstore.InsertRunParams{
		ID: p.ID, JobID: p.JobID, ScheduledAt: p.ScheduledAt, IdempotencyKey: p.IdempotencyKey,
		DedupeKey: p.DedupeKey, Status: p.Status, CreatedAt: p.CreatedAt, UpdatedAt: p.UpdatedAt,
	}
}

func toStoreCronCASRunStatusParams(p cron.CASRunStatusParams) cronstore.CASRunStatusParams {
	return cronstore.CASRunStatusParams{
		ID: p.ID, ExpectedStatus: p.ExpectedStatus, NextStatus: p.NextStatus, Error: p.Error, UpdatedAt: p.UpdatedAt,
	}
}

func toStoreCronSetRunTurnParams(p cron.SetRunTurnParams) cronstore.SetRunTurnParams {
	return cronstore.SetRunTurnParams{
		ID: p.ID, ThreadID: p.ThreadID, AgentID: p.AgentID, TurnID: p.TurnID,
		SubmittedAt: p.SubmittedAt, UpdatedAt: p.UpdatedAt,
	}
}

func fromStoreJobs(rows []cronstore.Job) []cron.JobRecord {
	out := make([]cron.JobRecord, len(rows))
	for i, row := range rows {
		out[i] = fromStoreJob(row)
	}
	return out
}

// fromStoreJob 复制 store Job 字段到本包记录，JSON 字节会复制后再交给上层。
func fromStoreJob(row cronstore.Job) cron.JobRecord {
	return cron.JobRecord{
		ID:              row.ID,
		Name:            row.Name,
		Prompt:          row.Prompt,
		ScheduleType:    row.ScheduleType,
		ScheduleExpr:    row.ScheduleExpr,
		Timezone:        row.Timezone,
		Provider:        row.Provider,
		Model:           row.Model,
		CWD:             row.CWD,
		Config:          cloneCronStoreJSON(row.Config),
		Skills:          cloneCronStoreJSON(row.Skills),
		NotifyChannel:   row.NotifyChannel,
		Enabled:         row.Enabled,
		NextRunAt:       row.NextRunAt,
		LastScheduledAt: row.LastScheduledAt,
		LastRunAt:       row.LastRunAt,
		ClaimedAt:       row.ClaimedAt,
		ClaimedBy:       row.ClaimedBy,
		LeaseExpiresAt:  row.LeaseExpiresAt,
		ClaimToken:      row.ClaimToken,
		ThreadID:        row.ThreadID,
		AgentID:         row.AgentID,
		ActiveTurnID:    row.ActiveTurnID,
		LastTurnID:      row.LastTurnID,
		FailureCount:    row.FailureCount,
		MaxAttempts:     row.MaxAttempts,
		NextRetryAt:     row.NextRetryAt,
		LastStatus:      row.LastStatus,
		LastErrorAt:     row.LastErrorAt,
		LastError:       row.LastError,
		CreatedAt:       row.CreatedAt,
		UpdatedAt:       row.UpdatedAt,
	}
}

func fromStoreRuns(rows []cronstore.Run) []cron.RunRecord {
	out := make([]cron.RunRecord, len(rows))
	for i, row := range rows {
		out[i] = fromStoreRun(row)
	}
	return out
}

func fromStoreRun(row cronstore.Run) cron.RunRecord {
	return cron.RunRecord{
		ID:             row.ID,
		JobID:          row.JobID,
		ScheduledAt:    row.ScheduledAt,
		IdempotencyKey: row.IdempotencyKey,
		DedupeKey:      row.DedupeKey,
		ThreadID:       row.ThreadID,
		AgentID:        row.AgentID,
		TurnID:         row.TurnID,
		SubmittedAt:    row.SubmittedAt,
		Status:         row.Status,
		Error:          row.Error,
		CreatedAt:      row.CreatedAt,
		UpdatedAt:      row.UpdatedAt,
	}
}

func cloneCronStoreJSON(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 {
		return nil
	}
	out := make([]byte, len(raw))
	copy(out, raw)
	return out
}

// mapCronStoreError 把 store 哨兵错误换成本包错误，避免非 assembly 文件依赖 store 包。
func mapCronStoreError(err error) error {
	if err == nil {
		return nil
	}
	switch {
	case errors.Is(err, cronstore.ErrJobNotFound):
		return fmt.Errorf("%w: %v", cron.ErrStoreJobNotFound, err)
	case errors.Is(err, cronstore.ErrJobRunNotFound):
		return fmt.Errorf("%w: %v", cron.ErrStoreJobRunNotFound, err)
	case errors.Is(err, cronstore.ErrClaimTokenMismatch):
		return fmt.Errorf("%w: %v", cron.ErrStoreClaimTokenMismatch, err)
	case errors.Is(err, cronstore.ErrStatusTransitionRefused):
		return fmt.Errorf("%w: %v", cron.ErrStoreStatusTransitionRefused, err)
	case errors.Is(err, cronstore.ErrEmptyID):
		return fmt.Errorf("%w: %v", cron.ErrStoreEmptyID, err)
	case errors.Is(err, cronstore.ErrEmptyCWD):
		return fmt.Errorf("%w: %v", cron.ErrStoreEmptyCWD, err)
	case errors.Is(err, cronstore.ErrEmptyProvider):
		return fmt.Errorf("%w: %v", cron.ErrStoreEmptyProvider, err)
	case errors.Is(err, cronstore.ErrEmptyScheduleExpr):
		return fmt.Errorf("%w: %v", cron.ErrStoreEmptyScheduleExpr, err)
	default:
		return err
	}
}
