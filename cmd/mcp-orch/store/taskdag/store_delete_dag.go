package taskdag

import (
	"context"

	"github.com/jackc/pgx/v5"
)

func (s *store) lockDAGForDelete(ctx context.Context, dagKey string) (int64, error) {
	id, err := s.q.LockTaskDagForDelete(ctx, dagKey)
	if err != nil {
		return 0, wrapTaskDAGError(err, "get_for_update", "task_dag")
	}
	return id, nil
}

func (s *store) deleteDAGDependents(ctx context.Context, dagKey string) error {
	if _, err := s.q.DeleteTaskDagWakeupsByDAG(ctx, dagKey); err != nil {
		return wrapTaskDAGError(err, "delete", "task_dag_wakeup")
	}
	if _, err := s.q.DeleteTaskDagNodesByDAG(ctx, dagKey); err != nil {
		return wrapTaskDAGError(err, "delete", "task_dag_node")
	}
	if _, err := s.q.DeleteTaskDagRunsByDAG(ctx, dagKey); err != nil {
		return wrapTaskDAGError(err, "delete", "task_dag_run")
	}
	return nil
}

func (s *store) deleteDAGRow(ctx context.Context, dagKey string) (int64, error) {
	rows, err := s.q.DeleteTaskDagRow(ctx, dagKey)
	if err != nil {
		return 0, wrapTaskDAGError(err, "delete", "task_dag")
	}
	if rows == 0 {
		return 0, pgx.ErrNoRows
	}
	return rows, nil
}
