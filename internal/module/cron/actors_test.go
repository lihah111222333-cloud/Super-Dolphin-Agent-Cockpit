package cron

import (
	"context"
	"log/slog"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// tickActorRecorder counts how many ClaimDueJobsForUpdate fires so tests can
// verify the tick actor actually drives the scheduler.
type tickActorRecorder struct {
	recordingCronStore
	ticks int32
}

func startActorForTest(t *testing.T, run func(context.Context) error) (context.CancelFunc, <-chan error) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	finished := make(chan struct{})
	var wg sync.WaitGroup
	wg.Go(func() {
		defer close(finished)
		done <- run(ctx)
	})
	t.Cleanup(func() {
		cancel()
		select {
		case <-finished:
			wg.Wait()
		case <-time.After(time.Second):
			t.Fatal("actor goroutine did not stop")
		}
	})
	return cancel, done
}

func (r *tickActorRecorder) ClaimDueJobsForUpdate(ctx context.Context, p ClaimDueJobsForUpdateParams) ([]JobRecord, error) {
	atomic.AddInt32(&r.ticks, 1)
	if r.claimFn != nil {
		return r.claimFn(ctx, p)
	}
	return nil, nil
}

func TestTickActorRunsOnCtxCancel(t *testing.T) {
	t.Parallel()
	store := &tickActorRecorder{}
	s := NewScheduler(slog.Default(), store, &programmableSubmitter{}, SchedulerConfig{
		ClaimedBy:    "test",
		TickInterval: 10 * time.Millisecond,
		MaxClaim:     4,
	})
	actor := NewTickActor(slog.Default(), s)

	cancel, done := startActorForTest(t, actor.Run)

	// Give the actor long enough to tick at least twice before we cut
	// the context (immediate tick + one via ticker).
	time.Sleep(60 * time.Millisecond)
	cancel()

	select {
	case err := <-done:
		if err != context.Canceled {
			t.Fatalf("Run err = %v, want context.Canceled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("actor did not exit on cancel")
	}
	if atomic.LoadInt32(&store.ticks) < 2 {
		t.Fatalf("ticks = %d, want >= 2", store.ticks)
	}
}

func TestLeaseActorCallsRenewOnTick(t *testing.T) {
	t.Parallel()
	renewed := int32(0)
	store := &recordingCronStore{
		listJobsFn: func(context.Context) ([]JobRecord, error) {
			return []JobRecord{{ID: "mine", ClaimedBy: "test", ClaimToken: "tok"}}, nil
		},
		renewLeaseFn: func(context.Context, LeaseParams) error {
			atomic.AddInt32(&renewed, 1)
			return nil
		},
	}
	s := NewScheduler(slog.Default(), store, &programmableSubmitter{}, SchedulerConfig{
		ClaimedBy:      "test",
		LeaseHeartbeat: 10 * time.Millisecond,
	})
	actor := NewLeaseActor(slog.Default(), s)

	cancel, done := startActorForTest(t, actor.Run)

	// Wait for a couple of heartbeats.
	time.Sleep(40 * time.Millisecond)
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("lease actor did not exit on cancel")
	}
	if atomic.LoadInt32(&renewed) < 1 {
		t.Fatalf("renewed = %d, want >= 1", renewed)
	}
}
