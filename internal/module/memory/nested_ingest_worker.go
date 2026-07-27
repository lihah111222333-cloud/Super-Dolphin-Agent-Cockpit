package memory

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/util/ctxutil"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/util/safego"
	pkglogger "github.com/lihah111222333-cloud/super-dolphin-agent/pkg/logger"
)

// nestedIngestDrainGrace 限制 nested ingest worker 的关闭等待时间。
// AddToolReadResult 可能在 drain 阶段读持久化工具输出，超时可避免 RunnerModule
// 关闭被慢磁盘长期卡住。
const (
	nestedIngestDrainGrace = 10 * time.Second
	// nestedIngestPendingLimit 限制尚未交给 runtime 的不同 ingest key 数量。
	nestedIngestPendingLimit = 256
	// nestedIngestResultByteLimit 限制单条 ToolCallEnd 请求所有字符串在队列中的驻留字节数。
	nestedIngestResultByteLimit = 1 << 20
)

var (
	ErrNestedIngestUnavailable = errors.New("nested ingest: runtime is unavailable")
	ErrNestedIngestStopped     = errors.New("nested ingest: worker is stopped")
	ErrNestedIngestInvalid     = errors.New("nested ingest: thread id is required")
	ErrNestedIngestQueueFull   = errors.New("nested ingest: pending queue limit exceeded")
	ErrNestedIngestResultLarge = errors.New("nested ingest: result exceeds byte limit")
)

// nestedIngestRuntime 是 worker 依赖的 NestedRuntime 最小接口。
// 合并、去重和关闭控制都留在 worker 内部，测试可用轻量替身验证这些并发边界。
type nestedIngestRuntime interface {
	AddToolReadResult(threadID, toolName, result, persistedPath string) error
}

// nestedIngestKey 标识可合并的 nested ingest 请求。
// 同一 thread、tool、persistedPath 的重复事件只保留最新 payload；关闭前的 pending
// 请求必须由 worker drain，不能在总线回调里做慢速读盘。
type nestedIngestKey struct {
	threadID      string
	toolName      string
	persistedPath string
}

type nestedIngestRequest struct {
	threadID      string
	toolName      string
	result        string
	persistedPath string
}

// nestedIngestWorker 负责把 ToolCallEnd 事件转交给 NestedRuntime 的慢路径处理。
// 总线回调只入队和发 wake 信号；真正的工具输出解析、持久化文件读取和 nested trigger
// 都在单 worker goroutine 内串行执行，避免阻塞 dispatcher。
// Stop 会关闭入口并在 ctx 限制内 drain pending 请求，保证关闭过程有边界。
type nestedIngestWorker struct {
	runtime nestedIngestRuntime
	logger  *slog.Logger

	mu      sync.Mutex
	pending map[nestedIngestKey]nestedIngestRequest

	wake chan struct{}

	startOnce sync.Once
	stopOnce  sync.Once
	doneOnce  sync.Once
	started   atomic.Bool
	stopCh    chan struct{}
	doneCh    chan struct{}

	enqueuedTotal  atomic.Int64
	coalescedTotal atomic.Int64
	processedTotal atomic.Int64
	rejectedTotal  atomic.Int64
	droppedTotal   atomic.Int64
	failedTotal    atomic.Int64
}

func newNestedIngestWorker(runtime nestedIngestRuntime, logger *slog.Logger) *nestedIngestWorker {
	if logger == nil {
		logger = pkglogger.Get()
	}
	return &nestedIngestWorker{
		runtime: runtime,
		logger:  logger,
		pending: map[nestedIngestKey]nestedIngestRequest{},
		wake:    make(chan struct{}, 1),
		stopCh:  make(chan struct{}),
		doneCh:  make(chan struct{}),
	}
}

// Start 幂等启动 worker goroutine。
// runtime 缺失时立即关闭 doneCh，使 Stop 可快速返回，Enqueue 也保持为轻量 no-op。
func (w *nestedIngestWorker) Start() {
	if w == nil {
		return
	}
	w.startOnce.Do(func() {
		if w.runtime == nil {
			w.closeDone()
			return
		}
		select {
		case <-w.stopCh:
			w.closeDone()
			return
		default:
		}
		w.started.Store(true)
		safego.Go(context.Background(), pkglogger.Get(), "memory.nested_ingest.worker", func(context.Context) {
			w.runWorker()
		})
	})
}

// Enqueue 记录一次 ToolCallEnd nested ingest 请求。
// 该方法只做 O(1) 内存写入和非阻塞 wake，适合总线回调调用；同 key 重复事件会合并为
// 最新 payload，慢速文件读取留给 worker drain。拒绝原因通过 error 和指标显式暴露。
func (w *nestedIngestWorker) Enqueue(threadID, toolName, result, persistedPath string) error {
	if w == nil {
		return ErrNestedIngestUnavailable
	}
	if w.runtime == nil {
		return w.reject(ErrNestedIngestUnavailable)
	}
	threadID = strings.TrimSpace(threadID)
	if threadID == "" {
		return w.reject(ErrNestedIngestInvalid)
	}
	requestBytes := len(threadID) + len(toolName) + len(result) + len(persistedPath)
	if requestBytes > nestedIngestResultByteLimit {
		return w.reject(fmt.Errorf(
			"%w: bytes=%d limit=%d",
			ErrNestedIngestResultLarge,
			requestBytes,
			nestedIngestResultByteLimit,
		))
	}
	select {
	case <-w.stopCh:
		return w.reject(ErrNestedIngestStopped)
	default:
	}
	key := nestedIngestKey{
		threadID:      threadID,
		toolName:      strings.TrimSpace(toolName),
		persistedPath: strings.TrimSpace(persistedPath),
	}
	req := nestedIngestRequest{
		threadID:      threadID,
		toolName:      key.toolName,
		result:        result,
		persistedPath: key.persistedPath,
	}
	w.mu.Lock()
	_, duplicate := w.pending[key]
	if !duplicate && len(w.pending) >= nestedIngestPendingLimit {
		w.mu.Unlock()
		return w.reject(fmt.Errorf(
			"%w: pending=%d limit=%d",
			ErrNestedIngestQueueFull,
			nestedIngestPendingLimit,
			nestedIngestPendingLimit,
		))
	}
	if duplicate {
		w.coalescedTotal.Add(1)
	}
	w.pending[key] = req
	w.mu.Unlock()
	w.enqueuedTotal.Add(1)
	select {
	case w.wake <- struct{}{}:
	default:
	}
	return nil
}

