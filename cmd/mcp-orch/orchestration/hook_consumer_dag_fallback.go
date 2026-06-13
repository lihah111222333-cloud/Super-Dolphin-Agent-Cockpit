package orchestration

import (
	"context"
	"strings"

	orchmetrics "github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/orchestration/metrics"
	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/orchestration/nodeexec"
	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/store/taskdag"
)

// runThreadStoppedDAGFallback advances the DAG fallback outside the agent lock.
// runThreadStoppedDAGFallback 运行线程stoppedDAG兜底。
func (c *hookConsumer) runThreadStoppedDAGFallback(ctx context.Context, threadID string) {
	threadID = strings.TrimSpace(threadID)
	if threadID == "" {
		return
	}
	lookup := c.dagFallbackLookup
	flow := c.dagFallbackFlow
	if lookup == nil || flow == nil {
		return
	}
	nodes, err := lookup.LookupNodesBySpawningThread(ctx, threadID)
	if err != nil {
		orchmetrics.IncDAGFallbackLookupFailed()
		c.logger.Warn("thread stopped fallback: lookup nodes failed",
			"thread_id", threadID, "error", err)
		return
	}
	if len(nodes) == 0 {
		orchmetrics.IncDAGFallbackNoNode()
		return
	}
	for i := range nodes {
		if ctx.Err() != nil {
			return
		}
		c.failThreadStoppedFallbackNode(ctx, flow, nodes[i])
	}
}

func (c *hookConsumer) failThreadStoppedFallbackNode(ctx context.Context, flow taskdag.NodeFlowStore, n taskdag.Node) {
	if !isDAGFallbackFailEligibleStatus(n.Status) {
		orchmetrics.IncDAGFallbackIdempotentSkipped()
		return
	}
	res, failErr := flow.FailNodeAndCancelDownstream(ctx, taskdag.FailNodeInput{
		DagKey:   n.DagKey,
		NodeKey:  n.NodeKey,
		RunID:    taskNodeRunID(&n),
		Reason:   "thread_stopped_fallback",
		FailFast: false,
	})
	if failErr != nil {
		orchmetrics.IncDAGFallbackFailNodeErr()
		c.logger.Warn("thread stopped fallback: fail node failed",
			"dag_key", n.DagKey, "node_key", n.NodeKey, "error", failErr)
		return
	}
	orchmetrics.IncDAGFallbackFailed()
	c.invokeThreadStoppedFallbackLifecycleHook(ctx, n, res)
}

func (c *hookConsumer) invokeThreadStoppedFallbackLifecycleHook(ctx context.Context, n taskdag.Node, res *taskdag.FailNodeResult) {
	if router := c.dagTurnCompletedDeps.NodeRouter; router != nil {
		router.invokeTerminalFailureHooksForTaskNode(ctx, failedNodeForLifecycle(n, res), nodeexec.NodeOutcome{
			Status:       nodeexec.NodeStatusFailed,
			ErrorSummary: "thread_stopped_fallback",
		})
	}
}

// failedNodeForLifecycle 为生命周期处理failed节点。
func failedNodeForLifecycle(original taskdag.Node, result *taskdag.FailNodeResult) *taskdag.Node {
	node := original
	if result != nil && result.Node != nil {
		node = *result.Node
		if node.NodeType == "" {
			node.NodeType = original.NodeType
		}
		if node.Title == "" {
			node.Title = original.Title
		}
		if len(node.Config) == 0 {
			node.Config = append(node.Config[:0], original.Config...)
		}
	}
	if node.Status == "" {
		node.Status = string(nodeexec.NodeStatusFailed)
	}
	return &node
}

func isDAGFallbackFailEligibleStatus(status string) bool {
	switch strings.TrimSpace(status) {
	case "done", "failed", "cancelled", "skipped", "awaiting_verify":
		return false
	default:
		return true
	}
}
