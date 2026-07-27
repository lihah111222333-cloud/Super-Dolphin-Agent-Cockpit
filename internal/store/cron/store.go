package cron

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	platformdb "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/db"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/store/sqlc"
)

// querier 是 cron store 使用的 sqlc 查询子集，测试可用窄接口替身覆盖状态流转。
type querier interface {
	CreateCronJob(ctx context.Context, arg sqlc.CreateCronJobParams) (sqlc.CronJob, error)
	GetCronJobByID(ctx context.Context, arg sqlc.GetCronJobByIDParams) (sqlc.CronJob, error)
	ListCronJobsPage(ctx context.Context, arg sqlc.ListCronJobsPageParams) ([]sqlc.CronJob, error)
	DeleteCronJob(ctx context.Context, arg sqlc.DeleteCronJobParams) (int64, error)
	UpdateCronJobSchedule(ctx context.Context, arg sqlc.UpdateCronJobScheduleParams) error
	SetCronJobEnabled(ctx context.Context, arg sqlc.SetCronJobEnabledParams) (int64, error)
	PatchCronJobNextRunAt(ctx context.Context, arg sqlc.PatchCronJobNextRunAtParams) error
	ClaimDueJobsForUpdate(ctx context.Context, arg sqlc.ClaimDueJobsForUpdateParams) ([]sqlc.CronJob, error)
	RenewLease(ctx context.Context, arg sqlc.RenewLeaseParams) (int64, error)
	ExtendClaim(ctx context.Context, arg sqlc.ExtendClaimParams) (int64, error)
	ReleaseClaim(ctx context.Context, arg sqlc.ReleaseClaimParams) (int64, error)
	MarkCronJobFinished(ctx context.Context, arg sqlc.MarkCronJobFinishedParams) (int64, error)
	MarkCronJobFailed(ctx context.Context, arg sqlc.MarkCronJobFailedParams) (int64, error)
	SetCronJobActiveTurn(ctx context.Context, arg sqlc.SetCronJobActiveTurnParams) (int64, error)
	InsertCronJobRun(ctx context.Context, arg sqlc.InsertCronJobRunParams) (sqlc.CronJobRun, error)
	CASCronJobRunStatus(ctx context.Context, arg sqlc.CASCronJobRunStatusParams) (int64, error)
	SetCronJobRunTurn(ctx context.Context, arg sqlc.SetCronJobRunTurnParams) (int64, error)
	GetCronJobRunByDedupeKey(ctx context.Context, arg sqlc.GetCronJobRunByDedupeKeyParams) (sqlc.CronJobRun, error)
	GetCronJobRunByID(ctx context.Context, arg sqlc.GetCronJobRunByIDParams) (sqlc.CronJobRun, error)
	ListCronJobRunsByJob(ctx context.Context, arg sqlc.ListCronJobRunsByJobParams) ([]sqlc.CronJobRun, error)
	ListUnresolvedCronJobRuns(ctx context.Context) ([]sqlc.CronJobRun, error)
	GetRunningCronJobRunByTurnID(ctx context.Context, arg sqlc.GetRunningCronJobRunByTurnIDParams) (sqlc.CronJobRun, error)
	ListCronJobsClaimedBy(ctx context.Context, arg sqlc.ListCronJobsClaimedByParams) ([]sqlc.CronJob, error)
}

type unresolvedRunsPageQuerier interface {
	ListUnresolvedCronJobRunsPage(ctx context.Context, arg sqlc.ListUnresolvedCronJobRunsPageParams) ([]sqlc.CronJobRun, error)
}

type submittedOrRunningTurnQuerier interface {
	GetSubmittedOrRunningCronJobRunByTurnID(ctx context.Context, arg sqlc.GetSubmittedOrRunningCronJobRunByTurnIDParams) (sqlc.CronJobRun, error)
}

// store 实现 cron Store，所有数据库错误统一通过 wrap 带上 cron 操作名。
type store struct {
	*submitStore
	q querier
}

type cronJobQueryStore = store
type cronJobCommandStore = store
type cronClaimStore = store
type cronRunQueryStore = store
type cronRunCommandStore = store

type submitStore struct {
	db        *sql.DB
	q         querier
	sqlcQuery *sqlc.Queries
}

// cronClaimBusyRetryAttempts 是 SQLite claim 遇到 busy 或 locked 时的有限重试次数。
const cronClaimBusyRetryAttempts = 3

// NewStore 使用 sqlc 查询对象创建 cron Store。生产 Fx 装配必须使用 NewStoreWithDB。
func NewStore(q *sqlc.Queries) Store {
	return &store{submitStore: &submitStore{q: q, sqlcQuery: q}, q: q}
}

