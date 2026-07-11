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

// stubAgentLaunchedProcessor 是测试用 agentLaunchedProcessor。
// 它记录所有已处理事件，并可通过 block channel 人为卡住处理过程以观察 drain 顺序。
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

// TestAgentLaunchedWorkerProcessesEnqueuedEvent 验证入队事件会经 worker 异步派发。
// 收到的事件必须保持 agent/session 身份不变。
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

// TestAgentLaunchedWorkerCoalescesSameKey 验证同 agentID 的积压事件会合并。
// worker 正在处理首个事件时，后续同 key 事件只保留最后一次，避免重复启动恢复流程。
func TestAgentLaunchedWorkerCoalescesSameKey(t *testing.T) {
	t.Parallel()
	stub := &stubAgentLaunchedProcessor{block: make(chan struct{})}
	w := newAgentLaunchedWorker(stub, pkglogger.Get())
	w.Start()
	defer func() {
		// 解除被阻塞的 processor 调用，确保 Stop 能正常返回。
		var unblockWG sync.WaitGroup
		unblockWG.Go(func() {
			for {
				select {
				case stub.block <- struct{}{}:
				default:
					return
				}
			}
		})
		_ = w.Stop(context.Background())
		unblockWG.Wait()
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

// TestAgentLaunchedWorkerStopDrainsPending 验证 Stop 退出前会处理待办条目。
// 这是关闭路径的可见边界，不能让已入队的 agent launched 事件静默丢失。
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

// TestAgentLaunchedWorkerEnqueueAfterStopDrops 验证关闭后的入队会被闸门丢弃。
// 丢弃不增加 EnqueuedTotal，避免监控把关闭后的调用误计入待处理工作。
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

// TestAgentLaunchedWorkerStopIdempotent 验证 Stop 可重复调用。
// 第二次 Stop 必须快速返回，避免 shutdown 聚合路径被已关闭 worker 卡住。
func TestAgentLaunchedWorkerStopIdempotent(t *testing.T) {
	t.Parallel()
	stub := &stubAgentLaunchedProcessor{}
	w := newAgentLaunchedWorker(stub, pkglogger.Get())
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

// TestAgentLaunchedWorkerNilProcessorShortCircuits 验证缺少 processor 时 worker 只是轻量空操作。
// 该路径用于可选回调未接入的测试和降级环境，不能 panic 或阻塞 Stop。
func TestAgentLaunchedWorkerNilProcessorShortCircuits(t *testing.T) {
	t.Parallel()
	w := newAgentLaunchedWorker(nil, pkglogger.Get())
	w.Start()
	w.Enqueue("agent-1", newAgentLaunchedForWorker("agent-1", "thread-1", "uuid-1"))
	if err := w.Stop(context.Background()); err != nil {
		t.Fatalf("Stop = %v", err)
	}
}

// TestAgentLaunchedCallbackEnqueueOnly 固定 onAgentLaunched 的 enqueue-only 行为。
// 事件分发 goroutine 不能同步执行 processor；所有命中都必须进入 worker 队列，避免被业务处理阻塞。
func TestAgentLaunchedCallbackEnqueueOnly(t *testing.T) {
	t.Parallel()
	stub := &stubAgentLaunchedProcessor{block: make(chan struct{})}

	bindings := &eventBindingStore{binding: &BindingRecord{AgentID: "agent-1"}}
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
	var wg sync.WaitGroup
	wg.Go(func() {
		svc.onAgentLaunched(newAgentLaunchedForWorker("agent-1", "thread-1", "uuid-1"))
		close(done)
	})

	select {
	case <-done:
		wg.Wait()
	case <-time.After(2 * time.Second):
		t.Fatal("onAgentLaunched blocked on synchronous processor; expected Enqueue-only")
	}

	if got := svc.agentLaunchedWorker.EnqueuedTotal(); got != 1 {
		t.Errorf("EnqueuedTotal after onAgentLaunched = %d, want 1", got)
	}
}
