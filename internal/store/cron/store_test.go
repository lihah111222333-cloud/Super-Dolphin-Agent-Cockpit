package cron

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"
	"time"

	platformdb "github.com/anthropic-ai/super-agent-v3/internal/platform/db"
	"github.com/anthropic-ai/super-agent-v3/internal/store/sqlc"
)

// cronQuerierStub is a minimal recording fake of the store's internal querier.
type cronQuerierStub struct {
	createFn            func(context.Context, sqlc.CreateCronJobParams) (sqlc.CronJob, error)
	getByIDFn           func(context.Context, string) (sqlc.CronJob, error)
	listFn              func(context.Context) ([]sqlc.CronJob, error)
	deleteFn            func(context.Context, string) error
	updateScheduleFn    func(context.Context, sqlc.UpdateCronJobScheduleParams) error
	setEnabledFn        func(context.Context, sqlc.SetCronJobEnabledParams) error
	claimFn             func(context.Context, sqlc.ClaimDueJobsForUpdateParams) ([]sqlc.CronJob, error)
	renewFn             func(context.Context, sqlc.RenewLeaseParams) (int64, error)
	extendFn            func(context.Context, sqlc.ExtendClaimParams) (int64, error)
	releaseFn           func(context.Context, sqlc.ReleaseClaimParams) (int64, error)
	markFinishedFn      func(context.Context, sqlc.MarkCronJobFinishedParams) (int64, error)
	markFailedFn        func(context.Context, sqlc.MarkCronJobFailedParams) (int64, error)
	setActiveTurnFn     func(context.Context, sqlc.SetCronJobActiveTurnParams) (int64, error)
	insertRunFn         func(context.Context, sqlc.InsertCronJobRunParams) (sqlc.CronJobRun, error)
	casRunStatusFn      func(context.Context, sqlc.CASCronJobRunStatusParams) (int64, error)
	setRunTurnFn        func(context.Context, sqlc.SetCronJobRunTurnParams) (int64, error)
	getRunByDedupeKeyFn func(context.Context, string) (sqlc.CronJobRun, error)
	getRunByIDFn        func(context.Context, string) (sqlc.CronJobRun, error)
	listRunsByJobFn     func(context.Context, sqlc.ListCronJobRunsByJobParams) ([]sqlc.CronJobRun, error)
	listUnresolvedFn    func(context.Context) ([]sqlc.CronJobRun, error)
}