// NewStoreWithDB 创建带 IMMEDIATE 事务能力的 cron Store，供 Fx 和测试显式注入 DB。
func NewStoreWithDB(db *sql.DB, q *sqlc.Queries) Store {
	return newStoreWithDB(db, q)
}

func newStoreWithDB(db *sql.DB, q *sqlc.Queries) Store {
	return &store{submitStore: &submitStore{db: db, q: q, sqlcQuery: q}, q: q}
}

// ----- 校验与转换辅助 -----

// requireID 校验任务或运行记录 ID，空值会阻断写入和查询。
func requireID(id string) (string, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return "", ErrEmptyID
	}
	return id, nil
}

// requireClaim 同时校验任务 ID 和 claim token，租约相关写操作必须持有二者。
func requireClaim(id, token string) (string, string, error) {
	id, err := requireID(id)
	if err != nil {
		return "", "", err
	}
	if strings.TrimSpace(token) == "" {
		return "", "", ErrEmptyClaimToken
	}
	return id, token, nil
}

// requireRunID 校验 run 级 fence，避免 job 终态更新脱离具体运行记录。
func requireRunID(runID string) (string, error) {
	runID = strings.TrimSpace(runID)
	if runID == "" {
		return "", ErrEmptyID
	}
	return runID, nil
}

// expectedActiveTurnIDForStore 校验 terminal turn fence；带 turn 的终态必须与 active_turn_id 精确一致。
func expectedActiveTurnIDForStore(lastTurnID, expectedActiveTurnID string) (string, error) {
	lastTurnID = strings.TrimSpace(lastTurnID)
	expectedActiveTurnID = strings.TrimSpace(expectedActiveTurnID)
	if lastTurnID != "" && expectedActiveTurnID == "" {
		return "", errors.New("cron: expected_active_turn_id is required")
	}
	if lastTurnID != "" && expectedActiveTurnID != lastTurnID {
		return "", errors.New("cron: expected_active_turn_id must match last_turn_id")
	}
	return expectedActiveTurnID, nil
}

// ts 将 time.Time 转为毫秒时间戳，零值按 0 写入数据库。
func ts(t time.Time) int64 {
	if t.IsZero() {
		return 0
	}
	return platformdb.Millis(t)
}

// tsPtr 将可选时间转成毫秒指针，零值表示 SQL NULL。
func tsPtr(t time.Time) *int64 {
	if t.IsZero() {
		return nil
	}
	ms := platformdb.Millis(t)
	return &ms
}

// bytesOrDefault 为 JSON 配置字段提供显式默认字节，避免写入空 blob。
func bytesOrDefault(b []byte, def string) []byte {
	if len(b) == 0 {
		return []byte(def)
	}
	return b
}

// ----- 任务 -----

// CreateJob 校验必需字段并创建 cron job；schedule type 缺失时按存储契约写入 cron。
func (s *cronJobCommandStore) CreateJob(ctx context.Context, p CreateJobParams) (Job, error) {
	if _, err := requireID(p.ID); err != nil {
		return Job{}, wrap(err, "create_job")
	}
	if strings.TrimSpace(p.CWD) == "" {
		return Job{}, wrap(ErrEmptyCWD, "create_job")
	}
	if strings.TrimSpace(p.Provider) == "" {
		return Job{}, wrap(ErrEmptyProvider, "create_job")
	}
	if strings.TrimSpace(p.ScheduleExpr) == "" {
		return Job{}, wrap(ErrEmptyScheduleExpr, "create_job")
	}
	scheduleType := strings.TrimSpace(p.ScheduleType)
	if scheduleType == "" {
		scheduleType = "cron"
	}
	row, err := s.q.CreateCronJob(ctx, sqlc.CreateCronJobParams{
		ID:            p.ID,
		Name:          p.Name,
		Prompt:        p.Prompt,
		ScheduleType:  scheduleType,
		ScheduleExpr:  p.ScheduleExpr,
		Timezone:      p.Timezone,
		Provider:      p.Provider,
		Model:         p.Model,
		CWD:           p.CWD,
		Config:        bytesOrDefault(p.Config, "{}"),
		Skills:        bytesOrDefault(p.Skills, "[]"),
		NotifyChannel: p.NotifyChannel,
		Enabled:       boolToInt(p.Enabled),
		NextRunAt:     ts(p.NextRunAt),
		MaxAttempts:   int64(p.MaxAttempts),
		CreatedAt:     ts(p.CreatedAt),
		UpdatedAt:     ts(p.UpdatedAt),
	})
	if err != nil {
		return Job{}, wrap(err, "create_job")
	}
	return fromCronJob(row), nil
}

