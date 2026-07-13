package hooks

import (
	"context"
	"encoding/json"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	mcp "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/mcp"
	platformconfig "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/config"
	platformshared "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/shared"
	pkglogger "github.com/lihah111222333-cloud/super-dolphin-agent/pkg/logger"
)

// hooks dispatch 队列与退化通知常量。
const (
	hookDispatchDrainGrace      = 10 * time.Second
	hookDispatchPendingLimit    = 4
	hookDispatchDegradedEvent   = "platform/queue/degraded"
	hookDispatchDegradedQueueID = "hooks_dispatch"
)

type hookDispatchRequest struct {
	topic     string
	payload   mcp.HookPayload
	eventTime time.Time
	degraded  bool
	dropped   int64
}

// hookDispatchFanout 是 worker 需要的 Manager 最小能力。
// 只依赖 DispatchAfter，测试可以用轻量 fake 覆盖队列和关闭行为。
type hookDispatchFanout interface {
	DispatchAfter(ctx context.Context, topic string, payload mcp.HookPayload) (mcp.AfterDecision, error)
}

// hookDispatchWorker 串行承接 bus 事件到 Manager.DispatchAfter 的 fanout。
// bus callback 只入队，不直接调用 peer；Stop(ctx) 关闭入口并排空队列，避免关闭后仍有未托管的 dispatch。
type hookDispatchWorker struct {
	fanout hookDispatchFanout
	logger *pkglogger.Logger

	mu      sync.Mutex
	stopped bool
	queue   []hookDispatchRequest

	wake chan struct{}

	startOnce sync.Once
	stopOnce  sync.Once
	stopCh    chan struct{}
	doneCh    chan struct{}

	enqueuedTotal          atomic.Int64
	processedTotal         atomic.Int64
	rejectedAfterStopTotal atomic.Int64
}

// newHookDispatchWorker 创建 hooks dispatch worker 并补齐默认 logger。
func newHookDispatchWorker(fanout hookDispatchFanout, logger *pkglogger.Logger) *hookDispatchWorker {
	if logger == nil {
		logger = pkglogger.Get()
	}
	return &hookDispatchWorker{
		fanout: fanout,
		logger: logger,
		wake:   make(chan struct{}, 1),
		stopCh: make(chan struct{}),
		doneCh: make(chan struct{}),
	}
}

// Start 启动单 worker goroutine。
// fanout 为空时立即关闭 doneCh，让 Stop 成为无副作用空等待；panic 会被捕获并写日志。
func (w *hookDispatchWorker) Start() {
	if w == nil {
		return
	}
	w.startOnce.Do(func() {
		if w.fanout == nil {
			close(w.doneCh)
			return
		}
		var workerWG sync.WaitGroup
		workerWG.Go(func() {
			defer func() {
				if rec := recover(); rec != nil {
					pkglogger.Error("hooks: recovered dispatch_worker panic", "panic", rec)
				}
			}()
			w.runWorker()
		})
	})
}

// Enqueue 记录一次 hook dispatch 请求并用非阻塞信号唤醒 worker。
// 它只做 O(1) 入队，适合 bus callback 调用；Stop 后的新请求会被丢弃以封住关闭入口。
func (w *hookDispatchWorker) Enqueue(topic string, eventTime time.Time, payload mcp.HookPayload) {
	if w == nil {
		return
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.stopped {
		w.rejectedAfterStopTotal.Add(1)
		return
	}
	w.enqueueLocked(hookDispatchRequest{
		topic:     topic,
		payload:   payload,
		eventTime: eventTime,
	})
	w.enqueuedTotal.Add(1)
	select {
	case w.wake <- struct{}{}:
	default:
	}
}

// enqueueLocked 在持锁状态下写入队列，满载时只保留退化信号和最新请求。
func (w *hookDispatchWorker) enqueueLocked(req hookDispatchRequest) {
	if len(w.queue) > 0 && w.queue[0].degraded {
		w.queue = []hookDispatchRequest{hookDispatchDegradedRequest(req, w.queue[0].dropped+1), req}
		return
	}
	if len(w.queue) >= hookDispatchPendingLimit {
		dropped := int64(len(w.queue))
		w.queue = []hookDispatchRequest{hookDispatchDegradedRequest(req, dropped), req}
		return
	}
	w.queue = append(w.queue, req)
}

// hookDispatchDegradedRequest 构造 hook 队列压缩后的显式退化请求。
func hookDispatchDegradedRequest(latest hookDispatchRequest, dropped int64) hookDispatchRequest {
	return hookDispatchRequest{
		topic:     latest.topic,
		eventTime: latest.eventTime,
		degraded:  true,
		dropped:   dropped,
		payload: mcp.HookPayload{
			AgentID: "platform",
			Topic:   latest.topic,
			Context: json.RawMessage(`{"event":"` + hookDispatchDegradedEvent + `","queue":"` + hookDispatchDegradedQueueID + `","dropped":` + strconv.FormatInt(dropped, 10) + `,"mode":"latest_only"}`),
		},
	}
}

// Stop 关闭入队入口、等待 worker 排空已接收请求，并受 ctx 或 hookDispatchDrainGrace 限制。
// 多次调用只执行一次关闭动作。
func (w *hookDispatchWorker) Stop(ctx context.Context) error {
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
		if deadline, ok := waitCtx.Deadline(); !ok || time.Until(deadline) > hookDispatchDrainGrace {
			var cancel context.CancelFunc
			waitCtx, cancel = platformconfig.WithTimeout(waitCtx, hookDispatchDrainGrace)
			defer cancel()
			_ = deadline
		}
		select {
		case <-w.doneCh:
		case <-waitCtx.Done():
			firstErr = waitCtx.Err()
		}
	})
	return firstErr
}

// EnqueuedTotal 返回已接收的 relay 请求数量，供测试和指标采集读取。
func (w *hookDispatchWorker) EnqueuedTotal() int64 { return w.enqueuedTotal.Load() }

// RejectedAfterStopTotal 返回 stop gate 拒绝的请求数量。
func (w *hookDispatchWorker) RejectedAfterStopTotal() int64 { return w.rejectedAfterStopTotal.Load() }

// ProcessedTotal 返回已完成 dispatch 的请求数量。
func (w *hookDispatchWorker) ProcessedTotal() int64 { return w.processedTotal.Load() }

// runWorker 监听 wake/stop 信号并串行排空队列。
// stop 信号到达后仍会处理已经入队的请求，随后关闭 doneCh。
func (w *hookDispatchWorker) runWorker() {
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

// drainPending 在锁内取出当前批次，再释放锁执行 dispatch。
// 这样保持 FIFO 批次顺序，同时避免 peer 调用阻塞入队路径；单个 peer 错误只记录不终止 worker。
func (w *hookDispatchWorker) drainPending() {
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

// dispatch 为单个 relay 请求构造事件时间 context 并调用 hooks fanout。
// DispatchAfter 错误只影响该请求，worker 继续处理后续队列项。
func (w *hookDispatchWorker) dispatch(req hookDispatchRequest) {
	ctx := platformshared.WithEventTime(context.Background(), req.eventTime)
	if _, err := w.fanout.DispatchAfter(ctx, req.topic, req.payload); err != nil && w.logger != nil {
		w.logger.Warn("hooks: observed event relay failed",
			"topic", req.topic,
			"agent_id", req.payload.AgentID,
			"thread_id", req.payload.ThreadID,
			"error", err,
		)
	}
}
