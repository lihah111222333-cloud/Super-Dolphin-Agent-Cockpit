package memory

import (
	"context"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
	threaddto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/thread"
	teampkg "github.com/lihah111222333-cloud/super-dolphin-agent/internal/module/memory/team"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/util/ctxutil"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/util/safego"
	pkglogger "github.com/lihah111222333-cloud/super-dolphin-agent/pkg/logger"
)

// teamSyncCoordinatorDrainGrace 限制协调器 OnStop 等待 worker 的时间。
// TeamSyncService 可能触发 git、远端 HTTP 和文件监听收尾，关机路径必须有固定上限。
const teamSyncCoordinatorDrainGrace = 10 * time.Second

// teamSyncOpKind 标记队列中的线程生命周期事件类型。
type teamSyncOpKind int

// team sync 队列事件类型。
const (
	teamSyncOpStart teamSyncOpKind = iota + 1
	teamSyncOpStop
)

// teamSyncOp 保存单个待串行派发的线程 start/stop 事件。
type teamSyncOp struct {
	kind    teamSyncOpKind
	started threaddto.Started
	stopped threaddto.Stopped
}

// teamSyncCoordinator 把 thread start/stop 事件从 bus 回调转交给单 worker 串行处理。
// 回调路径只做入队和唤醒，避免把磁盘读取、git 探测、远端拉取和 fsnotify 启动压在事件分发 goroutine 上。
// 队列不合并事件：除 Stop 之后的新事件会被丢弃外，已入队事件都按 FIFO 进入 TeamSyncService。
type teamSyncCoordinator struct {
	svc    teampkg.Lifecycle            // 团队记忆同步服务；nil 表示功能关闭。
	store  contract.ThreadMetadataStore // start 事件恢复 BuildCtx 所需的线程元数据存储。
	logger *slog.Logger                 // worker 异常和派发失败的日志出口。

	mu    sync.Mutex   // 保护 queue，worker 派发前会释放锁。
	queue []teamSyncOp // FIFO 事件队列，保持线程生命周期顺序。

	wake chan struct{} // 带缓冲唤醒信号，避免重复 wake 阻塞回调路径。

	startOnce sync.Once     // 保证 worker 只启动一次。
	stopOnce  sync.Once     // 保证 stopCh 只关闭一次。
	stopCh    chan struct{} // 关闭后拒绝新事件并通知 worker drain。
	doneCh    chan struct{} // worker 完全退出后关闭，Stop 用它做有界等待。

	enqueuedTotal  atomic.Int64 // 入队事件计数，用于测试和后续观测。
	processedTotal atomic.Int64 // 已派发事件计数，用于确认 drain 进度。
}

// newTeamSyncCoordinator 创建线程事件同步协调器，logger 为空时使用全局 logger。
func newTeamSyncCoordinator(svc teampkg.Lifecycle, store contract.ThreadMetadataStore, logger *slog.Logger) *teamSyncCoordinator {
	if logger == nil {
		logger = pkglogger.Get()
	}
	return &teamSyncCoordinator{
		svc:    svc,
		store:  store,
		logger: logger,
		wake:   make(chan struct{}, 1),
		stopCh: make(chan struct{}),
		doneCh: make(chan struct{}),
	}
}

// Start 启动单 worker goroutine；方法可重复调用。
// svc 为 nil 时立即关闭 doneCh，让 Stop 不阻塞，入队路径保持轻量丢弃。
func (c *teamSyncCoordinator) Start() {
	if c == nil {
		return
	}
	c.startOnce.Do(func() {
		if c.svc == nil {
			close(c.doneCh)
			return
		}
		safego.Go(context.Background(), pkglogger.Get(), "memory.team_sync.coordinator", func(context.Context) {
			c.runWorker()
		})
	})
}

// EnqueueStart 记录 thread.Started 事件。
// 事件分发回调只做 O(1) 入队和非阻塞唤醒，真正的磁盘/网络工作由 worker 处理。
func (c *teamSyncCoordinator) EnqueueStart(ev threaddto.Started) {
	if c == nil {
		return
	}
	if strings.TrimSpace(ev.ThreadID) == "" {
		return
	}
	c.enqueue(teamSyncOp{kind: teamSyncOpStart, started: ev})
}

