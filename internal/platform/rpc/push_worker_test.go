package rpc

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/anthropic-ai/super-agent-v3/internal/platform/eventsurface"
	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
)

// fakePushBroadcaster records NotifyAll calls so tests can assert the
// worker (not the bus callback) drove them and that the ctx the worker
// passes through is the cancellable pushCtx.
type fakePushBroadcaster struct {
	mu      sync.Mutex
	calls   []fakePushCall
	entered chan struct{}
	block   chan struct{}
	exited  chan error
}

type fakePushCall struct {
	method  string
	payload any
	ctxErr  error
}

func (f *fakePushBroadcaster) NotifyAll(ctx context.Context, _ *PushBridge, method string, params any) {
	// Signal "notify entered" exactly once so tests can synchronise on
	// the worker having consumed the batch.
	if f.entered != nil {
		select {
		case <-f.entered:
		default:
			close(f.entered)
		}
	}
	if f.block != nil {
		<-f.block
	}
	if f.exited != nil {
		select {
		case f.exited <- ctx.Err():
		default:
		}
	}
	f.mu.Lock()
	f.calls = append(f.calls, fakePushCall{
		method:  method,
		payload: params,
		ctxErr:  ctx.Err(),
	})
	f.mu.Unlock()
}

func (f *fakePushBroadcaster) observed() []fakePushCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]fakePushCall, len(f.calls))
	copy(out, f.calls)
	return out
}

// TestRPCPushQueuePreservesLegacyExpansion is the P22 P2 §TDD test named
// in docs/plans/迁移/p22/P2_BusRuntimeDecoupling.md:415.
//
// Contract (dual invariant):
//   1. The worker must use the cancellable pushCtx passed by Stop, not
//      `context.Background()` as the pre-P2 callback path did.
//   2. Legacy expansion semantics (thread/started → thread/started +
//      ui/thread/changed + ui/sidebar/changed) must arrive at NotifyAll
//      in the exact order the expander produced them. The worker batches
//      one push-request per bus event and drains it serially, so the
//      source notification and its legacy-refresh companions must stay
//      together + in-order.
func TestRPCPushQueuePreservesLegacyExpansion(t *testing.T) {
	t.Parallel()

	broadcaster := &fakePushBroadcaster{}
	bridge := &PushBridge{logger: pkglogger.Get()}
	worker := newPushNotificationWorker(broadcaster, bridge, pkglogger.Get())
	worker.Start()
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = worker.Stop(ctx)
	}()

	// Drive the same expansion the production callback uses. Using
	// ExpandNotifications here (instead of hard-coding a 3-element slice)
	// means if the legacy expander semantics ever drift, this test flags
	// it too.
	expanded := eventsurface.ExpandNotifications(eventsurface.MethodThreadStarted, map[string]any{
		"thread_id": "thread-A",
	})
	if len(expanded) != 3 {
		t.Fatalf("ExpandNotifications returned %d notifications, want 3 (source + ui/thread/changed + ui/sidebar/changed)", len(expanded))
	}

	worker.Enqueue(expanded)

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if worker.NotifySentTotal() >= 3 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if got := worker.NotifySentTotal(); got != 3 {
		t.Fatalf("NotifySentTotal = %d, want 3", got)
	}

	calls := broadcaster.observed()
	if len(calls) != 3 {
		t.Fatalf("NotifyAll call count = %d, want 3", len(calls))
	}
	wantMethods := []string{
		eventsurface.MethodThreadStarted,
		eventsurface.MethodUIThreadChanged,
		eventsurface.MethodUISidebarChanged,
	}
	for i, want := range wantMethods {
		if calls[i].method != want {
			t.Errorf("calls[%d].method = %q, want %q (legacy expansion order must be preserved through the worker)", i, calls[i].method, want)
		}
		if calls[i].ctxErr != nil {
			t.Errorf("calls[%d].ctxErr = %v, want nil (pre-Stop pushCtx should be live)", i, calls[i].ctxErr)
		}
	}
}

