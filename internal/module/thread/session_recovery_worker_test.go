package thread

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	agentdto "github.com/anthropic-ai/super-agent-v3/internal/dto/agent"
	sharedto "github.com/anthropic-ai/super-agent-v3/internal/dto/shared"
	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
)

// stubSessionRecoverer satisfies sessionRecoverer for tests. It records
// every processed event (plus the ctx snapshot at call time) and can be
// configured to block until the context cancels.
type stubSessionRecoverer struct {
	mu         sync.Mutex
	calls      []agentdto.AgentFailed
	block      chan struct{} // when non-nil, processSessionRecovery waits on block OR ctx
	count      atomic.Int64
	ctxCancels atomic.Int64
}

func (s *stubSessionRecoverer) processSessionRecovery(ctx context.Context, ev agentdto.AgentFailed) {
	s.count.Add(1)
	s.mu.Lock()
	s.calls = append(s.calls, ev)
	block := s.block
	s.mu.Unlock()
	if block != nil {
		select {
		case <-block:
		case <-ctx.Done():
			s.ctxCancels.Add(1)
			return
		}
	}
}

func (s *stubSessionRecoverer) snapshot() []agentdto.AgentFailed {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]agentdto.AgentFailed, len(s.calls))
	copy(out, s.calls)
	return out
}

