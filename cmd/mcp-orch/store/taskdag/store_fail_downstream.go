package taskdag

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/store/sqlc"
	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/store/sqlctx"
)

// 节点失败重试耗尽后，dispatcher 调本方法把节点置 failed；所有直接或间接依赖
// 该节点且仍处 pending 的下游节点都会在同事务内级联标记 failed，避免它们永远
// 卡在 pending（依赖永远不会变 done）。fail_fast 只影响其它仍可运行分支的策略，
// 不再决定这些已不可能完成的下游节点是否收敛。
// 主节点失败写入使用非终态 SQL fence，迟到失败路径不能改写已完成节点；
// result 里保留失败原因，方便排查一次失败是原发还是级联。

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
	err := sqlctx.WithImmediateTxOrReuse(ctx, s.db, s.q, func(txq *sqlc.Queries, txdb sqlc.DBTX) error {
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
	canceled, cascadeErr := cancelDownstreamTx(ctx, txStore, input.DagKey, input.NodeKey, input.RunID, input.Reason)
	if cascadeErr != nil {
		return nil, cascadeErr
	}
	result.CanceledDownstream = canceled
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

// failNodeTx 在当前事务内把单个非终态节点写成 failed。
// 失败原因编码进 result，调用方无需额外 join 就能区分原发失败和级联失败。
func failNodeTx(ctx context.Context, txStore *store, dagKey, nodeKey string, runID int64, reason failNodeReason) (*Node, error) {
	encoded, err := json.Marshal(reason)
	if err != nil {
		return nil, fmt.Errorf("marshal fail reason for %s/%s: %w", dagKey, nodeKey, err)
	}
	return updateNodeStatus(func() (sqlc.FailTaskDagNodeIfNonTerminalRow, error) {
		return txStore.q.FailTaskDagNodeIfNonTerminal(ctx, sqlc.FailTaskDagNodeIfNonTerminalParams{
			Status:  "failed",
			Result:  encoded,
			DagKey:  dagKey,
			NodeKey: nodeKey,
			RunID:   int64Ptr(runID),
		})
	}, "fail_non_terminal", fromNodeFailNonTerminalRow)
}

// cancelDownstreamTx 从失败节点出发遍历反向依赖图，级联失败所有仍为 pending 的下游节点。
// 已开始运行或已到终态的节点不回写，避免覆盖它们自己的执行历史。
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
		// 先扩展子节点再检查当前状态：当前节点即使不再 pending，
		// 它的后代仍可能通过其它失败路径被级联命中。
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

// buildDependentIndex 反转每个节点的 depends_on 列表，供失败级联沿“谁依赖我”方向遍历。
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
