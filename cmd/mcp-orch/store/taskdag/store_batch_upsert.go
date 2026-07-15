package taskdag

import (
	"context"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/sqlc"
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/sqlctx"
)

// BatchUpsertingNodeStore 是 *store 的窄能力扩展接口，用于 service 层
// type-assert 走批量 UPSERT 路径，避免调用方循环发起 N 次 store 方法。**故意不嵌入**
// DAGMutationStore 接口、保持 InterfaceIsolation 预算（DAGMutationStore 当前
// 2 direct + 1 embedded，新增方法会突破）。生产 *store 实现该方法；测试 mock
// 不实现时 service 层 type-assert 失败、自动 fallback 到逐行 UpsertNode 路径。
type BatchUpsertingNodeStore interface {
	BatchUpsertNodes(ctx context.Context, nodes []Node) (int64, error)
}

// BatchUpsertNodes 在一个 IMMEDIATE 事务内逐行复用节点的 UPDATE/INSERT/Get 等价写路径。
// 任一节点失败时整批回滚并返回 0，避免调用方误把已回滚的中间计数当成已持久化行数。
func (s *store) BatchUpsertNodes(ctx context.Context, nodes []Node) (int64, error) {
	var rows int64
	err := sqlctx.WithImmediateTxOrReuse(ctx, s.db, s.q, func(txq *sqlc.Queries, _ sqlc.DBTX) error {
		for _, node := range nodes {
			if _, err := upsertNodeTx(ctx, txq, node); err != nil {
				return err
			}
			rows++
		}
		return nil
	})
	if err != nil {
		return 0, wrapTaskDAGError(err, "batch_upsert", "task_dag_node")
	}
	return rows, nil
}
