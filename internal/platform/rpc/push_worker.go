package rpc

import (
	"context"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	platformconfig "github.com/anthropic-ai/super-agent-v3/internal/platform/config"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/eventsurface"
	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
)

// pushWorkerDrainGrace bounds OnStop wait for pending RPC push batches.
// Mirrors the other P22 P2 drain budgets (auto-dream / nested ingest /
// hook dispatch / cachekeepalive / config fanout) so total module
// shutdown cost stays predictable.
const pushWorkerDrainGrace = 10 * time.Second

// pushBroadcaster is the subset of rpc.Server the worker uses. Declaring
// it here lets tests fake notify delivery without wiring a full jrpc2
// server, and documents that the worker's only contract with the server
// is "push method+payload to all clients".
type pushBroadcaster interface {
	NotifyAll(ctx context.Context, bridge *PushBridge, method string, params any)
}

type pushRequest struct {
	notifications []eventsurface.Notification
}

// pushNotificationWorker is the P22 P2 single owner of the
// bus-event → Server.NotifyAll slow-path that `push.go` previously drove
// with `broadcastNotifications(context.Background(), ...)` directly on
// the dispatcher callback goroutine.
//
// Pre-P2 shape: both `subscribeCoreEventPushes` and
// `subscribeRawProviderEventPushes` called `broadcastNotifications` with
// `context.Background()`, so every notify RPC ran on the callback
// goroutine and Lifecycle.OnStop could not cancel an in-flight push — the
// `context.Background()` bypass was flagged in
// `docs/plans/迁移/p22/P2_BusRuntimeDecoupling.md:95-108`.
//
// P2 shape: the callback only calls Enqueue with the already-expanded
// `[]eventsurface.Notification` slice (legacy thread/sidebar refresh
// expansion stays deterministic and happens on the callback side so the
// expansion itself is still trivially testable with
// `TestExpandNotificationsAddsLegacyThreadRefresh` etc.). A tracked
// worker drains the FIFO batch queue under its own pushCtx; Stop(ctx)
// cancels that ctx and waits bounded by ctx so any in-flight NotifyAll
// sees ctx.Err() promptly — the contract pinned by the P2 §TDD test
// `TestRPCPushQueuePreservesLegacyExpansion` (P2:415).
type pushNotificationWorker struct {
	server pushBroadcaster
	bridge *PushBridge
	logger *pkglogger.Logger

	pushCtx    context.Context
	pushCancel context.CancelFunc

	mu    sync.Mutex
	queue []pushRequest

	wake chan struct{}

	startOnce sync.Once
	stopOnce  sync.Once
	stopCh    chan struct{}
	doneCh    chan struct{}

	enqueuedTotal   atomic.Int64
	processedTotal  atomic.Int64
	notifySentTotal atomic.Int64
}

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

// Start spawns the worker goroutine. Idempotent. When server/bridge is nil
// the worker short-circuits: doneCh closes immediately so Stop is a no-op
// and Enqueue stays a cheap silent drop.
// Start 启动平台RPC流程。
func (w *pushNotificationWorker) Start() {
	if w == nil {
		return
	}
	w.startOnce.Do(func() {
		if w.server == nil || w.bridge == nil {
			close(w.doneCh)
			return
		}
		go func() {
			defer func() {
				if rec := recover(); rec != nil {
					pkglogger.Error("rpc: recovered push_notification_worker panic", "panic", rec)
				}
			}()
			w.runWorker()
		}()
	})
}

// Enqueue queues a pre-expanded batch of notifications for delivery. Safe
// to call from bus callbacks: O(1) slice append + non-blocking wake; no
// NotifyAll or RPC transport work runs on the callback goroutine.
// Enqueue 把项目追加到队尾。
func (w *pushNotificationWorker) Enqueue(notifications []eventsurface.Notification) {
	if w == nil || len(notifications) == 0 {
		return
	}
	// Callback-side sanity: drop notifications with an empty method so
	// the queue never carries a batch whose first entry is unusable. The
	// legacy expander can technically emit a zero-method refresh if the
	// source method slips past trim, and we want to catch that early.
	filtered := make([]eventsurface.Notification, 0, len(notifications))
	for _, n := range notifications {
		if strings.TrimSpace(n.Method) != "" {
			filtered = append(filtered, n)
		}
	}
	if len(filtered) == 0 {
		return
	}
	select {
	case <-w.stopCh:
		return
	default:
	}
	w.mu.Lock()
	w.queue = append(w.queue, pushRequest{notifications: filtered})
	w.mu.Unlock()
	w.enqueuedTotal.Add(1)
	select {
	case w.wake <- struct{}{}:
	default:
	}
}

// PushCtx returns the cancellable ctx the worker passes to NotifyAll.
// Exposed for tests to assert the no-context.Background() contract.
// PushCtx 处理pushctx。
func (w *pushNotificationWorker) PushCtx() context.Context {
	if w == nil {
		return context.Background()
	}
	return w.pushCtx
}

// Stop closes the gate, cancels pushCtx, drains pending batches, and
// waits bounded by ctx for the worker to exit. Idempotent.
// Stop 停止平台RPC流程。
func (w *pushNotificationWorker) Stop(ctx context.Context) error {
	if w == nil {
		return nil
	}
	var firstErr error
	w.stopOnce.Do(func() {
		close(w.stopCh)
		w.pushCancel()
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
		case <-waitCtx.Done():
			firstErr = waitCtx.Err()
		}
	})
	return firstErr
}

// EnqueuedTotal / ProcessedTotal / NotifySentTotal expose observability
// counters for tests and future metric hookup.
// EnqueuedTotal 处理enqueuedtotal。
func (w *pushNotificationWorker) EnqueuedTotal() int64 { return w.enqueuedTotal.Load() }

// ProcessedTotal 处理processedtotal。
func (w *pushNotificationWorker) ProcessedTotal() int64 { return w.processedTotal.Load() }

// NotifySentTotal 处理notifysenttotal。
func (w *pushNotificationWorker) NotifySentTotal() int64 { return w.notifySentTotal.Load() }

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

// drainPending pops each batch under the lock and iterates NotifyAll with
// the lock released, preserving FIFO order across batches *and* preserving
// the per-batch notification order (which is load-bearing because legacy
// refresh notifications must land after their source event).
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
		// Best-effort compatibility enrich for the frontend transition:
		// preserve standalone ui/thread/patch while adding a copy to the
		// matching source notification when both are still in this drain snapshot.
		reqs = embedThreadPatchRequests(reqs)
		for _, req := range reqs {
			w.dispatch(req)
			w.processedTotal.Add(1)
		}
	}
}

func (w *pushNotificationWorker) dispatch(req pushRequest) {
	for _, n := range req.notifications {
		w.server.NotifyAll(w.pushCtx, w.bridge, n.Method, n.Payload)
		w.notifySentTotal.Add(1)
	}
}
