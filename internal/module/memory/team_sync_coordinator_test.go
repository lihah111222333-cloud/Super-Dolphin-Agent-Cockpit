package memory

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	threaddto "github.com/anthropic-ai/super-agent-v3/internal/dto/thread"
	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
)

// fakeTeamSyncLifecycle 是测试用的最小 teampkg.Lifecycle。
// startBlock 可把 worker 固定在 StartSession 内，便于断言总线回调只入队、不共享慢路径 goroutine。
type fakeTeamSyncLifecycle struct {
	mu         sync.Mutex
	starts     []threadStartRecord
	stops      []string
	startBlock chan struct{}
}

type threadStartRecord struct {
	threadID string
	buildCtx contract.BuildCtx
}

func (f *fakeTeamSyncLifecycle) StartSession(_ context.Context, threadID string, buildCtx contract.BuildCtx) error {
	if f.startBlock != nil {
		<-f.startBlock
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.starts = append(f.starts, threadStartRecord{threadID: threadID, buildCtx: buildCtx})
	return nil
}

func (f *fakeTeamSyncLifecycle) StopSession(_ context.Context, threadID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.stops = append(f.stops, threadID)
	return nil
}

func (f *fakeTeamSyncLifecycle) StartCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.starts)
}

func (f *fakeTeamSyncLifecycle) StopIDs() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]string, len(f.stops))
	copy(out, f.stops)
	return out
}

// TestTeamSyncCallbackEnqueueOnly 固定 StartSession 慢路径来验证两个当前不变量：
// EnqueueStart/EnqueueStop 在突发调用时不能阻塞总线回调；worker 仍必须串行执行，
// 因此 Stop 操作要排在被卡住的 Start 后面。
func TestTeamSyncCallbackEnqueueOnly(t *testing.T) {
	t.Parallel()

	block := make(chan struct{})
	svc := &fakeTeamSyncLifecycle{startBlock: block}
	c := newTeamSyncCoordinator(svc, nil, pkglogger.Get())
	c.Start()

	enqueueDone := make(chan struct{})
	go enqueueTeamSyncBurst(c, enqueueDone)
	assertTeamSyncEnqueueCompletes(t, enqueueDone)
	assertTeamSyncWorkerBlocked(t, svc)
	if enq := c.EnqueuedTotal(); enq != 33 { // 1 + 16*(Start+Stop)
		t.Fatalf("EnqueuedTotal = %d, want 33", enq)
	}

	close(block)
	waitForTeamSyncProcessed(t, c, 33, 2*time.Second)
	assertTeamSyncLifecycleTotals(t, svc, 17, 16)

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := c.Stop(ctx); err != nil {
		t.Fatalf("Stop() = %v, want nil", err)
	}
}

func enqueueTeamSyncBurst(c *teamSyncCoordinator, done chan<- struct{}) {
	c.EnqueueStart(threaddto.Started{ThreadID: "thread-A", CWD: "/tmp"})
	for i := 0; i < 16; i++ {
		c.EnqueueStart(threaddto.Started{ThreadID: "thread-burst", CWD: "/tmp"})
		c.EnqueueStop(threaddto.Stopped{ThreadID: "thread-burst"})
	}
	close(done)
}

func assertTeamSyncEnqueueCompletes(t *testing.T, done <-chan struct{}) {
	t.Helper()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatalf("Enqueue blocked while StartSession was pinned; bus callback must never share that goroutine")
	}
}

func assertTeamSyncWorkerBlocked(t *testing.T, svc *fakeTeamSyncLifecycle) {
	t.Helper()
	if got := svc.StartCount(); got != 0 {
		t.Fatalf("StartSession invoked %d times while worker was blocked; callback must not drive the lifecycle", got)
	}
	if got := len(svc.StopIDs()); got != 0 {
		t.Fatalf("StopSession invoked %d times while worker was blocked; worker must be strictly serial", got)
	}
}

