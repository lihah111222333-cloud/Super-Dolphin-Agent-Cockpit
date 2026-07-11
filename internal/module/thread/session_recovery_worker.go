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

// sessionRecoveryDrainGrace 限制恢复 worker 停止时等待 goroutine 退出的时间。
// 恢复路径包含 provider 关闭等待和一次 Resume 往返，因此预算比普通线程 worker 更宽。
const sessionRecoveryDrainGrace = 15 * time.Second

// sessionRecoveryReconnectDelay 是 provider 断线后默认等待窗口。
// 这段时间留给 Codex 等 provider 完成旧会话关闭，测试可通过 service.reconnectDelay 覆盖。
const sessionRecoveryReconnectDelay = 3 * time.Second

// sessionRecoverer 是恢复 worker 依赖的最小 service 接口。
// processSessionRecovery 负责限流、驱逐僵尸 session、可取消等待和后台 Resume，worker 只管排队与并发收束。
type sessionRecoverer interface {
	processSessionRecovery(ctx context.Context, ev agentdto.AgentFailed)
}

// sessionRecoveryWorker 是 AgentFailed 事件到会话恢复慢路径之间的排队层。
// pending map 以 threadID 优先、agentID 兜底聚合重复失败事件；inflight WaitGroup
// 允许不同 agent 并行恢复，同时让 Stop 能取消等待并等所有恢复 goroutine 退出。
type sessionRecoveryWorker struct {
	recoverer sessionRecoverer
	logger    *slog.Logger

	// ctx 传入每次 processSessionRecovery；Stop 调用 cancel 后会打断等待窗口和正在恢复的 Resume。
	ctx    context.Context
	cancel context.CancelFunc

	mu      sync.Mutex
	pending map[string]agentdto.AgentFailed

	wake chan struct{}

	startOnce, stopOnce sync.Once
	stopCh, doneCh      chan struct{}

	// inflight 跟踪每个恢复 goroutine，doneCh 关闭前必须等它们全部观察到取消并返回。
	inflight sync.WaitGroup

	enqueuedTotal, coalescedTotal, processedTotal atomic.Int64
}

func newSessionRecoveryWorker(recoverer sessionRecoverer, logger *slog.Logger) *sessionRecoveryWorker {
	if logger == nil {
		logger = pkglogger.Get()
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &sessionRecoveryWorker{recoverer: recoverer, logger: logger, ctx: ctx, cancel: cancel, pending: map[string]agentdto.AgentFailed{}, wake: make(chan struct{}, 1), stopCh: make(chan struct{}), doneCh: make(chan struct{})}
}

// Start 启动恢复分发 goroutine，重复调用只生效一次。
// recoverer 为空时直接关闭 doneCh，让 Stop 立即返回、Enqueue 只保留廉价 no-op 行为。
func (w *sessionRecoveryWorker) Start() {
	if w == nil {
		return
	}
	w.startOnce.Do(func() {
		if w.recoverer == nil {
			close(w.doneCh)
			return
		}
		safego.Go(w.ctx, pkglogger.Get(), "thread.session_recovery.worker", func(context.Context) {
			w.runWorker()
		})
	})
}

// Enqueue 记录一次待恢复的 AgentFailed 事件。
// bus 回调里只做 O(1) map 写入和非阻塞唤醒；同一 target 的重复事件会合并成一次恢复尝试。
func (w *sessionRecoveryWorker) Enqueue(target string, ev agentdto.AgentFailed) {
	if w == nil {
		return
	}
	target = strings.TrimSpace(target)
	if target == "" {
		return
	}
	select {
	case <-w.stopCh:
		return
	default:
	}
	w.mu.Lock()
	if _, dup := w.pending[target]; dup {
		w.coalescedTotal.Add(1)
	}
	w.pending[target] = ev
	w.mu.Unlock()
	w.enqueuedTotal.Add(1)
	select {
	case w.wake <- struct{}{}:
	default:
	}
}

// Stop 关闭入队闸门、取消 worker context，并按 ctx/deadline 等待分发器和恢复 goroutine 退出。
// 等待超时时返回 ctx 错误，调用方可据此记录 drain 风险。
func (w *sessionRecoveryWorker) Stop(ctx context.Context) error {
	if w == nil {
		return nil
	}
	var firstErr error
	w.stopOnce.Do(func() {
		close(w.stopCh)
		w.cancel()
		waitCtx := ctx
		if waitCtx == nil {
			waitCtx = context.Background()
		}
		if deadline, ok := waitCtx.Deadline(); !ok || time.Until(deadline) > sessionRecoveryDrainGrace {
			var cancel context.CancelFunc
			waitCtx, cancel = ctxutil.WithTimeout(waitCtx, sessionRecoveryDrainGrace)
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

// EnqueuedTotal 返回已入队事件数，用于测试和后续指标接入。
func (w *sessionRecoveryWorker) EnqueuedTotal() int64 { return w.enqueuedTotal.Load() }

// CoalescedTotal 返回因同 target 合并而没有新增恢复任务的事件数。
func (w *sessionRecoveryWorker) CoalescedTotal() int64 { return w.coalescedTotal.Load() }

// ProcessedTotal 返回实际执行完恢复处理的事件数。
func (w *sessionRecoveryWorker) ProcessedTotal() int64 { return w.processedTotal.Load() }

func (w *sessionRecoveryWorker) runWorker() {
	// inflight.Wait 必须早于 doneCh 关闭，Stop 才能确认所有恢复 goroutine 已退出。
	defer close(w.doneCh)
	defer w.inflight.Wait()
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

// drainPending 在锁内取出当前 pending 批次，再为每个 target 启动受 WaitGroup 跟踪的恢复 goroutine。
// 批次内不同 agent 仍可并行恢复，但 Stop 可以通过 worker context 和 inflight 等待收束它们。
func (w *sessionRecoveryWorker) drainPending() {
	w.mu.Lock()
	if len(w.pending) == 0 {
		w.mu.Unlock()
		return
	}
	batch := make([]agentdto.AgentFailed, 0, len(w.pending))
	for _, ev := range w.pending {
		batch = append(batch, ev)
	}
	w.pending = map[string]agentdto.AgentFailed{}
	w.mu.Unlock()
	for _, ev := range batch {
		w.inflight.Add(1)
		event := ev
		safego.Go(w.ctx, pkglogger.Get(), "thread.session_recovery.event", func(context.Context) {
			defer w.inflight.Done()
			w.recoverer.processSessionRecovery(w.ctx, event)
			w.processedTotal.Add(1)
		})
	}
}
