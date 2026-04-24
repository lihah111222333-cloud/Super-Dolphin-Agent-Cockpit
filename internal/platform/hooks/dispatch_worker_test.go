package hooks

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	mcp "github.com/anthropic-ai/super-agent-v3/internal/dto/mcp"
	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
)

// fakeHookFanout records DispatchAfter calls so tests can assert that the
// worker (not the callback) is what reaches the Manager and that no
// goroutine leaks past Stop.
type fakeHookFanout struct {
	mu    sync.Mutex
	calls []hookDispatchRequest
	block chan struct{} // if non-nil, DispatchAfter blocks on receive
}

func (f *fakeHookFanout) DispatchAfter(_ context.Context, topic string, payload mcp.HookPayload) (mcp.AfterDecision, error) {
	if f.block != nil {
		<-f.block
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, hookDispatchRequest{topic: topic, payload: payload})
	return mcp.AfterDecision{}, nil
}

func (f *fakeHookFanout) Calls() []hookDispatchRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]hookDispatchRequest, len(f.calls))
	copy(out, f.calls)
	return out
}

// TestHookRelayDrainAfterShutdown is the P22 P2 TDD test named in
// docs/plans/迁移/p22/P2_BusRuntimeDecoupling.md:415.
//
// Contract: any dispatch request that was enqueued before Stop fires
// must reach DispatchAfter before Stop returns (bounded by ctx) — the
// P2 §验收 bullet "hooks relay 在 shutdown 后无残留 in-flight dispatch 越过
// stop" pins this down. A fire-and-forget `go func()` would not satisfy
// it because Stop could return before the goroutine finished.
func TestHookRelayDrainAfterShutdown(t *testing.T) {
	t.Parallel()

	fanout := &fakeHookFanout{}
	w := newHookDispatchWorker(fanout, pkglogger.Get())
	w.Start()

	payload := mcp.HookPayload{AgentID: "agent-A", ThreadID: "thread-A", Context: json.RawMessage(`{}`)}
	for i := 0; i < 8; i++ {
		w.Enqueue("session.start", time.Now(), payload)
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := w.Stop(ctx); err != nil {
		t.Fatalf("Stop() = %v, want nil", err)
	}

	if got := w.ProcessedTotal(); got != 8 {
		t.Fatalf("ProcessedTotal after drain = %d, want 8 (all enqueued must reach DispatchAfter)", got)
	}
	if got := len(fanout.Calls()); got != 8 {
		t.Fatalf("DispatchAfter call count after drain = %d, want 8", got)
	}
}

// TestHookDispatchWorkerEnqueueNonBlockingUnderSlowFanout checks the
// fire-and-forget replacement contract: even if DispatchAfter is pinned
// inside a slow peer, bus callback Enqueue must not block.
func TestHookDispatchWorkerEnqueueNonBlockingUnderSlowFanout(t *testing.T) {
	t.Parallel()

	block := make(chan struct{})
	fanout := &fakeHookFanout{block: block}
	w := newHookDispatchWorker(fanout, pkglogger.Get())
	w.Start()

	payload := mcp.HookPayload{AgentID: "agent-A", ThreadID: "thread-A", Context: json.RawMessage(`{}`)}
	enqueueDone := make(chan struct{})
	go func() {
		for i := 0; i < 32; i++ {
			w.Enqueue("session.start", time.Now(), payload)
		}
		close(enqueueDone)
	}()
	select {
	case <-enqueueDone:
	case <-time.After(time.Second):
		t.Fatalf("Enqueue blocked while DispatchAfter was stuck; pre-P2 fire-and-forget replacement must stay non-blocking")
	}
	if got := len(fanout.Calls()); got != 0 {
		t.Fatalf("DispatchAfter invoked %d times while fanout blocked; callback must never drive it", got)
	}
	if got := w.EnqueuedTotal(); got != 32 {
		t.Fatalf("EnqueuedTotal = %d, want 32 (lossless)", got)
	}

	close(block)
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if w.ProcessedTotal() >= 32 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if got := w.ProcessedTotal(); got != 32 {
		t.Errorf("ProcessedTotal after unblock = %d, want 32", got)
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_ = w.Stop(ctx)
}

// TestHookDispatchWorkerEnqueueAfterStopDrops documents the only drop
// path in the lossless contract: once Stop fires, further Enqueue is
// silently dropped. That's necessary because event_relay's cancel func
// runs immediately after Stop and the subscriptions stop firing anyway.
func TestHookDispatchWorkerEnqueueAfterStopDrops(t *testing.T) {
	t.Parallel()

	fanout := &fakeHookFanout{}
	w := newHookDispatchWorker(fanout, pkglogger.Get())
	w.Start()

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := w.Stop(ctx); err != nil {
		t.Fatalf("Stop() = %v, want nil", err)
	}

	payload := mcp.HookPayload{AgentID: "agent-A", ThreadID: "thread-A", Context: json.RawMessage(`{}`)}
	beforeEnq := w.EnqueuedTotal()
	w.Enqueue("session.start", time.Now(), payload)
	if got := w.EnqueuedTotal(); got != beforeEnq {
		t.Errorf("EnqueuedTotal after post-Stop enqueue = %d, want %d", got, beforeEnq)
	}
	if got := len(fanout.Calls()); got != 0 {
		t.Errorf("DispatchAfter invoked after Stop: %d, want 0", got)
	}
}

// TestHookDispatchWorkerPreservesFIFOOrder verifies that dispatch
// requests reach the fanout in enqueue order. That matters for hook
// observers that treat topics as a causal event stream.
func TestHookDispatchWorkerPreservesFIFOOrder(t *testing.T) {
	t.Parallel()

	fanout := &fakeHookFanout{}
	w := newHookDispatchWorker(fanout, pkglogger.Get())
	w.Start()
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = w.Stop(ctx)
	}()

	payload := mcp.HookPayload{AgentID: "agent-A", ThreadID: "thread-A", Context: json.RawMessage(`{}`)}
	topics := []string{"session.start", "turn.after", "turn.failed", "process.exit"}
	for _, topic := range topics {
		w.Enqueue(topic, time.Now(), payload)
	}

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if w.ProcessedTotal() >= int64(len(topics)) {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	calls := fanout.Calls()
	if len(calls) != len(topics) {
		t.Fatalf("DispatchAfter count = %d, want %d", len(calls), len(topics))
	}
	for i, call := range calls {
		if call.topic != topics[i] {
			t.Fatalf("calls[%d].topic = %q, want %q", i, call.topic, topics[i])
		}
	}
}

// TestEnqueueHookDispatchFiltersInvalidPayloads keeps the pre-P2 input-
// validation contract. Empty topic, empty agentID, or empty context must
// never reach the worker queue — the callback's cheap filter rejects them
// before spending a queue slot.
func TestEnqueueHookDispatchFiltersInvalidPayloads(t *testing.T) {
	t.Parallel()

	fanout := &fakeHookFanout{}
	w := newHookDispatchWorker(fanout, pkglogger.Get())
	w.Start()
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = w.Stop(ctx)
	}()

	validCtx := json.RawMessage(`{}`)
	cases := []struct {
		name    string
		topic   string
		payload mcp.HookPayload
	}{
		{"nil worker is a no-op", "session.start", mcp.HookPayload{AgentID: "a", Context: validCtx}},
		{"empty topic", "", mcp.HookPayload{AgentID: "a", Context: validCtx}},
		{"blank topic", "   ", mcp.HookPayload{AgentID: "a", Context: validCtx}},
		{"empty agent", "session.start", mcp.HookPayload{AgentID: "", Context: validCtx}},
		{"empty context", "session.start", mcp.HookPayload{AgentID: "a", Context: nil}},
	}

	// "nil worker is a no-op" is special — use a nil worker explicitly.
	enqueueHookDispatch(nil, cases[0].topic, time.Now(), cases[0].payload)
	for _, tc := range cases[1:] {
		enqueueHookDispatch(w, tc.topic, time.Now(), tc.payload)
	}

	time.Sleep(20 * time.Millisecond)
	if got := w.EnqueuedTotal(); got != 0 {
		t.Errorf("EnqueuedTotal after invalid enqueues = %d, want 0", got)
	}
	if got := len(fanout.Calls()); got != 0 {
		t.Errorf("DispatchAfter invoked for invalid payloads: %d, want 0", got)
	}
}