// GetJobByID 按 ID 读取 cron job，并把 sqlc not found 映射为领域错误。
func (s *cronJobQueryStore) GetJobByID(ctx context.Context, id string) (Job, error) {
	id, err := requireID(id)
	if err != nil {
		return Job{}, wrap(err, "get_job_by_id")
	}
	row, err := s.q.GetCronJobByID(ctx, sqlc.GetCronJobByIDParams{ID: id})
	if err != nil {
		if platformdb.IsNotFound(err) {
			return Job{}, wrap(ErrJobNotFound, "get_job_by_id")
		}
		return Job{}, wrap(err, "get_job_by_id")
	}
	return fromCronJob(row), nil
}

// DeleteJob 删除指定 cron job，空 ID 会在进入 sqlc 前被拒绝。
func (s *cronJobCommandStore) DeleteJob(ctx context.Context, id string) error {
	id, err := requireID(id)
	if err != nil {
		return wrap(err, "delete_job")
	}
	rows, err := s.q.DeleteCronJob(ctx, sqlc.DeleteCronJobParams{ID: id})
	if err != nil {
		return wrap(err, "delete_job")
	}
	if rows == 0 {
		return wrap(ErrJobNotFound, "delete_job")
	}
	return nil
}

// UpdateJobSchedule 覆盖任务调度和执行配置，必需字段缺失时直接返回错误。
func (s *cronJobCommandStore) UpdateJobSchedule(ctx context.Context, p UpdateJobScheduleParams) error {
	if _, err := requireID(p.ID); err != nil {
		return wrap(err, "update_job_schedule")
	}
	if strings.TrimSpace(p.CWD) == "" {
		return wrap(ErrEmptyCWD, "update_job_schedule")
	}
	if strings.TrimSpace(p.Provider) == "" {
		return wrap(ErrEmptyProvider, "update_job_schedule")
	}
	if strings.TrimSpace(p.ScheduleExpr) == "" {
		return wrap(ErrEmptyScheduleExpr, "update_job_schedule")
	}
	return wrap(s.q.UpdateCronJobSchedule(ctx, sqlc.UpdateCronJobScheduleParams{
		Name:          p.Name,
		Prompt:        p.Prompt,
		ScheduleType:  firstNonEmpty(p.ScheduleType, "cron"),
		ScheduleExpr:  p.ScheduleExpr,
		Timezone:      p.Timezone,
		Provider:      p.Provider,
		Model:         p.Model,
		CWD:           p.CWD,
		Config:        p.Config,
		Skills:        p.Skills,
		NotifyChannel: p.NotifyChannel,
		Enabled:       boolToInt(p.Enabled),
		NextRunAt:     ts(p.NextRunAt),
		MaxAttempts:   int64(p.MaxAttempts),
		UpdatedAt:     ts(p.UpdatedAt),
		ID:            p.ID,
	}), "update_job_schedule")
}

// SetJobEnabled 切换任务启停状态，并同步更新时间戳。
func (s *cronJobCommandStore) SetJobEnabled(ctx context.Context, id string, enabled bool, now time.Time) error {
	id, err := requireID(id)
	if err != nil {
		return wrap(err, "set_job_enabled")
	}
	rows, err := s.q.SetCronJobEnabled(ctx, sqlc.SetCronJobEnabledParams{
		Enabled:   boolToInt(enabled),
		UpdatedAt: ts(now),
		ID:        id,
	})
	if err != nil {
		return wrap(err, "set_job_enabled")
	}
	if rows == 0 {
		return wrap(ErrJobNotFound, "set_job_enabled")
	}
	return nil
}

// PatchNextRunAt 只更新下一次调度时间，供 scheduler 在计算后持久化游标。
func (s *cronJobCommandStore) PatchNextRunAt(ctx context.Context, id string, nextRunAt time.Time, now time.Time) error {
	id, err := requireID(id)
	if err != nil {
		return wrap(err, "patch_next_run_at")
	}
	return wrap(s.q.PatchCronJobNextRunAt(ctx, sqlc.PatchCronJobNextRunAtParams{
		NextRunAt: ts(nextRunAt),
		UpdatedAt: ts(now),
		ID:        id,
	}), "patch_next_run_at")
}

// ----- 认领与租约 -----

