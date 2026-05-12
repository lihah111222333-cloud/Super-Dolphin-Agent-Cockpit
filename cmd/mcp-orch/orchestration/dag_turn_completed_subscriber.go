package orchestration

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"

	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/store/taskdag"
	turndto "github.com/anthropic-ai/super-agent-v3/internal/dto/turn"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/bus"
	platformdb "github.com/anthropic-ai/super-agent-v3/internal/platform/db"
	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
	"github.com/jackc/pgx/v5"
	"github.com/kelindar/event"
	"go.uber.org/fx"
)

// completeNodeResultCap is the 4KB upper bound ADR-006 places on
// task_dag_nodes.result. ADR-017 v1.2 §2.7 marks values exceeding this cap
// with a metric counter; A1 itself does not attempt jsonb merge (that goes
// into ADR-018 / A2).
const completeNodeResultCap = 4 * 1024

// isTerminalNodeStatus is the application-side idempotency short-circuit
// for ADR-017 v1.2 §2.6 race C. The subscriber checks this BEFORE invoking
// CompleteNode/FailNode so it can skip SQL when another path (fallback /
// duplicate TurnCompleted / retry) already landed a terminal state.
//
// SQL fences (CompleteTaskDagNode / FailNode UpdateNodeStatusFlexible)
// still guard from below — this is a defense-in-depth check, not the only
// barrier.
func isTerminalNodeStatus(status string) bool {
	switch strings.TrimSpace(strings.ToLower(status)) {
	case "done", "failed", "cancelled", "skipped":
		return true
	}
	return false
}

// DAGSubscriberDeps is the fx.In bundle injected into
// RegisterDAGTurnCompletedSubscriber. Every dependency is a narrow port —
// the subscriber never sees the aggregate taskdag.Store or *service.
//
// The HookConsumer thread.stopped DAG fallback (commit 6) reuses
// LookupStore + FlowStore; AgentThreads / SvcStopper are subscriber-only.
type DAGSubscriberDeps struct {
	fx.In

	LookupStore  taskdag.NodeSpawningThreadLookup
	FlowStore    taskdag.NodeFlowStore
	AgentThreads AgentThreadLookup
	SvcStopper   StopAgentService
}

// RegisterDAGTurnCompletedSubscriber wires the third TurnCompleted
// subscriber (ADR-017 v1.2 §2.1). It is independent of
// service.go RegisterTurnLifecycle (which advances agent runtime state) and
// hook_consumer.go handleTurnCompleted (which also advances agent runtime).
//
// The three paths split responsibilities:
//   - service.go:RegisterTurnLifecycle → agent runtime + svc.CompleteTurn
//   - hook_consumer.go:handleTurnCompleted → agent runtime
//   - A1 (this file) → DAG store status machine (advance node done/failed,
//     fire stop_helper, schedule/cancel downstream)
//
// No overlap with the other two: turn_lifecycle.go's
// handleTurnCompletedEventWithCtx does NOT touch the DAG store (verified
// at ADR-017 v1.2 §2.1 review).
//
// fx lifecycle:
//   - OnStart: build an independent lifecycleCtx via
//     context.WithCancel(context.Background()) — do NOT use the OnStart
//     ctx, which is cancelled when OnStart returns (project A-P0-3
//     mistake the original draft made). Subscribe with bus.ResilientSubscribe
//     so panics in the handler are recovered and the dispatcher keeps
//     delivering events to the other two subscribers.
//   - OnStop: cancel the lifecycleCtx and the subscriber returned by
//     bus.ResilientSubscribe so OnStop blocks return cleanly.
func RegisterDAGTurnCompletedSubscriber(
	lc fx.Lifecycle,
	dispatcher *event.Dispatcher,
	deps DAGSubscriberDeps,
	logger *slog.Logger,
) {
	if logger == nil {
		logger = pkglogger.Get()
	}
	var cancelSub = func() {}
	var (
		lifecycleCtx    context.Context
		lifecycleCancel context.CancelFunc
	)
	lc.Append(fx.Hook{
		OnStart: func(context.Context) error {
			// v1.1 修正：独立 lifecycleCtx，不复用 OnStart ctx（后者 return 即取消）。
			lifecycleCtx, lifecycleCancel = context.WithCancel(context.Background())
			cancelSub = bus.ResilientSubscribe(dispatcher, func(ev turndto.TurnCompleted) {
				if lifecycleCtx.Err() != nil {
					return // OnStop 后丢事件
				}
				handleDAGTurnCompleted(lifecycleCtx, deps, logger, ev)
			}, logger)
			return nil
		},
		OnStop: func(context.Context) error {
			if lifecycleCancel != nil {
				lifecycleCancel()
			}
			cancelSub()
			return nil
		},
	})
}

