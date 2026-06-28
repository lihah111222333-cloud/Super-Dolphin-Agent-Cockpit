package memory

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	turndto "github.com/anthropic-ai/super-agent-v3/internal/dto/turn"
	"github.com/anthropic-ai/super-agent-v3/internal/util/ctxutil"
	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
)

// memoryHookDrainGrace 限制 Stop 等待 worker 的最长时间，避免磁盘 I/O 卡住时拖住进程退出。
const memoryHookDrainGrace = 10 * time.Second

// memoryHookMaxQueue 限制生命周期 hook 积压，防止 turn 风暴把内存队列撑成无界 slice。
const memoryHookMaxQueue = 32

// ----- 记忆事件 worker -----

// memoryHookEventKind 标记 worker 当前处理的 turn 事件类型。
type memoryHookEventKind int

const (
	memoryHookTurnInputReceived memoryHookEventKind = iota
	memoryHookTurnCompleted
)

// memoryHookRequest 是 bus 回调投递给 worker 的最小任务单元。
type memoryHookRequest struct {
	kind          memoryHookEventKind
	turnInput     turndto.TurnInputReceived
	turnCompleted turndto.TurnCompleted
}

// memoryHookWorker 用单 goroutine 承接 turn 回调里的记忆磁盘 I/O。
// bus 回调只做轻量内存记录并入队，真正的 remember/forget 和抽取写入都在这里顺序执行。
type memoryHookWorker struct {
	hooks  *MemoryLifecycleHooks
	logger *pkglogger.Logger

	mu    sync.Mutex
	queue []memoryHookRequest

	wake chan struct{}

	lifecycleCtx    context.Context
	lifecycleCancel context.CancelFunc

	startOnce sync.Once
	stopOnce  sync.Once
	stopCh    chan struct{}
	doneCh    chan struct{}

	enqueuedTotal  atomic.Int64
	processedTotal atomic.Int64
	droppedTotal   atomic.Int64
	degraded       atomic.Bool
}

// newMemoryHookWorker 创建记忆事件 worker，并在未注入 logger 时使用全局 logger。
func newMemoryHookWorker(hooks *MemoryLifecycleHooks, logger *pkglogger.Logger) *memoryHookWorker {
	if logger == nil {
		logger = pkglogger.Get()
	}
	lifecycleCtx, lifecycleCancel := context.WithCancel(context.Background())
	return &memoryHookWorker{
		hooks:           hooks,
		logger:          logger,
		wake:            make(chan struct{}, 1),
		lifecycleCtx:    lifecycleCtx,
		lifecycleCancel: lifecycleCancel,
		stopCh:          make(chan struct{}),
		doneCh:          make(chan struct{}),
	}
}

// Start 幂等启动 worker goroutine；hooks 缺失时直接关闭 doneCh，让 Stop 可立即返回。
func (w *memoryHookWorker) Start() {
	if w == nil {
		return
	}
	w.startOnce.Do(func() {
		if w.hooks == nil {
			close(w.doneCh)
			return
		}
		go func() {
			defer func() {
				if rec := recover(); rec != nil {
					pkglogger.Error("memory: recovered hook_worker panic", "panic", rec)
				}
			}()
			w.runWorker()
		}()
	})
}

// Stop 幂等关闭入队入口并等待 worker drain。
// 等待时间受调用方 ctx 和 memoryHookDrainGrace 共同限制。
func (w *memoryHookWorker) Stop(ctx context.Context) error {
	if w == nil {
		return nil
	}
	var firstErr error
	w.stopOnce.Do(func() {
		close(w.stopCh)
		w.lifecycleCancel()
		waitCtx := ctx
		if waitCtx == nil {
			waitCtx = context.Background()
		}
		if deadline, ok := waitCtx.Deadline(); !ok || time.Until(deadline) > memoryHookDrainGrace {
			var cancel context.CancelFunc
			waitCtx, cancel = ctxutil.WithTimeout(waitCtx, memoryHookDrainGrace)
			defer cancel()
			_ = deadline
		}
		select {
		case <-w.doneCh:
		case <-waitCtx.Done():
			firstErr = w.stopError(waitCtx.Err())
		}
	})
	return firstErr
}

// Enqueue 从 bus 回调追加记忆任务，只做 O(1) 入队和非阻塞唤醒。
func (w *memoryHookWorker) Enqueue(req memoryHookRequest) {
	if w == nil {
		return
	}
	select {
	case <-w.stopCh:
		return
	default:
	}
	w.mu.Lock()
	if len(w.queue) >= memoryHookMaxQueue {
		w.queue = append(w.queue[1:], req)
		w.droppedTotal.Add(1)
		w.degraded.Store(true)
	} else {
		w.queue = append(w.queue, req)
	}
	w.mu.Unlock()
	w.enqueuedTotal.Add(1)
	select {
	case w.wake <- struct{}{}:
	default:
	}
}

// Backlog 返回当前尚未处理的记忆 hook 数量，用于退出和诊断时暴露积压状态。
func (w *memoryHookWorker) Backlog() int {
	if w == nil {
		return 0
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	return len(w.queue)
}

// Degraded 返回 worker 是否发生过队列压缩，供调用方把后台记忆写入退化状态显式暴露。
func (w *memoryHookWorker) Degraded() bool {
	if w == nil {
		return false
	}
	return w.degraded.Load()
}

// stopError 将 Stop 超时和剩余 backlog/dropped 数量放进同一个错误，避免上层只能看到裸 context 错误。
func (w *memoryHookWorker) stopError(cause error) error {
	return fmt.Errorf("memory hook worker stop: %w (backlog=%d dropped=%d degraded=%t)",
		cause, w.Backlog(), w.droppedTotal.Load(), w.Degraded())
}

// runWorker 持有唯一的消费循环；收到 stop 后会先清空队列再关闭 doneCh。
func (w *memoryHookWorker) runWorker() {
	defer close(w.doneCh)
	for {
		select {
		case <-w.stopCh:
			w.drainPending()
			return
		case <-w.wake:
			w.drainPending()
		}
	}
}

// drainPending 批量取出当前队列后在锁外执行，避免磁盘 I/O 阻塞新的入队操作。
func (w *memoryHookWorker) drainPending() {
	for {
		w.mu.Lock()
		if len(w.queue) == 0 {
			w.mu.Unlock()
			return
		}
		reqs := w.queue
		w.queue = nil
		w.mu.Unlock()
		for _, req := range reqs {
			w.dispatch(req)
			w.processedTotal.Add(1)
		}
	}
}

// dispatch 将 worker 请求分发回 MemoryLifecycleHooks。
// 所有记忆磁盘写入都保持在 worker goroutine 上串行化。
func (w *memoryHookWorker) dispatch(req memoryHookRequest) {
	switch req.kind {
	case memoryHookTurnInputReceived:
		w.hooks.onTurnInputReceived(w.lifecycleCtx, req.turnInput)
	case memoryHookTurnCompleted:
		w.hooks.onTurnCompleted(w.lifecycleCtx, req.turnCompleted)
	}
}
