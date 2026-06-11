package notify

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/kelindar/event"

	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/store/taskdag"
	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	taskdto "github.com/anthropic-ai/super-agent-v3/internal/dto/task"
	platformbus "github.com/anthropic-ai/super-agent-v3/internal/platform/bus"
	"github.com/anthropic-ai/super-agent-v3/internal/util/ctxutil"
	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
)

// dagNotifyDrainGrace bounds the Stop wait for the DAGNotifier worker
// so fx.Lifecycle.OnStop never hangs on a stuck store query.
const dagNotifyDrainGrace = 10 * time.Second

const defaultDAGNotifyQueueCapacity = 1024

// dagNotifyProcessTimeout bounds each worker lookup cycle so one stuck
// store call cannot permanently block later DAG notifications.
const dagNotifyProcessTimeout = 5 * time.Second

type DAGNotifierOption func(*DAGNotifier)

func WithDAGNotifyQueueCapacity(capacity int) (DAGNotifierOption, error) {
	if capacity <= 0 {
		return nil, fmt.Errorf("notify(orch): dag notifier queue capacity must be positive, got %d", capacity)
	}
	return func(n *DAGNotifier) {
		n.queueCapacity = capacity
	}, nil
}

// dagNotifyRequest is the unit of work enqueued by the bus callback.
type dagNotifyRequest struct {
	ev taskdto.TaskNodeStatusChanged
}

// DAGNotifier holds the orch-specific bus subscribers. The bus callback
// only performs cheap checks and enqueues; a single worker goroutine
// owns all DB queries and TryEnqueue calls so no synchronous I/O runs
// on the dispatcher callback goroutine.
type DAGNotifier struct {
	logger   *slog.Logger
	notifier contract.MessageNotifier
	store    taskdag.Store

	mu            sync.Mutex
	queue         []dagNotifyRequest
	queueCapacity int

	wake chan struct{}

	startOnce sync.Once
	stopOnce  sync.Once
	stopCh    chan struct{}
	doneCh    chan struct{}

	skipped       atomic.Int64
	enqueueErrors atomic.Int64
	enqueued      atomic.Int64
	dropped       atomic.Int64
}

// NewDAGNotifier wires the orch-side DAG notifier. A nil store is
// tolerated (the subscribers then log + drop every event) so the app
// still boots when the workspace setup doesn't include taskdag.
func NewDAGNotifier(logger *slog.Logger, notifier contract.MessageNotifier, store taskdag.Store, opts ...DAGNotifierOption) *DAGNotifier {
	if logger == nil {
		logger = pkglogger.Get()
	}
	n := &DAGNotifier{
		logger:        logger,
		notifier:      notifier,
		store:         store,
		queueCapacity: defaultDAGNotifyQueueCapacity,
		wake:          make(chan struct{}, 1),
		stopCh:        make(chan struct{}),
		doneCh:        make(chan struct{}),
	}
	for _, opt := range opts {
		if opt != nil {
			opt(n)
		}
	}
	return n
}

// Start spawns the worker goroutine. Idempotent.
func (n *DAGNotifier) Start() {
	if n == nil {
		return
	}
	n.startOnce.Do(func() {
		go func() {
			defer func() {
				if rec := recover(); rec != nil {
					pkglogger.Error("notify(orch): recovered dag_notifier_worker panic", "panic", rec)
				}
			}()
			n.runWorker()
		}()
	})
}

// Stop closes the gate, drains pending requests through the worker, and
// waits bounded by ctx for the worker to exit. Idempotent.
func (n *DAGNotifier) Stop(ctx context.Context) error {
	if n == nil {
		return nil
	}
	var firstErr error
	n.stopOnce.Do(func() {
		close(n.stopCh)
		waitCtx := ctx
		if waitCtx == nil {
			waitCtx = context.Background()
		}
		if deadline, ok := waitCtx.Deadline(); !ok || time.Until(deadline) > dagNotifyDrainGrace {
			var cancel context.CancelFunc
			waitCtx, cancel = ctxutil.WithTimeout(waitCtx, dagNotifyDrainGrace)
			defer cancel()
			_ = deadline
		}
		select {
		case <-n.doneCh:
		case <-waitCtx.Done():
			firstErr = waitCtx.Err()
		}
	})
	return firstErr
}