// handleDAGTurnCompleted is the per-event entry point invoked by the
// resilient subscriber. The function is split out so unit tests can drive
// it directly without booting fx.
//
// Flow (ADR-017 v1.2 §2.8):
//  1. Reverse lookup nodes carrying ev.ThreadID via LookupStore. Empty
//     result → metric LookupNoNode, return. DB error → metric LookupFailed,
//     return.
//  2. N>1 result → metric LookupDirtyData, iterate every row (the partial
//     index is non-UNIQUE — retry / recovery chains can dual-mount).
//  3. For each node:
//     a. Short-circuit if already terminal (race C / duplicate
//        TurnCompleted) — metric IdempotentSkipped.
//     b. Build CompleteNodeInput.Result = ev.Result (string passthrough;
//        ADR-018 will rework into jsonb merge). Empty ev.Result fires
//        metric CompleteResultEmpty as a soft alarm.
//     c. ev.Success=true → FlowStore.CompleteNodeAndScheduleDownstream
//        (metric CompleteDone / size-cap / DB err).
//        ev.Success=false → FlowStore.FailNodeAndCancelDownstream
//        (metric CompleteFailed).
//  4. After the DB advance, call stop_helper.StopSpawnedAgent (ADR-016
//     §3.2 hard constraint — never inline the 5 contracts). Failure is a
//     Warn log; the subscriber does NOT propagate the error and continues
//     to the next node.
//
// Important guarantees:
//   - ctx.Err() short-circuit on every iteration (lifecycle cancel propagation).
//   - The subscriber never invokes nodeexec / dispatcher — it only operates
//     on store-level narrow ports.
func handleDAGTurnCompleted(
	ctx context.Context,
	deps DAGSubscriberDeps,
	logger *slog.Logger,
	ev turndto.TurnCompleted,
) {
	threadID := strings.TrimSpace(ev.ThreadID)
	if threadID == "" {
		// No thread id — nothing to reverse-lookup. Counts as LookupNoNode for
		// observability symmetry with the empty-result branch.
		dagSubscriberMetrics.IncLookupNoNode()
		return
	}
	if deps.LookupStore == nil || deps.FlowStore == nil {
		logger.Warn("dag subscriber: deps not wired", "thread_id", threadID)
		dagSubscriberMetrics.IncLookupFailed()
		return
	}

	nodes, err := deps.LookupStore.LookupNodesBySpawningThread(ctx, threadID)
	if err != nil {
		dagSubscriberMetrics.IncLookupFailed()
		logger.Warn("dag subscriber: lookup nodes by spawning thread failed",
			"thread_id", threadID, "error", err)
		return
	}
	if len(nodes) == 0 {
		dagSubscriberMetrics.IncLookupNoNode()
		logger.Debug("dag subscriber: no node carries this thread id",
			"thread_id", threadID)
		// Still call stop_helper — agent may exist (race against
		// dispatchAgent recording spawning_thread_id). Failure is logged.
		stopSpawnedAgentForSubscriber(ctx, deps, logger, threadID)
		return
	}
	if len(nodes) > 1 {
		dagSubscriberMetrics.IncLookupDirtyData()
		logger.Warn("dag subscriber: N>1 nodes carry the same spawning thread id",
			"thread_id", threadID, "node_count", len(nodes))
	}

	for i := range nodes {
		if ctx.Err() != nil {
			return
		}
		advanceNodeForTurnCompleted(ctx, deps.FlowStore, logger, &nodes[i], ev)
	}
	// stop_helper after DB advance (DB is source of truth — stop failure
	// does not affect DAG state). Called once per event regardless of
	// node count: each node's spawning_thread_id is the same threadID.
	stopSpawnedAgentForSubscriber(ctx, deps, logger, threadID)
}

