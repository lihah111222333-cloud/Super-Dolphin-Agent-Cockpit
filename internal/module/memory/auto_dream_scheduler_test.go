package memory

import (
	"context"
	"testing"
	"time"

	pkglogger "github.com/lihah111222333-cloud/super-dolphin-agent/pkg/logger"
)

// -----------------------------------------------------------------------------
// Test 1: scheduler with disabled hooks is a no-op
// -----------------------------------------------------------------------------

func TestAutoDreamSchedulerDisabledHooksSkipsWorker(t *testing.T) {
	t.Parallel()
	hooks := newTestHooks(withEnabled(false))
	s := newAutoDreamScheduler(hooks, pkglogger.Get())
	s.Start()

	// Enqueue with disabled hooks must not block and must not crash; because
	// no worker is running, every enqueue should fall through. Depending on
	// channel scheduling the first autoDreamSchedulerQueueCap signals may
	// actually land in the queue buffer (no worker drains it), but that is
	// fine — the invariant we care about is: Stop returns immediately and
	// ProcessedTotal stays at 0.
	for i := 0; i < 3; i++ {
		s.Enqueue("thread-noop")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	if err := s.Stop(ctx); err != nil {
		t.Fatalf("Stop() with disabled hooks should be immediate, got %v", err)
	}
	if s.ProcessedTotal() != 0 {
		t.Errorf("ProcessedTotal = %d, want 0 for disabled hooks", s.ProcessedTotal())
	}
}

// -----------------------------------------------------------------------------
// Test 2: Enqueue is non-blocking; overflow is coalesced for retry
// -----------------------------------------------------------------------------

func TestAutoDreamSchedulerEnqueueOverflowCoalescesForRetry(t *testing.T) {
	t.Parallel()
	// Don't Start() — worker never consumes, so the queue fills predictably.
	// enabled=true keeps Enqueue on the production code path (otherwise it
	// would still enqueue, but ensures future enabled checks inside Enqueue
	// do not short-circuit without being counted).
	hooks := newTestHooks(withEnabled(true))
	s := newAutoDreamScheduler(hooks, pkglogger.Get())

	for i := 0; i < autoDreamSchedulerQueueCap; i++ {
		s.Enqueue("thread-fill")
	}
	if s.DroppedTotal() != 0 {
		t.Fatalf("DroppedTotal after cap fills = %d, want 0", s.DroppedTotal())
	}

	const overflow = 5
	for i := 0; i < overflow; i++ {
		s.Enqueue("thread-overflow")
	}
	if got := s.DroppedTotal(); got != 0 {
		t.Fatalf("DroppedTotal after overflow = %d, want 0 because overflow is coalesced", got)
	}

	// Stop on a never-started scheduler must still be safe (taskCancel +
	// close(stopCh); doneCh never gets closed so Stop will wait until
	// waitCtx expires).
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	_ = s.Stop(ctx) // returns ctx.Err(); we don't assert, just ensure no panic
}

// -----------------------------------------------------------------------------
// Test 3: Enqueue after Stop is dropped; real worker processes before Stop
// -----------------------------------------------------------------------------

func TestAutoDreamSchedulerStopGatesFurtherEnqueue(t *testing.T) {
	t.Parallel()
	// Use enabled=true + consolidator=nil so maybeScheduleAutoDream falls
	// through autoDreamThreadEligible quickly (h.consolidator == nil path).
	// That returns (false, nil) without any side effects, so process()
	// increments processedTotal and returns.
	hooks := newTestHooks(withEnabled(true))
	s := newAutoDreamScheduler(hooks, pkglogger.Get())
	s.Start()

	// Pump a few enqueues; wait for the worker to drain them.
	const pump = 3
	for i := 0; i < pump; i++ {
		s.Enqueue("thread-live")
	}
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if s.ProcessedTotal() >= pump {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if got := s.ProcessedTotal(); got < pump {
		t.Fatalf("ProcessedTotal = %d, want >= %d within timeout", got, pump)
	}

	// Stop drains the worker.
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := s.Stop(ctx); err != nil {
		t.Fatalf("Stop() err = %v, want nil", err)
	}

	// Post-Stop enqueue must be dropped (gate closed).
	beforeDropped := s.DroppedTotal()
	s.Enqueue("thread-post-stop")
	if got := s.DroppedTotal(); got != beforeDropped+1 {
		t.Errorf("Enqueue after Stop did not drop: delta = %d, want 1", got-beforeDropped)
	}
	// Worker must not process anything past the pre-Stop count.
	if got := s.ProcessedTotal(); got != int64(pump) {
		t.Errorf("ProcessedTotal after post-Stop enqueue = %d, want %d", got, pump)
	}
}

// -----------------------------------------------------------------------------
// Test 4: Enqueue with empty threadID is a no-op
// -----------------------------------------------------------------------------

func TestAutoDreamSchedulerIgnoresBlankThreadID(t *testing.T) {
	t.Parallel()
	hooks := newTestHooks(withEnabled(true))
	s := newAutoDreamScheduler(hooks, pkglogger.Get())
	s.Start()
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
		defer cancel()
		_ = s.Stop(ctx)
	}()

	s.Enqueue("")
	s.Enqueue("   ")

	// No processing, no drops for blank inputs — they short-circuit at the
	// top of Enqueue before the gate check.
	time.Sleep(20 * time.Millisecond)
	if got := s.ProcessedTotal(); got != 0 {
		t.Errorf("ProcessedTotal after blank enqueues = %d, want 0", got)
	}
	if got := s.DroppedTotal(); got != 0 {
		t.Errorf("DroppedTotal after blank enqueues = %d, want 0", got)
	}
}
