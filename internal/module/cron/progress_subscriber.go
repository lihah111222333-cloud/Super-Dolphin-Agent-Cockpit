package cron

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	"github.com/anthropic-ai/super-agent-v3/internal/util/ctxutil"
	"github.com/kelindar/event"

	turndto "github.com/anthropic-ai/super-agent-v3/internal/dto/turn"
	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
)

// cronProgressDrainGrace 限制 cronProgressWorker 停止等待时间，避免卡住进程退出。
const cronProgressDrainGrace = 10 * time.Second

// cronProgressEventKind 区分进度事件（续租）和终态事件（完成 turn）两种类型。
type cronProgressEventKind int

const (
	cronExtendClaim cronProgressEventKind = iota
	cronCompleteTurn
)

// cronProgressRequest 是 cronProgressWorker 队列中的一条工作项。
type cronProgressRequest struct {
	kind        cronProgressEventKind
	turnID      string
	success     bool
	terminalErr string
}

// cronProgressWorker 用单 goroutine 串行执行 progress/terminal 事件引发的 Scheduler 写库。
// bus 回调只入队，真正写库放到单 worker 里做。这样慢 DB 不会拖住事件分发，
// 也能保持本进程内顺序。
type cronProgressWorker struct {
	scheduler *Scheduler
	logger    *slog.Logger

	mu    sync.Mutex
	queue []cronProgressRequest

	wake chan struct{}

	startOnce sync.Once
	stopOnce  sync.Once
	stopCh    chan struct{}
	doneCh    chan struct{}

	enqueuedTotal  atomic.Int64
	processedTotal atomic.Int64
	staleTotal     atomic.Int64
}

// newCronProgressWorker 创建未启动的进度 worker，scheduler 和 logger 均为必填项。
func newCronProgressWorker(scheduler *Scheduler, logger *slog.Logger) *cronProgressWorker {
	if logger == nil {
		logger = pkglogger.Get()
	}
	return &cronProgressWorker{
		scheduler: scheduler,
		logger:    logger,
		wake:      make(chan struct{}, 1),
		stopCh:    make(chan struct{}),
		doneCh:    make(chan struct{}),
	}
}

// Start 启动进度 worker goroutine。
// startOnce 保证幂等，panic 会被记录，避免事件订阅链直接崩溃。
func (w *cronProgressWorker) Start() {
	if w == nil {
		return
	}
	w.startOnce.Do(func() {
		go func() {
			defer func() {
				if rec := recover(); rec != nil {
					pkglogger.Error("cron: recovered progress_worker panic", "panic", rec)
				}
			}()
			w.runWorker()
		}()
	})
}