func waitForSessionRecoveryCount(t *testing.T, stub *stubSessionRecoverer, want int64, d time.Duration) {
	t.Helper()
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if stub.count.Load() >= want {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("process count = %d, want %d after %s", stub.count.Load(), want, d)
}

func newAgentFailedForWorker(agentID, threadID string, recoverable bool) agentdto.AgentFailed {
	return agentdto.AgentFailed{
		AgentSessionHeader: sharedto.AgentSessionHeader{
			AgentHeader: sharedto.AgentHeader{
				ThreadHeader: sharedto.ThreadHeader{ThreadID: threadID},
				AgentID:      agentID,
			},
		},
		Recoverable: recoverable,
	}
}

// TestSessionRecoveryWorkerProcessesEnqueuedEvent verifies the happy
// path: one Enqueue -> tracked goroutine dispatch -> processor invoked.
func TestSessionRecoveryWorkerProcessesEnqueuedEvent(t *testing.T) {
	t.Parallel()
	stub := &stubSessionRecoverer{}
	w := newSessionRecoveryWorker(stub, pkglogger.Get())
	w.Start()
	defer func() { _ = w.Stop(context.Background()) }()

	ev := newAgentFailedForWorker("agent-1", "thread-1", true)
	w.Enqueue("thread-1", ev)

	waitForSessionRecoveryCount(t, stub, 1, 2*time.Second)
	if got := stub.snapshot()[0].AgentID; got != "agent-1" {
		t.Errorf("processed event AgentID = %q, want agent-1", got)
	}
	if got := w.EnqueuedTotal(); got != 1 {
		t.Errorf("EnqueuedTotal = %d, want 1", got)
	}
	if got := w.ProcessedTotal(); got != 1 {
		t.Errorf("ProcessedTotal = %d, want 1", got)
	}
}

// TestSessionRecoveryWorkerCoalescesSameTarget verifies that a burst of
// events for the same target collapses to one processor invocation
// (rate-limit and recovery work happen once per burst, matching the
// coalesced worker contract).
func TestSessionRecoveryWorkerCoalescesSameTarget(t *testing.T) {
	t.Parallel()
	stub := &stubSessionRecoverer{block: make(chan struct{})}
	w := newSessionRecoveryWorker(stub, pkglogger.Get())
	w.Start()
	defer func() {
		close(stub.block)
		_ = w.Stop(context.Background())
	}()

	w.Enqueue("thread-1", newAgentFailedForWorker("agent-1", "thread-1", true))
	waitForSessionRecoveryCount(t, stub, 1, 2*time.Second)

	w.Enqueue("thread-1", newAgentFailedForWorker("agent-1", "thread-1", true))
	w.Enqueue("thread-1", newAgentFailedForWorker("agent-1", "thread-1", true))

	if got := w.CoalescedTotal(); got < 1 {
		t.Errorf("CoalescedTotal = %d, want >= 1", got)
	}
}

// TestSessionRecoveryWorkerDispatchesParallelForDifferentTargets verifies
// that concurrent failures for different targets run their recovery
// goroutines in parallel (not serialized through a single worker loop).
func TestSessionRecoveryWorkerDispatchesParallelForDifferentTargets(t *testing.T) {
	t.Parallel()
	stub := &stubSessionRecoverer{block: make(chan struct{})}
	w := newSessionRecoveryWorker(stub, pkglogger.Get())
	w.Start()
	defer func() {
		close(stub.block)
		_ = w.Stop(context.Background())
	}()

	w.Enqueue("thread-a", newAgentFailedForWorker("agent-a", "thread-a", true))
	w.Enqueue("thread-b", newAgentFailedForWorker("agent-b", "thread-b", true))

	// Both should land inside processSessionRecovery (blocked on stub) —
	// if the worker serialized, only one would be observed until we
	// unblock the first.
	waitForSessionRecoveryCount(t, stub, 2, 2*time.Second)
}

// TestSessionRecoveryWorkerStopCancelsCtx verifies that Stop's cancel
// breaks an in-flight processSessionRecovery blocking on ctx.Done.
func TestSessionRecoveryWorkerStopCancelsCtx(t *testing.T) {
	t.Parallel()
	stub := &stubSessionRecoverer{block: make(chan struct{})}
	defer close(stub.block)

	w := newSessionRecoveryWorker(stub, pkglogger.Get())
	w.Start()

	w.Enqueue("thread-1", newAgentFailedForWorker("agent-1", "thread-1", true))
	waitForSessionRecoveryCount(t, stub, 1, 2*time.Second)

	// Stop should cancel w.ctx, which the blocked stub observes and
	// returns from; inflight.Wait then completes and Stop returns.
	start := time.Now()
	if err := w.Stop(context.Background()); err != nil {
		t.Fatalf("Stop error = %v", err)
	}
	if elapsed := time.Since(start); elapsed > 3*time.Second {
		t.Errorf("Stop took %s, want ctx-cancel-fast exit", elapsed)
	}
	if got := stub.ctxCancels.Load(); got != 1 {
		t.Errorf("ctxCancels = %d, want 1 (blocked recovery saw ctx.Done)", got)
	}
}

// TestSessionRecoveryWorkerEnqueueAfterStopDrops confirms the gated-drop
// contract.
func TestSessionRecoveryWorkerEnqueueAfterStopDrops(t *testing.T) {
	t.Parallel()
	stub := &stubSessionRecoverer{}
	w := newSessionRecoveryWorker(stub, pkglogger.Get())
	w.Start()
	if err := w.Stop(context.Background()); err != nil {
		t.Fatalf("Stop error = %v", err)
	}

	w.Enqueue("thread-1", newAgentFailedForWorker("agent-1", "thread-1", true))
	if got := stub.count.Load(); got != 0 {
		t.Errorf("count after Enqueue-past-Stop = %d, want 0", got)
	}
	if got := w.EnqueuedTotal(); got != 0 {
		t.Errorf("EnqueuedTotal after Enqueue-past-Stop = %d, want 0", got)
	}
}

// TestSessionRecoveryWorkerStopIdempotent verifies a second Stop is a no-op.
func TestSessionRecoveryWorkerStopIdempotent(t *testing.T) {
	t.Parallel()
	stub := &stubSessionRecoverer{}
	w := newSessionRecoveryWorker(stub, pkglogger.Get())
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

// TestSessionRecoveryWorkerNilRecovererShortCircuits verifies cheap
// no-op when constructed without a recoverer.
func TestSessionRecoveryWorkerNilRecovererShortCircuits(t *testing.T) {
	t.Parallel()
	w := newSessionRecoveryWorker(nil, pkglogger.Get())
	w.Start()
	w.Enqueue("thread-1", newAgentFailedForWorker("agent-1", "thread-1", true))
	if err := w.Stop(context.Background()); err != nil {
		t.Fatalf("Stop = %v", err)
	}
}

// TestAgentFailedCallbackEnqueueOnly is the P22 P2 (thread S2) behavioral
// guard matching TestTaskHandoffCallbackEnqueueOnly /
// TestAgentLaunchedCallbackEnqueueOnly: onAgentFailed must not run the
// 3s reconnect delay or the recovery on the dispatcher goroutine; every
// hit goes through the worker's Enqueue path.
func TestAgentFailedCallbackEnqueueOnly(t *testing.T) {
	t.Parallel()
	stub := &stubSessionRecoverer{block: make(chan struct{})}

	svc := &service{logger: silentLogger()}
	svc.sessionRecoveryWorker = newSessionRecoveryWorker(stub, svc.logger)
	svc.sessionRecoveryWorker.Start()
	defer func() {
		close(stub.block)
		_ = svc.sessionRecoveryWorker.Stop(context.Background())
	}()

	done := make(chan struct{})
	go func() {
		svc.onAgentFailed(newAgentFailedForWorker("agent-1", "thread-1", true))
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("onAgentFailed blocked on synchronous recovery; expected Enqueue-only")
	}

	if got := svc.sessionRecoveryWorker.EnqueuedTotal(); got != 1 {
		t.Errorf("EnqueuedTotal after onAgentFailed = %d, want 1", got)
	}
}

// TestAgentFailedCallbackDropsNonRecoverable verifies the cheap
// early-return path stays on the callback (no Enqueue when the event
// flags Recoverable=false).
func TestAgentFailedCallbackDropsNonRecoverable(t *testing.T) {
	t.Parallel()
	stub := &stubSessionRecoverer{}
	svc := &service{logger: silentLogger()}
	svc.sessionRecoveryWorker = newSessionRecoveryWorker(stub, svc.logger)
	svc.sessionRecoveryWorker.Start()
	defer func() { _ = svc.sessionRecoveryWorker.Stop(context.Background()) }()

	svc.onAgentFailed(newAgentFailedForWorker("agent-1", "thread-1", false))

	if got := svc.sessionRecoveryWorker.EnqueuedTotal(); got != 0 {
		t.Errorf("EnqueuedTotal after non-recoverable event = %d, want 0", got)
	}
}
