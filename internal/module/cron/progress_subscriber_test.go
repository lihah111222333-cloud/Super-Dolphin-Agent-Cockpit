package cron

import (
	"context"
	"errors"
	"log/slog"
	"sync/atomic"
	"testing"
	"time"
)

func TestCronProgressWorkerQueueIsBoundedAndCoalescesProgress(t *testing.T) {
	worker := newCronProgressWorker(nil, slog.New(slog.DiscardHandler))
	worker.queueLimit = 2

	worker.enqueue(cronProgressRequest{kind: cronExtendClaim, turnID: "turn-1"})
	worker.enqueue(cronProgressRequest{kind: cronExtendClaim, turnID: "turn-1"})
	worker.enqueue(cronProgressRequest{kind: cronExtendClaim, turnID: "turn-2"})
	worker.enqueue(cronProgressRequest{kind: cronExtendClaim, turnID: "turn-3"})
	worker.enqueue(cronProgressRequest{kind: cronCompleteTurn, turnID: "turn-terminal", success: true})

	snapshot := worker.HealthSnapshot()
	if snapshot.Backlog > 2 {
		t.Fatalf("Backlog = %d, want bounded <= 2", snapshot.Backlog)
	}
	if snapshot.Coalesced == 0 {
		t.Fatalf("Coalesced = 0, want duplicate progress coalesced")
	}
	if snapshot.Dropped == 0 {
		t.Fatalf("Dropped = 0, want progress dropped under pressure")
	}
	if !snapshot.HasTerminal {
		t.Fatalf("HasTerminal = false, want terminal event preserved: %+v", snapshot)
	}
}

func TestCronProgressWorkerPreservesTerminalWhenQueueIsAllTerminal(t *testing.T) {
	worker := newCronProgressWorker(nil, slog.New(slog.DiscardHandler))
	worker.queueLimit = 2

	worker.enqueue(cronProgressRequest{kind: cronCompleteTurn, turnID: "turn-1", success: true})
	worker.enqueue(cronProgressRequest{kind: cronCompleteTurn, turnID: "turn-2", success: true})
	worker.enqueue(cronProgressRequest{kind: cronCompleteTurn, turnID: "turn-3", success: false})

	snapshot := worker.HealthSnapshot()
	if snapshot.Backlog != 3 {
		t.Fatalf("Backlog = %d, want 3 terminal events preserved beyond soft limit", snapshot.Backlog)
	}
	if snapshot.Dropped != 0 {
		t.Fatalf("Dropped = %d, want 0 for terminal-only pressure", snapshot.Dropped)
	}
}

func TestCronProgressWorkerRetriesTransientTerminalError(t *testing.T) {
	store := &recordingCronStore{}
	scheduler := newTestScheduler(t, store, &programmableSubmitter{})
	job := jobRecord{
		ID:           "job-1",
		ScheduleExpr: "0 9 * * *",
		Timezone:     "UTC",
		ClaimToken:   "tok",
		ActiveTurnID: "turn-1",
		NextRunAt:    scheduler.now(),
	}
	run := runRecord{ID: "run-1", JobID: job.ID, TurnID: "turn-1", Status: statusRunning, ScheduledAt: scheduler.now()}
	var lookupCalls atomic.Int32
	store.getRunningRunByTurnIDFn = func(ctx context.Context, turnID string) (runRecord, error) {
		if lookupCalls.Add(1) == 1 {
			return runRecord{}, context.DeadlineExceeded
		}
		return run, nil
	}
	store.getJobFn = func(context.Context, string) (jobRecord, error) { return job, nil }
	done := make(chan struct{})
	store.markFinishedFn = func(context.Context, markFinishedParams) error {
		close(done)
		return nil
	}
	worker := newCronProgressWorker(scheduler, slog.New(slog.DiscardHandler))
	worker.Start()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = worker.Stop(ctx)
	})

	worker.enqueue(cronProgressRequest{kind: cronCompleteTurn, turnID: "turn-1", success: true})
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatalf("terminal event was not retried after transient error")
	}
	if got := lookupCalls.Load(); got < 2 {
		t.Fatalf("lookupCalls = %d, want retry", got)
	}
	if snapshot := worker.HealthSnapshot(); snapshot.LastError == "" {
		t.Fatalf("LastError empty, want transient retry error recorded")
	}
}

func TestCronProgressRetryableClassification(t *testing.T) {
	if !isCronProgressRetryable(context.DeadlineExceeded) {
		t.Fatalf("DeadlineExceeded must be retryable")
	}
	if isCronProgressRetryable(context.Canceled) {
		t.Fatalf("Canceled must not be retryable")
	}
	if isCronProgressRetryable(errStoreClaimTokenMismatch) {
		t.Fatalf("stale claim mismatch must not be retried")
	}
	if isCronProgressRetryable(errors.New("validation failed")) {
		t.Fatalf("generic validation error must not be retried")
	}
}