// Run implements platformrunner.Runner. It starts the internal worker,
// blocks until ctx is cancelled, then drains and stops the worker.
// This allows DAGNotifier to be managed by run.Group instead of manual
// goroutine management in fx.Lifecycle hooks.
func (n *DAGNotifier) Run(ctx context.Context) error {
	if n == nil {
		<-ctx.Done()
		return nil
	}
	n.Start()
	<-ctx.Done()
	cleanupCtx, cancel := ctxutil.WithTimeout(context.Background(), dagNotifyDrainGrace)
	defer cancel()
	return n.Stop(cleanupCtx)
}

// Subscribe registers the orch bus subscribers. Returns a cancel that
// tears every subscription down — the fx.Lifecycle wrapper invokes it
// OnStop so no late events arrive after the flusher is shut down.
//
// This method deliberately does NOT subscribe for core turn terminal
// events — those arrive via the hook_consumer processing chain, not
// the orch dispatcher, and will be wired in a follow-up tap PR.
func (n *DAGNotifier) Subscribe(dispatcher *event.Dispatcher, logger *pkglogger.Logger) context.CancelFunc {
	if n == nil || dispatcher == nil || n.notifier == nil {
		return func() {}
	}
	cancels := []context.CancelFunc{
		platformbus.ResilientSubscribe(dispatcher, n.onNodeStatusChanged, logger),
	}
	return func() {
		for _, c := range cancels {
			c()
		}
	}
}

// onNodeStatusChanged is the bus callback for DAG node status changes.
// It performs only cheap checks (terminal status, empty key) and then
// enqueues the event for the worker goroutine to process. No DB queries
// or blocking I/O happen on the bus dispatcher's callback goroutine.
func (n *DAGNotifier) onNodeStatusChanged(ev taskdto.TaskNodeStatusChanged) {
	if !isTerminalNodeStatus(ev.NewStatus) {
		return
	}
	if strings.TrimSpace(ev.DagKey) == "" || strings.TrimSpace(ev.NodeKey) == "" {
		return
	}
	select {
	case <-n.stopCh:
		return
	default:
	}
	n.mu.Lock()
	if len(n.queue) >= n.queueCapacity {
		n.mu.Unlock()
		n.dropped.Add(1)
		n.logger.Warn("notify(orch): dag notifier queue full; dropping event",
			slog.String("dag_key", strings.TrimSpace(ev.DagKey)),
			slog.String("node_key", strings.TrimSpace(ev.NodeKey)),
		)
		return
	}
	n.queue = append(n.queue, dagNotifyRequest{ev: ev})
	n.mu.Unlock()
	select {
	case n.wake <- struct{}{}:
	default:
	}
}

// processEvent runs on the worker goroutine and performs the DB lookups
// + TryEnqueue that were previously done synchronously in the callback.
func (n *DAGNotifier) processEvent(ev taskdto.TaskNodeStatusChanged) {
	dagKey := strings.TrimSpace(ev.DagKey)
	nodeKey := strings.TrimSpace(ev.NodeKey)
	ctx, cancel := ctxutil.WithTimeout(context.Background(), dagNotifyProcessTimeout)
	defer cancel()
	node := n.findNode(ctx, dagKey, nodeKey)
	dag := n.getDAG(ctx, dagKey)
	alias := resolveNodeAlias(node, dag)
	if alias == "" {
		n.skipped.Add(1)
		n.logger.Debug("notify(orch): no alias configured for dag node",
			slog.String("dag_key", dagKey),
			slog.String("node_key", nodeKey),
			slog.String("new_status", ev.NewStatus),
		)
		return
	}
	msg := contract.NotifyMessage{
		Title: nodeTerminalTitle(ev),
		Body:  buildNodeBody(ev, node, dag),
		Level: levelForNodeStatus(ev.NewStatus),
	}
	if err := n.notifier.TryEnqueue(ctx, contract.NotifyRequest{
		ChannelAlias: alias,
		Message:      msg,
	}); err != nil {
		n.enqueueErrors.Add(1)
		n.logger.Warn("notify(orch): enqueue failed",
			slog.String("dag_key", dagKey),
			slog.String("node_key", nodeKey),
			slog.String("alias", alias),
			slog.String("error", err.Error()),
		)
		return
	}
	n.enqueued.Add(1)
}