func (s *cronQuerierStub) CreateCronJob(ctx context.Context, a sqlc.CreateCronJobParams) (sqlc.CronJob, error) {
	if s.createFn != nil {
		return s.createFn(ctx, a)
	}
	return sqlc.CronJob{ID: a.ID}, nil
}
func (s *cronQuerierStub) GetCronJobByID(ctx context.Context, arg sqlc.GetCronJobByIDParams) (sqlc.CronJob, error) {
	if s.getByIDFn != nil {
		return s.getByIDFn(ctx, arg.ID)
	}
	return sqlc.CronJob{ID: arg.ID}, nil
}
func (s *cronQuerierStub) ListCronJobs(ctx context.Context) ([]sqlc.CronJob, error) {
	if s.listFn != nil {
		return s.listFn(ctx)
	}
	return nil, nil
}
func (s *cronQuerierStub) DeleteCronJob(ctx context.Context, arg sqlc.DeleteCronJobParams) error {
	if s.deleteFn != nil {
		return s.deleteFn(ctx, arg.ID)
	}
	return nil
}
func (s *cronQuerierStub) UpdateCronJobSchedule(ctx context.Context, a sqlc.UpdateCronJobScheduleParams) error {
	if s.updateScheduleFn != nil {
		return s.updateScheduleFn(ctx, a)
	}
	return nil
}
func (s *cronQuerierStub) SetCronJobEnabled(ctx context.Context, a sqlc.SetCronJobEnabledParams) error {
	if s.setEnabledFn != nil {
		return s.setEnabledFn(ctx, a)
	}
	return nil
}
func (s *cronQuerierStub) PatchCronJobNextRunAt(ctx context.Context, a sqlc.PatchCronJobNextRunAtParams) error {
	return nil
}
func (s *cronQuerierStub) ClaimDueJobsForUpdate(ctx context.Context, a sqlc.ClaimDueJobsForUpdateParams) ([]sqlc.CronJob, error) {
	if s.claimFn != nil {
		return s.claimFn(ctx, a)
	}
	return nil, nil
}
func (s *cronQuerierStub) RenewLease(ctx context.Context, a sqlc.RenewLeaseParams) (int64, error) {
	if s.renewFn != nil {
		return s.renewFn(ctx, a)
	}
	return 1, nil
}
func (s *cronQuerierStub) ExtendClaim(ctx context.Context, a sqlc.ExtendClaimParams) (int64, error) {
	if s.extendFn != nil {
		return s.extendFn(ctx, a)
	}
	return 1, nil
}
func (s *cronQuerierStub) ReleaseClaim(ctx context.Context, a sqlc.ReleaseClaimParams) (int64, error) {
	if s.releaseFn != nil {
		return s.releaseFn(ctx, a)
	}
	return 1, nil
}
func (s *cronQuerierStub) MarkCronJobFinished(ctx context.Context, a sqlc.MarkCronJobFinishedParams) (int64, error) {
	if s.markFinishedFn != nil {
		return s.markFinishedFn(ctx, a)
	}
	return 1, nil
}
func (s *cronQuerierStub) MarkCronJobFailed(ctx context.Context, a sqlc.MarkCronJobFailedParams) (int64, error) {
	if s.markFailedFn != nil {
		return s.markFailedFn(ctx, a)
	}
	return 1, nil
}
func (s *cronQuerierStub) SetCronJobActiveTurn(ctx context.Context, a sqlc.SetCronJobActiveTurnParams) (int64, error) {
	if s.setActiveTurnFn != nil {
		return s.setActiveTurnFn(ctx, a)
	}
	return 1, nil
}
func (s *cronQuerierStub) InsertCronJobRun(ctx context.Context, a sqlc.InsertCronJobRunParams) (sqlc.CronJobRun, error) {
	if s.insertRunFn != nil {
		return s.insertRunFn(ctx, a)
	}
	return sqlc.CronJobRun{ID: a.ID, JobID: a.JobID, Status: a.Status}, nil
}
func (s *cronQuerierStub) CASCronJobRunStatus(ctx context.Context, a sqlc.CASCronJobRunStatusParams) (int64, error) {
	if s.casRunStatusFn != nil {
		return s.casRunStatusFn(ctx, a)
	}
	return 1, nil
}
func (s *cronQuerierStub) SetCronJobRunTurn(ctx context.Context, a sqlc.SetCronJobRunTurnParams) (int64, error) {
	if s.setRunTurnFn != nil {
		return s.setRunTurnFn(ctx, a)
	}
	return 1, nil
}
func (s *cronQuerierStub) GetCronJobRunByDedupeKey(ctx context.Context, arg sqlc.GetCronJobRunByDedupeKeyParams) (sqlc.CronJobRun, error) {
	if s.getRunByDedupeKeyFn != nil {
		return s.getRunByDedupeKeyFn(ctx, arg.DedupeKey)
	}
	return sqlc.CronJobRun{}, nil
}
func (s *cronQuerierStub) GetCronJobRunByID(ctx context.Context, arg sqlc.GetCronJobRunByIDParams) (sqlc.CronJobRun, error) {
	if s.getRunByIDFn != nil {
		return s.getRunByIDFn(ctx, arg.ID)
	}
	return sqlc.CronJobRun{ID: arg.ID}, nil
}
func (s *cronQuerierStub) ListCronJobRunsByJob(ctx context.Context, a sqlc.ListCronJobRunsByJobParams) ([]sqlc.CronJobRun, error) {
	if s.listRunsByJobFn != nil {
		return s.listRunsByJobFn(ctx, a)
	}
	return nil, nil
}
func (s *cronQuerierStub) ListUnresolvedCronJobRuns(ctx context.Context) ([]sqlc.CronJobRun, error) {
	if s.listUnresolvedFn != nil {
		return s.listUnresolvedFn(ctx)
	}
	return nil, nil
}
func (s *cronQuerierStub) GetRunningCronJobRunByTurnID(ctx context.Context, arg sqlc.GetRunningCronJobRunByTurnIDParams) (sqlc.CronJobRun, error) {
	return sqlc.CronJobRun{}, platformdb.ErrNotFound
}
func (s *cronQuerierStub) ListCronJobsClaimedBy(ctx context.Context, arg sqlc.ListCronJobsClaimedByParams) ([]sqlc.CronJob, error) {
	return nil, nil
}

