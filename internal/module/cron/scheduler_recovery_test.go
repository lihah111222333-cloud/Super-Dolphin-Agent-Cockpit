package cron

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestRecoverSubmittingRunDedupeHitPersistsSubmittedTurnAtomically(t *testing.T) {
	t.Parallel()
	t.Run("atomically binds run job and submitted status before observe", testRecoveryDedupeAtomicBind)
	t.Run("atomic fence or write failure leaves no run-only turn", testRecoveryDedupeFenceFailure)
}

func testRecoveryDedupeAtomicBind(t *testing.T) {
	const observedTurnID = "turn-recovered"
	var job *JobRecord
	var run *RunRecord
	s, _, jobRef, runRef := newRecoveryDedupeFixture(t, func(_ context.Context, params SubmitRunWithActiveTurnParams) error {
		assertRecoverySubmitParams(t, params, job, run, observedTurnID)
		run.TurnID = params.ActiveTurnID
		run.Status = statusSubmitted
		job.ActiveTurnID = params.ActiveTurnID
		return nil
	})
	job, run = jobRef, runRef

	if err := s.recoverSubmittingRun(context.Background(), *job, *run); err != nil {
		t.Fatalf("recoverSubmittingRun() error = %v", err)
	}
	if run.TurnID != observedTurnID || job.ActiveTurnID != observedTurnID || run.Status != statusRunning {
		t.Fatalf("recovered durable state run=%+v job=%+v", run, job)
	}
}

func assertRecoverySubmitParams(t *testing.T, params SubmitRunWithActiveTurnParams, job *JobRecord, run *RunRecord, observedTurnID string) {
	t.Helper()
	if params.RunID != run.ID || params.JobID != job.ID || params.ClaimToken != job.ClaimToken ||
		params.ActiveTurnID != observedTurnID || params.ThreadID != run.ThreadID || params.AgentID != run.AgentID {
		t.Fatalf("SubmitRunWithActiveTurn params = %+v", params)
	}
}

func testRecoveryDedupeFenceFailure(t *testing.T) {
	wantErr := errors.New("submitted turn fence refused")
	s, store, job, run := newRecoveryDedupeFixture(t, func(context.Context, SubmitRunWithActiveTurnParams) error {
		return wantErr
	})

	if err := s.recoverSubmittingRun(context.Background(), *job, *run); !errors.Is(err, wantErr) {
		t.Fatalf("recoverSubmittingRun() error = %v, want %v", err, wantErr)
	}
	if run.TurnID != "" || job.ActiveTurnID != "" || run.Status != statusSubmitting {
		t.Fatalf("fence failure left partial durable state run=%+v job=%+v", run, job)
	}
	if len(store.casCalls) != 0 {
		t.Fatalf("fence failure advanced run status after atomic write refusal: %+v", store.casCalls)
	}
}

func newRecoveryDedupeFixture(t *testing.T, submitFn func(context.Context, SubmitRunWithActiveTurnParams) error) (*Scheduler, *recordingCronStore, *JobRecord, *RunRecord) {
	t.Helper()
	job := &JobRecord{ID: "job-1", ScheduleExpr: "0 9 * * *", Timezone: "UTC", ClaimToken: "claim-token"}
	run := &RunRecord{ID: "run-1", JobID: job.ID, ThreadID: "thread-1", AgentID: "agent-1", DedupeKey: "dedupe-1", Status: statusSubmitting, ScheduledAt: time.Unix(1_700_000_000, 0).UTC()}
	store := &recordingCronStore{}
	store.submitRunWithActiveTurn = submitFn
	store.setRunTurnFn = func(context.Context, SetRunTurnParams) error {
		t.Fatal("recovery dedupe hit must not persist a run-only turn")
		return nil
	}
	store.casStatusFn = func(_ context.Context, params CASRunStatusParams) error {
		if params.ID != run.ID || params.ExpectedStatus != statusSubmitted || params.NextStatus != statusRunning {
			t.Fatalf("recovery CAS = %+v, want submitted -> running for %s", params, run.ID)
		}
		run.Status = statusRunning
		return nil
	}
	submitter := &programmableSubmitter{lookupFn: func(_ context.Context, dedupeKey string) (ObservedTurn, error) {
		if dedupeKey != run.DedupeKey {
			t.Fatalf("LookupByDedupeKey(%q), want %q", dedupeKey, run.DedupeKey)
		}
		return ObservedTurn{Found: true, TurnID: "turn-recovered"}, nil
	}}
	return newTestScheduler(t, store, submitter), store, job, run
}
