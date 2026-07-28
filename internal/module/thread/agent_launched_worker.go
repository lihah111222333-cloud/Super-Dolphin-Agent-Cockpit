package thread

import (
	"context"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	agentdto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/agent"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/util/ctxutil"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/util/safego"
	pkglogger "github.com/lihah111222333-cloud/super-dolphin-agent/pkg/logger"
)

// agentLaunchedDrainGrace 限制 agent 启动事件 worker 的停止等待时间。
// 绑定写入或 prompt 缓存失效卡在 I/O 上时，订阅 OnStop 也不能无限阻塞。
const (
	agentLaunchedDrainGrace     = 10 * time.Second
	agentLaunchedMaxAttempts    = 3
	agentLaunchedRetryBaseDelay = 100 * time.Millisecond
)

// agentLaunchedProcessor 是 worker 对 thread service 的最小依赖。
// worker 只负责串行化和合并事件，真正的绑定解析、CWD 同步和缓存失效由 service 执行。
type agentLaunchedProcessor interface {
	processAgentLaunched(ev agentdto.AgentLaunched) error
}

// agentLaunchedWorkerHealth 是 worker 的进程内健康快照，不扩散到 RPC 契约。
type agentLaunchedWorkerHealth struct {
	Enqueued, Coalesced, Processed int64
	Failed, Retried, Dropped       int64
	LastError                      string
}

// agentLaunchedWorker 串行处理 AgentLaunched 事件中的慢路径副作用。
// bus 回调只入队；worker 按 agentID 或回退 threadID 合并突发事件，并在 Stop 时按 ctx 有界 drain。
type agentLaunchedWorker struct {
	processor agentLaunchedProcessor
	logger    *slog.Logger

	mu      sync.Mutex
	pending map[string]agentdto.AgentLaunched

	wake chan struct{}

	startOnce, stopOnce sync.Once
	stopCh, doneCh      chan struct{}

	enqueuedTotal, coalescedTotal, processedTotal atomic.Int64
	failedTotal, retriedTotal, droppedTotal       atomic.Int64
	retryBaseDelay                                time.Duration
	lastErrorMu                                   sync.RWMutex
	lastError                                     string
}

func newAgentLaunchedWorker(processor agentLaunchedProcessor, logger *slog.Logger) *agentLaunchedWorker {
	if logger == nil {
		logger = pkglogger.Get()
	}
	return &agentLaunchedWorker{
		processor: processor, logger: logger,
		pending: map[string]agentdto.AgentLaunched{},
		wake:    make(chan struct{}, 1), stopCh: make(chan struct{}), doneCh: make(chan struct{}),
		retryBaseDelay: agentLaunchedRetryBaseDelay,
	}
}

// Start 启动后台 worker goroutine。
// processor 为空时直接关闭 doneCh，使 Stop 立即返回且 Enqueue 仍是低成本 no-op。
func (w *agentLaunchedWorker) Start() {
	if w == nil {
		return
	}
	w.startOnce.Do(func() {
		if w.processor == nil {
			close(w.doneCh)
			return
		}
		safego.Go(context.Background(), pkglogger.Get(), "thread.agent_launched.worker", func(context.Context) {
			w.runWorker()
		})
	})
}

// Enqueue 记录等待异步处理的 AgentLaunched 事件。
// bus 回调里只做 O(1) map 写入和非阻塞唤醒；key 为空时丢弃，停止后到达的事件也丢弃。
func (w *agentLaunchedWorker) Enqueue(key string, ev agentdto.AgentLaunched) {
	if w == nil {
		return
	}
	key = strings.TrimSpace(key)
	if key == "" {
		return
	}
	select {
	case <-w.stopCh:
		return
	default:
	}
	w.mu.Lock()
	if previous, dup := w.pending[key]; dup {
		w.coalescedTotal.Add(1)
		// cache-keepalive 可能紧随 provider 启动事件发送空身份快照；空值不能覆盖已观察到的权威 provider thread id。
		if strings.TrimSpace(ev.ProviderThreadID) == "" {
			ev.ProviderThreadID = previous.ProviderThreadID
		}
	}
	w.pending[key] = ev
	w.mu.Unlock()
	w.enqueuedTotal.Add(1)
	select {
	case w.wake <- struct{}{}:
	default:
	}
}