// advanceNodeForTurnCompleted is the per-node branch of handleDAGTurnCompleted.
// Kept separate so the loop in handleDAGTurnCompleted stays short — and
// unit tests can target the branch independently.
func advanceNodeForTurnCompleted(
	ctx context.Context,
	flow taskdag.NodeFlowStore,
	logger *slog.Logger,
	node *taskdag.Node,
	ev turndto.TurnCompleted,
) {
	if isTerminalNodeStatus(node.Status) {
		dagSubscriberMetrics.IncIdempotentSkipped()
		logger.Debug("dag subscriber: node already terminal, skip",
			"dag_key", node.DagKey, "node_key", node.NodeKey, "status", node.Status)
		return
	}
	resultBytes := encodeTurnResultForNodeUpdate(ev.Result)
	if len(resultBytes) > completeNodeResultCap {
		dagSubscriberMetrics.IncCompleteSizeCapExceeded()
		logger.Warn("dag subscriber: complete result exceeds ADR-006 4KB cap",
			"dag_key", node.DagKey, "node_key", node.NodeKey, "size", len(resultBytes))
		// Continue anyway — store layer will return validation error which
		// we log below. We do NOT proactively reject (ADR-018 will rework).
	}
	if strings.TrimSpace(ev.Result) == "" {
		dagSubscriberMetrics.IncCompleteResultEmpty()
	}

	if ev.Success {
		advanceNodeDone(ctx, flow, logger, node, resultBytes)
		return
	}
	advanceNodeFailed(ctx, flow, logger, node, ev)
}

// advanceNodeDone calls CompleteNodeAndScheduleDownstream and records the
// CompleteDone metric. DB errors are Warn-logged but not propagated (the
// subscriber's contract is fire-and-forget per ADR-017 §2.8).
func advanceNodeDone(
	ctx context.Context,
	flow taskdag.NodeFlowStore,
	logger *slog.Logger,
	node *taskdag.Node,
	result json.RawMessage,
) {
	_, err := flow.CompleteNodeAndScheduleDownstream(ctx, taskdag.CompleteNodeInput{
		Status:  "done",
		Result:  result,
		DagKey:  node.DagKey,
		NodeKey: node.NodeKey,
	})
	switch {
	case err == nil:
		dagSubscriberMetrics.IncCompleteDone()
	case errors.Is(err, pgx.ErrNoRows) || platformdb.IsNotFound(err):
		// SQL fence rejection — another path (fallback / duplicate event)
		// already pushed terminal status. Count as idempotent skip.
		dagSubscriberMetrics.IncIdempotentSkipped()
		logger.Debug("dag subscriber: complete fence rejected, node already terminal",
			"dag_key", node.DagKey, "node_key", node.NodeKey)
	default:
		logger.Warn("dag subscriber: complete node failed",
			"dag_key", node.DagKey, "node_key", node.NodeKey, "error", err)
	}
}

