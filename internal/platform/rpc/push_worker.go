package rpc

import (
	"context"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	platformconfig "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/config"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/eventsurface"
	pkglogger "github.com/lihah111222333-cloud/super-dolphin-agent/pkg/logger"
)

// push worker 队列与退化通知常量。
const (
	pushWorkerDrainGrace = 10 * time.Second
	// pushWorkerPendingLimit 从 4 扩至 16，覆盖 UI 操作 burst 场景下短时多事件并发通知，
	// 避免队列过早触发 degraded 丢弃。
	pushWorkerPendingLimit    = 16
	pushWorkerDegradedMethod  = "platform/queue/degraded"
	pushWorkerDegradedQueueID = "rpc_push"
)

// pushBroadcaster 是 push worker 依赖的 Server 最小接口。
// 测试可用假实现替代完整 jrpc2 server，生产契约只要求广播 method+payload。
type pushBroadcaster interface {
	NotifyAll(ctx context.Context, bridge *PushBridge, method string, params any)
}

// pushRequest 是一次已展开的通知 batch。
type pushRequest struct {
	notifications []eventsurface.Notification
	degraded      bool
	dropped       int64
}

// pushNotificationWorker 是 bus event 到 Server.NotifyAll 慢路径的唯一拥有者。
// callback 只负责 Enqueue；worker 用可取消的 pushCtx 按 FIFO drain 队列，Stop 会先排空已接收请求再取消。
type pushNotificationWorker struct {
	server pushBroadcaster
	bridge *PushBridge
	logger *pkglogger.Logger

	pushCtx    context.Context
	pushCancel context.CancelFunc

	mu      sync.Mutex
	stopped bool
	queue   []pushRequest

	wake chan struct{}

	startOnce sync.Once
	stopOnce  sync.Once
	stopCh    chan struct{}
	doneCh    chan struct{}

	enqueuedTotal          atomic.Int64
	processedTotal         atomic.Int64
	notifySentTotal        atomic.Int64
	rejectedAfterStopTotal atomic.Int64
	droppedOnShutdownTotal atomic.Int64
}

