package orchestration

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/orchestration/nodeexec"
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
// task_dag_nodes.result. A2 enforces it for agent node.result materialization;
// legacy non-agent rows still only emit the ADR-017 soft metric before store
// handling.
const completeNodeResultCap = 4 * 1024

// isTerminalNodeStatus is the application-side idempotency short-circuit
// for ADR-017 v1.2 §2.6 race C. The subscriber checks this BEFORE invoking
// CompleteNode/FailNode so it can skip SQL when another path (fallback /
// duplicate TurnCompleted / retry) already landed a terminal state.
//
// SQL fences (CompleteTaskDagNode / FailTaskDagNodeIfNonTerminal) still guard
// from below — this is a defense-in-depth check, not the only barrier.
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
	// SharedFileReader/Writer are only used by A2 agent output materialization
	// after TurnCompleted carries the real child response.
	SharedFileReader nodeexec.SharedFileReader `optional:"true"`
	SharedFileWriter nodeexec.SharedFileWriter `optional:"true"`
	NodeRouter       *NodeExecutorRouter       `optional:"true"`
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
//     TurnCompleted) — metric IdempotentSkipped.
//     b. Materialize CompleteNodeInput.Result from ev.Result and the agent
//     node's config.outputs. Empty ev.Result fires metric
//     CompleteResultEmpty as a soft alarm.
//     c. ev.Success=true → FlowStore.CompleteNodeAndScheduleDownstream
//     (metric CompleteDone / size-cap / DB err).
//     ev.Success=false → FlowStore.FailNodeAndCancelDownstream
//     (metric CompleteFailed).
//  4. After the DB advance, call stop_helper.StopSpawnedAgent (ADR-016
//     §3.2 hard constraint — never inline the 5 contracts). Failure is a
//     Warn log; the subscriber does NOT propagate the error and continues
//     to the next node.
//
// Important guarantees:
//   - ctx.Err() short-circuit on every iteration (lifecycle cancel propagation).
//   - The subscriber never invokes dispatcher. A2 uses nodeexec's config parser
//     and sharedfile writer port, but still avoids aggregate store / service access.
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
		advanceNodeForTurnCompleted(ctx, deps, logger, &nodes[i], ev)
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
	deps DAGSubscriberDeps,
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
	if strings.TrimSpace(ev.Result) == "" {
		dagSubscriberMetrics.IncCompleteResultEmpty()
	}

	if ev.Success {
		advanceNodeDoneForSuccess(ctx, deps, logger, node, ev.Result)
		return
	}
	advanceNodeFailed(ctx, deps.FlowStore, deps.NodeRouter, logger, node, ev)
}

func advanceNodeDoneForSuccess(
	ctx context.Context,
	deps DAGSubscriberDeps,
	logger *slog.Logger,
	node *taskdag.Node,
	rawResult string,
) {
	materialized, failure := prepareTurnCompletedResult(node, rawResult)
	if failure != nil {
		failNodeForMaterializationFailure(ctx, deps, logger, node, failure)
		return
	}
	result, ok := materializeSharedfileAfterClaim(ctx, deps, logger, node, materialized)
	if !ok {
		return
	}
	recordLegacyResultCapMetric(logger, node, result)
	if advanceNodeDone(ctx, deps.FlowStore, logger, node, result) && deps.NodeRouter != nil {
		deps.NodeRouter.invokeStateChangeHooksForTaskNode(ctx, node, nodeexec.NodeOutcome{
			Status: nodeexec.NodeStatusDone,
			Result: result,
		})
	}
}

func failNodeForMaterializationFailure(
	ctx context.Context,
	deps DAGSubscriberDeps,
	logger *slog.Logger,
	node *taskdag.Node,
	failure *turnOutputMaterializationFailure,
) {
	if failure.SizeCapExceeded {
		dagSubscriberMetrics.IncCompleteSizeCapExceeded()
	}
	logger.Warn("dag subscriber: materialize agent output failed",
		"dag_key", node.DagKey, "node_key", node.NodeKey, "reason", failure.Reason)
	if advanceNodeFailedWithReason(ctx, deps.FlowStore, logger, node, failure.Reason) && deps.NodeRouter != nil {
		deps.NodeRouter.invokeTerminalFailureHooksForTaskNode(ctx, node, nodeexec.NodeOutcome{
			Status:       nodeexec.NodeStatusFailed,
			FailureClass: classifyMaterializationFailure(failure),
			ErrorSummary: failure.Reason,
		})
	}
}

