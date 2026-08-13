package cron

import (
	"context"
	"errors"
	"log/slog"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kelindar/event"

	turndto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/turn"
	platformbus "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/bus"
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
	store.isTurnOwnedFn = func(context.Context, string) (bool, error) { return true, nil }
	scheduler := newTestScheduler(t, store, &programmableSubmitter{})
	job := JobRecord{
		ID:           "job-1",
		ScheduleExpr: "0 9 * * *",
		Timezone:     "UTC",
		ClaimToken:   "tok",
		ActiveTurnID: "turn-1",
		NextRunAt:    scheduler.now(),
	}
	run := RunRecord{ID: "run-1", JobID: job.ID, TurnID: "turn-1", Status: statusRunning, ScheduledAt: scheduler.now()}
	var lookupCalls atomic.Int32
	store.getRunningRunByTurnIDFn = func(ctx context.Context, turnID string) (RunRecord, error) {
		if lookupCalls.Add(1) == 1 {
			return RunRecord{}, context.DeadlineExceeded
		}
		return run, nil
	}
	store.getJobFn = func(context.Context, string) (JobRecord, error) { return job, nil }
	done := make(chan struct{})
	store.finalizeRecoveredRunFn = func(context.Context, FinalizeRecoveredRunParams) error {
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

// TestCronTerminalOwnershipRetryCompletesOnce 固定一次性终态在判域超时后仍会重试，且只收尾一次。
func TestCronTerminalOwnershipRetryCompletesOnce(t *testing.T) {
	store := &recordingCronStore{}
	var ownershipCalls atomic.Int32
	store.isTurnOwnedFn = func(context.Context, string) (bool, error) {
		if ownershipCalls.Add(1) == 1 {
			return false, context.DeadlineExceeded
		}
		return true, nil
	}
	scheduler := newTestScheduler(t, store, &programmableSubmitter{})
	job := JobRecord{
		ID: "job-owned", ScheduleExpr: "0 9 * * *", Timezone: "UTC",
		ClaimToken: "claim-owned", ActiveTurnID: "turn-owned", NextRunAt: scheduler.now(),
	}
	run := RunRecord{ID: "run-owned", JobID: job.ID, TurnID: "turn-owned", Status: statusRunning, ScheduledAt: scheduler.now()}
	store.getRunningRunByTurnIDFn = func(context.Context, string) (RunRecord, error) { return run, nil }
	store.getJobFn = func(context.Context, string) (JobRecord, error) { return job, nil }
	done := make(chan struct{}, 1)
	var completeCalls atomic.Int32
	store.finalizeRecoveredRunFn = func(context.Context, FinalizeRecoveredRunParams) error {
		completeCalls.Add(1)
		done <- struct{}{}
		return nil
	}
	worker := newCronProgressWorker(scheduler, slog.New(slog.DiscardHandler))
	worker.Start()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = worker.Stop(ctx)
	})

	worker.enqueue(cronProgressRequest{kind: cronCompleteTurn, turnID: "turn-owned", success: true})
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatalf("terminal event was not completed after ownership retry")
	}
	deadline := time.Now().Add(time.Second)
	for worker.processedTotal.Load() < 2 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if got := ownershipCalls.Load(); got != 2 {
		t.Fatalf("ownership calls = %d, want 2", got)
	}
	if got := completeCalls.Load(); got != 1 {
		t.Fatalf("complete calls = %d, want 1", got)
	}
}

// TestCronTerminalOwnershipRetryIsBounded 固定持续超时最多执行约定次数，且不会误入 CompleteTurn。
func TestCronTerminalOwnershipRetryIsBounded(t *testing.T) {
	store := &recordingCronStore{}
	var ownershipCalls atomic.Int32
	store.isTurnOwnedFn = func(context.Context, string) (bool, error) {
		ownershipCalls.Add(1)
		return false, context.DeadlineExceeded
	}
	var completeCalls atomic.Int32
	store.getRunningRunByTurnIDFn = func(context.Context, string) (RunRecord, error) {
		completeCalls.Add(1)
		return RunRecord{}, ErrStoreJobRunNotFound
	}
	worker := newCronProgressWorker(
		newTestScheduler(t, store, &programmableSubmitter{}),
		slog.New(slog.DiscardHandler),
	)
	worker.Start()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = worker.Stop(ctx)
	})
	worker.enqueue(cronProgressRequest{kind: cronCompleteTurn, turnID: "turn-timeout", success: true})

	deadline := time.Now().Add(2 * time.Second)
	for worker.processedTotal.Load() < cronTerminalOwnershipMaxAttempts && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if got := ownershipCalls.Load(); got != cronTerminalOwnershipMaxAttempts {
		t.Fatalf("ownership calls = %d, want %d", got, cronTerminalOwnershipMaxAttempts)
	}
	if got := completeCalls.Load(); got != 0 {
		t.Fatalf("CompleteTurn calls = %d, want 0", got)
	}
}

