package memory

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	threaddto "github.com/anthropic-ai/super-agent-v3/internal/dto/thread"
	teampkg "github.com/anthropic-ai/super-agent-v3/internal/module/memory/team"
	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
)

// drainTestNestedRuntime 实现 nestedIngestRuntime 并记录每次 AddToolReadResult。
// 测试通过调用顺序确认 Stop 前排队的嵌套读取任务没有被丢弃。
type drainTestNestedRuntime struct {
	mu    sync.Mutex
	calls []string // 按 AddToolReadResult 调用顺序记录 threadID
}

func (r *drainTestNestedRuntime) AddToolReadResult(threadID, _, _, _ string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, threadID)
}

func (r *drainTestNestedRuntime) callCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.calls)
}

// drainTestTeamLifecycle 实现 drain 测试所需的 teampkg.Lifecycle。
// Start/Stop 计数使用 atomic，避免断言与 coordinator dispatcher goroutine 发生数据竞争。
type drainTestTeamLifecycle struct {
	startCalls atomic.Int64
	stopCalls  atomic.Int64
}

func (n *drainTestTeamLifecycle) StartSession(_ context.Context, _ string, _ contract.BuildCtx) error {
	n.startCalls.Add(1)
	return nil
}

func (n *drainTestTeamLifecycle) StopSession(_ context.Context, _ string) error {
	n.stopCalls.Add(1)
	return nil
}

// TestMemoryHookWorkerDrainsOnStop 固定 drainMemoryHooks 的关闭边界。
// 三类 bus callback worker 预先排入工作后，Stop 必须让可 drain 的 worker 处理完待办并拒绝关闭后的新入队。
// 对用户可见的正确性边界是 shutdown；新增 worker 或调整 drain 顺序时不能静默丢掉已入队任务。
func TestMemoryHookWorkerDrainsOnStop(t *testing.T) {
	t.Parallel()

	// autoDreamScheduler 使用启用 hook 与 nil consolidator，走快速短路路径，只推进 processedTotal。
	hooks := newTestHooks(withEnabled(true))
	scheduler := newAutoDreamScheduler(hooks, pkglogger.Get())

	// nestedIngestWorker 使用记录型 runtime，便于断言所有 dispatch 都被 drain。
	rt := &drainTestNestedRuntime{}
	nested := newNestedIngestWorker(rt, pkglogger.Get())

	// teamSyncCoordinator 使用空 lifecycle 和 nil store，覆盖仅依赖事件 CWD 的分支。
	var svc teampkg.Lifecycle = &drainTestTeamLifecycle{}
	teamSync := newTeamSyncCoordinator(svc, nil, pkglogger.Get())

	startDrainTestWorkers(scheduler, nested, teamSync)

	const enqueuePerWorker = 3
	enqueueDrainTestWork(scheduler, nested, teamSync, enqueuePerWorker)

	// autoDreamScheduler.Stop 本身不 drain channel buffer；并行测试下先按已处理数量校准期望值。
	// 否则 drainMemoryHooks 触发 Stop 时会与未消费 buffer 竞争并丢条目。
	// nested/teamSync 会在 Stop 内 drain pending map，不需要预等待。
	waitSchedulerProcessed(scheduler, enqueuePerWorker)

	// drainMemoryHooks 是被测目标，必须在 ctx 边界内按顺序 drain 每个 worker 后再返回。
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	drainMemoryHooks(ctx, scheduler, nested, teamSync, nil)

	// drain 后每个 worker 的 ProcessedTotal 都要覆盖它观察到的待办工作。
	// autoDreamScheduler 的队列按字符串发送，重复 threadID 仍按多次入队计数。
	assertDrainTestProcessed(t, scheduler, nested, teamSync, rt, enqueuePerWorker)

	// 每个 owner 的第二次 Stop 必须是幂等空操作，并能观察到 doneCh 已关闭后立即返回。
	assertDrainStopsIdempotent(t, ctx, scheduler, nested, teamSync)

	// Stop 关闭闸门后，drain 后的 Enqueue 必须被所有 owner 丢弃。
	// 这样 shutdown 后的 bus 尾随事件不会写入永远不会被处理的工作。
	assertPostDrainEnqueuesDropped(t, scheduler, nested, teamSync)
}

