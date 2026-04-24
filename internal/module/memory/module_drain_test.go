package memory

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	threaddto "github.com/anthropic-ai/super-agent-v3/internal/dto/thread"
	teampkg "github.com/anthropic-ai/super-agent-v3/internal/module/memory/team"
	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
)

// drainTestNestedRuntime satisfies nestedIngestRuntime and records every
// AddToolReadResult call so tests can verify the worker drained pending
// entries before exit.
type drainTestNestedRuntime struct {
	mu    sync.Mutex
	calls []string // threadIDs in the order AddToolReadResult was called
}

func (r *drainTestNestedRuntime) AddToolReadResult(threadID, _, _, _ string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, threadID)
}

func (r *drainTestNestedRuntime) callCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.calls)
}

// drainTestTeamLifecycle satisfies teampkg.Lifecycle for drain tests. It
// counts StartSession/StopSession calls atomically so the test can read
// them without racing the coordinator's dispatcher goroutine.
type drainTestTeamLifecycle struct {
	startCalls atomic.Int64
	stopCalls  atomic.Int64
}

func (n *drainTestTeamLifecycle) StartSession(_ context.Context, _ string, _ contract.BuildCtx) error {
	n.startCalls.Add(1)
	return nil
}

func (n *drainTestTeamLifecycle) StopSession(_ context.Context, _ string) error {
	n.stopCalls.Add(1)
	return nil
}

