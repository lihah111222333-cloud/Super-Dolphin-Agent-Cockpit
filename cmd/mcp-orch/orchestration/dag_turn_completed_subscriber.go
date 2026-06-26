package orchestration

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"

	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/orchestration/nodeevents"
	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/orchestration/nodeexec"
	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/orchestration/sharedfileowner"
	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/orchestration/turncompletionretry"
	sharedfilestore "github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/store/sharedfile"
	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/store/taskdag"
	turndto "github.com/anthropic-ai/super-agent-v3/internal/dto/turn"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/bus"
	platformdb "github.com/anthropic-ai/super-agent-v3/internal/platform/db"
	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
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

// DAGSubscriberDeps 收拢 TurnCompleted 订阅器需要的最小端口。
// 可选端口缺失时只跳过对应副作用，节点状态推进仍由 LookupStore/FlowStore 负责。
type DAGSubscriberDeps struct {
	fx.In

	LookupStore      taskdag.NodeSpawningThreadLookup
	FlowStore        taskdag.NodeFlowStore
	EventBus         *event.Dispatcher `optional:"true"`
	AgentThreads     AgentThreadLookup
	SvcStopper       StopAgentService
	SharedFileReader nodeexec.SharedFileReader `optional:"true"`
	SharedFileWriter nodeexec.SharedFileWriter `optional:"true"`
	ArtifactImporter sharedfilestore.Importer  `optional:"true"`
	NodeRouter       *NodeExecutorRouter       `optional:"true"`
}