// ClaimDueJobsForUpdate 使用 claim token 抢占到期任务，并返回已转换的领域 Job。
// SQLite busy 或 locked 只在有限次数内重试，避免 scheduler 长时间卡住。
func (s *cronClaimStore) ClaimDueJobsForUpdate(ctx context.Context, p ClaimDueJobsForUpdateParams) ([]Job, error) {
	if strings.TrimSpace(p.ClaimToken) == "" {
		return nil, wrap(ErrEmptyClaimToken, "claim_due_jobs")
	}
	if strings.TrimSpace(p.ClaimedBy) == "" {
		return nil, wrap(errors.New("cron: claimed_by is required"), "claim_due_jobs")
	}
	maxClaim := p.MaxClaim
	if maxClaim <= 0 {
		maxClaim = 16
	}
	arg := sqlc.ClaimDueJobsForUpdateParams{
		ClaimedBy:      p.ClaimedBy,
		Now:            tsPtr(p.Now),
		LeaseExpiresAt: tsPtr(p.LeaseExpiresAt),
		ClaimToken:     p.ClaimToken,
		MaxClaim:       int64(maxClaim),
	}
	rows, err := s.claimDueJobsWithRetry(ctx, arg)
	if err != nil {
		return nil, wrap(err, "claim_due_jobs")
	}
	out := make([]Job, len(rows))
	for i, r := range rows {
		out[i] = fromCronJob(r)
	}
	return out, nil
}

// claimDueJobsWithRetry 包装 SQLite 抢占时的短暂 busy 重试；ctx 取消时立即退出。
func (s *cronClaimStore) claimDueJobsWithRetry(ctx context.Context, arg sqlc.ClaimDueJobsForUpdateParams) ([]sqlc.CronJob, error) {
	var lastErr error
	for attempt := 1; attempt <= cronClaimBusyRetryAttempts; attempt++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		rows, err := s.q.ClaimDueJobsForUpdate(ctx, arg)
		if err == nil {
			return rows, nil
		}
		lastErr = err
		if !platformdb.IsSQLiteBusyLocked(err) {
			return nil, err
		}
		if attempt == cronClaimBusyRetryAttempts {
			return nil, fmt.Errorf("sqlite claim busy after %d attempts: %w", attempt, err)
		}
	}
	return nil, lastErr
}

// RenewLease 在 claim token 匹配时刷新租约，到期或 token 不匹配会返回领域错误。
func (s *cronClaimStore) RenewLease(ctx context.Context, p LeaseParams) error {
	id, token, err := requireClaim(p.ID, p.ClaimToken)
	if err != nil {
		return wrap(err, "renew_lease")
	}
	rows, err := s.q.RenewLease(ctx, sqlc.RenewLeaseParams{
		LeaseExpiresAt: tsPtr(p.LeaseExpiresAt),
		Now:            ts(p.Now),
		ID:             id,
		ClaimToken:     token,
	})
	if err != nil {
		return wrap(err, "renew_lease")
	}
	if rows == 0 {
		return wrap(ErrClaimTokenMismatch, "renew_lease")
	}
	return nil
}

// ExtendClaim 只延长当前 claim 的租约，数据库拒绝缩短 TTL 或 token 不匹配时返回错误。
func (s *cronClaimStore) ExtendClaim(ctx context.Context, p LeaseParams) error {
	id, token, err := requireClaim(p.ID, p.ClaimToken)
	if err != nil {
		return wrap(err, "extend_claim")
	}
	rows, err := s.q.ExtendClaim(ctx, sqlc.ExtendClaimParams{
		LeaseExpiresAt: tsPtr(p.LeaseExpiresAt),
		Now:            ts(p.Now),
		ID:             id,
		ClaimToken:     token,
	})
	if err != nil {
		return wrap(err, "extend_claim")
	}
	if rows == 0 {
		// 受影响行数为 0 可能是 token 不匹配，也可能是调用方试图缩短 TTL。
		// 两种情况都不能静默成功，上层需要按 ErrClaimTokenMismatch 分支处理。
		return wrap(ErrClaimTokenMismatch, "extend_claim")
	}
	return nil
}

// ReleaseClaim 释放当前 claim，必须带匹配 token 才能避免误释放别的 worker 租约。
func (s *cronClaimStore) ReleaseClaim(ctx context.Context, id, claimToken string, now time.Time) error {
	id, token, err := requireClaim(id, claimToken)
	if err != nil {
		return wrap(err, "release_claim")
	}
	rows, err := s.q.ReleaseClaim(ctx, sqlc.ReleaseClaimParams{
		Now:        ts(now),
		ID:         id,
		ClaimToken: token,
	})
	if err != nil {
		return wrap(err, "release_claim")
	}
	if rows == 0 {
		return wrap(ErrClaimTokenMismatch, "release_claim")
	}
	return nil
}