// TestMemoryHookWorkerDrainsOnStop is the P22 P2 behavioral guard for
// drainMemoryHooks: with all three bus-callback workers
// (autoDreamScheduler, nestedIngestWorker, teamSyncCoordinator) started
// and pre-loaded with pending work, drainMemoryHooks must Stop every
// owner so their processed counters catch up to the enqueued ones
// before it returns, and post-drain Enqueue calls must be silently
// dropped.
//
// Shutdown is the only user-observable correctness boundary for these
// workers — if one fails to drain, shutdown silently loses the work
// that was pending. This test locks that invariant so future additions
// to drainMemoryHooks (new worker types, reordered drains) cannot
// regress it without failing here first.
func TestMemoryHookWorkerDrainsOnStop(t *testing.T) {
	t.Parallel()

	// autoDreamScheduler: enabled hooks + nil consolidator = fast-path
	// that short-circuits inside autoDreamThreadEligible so process()
	// just increments processedTotal. Mirrors the existing
	// auto_dream_scheduler_test.go pattern.
	hooks := &MemoryLifecycleHooks{enabled: true}
	scheduler := newAutoDreamScheduler(hooks, pkglogger.Get())

	// nestedIngestWorker: a tiny runtime that records every dispatch.
	rt := &drainTestNestedRuntime{}
	nested := newNestedIngestWorker(rt, pkglogger.Get())

	// teamSyncCoordinator: noop lifecycle; store nil so
	// StartSessionFromThreadEvent takes the ev.CWD-only branch.
	var svc teampkg.Lifecycle = &drainTestTeamLifecycle{}
	teamSync := newTeamSyncCoordinator(svc, nil, pkglogger.Get())

	scheduler.Start()
	nested.Start()
	teamSync.Start()

	const enqueuePerWorker = 3
	for i := 0; i < enqueuePerWorker; i++ {
		scheduler.Enqueue("thread-scheduler")
		// Unique persistedPath per call so nestedIngestWorker's
		// coalesce key (thread + tool + persistedPath) doesn't collapse
		// the three enqueues into one — we want the drain to process
		// every distinct pending entry.
		nested.Enqueue("thread-nested", "tool", "result",
			"/tmp/path-"+string(rune('0'+i)))
		teamSync.EnqueueStart(threaddto.Started{
			ThreadID: "thread-teamsync",
			CWD:      "/tmp/cwd",
		})
	}

	// autoDreamScheduler.Stop is lossy: runWorker exits on stopCh
	// without draining the remaining channel buffer (see
	// auto_dream_scheduler.go runWorker — no drainPending branch on
	// stopCh). Under parallel test load the scheduler's goroutine may
	// not have pulled all 3 enqueued thread-scheduler entries before
	// drainMemoryHooks fires; that would race the lossy Stop and drop
	// entries. Poll for the scheduler to catch up before calling
	// drainMemoryHooks. nested / teamSync both drain their pending map
	// inside Stop, so they do not need a pre-wait.
	pollDeadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(pollDeadline) {
		if scheduler.ProcessedTotal() >= int64(enqueuePerWorker) {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}

	// drainMemoryHooks is the subject under test. It must drain every
	// worker in turn, bounded by ctx, before returning.
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	drainMemoryHooks(ctx, scheduler, nested, teamSync, nil)

	// After drain every worker's ProcessedTotal must reflect the
	// pending work it observed. autoDreamScheduler dedup is per-threadID
	// too (queue of strings; repeated identical IDs still enqueue as
	// separate channel sends), so ProcessedTotal matches enqueue count.
	if got := scheduler.ProcessedTotal(); got < int64(enqueuePerWorker) {
		t.Errorf("autoDreamScheduler ProcessedTotal = %d, want >= %d", got, enqueuePerWorker)
	}
	// nestedIngestWorker's pending-set was keyed by unique persistedPaths,
	// so all three must have been dispatched.
	if got := nested.ProcessedTotal(); got != int64(enqueuePerWorker) {
		t.Errorf("nestedIngestWorker ProcessedTotal = %d, want %d", got, enqueuePerWorker)
	}
	if got := rt.callCount(); got != enqueuePerWorker {
		t.Errorf("nestedIngestWorker AddToolReadResult calls = %d, want %d", got, enqueuePerWorker)
	}
	// teamSyncCoordinator is strict FIFO with no coalescing.
	if got := teamSync.ProcessedTotal(); got != int64(enqueuePerWorker) {
		t.Errorf("teamSyncCoordinator ProcessedTotal = %d, want %d", got, enqueuePerWorker)
	}

	// A second Stop per owner must be a no-op (idempotent). This also
	// confirms the workers closed doneCh — Stop observes it already
	// closed on the repeat call and returns immediately.
	if err := scheduler.Stop(ctx); err != nil {
		t.Errorf("second Stop autoDreamScheduler = %v", err)
	}
	if err := nested.Stop(ctx); err != nil {
		t.Errorf("second Stop nestedIngestWorker = %v", err)
	}
	if err := teamSync.Stop(ctx); err != nil {
		t.Errorf("second Stop teamSyncCoordinator = %v", err)
	}

	// Enqueue after drain must be silently dropped across every owner
	// (the gate closed when Stop fired). This prevents a post-shutdown
	// bus straggler from pushing work that would never be processed.
	// autoDreamScheduler counts post-gate drops via DroppedTotal;
	// nested / teamSync expose EnqueuedTotal directly.
	preSchedDropped := scheduler.DroppedTotal()
	preNestedEnqueued := nested.EnqueuedTotal()
	preTeamEnqueued := teamSync.EnqueuedTotal()
	scheduler.Enqueue("thread-late")
	nested.Enqueue("thread-late", "tool", "result", "/tmp/late")
	teamSync.EnqueueStart(threaddto.Started{ThreadID: "thread-late", CWD: "/tmp/cwd"})
	if got := scheduler.DroppedTotal(); got != preSchedDropped+1 {
		t.Errorf("autoDreamScheduler did not drop post-Stop enqueue: DroppedTotal delta = %d, want 1", got-preSchedDropped)
	}
	if nested.EnqueuedTotal() != preNestedEnqueued {
		t.Error("nestedIngestWorker accepted enqueue after drain")
	}
	if teamSync.EnqueuedTotal() != preTeamEnqueued {
		t.Error("teamSyncCoordinator accepted enqueue after drain")
	}
}
