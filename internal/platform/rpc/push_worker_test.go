package rpc

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/eventsurface"
	pkglogger "github.com/lihah111222333-cloud/super-dolphin-agent/pkg/logger"
)

// fakePushBroadcaster 记录 NotifyAll 调用，供测试区分通知是否由 worker 驱动。
// 这里还保存传入 context 的状态，用来确认 worker 使用的是可取消的 pushCtx。
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
	// 首次进入 NotifyAll 时通知测试协程，确认 worker 已经消费到该批通知。
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

func waitForNotifySent(t *testing.T, worker *pushNotificationWorker, want int64) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if worker.NotifySentTotal() >= want {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if got := worker.NotifySentTotal(); got != want {
		t.Fatalf("NotifySentTotal = %d, want %d", got, want)
	}
}

func callPayloadMap(t *testing.T, call fakePushCall) map[string]any {
	t.Helper()
	payload, ok := call.payload.(map[string]any)
	if !ok {
		t.Fatalf("payload type = %T, want map", call.payload)
	}
	return payload
}

func embeddedThreadPatch(t *testing.T, payload map[string]any) map[string]any {
	t.Helper()
	embedded, ok := payload["_threadPatch"].(map[string]any)
	if !ok {
		t.Fatalf("embedded patch = %#v, want map", payload["_threadPatch"])
	}
	return embedded
}

func assertNoEmbeddedThreadPatch(t *testing.T, call fakePushCall) {
	t.Helper()
	payload := callPayloadMap(t, call)
	if payload["_threadPatch"] != nil {
		t.Fatalf("unexpected embedded patch on %s: %#v", call.method, payload["_threadPatch"])
	}
}

func TestRPCPushWorkerEmbedsMatchingThreadPatchWithoutDroppingStandalone(t *testing.T) {
	broadcaster := &fakePushBroadcaster{}
	bridge := &PushBridge{logger: pkglogger.Get()}
	worker := newPushNotificationWorker(broadcaster, bridge, pkglogger.Get())
	sourcePayload := map[string]any{"threadId": "thread-1", "delta": "OK"}
	patchPayload := map[string]any{"threadId": "thread-1", "source": "turn/outputDelta", "sequence": 7}
	worker.Enqueue([]eventsurface.Notification{
		{Method: eventsurface.MethodAgentMessageDelta, Payload: sourcePayload},
	})
	worker.Enqueue([]eventsurface.Notification{
		{Method: eventsurface.MethodUIThreadPatch, Payload: patchPayload},
	})

	worker.Start()
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = worker.Stop(ctx)
	}()

	waitForNotifySent(t, worker, 2)
	calls := broadcaster.observed()
	if len(calls) != 2 {
		t.Fatalf("NotifyAll call count = %d, want source plus standalone patch", len(calls))
	}
	if calls[0].method != eventsurface.MethodAgentMessageDelta {
		t.Fatalf("method = %q, want source method", calls[0].method)
	}
	embedded := embeddedThreadPatch(t, callPayloadMap(t, calls[0]))
	if embedded["sequence"] != 7 {
		t.Fatalf("embedded sequence = %#v, want 7", embedded["sequence"])
	}
	if calls[1].method != eventsurface.MethodUIThreadPatch {
		t.Fatalf("second method = %q, want standalone patch for compatibility", calls[1].method)
	}
	if _, mutated := sourcePayload["_threadPatch"]; mutated {
		t.Fatal("source payload was mutated with _threadPatch")
	}
}

func TestRPCPushWorkerEmbedsThreadPatchFromSameBatchWithoutDroppingStandalone(t *testing.T) {
	broadcaster := &fakePushBroadcaster{}
	bridge := &PushBridge{logger: pkglogger.Get()}
	worker := newPushNotificationWorker(broadcaster, bridge, pkglogger.Get())
	worker.Enqueue([]eventsurface.Notification{
		{Method: eventsurface.MethodAgentMessageDelta, Payload: map[string]any{"threadId": "thread-1", "delta": "OK"}},
		{Method: eventsurface.MethodUIThreadPatch, Payload: map[string]any{"threadId": "thread-1", "source": "turn/outputDelta", "sequence": 8}},
	})

	worker.Start()
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = worker.Stop(ctx)
	}()

	waitForNotifySent(t, worker, 2)
	calls := broadcaster.observed()
	if len(calls) != 2 {
		t.Fatalf("NotifyAll call count = %d, want source plus standalone patch", len(calls))
	}
	embedded := embeddedThreadPatch(t, callPayloadMap(t, calls[0]))
	if embedded["sequence"] != 8 {
		t.Fatalf("embedded sequence = %#v, want 8", embedded["sequence"])
	}
	if calls[1].method != eventsurface.MethodUIThreadPatch {
		t.Fatalf("second method = %q, want standalone patch", calls[1].method)
	}
}

