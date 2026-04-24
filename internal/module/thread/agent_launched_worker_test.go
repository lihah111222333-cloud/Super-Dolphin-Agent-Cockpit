package thread

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	agentdto "github.com/anthropic-ai/super-agent-v3/internal/dto/agent"
	sharedto "github.com/anthropic-ai/super-agent-v3/internal/dto/shared"
	bindingstore "github.com/anthropic-ai/super-agent-v3/internal/store/binding"
	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
)

// stubAgentLaunchedProcessor satisfies agentLaunchedProcessor for tests.
// It records every processed event and can be configured to block on a
// signal so tests can observe drain ordering.
type stubAgentLaunchedProcessor struct {
	mu    sync.Mutex
	calls []agentdto.AgentLaunched
	block chan struct{}
	count atomic.Int64
}

func (s *stubAgentLaunchedProcessor) processAgentLaunched(ev agentdto.AgentLaunched) {
	s.count.Add(1)
	s.mu.Lock()
	s.calls = append(s.calls, ev)
	block := s.block
	s.mu.Unlock()
	if block != nil {
		<-block
	}
}

func (s *stubAgentLaunchedProcessor) snapshot() []agentdto.AgentLaunched {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]agentdto.AgentLaunched, len(s.calls))
	copy(out, s.calls)
	return out
}

