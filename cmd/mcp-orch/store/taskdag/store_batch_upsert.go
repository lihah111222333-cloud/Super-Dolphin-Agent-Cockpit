package taskdag

import (
	"context"
	"encoding/json"

	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/store/sqlc"
)

// BatchUpsertingNodeStore 是 *store 的窄能力扩展接口，用于 service 层 service
// type-assert 走批量 UPSERT 路径（C1：避免循环 N 次单行 INSERT）。**故意不嵌入**
// DAGMutationStore 接口、保持 InterfaceIsolation 预算（DAGMutationStore 当前
// 2 direct + 1 embedded，新增方法会突破）。生产 *store 实现该方法；测试 mock
// 不实现时 service 层 type-assert 失败、自动 fallback 到逐行 UpsertNode 路径。
type BatchUpsertingNodeStore interface {
	BatchUpsertNodes(ctx context.Context, nodes []Node) (int64, error)
}

// BatchUpsertNodes 把 N 个 Node 单条 multi-row INSERT … ON CONFLICT … DO UPDATE
// 写入 task_dag_nodes，1 次 PG round-trip 完成。语义与 N 次 UpsertNode 等价
// （on conflict 走 UPDATE，updated_at 刷新；status/result/run_id/reads/writes 不
// 在 INSERT 列里、回到 DEFAULT/原值，与 UpsertNode 完全一致）。
//
// 空 nodes 是 no-op 返回 (0, nil)，避免 service 层调用前必须特判。
//
// 注意：本方法不返回 RETURNING 行，故不复用 queryMany helper。service 层
// upsertDAGNodes 现状也丢弃了 UpsertNode 返回的 *Node，行为一致。如未来需要
// 拿回写后的 N 行，扩 RETURNING + Scan 循环。
func (s *store) BatchUpsertNodes(ctx context.Context, nodes []Node) (int64, error) {
	if len(nodes) == 0 {
		return 0, nil
	}
	params := sqlc.BatchUpsertTaskDagNodesParams{
		DagKeys:     make([]string, len(nodes)),
		NodeKeys:    make([]string, len(nodes)),
		Titles:      make([]string, len(nodes)),
		NodeTypes:   make([]string, len(nodes)),
		AssignedTos: make([]string, len(nodes)),
		DependsOns:  make([][]byte, len(nodes)),
		CommandRefs: make([]string, len(nodes)),
		Configs:     make([][]byte, len(nodes)),
	}
	for i, n := range nodes {
		params.DagKeys[i] = n.DagKey
		params.NodeKeys[i] = n.NodeKey
		params.Titles[i] = n.Title
		params.NodeTypes[i] = n.NodeType
		params.AssignedTos[i] = n.AssignedTo
		// jsonb[] 元素必须是合法 JSON；空 RawMessage 兜底成 '[]'/`{}` 与 UpsertNode
		// 单行路径一致（单行 sqlc 把 nil []byte 当 NULL 让 DEFAULT 生效，但批量
		// jsonb[] 不能含 NULL 元素 — 不然 PG 报 "array element type ... cannot
		// be null"）。所以这里给 nil 元素显式补 jsonb 默认。
		params.DependsOns[i] = jsonbOrDefault(n.DependsOn, "[]")
		params.CommandRefs[i] = n.CommandRef
		params.Configs[i] = jsonbOrDefault(n.Config, "{}")
	}
	return s.q.BatchUpsertTaskDagNodes(ctx, params)
}

// jsonbOrDefault 把 nil/empty json.RawMessage 兜底成给定默认 JSON 字面量，
// 用于 jsonb[] 元素不能含 SQL NULL 的批量路径。
func jsonbOrDefault(raw json.RawMessage, def string) []byte {
	if len(raw) == 0 {
		return []byte(def)
	}
	return []byte(raw)
}