func TestRPCPushWorkerDoesNotEmbedThreadPatchIntoDifferentSourceMethod(t *testing.T) {
	broadcaster := &fakePushBroadcaster{}
	bridge := &PushBridge{logger: pkglogger.Get()}
	worker := newPushNotificationWorker(broadcaster, bridge, pkglogger.Get())
	worker.Enqueue([]eventsurface.Notification{
		{Method: eventsurface.MethodAgentStopped, Payload: map[string]any{"threadId": "thread-1", "status": "stopped"}},
	})
	worker.Enqueue([]eventsurface.Notification{
		{Method: eventsurface.MethodUIThreadPatch, Payload: map[string]any{"threadId": "thread-1", "source": "turn/outputDelta", "sequence": 10}},
	})

	worker.Start()
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = worker.Stop(ctx)
	}()

	waitForNotifySent(t, worker, 2)
	calls := broadcaster.observed()
	if len(calls) != 2 {
		t.Fatalf("NotifyAll call count = %d, want source and standalone patch", len(calls))
	}
	if calls[0].method != eventsurface.MethodAgentStopped {
		t.Fatalf("first method = %q, want source method", calls[0].method)
	}
	if payload, ok := calls[0].payload.(map[string]any); ok && payload["_threadPatch"] != nil {
		t.Fatalf("unrelated source carried embedded patch: %#v", payload["_threadPatch"])
	}
	if calls[1].method != eventsurface.MethodUIThreadPatch {
		t.Fatalf("second method = %q, want standalone patch", calls[1].method)
	}
}

// TestRPCPushQueuePreservesLegacyExpansion 锁定 push worker 的扩展通知顺序。
// worker 必须使用 Stop 可取消的 pushCtx，并把 source 通知及其兼容刷新通知按 expander 产出的顺序串行送达 NotifyAll。
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

	// 复用生产回调使用的扩展函数；如果兼容刷新语义漂移，测试会跟着暴露。
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

// TestRPCPushWorkerUsesCancelablePushCtx 锁定 worker 自有 context 的取消路径。
// Stop 必须取消 pushCtx，并让正在执行的 NotifyAll 及时观察到 context.Canceled。
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
	var wg sync.WaitGroup
	wg.Go(func() {
		time.Sleep(10 * time.Millisecond)
		close(broadcaster.block)
	})
	defer wg.Wait()
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

// TestRPCPushWorkerEnqueueNonBlocking 确认入队路径不被阻塞的 NotifyAll 拖住。
// 这保护 bus callback goroutine，不让慢推送反向卡住事件分发。
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
	var wg sync.WaitGroup
	wg.Go(func() {
		for range 32 {
			worker.Enqueue(payload)
		}
		close(done)
	})
	select {
	case <-done:
		wg.Wait()
	case <-time.After(time.Second):
		t.Fatal("Enqueue blocked while NotifyAll was stuck; callback path must stay non-blocking")
	}
	if got := worker.EnqueuedTotal(); got != 32 {
		t.Errorf("EnqueuedTotal = %d, want 32", got)
	}
}

// TestRPCPushWorkerOverflowKeepsLatestAndEmitsDegradedEvent 锁定 push 队列满载时的显式退化信号。
// worker 正在发送慢通知时，后续爆量刷新只能保留最后一条业务通知，并向前端发出一次 degraded 事件。
func TestRPCPushWorkerOverflowKeepsLatestAndEmitsDegradedEvent(t *testing.T) {
	t.Parallel()

	broadcaster := &fakePushBroadcaster{
		entered: make(chan struct{}),
		block:   make(chan struct{}),
	}
	bridge := &PushBridge{logger: pkglogger.Get()}
	worker := newPushNotificationWorker(broadcaster, bridge, pkglogger.Get())
	worker.Start()
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = worker.Stop(ctx)
	}()

	worker.Enqueue([]eventsurface.Notification{{
		Method: eventsurface.MethodThreadStarted,
		Payload: map[string]any{
			"threadId": "thread-0",
			"sequence": 0,
		},
	}})
	select {
	case <-broadcaster.entered:
	case <-time.After(time.Second):
		t.Fatal("NotifyAll did not enter within 1s")
	}

	// 发送 pushWorkerPendingLimit+2 条，保证超出队列容量触发退化。
	const overflowCount = pushWorkerPendingLimit + 2
	for i := 1; i <= overflowCount; i++ {
		worker.Enqueue([]eventsurface.Notification{{
			Method: eventsurface.MethodThreadStarted,
			Payload: map[string]any{
				"threadId": "thread-overflow",
				"sequence": i,
			},
		}})
	}
	close(broadcaster.block)

	waitForNotifySent(t, worker, 3)
	calls := broadcaster.observed()
	if len(calls) != 3 {
		t.Fatalf("NotifyAll call count = %d, want initial + degraded + latest; calls=%#v", len(calls), calls)
	}
	if calls[1].method != "platform/queue/degraded" {
		t.Fatalf("overflow method = %q, want platform/queue/degraded", calls[1].method)
	}
	degraded := callPayloadMap(t, calls[1])
	if degraded["queue"] != "rpc_push" {
		t.Fatalf("degraded queue = %#v, want rpc_push", degraded["queue"])
	}
	if degraded["dropped"] == nil {
		t.Fatalf("degraded payload missing dropped count: %#v", degraded)
	}
	latest := callPayloadMap(t, calls[2])
	if latest["sequence"] != overflowCount {
		t.Fatalf("latest sequence = %#v, want %d", latest["sequence"], overflowCount)
	}
}

// TestRPCPushWorkerEmptyNotificationDropped 锁定 callback 侧的廉价过滤。
// 空批次或 method 全为空白的批次不能进入队列，避免 worker 消耗无意义任务。
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