func startDrainTestWorkers(scheduler *autoDreamScheduler, nested *nestedIngestWorker, teamSync *teamSyncCoordinator) {
	scheduler.Start()
	nested.Start()
	teamSync.Start()
}

func enqueueDrainTestWork(scheduler *autoDreamScheduler, nested *nestedIngestWorker, teamSync *teamSyncCoordinator, count int) {
	for i := 0; i < count; i++ {
		scheduler.Enqueue("thread-scheduler")
		// 每次使用不同 persistedPath，避免 nestedIngestWorker 的 coalesce key 合并入队。
		nested.Enqueue("thread-nested", "tool", "result", "/tmp/path-"+string(rune('0'+i)))
		teamSync.EnqueueStart(threaddto.Started{
			ThreadID: "thread-teamsync",
			CWD:      "/tmp/cwd",
		})
	}
}

func waitSchedulerProcessed(scheduler *autoDreamScheduler, count int) {
	pollDeadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(pollDeadline) {
		if scheduler.ProcessedTotal() >= int64(count) {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func assertDrainTestProcessed(
	t *testing.T,
	scheduler *autoDreamScheduler,
	nested *nestedIngestWorker,
	teamSync *teamSyncCoordinator,
	rt *drainTestNestedRuntime,
	want int,
) {
	t.Helper()

	if got := scheduler.ProcessedTotal(); got < int64(want) {
		t.Errorf("autoDreamScheduler ProcessedTotal = %d, want >= %d", got, want)
	}
	// nestedIngestWorker 的 pending set 使用唯一 persistedPath，因此三条都必须被派发。
	if got := nested.ProcessedTotal(); got != int64(want) {
		t.Errorf("nestedIngestWorker ProcessedTotal = %d, want %d", got, want)
	}
	if got := rt.callCount(); got != want {
		t.Errorf("nestedIngestWorker AddToolReadResult calls = %d, want %d", got, want)
	}
	// teamSyncCoordinator 保持严格 FIFO，不做合并。
	if got := teamSync.ProcessedTotal(); got != int64(want) {
		t.Errorf("teamSyncCoordinator ProcessedTotal = %d, want %d", got, want)
	}
}

func assertDrainStopsIdempotent(
	t *testing.T,
	ctx context.Context,
	scheduler *autoDreamScheduler,
	nested *nestedIngestWorker,
	teamSync *teamSyncCoordinator,
) {
	t.Helper()

	if err := scheduler.Stop(ctx); err != nil {
		t.Errorf("second Stop autoDreamScheduler = %v", err)
	}
	if err := nested.Stop(ctx); err != nil {
		t.Errorf("second Stop nestedIngestWorker = %v", err)
	}
	if err := teamSync.Stop(ctx); err != nil {
		t.Errorf("second Stop teamSyncCoordinator = %v", err)
	}
}

func assertPostDrainEnqueuesDropped(
	t *testing.T,
	scheduler *autoDreamScheduler,
	nested *nestedIngestWorker,
	teamSync *teamSyncCoordinator,
) {
	t.Helper()

	preSchedDropped := scheduler.DroppedTotal()
	preNestedEnqueued := nested.EnqueuedTotal()
	preTeamEnqueued := teamSync.EnqueuedTotal()
	scheduler.Enqueue("thread-late")
	nested.Enqueue("thread-late", "tool", "result", "/tmp/late")
	teamSync.EnqueueStart(threaddto.Started{ThreadID: "thread-late", CWD: "/tmp/cwd"})
	if got := scheduler.DroppedTotal(); got != preSchedDropped+1 {
		t.Errorf("autoDreamScheduler did not drop post-Stop enqueue: DroppedTotal delta = %d, want 1", got-preSchedDropped)
	}
	if nested.EnqueuedTotal() != preNestedEnqueued {
		t.Error("nestedIngestWorker accepted enqueue after drain")
	}
	if teamSync.EnqueuedTotal() != preTeamEnqueued {
		t.Error("teamSyncCoordinator accepted enqueue after drain")
	}
}