// MarkFinished 在 claim token 匹配时标记任务完成，并写入下一次运行时间和最近 turn。
func (s *cronClaimStore) MarkFinished(ctx context.Context, p MarkFinishedParams) error {
	id, token, err := requireClaim(p.ID, p.ClaimToken)
	if err != nil {
		return wrap(err, "mark_finished")
	}
	runID, err := requireRunID(p.RunID)
	if err != nil {
		return wrap(err, "mark_finished")
	}
	expectedActiveTurnID, err := expectedActiveTurnIDForStore(p.LastTurnID, p.ExpectedActiveTurnID)
	if err != nil {
		return wrap(err, "mark_finished")
	}
	rows, err := s.q.MarkCronJobFinished(ctx, sqlc.MarkCronJobFinishedParams{
		LastRunAt:            tsPtr(p.LastRunAt),
		LastTurnID:           p.LastTurnID,
		NextRunAt:            ts(p.NextRunAt),
		UpdatedAt:            ts(p.Now),
		ID:                   id,
		ClaimToken:           token,
		ExpectedActiveTurnID: expectedActiveTurnID,
		RunID:                runID,
	})
	if err != nil {
		return wrap(err, "mark_finished")
	}
	if rows == 0 {
		return wrap(ErrClaimTokenMismatch, "mark_finished")
	}
	return nil
}

// MarkFailed 在 claim token 匹配时记录失败信息，空状态按 failed 持久化。
func (s *cronClaimStore) MarkFailed(ctx context.Context, p MarkFailedParams) error {
	id, token, err := requireClaim(p.ID, p.ClaimToken)
	if err != nil {
		return wrap(err, "mark_failed")
	}
	runID, err := requireRunID(p.RunID)
	if err != nil {
		return wrap(err, "mark_failed")
	}
	expectedActiveTurnID, err := expectedActiveTurnIDForStore(p.LastTurnID, p.ExpectedActiveTurnID)
	if err != nil {
		return wrap(err, "mark_failed")
	}
	status := strings.TrimSpace(p.LastStatus)
	if status == "" {
		status = StatusFailed
	}
	if p.NextRunAt.IsZero() {
		return wrap(errors.New("cron: next_run_at is required"), "mark_failed")
	}
	rows, err := s.q.MarkCronJobFailed(ctx, sqlc.MarkCronJobFailedParams{
		LastRunAt:            tsPtr(p.LastRunAt),
		LastTurnID:           p.LastTurnID,
		LastStatus:           status,
		LastErrorAt:          tsPtr(p.LastErrorAt),
		LastError:            p.LastError,
		NextRunAt:            ts(p.NextRunAt),
		NextRetryAt:          tsPtr(p.NextRetryAt),
		UpdatedAt:            ts(p.Now),
		ID:                   id,
		ClaimToken:           token,
		ExpectedActiveTurnID: expectedActiveTurnID,
		RunID:                runID,
	})
	if err != nil {
		return wrap(err, "mark_failed")
	}
	if rows == 0 {
		return wrap(ErrClaimTokenMismatch, "mark_failed")
	}
	return nil
}

// SetActiveTurn 记录当前运行中的 thread 和 turn，只有持有 claim 的 worker 可以更新。
func (s *cronClaimStore) SetActiveTurn(ctx context.Context, p SetActiveTurnParams) error {
	id, token, err := requireClaim(p.ID, p.ClaimToken)
	if err != nil {
		return wrap(err, "set_active_turn")
	}
	rows, err := s.q.SetCronJobActiveTurn(ctx, sqlc.SetCronJobActiveTurnParams{
		ActiveTurnID: p.ActiveTurnID,
		ThreadID:     p.ThreadID,
		AgentID:      p.AgentID,
		Now:          ts(p.Now),
		ID:           id,
		ClaimToken:   token,
	})
	if err != nil {
		return wrap(err, "set_active_turn")
	}
	if rows == 0 {
		return wrap(ErrClaimTokenMismatch, "set_active_turn")
	}
	return nil
}