func waitForTeamSyncProcessed(t *testing.T, c *teamSyncCoordinator, want int64, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if c.ProcessedTotal() >= want {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if got := c.ProcessedTotal(); got != want {
		t.Fatalf("ProcessedTotal after drain = %d, want %d", got, want)
	}
}

func assertTeamSyncLifecycleTotals(t *testing.T, svc *fakeTeamSyncLifecycle, wantStarts, wantStops int) {
	t.Helper()
	// 已入队的操作必须全部到达 lifecycle，验证 Stop 前的队列不丢事件。
	if got := svc.StartCount(); got != wantStarts {
		t.Errorf("StartSession total = %d, want %d (lossless)", got, wantStarts)
	}
	if got := len(svc.StopIDs()); got != wantStops {
		t.Errorf("StopSession total = %d, want %d (lossless)", got, wantStops)
	}
}

// TestTeamSyncCoordinatorEnqueueAfterStopDrops 验证 Stop 后的 Enqueue 不再进入队列。
// 这与 auto-dream scheduler 和 nested ingest worker 的关闭门控一致，防止关闭后积压新任务。
func TestTeamSyncCoordinatorEnqueueAfterStopDrops(t *testing.T) {
	t.Parallel()

	svc := &fakeTeamSyncLifecycle{}
	c := newTeamSyncCoordinator(svc, nil, pkglogger.Get())
	c.Start()

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := c.Stop(ctx); err != nil {
		t.Fatalf("Stop() = %v, want nil", err)
	}

	beforeEnq := c.EnqueuedTotal()
	c.EnqueueStart(threaddto.Started{ThreadID: "thread-post-stop", CWD: "/tmp"})
	c.EnqueueStop(threaddto.Stopped{ThreadID: "thread-post-stop"})
	if got := c.EnqueuedTotal(); got != beforeEnq {
		t.Errorf("EnqueuedTotal after post-Stop enqueue = %d, want %d", got, beforeEnq)
	}
	if got := svc.StartCount(); got != 0 {
		t.Errorf("StartSession invoked after Stop: %d, want 0", got)
	}
	if got := len(svc.StopIDs()); got != 0 {
		t.Errorf("StopSession invoked after Stop: %d, want 0", got)
	}
}

// TestTeamSyncCoordinatorPreservesFIFOOrder 验证 lifecycle 按入队顺序收到操作。
// TeamSyncService 的 runtime 切换和最终 flush 依赖 Start-before-Stop 顺序，coordinator 不能重排。
func TestTeamSyncCoordinatorPreservesFIFOOrder(t *testing.T) {
	t.Parallel()

	svc := &fakeTeamSyncLifecycle{}
	c := newTeamSyncCoordinator(svc, nil, pkglogger.Get())
	c.Start()
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = c.Stop(ctx)
	}()

	c.EnqueueStart(threaddto.Started{ThreadID: "thread-1", CWD: "/tmp/one"})
	c.EnqueueStart(threaddto.Started{ThreadID: "thread-2", CWD: "/tmp/two"})
	c.EnqueueStop(threaddto.Stopped{ThreadID: "thread-1"})
	c.EnqueueStop(threaddto.Stopped{ThreadID: "thread-2"})

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if c.ProcessedTotal() >= 4 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if got := c.ProcessedTotal(); got != 4 {
		t.Fatalf("ProcessedTotal = %d, want 4", got)
	}

	svc.mu.Lock()
	starts := append([]threadStartRecord(nil), svc.starts...)
	stops := append([]string(nil), svc.stops...)
	svc.mu.Unlock()

	wantStarts := []string{"thread-1", "thread-2"}
	for i, want := range wantStarts {
		if i >= len(starts) || starts[i].threadID != want {
			t.Fatalf("starts[%d] = %+v, want thread_id=%q", i, starts, want)
		}
	}
	wantStops := []string{"thread-1", "thread-2"}
	for i, want := range wantStops {
		if i >= len(stops) || stops[i] != want {
			t.Fatalf("stops[%d] = %+v, want thread_id=%q", i, stops, want)
		}
	}
}

// TestTeamSyncCoordinatorBlankThreadIDIsNoop 验证空 threadID 会在入队前短路。
// 空输入不能增加 EnqueuedTotal，也不能触达 lifecycle。
func TestTeamSyncCoordinatorBlankThreadIDIsNoop(t *testing.T) {
	t.Parallel()

	svc := &fakeTeamSyncLifecycle{}
	c := newTeamSyncCoordinator(svc, nil, pkglogger.Get())
	c.Start()
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
		defer cancel()
		_ = c.Stop(ctx)
	}()

	c.EnqueueStart(threaddto.Started{ThreadID: ""})
	c.EnqueueStart(threaddto.Started{ThreadID: "   "})
	c.EnqueueStop(threaddto.Stopped{ThreadID: ""})

	time.Sleep(20 * time.Millisecond)
	if got := c.EnqueuedTotal(); got != 0 {
		t.Errorf("EnqueuedTotal after blank enqueues = %d, want 0", got)
	}
	if got := svc.StartCount(); got != 0 {
		t.Errorf("StartSession invoked for blank threadID: %d, want 0", got)
	}
}

// TestTeamSyncCoordinatorStopDrainsPending 验证 Stop 会排空已入队但尚未处理的操作。
// 即使 wake 路径还没处理，stopCh 分支也必须在 Stop 返回前完成 drain。
func TestTeamSyncCoordinatorStopDrainsPending(t *testing.T) {
	t.Parallel()

	// 构造一个会卡住首个 Start 的 lifecycle，便于稳定制造后续待处理队列。
	block := make(chan struct{})
	svc := &fakeTeamSyncLifecycle{startBlock: block}
	c := newTeamSyncCoordinator(svc, nil, pkglogger.Get())
	c.Start()

	// 先让 worker 拿到一个 Start，再把更多操作排在后面。
	// 外部无法直接观察 worker 是否已进入 StartSession；EnqueuedTotal 达到 1 足以证明 wake 已触发。
	c.EnqueueStart(threaddto.Started{ThreadID: "thread-in-flight", CWD: "/tmp"})
	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		if c.EnqueuedTotal() >= 1 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	c.EnqueueStart(threaddto.Started{ThreadID: "thread-queued", CWD: "/tmp"})
	c.EnqueueStop(threaddto.Stopped{ThreadID: "thread-queued"})

	// 释放阻塞并立即 Stop；Stop 必须等队列排空后才返回。
	close(block)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := c.Stop(ctx); err != nil {
		t.Fatalf("Stop() = %v, want nil", err)
	}

	if got := c.ProcessedTotal(); got != 3 {
		t.Fatalf("ProcessedTotal after drain = %d, want 3", got)
	}
	if got := svc.StartCount(); got != 2 {
		t.Errorf("StartSession total = %d, want 2", got)
	}
	if got := len(svc.StopIDs()); got != 1 {
		t.Errorf("StopSession total = %d, want 1", got)
	}
}