// ----- CreateJob validation -----

func TestCreateJobValidatesRequiredFields(t *testing.T) {
	t.Parallel()

	now := time.Unix(1_700_000_000, 0).UTC()
	base := CreateJobParams{
		ID: "job-1", Name: "daily", Prompt: "check",
		ScheduleType: "cron", ScheduleExpr: "0 9 * * *", Timezone: "UTC",
		Provider: ProviderCodex, CWD: "/repo", Enabled: true,
		NextRunAt: now, CreatedAt: now, UpdatedAt: now,
	}

	cases := []struct {
		name   string
		mutate func(*CreateJobParams)
		want   error
	}{
		{"empty id", func(p *CreateJobParams) { p.ID = "" }, ErrEmptyID},
		{"empty cwd", func(p *CreateJobParams) { p.CWD = "  " }, ErrEmptyCWD},
		{"empty provider", func(p *CreateJobParams) { p.Provider = "" }, ErrEmptyProvider},
		{"empty schedule_expr", func(p *CreateJobParams) { p.ScheduleExpr = "" }, ErrEmptyScheduleExpr},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			s := &store{q: &cronQuerierStub{}}
			p := base
			tc.mutate(&p)
			_, err := s.CreateJob(context.Background(), p)
			if !errors.Is(err, tc.want) {
				t.Fatalf("CreateJob(%s) error = %v, want %v", tc.name, err, tc.want)
			}
		})
	}
}

