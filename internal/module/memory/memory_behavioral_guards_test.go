package memory

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	threaddto "github.com/anthropic-ai/super-agent-v3/internal/dto/thread"
	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
)

// -----------------------------------------------------------------------------
// TestAutoDreamOverflowCoalescesForReplay
// -----------------------------------------------------------------------------

// TestAutoDreamOverflowCoalescesForReplay 锁定 auto-dream 队列溢出后的耐久补处理。
// Enqueue 仍然不能阻塞总线回调，但满队列时必须按 threadID 合并为 pending，
// worker 启动后除了队列内事件，还要至少补处理每个溢出 threadID 一次。
func TestAutoDreamOverflowCoalescesForReplay(t *testing.T) {
	t.Parallel()
	// enabled=true 且 consolidator=nil 会走生产 Enqueue 路径，同时让 process 快速自增返回，
	// 便于测试只观察队列容量与丢弃语义。
	hooks := newTestHooks(withEnabled(true))
	s := newAutoDreamScheduler(hooks, pkglogger.Get())

	// worker 未启动时先填满队列，确保缓冲区正好持有 cap 条事件。
	for i := 0; i < autoDreamSchedulerQueueCap; i++ {
		s.Enqueue("thread-filler")
	}
	if got := s.DroppedTotal(); got != 0 {
		t.Fatalf("DroppedTotal at cap = %d, want 0 (queue not yet full)", got)
	}

	// 队列满后继续写入同一个 threadID，必须合并为一次 pending 重试而不是丢弃为终态。
	const overflow = 7
	for i := 0; i < overflow; i++ {
		s.Enqueue("thread-coalesced")
	}
	if got := s.DroppedTotal(); got != 0 {
		t.Fatalf("DroppedTotal after overflow = %d, want 0 because overflow is coalesced", got)
	}

	// worker 必须处理已入队的 cap 条事件，并补处理合并后的溢出 threadID 一次。
	// Stop 会让 runWorker 直接退出，所以先轮询自然 drain，避免未取出的队列事件被测试自身截断。
	s.Start()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if s.ProcessedTotal() >= int64(autoDreamSchedulerQueueCap+1) {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := s.Stop(ctx); err != nil {
		t.Fatalf("Stop error = %v", err)
	}

	// 核心不变量：溢出 threadID 只 coalesce 一次，既不静默丢失，也不按溢出次数重复放大。
	if got := s.ProcessedTotal(); got != int64(autoDreamSchedulerQueueCap+1) {
		t.Errorf("ProcessedTotal after drain = %d, want %d (queue plus one coalesced retry)",
			got, autoDreamSchedulerQueueCap+1)
	}
	if got := s.DroppedTotal(); got != 0 {
		t.Errorf("DroppedTotal after drain = %d, want 0 (overflow should be durable/coalesced)", got)
	}
}

// -----------------------------------------------------------------------------
// TestAutoDreamRequiresExplicitProjectScope
// -----------------------------------------------------------------------------

// stubAutoDreamExplicitScopeStore serves a single ThreadMetadata row
// with a non-empty AgentMemoryScope. When autoDreamAllowed runs against
// this metadata, hasAgentMemoryScope() returns true and the scheduler
// must decline to schedule a dream task; child-agent scoped threads do not
// own project-scope auto-dream writes.

type stubAutoDreamExplicitScopeStore struct {
	thread *contract.ThreadMetadata
}

func (s *stubAutoDreamExplicitScopeStore) GetByThreadID(_ context.Context, _ string) (*contract.ThreadMetadata, error) {
	return s.thread, nil
}

func (s *stubAutoDreamExplicitScopeStore) ListAll(context.Context) ([]contract.ThreadMetadata, error) {
	return nil, nil
}

// TestAutoDreamRequiresExplicitProjectScope 锁定 autoDreamAllowed 的 scope 边界。
// 带显式 AgentMemoryScope 的 thread 不能复用项目级 auto-dream consolidator；
// maybeScheduleAutoDream 必须返回 false 且不能启动 dream task。

func TestAutoDreamRequiresExplicitProjectScope(t *testing.T) {
	t.Parallel()

	// 构造一个看起来像 auto-memory-root 的 thread，但显式携带 AgentMemoryScope。
	// 该组合必须被 autoDreamAllowed 拒绝，因为子 agent 作用域 thread 不是项目级 auto-dream 写入者。

	now := time.Date(2026, 4, 24, 12, 0, 0, 0, time.UTC)
	finishedAt := now.Unix()
	thread := &contract.ThreadMetadata{
		ThreadID:         "thread-agent-scope",
		Cwd:              t.TempDir(),
		UpdatedAt:        now.Unix(),
		FinishedAt:       &finishedAt,
		AgentMemoryScope: "writer-agent",
		ConfigOverride:   mustStoredRuntimeConfig(t, map[string]any{"threadKind": "main"}),
	}
	store := &stubAutoDreamExplicitScopeStore{thread: thread}

	hooks := newMemoryLifecycleHooks(
		&Config{Enabled: true, RootDir: t.TempDir()},
		NewAutoDreamConsolidator(NewMemoryExtractor()),
		nil,
		nil,
		store,
		nil,
		NewMemoryExtractor(),
		NewManifestBuilder(),
	)
	hooks.timeNow = func() time.Time { return now }
	hooks.extractFn = func(context.Context, string) (string, error) {
		t.Fatal("extractFn must not run for agent-scoped threads")
		return "", nil
	}

	started, err := hooks.maybeScheduleAutoDream(context.Background(), "thread-agent-scope")
	if err != nil {
		t.Fatalf("maybeScheduleAutoDream() error = %v", err)
	}
	if started {
		t.Fatal("maybeScheduleAutoDream() = true for agent-scoped thread, want false")
	}
	if snap := hooks.dreamTaskSnapshot(); snap.Running {
		t.Fatalf("dream task running after agent-scope refusal: %#v", snap)
	}
}

// -----------------------------------------------------------------------------
// TestTeamSyncRuntimeSwapFinalFlush
// -----------------------------------------------------------------------------

// recordingTeamLifecycle satisfies teampkg.Lifecycle and records every
// Start/Stop call with its threadID under a mutex so tests can observe
// dispatch ordering after a drain.
type recordingTeamLifecycle struct {
	mu        sync.Mutex
	starts    []string
	stops     []string
	startN    atomic.Int64
	stopN     atomic.Int64
	blockStop chan struct{} // optional: hold a StopSession call open
}

func (r *recordingTeamLifecycle) StartSession(_ context.Context, threadID string, _ contract.BuildCtx) error {
	r.startN.Add(1)
	r.mu.Lock()
	r.starts = append(r.starts, threadID)
	r.mu.Unlock()
	return nil
}

func (r *recordingTeamLifecycle) StopSession(_ context.Context, threadID string) error {
	r.stopN.Add(1)
	r.mu.Lock()
	r.stops = append(r.stops, threadID)
	block := r.blockStop
	r.mu.Unlock()
	if block != nil {
		<-block
	}
	return nil
}

func (r *recordingTeamLifecycle) snapshot() (starts, stops []string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	starts = append([]string(nil), r.starts...)
	stops = append([]string(nil), r.stops...)
	// archguard:ignore naked_returns -- named results document the paired snapshot slices.
	return
}

// TestTeamSyncRuntimeSwapFinalFlush 锁定 team-sync coordinator 的 FIFO drain 行为。
// runtime swap 会交错多个 thread 的 Start/Stop；Stop 必须按入队顺序交付所有事件，
// 不能在 shutdown drain 中静默丢失最后一批生命周期通知。
func TestTeamSyncRuntimeSwapFinalFlush(t *testing.T) {
	t.Parallel()

	lc := &recordingTeamLifecycle{}
	c := newTeamSyncCoordinator(lc, nil, pkglogger.Get())
	c.Start()

	// 模拟项目切换：A 启停、B 启停、再回到 A。dispatch loop 必须严格 FIFO，
	// 即使 wake 被合并也不能打乱顺序。
	ops := []struct {
		kind string
		id   string
	}{
		{"start", "thread-A"},
		{"stop", "thread-A"},
		{"start", "thread-B"},
		{"stop", "thread-B"},
		{"start", "thread-A"},
	}
	for _, op := range ops {
		switch op.kind {
		case "start":
			c.EnqueueStart(threaddto.Started{ThreadID: op.id, CWD: "/tmp/cwd"})
		case "stop":
			c.EnqueueStop(threaddto.Stopped{ThreadID: op.id})
		}
	}

	// Stop 同步 drain：返回时所有 pending op 都已交付给 lifecycle，
	// 这是关闭阶段最后一次 flush 的保障，不能截断队列。
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := c.Stop(ctx); err != nil {
		t.Fatalf("Stop error = %v", err)
	}

	if got := c.ProcessedTotal(); got != int64(len(ops)) {
		t.Errorf("ProcessedTotal = %d, want %d (final flush must deliver every op)", got, len(ops))
	}

	starts, stops := lc.snapshot()
	wantStarts := []string{"thread-A", "thread-B", "thread-A"}
	wantStops := []string{"thread-A", "thread-B"}
	if !equalStringSlices(starts, wantStarts) {
		t.Errorf("StartSession order = %v, want %v (FIFO across swap)", starts, wantStarts)
	}
	if !equalStringSlices(stops, wantStops) {
		t.Errorf("StopSession order = %v, want %v (FIFO across swap)", stops, wantStops)
	}

	// Post-drain enqueue must be silently dropped — the swap is closed;
	// no new runtime binding should leak into the finished coordinator.
	preEnqueued := c.EnqueuedTotal()
	c.EnqueueStart(threaddto.Started{ThreadID: "thread-late", CWD: "/tmp/cwd"})
	if got := c.EnqueuedTotal(); got != preEnqueued {
		t.Errorf("teamSyncCoordinator accepted enqueue after final flush: delta = %d", got-preEnqueued)
	}
}

func equalStringSlices(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
