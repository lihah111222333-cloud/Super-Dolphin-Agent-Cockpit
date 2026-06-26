package memory

import (
	"context"
	"sync"
	"testing"
	"time"

	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
)

// fakeNestedIngestRuntime 记录 AddToolReadResult 调用。
// 测试用它区分“bus 回调只入队”和“worker 才真正写入运行时”的并发边界。
type fakeNestedIngestRuntime struct {
	mu      sync.Mutex
	calls   []nestedIngestRequest
	block   chan struct{} // 非 nil 时每次 AddToolReadResult 都会阻塞在该通道。
	started chan struct{} // 可选的一次性信号，表示 AddToolReadResult 已被进入。
	once    sync.Once
}

func (f *fakeNestedIngestRuntime) AddToolReadResult(threadID, toolName, result, persistedPath string) {
	if f.started != nil {
		f.once.Do(func() { close(f.started) })
	}
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

// TestNestedToolReadIngestEnqueueOnly 验证工具读取结果只由后台 worker 写入运行时。
// bus 回调只能快速入队，不能被慢速持久化或运行时写入阻塞。
//
// 该测试同时覆盖三条安全边界：
//
//  1. AddToolReadResult 被慢 I/O 卡住时，Enqueue 仍不能阻塞。
//  2. AddToolReadResult 只能由 worker 调用，不能由回调路径直接调用。
//  3. 相同 thread/tool/persistedPath 的重复入队会合并为最后一次请求。
func TestNestedToolReadIngestEnqueueOnly(t *testing.T) {
	t.Parallel()

	// 边界 1：AddToolReadResult 阻塞时，Enqueue 仍必须快速返回。
	block := make(chan struct{})
	rt := &fakeNestedIngestRuntime{block: block, started: make(chan struct{})}
	w := newNestedIngestWorker(rt, pkglogger.Get())
	w.Start()
	w.Enqueue("thread-A", "Read", "payload", "/tmp/blocked")
	waitNestedWorkerStarted(t, rt)
	enqueueNestedIngestBurst(t, w)

	// worker 仍被阻塞时，不应已有调用落到 runtime。
	if got := len(rt.Calls()); got != 0 {
		t.Fatalf("AddToolReadResult invoked %d times while worker was blocked; bus callback must not drive it", got)
	}

	// 边界 3：相同 key 的重复入队需要合并。
	assertNestedIngestCoalesced(t, w)

	// 边界 2：解除阻塞后由 worker 推进 AddToolReadResult。
	close(block)
	waitNestedIngestProcessed(t, w)
	if got := len(rt.Calls()); got != 2 {
		t.Fatalf("AddToolReadResult call count after drain = %d, want 2 (blocked + coalesced)", got)
	}
	stopNestedIngestWorker(t, w)
}

func waitNestedWorkerStarted(t *testing.T, rt *fakeNestedIngestRuntime) {
	t.Helper()
	select {
	case <-rt.started:
	case <-time.After(time.Second):
		t.Fatal("worker did not enter AddToolReadResult")
	}
}

func enqueueNestedIngestBurst(t *testing.T, w *nestedIngestWorker) {
	t.Helper()
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
}

func assertNestedIngestCoalesced(t *testing.T, w *nestedIngestWorker) {
	t.Helper()
	if enq := w.EnqueuedTotal(); enq != 17 {
		t.Fatalf("EnqueuedTotal = %d, want 17", enq)
	}
	if coal := w.CoalescedTotal(); coal != 15 {
		t.Fatalf("CoalescedTotal = %d, want 15 (16 events - 1 distinct key)", coal)
	}
}

func waitNestedIngestProcessed(t *testing.T, w *nestedIngestWorker) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if w.ProcessedTotal() >= 2 {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("ProcessedTotal = %d, want >= 2 after unblocking runtime", w.ProcessedTotal())
}

func stopNestedIngestWorker(t *testing.T, w *nestedIngestWorker) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := w.Stop(ctx); err != nil {
		t.Fatalf("Stop() = %v, want nil", err)
	}
}

// TestNestedIngestWorkerEnqueueAfterStopDrops 验证 Stop 后的入队门禁。
// Stop 触发后 Enqueue 不再进入缓冲，避免已取消的 bus 订阅和延迟写入发生竞态。
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

// TestNestedIngestWorkerStopDrainsPending 验证 Stop 前已入队请求必须被排空。
// Stop 返回前会在 ctx 期限内把 pending 请求交给 AddToolReadResult，避免关闭时丢失在途结果。
func TestNestedIngestWorkerStopDrainsPending(t *testing.T) {
	t.Parallel()

	rt := &fakeNestedIngestRuntime{}
	w := newNestedIngestWorker(rt, pkglogger.Get())
	// 故意不启动 worker goroutine；Stop 仍要排空 pending，而不是无限等待 doneCh。

	for i := 0; i < 3; i++ {
		w.Enqueue("thread-drain", "Read", "payload", "/tmp/file") // 相同 key 只保留 1 个 pending。
	}
	w.Enqueue("thread-drain", "Read", "payload-other", "/tmp/other") // 不同 key 额外保留 1 个 pending。

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

// TestNestedIngestWorkerStartAfterStopIsNoop 锁定先 Stop 后 Start 的生命周期边界。
// doneCh 一旦被 Stop 关闭，后续 Start 必须保持无操作，避免再次启动 worker 并重复关闭通道。
func TestNestedIngestWorkerStartAfterStopIsNoop(t *testing.T) {
	t.Parallel()

	rt := &fakeNestedIngestRuntime{}
	w := newNestedIngestWorker(rt, pkglogger.Get())
	w.Enqueue("thread-drain", "Read", "payload", "/tmp/file")

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := w.Stop(ctx); err != nil {
		t.Fatalf("Stop() = %v, want nil", err)
	}

	w.Start()
	if w.started.Load() {
		t.Fatal("Start after Stop marked worker as started; want no-op")
	}
	w.Enqueue("thread-after-stop", "Read", "payload", "/tmp/after")
	time.Sleep(20 * time.Millisecond)

	if got := len(rt.Calls()); got != 1 {
		t.Fatalf("AddToolReadResult call count after Start-after-Stop = %d, want 1", got)
	}
}

// TestNestedIngestWorkerBlankThreadIDIsNoop 验证空 threadID 不进入队列。
// 空白输入不会计入 enqueued 或 dropped，避免生成无法关联线程的嵌套记忆结果。
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
