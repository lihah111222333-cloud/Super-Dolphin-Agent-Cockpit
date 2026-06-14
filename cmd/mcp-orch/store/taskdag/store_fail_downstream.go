package taskdag

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/store/sqlc"
	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/store/sqlctx"
)

// Phase 3.5 / 3B · 节点失败重试策略
//
// dispatcher 判定 wakeup 已 retry 上限后，调本方法把节点置 failed；如 caller
// 决定 fail_fast=true（来自 DAG retry metadata），同事务内对所有
// 直接或间接依赖该节点且仍处 pending 的下游节点级联标记 failed，避免它们
// 永远卡在 pending（依赖永远不会变 done）。
//
// Primary node failure uses a non-terminal SQL fence so a late failure path
// cannot rewrite a node another path already completed. The failure reason is
// still written into result for forensic visibility; status stays "failed" to
// avoid another enum.

const (
	failNodeKindExhaustedRetries = "exhausted_retries"
	failNodeKindCascade          = "cascade"
)

// FailNodeAndCancelDownstream 标记节点失败，并取消下游节点。
func (s *store) FailNodeAndCancelDownstream(ctx context.Context, input FailNodeInput) (*FailNodeResult, error) {
	if err := requireRuntimeRunID("fail_and_cancel_downstream", input.RunID); err != nil {
		return nil, err
	}
	var result FailNodeResult
	err := sqlctx.WithTxOrReuse(ctx, s.db, s.q, func(txq *sqlc.Queries, txdb sqlc.DBTX) error {
		txStore := &store{db: txdb, q: txq}
		res, failErr := failNodeAndCancelDownstreamTx(ctx, txStore, input)
		if failErr != nil {
			return failErr
		}
		result = *res
		return nil
	})
	if err != nil {
		return nil, wrapTaskDAGError(err, "fail_and_cancel_downstream", "task_dag_node")
	}
	return &result, nil
}

// failNodeAndCancelDownstreamTx 在事务内标记节点失败并取消下游节点。
func failNodeAndCancelDownstreamTx(ctx context.Context, txStore *store, input FailNodeInput) (*FailNodeResult, error) {
	oldStatus, oldErr := lockedNodeStatusBeforeFailTx(ctx, txStore, input.DagKey, input.NodeKey, input.RunID)
	if oldErr != nil {
		return nil, oldErr
	}
	node, failErr := failNodeTx(ctx, txStore, input.DagKey, input.NodeKey, input.RunID, failNodeReason{
		Kind:   failNodeKindExhaustedRetries,
		Reason: input.Reason,
	})
	if failErr != nil {
		return nil, failErr
	}
	result := &FailNodeResult{Node: node, OldStatus: oldStatus}
	if input.FailFast {
		canceled, cascadeErr := cancelDownstreamTx(ctx, txStore, input.DagKey, input.NodeKey, input.RunID, input.Reason)
		if cascadeErr != nil {
			return nil, cascadeErr
		}
		result.CanceledDownstream = canceled
	}
	finalized, finalizeErr := maybeFinalizeRunTx(ctx, txStore, input.DagKey, input.RunID)
	if finalizeErr != nil {
		return nil, finalizeErr
	}
	result.FinalizedRun = finalized
	return result, nil
}

func lockedNodeStatusBeforeFailTx(ctx context.Context, txStore *store, dagKey, nodeKey string, runID int64) (string, error) {
	row, err := txStore.q.GetTaskDagRunNodeForUpdate(ctx, sqlc.GetTaskDagRunNodeForUpdateParams{
		DagKey:  dagKey,
		NodeKey: nodeKey,
		RunID:   int64Ptr(runID),
	})
	if err != nil {
		return "", err
	}
	return row.Status, nil
}

