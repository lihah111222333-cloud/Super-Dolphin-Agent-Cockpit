package cron

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/pkg/cronmetrics"
)

type recordingCronRecoveryStore struct {
	finalizeRecoveredRunFn func(context.Context, FinalizeRecoveredRunParams) error
	getRunByIDFn           func(context.Context, string) (RunRecord, error)
}

func (s *recordingCronRecoveryStore) FinalizeRecoveredRun(ctx context.Context, p FinalizeRecoveredRunParams) error {
	if s.finalizeRecoveredRunFn != nil {
		return s.finalizeRecoveredRunFn(ctx, p)
	}
	return nil
}

func (s *recordingCronRecoveryStore) GetRunByID(ctx context.Context, id string) (RunRecord, error) {
	if s.getRunByIDFn != nil {
		return s.getRunByIDFn(ctx, id)
	}
	return RunRecord{}, ErrStoreJobRunNotFound
}

func TestFinalizeRecoveredFailureCASFailureDoesNotMarkJobFailed(t *testing.T) {
	store := &recordingCronStore{}
	s := newTestScheduler(t, store, &programmableSubmitter{})
	s.metrics.ResetForTesting()
	t.Cleanup(s.metrics.ResetForTesting)
	job := JobRecord{ID: "job-1", ScheduleExpr: "0 9 * * *", Timezone: "UTC", ClaimToken: "tok", ActiveTurnID: "turn-1", MaxAttempts: 1}
	run := RunRecord{ID: "run-1", JobID: job.ID, TurnID: "turn-1", Status: statusSubmitted, ScheduledAt: s.now()}
	finalizeCalls := 0
	store.finalizeRecoveredRunFn = func(_ context.Context, p FinalizeRecoveredRunParams) error {
		finalizeCalls++
		if p.ExpectedRunStatus != statusSubmitted || p.LastStatus != statusFailed {
			t.Fatalf("FinalizeRecoveredRun params = %+v", p)
		}
		return errors.New("CAS lost")
	}

	err := s.finalizeRecoveredFailure(context.Background(), job, run, errors.New("provider dedupe lookup missed"))
	if err == nil || !strings.Contains(err.Error(), "CAS lost") {
		t.Fatalf("finalizeRecoveredFailure() error = %v, want CAS failure", err)
	}
	if finalizeCalls != 1 {
		t.Fatalf("FinalizeRecoveredRun calls = %d, want 1", finalizeCalls)
	}
	assertCronRecoveryMetrics(t, s.metrics, 0, 1)
}

func TestFinalizeRecoveredObserveLostCASFailureDoesNotMarkJobFailed(t *testing.T) {
	store := &recordingCronStore{}
	s := newTestScheduler(t, store, &programmableSubmitter{})
	s.metrics.ResetForTesting()
	t.Cleanup(s.metrics.ResetForTesting)
	job := JobRecord{ID: "job-1", ScheduleExpr: "0 9 * * *", Timezone: "UTC", ClaimToken: "tok", ActiveTurnID: "turn-1", MaxAttempts: 1}
	run := RunRecord{ID: "run-1", JobID: job.ID, TurnID: "turn-1", Status: statusRunning, ScheduledAt: s.now()}
	finalizeCalls := 0
	store.finalizeRecoveredRunFn = func(_ context.Context, p FinalizeRecoveredRunParams) error {
		finalizeCalls++
		if p.ExpectedRunStatus != statusRunning || p.LastStatus != statusObserveLost || !p.NextRetryAt.IsZero() {
			t.Fatalf("FinalizeRecoveredRun params = %+v", p)
		}
		return errors.New("CAS lost")
	}

	err := s.finalizeRecoveredObserveLost(context.Background(), job, run, run.TurnID, errors.New("observe lost"))
	if err == nil || !strings.Contains(err.Error(), "CAS lost") {
		t.Fatalf("finalizeRecoveredObserveLost() error = %v, want CAS failure", err)
	}
	if finalizeCalls != 1 {
		t.Fatalf("FinalizeRecoveredRun calls = %d, want 1", finalizeCalls)
	}
	assertCronRecoveryMetrics(t, s.metrics, 0, 1)
}

func TestFinalizeRecoveredRunTreatsMatchingTerminalStateAsIdempotent(t *testing.T) {
	store := &recordingCronStore{}
	s := newTestScheduler(t, store, &programmableSubmitter{})
	s.metrics.ResetForTesting()
	t.Cleanup(s.metrics.ResetForTesting)
	job := JobRecord{ID: "job-1", ClaimToken: "tok", ActiveTurnID: "turn-1"}
	run := RunRecord{ID: "run-1", JobID: job.ID, TurnID: "turn-1", Status: statusSubmitted}
	store.getRunByIDFn = func(context.Context, string) (RunRecord, error) {
		return RunRecord{ID: run.ID, JobID: job.ID, TurnID: run.TurnID, Status: statusFailed}, nil
	}
	store.getJobFn = func(context.Context, string) (JobRecord, error) {
		return JobRecord{ID: job.ID, LastStatus: statusFailed, LastTurnID: run.TurnID}, nil
	}
	if err := s.finalizeRecoveredRun(context.Background(), job, run, statusFailed, ErrStoreStatusTransitionRefused); err != nil {
		t.Fatalf("finalizeRecoveredRun() error = %v, want idempotent success", err)
	}
	assertCronRecoveryMetrics(t, s.metrics, 1, 0)
}

