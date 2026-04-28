package memory

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	threaddto "github.com/anthropic-ai/super-agent-v3/internal/dto/thread"
	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
)

// fakeTeamSyncLifecycle is a minimal teampkg.Lifecycle used to observe the
// coordinator from the outside. startBlock lets a test pin the worker
// goroutine inside StartSession so we can prove the callback path does not
// share that goroutine.
type fakeTeamSyncLifecycle struct {
	mu         sync.Mutex
	starts     []threadStartRecord
	stops      []string
	startBlock chan struct{}
}

type threadStartRecord struct {
	threadID string
	buildCtx contract.BuildCtx
}

func (f *fakeTeamSyncLifecycle) StartSession(_ context.Context, threadID string, buildCtx contract.BuildCtx) error {
	if f.startBlock != nil {
		<-f.startBlock
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.starts = append(f.starts, threadStartRecord{threadID: threadID, buildCtx: buildCtx})
	return nil
}

func (f *fakeTeamSyncLifecycle) StopSession(_ context.Context, threadID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.stops = append(f.stops, threadID)
	return nil
}

func (f *fakeTeamSyncLifecycle) StartCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.starts)
}

func (f *fakeTeamSyncLifecycle) StopIDs() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]string, len(f.stops))
	copy(out, f.stops)
	return out
}

// TestTeamSyncCallbackEnqueueOnly is the P22 P2 Finding 5/6 TDD test named
// in docs/plans/迁移/p22/P2_BusRuntimeDecoupling.md:415.
//
// It pins StartSession inside the fake lifecycle so we can prove two facts
// in one shot:
//
//  1. A burst of EnqueueStart / EnqueueStop calls never blocks on the
//     lifecycle's slow-path. That's the whole reason Finding 5 exists —
//     pre-P2 the callback called StartSessionFromThreadEvent directly,
//     which was how git/repo-slug resolution + remote pull ended up on
//     the dispatcher goroutine.
//  2. While StartSession is still pinned, the lifecycle must not have
//     observed any Stop calls either — the worker is strictly serial, so
//     the pending Stops have to wait behind the stuck Start.
func TestTeamSyncCallbackEnqueueOnly(t *testing.T) {
	t.Parallel()

	block := make(chan struct{})
	svc := &fakeTeamSyncLifecycle{startBlock: block}
	c := newTeamSyncCoordinator(svc, nil, pkglogger.Get())
	c.Start()

	enqueueDone := make(chan struct{})
	go func() {
		c.EnqueueStart(threaddto.Started{ThreadID: "thread-A", CWD: "/tmp"})
		for i := 0; i < 16; i++ {
			c.EnqueueStart(threaddto.Started{ThreadID: "thread-burst", CWD: "/tmp"})
			c.EnqueueStop(threaddto.Stopped{ThreadID: "thread-burst"})
		}
		close(enqueueDone)
	}()
	select {
	case <-enqueueDone:
	case <-time.After(time.Second):
		t.Fatalf("Enqueue blocked while StartSession was pinned; bus callback must never share that goroutine")
	}

	if got := svc.StartCount(); got != 0 {
		t.Fatalf("StartSession invoked %d times while worker was blocked; callback must not drive the lifecycle", got)
	}
	if got := len(svc.StopIDs()); got != 0 {
		t.Fatalf("StopSession invoked %d times while worker was blocked; worker must be strictly serial", got)
	}
	if enq := c.EnqueuedTotal(); enq != 33 { // 1 + 16*(Start+Stop)
		t.Fatalf("EnqueuedTotal = %d, want 33", enq)
	}

	close(block)

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if c.ProcessedTotal() >= 33 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if got := c.ProcessedTotal(); got != 33 {
		t.Fatalf("ProcessedTotal after drain = %d, want 33", got)
	}
	// All enqueued ops must reach the lifecycle (lossless).
	if got := svc.StartCount(); got != 17 { // 1 + 16 bursty Starts
		t.Errorf("StartSession total = %d, want 17 (lossless)", got)
	}
	if got := len(svc.StopIDs()); got != 16 {
		t.Errorf("StopSession total = %d, want 16 (lossless)", got)
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := c.Stop(ctx); err != nil {
		t.Fatalf("Stop() = %v, want nil", err)
	}
}

// TestTeamSyncCoordinatorEnqueueAfterStopDrops mirrors the gate semantics
// used by the auto-dream scheduler and nested ingest worker: once Stop
// fires, further Enqueue* is silently dropped rather than buffered.
func TestTeamSyncCoordinatorEnqueueAfterStopDrops(t *testing.T) {
	t.Parallel()

	svc := &fakeTeamSyncLifecycle{}
	c := newTeamSyncCoordinator(svc, nil, pkglogger.Get())
	c.Start()

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := c.Stop(ctx); err != nil {
		t.Fatalf("Stop() = %v, want nil", err)
	}

	beforeEnq := c.EnqueuedTotal()
	c.EnqueueStart(threaddto.Started{ThreadID: "thread-post-stop", CWD: "/tmp"})
	c.EnqueueStop(threaddto.Stopped{ThreadID: "thread-post-stop"})
	if got := c.EnqueuedTotal(); got != beforeEnq {
		t.Errorf("EnqueuedTotal after post-Stop enqueue = %d, want %d", got, beforeEnq)
	}
	if got := svc.StartCount(); got != 0 {
		t.Errorf("StartSession invoked after Stop: %d, want 0", got)
	}
	if got := len(svc.StopIDs()); got != 0 {
		t.Errorf("StopSession invoked after Stop: %d, want 0", got)
	}
}

// TestTeamSyncCoordinatorPreservesFIFOOrder verifies that ops land on the
// lifecycle in the same order they were enqueued. The runtime-swap + final
// flush invariant inside TeamSyncService depends on Start-before-Stop
// ordering at the service boundary; the coordinator must not reorder.
func TestTeamSyncCoordinatorPreservesFIFOOrder(t *testing.T) {
	t.Parallel()

	svc := &fakeTeamSyncLifecycle{}
	c := newTeamSyncCoordinator(svc, nil, pkglogger.Get())
	c.Start()
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = c.Stop(ctx)
	}()

	c.EnqueueStart(threaddto.Started{ThreadID: "thread-1", CWD: "/tmp/one"})
	c.EnqueueStart(threaddto.Started{ThreadID: "thread-2", CWD: "/tmp/two"})
	c.EnqueueStop(threaddto.Stopped{ThreadID: "thread-1"})
	c.EnqueueStop(threaddto.Stopped{ThreadID: "thread-2"})

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if c.ProcessedTotal() >= 4 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if got := c.ProcessedTotal(); got != 4 {
		t.Fatalf("ProcessedTotal = %d, want 4", got)
	}

	svc.mu.Lock()
	starts := append([]threadStartRecord(nil), svc.starts...)
	stops := append([]string(nil), svc.stops...)
	svc.mu.Unlock()

	wantStarts := []string{"thread-1", "thread-2"}
	for i, want := range wantStarts {
		if i >= len(starts) || starts[i].threadID != want {
			t.Fatalf("starts[%d] = %+v, want thread_id=%q", i, starts, want)
		}
	}
	wantStops := []string{"thread-1", "thread-2"}
	for i, want := range wantStops {
		if i >= len(stops) || stops[i] != want {
			t.Fatalf("stops[%d] = %+v, want thread_id=%q", i, stops, want)
		}
	}
}