// TestRPCPushWorkerUsesCancelablePushCtx pins the other half of
// `rpc push 不再以 context.Background() 旁路 publish/shutdown cancel`
// (P2:438): Stop must cancel pushCtx and make an in-flight NotifyAll
// observe ctx.Err() == context.Canceled promptly.
func TestRPCPushWorkerUsesCancelablePushCtx(t *testing.T) {
	t.Parallel()

	broadcaster := &fakePushBroadcaster{
		entered: make(chan struct{}),
		block:   make(chan struct{}),
		exited:  make(chan error, 1),
	}
	bridge := &PushBridge{logger: pkglogger.Get()}
	worker := newPushNotificationWorker(broadcaster, bridge, pkglogger.Get())
	worker.Start()

	if worker.PushCtx() == context.Background() {
		t.Fatal("worker.PushCtx() is context.Background(); must be worker-owned cancellable ctx")
	}

	worker.Enqueue([]eventsurface.Notification{
		{Method: "thread/started", Payload: map[string]any{"thread_id": "T"}},
	})
	select {
	case <-broadcaster.entered:
	case <-time.After(time.Second):
		t.Fatal("NotifyAll did not enter within 1s; worker dispatch broken")
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	go func() {
		time.Sleep(10 * time.Millisecond)
		close(broadcaster.block)
	}()
	start := time.Now()
	if err := worker.Stop(shutdownCtx); err != nil {
		t.Fatalf("Stop() err = %v, want nil (drain must finish within ctx budget)", err)
	}
	if elapsed := time.Since(start); elapsed > 250*time.Millisecond {
		t.Errorf("Stop took %s, want <250ms — pushCtx cancel should propagate immediately", elapsed)
	}

	select {
	case ctxErr := <-broadcaster.exited:
		if !errors.Is(ctxErr, context.Canceled) {
			t.Errorf("broadcaster observed ctx.Err() = %v, want context.Canceled", ctxErr)
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("broadcaster did not publish its ctx.Err() after Stop")
	}
}

// TestRPCPushWorkerEnqueueNonBlocking mirrors the other P2 workers: a
// blocked NotifyAll must not pin the dispatcher callback goroutine.
func TestRPCPushWorkerEnqueueNonBlocking(t *testing.T) {
	t.Parallel()

	broadcaster := &fakePushBroadcaster{
		entered: make(chan struct{}),
		block:   make(chan struct{}),
	}
	bridge := &PushBridge{logger: pkglogger.Get()}
	worker := newPushNotificationWorker(broadcaster, bridge, pkglogger.Get())
	worker.Start()
	defer func() {
		close(broadcaster.block)
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = worker.Stop(ctx)
	}()

	payload := []eventsurface.Notification{{Method: "thread/started", Payload: map[string]any{"thread_id": "T"}}}
	done := make(chan struct{})
	go func() {
		for i := 0; i < 32; i++ {
			worker.Enqueue(payload)
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Enqueue blocked while NotifyAll was stuck; callback path must stay non-blocking")
	}
	if got := worker.EnqueuedTotal(); got != 32 {
		t.Errorf("EnqueuedTotal = %d, want 32", got)
	}
}

// TestRPCPushWorkerEmptyNotificationDropped verifies the cheap callback-
// side filters: empty batch and batches whose entries all have blank
// methods never reach the queue.
func TestRPCPushWorkerEmptyNotificationDropped(t *testing.T) {
	t.Parallel()

	broadcaster := &fakePushBroadcaster{}
	bridge := &PushBridge{logger: pkglogger.Get()}
	worker := newPushNotificationWorker(broadcaster, bridge, pkglogger.Get())
	worker.Start()
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = worker.Stop(ctx)
	}()

	worker.Enqueue(nil)
	worker.Enqueue([]eventsurface.Notification{})
	worker.Enqueue([]eventsurface.Notification{{Method: "   "}, {Method: ""}})

	time.Sleep(20 * time.Millisecond)
	if got := worker.EnqueuedTotal(); got != 0 {
		t.Errorf("EnqueuedTotal after blank batches = %d, want 0", got)
	}
	if got := len(broadcaster.observed()); got != 0 {
		t.Errorf("NotifyAll invoked for blank batches: %d, want 0", got)
	}
}