// failNodeTx writes a failed-status update for a single node row inside the
// given transaction. The reason struct is JSON-encoded into the node's
// `result` column so operators can distinguish primary vs cascade failures
// without joining other tables.
func failNodeTx(ctx context.Context, txStore *store, dagKey, nodeKey string, runID int64, reason failNodeReason) (*Node, error) {
	encoded, err := json.Marshal(reason)
	if err != nil {
		return nil, fmt.Errorf("marshal fail reason for %s/%s: %w", dagKey, nodeKey, err)
	}
	return updateNodeStatus(func() (sqlc.TaskDagNode, error) {
		return txStore.q.FailTaskDagNodeIfNonTerminal(ctx, sqlc.FailTaskDagNodeIfNonTerminalParams{
			Status:  "failed",
			Result:  encoded,
			DagKey:  dagKey,
			NodeKey: nodeKey,
			RunID:   int64Ptr(runID),
		})
	}, "fail_non_terminal")
}

// cancelDownstreamTx walks the reverse-dependency graph from the failed node
// and marks every transitively-dependent pending node as failed (cascade).
// Nodes already in non-pending states are left alone — once a node has
// started running or reached terminal we don't rewrite history.
// cancelDownstreamTx 处理canceldownstreamtx。
func cancelDownstreamTx(ctx context.Context, txStore *store, dagKey, failedNodeKey string, runID int64, reason string) ([]CanceledDownstreamNode, error) {
	if err := requireRuntimeRunID("cancel_downstream", runID); err != nil {
		return nil, err
	}
	nodes, listErr := txStore.ListRunNodes(ctx, dagKey, runID)
	if listErr != nil {
		return nil, listErr
	}
	dependents, decodeErr := buildDependentIndex(nodes)
	if decodeErr != nil {
		return nil, decodeErr
	}
	nodeStatusByKey := make(map[string]string, len(nodes))
	for i := range nodes {
		nodeStatusByKey[nodes[i].NodeKey] = nodes[i].Status
	}
	visited := make(map[string]bool)
	queue := append([]string(nil), dependents[failedNodeKey]...)
	canceled := make([]CanceledDownstreamNode, 0)
	for len(queue) > 0 {
		key := queue[0]
		queue = queue[1:]
		if visited[key] {
			continue
		}
		visited[key] = true
		// Recurse before status check: even if the current node is not
		// pending, its descendants may still be reachable through other
		// failed paths — propagate to keep cascade closure correct.
		queue = append(queue, dependents[key]...)
		if nodeStatusByKey[key] != "pending" {
			continue
		}
		inserted, failErr := cascadeFailPendingNodeTx(ctx, txStore, dagKey, key, runID, failNodeReason{
			Kind:         failNodeKindCascade,
			Reason:       reason,
			CausedByNode: failedNodeKey,
		})
		if failErr != nil {
			return nil, failErr
		}
		if !inserted {
			continue
		}
		canceled = append(canceled, CanceledDownstreamNode{DagKey: dagKey, NodeKey: key, RunID: runID})
	}
	return canceled, nil
}

func cascadeFailPendingNodeTx(ctx context.Context, txStore *store, dagKey, nodeKey string, runID int64, reason failNodeReason) (bool, error) {
	encoded, err := json.Marshal(reason)
	if err != nil {
		return false, fmt.Errorf("marshal cascade fail reason for %s/%s: %w", dagKey, nodeKey, err)
	}
	rows, err := txStore.q.CascadeFailPendingTaskDagNode(ctx, sqlc.CascadeFailPendingTaskDagNodeParams{
		Result:  encoded,
		DagKey:  dagKey,
		NodeKey: nodeKey,
		RunID:   int64Ptr(runID),
	})
	if err != nil {
		return false, err
	}
	return rows > 0, nil
}

// buildDependentIndex inverts each node's depends_on list so callers can
// walk dependency arrows in the forward direction (\"who waits on me?\").
func buildDependentIndex(nodes []Node) (map[string][]string, error) {
	dependents := make(map[string][]string, len(nodes))
	for i := range nodes {
		deps, decodeErr := decodeDependsOn(nodes[i].DependsOn)
		if decodeErr != nil {
			return nil, fmt.Errorf("decode depends_on for %s/%s: %w", nodes[i].DagKey, nodes[i].NodeKey, decodeErr)
		}
		for _, dep := range deps {
			dependents[dep] = append(dependents[dep], nodes[i].NodeKey)
		}
	}
	return dependents, nil
}
