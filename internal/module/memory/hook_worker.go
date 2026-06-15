package memory

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	turndto "github.com/anthropic-ai/super-agent-v3/internal/dto/turn"
	"github.com/anthropic-ai/super-agent-v3/internal/util/ctxutil"
	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
)

// memoryHookDrainGrace bounds the Stop wait for the memoryHookWorker
// so shutdown never hangs on a stuck disk I/O call.
const memoryHookDrainGrace = 10 * time.Second

// ---------------------------------------------------------------------------
// memoryHookWorker: async worker for bus callbacks that involve disk I/O
// ---------------------------------------------------------------------------

// memoryHookEventKind distinguishes the two event types the worker handles.
type memoryHookEventKind int

const (
	memoryHookTurnInputReceived memoryHookEventKind = iota
	memoryHookTurnCompleted
)

// memoryHookRequest is the unit of work enqueued by bus callbacks.
type memoryHookRequest struct {
	kind          memoryHookEventKind
	turnInput     turndto.TurnInputReceived
	turnCompleted turndto.TurnCompleted
}

// memoryHookWorker is a single-goroutine worker that owns the disk I/O
// previously done synchronously in onTurnInputReceived and onTurnCompleted
// bus callbacks. Bus callbacks now only perform cheap in-memory checks
// and enqueue the I/O-heavy work here.
type memoryHookWorker struct {
	hooks  *MemoryLifecycleHooks
	logger *pkglogger.Logger

	mu    sync.Mutex
	queue []memoryHookRequest

	wake chan struct{}

	startOnce sync.Once
	stopOnce  sync.Once
	stopCh    chan struct{}
	doneCh    chan struct{}

	enqueuedTotal  atomic.Int64
	processedTotal atomic.Int64
}

func newMemoryHookWorker(hooks *MemoryLifecycleHooks, logger *pkglogger.Logger) *memoryHookWorker {
	if logger == nil {
		logger = pkglogger.Get()
	}
	return &memoryHookWorker{
		hooks:  hooks,
		logger: logger,
		wake:   make(chan struct{}, 1),
		stopCh: make(chan struct{}),
		doneCh: make(chan struct{}),
	}
}

// Start spawns the worker goroutine. Idempotent.
// Start 启动记忆流程。
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

// Stop closes the gate, drains pending requests, and waits bounded by
// ctx for the worker to exit. Idempotent.
// Stop 停止记忆流程。
func (w *memoryHookWorker) Stop(ctx context.Context) error {
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
		if deadline, ok := waitCtx.Deadline(); !ok || time.Until(deadline) > memoryHookDrainGrace {
			var cancel context.CancelFunc
			waitCtx, cancel = ctxutil.WithTimeout(waitCtx, memoryHookDrainGrace)
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

// Enqueue records a hook request. Safe to call from bus callbacks: O(1)
// slice append + non-blocking wake signal.
// Enqueue 把项目追加到队尾。
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
	w.queue = append(w.queue, req)
	w.mu.Unlock()
	w.enqueuedTotal.Add(1)
	select {
	case w.wake <- struct{}{}:
	default:
	}
}

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

func (w *memoryHookWorker) dispatch(req memoryHookRequest) {
	ctx := context.Background()
	switch req.kind {
	case memoryHookTurnInputReceived:
		w.hooks.onTurnInputReceived(ctx, req.turnInput)
	case memoryHookTurnCompleted:
		w.hooks.onTurnCompleted(ctx, req.turnCompleted)
	}
}