// advanceNodeFailed calls FailNodeAndCancelDownstream with a synthesized
// reason carrying the turn error so the cascade downstream can identify
// the root cause.
//
// FailFast=false: A1 does not aggressively cancel sibling subgraphs — that
// policy belongs to dispatcher.RetryPolicy. The subscriber only marks the
// primary node failed; cascade decision is left to the store's FailNode SQL
// (which respects FailFast).
func advanceNodeFailed(
	ctx context.Context,
	flow taskdag.NodeFlowStore,
	logger *slog.Logger,
	node *taskdag.Node,
	ev turndto.TurnCompleted,
) {
	reason := strings.TrimSpace(ev.Error)
	if reason == "" {
		reason = strings.TrimSpace(ev.Reason)
	}
	if reason == "" {
		reason = "turn_completed_failure"
	}
	_, err := flow.FailNodeAndCancelDownstream(ctx, taskdag.FailNodeInput{
		DagKey:   node.DagKey,
		NodeKey:  node.NodeKey,
		Reason:   reason,
		FailFast: false,
	})
	switch {
	case err == nil:
		dagSubscriberMetrics.IncCompleteFailed()
	case errors.Is(err, pgx.ErrNoRows) || platformdb.IsNotFound(err):
		dagSubscriberMetrics.IncIdempotentSkipped()
		logger.Debug("dag subscriber: fail fence rejected, node already terminal",
			"dag_key", node.DagKey, "node_key", node.NodeKey)
	default:
		logger.Warn("dag subscriber: fail node failed",
			"dag_key", node.DagKey, "node_key", node.NodeKey, "error", err)
	}
}

// encodeTurnResultForNodeUpdate prepares ev.Result for storage in
// task_dag_nodes.result (jsonb). ADR-017 v1.2 §2.7 explicitly defers the
// jsonb merge / _handshake to ADR-018 — here we only normalize empty into
// `{}` so the store's NOT NULL constraint stays happy.
func encodeTurnResultForNodeUpdate(raw string) json.RawMessage {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return json.RawMessage(`{}`)
	}
	// If raw is already valid JSON, pass through. Otherwise wrap as a
	// {"text": "..."} envelope so the column remains valid jsonb without
	// committing to a richer shape (ADR-018 will redo).
	if json.Valid([]byte(trimmed)) {
		return json.RawMessage(trimmed)
	}
	wrapped, err := json.Marshal(struct {
		Text string `json:"text"`
	}{Text: raw})
	if err != nil {
		return json.RawMessage(`{}`)
	}
	return json.RawMessage(wrapped)
}

// stopSpawnedAgentForSubscriber is the §2.8 helper hook. ADR-016 v1.2 §3.2
// hard-constraint: the subscriber MUST call StopSpawnedAgent rather than
// inline the 5 semantic contracts. Failure is logged at Warn but does NOT
// propagate to the caller — the DAG advance is the source of truth, the
// child agent's resource release is a side-effect.
func stopSpawnedAgentForSubscriber(
	ctx context.Context,
	deps DAGSubscriberDeps,
	logger *slog.Logger,
	threadID string,
) {
	if deps.AgentThreads == nil || deps.SvcStopper == nil {
		logger.Debug("dag subscriber: stop helper deps not wired, skip",
			"thread_id", threadID)
		return
	}
	_, err := StopSpawnedAgent(ctx, deps.AgentThreads, deps.SvcStopper, threadID)
	if err != nil {
		logger.Warn("dag subscriber: stop spawned agent failed",
			"thread_id", threadID, "error", err)
	}
}

// Compile-time guard ensuring DAGSubscriberDeps remains an fx.In-tagged
// struct. Adding new fields without fx.In would silently drop them from
// the fx graph at run-time.
// 顶层编译期断言：保证 DAGSubscriberDeps 需含 fx.In 嵌入（Reviewer B 揭出
// 原写法 var _ = func() any { ... } 函数从不被调，编译器不检函数体内
// 类型断言是否仍成立，断言实际失效）。顶层 var 断言才能真正守住
// “人工删 fx.In 后编译即报错”该不变式。
var _ fx.In = DAGSubscriberDeps{}.In
