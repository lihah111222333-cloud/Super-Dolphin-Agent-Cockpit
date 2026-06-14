package mcpcontrol

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/mcp"
	platformconfig "github.com/anthropic-ai/super-agent-v3/internal/platform/config"
	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
)

// configFanoutNotifier is the subset of contract.ToolNotifier the worker
// uses. Declaring it here lets tests drive the worker with a fake that
// only implements NotifyConfigChanged, without having to also stub the
// other notify verbs (NotifyBySubscription / NotifyByCapability / ...).
type configFanoutNotifier interface {
	NotifyConfigChanged(ctx context.Context, topic string, scope *dto.SelectorScope, configVersion int64, payload json.RawMessage) error
}

// configFanoutWorkerDrainGrace bounds the OnStop wait for pending config-
// change notifies. Matches the other P22 P2 drain budgets so the global
// OnStop cost stays predictable when multiple subsystems are shutting
// down concurrently.
const configFanoutWorkerDrainGrace = 10 * time.Second

type configFanoutRequest struct {
	topic   string
	payload map[string]any
}

// configFanoutWorker is the P22 P2 single owner of the bus-event →
// ToolNotifier.NotifyConfigChanged fanout path.
//
// Pre-P2 shape: `registerConfigChangeSubscriptions` wired 8 bus callbacks
// that synchronously called `publishConfigChanged`, which itself called
// `notifier.NotifyConfigChanged(context.Background(), ...)` on the
// dispatcher goroutine. The `context.Background()` bypass meant there was
// no way for Lifecycle.OnStop to cancel an in-flight notify — it just
// cancelled the subscription and left whatever peer RPC was in progress
// to finish on its own.
//
// P2 shape: the callback only calls Enqueue. A tracked worker drains the
// FIFO queue under its own cancellable fanoutCtx; Stop(ctx) cancels that
// ctx + waits bounded by ctx for the worker to exit, so any peer RPC in
// `ToolNotifier.NotifyConfigChanged` observes ctx.Err() cleanly. This is
// the contract pinned by `TestConfigFanoutWorkerUsesCancelableContext`
// (docs/plans/迁移/p22/P2_BusRuntimeDecoupling.md:415) and the P2 §验收
// bullet "config_change … 不再以 context.Background() 旁路 publish /
// shutdown cancel".
type configFanoutWorker struct {
	notifier configFanoutNotifier
	versions configVersionSource
	logger   *pkglogger.Logger

	fanoutCtx    context.Context
	fanoutCancel context.CancelFunc

	mu    sync.Mutex
	queue []configFanoutRequest

	wake chan struct{}

	startOnce sync.Once
	stopOnce  sync.Once
	stopCh    chan struct{}
	doneCh    chan struct{}

	enqueuedTotal  atomic.Int64
	processedTotal atomic.Int64
}