// Stop 关闭入队入口并等待 worker 排空队列。
// 等待时间受 ctx 和 cronProgressDrainGrace 共同限制，避免 shutdown 被慢写库拖死。
func (w *cronProgressWorker) Stop(ctx context.Context) error {
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
		if deadline, ok := waitCtx.Deadline(); !ok || time.Until(deadline) > cronProgressDrainGrace {
			var cancel context.CancelFunc
			waitCtx, cancel = ctxutil.WithTimeout(waitCtx, cronProgressDrainGrace)
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

// enqueue 将请求追加到队列并发送非阻塞唤醒信号。
func (w *cronProgressWorker) enqueue(req cronProgressRequest) {
	if w == nil {
		return
	}
	select {
	case <-w.stopCh:
		return
	default:
	}
	w.mu.Lock()
	w.queue = append(w.queue, req)
	w.mu.Unlock()
	w.enqueuedTotal.Add(1)
	select {
	case w.wake <- struct{}{}:
	default:
	}
}

// runWorker 是 worker goroutine 的主循环，stopCh 关闭时排干队列后退出。
func (w *cronProgressWorker) runWorker() {
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

// drainPending 批量取出队列中的所有请求并依次 dispatch，不持锁执行 dispatch。
func (w *cronProgressWorker) drainPending() {
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

// dispatch 根据 kind 调用 Scheduler 的对应方法，错误只记录不上传。
func (w *cronProgressWorker) dispatch(req cronProgressRequest) {
	ctx := context.Background()
	switch req.kind {
	case cronExtendClaim:
		// 进度事件只续租，不改 run 状态。
		if err := w.scheduler.ExtendClaimForTurnProgress(ctx, req.turnID); err != nil {
			w.logProgressError("cron: extend claim for turn progress failed", req.turnID, err)
		}
	case cronCompleteTurn:
		// 终态事件才把 running run 结束；找不到 run 时让 CompleteTurn 暴露问题。
		if err := w.scheduler.CompleteTurn(ctx, req.turnID, req.success, req.terminalErr); err != nil {
			w.logProgressError("cron: complete turn from terminal event failed", req.turnID, err)
		}
	}
}

// logProgressError 将 stale/mismatch 事件升级为 warn，并记录累计数，避免迟到终态只停留在 debug。
func (w *cronProgressWorker) logProgressError(message, turnID string, err error) {
	if isCronProgressStaleMismatch(err) {
		total := w.staleTotal.Add(1)
		w.logger.Warn(message,
			slog.String("turn_id", turnID),
			slog.Int64("stale_total", total),
			slog.String("error", err.Error()),
		)
		return
	}
	w.logger.Debug(message,
		slog.String("turn_id", turnID),
		slog.String("error", err.Error()),
	)
}

func isCronProgressStaleMismatch(err error) bool {
	return errors.Is(err, errStoreClaimTokenMismatch) ||
		errors.Is(err, errStoreStatusTransitionRefused) ||
		errors.Is(err, errStoreJobRunNotFound)
}

// subscribeCronProgress 订阅 turn progress 事件并委托给 worker 续租。
func subscribeCronProgress(dispatcher *event.Dispatcher, worker *cronProgressWorker, logger *pkglogger.Logger) context.CancelFunc {
	if logger == nil {
		logger = pkglogger.Get()
	}
	return contract.ResilientSubscribe(dispatcher, func(ev turndto.ItemCompleted) {
		worker.enqueue(cronProgressRequest{
			kind:   cronExtendClaim,
			turnID: ev.TurnID,
		})
	}, logger)
}

// subscribeCronTerminalEvents 订阅 TurnCompleted 和 TurnInterrupted 事件并委托给 worker 处理终态。
func subscribeCronTerminalEvents(dispatcher *event.Dispatcher, worker *cronProgressWorker, logger *pkglogger.Logger) context.CancelFunc {
	if logger == nil {
		logger = pkglogger.Get()
	}
	cancelCompleted := contract.ResilientSubscribe(dispatcher, func(ev turndto.TurnCompleted) {
		worker.enqueue(cronProgressRequest{
			kind:        cronCompleteTurn,
			turnID:      ev.TurnID,
			success:     ev.Success,
			terminalErr: terminalErrorText(ev),
		})
	}, logger)
	cancelInterrupted := contract.ResilientSubscribe(dispatcher, func(ev turndto.TurnInterrupted) {
		worker.enqueue(cronProgressRequest{
			kind:        cronCompleteTurn,
			turnID:      ev.TurnID,
			success:     false,
			terminalErr: "turn interrupted: " + ev.Reason,
		})
	}, logger)
	return func() {
		if cancelCompleted != nil {
			cancelCompleted()
		}
		if cancelInterrupted != nil {
			cancelInterrupted()
		}
	}
}

// terminalErrorText 从 TurnCompleted 事件中提取可读的错误文本。
func terminalErrorText(ev turndto.TurnCompleted) string {
	if ev.Success {
		return ""
	}
	for _, value := range []string{ev.Error, ev.Reason, ev.Status, ev.StopReason, ev.Message} {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return "cron: turn completed unsuccessfully"
}