// SubmitRunWithActiveTurn 在 IMMEDIATE 事务内绑定 run turn、job active turn 和 submitted 状态。
func (s *submitStore) SubmitRunWithActiveTurn(ctx context.Context, p SubmitRunWithActiveTurnParams) error {
	runID, jobID, token, turnID, err := submitRunWithActiveTurnFields(p)
	if err != nil {
		return wrap(err, "submit_run_with_active_turn")
	}
	if s.db == nil {
		return wrap(errors.New("cron: explicit DB is required for submit_run_with_active_turn"), "submit_run_with_active_turn")
	}
	if s.sqlcQuery == nil {
		return wrap(errors.New("cron: sqlc queries is required for submit_run_with_active_turn"), "submit_run_with_active_turn")
	}
	err = platformdb.BoundedWriteRetry(ctx, cronClaimBusyRetryAttempts, func() error {
		return platformdb.WithImmediateTx(ctx, s.db, func(tx *sql.Tx) error {
			txq := s.sqlcQuery.WithTx(tx)
			if txq == nil {
				return errors.New("cron: failed to bind tx queries")
			}
			if err := setSubmittedRunTurn(ctx, txq, runID, p); err != nil {
				return err
			}
			if err := setSubmittedActiveTurn(ctx, txq, jobID, token, turnID, p); err != nil {
				return err
			}
			return casSubmittedRunStatus(ctx, txq, runID, p.Now)
		})
	})
	return wrap(err, "submit_run_with_active_turn")
}

// submitRunWithActiveTurnFields 校验事务写入的必需 fence 字段。
func submitRunWithActiveTurnFields(p SubmitRunWithActiveTurnParams) (string, string, string, string, error) {
	runID, err := requireRunID(p.RunID)
	if err != nil {
		return "", "", "", "", err
	}
	jobID, token, err := requireClaim(p.JobID, p.ClaimToken)
	if err != nil {
		return "", "", "", "", err
	}
	turnID := strings.TrimSpace(p.ActiveTurnID)
	if turnID == "" {
		return "", "", "", "", errors.New("cron: active_turn_id is required")
	}
	return runID, jobID, token, turnID, nil
}

// setSubmittedRunTurn writes the provider turn metadata for a submitting run.
func setSubmittedRunTurn(ctx context.Context, q *sqlc.Queries, runID string, p SubmitRunWithActiveTurnParams) error {
	rows, err := q.SetCronJobRunTurn(ctx, sqlc.SetCronJobRunTurnParams{
		TurnID:      strings.TrimSpace(p.ActiveTurnID),
		ThreadID:    p.ThreadID,
		AgentID:     p.AgentID,
		SubmittedAt: tsPtr(p.SubmittedAt),
		UpdatedAt:   ts(p.Now),
		ID:          runID,
	})
	if err != nil {
		return err
	}
	if rows == 0 {
		return ErrJobRunNotFound
	}
	return nil
}

// setSubmittedActiveTurn binds the claimed job to the same turn within the transaction.
func setSubmittedActiveTurn(
	ctx context.Context,
	q *sqlc.Queries,
	jobID string,
	claimToken string,
	turnID string,
	p SubmitRunWithActiveTurnParams,
) error {
	rows, err := q.SetCronJobActiveTurn(ctx, sqlc.SetCronJobActiveTurnParams{
		ActiveTurnID: turnID,
		ThreadID:     p.ThreadID,
		AgentID:      p.AgentID,
		Now:          ts(p.Now),
		ID:           jobID,
		ClaimToken:   claimToken,
	})
	if err != nil {
		return err
	}
	if rows == 0 {
		return ErrClaimTokenMismatch
	}
	return nil
}

// casSubmittedRunStatus advances the run from submitting to submitted as the final transactional step.
func casSubmittedRunStatus(ctx context.Context, q *sqlc.Queries, runID string, now time.Time) error {
	rows, err := q.CASCronJobRunStatus(ctx, sqlc.CASCronJobRunStatusParams{
		NextStatus:     StatusSubmitted,
		UpdatedAt:      ts(now),
		ID:             runID,
		ExpectedStatus: StatusSubmitting,
	})
	if err != nil {
		return err
	}
	if rows == 0 {
		return ErrStatusTransitionRefused
	}
	return nil
}

// ----- 运行记录 -----

// InsertRun 创建一次 cron job run 记录，空状态按 pending 写入。
func (s *cronRunCommandStore) InsertRun(ctx context.Context, p InsertRunParams) (Run, error) {
	if _, err := requireID(p.ID); err != nil {
		return Run{}, wrap(err, "insert_run")
	}
	if _, err := requireID(p.JobID); err != nil {
		return Run{}, wrap(errors.New("cron: job_id is required"), "insert_run")
	}
	status := strings.TrimSpace(p.Status)
	if status == "" {
		status = StatusPending
	}
	row, err := s.q.InsertCronJobRun(ctx, sqlc.InsertCronJobRunParams{
		ID:             p.ID,
		JobID:          p.JobID,
		ScheduledAt:    ts(p.ScheduledAt),
		IdempotencyKey: p.IdempotencyKey,
		DedupeKey:      p.DedupeKey,
		Status:         status,
		CreatedAt:      ts(p.CreatedAt),
		UpdatedAt:      ts(p.UpdatedAt),
	})
	if err != nil {
		return Run{}, wrap(err, "insert_run")
	}
	return fromCronJobRun(row), nil
}