// RegisterDAGTurnCompletedSubscriber 注册 TurnCompleted 到 DAG 节点状态的桥接订阅。
// lifecycle 停止时先取消上下文再取消订阅，避免 shutdown 期间继续推进节点。
func RegisterDAGTurnCompletedSubscriber(lc fx.Lifecycle, dispatcher *event.Dispatcher, deps DAGSubscriberDeps, logger *slog.Logger) {
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

// handleDAGTurnCompleted 根据 thread_id 找到由该 turn 驱动的 DAG 节点并推进终态。
// 同一 thread_id 命中多个节点会告警但逐个处理，避免脏数据导致其他节点卡住。
func handleDAGTurnCompleted(ctx context.Context, deps DAGSubscriberDeps, logger *slog.Logger, ev turndto.TurnCompleted) {
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

// advanceNodeForTurnCompleted 把一次 turn 完成事件映射成节点 done/failed。
// 已经终态的节点只计幂等跳过，防止重复 hook 或重放事件覆盖最终状态。
func advanceNodeForTurnCompleted(ctx context.Context, deps DAGSubscriberDeps, logger *slog.Logger, node *taskdag.Node, ev turndto.TurnCompleted) {
	if isTerminalNodeStatus(node.Status) {
		dagSubscriberMetrics.IncIdempotentSkipped()
		logger.Debug("dag subscriber: node already terminal, skip", "dag_key", node.DagKey, "node_key", node.NodeKey, "status", node.Status)
		return
	}
	result := ev.Result
	if ev.Success && strings.TrimSpace(node.NodeType) == "agent" {
		if agentNodeUsesArtifactResult(node.Config) {
			result = ev.Result
		} else {
			result = turnCompletedReportText(ev)
		}
	}
	if strings.TrimSpace(result) == "" {
		dagSubscriberMetrics.IncCompleteResultEmpty()
	}
	if ev.Success {
		advanceNodeDoneForSuccess(ctx, deps, logger, node, result, ev)
		return
	}
	advanceNodeFailed(ctx, deps.FlowStore, deps.EventBus, deps.NodeRouter, logger, node, ev)
}

func advanceNodeDoneForSuccess(ctx context.Context, deps DAGSubscriberDeps, logger *slog.Logger, node *taskdag.Node, rawResult string, ev turndto.TurnCompleted) {
	materialized, failure := prepareTurnCompletedResult(node, rawResult)
	if failure != nil {
		handleMaterializationFailure(ctx, deps, logger, node, failure)
		return
	}
	owner := sharedfileowner.Owner{
		DagKey:   strings.TrimSpace(node.DagKey),
		NodeKey:  strings.TrimSpace(node.NodeKey),
		RunID:    taskNodeRunID(node),
		ThreadID: strings.TrimSpace(ev.ThreadID),
		TurnID:   strings.TrimSpace(ev.TurnID),
	}
	result, ok := materializeTurnOutputAfterClaim(ctx, deps, logger, node, materialized, owner)
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

func materializeTurnOutputAfterClaim(ctx context.Context, deps DAGSubscriberDeps, logger *slog.Logger, node *taskdag.Node, materialized turnOutputMaterialization, owner sharedfileowner.Owner) (json.RawMessage, bool) {
	if materialized.Artifact != nil {
		return materializeArtifactAfterClaim(ctx, deps, logger, node, materialized)
	}
	return materializeSharedfileAfterClaim(ctx, deps, logger, node, materialized, owner)
}

func advanceNodeDone(ctx context.Context, flow taskdag.NodeFlowStore, eventBus *event.Dispatcher, logger *slog.Logger, node *taskdag.Node, result json.RawMessage) bool {
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
	case errors.Is(err, sql.ErrNoRows) || platformdb.IsNotFound(err):
		dagSubscriberMetrics.IncIdempotentSkipped()
		logger.Debug("dag subscriber: complete fence rejected, node already terminal", "dag_key", node.DagKey, "node_key", node.NodeKey)
	default:
		logger.Warn("dag subscriber: complete node failed", "dag_key", node.DagKey, "node_key", node.NodeKey, "error", err)
		if retryErr := turncompletionretry.Enqueue(ctx, flow, node, result); retryErr != nil {
			reason := truncateWakeupError("infrastructure: turn.completed completion retry enqueue failed: " + retryErr.Error() + "; original completion error: " + err.Error())
			advanceNodeFailedWithReason(ctx, flow, eventBus, logger, node, reason, true)
		}
	}
	return false
}

func advanceNodeFailed(ctx context.Context, flow taskdag.NodeFlowStore, eventBus *event.Dispatcher, router *NodeExecutorRouter, logger *slog.Logger, node *taskdag.Node, ev turndto.TurnCompleted) {
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

func advanceNodeFailedWithReason(ctx context.Context, flow taskdag.NodeFlowStore, eventBus *event.Dispatcher, logger *slog.Logger, node *taskdag.Node, reason string, failFast bool) bool {
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
	case errors.Is(err, sql.ErrNoRows) || platformdb.IsNotFound(err):
		dagSubscriberMetrics.IncIdempotentSkipped()
		logger.Debug("dag subscriber: fail fence rejected, node already terminal", "dag_key", node.DagKey, "node_key", node.NodeKey)
	}
	logger.Warn("dag subscriber: fail node failed", "dag_key", node.DagKey, "node_key", node.NodeKey, "error", err)
	turncompletionretry.EnqueueTerminalFailureCompensation(ctx, flow, logger, node, reason, err, failFast)
	return false
}

// materializeSharedfileAfterClaim 在领取输出后写入 shared file。
func materializeSharedfileAfterClaim(ctx context.Context, deps DAGSubscriberDeps, logger *slog.Logger, node *taskdag.Node, materialized turnOutputMaterialization, owner sharedfileowner.Owner) (json.RawMessage, bool) {
	result := materialized.Result
	if materialized.SharedfilePath == "" {
		return result, true
	}
	if strings.TrimSpace(materialized.RawResult) == "" {
		current, err := sharedfileowner.HasCurrent(ctx, deps.SharedFileReader, materialized.SharedfilePath, owner)
		if err != nil {
			handleMaterializationFailure(ctx, deps, logger, node, sharedfileOwnerFailure("outputs.to_sharedfile["+materialized.SharedfilePath+"]: "+err.Error(), err))
			return nil, false
		}
		if !current {
			handleMaterializationFailure(ctx, deps, logger, node, validationMaterializationFailure("empty agent output and configured sharedfile lacks current-run ownership marker"))
			return nil, false
		}
		if !claimNodeOutputMaterialization(ctx, deps.FlowStore, deps.EventBus, logger, node, result) {
			return nil, false
		}
		logger.Debug("dag subscriber: configured sharedfile has current-run marker, preserve existing content", "dag_key", node.DagKey, "node_key", node.NodeKey, "path", materialized.SharedfilePath)
		return result, true
	}
	if !claimNodeOutputMaterialization(ctx, deps.FlowStore, deps.EventBus, logger, node, result) {
		return nil, false
	}
	if failure := writeAgentTurnSharedfile(ctx, deps.SharedFileWriter, materialized.SharedfilePath, materialized.RawResult, owner); failure != nil {
		handleMaterializationFailure(ctx, deps, logger, node, failure)
		return nil, false
	}
	return result, true
}

type nodeOutputMaterializationClaimer interface {
	ClaimNodeOutputMaterialization(context.Context, taskdag.OutputMaterializationClaimInput) (*taskdag.Node, error)
}

func claimNodeOutputMaterialization(ctx context.Context, flow taskdag.NodeFlowStore, eventBus *event.Dispatcher, logger *slog.Logger, node *taskdag.Node, result json.RawMessage) bool {
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
	case errors.Is(err, sql.ErrNoRows) || platformdb.IsNotFound(err):
		dagSubscriberMetrics.IncIdempotentSkipped()
		logger.Debug("dag subscriber: output materialization claim rejected, node already claimed or terminal", "dag_key", node.DagKey, "node_key", node.NodeKey)
		return false
	}
	logger.Warn("dag subscriber: output materialization claim failed", "dag_key", node.DagKey, "node_key", node.NodeKey, "error", err)
	return false
}

func stopSpawnedAgentForSubscriber(ctx context.Context, deps DAGSubscriberDeps, logger *slog.Logger, threadID string) {
	if deps.AgentThreads == nil || deps.SvcStopper == nil {
		logger.Debug("dag subscriber: stop helper deps not wired, skip", "thread_id", threadID)
		return
	}
	if _, err := StopSpawnedAgent(ctx, deps.AgentThreads, deps.SvcStopper, threadID); err != nil {
		logger.Warn("dag subscriber: stop spawned agent failed", "thread_id", threadID, "error", err)
	}
}

var _ fx.In = DAGSubscriberDeps{}.In