// TestCronTerminalOwnershipLookupCancelsOnWorkerStop 固定持续阻塞的判域查询可由 worker Stop 取消。
func TestCronTerminalOwnershipLookupCancelsOnWorkerStop(t *testing.T) {
	store := &recordingCronStore{}
	started := make(chan struct{})
	canceled := make(chan error, 1)
	store.isTurnOwnedFn = func(ctx context.Context, _ string) (bool, error) {
		close(started)
		<-ctx.Done()
		canceled <- ctx.Err()
		return false, ctx.Err()
	}
	worker := newCronProgressWorker(
		newTestScheduler(t, store, &programmableSubmitter{}),
		slog.New(slog.DiscardHandler),
	)
	worker.Start()
	worker.enqueue(cronProgressRequest{kind: cronCompleteTurn, turnID: "turn-blocked", success: true})
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatalf("ownership lookup did not start in worker")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := worker.Stop(ctx); err != nil {
		t.Fatalf("Stop() error = %v", err)
	}
	select {
	case err := <-canceled:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("ownership context error = %v, want context.Canceled", err)
		}
	case <-time.After(time.Second):
		t.Fatalf("ownership lookup was not canceled by worker stop")
	}
}

func TestCronProgressRetryableClassification(t *testing.T) {
	if !isCronProgressRetryable(context.DeadlineExceeded) {
		t.Fatalf("DeadlineExceeded must be retryable")
	}
	if isCronProgressRetryable(context.Canceled) {
		t.Fatalf("Canceled must not be retryable")
	}
	if isCronProgressRetryable(ErrStoreClaimTokenMismatch) {
		t.Fatalf("stale claim mismatch must not be retried")
	}
	if isCronProgressRetryable(errors.New("validation failed")) {
		t.Fatalf("generic validation error must not be retried")
	}
}

// TestCronTerminalSubscriberSkipsOrdinaryTurns 固定普通 Completed/Interrupted 可进入 worker 判域，
// 但 owned=false 时都不得调用 CompleteTurn。
func TestCronTerminalSubscriberSkipsOrdinaryTurns(t *testing.T) {
	cases := []struct {
		name    string
		turnID  string
		publish func(*event.Dispatcher, string)
	}{
		{
			name:   "ordinary completed turn",
			turnID: "ordinary-completed",
			publish: func(dispatcher *event.Dispatcher, turnID string) {
				event.Publish(dispatcher, turndto.TurnCompleted{
					TurnHeader: cronProgressTurnHeader("thread-ordinary", turnID, "agent-ordinary"),
					Success:    true,
				})
			},
		},
		{
			name:   "ordinary interrupted turn",
			turnID: "ordinary-interrupted",
			publish: func(dispatcher *event.Dispatcher, turnID string) {
				event.Publish(dispatcher, turndto.TurnInterrupted{
					TurnHeader: cronProgressTurnHeader("thread-ordinary", turnID, "agent-ordinary"),
					Reason:     "user interrupted",
				})
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store := &recordingCronStore{}
			var ownershipCalls atomic.Int32
			store.isTurnOwnedFn = func(context.Context, string) (bool, error) {
				ownershipCalls.Add(1)
				return false, nil
			}
			var completeCalls atomic.Int32
			store.getRunningRunByTurnIDFn = func(context.Context, string) (RunRecord, error) {
				completeCalls.Add(1)
				return RunRecord{}, ErrStoreJobRunNotFound
			}
			worker := newCronProgressWorker(
				newTestScheduler(t, store, &programmableSubmitter{}),
				slog.New(slog.DiscardHandler),
			)
			worker.Start()
			t.Cleanup(func() {
				ctx, cancel := context.WithTimeout(context.Background(), time.Second)
				defer cancel()
				_ = worker.Stop(ctx)
			})
			dispatcher := platformbus.NewDispatcher()
			t.Cleanup(func() { _ = dispatcher.Close() })
			cancel := subscribeCronTerminalEvents(dispatcher, worker, nil)
			t.Cleanup(cancel)

			tc.publish(dispatcher, tc.turnID)

			deadline := time.Now().Add(2 * time.Second)
			for worker.processedTotal.Load() == 0 && time.Now().Before(deadline) {
				time.Sleep(time.Millisecond)
			}
			if got := ownershipCalls.Load(); got != 1 {
				t.Fatalf("ownership lookup calls = %d, want 1", got)
			}
			if got := completeCalls.Load(); got != 0 {
				t.Fatalf("CompleteTurn calls = %d, want 0", got)
			}
		})
	}
}