func waitForAgentLaunchedCount(t *testing.T, stub *stubAgentLaunchedProcessor, want int64, d time.Duration) {
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

func newAgentLaunchedForWorker(agentID, threadID, sessionID string) agentdto.AgentLaunched {
	return agentdto.AgentLaunched{
		AgentSessionHeader: sharedto.AgentSessionHeader{
			AgentHeader: sharedto.AgentHeader{
				ThreadHeader: sharedto.ThreadHeader{ThreadID: threadID},
				AgentID:      agentID,
			},
			SessionID: sessionID,
		},
	}
}

// TestAgentLaunchedWorkerProcessesEnqueuedEvent verifies the happy path:
// one Enqueue -> worker dispatch -> processor invoked with same event.
func TestAgentLaunchedWorkerProcessesEnqueuedEvent(t *testing.T) {
	t.Parallel()
	stub := &stubAgentLaunchedProcessor{}
	w := newAgentLaunchedWorker(stub, pkglogger.Get())
	w.Start()
	defer func() { _ = w.Stop(context.Background()) }()

	ev := newAgentLaunchedForWorker("agent-1", "thread-1", "uuid-1")
	w.Enqueue("agent-1", ev)

	waitForAgentLaunchedCount(t, stub, 1, 2*time.Second)
	calls := stub.snapshot()
	if len(calls) != 1 || calls[0].AgentID != "agent-1" || calls[0].SessionID != "uuid-1" {
		t.Fatalf("calls = %#v, want [{agent-1, uuid-1}]", calls)
	}
	if got := w.EnqueuedTotal(); got != 1 {
		t.Errorf("EnqueuedTotal = %d, want 1", got)
	}
	if got := w.ProcessedTotal(); got != 1 {
		t.Errorf("ProcessedTotal = %d, want 1", got)
	}
}

// TestAgentLaunchedWorkerCoalescesSameKey verifies coalescing: multiple
// events for the same agentID piling up while the worker is processing
// the first collapse to a single second dispatch carrying the latest
// event.
func TestAgentLaunchedWorkerCoalescesSameKey(t *testing.T) {
	t.Parallel()
	stub := &stubAgentLaunchedProcessor{block: make(chan struct{})}
	w := newAgentLaunchedWorker(stub, pkglogger.Get())
	w.Start()
	defer func() {
		// Drain blocked processor calls.
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

	w.Enqueue("agent-1", newAgentLaunchedForWorker("agent-1", "thread-1", "uuid-first"))
	waitForAgentLaunchedCount(t, stub, 1, 2*time.Second)

	w.Enqueue("agent-1", newAgentLaunchedForWorker("agent-1", "thread-1", "uuid-mid"))
	w.Enqueue("agent-1", newAgentLaunchedForWorker("agent-1", "thread-1", "uuid-last"))

	if got := w.CoalescedTotal(); got < 1 {
		t.Errorf("CoalescedTotal = %d, want >= 1", got)
	}

	stub.block <- struct{}{}
	stub.block <- struct{}{}
	waitForAgentLaunchedCount(t, stub, 2, 2*time.Second)

	calls := stub.snapshot()
	if len(calls) != 2 {
		t.Fatalf("calls = %d, want 2", len(calls))
	}
	if calls[1].SessionID != "uuid-last" {
		t.Errorf("coalesced event SessionID = %q, want last-write-wins (%q)", calls[1].SessionID, "uuid-last")
	}
}

// TestAgentLaunchedWorkerStopDrainsPending verifies Stop processes
// pending entries before exit.
func TestAgentLaunchedWorkerStopDrainsPending(t *testing.T) {
	t.Parallel()
	stub := &stubAgentLaunchedProcessor{}
	w := newAgentLaunchedWorker(stub, pkglogger.Get())
	w.Start()

	w.Enqueue("agent-1", newAgentLaunchedForWorker("agent-1", "thread-1", "uuid-1"))
	w.Enqueue("agent-2", newAgentLaunchedForWorker("agent-2", "thread-2", "uuid-2"))

	if err := w.Stop(context.Background()); err != nil {
		t.Fatalf("Stop error = %v", err)
	}
	if got := stub.count.Load(); got != 2 {
		t.Errorf("count after Stop = %d, want 2", got)
	}
}

// TestAgentLaunchedWorkerEnqueueAfterStopDrops confirms the gated-drop
// contract.
func TestAgentLaunchedWorkerEnqueueAfterStopDrops(t *testing.T) {
	t.Parallel()
	stub := &stubAgentLaunchedProcessor{}
	w := newAgentLaunchedWorker(stub, pkglogger.Get())
	w.Start()
	if err := w.Stop(context.Background()); err != nil {
		t.Fatalf("Stop error = %v", err)
	}

	w.Enqueue("agent-1", newAgentLaunchedForWorker("agent-1", "thread-1", "uuid-1"))
	if got := stub.count.Load(); got != 0 {
		t.Errorf("count after Enqueue-past-Stop = %d, want 0", got)
	}
	if got := w.EnqueuedTotal(); got != 0 {
		t.Errorf("EnqueuedTotal after Enqueue-past-Stop = %d, want 0", got)
	}
}

// TestAgentLaunchedWorkerStopIdempotent verifies a second Stop is a no-op.
func TestAgentLaunchedWorkerStopIdempotent(t *testing.T) {
	t.Parallel()
	stub := &stubAgentLaunchedProcessor{}
	w := newAgentLaunchedWorker(stub, pkglogger.Get())
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

// TestAgentLaunchedWorkerNilProcessorShortCircuits verifies the worker
// is a cheap no-op when constructed without a processor.
func TestAgentLaunchedWorkerNilProcessorShortCircuits(t *testing.T) {
	t.Parallel()
	w := newAgentLaunchedWorker(nil, pkglogger.Get())
	w.Start()
	w.Enqueue("agent-1", newAgentLaunchedForWorker("agent-1", "thread-1", "uuid-1"))
	if err := w.Stop(context.Background()); err != nil {
		t.Fatalf("Stop = %v", err)
	}
}

// TestAgentLaunchedCallbackEnqueueOnly is the P22 P2 (thread S4)
// behavioral guard matching TestTaskHandoffCallbackEnqueueOnly /
// TestTeamSyncCallbackEnqueueOnly: onAgentLaunched must not invoke the
// processor synchronously on the dispatcher goroutine; every hit goes
// through the worker's Enqueue path.
func TestAgentLaunchedCallbackEnqueueOnly(t *testing.T) {
	t.Parallel()
	stub := &stubAgentLaunchedProcessor{block: make(chan struct{})}

	bindings := &eventBindingStore{binding: &bindingstore.Binding{AgentID: "agent-1"}}
	svc := &service{
		logger:       silentLogger(),
		bindingStore: bindings,
	}
	svc.agentLaunchedWorker = newAgentLaunchedWorker(stub, svc.logger)
	svc.agentLaunchedWorker.Start()
	defer func() {
		close(stub.block)
		_ = svc.agentLaunchedWorker.Stop(context.Background())
	}()

	done := make(chan struct{})
	go func() {
		svc.onAgentLaunched(newAgentLaunchedForWorker("agent-1", "thread-1", "uuid-1"))
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("onAgentLaunched blocked on synchronous processor; expected Enqueue-only")
	}

	if got := svc.agentLaunchedWorker.EnqueuedTotal(); got != 1 {
		t.Errorf("EnqueuedTotal after onAgentLaunched = %d, want 1", got)
	}
}
