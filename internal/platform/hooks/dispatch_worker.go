package hooks

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	mcp "github.com/anthropic-ai/super-agent-v3/internal/dto/mcp"
	platformconfig "github.com/anthropic-ai/super-agent-v3/internal/platform/config"
	platformshared "github.com/anthropic-ai/super-agent-v3/internal/platform/kernel"
	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
)

// hookDispatchDrainGrace bounds the OnStop wait for the dispatch worker so
// registerEventRelayLifecycle.OnStop never hangs on a stuck hook peer.
// Mirrors the P22 P2 drain budgets used elsewhere in the memory lane.
const hookDispatchDrainGrace = 10 * time.Second

type hookDispatchRequest struct {
	topic     string
	payload   mcp.HookPayload
	eventTime time.Time
}

// hookDispatchFanout is the subset of *Manager the worker uses. Declaring
// it here keeps the worker testable without spinning up a full hooks
// Manager (registry + dispatcher + resolver + review store).
type hookDispatchFanout interface {
	DispatchAfter(ctx context.Context, topic string, payload mcp.HookPayload) (mcp.AfterDecision, error)
}

// hookDispatchWorker is the P22 P2 single owner of the bus-event → Manager
// DispatchAfter slow-path that event_relay.go previously drove with a
// fire-and-forget `go func()` per event.
//
// Pre-P2 shape: every relayed bus event spawned a one-shot goroutine that
// ran Manager.DispatchAfter with context.Background(); nothing tracked
// those goroutines, so OnStop cancelled subscriptions but could not wait
// for any still-in-flight hook fanout to finish.
//
// P2 shape: the relay callback only calls Enqueue. A single tracked worker
// drains the FIFO queue and runs DispatchAfter serially under the
// worker's own ctx, so Stop(ctx) can guarantee "no in-flight dispatch
// leaks past shutdown" — the invariant the P2 §验收 section pins down as
// "hooks relay 在 shutdown 后无残留 in-flight dispatch 越过 stop".
type hookDispatchWorker struct {
	fanout hookDispatchFanout
	logger *pkglogger.Logger

	mu    sync.Mutex
	queue []hookDispatchRequest

	wake chan struct{}

	startOnce sync.Once
	stopOnce  sync.Once
	stopCh    chan struct{}
	doneCh    chan struct{}

	enqueuedTotal  atomic.Int64
	processedTotal atomic.Int64
}

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

// Start spawns the worker goroutine. Idempotent. When the fanout is nil
// the worker short-circuits: doneCh closes immediately so Stop is a no-op.
// Start 启动平台hooks流程。
func (w *hookDispatchWorker) Start() {
	if w == nil {
		return
	}
	w.startOnce.Do(func() {
		if w.fanout == nil {
			close(w.doneCh)
			return
		}
		go func() {
			defer func() {
				if rec := recover(); rec != nil {
					pkglogger.Error("hooks: recovered dispatch_worker panic", "panic", rec)
				}
			}()
			w.runWorker()
		}()
	})
}

// Enqueue records a hook dispatch request. Safe to call from bus callbacks:
// O(1) slice append + non-blocking wake signal, no DispatchAfter call on
// the callback goroutine. Post-Stop calls are silently dropped because the
// relay subscriptions are about to be cancelled anyway.
// Enqueue 把项目追加到队尾。
func (w *hookDispatchWorker) Enqueue(topic string, eventTime time.Time, payload mcp.HookPayload) {
	if w == nil {
		return
	}
	select {
	case <-w.stopCh:
		return
	default:
	}
	w.mu.Lock()
	w.queue = append(w.queue, hookDispatchRequest{
		topic:     topic,
		payload:   payload,
		eventTime: eventTime,
	})
	w.mu.Unlock()
	w.enqueuedTotal.Add(1)
	select {
	case w.wake <- struct{}{}:
	default:
	}
}

// Stop closes the gate, drains pending requests through the worker, and
// waits bounded by ctx for the worker to exit. Idempotent.
// Stop 停止平台hooks流程。
func (w *hookDispatchWorker) Stop(ctx context.Context) error {
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

// EnqueuedTotal / ProcessedTotal expose the observability counters for
// tests and future metric hookup (P22 observability lane).
// EnqueuedTotal 处理enqueuedtotal。
func (w *hookDispatchWorker) EnqueuedTotal() int64 { return w.enqueuedTotal.Load() }

// ProcessedTotal 处理processedtotal。
func (w *hookDispatchWorker) ProcessedTotal() int64 { return w.processedTotal.Load() }

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

// drainPending pops each queued request under the lock and dispatches it
// with the lock released, preserving FIFO order. DispatchAfter errors are
// logged but never halt the worker — hook peers are individually
// recoverable.
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
