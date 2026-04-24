package memory

import (
	"context"
	"sync"
	"testing"
	"time"

	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
)

// fakeNestedIngestRuntime records AddToolReadResult calls so tests can assert
// that the bus callback only enqueued and that the worker is what actually
// invokes AddToolReadResult.
type fakeNestedIngestRuntime struct {
	mu    sync.Mutex
	calls []nestedIngestRequest
	block chan struct{} // if non-nil, every AddToolReadResult blocks on it
}

func (f *fakeNestedIngestRuntime) AddToolReadResult(threadID, toolName, result, persistedPath string) {
	if f.block != nil {
		<-f.block
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, nestedIngestRequest{
		threadID:      threadID,
		toolName:      toolName,
		result:        result,
		persistedPath: persistedPath,
	})
}

func (f *fakeNestedIngestRuntime) Calls() []nestedIngestRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]nestedIngestRequest, len(f.calls))
	copy(out, f.calls)
	return out
}

// TestNestedToolReadIngestEnqueueOnly is the P22 P2 Finding 10 TDD test
// named in docs/plans/迁移/p22/P2_BusRuntimeDecoupling.md:415.
//
// It covers the three invariants that together make the callback path
// safe:
//
//  1. Enqueue is non-blocking even while AddToolReadResult is stuck on
//     slow disk I/O (the whole reason we moved to a worker);
//  2. the worker, not the caller, is what eventually drives
//     AddToolReadResult;
//  3. repeated Enqueue for the same (thread, tool, persistedPath) key
//     coalesces into a single latest-payload request.
func TestNestedToolReadIngestEnqueueOnly(t *testing.T) {
	t.Parallel()

	// --- Invariant 1: Enqueue does not block on AddToolReadResult ---
	block := make(chan struct{})
	rt := &fakeNestedIngestRuntime{block: block}
	w := newNestedIngestWorker(rt, pkglogger.Get())
	w.Start()

	enqueueDone := make(chan struct{})
	go func() {
		for i := 0; i < 16; i++ {
			w.Enqueue("thread-A", "Read", "payload", "/tmp/file")
		}
		close(enqueueDone)
	}()
	select {
	case <-enqueueDone:
	case <-time.After(time.Second):
		t.Fatalf("Enqueue blocked while AddToolReadResult was stuck; callback path must be non-blocking")
	}

	// While still blocked, no calls may have landed on the runtime.
	if got := len(rt.Calls()); got != 0 {
		t.Fatalf("AddToolReadResult invoked %d times while worker was blocked; bus callback must not drive it", got)
	}

	// --- Invariant 3: repeated same-key enqueues coalesce ---
	if enq := w.EnqueuedTotal(); enq != 16 {
		t.Fatalf("EnqueuedTotal = %d, want 16", enq)
	}
	if coal := w.CoalescedTotal(); coal != 15 {
		t.Fatalf("CoalescedTotal = %d, want 15 (16 events - 1 distinct key)", coal)
	}

	// --- Invariant 2: worker drives AddToolReadResult once unblocked ---
	close(block)
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if w.ProcessedTotal() >= 1 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if got := w.ProcessedTotal(); got < 1 {
		t.Fatalf("ProcessedTotal = %d, want >= 1 after unblocking runtime", got)
	}
	if got := len(rt.Calls()); got != 1 {
		t.Fatalf("AddToolReadResult call count after drain = %d, want 1 (coalesced)", got)
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := w.Stop(ctx); err != nil {
		t.Fatalf("Stop() = %v, want nil", err)
	}
}

// TestNestedIngestWorkerEnqueueAfterStopDrops verifies the gate semantics:
// once Stop fires, further Enqueue calls are silently dropped rather than
// buffered (a buffered post-Stop enqueue would race with cancelled bus
// subscriptions).
func TestNestedIngestWorkerEnqueueAfterStopDrops(t *testing.T) {
	t.Parallel()

	rt := &fakeNestedIngestRuntime{}
	w := newNestedIngestWorker(rt, pkglogger.Get())
	w.Start()

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := w.Stop(ctx); err != nil {
		t.Fatalf("Stop() = %v, want nil", err)
	}

	beforeEnq := w.EnqueuedTotal()
	w.Enqueue("thread-post-stop", "Read", "payload", "/tmp/file")
	if got := w.EnqueuedTotal(); got != beforeEnq {
		t.Errorf("EnqueuedTotal after post-Stop enqueue = %d, want %d", got, beforeEnq)
	}
	if got := len(rt.Calls()); got != 0 {
		t.Errorf("AddToolReadResult invoked after Stop: %d calls, want 0", got)
	}
}

// TestNestedIngestWorkerStopDrainsPending verifies the lossless contract:
// a request enqueued before Stop must be delivered to AddToolReadResult
// before Stop returns (bounded by ctx). This is the crux of the "lossless
// pending-set" design — unlike the auto-dream scheduler, which drops on
// overflow, the nested-ingest worker's contract is that in-flight requests
// get drained at shutdown.
func TestNestedIngestWorkerStopDrainsPending(t *testing.T) {
	t.Parallel()

	rt := &fakeNestedIngestRuntime{}
	w := newNestedIngestWorker(rt, pkglogger.Get())
	// Intentionally do not Start() the worker goroutine; we want to prove
	// that Stop itself also drains pending requests via the stopCh branch
	// of runWorker — but since the worker is not started, we instead test
	// a Start-then-instant-Stop race where nothing is processed by wake.
	w.Start()

	// Pump without giving the worker time to drain via wake.
	for i := 0; i < 3; i++ {
		w.Enqueue("thread-drain", "Read", "payload", "/tmp/file") // same key -> 1 pending
	}
	w.Enqueue("thread-drain", "Read", "payload-other", "/tmp/other") // distinct key -> +1

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := w.Stop(ctx); err != nil {
		t.Fatalf("Stop() = %v, want nil", err)
	}

	if got := len(rt.Calls()); got != 2 {
		t.Fatalf("AddToolReadResult call count after Stop drain = %d, want 2 distinct keys", got)
	}
	if got := w.ProcessedTotal(); got != 2 {
		t.Fatalf("ProcessedTotal after drain = %d, want 2", got)
	}
}

// TestNestedIngestWorkerBlankThreadIDIsNoop mirrors the scheduler's blank-
// input short-circuit: empty threadIDs are silently ignored, not counted
// as enqueued or dropped.
func TestNestedIngestWorkerBlankThreadIDIsNoop(t *testing.T) {
	t.Parallel()

	rt := &fakeNestedIngestRuntime{}
	w := newNestedIngestWorker(rt, pkglogger.Get())
	w.Start()
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
		defer cancel()
		_ = w.Stop(ctx)
	}()

	w.Enqueue("", "Read", "payload", "/tmp/file")
	w.Enqueue("   ", "Read", "payload", "/tmp/file")

	time.Sleep(20 * time.Millisecond)
	if got := w.EnqueuedTotal(); got != 0 {
		t.Errorf("EnqueuedTotal after blank enqueues = %d, want 0", got)
	}
	if got := len(rt.Calls()); got != 0 {
		t.Errorf("AddToolReadResult invoked for blank threadID: %d calls, want 0", got)
	}
}
