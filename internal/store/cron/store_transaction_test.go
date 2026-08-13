package cron

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/store/sqlc"
)

// TestSubmitRunWithActiveTurnPersistsAllFieldsWithExplicitDB locks the explicit DB constructor path.
func TestSubmitRunWithActiveTurnPersistsAllFieldsWithExplicitDB(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store, db := openSubmitRunStore(t, "submit-success")
	now := time.Unix(1_700_000_000, 0).UTC()
	seedClaimedSubmittingRun(ctx, t, store, now)

	err := store.SubmitRunWithActiveTurn(ctx, SubmitRunWithActiveTurnParams{
		RunID: "run-submit", JobID: "job-submit", ClaimToken: "claim-token",
		ActiveTurnID: "turn-submit", ThreadID: "thread-submit", AgentID: "agent-submit",
		SubmittedAt: now.Add(time.Second), Now: now.Add(2 * time.Second),
	})
	if err != nil {
		t.Fatalf("SubmitRunWithActiveTurn() error = %v", err)
	}
	assertSubmittedTurnState(ctx, t, db, "turn-submit", StatusSubmitted)
}

// TestIsTurnOwnedUsesCurrentRunAndJobFences 固定判域查询只接受仍由 Cron claim 持有的未解决 turn。
func TestIsTurnOwnedUsesCurrentRunAndJobFences(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store, _ := openSubmitRunStore(t, "turn-ownership")
	now := time.Unix(1_700_000_000, 0).UTC()
	seedClaimedSubmittingRun(ctx, t, store, now)

	owned, err := store.IsTurnOwned(ctx, "ordinary-turn")
	if err != nil || owned {
		t.Fatalf("ordinary turn ownership = %v, %v; want false, nil", owned, err)
	}
	if err := store.SubmitRunWithActiveTurn(ctx, SubmitRunWithActiveTurnParams{
		RunID: "run-submit", JobID: "job-submit", ClaimToken: "claim-token",
		ActiveTurnID: "turn-submit", SubmittedAt: now, Now: now,
	}); err != nil {
		t.Fatalf("SubmitRunWithActiveTurn() error = %v", err)
	}
	owned, err = store.IsTurnOwned(ctx, "turn-submit")
	if err != nil || !owned {
		t.Fatalf("submitted cron turn ownership = %v, %v; want true, nil", owned, err)
	}
	if err := store.FinalizeRecoveredRun(ctx, FinalizeRecoveredRunParams{
		ExpectedRunStatus: StatusSubmitted,
		MarkFailedParams: MarkFailedParams{
			ID: "job-submit", ClaimToken: "claim-token", RunID: "run-submit",
			ExpectedActiveTurnID: "turn-submit", LastRunAt: now, LastTurnID: "turn-submit",
			LastStatus: StatusFinished, NextRunAt: now.Add(time.Hour), Now: now,
		},
	}); err != nil {
		t.Fatalf("FinalizeRecoveredRun() error = %v", err)
	}
	owned, err = store.IsTurnOwned(ctx, "turn-submit")
	if err != nil || owned {
		t.Fatalf("finalized cron turn ownership = %v, %v; want false, nil", owned, err)
	}
}

// TestSubmitRunWithActiveTurnRollsBackWhenActiveTurnFenceFails proves one-transaction semantics.
func TestSubmitRunWithActiveTurnRollsBackWhenActiveTurnFenceFails(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store, db := openSubmitRunStore(t, "submit-rollback")
	now := time.Unix(1_700_000_000, 0).UTC()
	seedClaimedSubmittingRun(ctx, t, store, now)

	err := store.SubmitRunWithActiveTurn(ctx, SubmitRunWithActiveTurnParams{
		RunID: "run-submit", JobID: "job-submit", ClaimToken: "wrong-token",
		ActiveTurnID: "turn-submit", ThreadID: "thread-submit", AgentID: "agent-submit",
		SubmittedAt: now.Add(time.Second), Now: now.Add(2 * time.Second),
	})
	if !errors.Is(err, ErrClaimTokenMismatch) {
		t.Fatalf("SubmitRunWithActiveTurn() error = %v, want %v", err, ErrClaimTokenMismatch)
	}
	assertSubmittedTurnState(ctx, t, db, "", StatusSubmitting)
}

