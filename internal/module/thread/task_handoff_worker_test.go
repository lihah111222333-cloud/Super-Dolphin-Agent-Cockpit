package thread

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	shareddto "github.com/anthropic-ai/super-agent-v3/internal/dto/shared"
	turndto "github.com/anthropic-ai/super-agent-v3/internal/dto/turn"
	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
)

// stubTaskHandoffRefresher satisfies taskHandoffRefresher for tests. It
// records every (threadID, seed) refresh call and can be configured to
// block on a signal so tests can observe drain ordering.
type stubTaskHandoffRefresher struct {
	mu     sync.Mutex
	calls  []stubTaskHandoffCall
	block  chan struct{} // when non-nil, refresh waits on it before returning
	errOut error
	count  atomic.Int64
}

type stubTaskHandoffCall struct {
	threadID string
	seed     taskHandoffRenderSeed
}

func (s *stubTaskHandoffRefresher) refreshTaskHandoffFromThread(_ context.Context, threadID string, seed taskHandoffRenderSeed) error {
	s.count.Add(1)
	s.mu.Lock()
	s.calls = append(s.calls, stubTaskHandoffCall{threadID: threadID, seed: seed})
	block := s.block
	s.mu.Unlock()
	if block != nil {
		<-block
	}
	return s.errOut
}

func (s *stubTaskHandoffRefresher) snapshot() []stubTaskHandoffCall {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]stubTaskHandoffCall, len(s.calls))
	copy(out, s.calls)
	return out
}

