package orchestration

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/orchestration/nodeevents"
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

const completeNodeResultCap = 4 * 1024

func isTerminalNodeStatus(status string) bool {
	switch strings.TrimSpace(strings.ToLower(status)) {
	case "done", "failed", "cancelled", "skipped":
		return true
	}
	return false
}

// DAGSubscriberDeps keeps the TurnCompleted subscriber on narrow ports.
type DAGSubscriberDeps struct {
	fx.In

	LookupStore      taskdag.NodeSpawningThreadLookup
	FlowStore        taskdag.NodeFlowStore
	EventBus         *event.Dispatcher `optional:"true"`
	AgentThreads     AgentThreadLookup
	SvcStopper       StopAgentService
	SharedFileReader nodeexec.SharedFileReader `optional:"true"`
	SharedFileWriter nodeexec.SharedFileWriter `optional:"true"`
	NodeRouter       *NodeExecutorRouter       `optional:"true"`
}

// RegisterDAGTurnCompletedSubscriber advances DAG node state independently
// from the agent-runtime TurnCompleted subscribers.
func RegisterDAGTurnCompletedSubscriber(
	lc fx.Lifecycle,
	dispatcher *event.Dispatcher,
	deps DAGSubscriberDeps,
	logger *slog.Logger,
) {
	if logger == nil {
		logger = pkglogger.Get()
	}
	if deps.EventBus == nil {
		deps.EventBus = dispatcher
	}
	var cancelSub = func() {}
	var lifecycleCtx context.Context
	var lifecycleCancel context.CancelFunc
	lc.Append(fx.Hook{
		OnStart: func(context.Context) error {
			lifecycleCtx, lifecycleCancel = context.WithCancel(context.Background())
			cancelSub = bus.ResilientSubscribe(dispatcher, func(ev turndto.TurnCompleted) {
				if lifecycleCtx.Err() == nil {
					handleDAGTurnCompleted(lifecycleCtx, deps, logger, ev)
				}
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

// handleDAGTurnCompleted is the per-event subscriber entry point. It reverse
// looks up nodes for the completed thread, advances every non-terminal match,
// then stops the spawned agent after the DB state is authoritative.
// It never invokes dispatcher; A2 only reuses nodeexec config parsing and
// sharedfile ports for output materialization.
func handleDAGTurnCompleted(
	ctx context.Context,
	deps DAGSubscriberDeps,
	logger *slog.Logger,
	ev turndto.TurnCompleted,
) {
	threadID := strings.TrimSpace(ev.ThreadID)
	if threadID == "" {
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
		logger.Warn("dag subscriber: lookup nodes by spawning thread failed", "thread_id", threadID, "error", err)
		return
	}
	if len(nodes) == 0 {
		dagSubscriberMetrics.IncLookupNoNode()
		logger.Debug("dag subscriber: no node carries this thread id", "thread_id", threadID)
		stopSpawnedAgentForSubscriber(ctx, deps, logger, threadID)
		return
	}
	if len(nodes) > 1 {
		dagSubscriberMetrics.IncLookupDirtyData()
		logger.Warn("dag subscriber: N>1 nodes carry the same spawning thread id", "thread_id", threadID, "node_count", len(nodes))
	}
	for i := range nodes {
		if ctx.Err() != nil {
			return
		}
		advanceNodeForTurnCompleted(ctx, deps, logger, &nodes[i], ev)
	}
	stopSpawnedAgentForSubscriber(ctx, deps, logger, threadID)
}

func advanceNodeForTurnCompleted(
	ctx context.Context,
	deps DAGSubscriberDeps,
	logger *slog.Logger,
	node *taskdag.Node,
	ev turndto.TurnCompleted,
) {
	if isTerminalNodeStatus(node.Status) {
		dagSubscriberMetrics.IncIdempotentSkipped()
		logger.Debug("dag subscriber: node already terminal, skip", "dag_key", node.DagKey, "node_key", node.NodeKey, "status", node.Status)
		return
	}
	result := ev.Result
	if ev.Success && strings.TrimSpace(node.NodeType) == "agent" {
		result = turnCompletedReportText(ev)
	}
	if strings.TrimSpace(result) == "" {
		dagSubscriberMetrics.IncCompleteResultEmpty()
	}
	if ev.Success {
		advanceNodeDoneForSuccess(ctx, deps, logger, node, result)
		return
	}
	advanceNodeFailed(ctx, deps.FlowStore, deps.EventBus, deps.NodeRouter, logger, node, ev)
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
	if advanceNodeDone(ctx, deps.FlowStore, deps.EventBus, logger, node, result) && deps.NodeRouter != nil {
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
	if advanceNodeFailedWithReason(ctx, deps.FlowStore, deps.EventBus, logger, node, failure.Reason, true) && deps.NodeRouter != nil {
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
}

func advanceNodeDone(
	ctx context.Context,
	flow taskdag.NodeFlowStore,
	eventBus *event.Dispatcher,
	logger *slog.Logger,
	node *taskdag.Node,
	result json.RawMessage,
) bool {
	res, err := flow.CompleteNodeAndScheduleDownstream(ctx, taskdag.CompleteNodeInput{
		Status:  "done",
		Result:  result,
		DagKey:  node.DagKey,
		NodeKey: node.NodeKey,
		RunID:   taskNodeRunID(node),
	})
	switch {
	case err == nil:
		nodeevents.PublishComplete(eventBus, node.Status, res)
		dagSubscriberMetrics.IncCompleteDone()
		return true
	case errors.Is(err, pgx.ErrNoRows) || platformdb.IsNotFound(err):
		dagSubscriberMetrics.IncIdempotentSkipped()
		logger.Debug("dag subscriber: complete fence rejected, node already terminal", "dag_key", node.DagKey, "node_key", node.NodeKey)
	default:
		logger.Warn("dag subscriber: complete node failed", "dag_key", node.DagKey, "node_key", node.NodeKey, "error", err)
	}
	return false
}

func advanceNodeFailed(
	ctx context.Context,
	flow taskdag.NodeFlowStore,
	eventBus *event.Dispatcher,
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
	if advanceNodeFailedWithReason(ctx, flow, eventBus, logger, node, reason, false) && router != nil {
		router.invokeTerminalFailureHooksForTaskNode(ctx, node, nodeexec.NodeOutcome{
			Status:       nodeexec.NodeStatusFailed,
			ErrorSummary: reason,
		})
	}
}

func advanceNodeFailedWithReason(
	ctx context.Context,
	flow taskdag.NodeFlowStore,
	eventBus *event.Dispatcher,
	logger *slog.Logger,
	node *taskdag.Node,
	reason string,
	failFast bool,
) bool {
	res, err := flow.FailNodeAndCancelDownstream(ctx, taskdag.FailNodeInput{
		DagKey:   node.DagKey,
		NodeKey:  node.NodeKey,
		RunID:    taskNodeRunID(node),
		Reason:   reason,
		FailFast: failFast,
	})
	switch {
	case err == nil:
		nodeevents.PublishFail(eventBus, node.Status, res)
		dagSubscriberMetrics.IncCompleteFailed()
		return true
	case errors.Is(err, pgx.ErrNoRows) || platformdb.IsNotFound(err):
		dagSubscriberMetrics.IncIdempotentSkipped()
		logger.Debug("dag subscriber: fail fence rejected, node already terminal", "dag_key", node.DagKey, "node_key", node.NodeKey)
	}
	logger.Warn("dag subscriber: fail node failed", "dag_key", node.DagKey, "node_key", node.NodeKey, "error", err)
	return false
}

type nodeOutputMaterializationClaimer interface {
	ClaimNodeOutputMaterialization(context.Context, taskdag.OutputMaterializationClaimInput) (*taskdag.Node, error)
}

func claimNodeOutputMaterialization(
	ctx context.Context,
	flow taskdag.NodeFlowStore,
	eventBus *event.Dispatcher,
	logger *slog.Logger,
	node *taskdag.Node,
	result json.RawMessage,
) bool {
	claimer, ok := flow.(nodeOutputMaterializationClaimer)
	if !ok {
		logger.Warn("dag subscriber: output materialization claim not wired", "dag_key", node.DagKey, "node_key", node.NodeKey)
		advanceNodeFailedWithReason(ctx, flow, eventBus, logger, node, "infrastructure: output materialization claim not wired", true)
		return false
	}
	updated, err := claimer.ClaimNodeOutputMaterialization(ctx, taskdag.OutputMaterializationClaimInput{
		DagKey:  node.DagKey,
		NodeKey: node.NodeKey,
		RunID:   taskNodeRunID(node),
		Result:  result,
	})
	switch {
	case err == nil:
		nodeevents.Publish(eventBus, node.Status, updated)
		return true
	case errors.Is(err, pgx.ErrNoRows) || platformdb.IsNotFound(err):
		dagSubscriberMetrics.IncIdempotentSkipped()
		logger.Debug("dag subscriber: output materialization claim rejected, node already claimed or terminal", "dag_key", node.DagKey, "node_key", node.NodeKey)
		return false
	}
	logger.Warn("dag subscriber: output materialization claim failed", "dag_key", node.DagKey, "node_key", node.NodeKey, "error", err)
	return false
}

// encodeTurnResultForNodeUpdate prepares ev.Result for storage in task_dag_nodes.result.
func encodeTurnResultForNodeUpdate(raw string) json.RawMessage {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return json.RawMessage(`{}`)
	}
	if json.Valid([]byte(trimmed)) {
		return json.RawMessage(trimmed)
	}
	wrapped, err := json.Marshal(map[string]string{"text": raw})
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

func prepareTurnCompletedResult(node *taskdag.Node, rawResult string) (turnOutputMaterialization, *turnOutputMaterializationFailure) {
	if node == nil || strings.TrimSpace(node.NodeType) != "agent" {
		return turnOutputMaterialization{Result: encodeTurnResultForNodeUpdate(rawResult)}, nil
	}
	cfg, failure := parseAgentOutputConfig(node.Config)
	if failure != nil {
		return turnOutputMaterialization{}, failure
	}
	path := configuredSharedfilePath(cfg.Outputs)
	emitNodeResult := shouldMaterializeAgentNodeResult(cfg.Outputs)
	if strings.TrimSpace(rawResult) == "" {
		if !emitNodeResult && path != "" {
			return turnOutputMaterialization{Result: encodeSharedfileResultRef(path), SharedfilePath: path}, nil
		}
		return turnOutputMaterialization{}, validationMaterializationFailure("empty agent output")
	}
	nodeResult, failure := buildAgentNodeResult(rawResult, emitNodeResult)
	if failure != nil {
		return turnOutputMaterialization{}, failure
	}
	return turnOutputMaterialization{Result: finalAgentMaterializedResult(rawResult, nodeResult, path, emitNodeResult), SharedfilePath: path, RawResult: rawResult}, nil
}

func parseAgentOutputConfig(raw json.RawMessage) (*nodeexec.AgentNodeConfig, *turnOutputMaterializationFailure) {
	cfg, err := nodeexec.ParseAgentConfig(raw)
	if err != nil {
		return nil, validationMaterializationFailure("decode agent config: "+err.Error())
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
	return nil, &turnOutputMaterializationFailure{Reason: fmt.Sprintf("result exceeds 4KB size cap (%d > %d bytes), configure outputs.to_sharedfile (ADR-006)", len(nodeResult), completeNodeResultCap), SizeCapExceeded: true}
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
		if !claimNodeOutputMaterialization(ctx, deps.FlowStore, deps.EventBus, logger, node, result) {
			return nil, false
		}
		logger.Debug("dag subscriber: configured sharedfile already exists, preserve existing content", "dag_key", node.DagKey, "node_key", node.NodeKey, "path", materialized.SharedfilePath)
		return result, true
	}
	if strings.TrimSpace(materialized.RawResult) == "" {
		failNodeForMaterializationFailure(ctx, deps, logger, node, validationMaterializationFailure("empty agent output and configured sharedfile is missing"))
		return nil, false
	}
	if failure := validateAgentSharedfileWriter(deps.SharedFileWriter); failure != nil {
		failNodeForMaterializationFailure(ctx, deps, logger, node, failure)
		return nil, false
	}
	if !claimNodeOutputMaterialization(ctx, deps.FlowStore, deps.EventBus, logger, node, result) {
		return nil, false
	}
	if failure := writeAgentTurnSharedfile(ctx, deps.SharedFileWriter, materialized.SharedfilePath, materialized.RawResult); failure != nil {
		failNodeForMaterializationFailure(ctx, deps, logger, node, failure)
		return nil, false
	}
	return result, true
}

func writeAgentTurnSharedfile(ctx context.Context, writer nodeexec.SharedFileWriter, path, rawResult string) *turnOutputMaterializationFailure {
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

func configuredSharedfileAlreadyExists(ctx context.Context, reader nodeexec.SharedFileReader, path string) (bool, *turnOutputMaterializationFailure) {
	if path == "" {
		return false, nil
	}
	if failure := validateAgentSharedfileReader(reader); failure != nil {
		return false, failure
	}
	_, exists, err := reader.ReadSharedFile(ctx, path)
	if err != nil {
		return false, infrastructureMaterializationFailure(fmt.Sprintf("outputs.to_sharedfile[%q] preflight read: %v", path, err))
	}
	return exists, nil
}

func validateAgentSharedfileReader(reader nodeexec.SharedFileReader) *turnOutputMaterializationFailure {
	if reader != nil {
		return nil
	}
	return infrastructureMaterializationFailure("outputs.to_sharedfile configured but SharedFileReader not wired in DAG subscriber")
}

func validateAgentSharedfileWriter(writer nodeexec.SharedFileWriter) *turnOutputMaterializationFailure {
	if writer != nil {
		return nil
	}
	return infrastructureMaterializationFailure("outputs.to_sharedfile configured but SharedFileWriter not wired in DAG subscriber")
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
	return out.ToNodeResult || configuredSharedfilePath(out) == ""
}

func configuredSharedfilePath(out nodeexec.OutputsConfig) string {
	if out.ToSharedfile == nil {
		return ""
	}
	return strings.TrimSpace(out.ToSharedfile.Path)
}

func encodeSharedfileResultRef(path string) json.RawMessage {
	payload, err := json.Marshal(map[string]map[string]string{"sharedfile": {"path": path}})
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

// stopSpawnedAgentForSubscriber stops the runtime after DAG state is authoritative.
func stopSpawnedAgentForSubscriber(
	ctx context.Context,
	deps DAGSubscriberDeps,
	logger *slog.Logger,
	threadID string,
) {
	if deps.AgentThreads == nil || deps.SvcStopper == nil {
		logger.Debug("dag subscriber: stop helper deps not wired, skip", "thread_id", threadID)
		return
	}
	if _, err := StopSpawnedAgent(ctx, deps.AgentThreads, deps.SvcStopper, threadID); err != nil {
		logger.Warn("dag subscriber: stop spawned agent failed", "thread_id", threadID, "error", err)
	}
}

var _ fx.In = DAGSubscriberDeps{}.In