func TestCreateJobForwardsDefaultsToSqlc(t *testing.T) {
	t.Parallel()

	now := time.Unix(1_700_000_000, 0).UTC()
	var got sqlc.CreateCronJobParams
	s := &store{q: &cronQuerierStub{
		createFn: func(_ context.Context, a sqlc.CreateCronJobParams) (sqlc.CronJob, error) {
			got = a
			return sqlc.CronJob{ID: a.ID}, nil
		},
	}}
	_, err := s.CreateJob(context.Background(), CreateJobParams{
		ID: "job-1", Name: "daily", Prompt: "check",
		ScheduleExpr: "0 9 * * *", Provider: ProviderCodex, CWD: "/repo",
		Enabled: true, NextRunAt: now, CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		t.Fatalf("CreateJob() error = %v", err)
	}
	if got.ScheduleType != "cron" {
		t.Fatalf("ScheduleType default = %q, want cron", got.ScheduleType)
	}
	if string(got.Config) != "{}" {
		t.Fatalf("Config default = %q, want {}", got.Config)
	}
	if string(got.Skills) != "[]" {
		t.Fatalf("Skills default = %q, want []", got.Skills)
	}
}

// ----- GetJobByID not-found -----

func TestGetJobByIDMapsNotFound(t *testing.T) {
	t.Parallel()
	s := &store{q: &cronQuerierStub{
		getByIDFn: func(context.Context, string) (sqlc.CronJob, error) {
			return sqlc.CronJob{}, platformdb.ErrNotFound
		},
	}}
	_, err := s.GetJobByID(context.Background(), "missing")
	if !errors.Is(err, ErrJobNotFound) {
		t.Fatalf("err = %v, want ErrJobNotFound", err)
	}
}

// ----- claim / lease -----

func TestClaimDueJobsForUpdateRequiresTokenAndClaimedBy(t *testing.T) {
	t.Parallel()
	s := &store{q: &cronQuerierStub{}}
	_, err := s.ClaimDueJobsForUpdate(context.Background(), ClaimDueJobsForUpdateParams{ClaimedBy: "scheduler"})
	if !errors.Is(err, ErrEmptyClaimToken) {
		t.Fatalf("claim without token: err = %v, want ErrEmptyClaimToken", err)
	}
	_, err = s.ClaimDueJobsForUpdate(context.Background(), ClaimDueJobsForUpdateParams{ClaimToken: "tok"})
	if err == nil || !contains(err.Error(), "claimed_by is required") {
		t.Fatalf("claim without claimed_by: err = %v", err)
	}
}

func TestClaimDueJobsForUpdateForwardsParamsAndMapsRows(t *testing.T) {
	t.Parallel()

	now := time.Unix(1_700_000_000, 0).UTC()
	leaseExpiry := now.Add(30 * time.Minute)
	var got sqlc.ClaimDueJobsForUpdateParams
	s := &store{q: &cronQuerierStub{
		claimFn: func(_ context.Context, a sqlc.ClaimDueJobsForUpdateParams) ([]sqlc.CronJob, error) {
			got = a
			return []sqlc.CronJob{{ID: "job-1", Name: "daily", Provider: "codex", CWD: "/repo"}}, nil
		},
	}}
	jobs, err := s.ClaimDueJobsForUpdate(context.Background(), ClaimDueJobsForUpdateParams{
		Now: now, ClaimedBy: "scheduler-A",
		LeaseExpiresAt: leaseExpiry, ClaimToken: "tok-uuid", MaxClaim: 8,
	})
	if err != nil {
		t.Fatalf("ClaimDueJobsForUpdate error = %v", err)
	}
	if len(jobs) != 1 {
		t.Fatalf("ClaimDueJobsForUpdate rows = %+v", jobs)
	}
	gotSummary := []any{jobs[0].ID, jobs[0].Provider, got.ClaimToken, got.ClaimedBy, got.MaxClaim, time.UnixMilli(*got.Now).UTC(), time.UnixMilli(*got.LeaseExpiresAt).UTC()}
	if wantSummary := []any{"job-1", "codex", "tok-uuid", "scheduler-A", int64(8), now, leaseExpiry}; !reflect.DeepEqual(gotSummary, wantSummary) {
		t.Fatalf("ClaimDueJobsForUpdate summary = %+v, want %+v", gotSummary, wantSummary)
	}
}

func TestClaimDueJobsForUpdateDefaultsMaxClaim(t *testing.T) {
	t.Parallel()
	var got sqlc.ClaimDueJobsForUpdateParams
	s := &store{q: &cronQuerierStub{
		claimFn: func(_ context.Context, a sqlc.ClaimDueJobsForUpdateParams) ([]sqlc.CronJob, error) {
			got = a
			return nil, nil
		},
	}}
	_, err := s.ClaimDueJobsForUpdate(context.Background(), ClaimDueJobsForUpdateParams{
		ClaimedBy: "s", ClaimToken: "t",
	})
	if err != nil {
		t.Fatalf("ClaimDueJobsForUpdate error = %v", err)
	}
	if got.MaxClaim <= 0 {
		t.Fatalf("MaxClaim default = %d, want positive", got.MaxClaim)
	}
}

func TestClaimDueJobsForUpdateRetriesSQLiteBusy(t *testing.T) {
	t.Parallel()
	attempts := 0
	s := &store{q: &cronQuerierStub{
		claimFn: func(context.Context, sqlc.ClaimDueJobsForUpdateParams) ([]sqlc.CronJob, error) {
			attempts++
			if attempts < 3 {
				return nil, errors.New("database is locked")
			}
			return []sqlc.CronJob{{ID: "job-1"}}, nil
		},
	}}
	jobs, err := s.ClaimDueJobsForUpdate(context.Background(), ClaimDueJobsForUpdateParams{
		ClaimedBy:  "scheduler",
		ClaimToken: "tok",
	})
	if err != nil {
		t.Fatalf("ClaimDueJobsForUpdate error = %v", err)
	}
	if len(jobs) != 1 || jobs[0].ID != "job-1" {
		t.Fatalf("jobs = %+v, want job-1", jobs)
	}
	if attempts != 3 {
		t.Fatalf("attempts = %d, want 3", attempts)
	}
}

func TestClaimDueJobsForUpdateSQLiteBusyExhaustionReturnsContext(t *testing.T) {
	t.Parallel()
	const wantAttempts = 3
	attempts := 0
	s := &store{q: &cronQuerierStub{
		claimFn: func(context.Context, sqlc.ClaimDueJobsForUpdateParams) ([]sqlc.CronJob, error) {
			attempts++
			return nil, errors.New("database is locked")
		},
	}}
	_, err := s.ClaimDueJobsForUpdate(context.Background(), ClaimDueJobsForUpdateParams{
		ClaimedBy:  "scheduler",
		ClaimToken: "tok",
	})
	if err == nil {
		t.Fatal("ClaimDueJobsForUpdate error = nil, want locked error")
	}
	if attempts != wantAttempts {
		t.Fatalf("attempts = %d, want %d", attempts, wantAttempts)
	}
	if !errors.Is(err, platformdb.ErrTimeout) || !contains(err.Error(), "claim_due_jobs cron") || !contains(err.Error(), "database is locked") {
		t.Fatalf("err = %v, want wrapped timeout with claim context", err)
	}
}

func TestRenewLeaseReturnsTokenMismatchOnZeroRows(t *testing.T) {
	t.Parallel()
	s := &store{q: &cronQuerierStub{
		renewFn: func(context.Context, sqlc.RenewLeaseParams) (int64, error) { return 0, nil },
	}}
	err := s.RenewLease(context.Background(), LeaseParams{ID: "j", ClaimToken: "tok"})
	if !errors.Is(err, ErrClaimTokenMismatch) {
		t.Fatalf("err = %v, want ErrClaimTokenMismatch", err)
	}
}

func TestExtendClaimReturnsTokenMismatchOnZeroRows(t *testing.T) {
	t.Parallel()
	s := &store{q: &cronQuerierStub{
		extendFn: func(context.Context, sqlc.ExtendClaimParams) (int64, error) { return 0, nil },
	}}
	err := s.ExtendClaim(context.Background(), LeaseParams{ID: "j", ClaimToken: "tok"})
	if !errors.Is(err, ErrClaimTokenMismatch) {
		t.Fatalf("err = %v, want ErrClaimTokenMismatch", err)
	}
}

func TestMarkFinishedRejectsEmptyClaimToken(t *testing.T) {
	t.Parallel()
	s := &store{q: &cronQuerierStub{}}
	err := s.MarkFinished(context.Background(), MarkFinishedParams{ID: "j"})
	if !errors.Is(err, ErrEmptyClaimToken) {
		t.Fatalf("err = %v, want ErrEmptyClaimToken", err)
	}
}

func TestMarkFinishedReturnsTokenMismatchOnZeroRows(t *testing.T) {
	t.Parallel()
	s := &store{q: &cronQuerierStub{
		markFinishedFn: func(context.Context, sqlc.MarkCronJobFinishedParams) (int64, error) {
			return 0, nil
		},
	}}
	err := s.MarkFinished(context.Background(), MarkFinishedParams{ID: "j", ClaimToken: "tok", RunID: "run-1"})
	if !errors.Is(err, ErrClaimTokenMismatch) {
		t.Fatalf("err = %v, want ErrClaimTokenMismatch", err)
	}
}

func TestMarkFailedDefaultsStatus(t *testing.T) {
	t.Parallel()
	var got sqlc.MarkCronJobFailedParams
	s := &store{q: &cronQuerierStub{
		markFailedFn: func(_ context.Context, a sqlc.MarkCronJobFailedParams) (int64, error) {
			got = a
			return 1, nil
		},
	}}
	err := s.MarkFailed(context.Background(), MarkFailedParams{ID: "j", ClaimToken: "tok", RunID: "run-1"})
	if err != nil {
		t.Fatalf("MarkFailed error = %v", err)
	}
	if got.LastStatus != StatusFailed {
		t.Fatalf("LastStatus = %q, want %q", got.LastStatus, StatusFailed)
	}
}

// ----- run state machine -----

func TestInsertRunDefaultsStatusToPending(t *testing.T) {
	t.Parallel()
	var got sqlc.InsertCronJobRunParams
	s := &store{q: &cronQuerierStub{
		insertRunFn: func(_ context.Context, a sqlc.InsertCronJobRunParams) (sqlc.CronJobRun, error) {
			got = a
			return sqlc.CronJobRun{ID: a.ID, Status: a.Status}, nil
		},
	}}
	_, err := s.InsertRun(context.Background(), InsertRunParams{
		ID: "run-1", JobID: "job-1", IdempotencyKey: "k",
	})
	if err != nil {
		t.Fatalf("InsertRun error = %v", err)
	}
	if got.Status != StatusPending {
		t.Fatalf("Status default = %q, want %q", got.Status, StatusPending)
	}
}

func TestCASRunStatusRejectsMismatch(t *testing.T) {
	t.Parallel()
	s := &store{q: &cronQuerierStub{
		casRunStatusFn: func(context.Context, sqlc.CASCronJobRunStatusParams) (int64, error) {
			return 0, nil
		},
	}}
	err := s.CASRunStatus(context.Background(), CASRunStatusParams{
		ID: "run-1", ExpectedStatus: StatusSubmitting, NextStatus: StatusSubmitted,
	})
	if !errors.Is(err, ErrStatusTransitionRefused) {
		t.Fatalf("err = %v, want ErrStatusTransitionRefused", err)
	}
}

func TestCASRunStatusForwardsExpectedAndNext(t *testing.T) {
	t.Parallel()
	var got sqlc.CASCronJobRunStatusParams
	s := &store{q: &cronQuerierStub{
		casRunStatusFn: func(_ context.Context, a sqlc.CASCronJobRunStatusParams) (int64, error) {
			got = a
			return 1, nil
		},
	}}
	err := s.CASRunStatus(context.Background(), CASRunStatusParams{
		ID: "run-1", ExpectedStatus: StatusSubmitting, NextStatus: StatusSubmitted,
	})
	if err != nil {
		t.Fatalf("CASRunStatus error = %v", err)
	}
	if got.ExpectedStatus != StatusSubmitting || got.NextStatus != StatusSubmitted {
		t.Fatalf("CAS params = %+v", got)
	}
}

func TestGetRunByDedupeKeyRejectsEmpty(t *testing.T) {
	t.Parallel()
	s := &store{q: &cronQuerierStub{}}
	_, err := s.GetRunByDedupeKey(context.Background(), "")
	if !errors.Is(err, ErrJobRunNotFound) {
		t.Fatalf("err = %v, want ErrJobRunNotFound", err)
	}
}

func TestGetRunByDedupeKeyMapsNotFound(t *testing.T) {
	t.Parallel()
	s := &store{q: &cronQuerierStub{
		getRunByDedupeKeyFn: func(context.Context, string) (sqlc.CronJobRun, error) {
			return sqlc.CronJobRun{}, platformdb.ErrNotFound
		},
	}}
	_, err := s.GetRunByDedupeKey(context.Background(), "dedupe-x")
	if !errors.Is(err, ErrJobRunNotFound) {
		t.Fatalf("err = %v, want ErrJobRunNotFound", err)
	}
}

func TestListUnresolvedRunsPassesThrough(t *testing.T) {
	t.Parallel()
	s := &store{q: &cronQuerierStub{
		listUnresolvedFn: func(context.Context) ([]sqlc.CronJobRun, error) {
			return []sqlc.CronJobRun{
				{ID: "r1", Status: StatusSubmitting},
				{ID: "r2", Status: StatusSubmitted},
				{ID: "r3", Status: StatusRunning},
			}, nil
		},
	}}
	rows, err := s.ListUnresolvedRuns(context.Background())
	if err != nil {
		t.Fatalf("ListUnresolvedRuns error = %v", err)
	}
	if len(rows) != 3 {
		t.Fatalf("len(rows) = %d, want 3", len(rows))
	}
	for _, r := range rows {
		switch r.Status {
		case StatusSubmitting, StatusSubmitted, StatusRunning:
		default:
			t.Fatalf("unexpected status in unresolved list: %q", r.Status)
		}
	}
}

// ----- migration + query lint (schema-level guarantees) -----

func TestClaimQueryUsesSQLiteAtomicClaimSemantics(t *testing.T) {
	t.Parallel()
	sql := readCronQuerySQL(t, "cron_job.sql")
	for _, want := range []string{
		"WHERE id IN (",
		"claim_token = '' OR COALESCE(lease_expires_at, 0) <= sqlc.arg(now)",
		"COALESCE(next_retry_at, next_run_at) <= sqlc.arg(now)",
		"LIMIT sqlc.arg(max_claim)",
		"claim_token      = sqlc.arg(claim_token)", "active_turn_id = sqlc.arg(expected_active_turn_id)",
		"cron_job_runs.id = sqlc.arg(run_id)", "cron_job_runs.job_id = cron_jobs.id",
		"cron_job_runs.turn_id = sqlc.arg(expected_active_turn_id)", "COALESCE(NULLIF(sqlc.arg(thread_id), ''), thread_id)",
	} {
		if !contains(sql, want) {
			t.Fatalf("cron_job.sql missing required fragment %q", want)
		}
	}
	if idx := indexOf(sql, "FOR UPDATE SKIP LOCKED"); idx >= 0 {
		if !onCommentLine(sql, idx) {
			t.Fatalf("cron_job.sql still contains live FOR UPDATE SKIP LOCKED; SQLite does not support it")
		}
	}
}

// onCommentLine reports whether the byte at idx sits on a `--` SQL comment line.
func onCommentLine(sql string, idx int) bool {
	lineStart := idx
	for lineStart > 0 && sql[lineStart-1] != '\n' {
		lineStart--
	}
	line := sql[lineStart:idx]
	return indexOf(line, "--") >= 0
}

func TestMigration0045CrashRecoveryStatuses(t *testing.T) {
	t.Parallel()
	sql := readMigration0045(t)
	// The CHECK constraint must list every status the state machine
	// uses so the DB layer refuses to persist a typo'd status.
	for _, want := range []string{
		"'pending'", "'submitting'", "'submitted'",
		"'running'", "'finished'", "'failed'", "'observe_lost'",
	} {
		if !contains(sql, want) {
			t.Fatalf("migration 0045 missing status %s in CHECK constraint", want)
		}
	}
}

func TestMigration0045HasDueAndDedupeIndexes(t *testing.T) {
	t.Parallel()
	sql := readMigration0045(t)
	for _, want := range []string{
		"idx_cron_jobs_due",
		"uq_cron_job_runs_idempotency",
		"uq_cron_job_runs_dedupe_key",
	} {
		if !contains(sql, want) {
			t.Fatalf("migration 0045 missing required index %q", want)
		}
	}
}

// ----- helpers -----

func contains(s, sub string) bool {
	return len(sub) == 0 || (len(s) >= len(sub) && indexOf(s, sub) >= 0)
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

// readMigration0045 loads the 0045 migration SQL from the repo checkout.
func readMigration0045(t *testing.T) string {
	t.Helper()
	return readRepoFile(t, "migrations/0045_cron_jobs.sql")
}

// readCronQuerySQL loads sql/queries/<name> from the repo checkout.
func readCronQuerySQL(t *testing.T, name string) string {
	t.Helper()
	return readRepoFile(t, "sql/queries/"+name)
}

// readRepoFile walks up from the test source file to the go.mod root and
// reads `rel` relative to that root. Keeps tests independent of the `go
// test` working directory.
func readRepoFile(t *testing.T, rel string) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	dir := filepath.Dir(file)
	for i := 0; i < 8; i++ {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			data, err := os.ReadFile(filepath.Join(dir, rel))
			if err != nil {
				t.Fatalf("read %s: %v", rel, err)
			}
			return string(data)
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	t.Fatalf("go.mod not found walking up from %s", file)
	return ""
}
