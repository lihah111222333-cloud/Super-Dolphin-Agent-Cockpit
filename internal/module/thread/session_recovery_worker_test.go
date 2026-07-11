package thread

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	agentdto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/agent"
	sharedto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/shared"
	pkglogger "github.com/lihah111222333-cloud/super-dolphin-agent/pkg/logger"
)

// stubSessionRecoverer 是测试用 sessionRecoverer，记录已处理事件并可阻塞到 ctx 取消。
type stubSessionRecoverer struct {
	mu         sync.Mutex
	calls      []agentdto.AgentFailed
	block      chan struct{} // 非 nil 时 processSessionRecovery 等待 block 或 ctx 结束
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

// TestSessionRecoveryWorkerProcessesEnqueuedEvent 验证单次 Enqueue 会进入受跟踪 goroutine 并调用恢复处理器。
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

// TestSessionRecoveryWorkerCoalescesSameTarget 验证同一目标的连续事件会合并为一次恢复处理。
// 限流窗口内只执行一轮恢复，避免同一 thread 的失败风暴放大并发工作量。
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

// TestSessionRecoveryWorkerDispatchesParallelForDifferentTargets 验证不同目标的失败可并行恢复。
// worker 不能把不同 thread 串行化到单一循环里，否则一个阻塞恢复会拖住其它目标。
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

	// 两个事件都应进入 processSessionRecovery 并阻塞在 stub 上；
	// 若 worker 串行执行，解除第一个阻塞前只能观察到一次调用。
	waitForSessionRecoveryCount(t, stub, 2, 2*time.Second)
}

// TestSessionRecoveryWorkerStopCancelsCtx 验证 Stop 会取消 worker ctx 并打断正在阻塞的恢复处理。
func TestSessionRecoveryWorkerStopCancelsCtx(t *testing.T) {
	t.Parallel()
	stub := &stubSessionRecoverer{block: make(chan struct{})}
	defer close(stub.block)

	w := newSessionRecoveryWorker(stub, pkglogger.Get())
	w.Start()

	w.Enqueue("thread-1", newAgentFailedForWorker("agent-1", "thread-1", true))
	waitForSessionRecoveryCount(t, stub, 1, 2*time.Second)

	// Stop 会取消 w.ctx，阻塞中的 stub 观察到取消后返回；
	// inflight.Wait 随后完成，Stop 不应等待完整恢复超时。
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

// TestSessionRecoveryWorkerEnqueueAfterStopDrops 验证 Stop 后的 Enqueue 会被门控丢弃。
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

// TestSessionRecoveryWorkerStopIdempotent 验证重复 Stop 不会阻塞或重复关闭资源。
func TestSessionRecoveryWorkerStopIdempotent(t *testing.T) {
	t.Parallel()
	stub := &stubSessionRecoverer{}
	w := newSessionRecoveryWorker(stub, pkglogger.Get())
	w.Start()
	if err := w.Stop(context.Background()); err != nil {
		t.Fatalf("first Stop = %v", err)
	}
	done := make(chan struct{})
	var wg sync.WaitGroup
	wg.Go(func() {
		_ = w.Stop(context.Background())
		close(done)
	})
	select {
	case <-done:
		wg.Wait()
	case <-time.After(time.Second):
		t.Fatal("second Stop did not return")
	}
}

// TestSessionRecoveryWorkerNilRecovererShortCircuits 验证未配置 recoverer 时保持廉价空操作。
func TestSessionRecoveryWorkerNilRecovererShortCircuits(t *testing.T) {
	t.Parallel()
	w := newSessionRecoveryWorker(nil, pkglogger.Get())
	w.Start()
	w.Enqueue("thread-1", newAgentFailedForWorker("agent-1", "thread-1", true))
	if err := w.Stop(context.Background()); err != nil {
		t.Fatalf("Stop = %v", err)
	}
}

// TestAgentFailedCallbackEnqueueOnly 验证 onAgentFailed 只负责投递恢复事件。
// 回调 goroutine 不能执行重连等待或同步恢复，每次命中都必须进入 worker 的 Enqueue 路径。
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
	var wg sync.WaitGroup
	wg.Go(func() {
		svc.onAgentFailed(newAgentFailedForWorker("agent-1", "thread-1", true))
		close(done)
	})

	select {
	case <-done:
		wg.Wait()
	case <-time.After(2 * time.Second):
		t.Fatal("onAgentFailed blocked on synchronous recovery; expected Enqueue-only")
	}

	if got := svc.sessionRecoveryWorker.EnqueuedTotal(); got != 1 {
		t.Errorf("EnqueuedTotal after onAgentFailed = %d, want 1", got)
	}
}

// TestAgentFailedCallbackDropsNonRecoverable 验证不可恢复事件在回调内快速返回。
// Recoverable=false 时不能进入 Enqueue，避免无意义的后台恢复任务。
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
