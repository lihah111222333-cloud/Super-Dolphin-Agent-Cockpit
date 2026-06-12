package cron

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	platformdb "github.com/anthropic-ai/super-agent-v3/internal/platform/db"
	"github.com/anthropic-ai/super-agent-v3/internal/store/sqlc"
	"github.com/anthropic-ai/super-agent-v3/pkg/logger"
)

type querier interface {
	CreateCronJob(ctx context.Context, arg sqlc.CreateCronJobParams) (sqlc.CronJob, error)
	GetCronJobByID(ctx context.Context, arg sqlc.GetCronJobByIDParams) (sqlc.CronJob, error)
	ListCronJobs(ctx context.Context) ([]sqlc.CronJob, error)
	DeleteCronJob(ctx context.Context, arg sqlc.DeleteCronJobParams) error
	UpdateCronJobSchedule(ctx context.Context, arg sqlc.UpdateCronJobScheduleParams) error
	SetCronJobEnabled(ctx context.Context, arg sqlc.SetCronJobEnabledParams) error
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

type store struct{ q querier }

const cronClaimBusyRetryAttempts = 3

// NewStore returns the production Store backed by the sqlc queries.
func NewStore(q *sqlc.Queries) Store { return &store{q: q} }

// ----- validation helpers -----

func requireID(id string) (string, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return "", ErrEmptyID
	}
	return id, nil
}

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

func ts(t time.Time) int64 {
	if t.IsZero() {
		return 0
	}
	return platformdb.Millis(t)
}

func tsPtr(t time.Time) *int64 {
	if t.IsZero() {
		return nil
	}
	ms := platformdb.Millis(t)
	return &ms
}

func bytesOrDefault(b []byte, def string) []byte {
	if len(b) == 0 {
		return []byte(def)
	}
	return b
}

// ----- jobs -----

