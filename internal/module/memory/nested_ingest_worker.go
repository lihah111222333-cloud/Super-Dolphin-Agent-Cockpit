package memory

import (
	"context"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/anthropic-ai/super-agent-v3/internal/util/ctxutil"
	"github.com/anthropic-ai/super-agent-v3/internal/util/safego"
	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
)

// nestedIngestDrainGrace 限制 nested ingest worker 的关闭等待时间。
// AddToolReadResult 可能在 drain 阶段读持久化工具输出，超时可避免 RunnerModule
// 关闭被慢磁盘长期卡住。
const nestedIngestDrainGrace = 10 * time.Second

// nestedIngestRuntime 是 worker 依赖的 NestedRuntime 最小接口。
// 合并、去重和关闭控制都留在 worker 内部，测试可用轻量替身验证这些并发边界。
type nestedIngestRuntime interface {
	AddToolReadResult(threadID, toolName, result, persistedPath string)
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
// 最新 payload，慢速文件读取留给 worker drain。
func (w *nestedIngestWorker) Enqueue(threadID, toolName, result, persistedPath string) {
	if w == nil {
		return
	}
	if w.runtime == nil {
		return
	}
	threadID = strings.TrimSpace(threadID)
	if threadID == "" {
		return
	}
	select {
	case <-w.stopCh:
		return
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
	if _, dup := w.pending[key]; dup {
		w.coalescedTotal.Add(1)
	}
	w.pending[key] = req
	w.mu.Unlock()
	w.enqueuedTotal.Add(1)
	select {
	case w.wake <- struct{}{}:
	default:
	}
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
			w.runtime.AddToolReadResult(r.threadID, r.toolName, r.result, r.persistedPath)
			w.processedTotal.Add(1)
		}
	}
}