func waitForTaskHandoffCount(t *testing.T, stub *stubTaskHandoffRefresher, want int64, d time.Duration) {
	t.Helper()
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if stub.count.Load() >= want {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("refresh count = %d, want %d after %s", stub.count.Load(), want, d)
}

// TestTaskHandoffWorkerProcessesEnqueuedSeed verifies the happy path: a
// single Enqueue -> runWorker dispatch -> refresher is invoked with the
// same threadID + seed that was enqueued.
func TestTaskHandoffWorkerProcessesEnqueuedSeed(t *testing.T) {
	t.Parallel()
	stub := &stubTaskHandoffRefresher{}
	w := newTaskHandoffWorker(stub, pkglogger.Get())
	w.Start()
	defer func() { _ = w.Stop(context.Background()) }()

	seed := taskHandoffRenderSeed{SourceThreadID: "t-1", Status: "in_progress", Outcome: "ok"}
	w.Enqueue("t-1", seed)

	waitForTaskHandoffCount(t, stub, 1, 2*time.Second)
	calls := stub.snapshot()
	if len(calls) != 1 || calls[0].threadID != "t-1" || calls[0].seed != seed {
		t.Fatalf("calls = %#v, want single {t-1, seed}", calls)
	}
	if got := w.EnqueuedTotal(); got != 1 {
		t.Errorf("EnqueuedTotal = %d, want 1", got)
	}
	if got := w.ProcessedTotal(); got != 1 {
		t.Errorf("ProcessedTotal = %d, want 1", got)
	}
}

// TestTaskHandoffWorkerCoalescesSameThread verifies the last-write-wins
// contract: two Enqueues for the same threadID before the worker drains
// collapse to a single refresh that carries the *latest* seed.
func TestTaskHandoffWorkerCoalescesSameThread(t *testing.T) {
	t.Parallel()
	stub := &stubTaskHandoffRefresher{block: make(chan struct{})}
	w := newTaskHandoffWorker(stub, pkglogger.Get())
	w.Start()
	defer func() {
		// Drain any remaining blocked refresh calls so the worker can exit.
		go func() {
			for {
				select {
				case stub.block <- struct{}{}:
				default:
					return
				}
			}
		}()
		_ = w.Stop(context.Background())
	}()

	// First Enqueue -> worker starts processing (blocked on stub.block).
	w.Enqueue("t-1", taskHandoffRenderSeed{SourceThreadID: "t-1", Status: "first", Outcome: "first"})
	waitForTaskHandoffCount(t, stub, 1, 2*time.Second)

	// Two more Enqueues pile onto the pending map; the worker hasn't
	// woken yet (still blocked in the first call) so coalescing is
	// guaranteed to hit the "already in pending" branch.
	w.Enqueue("t-1", taskHandoffRenderSeed{SourceThreadID: "t-1", Status: "mid", Outcome: "mid"})
	w.Enqueue("t-1", taskHandoffRenderSeed{SourceThreadID: "t-1", Status: "last", Outcome: "last"})

	if got := w.CoalescedTotal(); got < 1 {
		t.Errorf("CoalescedTotal = %d, want >= 1", got)
	}

	// Let the first refresh complete; the coalesced pair becomes a
	// single second refresh carrying the latest seed.
	stub.block <- struct{}{}
	stub.block <- struct{}{}
	waitForTaskHandoffCount(t, stub, 2, 2*time.Second)

	calls := stub.snapshot()
	if len(calls) != 2 {
		t.Fatalf("calls = %d, want 2", len(calls))
	}
	if calls[1].seed.Status != "last" || calls[1].seed.Outcome != "last" {
		t.Errorf("coalesced seed = %#v, want last-write-wins", calls[1].seed)
	}
}

// TestTaskHandoffWorkerStopDrainsPending verifies Stop processes any
// pending entries before the worker goroutine exits.
func TestTaskHandoffWorkerStopDrainsPending(t *testing.T) {
	t.Parallel()
	stub := &stubTaskHandoffRefresher{}
	w := newTaskHandoffWorker(stub, pkglogger.Get())
	w.Start()

	w.Enqueue("t-1", taskHandoffRenderSeed{SourceThreadID: "t-1"})
	w.Enqueue("t-2", taskHandoffRenderSeed{SourceThreadID: "t-2"})

	if err := w.Stop(context.Background()); err != nil {
		t.Fatalf("Stop error = %v", err)
	}
	if got := stub.count.Load(); got != 2 {
		t.Errorf("count after Stop = %d, want 2", got)
	}
}

// TestTaskHandoffWorkerEnqueueAfterStopDrops confirms the gated-drop
// contract: once Stop fires, further Enqueues are silently dropped.
func TestTaskHandoffWorkerEnqueueAfterStopDrops(t *testing.T) {
	t.Parallel()
	stub := &stubTaskHandoffRefresher{}
	w := newTaskHandoffWorker(stub, pkglogger.Get())
	w.Start()
	if err := w.Stop(context.Background()); err != nil {
		t.Fatalf("Stop error = %v", err)
	}

	w.Enqueue("t-1", taskHandoffRenderSeed{SourceThreadID: "t-1"})
	if got := stub.count.Load(); got != 0 {
		t.Errorf("count after Enqueue-past-Stop = %d, want 0", got)
	}
	if got := w.EnqueuedTotal(); got != 0 {
		t.Errorf("EnqueuedTotal after Enqueue-past-Stop = %d, want 0", got)
	}
}

// TestTaskHandoffWorkerStopIdempotent verifies a second Stop is a no-op.
func TestTaskHandoffWorkerStopIdempotent(t *testing.T) {
	t.Parallel()
	stub := &stubTaskHandoffRefresher{}
	w := newTaskHandoffWorker(stub, pkglogger.Get())
	w.Start()
	if err := w.Stop(context.Background()); err != nil {
		t.Fatalf("first Stop = %v", err)
	}
	done := make(chan struct{})
	go func() {
		_ = w.Stop(context.Background())
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("second Stop did not return")
	}
}

// TestTaskHandoffWorkerNilRefresherShortCircuits verifies the worker
// stays a cheap no-op when constructed without a refresher.
func TestTaskHandoffWorkerNilRefresherShortCircuits(t *testing.T) {
	t.Parallel()
	w := newTaskHandoffWorker(nil, pkglogger.Get())
	w.Start()
	w.Enqueue("t-1", taskHandoffRenderSeed{SourceThreadID: "t-1"})
	if err := w.Stop(context.Background()); err != nil {
		t.Fatalf("Stop = %v", err)
	}
}

// TestTaskHandoffCallbackEnqueueOnly is the P22 P2 (thread S3) behavioral
// guard matching TestTeamSyncCallbackEnqueueOnly /
// TestNestedToolReadIngestEnqueueOnly: onTurnCompleted must not invoke
// the refresher synchronously on the dispatcher goroutine; every hit
// goes through the worker's Enqueue path.
func TestTaskHandoffCallbackEnqueueOnly(t *testing.T) {
	t.Parallel()
	// Block the refresher indefinitely so a synchronous call would
	// deadlock the test. If onTurnCompleted enqueues (the P2 shape), the
	// block is irrelevant.
	stub := &stubTaskHandoffRefresher{block: make(chan struct{})}

	svc := &service{
		logger:      silentLogger(),
		sharedFiles: &stubSharedFileStore{},
	}
	svc.taskHandoffWorker = newTaskHandoffWorker(stub, svc.logger)
	svc.taskHandoffWorker.Start()
	defer func() {
		// Unblock refresher so Stop's drain completes before grace fires.
		close(stub.block)
		_ = svc.taskHandoffWorker.Stop(context.Background())
	}()

	done := make(chan struct{})
	go func() {
		svc.onTurnCompleted(turndto.TurnCompleted{
			TurnHeader: shareddto.TurnHeader{
				AgentHeader: shareddto.AgentHeader{
					ThreadHeader: shareddto.ThreadHeader{ThreadID: "thread-xyz"},
				},
			},
			Status:  "done",
			Summary: "ok",
			Success: true,
		})
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("onTurnCompleted blocked on synchronous refresher; expected Enqueue-only")
	}

	if got := svc.taskHandoffWorker.EnqueuedTotal(); got != 1 {
		t.Errorf("EnqueuedTotal after onTurnCompleted = %d, want 1", got)
	}
}
