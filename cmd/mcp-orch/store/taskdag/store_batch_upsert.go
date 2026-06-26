package taskdag

import (
	"context"

	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/store/sqlc"
)

// BatchUpsertingNodeStore 是 *store 的窄能力扩展接口，用于 service 层
// type-assert 走批量 UPSERT 路径，避免调用方循环发起 N 次 store 方法。**故意不嵌入**
// DAGMutationStore 接口、保持 InterfaceIsolation 预算（DAGMutationStore 当前
// 2 direct + 1 embedded，新增方法会突破）。生产 *store 实现该方法；测试 mock
// 不实现时 service 层 type-assert 失败、自动 fallback 到逐行 UpsertNode 路径。
type BatchUpsertingNodeStore interface {
	BatchUpsertNodes(ctx context.Context, nodes []Node) (int64, error)
}

// BatchUpsertNodes 在一个 store 调用内逐行复用 sqlc 生成的 UpsertTaskDagNode。
// 这样保持 SQLC 边界，不在手写 Go 中恢复批量 raw SQL；任一行失败立即返回已写入行数和错误。
func (s *store) BatchUpsertNodes(ctx context.Context, nodes []Node) (int64, error) {
	var rows int64
	for _, node := range nodes {
		if _, err := s.q.UpsertTaskDagNode(ctx, sqlc.UpsertTaskDagNodeParams{
			DagKey:     node.DagKey,
			NodeKey:    node.NodeKey,
			Title:      node.Title,
			NodeType:   node.NodeType,
			AssignedTo: node.AssignedTo,
			DependsOn:  node.DependsOn,
			CommandRef: node.CommandRef,
			Config:     node.Config,
		}); err != nil {
			return rows, wrapTaskDAGError(err, "batch_upsert", "task_dag_node")
		}
		rows++
	}
	return rows, nil
}