// Stop 关闭入队入口，drain pending 请求，并在 ctx 限制内等待 worker 退出。
// Stop 后的新事件会被拒绝；这是关闭订阅后的唯一丢弃路径，可避免已取消生命周期里继续读盘。
func (w *nestedIngestWorker) Stop(ctx context.Context) error {
	if w == nil {
		return nil
	}
	var firstErr error
	w.stopOnce.Do(func() {
		close(w.stopCh)
		if !w.started.Load() && w.runtime != nil {
			w.drainPending()
			w.closeDone()
			return
		}
		waitCtx := ctx
		if waitCtx == nil {
			waitCtx = context.Background()
		}
		if deadline, ok := waitCtx.Deadline(); !ok || time.Until(deadline) > nestedIngestDrainGrace {
			var cancel context.CancelFunc
			waitCtx, cancel = ctxutil.WithTimeout(waitCtx, nestedIngestDrainGrace)
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

// EnqueuedTotal 返回已接收的 nested ingest 请求数量，供测试和指标读取。
func (w *nestedIngestWorker) EnqueuedTotal() int64 { return w.enqueuedTotal.Load() }

// CoalescedTotal 返回被同 key 合并的请求数量，用于观察高频工具输出是否被正确压缩。
func (w *nestedIngestWorker) CoalescedTotal() int64 { return w.coalescedTotal.Load() }

// ProcessedTotal 返回已交给 NestedRuntime 处理的请求数量。
func (w *nestedIngestWorker) ProcessedTotal() int64 { return w.processedTotal.Load() }

// RejectedTotal 返回因无效输入、停止、超限或无 runtime 被拒绝的请求数。
func (w *nestedIngestWorker) RejectedTotal() int64 { return w.rejectedTotal.Load() }

// DroppedTotal 返回因 thread stop 主动从 pending 队列移除的请求数。
func (w *nestedIngestWorker) DroppedTotal() int64 { return w.droppedTotal.Load() }

// FailedTotal 返回 runtime 处理返回错误的请求数。
func (w *nestedIngestWorker) FailedTotal() int64 { return w.failedTotal.Load() }

// DropThread 删除指定 thread 尚未处理的请求，返回删除数量。
func (w *nestedIngestWorker) DropThread(threadID string) int {
	if w == nil {
		return 0
	}
	threadID = strings.TrimSpace(threadID)
	if threadID == "" {
		return 0
	}
	w.mu.Lock()
	dropped := 0
	for key := range w.pending {
		if key.threadID == threadID {
			delete(w.pending, key)
			dropped++
		}
	}
	w.mu.Unlock()
	if dropped > 0 {
		w.droppedTotal.Add(int64(dropped))
	}
	return dropped
}

// reject 记录 admission 拒绝并原样返回错误。
func (w *nestedIngestWorker) reject(err error) error {
	if w != nil {
		w.rejectedTotal.Add(1)
	}
	return err
}

// runWorker 串行响应 stop 和 wake 信号。
// 每次唤醒都会 drain 当前 pending 快照，避免处理过程中持锁或阻塞新的 Enqueue。
func (w *nestedIngestWorker) runWorker() {
	defer w.closeDone()
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

// closeDone 幂等关闭 doneCh。
// Start 的 nil runtime 快路径、正常 worker 退出和 Stop 前置 drain 都会触达这里。
func (w *nestedIngestWorker) closeDone() {
	w.doneOnce.Do(func() {
		close(w.doneCh)
	})
}

// drainPending 在锁内取出当前 pending 快照，再释放锁逐条调用 NestedRuntime。
// 这样 AddToolReadResult 内部可能发生的持久化文件读取不会阻塞总线回调或新的 Enqueue。
func (w *nestedIngestWorker) drainPending() {
	for {
		w.mu.Lock()
		if len(w.pending) == 0 {
			w.mu.Unlock()
			return
		}
		reqs := make([]nestedIngestRequest, 0, len(w.pending))
		for _, r := range w.pending {
			reqs = append(reqs, r)
		}
		w.pending = map[nestedIngestKey]nestedIngestRequest{}
		w.mu.Unlock()
		for _, r := range reqs {
			if err := w.runtime.AddToolReadResult(r.threadID, r.toolName, r.result, r.persistedPath); err != nil {
				w.failedTotal.Add(1)
				if w.logger != nil {
					w.logger.Warn("memory: nested ingest request failed",
						"thread_id", r.threadID,
						"tool_name", r.toolName,
						"error", err,
					)
				}
				continue
			}
			w.processedTotal.Add(1)
		}
	}
}