// newPushNotificationWorker 构造带独立 pushCtx 和唤醒通道的 push worker。
func newPushNotificationWorker(server pushBroadcaster, bridge *PushBridge, logger *pkglogger.Logger) *pushNotificationWorker {
	if logger == nil {
		logger = pkglogger.Get()
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &pushNotificationWorker{
		server:     server,
		bridge:     bridge,
		logger:     logger,
		pushCtx:    ctx,
		pushCancel: cancel,
		wake:       make(chan struct{}, 1),
		stopCh:     make(chan struct{}),
		doneCh:     make(chan struct{}),
	}
}

// Start 启动 push worker goroutine，且多次调用只生效一次。
// server 或 bridge 缺失时直接关闭 doneCh，Stop 会成为无等待的空操作。
func (w *pushNotificationWorker) Start() {
	if w == nil {
		return
	}
	w.startOnce.Do(func() {
		if w.server == nil || w.bridge == nil {
			close(w.doneCh)
			return
		}
		var workerWG sync.WaitGroup
		workerWG.Go(func() {
			defer func() {
				if rec := recover(); rec != nil {
					pkglogger.Error("rpc: recovered push_notification_worker panic", "panic", rec)
				}
			}()
			w.runWorker()
		})
	})
}

// Enqueue 把预展开的通知 batch 加入队列并非阻塞唤醒 worker。
// 该方法可在 bus callback 内调用，不会在 callback goroutine 上执行 RPC 传输工作。
func (w *pushNotificationWorker) Enqueue(notifications []eventsurface.Notification) {
	if w == nil || len(notifications) == 0 {
		return
	}
	// callback 侧先丢弃空 method 通知，避免队列携带无法广播的 batch。
	// 兼容展开逻辑若收到空来源方法，可能生成空 method 刷新通知，这里提前拦住。
	filtered := make([]eventsurface.Notification, 0, len(notifications))
	for _, n := range notifications {
		if strings.TrimSpace(n.Method) != "" {
			filtered = append(filtered, n)
		}
	}
	if len(filtered) == 0 {
		return
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.stopped {
		w.rejectedAfterStopTotal.Add(1)
		return
	}
	w.enqueueLocked(pushRequest{notifications: filtered})
	w.enqueuedTotal.Add(1)
	select {
	case w.wake <- struct{}{}:
	default:
	}
}

// enqueueLocked 在持锁状态下写入队列，满载时压缩为 degraded 事件加最新请求。
func (w *pushNotificationWorker) enqueueLocked(req pushRequest) {
	if pushRequestMustPreserve(req) {
		w.queue = append(w.queue, req)
		return
	}
	if len(w.queue) > 0 && w.queue[0].degraded {
		w.compactOverflowQueueLocked(req)
		return
	}
	if len(w.queue) >= pushWorkerPendingLimit {
		w.compactOverflowQueueLocked(req)
		return
	}
	w.queue = append(w.queue, req)
}

// compactOverflowQueueLocked 只压缩可丢弃的高频请求，保留 assistant completion 与 terminal 的顺序。
func (w *pushNotificationWorker) compactOverflowQueueLocked(latest pushRequest) {
	var dropped int64
	preserved := make([]pushRequest, 0, len(w.queue))
	for _, queued := range w.queue {
		switch {
		case queued.degraded:
			dropped += queued.dropped
		case pushRequestMustPreserve(queued):
			preserved = append(preserved, queued)
		default:
			dropped++
		}
	}
	w.queue = append([]pushRequest{pushWorkerDegradedRequest(dropped)}, preserved...)
	w.queue = append(w.queue, latest)
}

// pushRequestMustPreserve 标记终态收敛所需的不可丢事件。
func pushRequestMustPreserve(req pushRequest) bool {
	for _, notification := range req.notifications {
		switch strings.TrimSpace(notification.Method) {
		case eventsurface.MethodItemCompleted, eventsurface.MethodTurnTerminal:
			return true
		}
	}
	return false
}

// pushWorkerDegradedRequest 构造队列压缩后的显式退化通知。
func pushWorkerDegradedRequest(dropped int64) pushRequest {
	return pushRequest{
		degraded: true,
		dropped:  dropped,
		notifications: []eventsurface.Notification{{
			Method: pushWorkerDegradedMethod,
			Payload: map[string]any{
				"event":   pushWorkerDegradedMethod,
				"queue":   pushWorkerDegradedQueueID,
				"dropped": dropped,
				"mode":    "latest_only",
			},
		}},
	}
}

// PushCtx 返回传给 NotifyAll 的可取消 context，测试用它确认没有绕回 Background。
func (w *pushNotificationWorker) PushCtx() context.Context {
	if w == nil {
		return context.Background()
	}
	return w.pushCtx
}

// Stop 关闭入队门、排空已接收请求，并在 ctx 或默认 grace 内等待 worker 退出后取消 pushCtx。
func (w *pushNotificationWorker) Stop(ctx context.Context) error {
	if w == nil {
		return nil
	}
	var firstErr error
	w.stopOnce.Do(func() {
		w.mu.Lock()
		w.stopped = true
		close(w.stopCh)
		w.mu.Unlock()
		waitCtx := ctx
		if waitCtx == nil {
			waitCtx = context.Background()
		}
		if deadline, ok := waitCtx.Deadline(); !ok || time.Until(deadline) > pushWorkerDrainGrace {
			var cancel context.CancelFunc
			waitCtx, cancel = platformconfig.WithTimeout(waitCtx, pushWorkerDrainGrace)
			defer cancel()
			_ = deadline
		}
		select {
		case <-w.doneCh:
			w.pushCancel()
		case <-waitCtx.Done():
			w.mu.Lock()
			w.droppedOnShutdownTotal.Add(int64(notificationCount(w.queue)))
			w.queue = nil
			w.mu.Unlock()
			w.pushCancel()
			firstErr = waitCtx.Err()
		}
	})
	return firstErr
}

// EnqueuedTotal 返回累计入队 batch 数。
func (w *pushNotificationWorker) EnqueuedTotal() int64 { return w.enqueuedTotal.Load() }

// RejectedAfterStopTotal 返回 stop gate 拒绝的通知 batch 数。
func (w *pushNotificationWorker) RejectedAfterStopTotal() int64 {
	return w.rejectedAfterStopTotal.Load()
}

// DroppedOnShutdownTotal 返回 drain 超时后取消发送的通知数。
func (w *pushNotificationWorker) DroppedOnShutdownTotal() int64 {
	return w.droppedOnShutdownTotal.Load()
}

// ProcessedTotal 返回累计处理 batch 数。
func (w *pushNotificationWorker) ProcessedTotal() int64 { return w.processedTotal.Load() }

// NotifySentTotal 返回累计发送通知数。
func (w *pushNotificationWorker) NotifySentTotal() int64 { return w.notifySentTotal.Load() }

// runWorker 等待唤醒或停止信号，并在停止时最后 drain 一次队列。
func (w *pushNotificationWorker) runWorker() {
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

// drainPending 在锁内取出队列快照，随后释放锁按 batch FIFO 发送。
// batch 内通知顺序不可打乱，因为兼容刷新必须落在源事件之后。
func (w *pushNotificationWorker) drainPending() {
	for {
		w.mu.Lock()
		if len(w.queue) == 0 {
			w.mu.Unlock()
			return
		}
		reqs := w.queue
		w.queue = nil
		w.mu.Unlock()
		// 前端过渡期兼容：保留独立 ui/thread/patch，同时在同一 drain 快照内
		// 给匹配源通知嵌入一份副本。
		reqs = embedThreadPatchRequests(reqs)
		for _, req := range reqs {
			w.dispatch(req)
			w.processedTotal.Add(1)
		}
	}
}

// dispatch 将单个 batch 中的通知逐条广播给所有活跃客户端。
func (w *pushNotificationWorker) dispatch(req pushRequest) {
	for index, n := range req.notifications {
		if w.pushCtx.Err() != nil {
			w.droppedOnShutdownTotal.Add(int64(len(req.notifications) - index))
			return
		}
		w.server.NotifyAll(w.pushCtx, w.bridge, n.Method, n.Payload)
		w.notifySentTotal.Add(1)
	}
}

func notificationCount(requests []pushRequest) int {
	total := 0
	for _, request := range requests {
		total += len(request.notifications)
	}
	return total
}