// TestTeamSyncCoordinatorBlankThreadIDIsNoop matches the blank-input
// short-circuit of the other P2 workers — an empty threadID neither hits
// EnqueuedTotal nor reaches the lifecycle.
func TestTeamSyncCoordinatorBlankThreadIDIsNoop(t *testing.T) {
	t.Parallel()

	svc := &fakeTeamSyncLifecycle{}
	c := newTeamSyncCoordinator(svc, nil, pkglogger.Get())
	c.Start()
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
		defer cancel()
		_ = c.Stop(ctx)
	}()

	c.EnqueueStart(threaddto.Started{ThreadID: ""})
	c.EnqueueStart(threaddto.Started{ThreadID: "   "})
	c.EnqueueStop(threaddto.Stopped{ThreadID: ""})

	time.Sleep(20 * time.Millisecond)
	if got := c.EnqueuedTotal(); got != 0 {
		t.Errorf("EnqueuedTotal after blank enqueues = %d, want 0", got)
	}
	if got := svc.StartCount(); got != 0 {
		t.Errorf("StartSession invoked for blank threadID: %d, want 0", got)
	}
}

// TestTeamSyncCoordinatorStopDrainsPending verifies the lossless Stop
// contract: an enqueued op that wasn't yet processed via wake is still
// drained through the stopCh branch before Stop returns.
func TestTeamSyncCoordinatorStopDrainsPending(t *testing.T) {
	t.Parallel()

	// Build a lifecycle that blocks the first Start so we can reliably pack
	// a pending queue behind it, then releases on demand.
	block := make(chan struct{})
	svc := &fakeTeamSyncLifecycle{startBlock: block}
	c := newTeamSyncCoordinator(svc, nil, pkglogger.Get())
	c.Start()

	// Seed the worker with one in-flight Start, then queue several more
	// ops behind it. We can't observe "worker is inside StartSession" from
	// the outside directly, but EnqueuedTotal climbing to 1 is enough: the
	// wake channel is cap 1, so the worker has either consumed it and is
	// now blocked in StartSession, or will consume on the next schedule.
	c.EnqueueStart(threaddto.Started{ThreadID: "thread-in-flight", CWD: "/tmp"})
	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		if c.EnqueuedTotal() >= 1 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	c.EnqueueStart(threaddto.Started{ThreadID: "thread-queued", CWD: "/tmp"})
	c.EnqueueStop(threaddto.Stopped{ThreadID: "thread-queued"})

	// Unblock and stop concurrently; Stop must wait for the drain.
	close(block)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := c.Stop(ctx); err != nil {
		t.Fatalf("Stop() = %v, want nil", err)
	}

	if got := c.ProcessedTotal(); got != 3 {
		t.Fatalf("ProcessedTotal after drain = %d, want 3", got)
	}
	if got := svc.StartCount(); got != 2 {
		t.Errorf("StartSession total = %d, want 2", got)
	}
	if got := len(svc.StopIDs()); got != 1 {
		t.Errorf("StopSession total = %d, want 1", got)
	}
}