func TestFinalizeRecoveredRunRollsBackRunWhenJobFenceFails(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store, _ := openSubmitRunStore(t, "recover-finalize-rollback")
	now := time.Unix(1_700_000_000, 0).UTC()
	seedClaimedSubmittingRun(ctx, t, store, now)
	if err := store.SubmitRunWithActiveTurn(ctx, SubmitRunWithActiveTurnParams{
		RunID: "run-submit", JobID: "job-submit", ClaimToken: "claim-token", ActiveTurnID: "turn-submit",
		SubmittedAt: now, Now: now,
	}); err != nil {
		t.Fatalf("SubmitRunWithActiveTurn() error = %v", err)
	}

	err := store.FinalizeRecoveredRun(ctx, FinalizeRecoveredRunParams{
		ExpectedRunStatus: StatusSubmitted,
		MarkFailedParams: MarkFailedParams{
			ID: "job-submit", ClaimToken: "wrong-token", RunID: "run-submit", ExpectedActiveTurnID: "turn-submit",
			LastRunAt: now, LastTurnID: "turn-submit", LastStatus: StatusObserveLost,
			LastErrorAt: now, LastError: "observe failed", NextRunAt: now.Add(time.Hour), Now: now,
		},
	})
	if !errors.Is(err, ErrClaimTokenMismatch) {
		t.Fatalf("FinalizeRecoveredRun() error = %v, want %v", err, ErrClaimTokenMismatch)
	}
	run, err := store.GetRunByID(ctx, "run-submit")
	if err != nil {
		t.Fatalf("GetRunByID() error = %v", err)
	}
	if run.Status != StatusSubmitted {
		t.Fatalf("run status = %q, want transaction rollback to %q", run.Status, StatusSubmitted)
	}
}

func TestFinalizeRecoveredRunCommitsFinishedRunAndJobTogether(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store, db := openSubmitRunStore(t, "live-finalize-finished")
	now := time.Unix(1_700_000_000, 0).UTC()
	seedClaimedSubmittingRun(ctx, t, store, now)
	if err := store.SubmitRunWithActiveTurn(ctx, SubmitRunWithActiveTurnParams{
		RunID: "run-submit", JobID: "job-submit", ClaimToken: "claim-token", ActiveTurnID: "turn-submit",
		SubmittedAt: now, Now: now,
	}); err != nil {
		t.Fatalf("SubmitRunWithActiveTurn() error = %v", err)
	}

	nextRunAt := now.Add(time.Hour)
	if err := store.FinalizeRecoveredRun(ctx, FinalizeRecoveredRunParams{
		ExpectedRunStatus: StatusSubmitted,
		MarkFailedParams: MarkFailedParams{
			ID: "job-submit", ClaimToken: "claim-token", RunID: "run-submit",
			ExpectedActiveTurnID: "turn-submit", LastRunAt: now, LastTurnID: "turn-submit",
			LastStatus: StatusFinished, NextRunAt: nextRunAt, Now: now.Add(time.Second),
		},
	}); err != nil {
		t.Fatalf("FinalizeRecoveredRun() error = %v", err)
	}

	var runStatus, jobStatus, claimToken, activeTurn string
	var storedNextRunAt int64
	err := db.QueryRowContext(ctx, `
		SELECT r.status, j.last_status, j.claim_token, j.active_turn_id, j.next_run_at
		FROM cron_job_runs AS r JOIN cron_jobs AS j ON j.id = r.job_id
		WHERE r.id = 'run-submit'
	`).Scan(&runStatus, &jobStatus, &claimToken, &activeTurn, &storedNextRunAt)
	if err != nil {
		t.Fatalf("read finalized state: %v", err)
	}
	if runStatus != StatusFinished || jobStatus != StatusFinished || claimToken != "" || activeTurn != "" || storedNextRunAt != nextRunAt.UnixMilli() {
		t.Fatalf("finalized state = run:%q job:%q claim:%q turn:%q next:%d", runStatus, jobStatus, claimToken, activeTurn, storedNextRunAt)
	}
}