func classifyMaterializationFailure(failure *turnOutputMaterializationFailure) nodeexec.FailureClass {
	if failure == nil {
		return nodeexec.FailureClassValidation
	}
	if strings.HasPrefix(failure.Reason, "infrastructure:") {
		return nodeexec.FailureClassInfrastructure
	}
	return nodeexec.FailureClassValidation
}

func recordLegacyResultCapMetric(logger *slog.Logger, node *taskdag.Node, result json.RawMessage) {
	if len(result) <= completeNodeResultCap {
		return
	}
	dagSubscriberMetrics.IncCompleteSizeCapExceeded()
	logger.Warn("dag subscriber: complete result exceeds ADR-006 4KB cap",
		"dag_key", node.DagKey, "node_key", node.NodeKey, "size", len(result))
	// Legacy non-agent path keeps the A1 behavior: surface the metric and let
	// the store layer reject if needed. Agent outputs are enforced by
	// prepareTurnCompletedResult before any CompleteNode call.
}

func materializeSharedfileAfterClaim(
	ctx context.Context,
	deps DAGSubscriberDeps,
	logger *slog.Logger,
	node *taskdag.Node,
	materialized turnOutputMaterialization,
) (json.RawMessage, bool) {
	result := materialized.Result
	if materialized.SharedfilePath == "" {
		return result, true
	}
	exists, failure := configuredSharedfileAlreadyExists(ctx, deps.SharedFileReader, materialized.SharedfilePath)
	if failure != nil {
		failNodeForMaterializationFailure(ctx, deps, logger, node, failure)
		return nil, false
	}
	if exists {
		if !claimNodeOutputMaterialization(ctx, deps.FlowStore, logger, node, result) {
			return nil, false
		}
		logger.Debug("dag subscriber: configured sharedfile already exists, preserve existing content",
			"dag_key", node.DagKey, "node_key", node.NodeKey, "path", materialized.SharedfilePath)
		return result, true
	}
	if failure := validateAgentSharedfileWriter(deps.SharedFileWriter); failure != nil {
		failNodeForMaterializationFailure(ctx, deps, logger, node, failure)
		return nil, false
	}
	if !claimNodeOutputMaterialization(ctx, deps.FlowStore, logger, node, result) {
		return nil, false
	}
	if failure := writeAgentTurnSharedfile(ctx, deps.SharedFileWriter, materialized.SharedfilePath, materialized.RawResult); failure != nil {
		failNodeForMaterializationFailure(ctx, deps, logger, node, failure)
		return nil, false
	}
	return result, true
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
) bool {
	_, err := flow.CompleteNodeAndScheduleDownstream(ctx, taskdag.CompleteNodeInput{
		Status:  "done",
		Result:  result,
		DagKey:  node.DagKey,
		NodeKey: node.NodeKey,
		RunID:   taskNodeRunID(node),
	})
	switch {
	case err == nil:
		dagSubscriberMetrics.IncCompleteDone()
		return true
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
	return false
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
	router *NodeExecutorRouter,
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
	if advanceNodeFailedWithReason(ctx, flow, logger, node, reason) && router != nil {
		router.invokeTerminalFailureHooksForTaskNode(ctx, node, nodeexec.NodeOutcome{
			Status:       nodeexec.NodeStatusFailed,
			ErrorSummary: reason,
		})
	}
}

func advanceNodeFailedWithReason(
	ctx context.Context,
	flow taskdag.NodeFlowStore,
	logger *slog.Logger,
	node *taskdag.Node,
	reason string,
) bool {
	_, err := flow.FailNodeAndCancelDownstream(ctx, taskdag.FailNodeInput{
		DagKey:   node.DagKey,
		NodeKey:  node.NodeKey,
		RunID:    taskNodeRunID(node),
		Reason:   reason,
		FailFast: false,
	})
	switch {
	case err == nil:
		dagSubscriberMetrics.IncCompleteFailed()
		return true
	case errors.Is(err, pgx.ErrNoRows) || platformdb.IsNotFound(err):
		dagSubscriberMetrics.IncIdempotentSkipped()
		logger.Debug("dag subscriber: fail fence rejected, node already terminal",
			"dag_key", node.DagKey, "node_key", node.NodeKey)
	default:
		logger.Warn("dag subscriber: fail node failed",
			"dag_key", node.DagKey, "node_key", node.NodeKey, "error", err)
	}
	return false
}

type nodeOutputMaterializationClaimer interface {
	ClaimNodeOutputMaterialization(context.Context, taskdag.OutputMaterializationClaimInput) (*taskdag.Node, error)
}

func claimNodeOutputMaterialization(
	ctx context.Context,
	flow taskdag.NodeFlowStore,
	logger *slog.Logger,
	node *taskdag.Node,
	result json.RawMessage,
) bool {
	claimer, ok := flow.(nodeOutputMaterializationClaimer)
	if !ok {
		logger.Warn("dag subscriber: output materialization claim not wired",
			"dag_key", node.DagKey, "node_key", node.NodeKey)
		advanceNodeFailedWithReason(ctx, flow, logger, node, "infrastructure: output materialization claim not wired")
		return false
	}
	_, err := claimer.ClaimNodeOutputMaterialization(ctx, taskdag.OutputMaterializationClaimInput{
		DagKey:  node.DagKey,
		NodeKey: node.NodeKey,
		RunID:   taskNodeRunID(node),
		Result:  result,
	})
	switch {
	case err == nil:
		return true
	case errors.Is(err, pgx.ErrNoRows) || platformdb.IsNotFound(err):
		dagSubscriberMetrics.IncIdempotentSkipped()
		logger.Debug("dag subscriber: output materialization claim rejected, node already claimed or terminal",
			"dag_key", node.DagKey, "node_key", node.NodeKey)
		return false
	default:
		logger.Warn("dag subscriber: output materialization claim failed",
			"dag_key", node.DagKey, "node_key", node.NodeKey, "error", err)
		return false
	}
}

// encodeTurnResultForNodeUpdate prepares ev.Result for storage in
// task_dag_nodes.result (jsonb). It normalizes empty into `{}` so the store's
// NOT NULL constraint stays happy and wraps non-JSON text in a compact envelope.
func encodeTurnResultForNodeUpdate(raw string) json.RawMessage {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return json.RawMessage(`{}`)
	}
	// If raw is already valid JSON, pass through. Otherwise wrap as a
	// {"text": "..."} envelope so the column remains valid jsonb while
	// preserving the raw model text.
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

type turnOutputMaterializationFailure struct {
	Reason          string
	SizeCapExceeded bool
}

type turnOutputMaterialization struct {
	Result         json.RawMessage
	SharedfilePath string
	RawResult      string
}

// prepareTurnCompletedResult is the ADR-018/A2 boundary: agent nodes use
// the real TurnCompleted.Result, not launch metadata, as their persisted
// output. Non-agent rows keep the ADR-017 normalization path for compatibility.
func prepareTurnCompletedResult(
	node *taskdag.Node,
	rawResult string,
) (turnOutputMaterialization, *turnOutputMaterializationFailure) {
	if node == nil || strings.TrimSpace(node.NodeType) != "agent" {
		return turnOutputMaterialization{Result: encodeTurnResultForNodeUpdate(rawResult)}, nil
	}
	cfg, failure := parseAgentOutputConfig(node.Config)
	if failure != nil {
		return turnOutputMaterialization{}, failure
	}
	path := configuredSharedfilePath(cfg.Outputs)
	emitNodeResult := shouldMaterializeAgentNodeResult(cfg.Outputs)
	nodeResult, failure := buildAgentNodeResult(rawResult, emitNodeResult)
	if failure != nil {
		return turnOutputMaterialization{}, failure
	}
	return turnOutputMaterialization{
		Result:         finalAgentMaterializedResult(rawResult, nodeResult, path, emitNodeResult),
		SharedfilePath: path,
		RawResult:      rawResult,
	}, nil
}

func parseAgentOutputConfig(raw json.RawMessage) (*nodeexec.AgentNodeConfig, *turnOutputMaterializationFailure) {
	cfg, err := nodeexec.ParseAgentConfig(raw)
	if err != nil {
		return nil, validationMaterializationFailure("decode agent config: " + err.Error())
	}
	if cfg == nil {
		return nil, validationMaterializationFailure("decode agent config: nil parsed config")
	}
	return cfg, nil
}

func buildAgentNodeResult(rawResult string, emit bool) (json.RawMessage, *turnOutputMaterializationFailure) {
	if !emit {
		return nil, nil
	}
	nodeResult := encodeTurnResultForNodeUpdate(rawResult)
	if len(nodeResult) <= completeNodeResultCap {
		return nodeResult, nil
	}
	return nil, &turnOutputMaterializationFailure{
		Reason: fmt.Sprintf(
			"result exceeds 4KB size cap (%d > %d bytes), configure outputs.to_sharedfile (ADR-006)",
			len(nodeResult), completeNodeResultCap,
		),
		SizeCapExceeded: true,
	}
}

func writeAgentTurnSharedfile(
	ctx context.Context,
	writer nodeexec.SharedFileWriter,
	path string,
	rawResult string,
) *turnOutputMaterializationFailure {
	if path == "" {
		return nil
	}
	if failure := validateAgentSharedfileWriter(writer); failure != nil {
		return failure
	}
	if err := writer.WriteSharedFile(ctx, path, rawResult); err != nil {
		return infrastructureMaterializationFailure(fmt.Sprintf("outputs.to_sharedfile[%q]: %v", path, err))
	}
	return nil
}

func configuredSharedfileAlreadyExists(
	ctx context.Context,
	reader nodeexec.SharedFileReader,
	path string,
) (bool, *turnOutputMaterializationFailure) {
	if path == "" {
		return false, nil
	}
	if failure := validateAgentSharedfileReader(reader); failure != nil {
		return false, failure
	}
	_, exists, err := reader.ReadSharedFile(ctx, path)
	if err != nil {
		return false, infrastructureMaterializationFailure(
			fmt.Sprintf("outputs.to_sharedfile[%q] preflight read: %v", path, err))
	}
	return exists, nil
}

func validateAgentSharedfileReader(reader nodeexec.SharedFileReader) *turnOutputMaterializationFailure {
	if reader != nil {
		return nil
	}
	return infrastructureMaterializationFailure(
		"outputs.to_sharedfile configured but SharedFileReader not wired in DAG subscriber")
}

func validateAgentSharedfileWriter(writer nodeexec.SharedFileWriter) *turnOutputMaterializationFailure {
	if writer != nil {
		return nil
	}
	return infrastructureMaterializationFailure(
		"outputs.to_sharedfile configured but SharedFileWriter not wired in DAG subscriber")
}

func finalAgentMaterializedResult(rawResult string, nodeResult json.RawMessage, path string, emit bool) json.RawMessage {
	switch {
	case emit:
		return nodeResult
	case path != "":
		return encodeSharedfileResultRef(path)
	default:
		return encodeTurnResultForNodeUpdate(rawResult)
	}
}

func shouldMaterializeAgentNodeResult(out nodeexec.OutputsConfig) bool {
	if out.ToNodeResult {
		return true
	}
	return configuredSharedfilePath(out) == ""
}

func configuredSharedfilePath(out nodeexec.OutputsConfig) string {
	if out.ToSharedfile == nil {
		return ""
	}
	return strings.TrimSpace(out.ToSharedfile.Path)
}

func encodeSharedfileResultRef(path string) json.RawMessage {
	payload, err := json.Marshal(struct {
		Sharedfile struct {
			Path string `json:"path"`
		} `json:"sharedfile"`
	}{Sharedfile: struct {
		Path string `json:"path"`
	}{Path: path}})
	if err != nil {
		return json.RawMessage(`{}`)
	}
	return payload
}

func validationMaterializationFailure(reason string) *turnOutputMaterializationFailure {
	return &turnOutputMaterializationFailure{Reason: "validation: " + reason}
}

func infrastructureMaterializationFailure(reason string) *turnOutputMaterializationFailure {
	return &turnOutputMaterializationFailure{Reason: "infrastructure: " + reason}
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