func newConfigFanoutWorker(notifier configFanoutNotifier, versions configVersionSource, logger *pkglogger.Logger) *configFanoutWorker {
	if logger == nil {
		logger = pkglogger.Get()
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &configFanoutWorker{
		notifier:     notifier,
		versions:     versions,
		logger:       logger,
		fanoutCtx:    ctx,
		fanoutCancel: cancel,
		wake:         make(chan struct{}, 1),
		stopCh:       make(chan struct{}),
		doneCh:       make(chan struct{}),
	}
}

// Start spawns the worker goroutine. Idempotent. When notifier/versions is
// nil the worker short-circuits: doneCh closes immediately so Stop is a
// no-op and Enqueue remains a cheap silent drop.
// Start 启动平台mcpcontrol流程。
func (w *configFanoutWorker) Start() {
	if w == nil {
		return
	}
	w.startOnce.Do(func() {
		if w.notifier == nil || w.versions == nil {
			close(w.doneCh)
			return
		}
		go func() {
			defer func() {
				if rec := recover(); rec != nil {
					pkglogger.Error("mcpcontrol: recovered config_fanout_worker panic", "panic", rec)
				}
			}()
			w.runWorker()
		}()
	})
}

// Enqueue queues a config-change fanout request. Safe to call from bus
// callbacks: O(1) slice append + non-blocking wake signal, no Notify call
// on the callback goroutine. Post-Stop calls are silently dropped.
// Enqueue 把项目追加到队尾。
func (w *configFanoutWorker) Enqueue(topic string, payload map[string]any) {
	if w == nil {
		return
	}
	if strings.TrimSpace(topic) == "" {
		return
	}
	select {
	case <-w.stopCh:
		return
	default:
	}
	w.mu.Lock()
	w.queue = append(w.queue, configFanoutRequest{topic: topic, payload: payload})
	w.mu.Unlock()
	w.enqueuedTotal.Add(1)
	select {
	case w.wake <- struct{}{}:
	default:
	}
}

// FanoutCtx returns the cancellable ctx the worker passes to
// NotifyConfigChanged. Exposed for tests that need to observe the
// cancellation plumbing without poking internal fields.
// FanoutCtx 处理fanoutctx。
func (w *configFanoutWorker) FanoutCtx() context.Context {
	if w == nil {
		return context.Background()
	}
	return w.fanoutCtx
}

// Stop closes the gate, cancels fanoutCtx, drains pending requests, and
// waits bounded by ctx for the worker to exit. Idempotent.
// Stop 停止平台mcpcontrol流程。
func (w *configFanoutWorker) Stop(ctx context.Context) error {
	if w == nil {
		return nil
	}
	var firstErr error
	w.stopOnce.Do(func() {
		close(w.stopCh)
		w.fanoutCancel()
		waitCtx := ctx
		if waitCtx == nil {
			waitCtx = context.Background()
		}
		if deadline, ok := waitCtx.Deadline(); !ok || time.Until(deadline) > configFanoutWorkerDrainGrace {
			var cancel context.CancelFunc
			waitCtx, cancel = platformconfig.WithTimeout(waitCtx, configFanoutWorkerDrainGrace)
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
func (w *configFanoutWorker) EnqueuedTotal() int64 { return w.enqueuedTotal.Load() }

// ProcessedTotal 处理processedtotal。
func (w *configFanoutWorker) ProcessedTotal() int64 { return w.processedTotal.Load() }

func (w *configFanoutWorker) runWorker() {
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
// with the lock released, preserving FIFO order. FIFO is load-bearing
// because advanceConfigVersion is serialized through the worker: peers
// observe monotonically increasing configVersion in exactly the same
// order the bus events enqueued.
func (w *configFanoutWorker) drainPending() {
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

// dispatch 派发平台mcpcontrol。
func (w *configFanoutWorker) dispatch(req configFanoutRequest) {
	raw, err := json.Marshal(req.payload)
	if err != nil {
		if w.logger != nil {
			w.logger.Warn("mcp config change marshal failed", "topic", req.topic, "err", err)
		}
		return
	}
	configVersion := w.versions.advanceConfigVersion()
	scope := configChangeSelectorScope(req.payload)
	if err := w.notifier.NotifyConfigChanged(w.fanoutCtx, req.topic, scope, configVersion, raw); err != nil {
		if w.logger != nil {
			w.logger.Warn("mcp config change notify failed", "topic", req.topic, "config_version", configVersion, "err", err)
		}
	}
	if dispatcher, ok := w.notifier.(lspReleaseScopeDispatcher); ok {
		if releaseReq, shouldDispatch := releaseScopeRequestFromConfigPayload(req.payload); shouldDispatch {
			if _, err := dispatcher.DispatchLSPReleaseScope(w.fanoutCtx, releaseReq); err != nil && w.logger != nil {
				w.logger.Warn("mcp lsp release-scope dispatch failed", "topic", req.topic, "err", err)
			}
		}
	}
}
