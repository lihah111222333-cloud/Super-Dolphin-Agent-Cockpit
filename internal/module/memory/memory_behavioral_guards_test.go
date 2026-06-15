package memory

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	threaddto "github.com/anthropic-ai/super-agent-v3/internal/dto/thread"
	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
)

// -----------------------------------------------------------------------------
// TestAutoDreamBusyDropsWithoutReplay
// -----------------------------------------------------------------------------

// TestAutoDreamBusyDropsWithoutReplay is the P22 P2 behavioral guard for
// the auto-dream scheduler's drop-is-terminal contract. Enqueue is a
// bounded, non-blocking send: when the queue is full the send takes the
// default branch and bumps droppedTotal. Once a dispatcher *is* running,
// those dropped events must stay dropped — the scheduler must not
// secretly replay them via a retry buffer or a pending map.
//
// The existing TestAutoDreamSchedulerEnqueueOverflowCountsAsDropped
// confirms the counter bumps; this test nails down the stronger
// invariant: after Start observes only what's in the queue at Start
// time, ProcessedTotal never exceeds the queue capacity regardless of
// how many overflow events were dropped before Start.
func TestAutoDreamBusyDropsWithoutReplay(t *testing.T) {
	t.Parallel()
	// enabled=true + consolidator=nil keeps Enqueue on the production
	// path and makes each process() call a fast increment-and-return,
	// so the worker drains whatever is in the queue as quickly as the
	// Go scheduler allows.
	hooks := newTestHooks(withEnabled(true))
	s := newAutoDreamScheduler(hooks, pkglogger.Get())

	// Fill the queue to its cap WITHOUT starting the worker — nothing
	// drains the queue yet, so the buffer holds exactly cap entries.
	for i := 0; i < autoDreamSchedulerQueueCap; i++ {
		s.Enqueue("thread-filler")
	}
	if got := s.DroppedTotal(); got != 0 {
		t.Fatalf("DroppedTotal at cap = %d, want 0 (queue not yet full)", got)
	}

	// Push overflow events. The queue is full, so every one of these
	// must take the drop branch and never reach the buffer.
	const overflow = 7
	for i := 0; i < overflow; i++ {
		s.Enqueue("thread-dropped")
	}
	if got := s.DroppedTotal(); got != overflow {
		t.Fatalf("DroppedTotal after overflow = %d, want %d", got, overflow)
	}

	// Now start the worker. It can only observe what's in the queue
	// buffer (cap entries); the overflow events were never buffered, so
	// the worker has no way to replay them. Poll for natural drain —
	// Stop on this scheduler is lossy (runWorker exits on stopCh without
	// draining the channel), so we must let the worker catch up before
	// shutting it down, otherwise pending-but-not-yet-picked entries
	// would be abandoned.
	s.Start()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if s.ProcessedTotal() >= int64(autoDreamSchedulerQueueCap) {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := s.Stop(ctx); err != nil {
		t.Fatalf("Stop error = %v", err)
	}

	// The critical invariant: the worker processed the queue's contents
	// exactly once each. Dropped events are terminal — their count
	// never contributes to ProcessedTotal, and after the buffer drains
	// no "replay" mechanism resurrects them.
	if got := s.ProcessedTotal(); got != int64(autoDreamSchedulerQueueCap) {
		t.Errorf("ProcessedTotal after drain = %d, want %d (no replay of dropped events)",
			got, autoDreamSchedulerQueueCap)
	}
	if got := s.DroppedTotal(); got != overflow {
		t.Errorf("DroppedTotal after drain = %d, want %d (drops stay terminal, no decrement)",
			got, overflow)
	}
}

// -----------------------------------------------------------------------------
// TestAutoDreamRequiresExplicitProjectScope
// -----------------------------------------------------------------------------

// stubAutoDreamExplicitScopeStore serves a single ThreadMetadata row
// with a non-empty AgentMemoryScope. When autoDreamAllowed runs against
// this metadata, hasAgentMemoryScope() returns true and the scheduler
// must decline to schedule a dream task; child-agent scoped threads do not
// own project-scope auto-dream writes.

type stubAutoDreamExplicitScopeStore struct {
	thread *contract.ThreadMetadata
}

func (s *stubAutoDreamExplicitScopeStore) GetByThreadID(_ context.Context, _ string) (*contract.ThreadMetadata, error) {
	return s.thread, nil
}

func (s *stubAutoDreamExplicitScopeStore) ListAll(context.Context) ([]contract.ThreadMetadata, error) {
	return nil, nil
}

// TestAutoDreamRequiresExplicitProjectScope is the P22 P2 behavioral
// guard for autoDreamAllowed's scope check: a thread that carries an
// explicit AgentMemoryScope must not use the project-scope auto-dream
// consolidator. maybeScheduleAutoDream must decline for such threads
// (return false) and must not start a dream task.

func TestAutoDreamRequiresExplicitProjectScope(t *testing.T) {
	t.Parallel()

	// Build a thread that LOOKS like an auto-memory-root thread
	// (threadKind=main, no parent/owner) but carries a non-empty
	// AgentMemoryScope. That combination must cause autoDreamAllowed
	// to refuse scheduling: child-agent scoped threads are not
	// project-scope auto-dream writers.

	now := time.Date(2026, 4, 24, 12, 0, 0, 0, time.UTC)
	finishedAt := now.Unix()
	thread := &contract.ThreadMetadata{
		ThreadID:         "thread-agent-scope",
		Cwd:              t.TempDir(),
		UpdatedAt:        now.Unix(),
		FinishedAt:       &finishedAt,
		AgentMemoryScope: "writer-agent",
		ConfigOverride:   mustStoredRuntimeConfig(t, map[string]any{"threadKind": "main"}),
	}
	store := &stubAutoDreamExplicitScopeStore{thread: thread}

	hooks := newMemoryLifecycleHooks(
		&Config{Enabled: true, RootDir: t.TempDir()},
		NewAutoDreamConsolidator(NewMemoryExtractor()),
		nil,
		nil,
		store,
		nil,
		NewMemoryExtractor(),
		NewManifestBuilder(),
	)
	hooks.timeNow = func() time.Time { return now }
	hooks.extractFn = func(context.Context, string) (string, error) {
		t.Fatal("extractFn must not run for agent-scoped threads")
		return "", nil
	}

	started, err := hooks.maybeScheduleAutoDream(context.Background(), "thread-agent-scope")
	if err != nil {
		t.Fatalf("maybeScheduleAutoDream() error = %v", err)
	}
	if started {
		t.Fatal("maybeScheduleAutoDream() = true for agent-scoped thread, want false")
	}
	if snap := hooks.dreamTaskSnapshot(); snap.Running {
		t.Fatalf("dream task running after agent-scope refusal: %#v", snap)
	}
}

// -----------------------------------------------------------------------------
// TestTeamSyncRuntimeSwapFinalFlush
// -----------------------------------------------------------------------------

// recordingTeamLifecycle satisfies teampkg.Lifecycle and records every
// Start/Stop call with its threadID under a mutex so tests can observe
// dispatch ordering after a drain.
type recordingTeamLifecycle struct {
	mu        sync.Mutex
	starts    []string
	stops     []string
	startN    atomic.Int64
	stopN     atomic.Int64
	blockStop chan struct{} // optional: hold a StopSession call open
}

func (r *recordingTeamLifecycle) StartSession(_ context.Context, threadID string, _ contract.BuildCtx) error {
	r.startN.Add(1)
	r.mu.Lock()
	r.starts = append(r.starts, threadID)
	r.mu.Unlock()
	return nil
}

func (r *recordingTeamLifecycle) StopSession(_ context.Context, threadID string) error {
	r.stopN.Add(1)
	r.mu.Lock()
	r.stops = append(r.stops, threadID)
	block := r.blockStop
	r.mu.Unlock()
	if block != nil {
		<-block
	}
	return nil
}

func (r *recordingTeamLifecycle) snapshot() (starts, stops []string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	starts = append([]string(nil), r.starts...)
	stops = append([]string(nil), r.stops...)
	// archguard:ignore naked_returns -- named results document the paired snapshot slices.
	return
}

// TestTeamSyncRuntimeSwapFinalFlush is the P22 P2 behavioral guard for
// the team-sync coordinator's FIFO drain contract. A "runtime swap"
// scenario is a sequence of thread-local Start/Stop events interleaved
// with events for other threads — the kind of traffic that arises when
// the user switches projects (Stop current session, Start new session).
// The coordinator must deliver every event to the TeamSync lifecycle in
// the order it was enqueued, with nothing lost during shutdown drain.
func TestTeamSyncRuntimeSwapFinalFlush(t *testing.T) {
	t.Parallel()

	lc := &recordingTeamLifecycle{}
	c := newTeamSyncCoordinator(lc, nil, pkglogger.Get())
	c.Start()

	// A realistic swap sequence: Start A, Stop A, Start B, Stop B,
	// Start A again (user returns to the first project). The
	// coordinator is strict FIFO per the dispatch loop, so these must
	// arrive in order regardless of any wake coalescing.
	ops := []struct {
		kind string
		id   string
	}{
		{"start", "thread-A"},
		{"stop", "thread-A"},
		{"start", "thread-B"},
		{"stop", "thread-B"},
		{"start", "thread-A"},
	}
	for _, op := range ops {
		switch op.kind {
		case "start":
			c.EnqueueStart(threaddto.Started{ThreadID: op.id, CWD: "/tmp/cwd"})
		case "stop":
			c.EnqueueStop(threaddto.Stopped{ThreadID: op.id})
		}
	}

	// Stop drains synchronously: by the time it returns every pending
	// op has been dispatched to the lifecycle. This is the "final
	// flush" guarantee — shutdown can never truncate the queue silently.
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := c.Stop(ctx); err != nil {
		t.Fatalf("Stop error = %v", err)
	}

	if got := c.ProcessedTotal(); got != int64(len(ops)) {
		t.Errorf("ProcessedTotal = %d, want %d (final flush must deliver every op)", got, len(ops))
	}

	starts, stops := lc.snapshot()
	wantStarts := []string{"thread-A", "thread-B", "thread-A"}
	wantStops := []string{"thread-A", "thread-B"}
	if !equalStringSlices(starts, wantStarts) {
		t.Errorf("StartSession order = %v, want %v (FIFO across swap)", starts, wantStarts)
	}
	if !equalStringSlices(stops, wantStops) {
		t.Errorf("StopSession order = %v, want %v (FIFO across swap)", stops, wantStops)
	}

	// Post-drain enqueue must be silently dropped — the swap is closed;
	// no new runtime binding should leak into the finished coordinator.
	preEnqueued := c.EnqueuedTotal()
	c.EnqueueStart(threaddto.Started{ThreadID: "thread-late", CWD: "/tmp/cwd"})
	if got := c.EnqueuedTotal(); got != preEnqueued {
		t.Errorf("teamSyncCoordinator accepted enqueue after final flush: delta = %d", got-preEnqueued)
	}
}

func equalStringSlices(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