func TestFinalizeRecoveredRunRejectsConflictingTerminalState(t *testing.T) {
	store := &recordingCronStore{}
	s := newTestScheduler(t, store, &programmableSubmitter{})
	s.metrics.ResetForTesting()
	t.Cleanup(s.metrics.ResetForTesting)
	job := JobRecord{ID: "job-1", ClaimToken: "tok", ActiveTurnID: "turn-1"}
	run := RunRecord{ID: "run-1", JobID: job.ID, TurnID: "turn-1", Status: statusSubmitted}
	store.getRunByIDFn = func(context.Context, string) (RunRecord, error) {
		return RunRecord{ID: run.ID, JobID: job.ID, TurnID: run.TurnID, Status: statusObserveLost}, nil
	}
	store.getJobFn = func(context.Context, string) (JobRecord, error) {
		return JobRecord{ID: job.ID, LastStatus: statusObserveLost, LastTurnID: run.TurnID}, nil
	}
	err := s.finalizeRecoveredRun(context.Background(), job, run, statusFailed, ErrStoreStatusTransitionRefused)
	if !errors.Is(err, ErrStoreStatusTransitionRefused) {
		t.Fatalf("finalizeRecoveredRun() error = %v, want classified conflict", err)
	}
	assertCronRecoveryMetrics(t, s.metrics, 1, 1)
}

// TestFinalizeRecoveredObserveLostOldTurnConflictDoesNotReleaseNewClaimOrRetry 模拟并发调度器替换旧 turn 的 claim。
func TestFinalizeRecoveredObserveLostOldTurnConflictDoesNotReleaseNewClaimOrRetry(t *testing.T) {
	store := &recordingCronStore{}
	s := newTestScheduler(t, store, &programmableSubmitter{})
	s.metrics.ResetForTesting()
	t.Cleanup(s.metrics.ResetForTesting)
	oldJob := JobRecord{ID: "job-1", ScheduleExpr: "0 9 * * *", Timezone: "UTC", ClaimToken: "old-claim", ActiveTurnID: "turn-old", MaxAttempts: 3}
	oldRun := RunRecord{ID: "run-old", JobID: oldJob.ID, TurnID: "turn-old", Status: statusRunning, ScheduledAt: s.now()}
	newJob := JobRecord{ID: oldJob.ID, ClaimToken: "new-claim", ActiveTurnID: "turn-new", NextRetryAt: s.now().Add(time.Hour)}
	finalizeCalls := 0
	store.finalizeRecoveredRunFn = func(_ context.Context, p FinalizeRecoveredRunParams) error {
		finalizeCalls++
		if p.ClaimToken != oldJob.ClaimToken || p.ExpectedActiveTurnID != oldRun.TurnID || p.LastStatus != statusObserveLost || !p.NextRetryAt.IsZero() {
			t.Fatalf("stale recovery finalizer params = %+v", p)
		}
		return ErrStoreClaimTokenMismatch
	}
	store.getRunByIDFn = func(context.Context, string) (RunRecord, error) {
		return RunRecord{ID: "run-new", JobID: oldJob.ID, TurnID: newJob.ActiveTurnID, Status: statusRunning}, nil
	}
	store.getJobFn = func(context.Context, string) (JobRecord, error) { return newJob, nil }

	err := s.finalizeRecoveredObserveLost(context.Background(), oldJob, oldRun, oldRun.TurnID, errors.New("old turn cannot be observed"))
	if !errors.Is(err, ErrStoreClaimTokenMismatch) {
		t.Fatalf("finalizeRecoveredObserveLost() error = %v, want stale claim conflict", err)
	}
	if finalizeCalls != 1 {
		t.Fatalf("FinalizeRecoveredRun calls = %d, want exactly one fenced finalization", finalizeCalls)
	}
	if newJob.ClaimToken != "new-claim" || newJob.ActiveTurnID != "turn-new" || newJob.NextRetryAt.IsZero() {
		t.Fatalf("new claim was released or its retry schedule changed: %+v", newJob)
	}
	assertCronRecoveryMetrics(t, s.metrics, 1, 1)
}

func assertCronRecoveryMetrics(t *testing.T, metrics *cronmetrics.Metrics, wantConflict, wantError uint64) {
	t.Helper()
	got := metrics.Read()
	if got.RecoveryFinalizeConflictTotal != wantConflict || got.RecoveryFinalizeErrorTotal != wantError {
		t.Fatalf("cron recovery metrics = %+v, want conflict=%d error=%d", got, wantConflict, wantError)
	}
}