func (n *DAGNotifier) runWorker() {
	defer close(n.doneCh)
	for {
		select {
		case <-n.stopCh:
			n.drainPending()
			return
		case <-n.wake:
			n.drainPending()
		}
	}
}

func (n *DAGNotifier) drainPending() {
	for {
		n.mu.Lock()
		if len(n.queue) == 0 {
			n.mu.Unlock()
			return
		}
		reqs := n.queue
		n.queue = nil
		n.mu.Unlock()
		for _, req := range reqs {
			n.processEvent(req.ev)
		}
	}
}

// findNode pulls the node row matching the event. ListNodes is the
// cheapest path currently available; if performance becomes an issue
// taskdag could add GetNode(dagKey, nodeKey) — but that's a store-
// level change outside this PR.
func (n *DAGNotifier) findNode(ctx context.Context, dagKey, nodeKey string) *taskdag.Node {
	if n.store == nil {
		return nil
	}
	nodes, err := n.store.ListNodes(ctx, dagKey)
	if err != nil {
		n.logger.Debug("notify(orch): list nodes failed",
			slog.String("dag_key", dagKey),
			slog.String("error", err.Error()),
		)
		return nil
	}
	for i := range nodes {
		if nodes[i].NodeKey == nodeKey {
			out := nodes[i]
			return &out
		}
	}
	return nil
}

func (n *DAGNotifier) getDAG(ctx context.Context, dagKey string) *taskdag.DAG {
	if n.store == nil {
		return nil
	}
	dag, err := n.store.GetDAG(ctx, dagKey)
	if err != nil {
		n.logger.Debug("notify(orch): get dag failed",
			slog.String("dag_key", dagKey),
			slog.String("error", err.Error()),
		)
		return nil
	}
	return dag
}

// buildNodeBody assembles a human-readable body with the key
// identifying fields. We intentionally do not include node.Result /
// dag.Metadata in full because those may contain user / customer data;
// platform.NormalizeBody in the flusher will re-escape anyway but
// keeping the surface small reduces accidental exposure.
func buildNodeBody(ev taskdto.TaskNodeStatusChanged, node *taskdag.Node, dag *taskdag.DAG) string {
	var b strings.Builder
	b.WriteString("DAG: ")
	b.WriteString(strings.TrimSpace(ev.DagKey))
	if dag != nil && strings.TrimSpace(dag.Title) != "" {
		b.WriteString(" (")
		b.WriteString(strings.TrimSpace(dag.Title))
		b.WriteString(")")
	}
	b.WriteString("\nNode: ")
	b.WriteString(strings.TrimSpace(ev.NodeKey))
	if node != nil && strings.TrimSpace(node.Title) != "" {
		b.WriteString(" (")
		b.WriteString(strings.TrimSpace(node.Title))
		b.WriteString(")")
	}
	b.WriteString("\nStatus: ")
	if old := strings.TrimSpace(ev.OldStatus); old != "" {
		b.WriteString(old)
		b.WriteString(" → ")
	}
	b.WriteString(strings.TrimSpace(ev.NewStatus))
	if turn := strings.TrimSpace(ev.ActiveTurnID); turn != "" {
		b.WriteString("\nTurn: ")
		b.WriteString(turn)
	}
	return b.String()
}

// levelForNodeStatus maps the terminal status into NotifyLevel so the
// platform renderer picks a matching colour / icon.
func levelForNodeStatus(status string) contract.NotifyLevel {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "failed", "error":
		return contract.NotifyLevelError
	case "cancelled", "canceled":
		return contract.NotifyLevelWarn
	default:
		return contract.NotifyLevelInfo
	}
}

// Metrics returns the subscriber's counters. Read-only snapshot for
// dashboards / /metrics endpoints.
type Metrics struct {
	Skipped       int64
	Enqueued      int64
	EnqueueErrors int64
	Dropped       int64
}

// Metrics returns a snapshot of subscriber counters.
func (n *DAGNotifier) Metrics() Metrics {
	if n == nil {
		return Metrics{}
	}
	return Metrics{
		Skipped:       n.skipped.Load(),
		Enqueued:      n.enqueued.Load(),
		EnqueueErrors: n.enqueueErrors.Load(),
		Dropped:       n.dropped.Load(),
	}
}