func (s *store) CreateJob(ctx context.Context, p CreateJobParams) (Job, error) {
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

func (s *store) GetJobByID(ctx context.Context, id string) (Job, error) {
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

func (s *store) ListJobs(ctx context.Context) ([]Job, error) {
	rows, err := s.q.ListCronJobs(ctx)
	if err != nil {
		return nil, wrap(err, "list_jobs")
	}
	out := make([]Job, len(rows))
	for i, r := range rows {
		out[i] = fromCronJob(r)
	}
	return out, nil
}

func (s *store) DeleteJob(ctx context.Context, id string) error {
	id, err := requireID(id)
	if err != nil {
		return wrap(err, "delete_job")
	}
	return wrap(s.q.DeleteCronJob(ctx, sqlc.DeleteCronJobParams{ID: id}), "delete_job")
}

func (s *store) UpdateJobSchedule(ctx context.Context, p UpdateJobScheduleParams) error {
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

func (s *store) SetJobEnabled(ctx context.Context, id string, enabled bool, now time.Time) error {
	id, err := requireID(id)
	if err != nil {
		return wrap(err, "set_job_enabled")
	}
	return wrap(s.q.SetCronJobEnabled(ctx, sqlc.SetCronJobEnabledParams{
		Enabled:   boolToInt(enabled),
		UpdatedAt: ts(now),
		ID:        id,
	}), "set_job_enabled")
}

func (s *store) PatchNextRunAt(ctx context.Context, id string, nextRunAt time.Time, now time.Time) error {
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

// ----- claim / lease -----

func (s *store) ClaimDueJobsForUpdate(ctx context.Context, p ClaimDueJobsForUpdateParams) ([]Job, error) {
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

func (s *store) claimDueJobsWithRetry(ctx context.Context, arg sqlc.ClaimDueJobsForUpdateParams) ([]sqlc.CronJob, error) {
	log := logger.FromContext(ctx)
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
			log.ErrorContext(ctx, "cron claim sqlite busy retry exhausted",
				logger.FieldComponent, "cron_store",
				logger.FieldAction, "claim_due_jobs",
				logger.FieldCount, attempt,
				logger.FieldError, err,
			)
			return nil, fmt.Errorf("sqlite claim busy after %d attempts: %w", attempt, err)
		}
		log.WarnContext(ctx, "cron claim sqlite busy; retrying",
			logger.FieldComponent, "cron_store",
			logger.FieldAction, "claim_due_jobs",
			logger.FieldCount, attempt,
			logger.FieldMax, cronClaimBusyRetryAttempts,
			logger.FieldError, err,
		)
	}
	return nil, lastErr
}

func (s *store) RenewLease(ctx context.Context, p LeaseParams) error {
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

func (s *store) ExtendClaim(ctx context.Context, p LeaseParams) error {
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
		// Either token mismatch or the caller asked for a shorter TTL
		// than the current one. Both are benign from the store POV;
		// expose token-mismatch as an error so upper layers can branch
		// on it without accidentally downgrading TTLs.
		return wrap(ErrClaimTokenMismatch, "extend_claim")
	}
	return nil
}

func (s *store) ReleaseClaim(ctx context.Context, id, claimToken string, now time.Time) error {
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

func (s *store) MarkFinished(ctx context.Context, p MarkFinishedParams) error {
	id, token, err := requireClaim(p.ID, p.ClaimToken)
	if err != nil {
		return wrap(err, "mark_finished")
	}
	rows, err := s.q.MarkCronJobFinished(ctx, sqlc.MarkCronJobFinishedParams{
		LastRunAt:  tsPtr(p.LastRunAt),
		LastTurnID: p.LastTurnID,
		NextRunAt:  ts(p.NextRunAt),
		Now:        ts(p.Now),
		ID:         id,
		ClaimToken: token,
	})
	if err != nil {
		return wrap(err, "mark_finished")
	}
	if rows == 0 {
		return wrap(ErrClaimTokenMismatch, "mark_finished")
	}
	return nil
}

func (s *store) MarkFailed(ctx context.Context, p MarkFailedParams) error {
	id, token, err := requireClaim(p.ID, p.ClaimToken)
	if err != nil {
		return wrap(err, "mark_failed")
	}
	status := strings.TrimSpace(p.LastStatus)
	if status == "" {
		status = StatusFailed
	}
	rows, err := s.q.MarkCronJobFailed(ctx, sqlc.MarkCronJobFailedParams{
		LastRunAt:   tsPtr(p.LastRunAt),
		LastTurnID:  p.LastTurnID,
		LastStatus:  status,
		LastErrorAt: tsPtr(p.LastErrorAt),
		LastError:   p.LastError,
		NextRetryAt: tsPtr(p.NextRetryAt),
		Now:         ts(p.Now),
		ID:          id,
		ClaimToken:  token,
	})
	if err != nil {
		return wrap(err, "mark_failed")
	}
	if rows == 0 {
		return wrap(ErrClaimTokenMismatch, "mark_failed")
	}
	return nil
}

func (s *store) SetActiveTurn(ctx context.Context, p SetActiveTurnParams) error {
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

// ----- runs -----

func (s *store) InsertRun(ctx context.Context, p InsertRunParams) (Run, error) {
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

func (s *store) CASRunStatus(ctx context.Context, p CASRunStatusParams) error {
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

func (s *store) SetRunTurn(ctx context.Context, p SetRunTurnParams) error {
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

func (s *store) GetRunByID(ctx context.Context, id string) (Run, error) {
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

func (s *store) GetRunByDedupeKey(ctx context.Context, dedupeKey string) (Run, error) {
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

func (s *store) ListRunsByJob(ctx context.Context, jobID string, limit int32) ([]Run, error) {
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

func (s *store) ListUnresolvedRuns(ctx context.Context) ([]Run, error) {
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

func firstNonEmpty(a, b string) string {
	if strings.TrimSpace(a) != "" {
		return a
	}
	return b
}

func (s *store) GetRunningRunByTurnID(ctx context.Context, turnID string) (Run, error) {
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

func (s *store) ListJobsClaimedBy(ctx context.Context, claimedBy string) ([]Job, error) {
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

func boolToInt(b bool) int64 {
	if b {
		return 1
	}
	return 0
}

func wrap(err error, operation string) error {
	return platformdb.WrapStoreError(err, operation, "cron")
}