// CASRunStatus 通过期望状态做原子状态转换，失败时返回状态流转被拒绝。
func (s *cronRunCommandStore) CASRunStatus(ctx context.Context, p CASRunStatusParams) error {
	if _, err := requireID(p.ID); err != nil {
		return wrap(err, "cas_run_status")
	}
	rows, err := s.q.CASCronJobRunStatus(ctx, sqlc.CASCronJobRunStatusParams{
		NextStatus:     p.NextStatus,
		Error:          p.Error,
		UpdatedAt:      ts(p.UpdatedAt),
		ID:             p.ID,
		ExpectedStatus: p.ExpectedStatus,
	})
	if err != nil {
		return wrap(err, "cas_run_status")
	}
	if rows == 0 {
		return wrap(ErrStatusTransitionRefused, "cas_run_status")
	}
	return nil
}

// SetRunTurn 绑定 run 与实际提交的 turn 信息，未命中运行记录时返回领域 not found。
func (s *cronRunCommandStore) SetRunTurn(ctx context.Context, p SetRunTurnParams) error {
	if _, err := requireID(p.ID); err != nil {
		return wrap(err, "set_run_turn")
	}
	rows, err := s.q.SetCronJobRunTurn(ctx, sqlc.SetCronJobRunTurnParams{
		TurnID:      p.TurnID,
		ThreadID:    p.ThreadID,
		AgentID:     p.AgentID,
		SubmittedAt: tsPtr(p.SubmittedAt),
		UpdatedAt:   ts(p.UpdatedAt),
		ID:          p.ID,
	})
	if err != nil {
		return wrap(err, "set_run_turn")
	}
	if rows == 0 {
		return wrap(ErrJobRunNotFound, "set_run_turn")
	}
	return nil
}

// GetRunByID 按运行记录 ID 读取 Run，并统一处理 not found 映射。
func (s *cronRunQueryStore) GetRunByID(ctx context.Context, id string) (Run, error) {
	id, err := requireID(id)
	if err != nil {
		return Run{}, wrap(err, "get_run_by_id")
	}
	row, err := s.q.GetCronJobRunByID(ctx, sqlc.GetCronJobRunByIDParams{ID: id})
	if err != nil {
		if platformdb.IsNotFound(err) {
			return Run{}, wrap(ErrJobRunNotFound, "get_run_by_id")
		}
		return Run{}, wrap(err, "get_run_by_id")
	}
	return fromCronJobRun(row), nil
}

// GetRunByDedupeKey 通过幂等键读取运行记录，空 key 按未找到处理。
func (s *cronRunQueryStore) GetRunByDedupeKey(ctx context.Context, dedupeKey string) (Run, error) {
	key := strings.TrimSpace(dedupeKey)
	if key == "" {
		return Run{}, wrap(ErrJobRunNotFound, "get_run_by_dedupe_key")
	}
	row, err := s.q.GetCronJobRunByDedupeKey(ctx, sqlc.GetCronJobRunByDedupeKeyParams{DedupeKey: key})
	if err != nil {
		if platformdb.IsNotFound(err) {
			return Run{}, wrap(ErrJobRunNotFound, "get_run_by_dedupe_key")
		}
		return Run{}, wrap(err, "get_run_by_dedupe_key")
	}
	return fromCronJobRun(row), nil
}

// ListRunsByJob 列出指定任务的运行记录，未传 limit 时使用 100 条窗口限制返回量。
func (s *cronRunQueryStore) ListRunsByJob(ctx context.Context, jobID string, limit int32) ([]Run, error) {
	jobID, err := requireID(jobID)
	if err != nil {
		return nil, wrap(err, "list_runs_by_job")
	}
	if limit <= 0 {
		limit = 100
	}
	rows, err := s.q.ListCronJobRunsByJob(ctx, sqlc.ListCronJobRunsByJobParams{
		JobID: jobID,
		Limit: int64(limit),
	})
	if err != nil {
		return nil, wrap(err, "list_runs_by_job")
	}
	out := make([]Run, len(rows))
	for i, r := range rows {
		out[i] = fromCronJobRun(r)
	}
	return out, nil
}

// ListUnresolvedRuns 列出仍需调度器收尾处理的运行记录。
func (s *cronRunQueryStore) ListUnresolvedRuns(ctx context.Context) ([]Run, error) {
	rows, err := s.q.ListUnresolvedCronJobRuns(ctx)
	if err != nil {
		return nil, wrap(err, "list_unresolved_runs")
	}
	out := make([]Run, len(rows))
	for i, r := range rows {
		out[i] = fromCronJobRun(r)
	}
	return out, nil
}