// EnqueueStop 记录 thread.Stopped 事件。
// final flush 和最后一个 session 的 watcher 关闭仍由 TeamSyncService 负责，协调器只保证串行派发。
func (c *teamSyncCoordinator) EnqueueStop(ev threaddto.Stopped) {
	if c == nil {
		return
	}
	if strings.TrimSpace(ev.ThreadID) == "" {
		return
	}
	c.enqueue(teamSyncOp{kind: teamSyncOpStop, stopped: ev})
}

// enqueue 在 Stop 前把事件追加到 FIFO 队列，并用有缓冲 wake 通知 worker。
func (c *teamSyncCoordinator) enqueue(op teamSyncOp) {
	select {
	case <-c.stopCh:
		return
	default:
	}
	c.mu.Lock()
	c.queue = append(c.queue, op)
	c.mu.Unlock()
	c.enqueuedTotal.Add(1)
	select {
	case c.wake <- struct{}{}:
	default:
	}
}

// Stop 关闭入队入口并等待 worker 把已有事件 drain 完成。
// 等待时间受 ctx 和 teamSyncCoordinatorDrainGrace 双重限制，避免关机卡在远端同步慢路径。
func (c *teamSyncCoordinator) Stop(ctx context.Context) error {
	if c == nil {
		return nil
	}
	var firstErr error
	c.stopOnce.Do(func() {
		close(c.stopCh)
		waitCtx := ctx
		if waitCtx == nil {
			waitCtx = context.Background()
		}
		if deadline, ok := waitCtx.Deadline(); !ok || time.Until(deadline) > teamSyncCoordinatorDrainGrace {
			var cancel context.CancelFunc
			waitCtx, cancel = ctxutil.WithTimeout(waitCtx, teamSyncCoordinatorDrainGrace)
			defer cancel()
			_ = deadline
		}
		select {
		case <-c.doneCh:
		case <-waitCtx.Done():
			firstErr = waitCtx.Err()
		}
	})
	return firstErr
}

// EnqueuedTotal 返回已进入队列的事件数量，主要用于测试和观测。
func (c *teamSyncCoordinator) EnqueuedTotal() int64 { return c.enqueuedTotal.Load() }

// ProcessedTotal 返回 worker 已派发的事件数量。
func (c *teamSyncCoordinator) ProcessedTotal() int64 { return c.processedTotal.Load() }

// runWorker 等待唤醒或停止信号，Stop 时先 drain 队列再关闭 doneCh。
func (c *teamSyncCoordinator) runWorker() {
	defer close(c.doneCh)
	for {
		select {
		case <-c.stopCh:
			c.drainPending()
			return
		case <-c.wake:
			c.drainPending()
		}
	}
}

// drainPending 在锁内取出当前批次、锁外派发，避免慢路径阻塞新的入队。
// 派发失败只记录日志，不终止 worker；线程同步失败不应拖垮事件总线。
func (c *teamSyncCoordinator) drainPending() {
	for {
		c.mu.Lock()
		if len(c.queue) == 0 {
			c.mu.Unlock()
			return
		}
		ops := c.queue
		c.queue = nil
		c.mu.Unlock()
		for _, op := range ops {
			c.dispatch(op)
			c.processedTotal.Add(1)
		}
	}
}

// dispatch 把队列事件转成 TeamSyncService 的 start/stop 调用。
// 这里不重试失败，避免 worker 因单个线程的远端错误长期占用队列。
func (c *teamSyncCoordinator) dispatch(op teamSyncOp) {
	switch op.kind {
	case teamSyncOpStart:
		if err := teampkg.StartSessionFromThreadEvent(c.svc, c.store, op.started); err != nil && c.logger != nil {
			c.logger.Warn("memory: team sync start session failed",
				"thread_id", op.started.ThreadID, "error", err)
		}
	case teamSyncOpStop:
		if err := teampkg.StopSessionFromThreadEvent(c.svc, op.stopped); err != nil && c.logger != nil {
			c.logger.Warn("memory: team sync stop session failed",
				"thread_id", op.stopped.ThreadID, "error", err)
		}
	}
}
