package cron

import (
	"context"
	"log/slog"
	"sync/atomic"
	"testing"
	"time"

	cronstore "github.com/anthropic-ai/super-agent-v3/internal/store/cron"
)

// tickActorRecorder counts how many ClaimDueJobs fires so tests can
// verify the tick actor actually drives the scheduler.
type tickActorRecorder struct {
	recordingCronStore
	ticks int32
}

func (r *tickActorRecorder) ClaimDueJobs(ctx context.Context, p cronstore.ClaimDueJobsParams) ([]cronstore.Job, error) {
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

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- actor.Run(ctx) }()

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
		listJobsFn: func(context.Context) ([]cronstore.Job, error) {
			return []cronstore.Job{{ID: "mine", ClaimedBy: "test", ClaimToken: "tok"}}, nil
		},
		renewLeaseFn: func(context.Context, cronstore.LeaseParams) error {
			atomic.AddInt32(&renewed, 1)
			return nil
		},
	}
	s := NewScheduler(slog.Default(), store, &programmableSubmitter{}, SchedulerConfig{
		ClaimedBy:      "test",
		LeaseHeartbeat: 10 * time.Millisecond,
	})
	actor := NewLeaseActor(slog.Default(), s)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- actor.Run(ctx) }()

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