// Stop 关闭入队入口并等待 worker 有界退出。
// 关闭后到达的事件会被静默丢弃，这是避免已取消订阅继续写绑定的唯一丢弃路径。
func (w *agentLaunchedWorker) Stop(ctx context.Context) error {
	if w == nil {
		return nil
	}
	var firstErr error
	w.stopOnce.Do(func() {
		close(w.stopCh)
		waitCtx := ctx
		if waitCtx == nil {
			waitCtx = context.Background()
		}
		if deadline, ok := waitCtx.Deadline(); !ok || time.Until(deadline) > agentLaunchedDrainGrace {
			var cancel context.CancelFunc
			waitCtx, cancel = ctxutil.WithTimeout(waitCtx, agentLaunchedDrainGrace)
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

// EnqueuedTotal 返回已成功入队的事件数，供测试和观测读取。
func (w *agentLaunchedWorker) EnqueuedTotal() int64 { return w.enqueuedTotal.Load() }

// CoalescedTotal 返回按同一 key 合并掉的重复事件数。
func (w *agentLaunchedWorker) CoalescedTotal() int64 { return w.coalescedTotal.Load() }

// ProcessedTotal 返回已被 service 成功处理的事件数。
func (w *agentLaunchedWorker) ProcessedTotal() int64 { return w.processedTotal.Load() }

// Health 返回 worker 的进程内健康快照。
func (w *agentLaunchedWorker) Health() agentLaunchedWorkerHealth {
	if w == nil {
		return agentLaunchedWorkerHealth{}
	}
	w.lastErrorMu.RLock()
	lastError := w.lastError
	w.lastErrorMu.RUnlock()
	return agentLaunchedWorkerHealth{
		Enqueued: w.enqueuedTotal.Load(), Coalesced: w.coalescedTotal.Load(),
		Processed: w.processedTotal.Load(), Failed: w.failedTotal.Load(),
		Retried: w.retriedTotal.Load(), Dropped: w.droppedTotal.Load(),
		LastError: lastError,
	}
}

func (w *agentLaunchedWorker) runWorker() {
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

// drainPending 取出当前待处理集合并在锁外调用 service。
func (w *agentLaunchedWorker) drainPending() {
	for {
		w.mu.Lock()
		if len(w.pending) == 0 {
			w.mu.Unlock()
			return
		}
		batch := make([]agentdto.AgentLaunched, 0, len(w.pending))
		for _, ev := range w.pending {
			batch = append(batch, ev)
		}
		w.pending = map[string]agentdto.AgentLaunched{}
		w.mu.Unlock()
		for _, ev := range batch {
			w.processWithRetry(ev)
		}
	}
}

func (w *agentLaunchedWorker) processWithRetry(ev agentdto.AgentLaunched) {
	for attempt := 1; attempt <= agentLaunchedMaxAttempts; attempt++ {
		err := w.processor.processAgentLaunched(ev)
		if err == nil {
			w.processedTotal.Add(1)
			return
		}
		w.recordFailure(err)
		w.logger.Error("thread: AgentLaunched processing failed",
			"attempt", attempt, "max_attempts", agentLaunchedMaxAttempts, "error", err)
		if attempt == agentLaunchedMaxAttempts {
			w.droppedTotal.Add(1)
			return
		}
		w.retriedTotal.Add(1)
		w.waitRetry(attempt)
	}
}

func (w *agentLaunchedWorker) recordFailure(err error) {
	w.failedTotal.Add(1)
	w.lastErrorMu.Lock()
	w.lastError = err.Error()
	w.lastErrorMu.Unlock()
}

func (w *agentLaunchedWorker) waitRetry(attempt int) {
	delay := w.retryBaseDelay * time.Duration(1<<(attempt-1))
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-timer.C:
	case <-w.stopCh:
	}
}
