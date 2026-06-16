package cron

import (
	"context"
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

// cronProgressDrainGrace bounds the Stop wait for the cronProgressWorker
// so shutdown never hangs on a stuck scheduler call.
const cronProgressDrainGrace = 10 * time.Second

// cronProgressEventKind distinguishes the type of event queued.
type cronProgressEventKind int

const (
	cronExtendClaim cronProgressEventKind = iota
	cronCompleteTurn
)

// cronProgressRequest is the unit of work enqueued by the bus callbacks.
type cronProgressRequest struct {
	kind        cronProgressEventKind
	turnID      string
	success     bool
	terminalErr string
}

// cronProgressWorker is a single-goroutine worker that owns all
// Scheduler DB calls previously done synchronously in bus callbacks.
// Bus callbacks only perform O(1) enqueue; the worker drains and
// dispatches to the appropriate Scheduler method.
//
// bus 回调只入队，真正写库放到单 worker 里做。这样慢 DB 不会拖住事件分发，
// 也能保持本进程内顺序。
type cronProgressWorker struct {
	scheduler *Scheduler
	logger    *pkglogger.Logger

	mu    sync.Mutex
	queue []cronProgressRequest

	wake chan struct{}

	startOnce sync.Once
	stopOnce  sync.Once
	stopCh    chan struct{}
	doneCh    chan struct{}

	enqueuedTotal  atomic.Int64
	processedTotal atomic.Int64
}

func newCronProgressWorker(scheduler *Scheduler, logger *pkglogger.Logger) *cronProgressWorker {
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

// Start spawns the worker goroutine. Idempotent.
// Start 启动订阅或后台处理流程。
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

// Stop closes the gate, drains pending requests, and waits bounded by
// ctx for the worker to exit. Idempotent.
// Stop 停止运行中的代理会话。
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

// Enqueue records a request. Safe to call from bus callbacks: O(1)
// slice append + non-blocking wake signal.
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

func (w *cronProgressWorker) dispatch(req cronProgressRequest) {
	ctx := context.Background()
	switch req.kind {
	case cronExtendClaim:
		// 进度事件只续租，不改 run 状态。
		if err := w.scheduler.ExtendClaimForTurnProgress(ctx, req.turnID); err != nil {
			w.logger.Debug("cron: extend claim for turn progress failed",
				pkglogger.String("turn_id", req.turnID),
				pkglogger.String("error", err.Error()),
			)
		}
	case cronCompleteTurn:
		// 终态事件才把 running run 结束；找不到 run 时让 CompleteTurn 暴露问题。
		if err := w.scheduler.CompleteTurn(ctx, req.turnID, req.success, req.terminalErr); err != nil {
			w.logger.Debug("cron: complete turn from terminal event failed",
				pkglogger.String("turn_id", req.turnID),
				pkglogger.String("error", err.Error()),
			)
		}
	}
}

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
