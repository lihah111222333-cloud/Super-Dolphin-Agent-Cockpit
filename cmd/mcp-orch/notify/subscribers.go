package notify

import (
	"context"
	"log/slog"
	"strings"
	"sync/atomic"

	"github.com/kelindar/event"

	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/store/taskdag"
	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	taskdto "github.com/anthropic-ai/super-agent-v3/internal/dto/task"
	platformbus "github.com/anthropic-ai/super-agent-v3/internal/platform/bus"
	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
)

// DAGNotifier holds the orch-specific bus subscribers. It is
// goroutine-safe; every notify action is a non-blocking TryEnqueue on
// the shared core-notifier implementation (reused by orch via its own
// Notifier instance). Callers drive it through fx.Lifecycle hooks;
// tests can also drive Subscribe / Close directly.
type DAGNotifier struct {
	logger   *slog.Logger
	notifier contract.MessageNotifier
	store    taskdag.Store

	skipped       atomic.Int64
	enqueueErrors atomic.Int64
	enqueued      atomic.Int64
}

// NewDAGNotifier wires the orch-side DAG notifier. A nil store is
// tolerated (the subscribers then log + drop every event) so the app
// still boots when the workspace setup doesn't include taskdag.
func NewDAGNotifier(logger *slog.Logger, notifier contract.MessageNotifier, store taskdag.Store) *DAGNotifier {
	if logger == nil {
		logger = pkglogger.Get()
	}
	return &DAGNotifier{logger: logger, notifier: notifier, store: store}
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

// onNodeStatusChanged is the terminal-state handler for DAG nodes. It
// applies the plan's alias hierarchy (node > dag > drop) and enqueues
// a NotifyMessage only when an alias resolves.
func (n *DAGNotifier) onNodeStatusChanged(ev taskdto.TaskNodeStatusChanged) {
	if !isTerminalNodeStatus(ev.NewStatus) {
		return
	}
	dagKey := strings.TrimSpace(ev.DagKey)
	nodeKey := strings.TrimSpace(ev.NodeKey)
	if dagKey == "" || nodeKey == "" {
		return
	}
	// Look up node config + dag metadata. We use a short-lived
	// background context because the subscriber callback has no ctx
	// of its own — the bus dispatcher fires these eagerly.
	ctx := context.Background()
	node := n.findNode(ctx, dagKey, nodeKey)
	dag := n.getDAG(ctx, dagKey)
	alias := resolveNodeAlias(node, dag)
	if alias == "" {
		// drop/error per the plan — no NOTIFY_DEFAULT_CHANNEL fallback.
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
			// Copy so the caller can't mutate the slice-backing array.
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
		b.WriteString(" \u2192 ")
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
	}
}