// ListUnresolvedRunsPage 列出一页仍需调度器收尾处理的运行记录。
func (s *submitStore) ListUnresolvedRunsPage(ctx context.Context, limit int32, cursor string) ([]Run, error) {
	if limit <= 0 {
		return nil, wrap(errors.New("cron: unresolved run page limit must be positive"), "list_unresolved_runs_page")
	}
	q, ok := s.q.(unresolvedRunsPageQuerier)
	if !ok {
		return nil, wrap(errors.New("cron: querier does not implement paged unresolved run lookup"), "list_unresolved_runs_page")
	}
	rows, err := q.ListUnresolvedCronJobRunsPage(ctx, sqlc.ListUnresolvedCronJobRunsPageParams{
		Cursor: strings.TrimSpace(cursor),
		Limit:  int64(limit),
	})
	if err != nil {
		return nil, wrap(err, "list_unresolved_runs_page")
	}
	out := make([]Run, len(rows))
	for i, r := range rows {
		out[i] = fromCronJobRun(r)
	}
	return out, nil
}

// firstNonEmpty 返回第一个非空字符串，用于保留调用方显式传入的调度类型。
func firstNonEmpty(a, b string) string {
	if strings.TrimSpace(a) != "" {
		return a
	}
	return b
}

// GetRunningRunByTurnID 按 turn ID 查找仍在运行中的 Run，供 turn 回调定位 cron run。
func (s *cronRunQueryStore) GetRunningRunByTurnID(ctx context.Context, turnID string) (Run, error) {
	turnID = strings.TrimSpace(turnID)
	if turnID == "" {
		return Run{}, wrap(ErrJobRunNotFound, "get_running_run_by_turn_id")
	}
	row, err := s.q.GetRunningCronJobRunByTurnID(ctx, sqlc.GetRunningCronJobRunByTurnIDParams{TurnID: turnID})
	if err != nil {
		if platformdb.IsNotFound(err) {
			return Run{}, wrap(ErrJobRunNotFound, "get_running_run_by_turn_id")
		}
		return Run{}, wrap(err, "get_running_run_by_turn_id")
	}
	return fromCronJobRun(row), nil
}

// GetSubmittedOrRunningRunByTurnID 按 turn ID 查找 submitted/running Run，供终态事件抢先到达时收尾。
func (s *submitStore) GetSubmittedOrRunningRunByTurnID(ctx context.Context, turnID string) (Run, error) {
	turnID = strings.TrimSpace(turnID)
	if turnID == "" {
		return Run{}, wrap(ErrJobRunNotFound, "get_submitted_or_running_run_by_turn_id")
	}
	q, ok := s.q.(submittedOrRunningTurnQuerier)
	if !ok {
		return Run{}, wrap(errors.New("cron: querier does not implement submitted/running turn lookup"), "get_submitted_or_running_run_by_turn_id")
	}
	row, err := q.GetSubmittedOrRunningCronJobRunByTurnID(ctx, sqlc.GetSubmittedOrRunningCronJobRunByTurnIDParams{TurnID: turnID})
	if err != nil {
		if platformdb.IsNotFound(err) {
			return Run{}, wrap(ErrJobRunNotFound, "get_submitted_or_running_run_by_turn_id")
		}
		return Run{}, wrap(err, "get_submitted_or_running_run_by_turn_id")
	}
	return fromCronJobRun(row), nil
}

// ListJobsClaimedBy 列出指定 worker 当前持有的任务，空 claimedBy 不触发全表扫描。
func (s *cronJobQueryStore) ListJobsClaimedBy(ctx context.Context, claimedBy string) ([]Job, error) {
	claimedBy = strings.TrimSpace(claimedBy)
	if claimedBy == "" {
		return nil, nil
	}
	rows, err := s.q.ListCronJobsClaimedBy(ctx, sqlc.ListCronJobsClaimedByParams{ClaimedBy: claimedBy})
	if err != nil {
		return nil, wrap(err, "list_jobs_claimed_by")
	}
	out := make([]Job, len(rows))
	for i, r := range rows {
		out[i] = fromCronJob(r)
	}
	return out, nil
}

// boolToInt 将 Go bool 转成 SQLite 使用的 0/1 整数。
func boolToInt(b bool) int64 {
	if b {
		return 1
	}
	return 0
}

// wrap 为 cron store 错误附加操作名和实体名，保留上层 errors.Is 判断能力。
func wrap(err error, operation string) error {
	return platformdb.WrapStoreError(err, operation, "cron")
}