func TestFinalizeRecoveredRunRejectsUnsupportedTerminalStatus(t *testing.T) {
	t.Parallel()
	_, err := validateFinalizeRecoveredRun(FinalizeRecoveredRunParams{
		ExpectedRunStatus: StatusSubmitted,
		MarkFailedParams: MarkFailedParams{
			ID: "job-submit", ClaimToken: "claim-token", RunID: "run-submit",
			ExpectedActiveTurnID: "turn-submit", LastTurnID: "turn-submit",
			LastStatus: "finished_typo", NextRunAt: time.Unix(1_700_000_000, 0).UTC(),
		},
	})
	if err == nil || !strings.Contains(err.Error(), "unsupported terminal status") {
		t.Fatalf("validateFinalizeRecoveredRun() error = %v, want unsupported terminal status", err)
	}
}

// openSubmitRunStore returns a migrated cron store built through the explicit DB constructor.
func openSubmitRunStore(t *testing.T, name string) (Store, *sql.DB) {
	t.Helper()
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "secure", name+".db")
	db := openMigratedCronDB(ctx, t, dbPath)
	return NewStoreWithDB(db, sqlc.New(db)), db
}

// seedClaimedSubmittingRun creates the exact precondition SubmitRunWithActiveTurn requires.
func seedClaimedSubmittingRun(ctx context.Context, t *testing.T, store Store, now time.Time) {
	t.Helper()
	if _, err := store.CreateJob(ctx, CreateJobParams{
		ID: "job-submit", Name: "submit", Prompt: "run", ScheduleExpr: "0 9 * * *",
		Timezone: "UTC", Provider: ProviderCodex, CWD: "/repo", Enabled: true,
		NextRunAt: now.Add(-time.Minute), CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("CreateJob() error = %v", err)
	}
	claimed, err := store.ClaimDueJobsForUpdate(ctx, ClaimDueJobsForUpdateParams{
		Now: now, ClaimedBy: "scheduler", LeaseExpiresAt: now.Add(time.Minute),
		ClaimToken: "claim-token", MaxClaim: 1,
	})
	if err != nil || len(claimed) != 1 {
		t.Fatalf("ClaimDueJobsForUpdate() = %v, %v; want one claim", claimed, err)
	}
	if _, err := store.InsertRun(ctx, InsertRunParams{
		ID: "run-submit", JobID: "job-submit", ScheduledAt: now,
		IdempotencyKey: "idem-submit", DedupeKey: "dedupe-submit",
		Status: StatusSubmitting, CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("InsertRun() error = %v", err)
	}
}

// assertSubmittedTurnState checks run and job state from the same committed SQLite view.
func assertSubmittedTurnState(ctx context.Context, t *testing.T, db *sql.DB, wantTurn, wantStatus string) {
	t.Helper()
	var runTurn, runStatus, threadID, agentID, activeTurn string
	err := db.QueryRowContext(ctx, `
		SELECT r.turn_id, r.status, r.thread_id, r.agent_id, j.active_turn_id
		FROM cron_job_runs AS r JOIN cron_jobs AS j ON j.id = r.job_id
		WHERE r.id = 'run-submit'
	`).Scan(&runTurn, &runStatus, &threadID, &agentID, &activeTurn)
	if err != nil {
		t.Fatalf("read submitted state: %v", err)
	}
	if runTurn != wantTurn || activeTurn != wantTurn || runStatus != wantStatus {
		t.Fatalf("state = run:%q active:%q status:%q, want turn %q status %q", runTurn, activeTurn, runStatus, wantTurn, wantStatus)
	}
	if wantTurn != "" && (threadID != "thread-submit" || agentID != "agent-submit") {
		t.Fatalf("thread/agent = %q/%q, want submitted identities", threadID, agentID)
	}
}
