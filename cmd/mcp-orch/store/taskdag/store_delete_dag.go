package taskdag

import (
	"context"
	"database/sql"

	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/store/sqlc"
)

// lockDAGForDelete 在事务内以 FOR UPDATE 锁定 DAG 行，阻止并发删除竞争。
func (s *store) lockDAGForDelete(ctx context.Context, dagKey string) (int64, error) {
	id, err := s.q.LockTaskDagForDelete(ctx, sqlc.LockTaskDagForDeleteParams{DagKey: dagKey})
	if err != nil {
		return 0, wrapTaskDAGError(err, "get_for_update", "task_dag")
	}
	return id, nil
}

// deleteDAGDependents 在删除 DAG 主行前清除所有关联的 wakeup、节点、run 记录；
// 顺序不能调整：先 wakeups，再 nodes，再 runs，最后 dag row。
func (s *store) deleteDAGDependents(ctx context.Context, dagKey string) error {
	if _, err := s.q.DeleteTaskDagWakeupsByDAG(ctx, sqlc.DeleteTaskDagWakeupsByDAGParams{DagKey: dagKey}); err != nil {
		return wrapTaskDAGError(err, "delete", "task_dag_wakeup")
	}
	if _, err := s.q.DeleteTaskDagNodesByDAG(ctx, sqlc.DeleteTaskDagNodesByDAGParams{DagKey: dagKey}); err != nil {
		return wrapTaskDAGError(err, "delete", "task_dag_node")
	}
	if _, err := s.q.DeleteTaskDagRunsByDAG(ctx, sqlc.DeleteTaskDagRunsByDAGParams{DagKey: dagKey}); err != nil {
		return wrapTaskDAGError(err, "delete", "task_dag_run")
	}
	return nil
}

// deleteDAGRow 删除 DAG 主行并断言受影响行数为 1；0 行返回 sql.ErrNoRows。
func (s *store) deleteDAGRow(ctx context.Context, dagKey string) (int64, error) {
	rows, err := s.q.DeleteTaskDagRow(ctx, sqlc.DeleteTaskDagRowParams{DagKey: dagKey})
	if err != nil {
		return 0, wrapTaskDAGError(err, "delete", "task_dag")
	}
	if rows == 0 {
		return 0, sql.ErrNoRows
	}
	return rows, nil
}
