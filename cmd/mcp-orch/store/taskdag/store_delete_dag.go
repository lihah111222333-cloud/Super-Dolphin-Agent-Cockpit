package taskdag

import (
	"context"

	"github.com/jackc/pgx/v5"
)

func (s *store) lockDAGForDelete(ctx context.Context, dagKey string) (int64, error) {
	const q = `SELECT id FROM task_dags WHERE dag_key = $1 FOR UPDATE`
	row := sqlcDB(s.q).QueryRow(ctx, q, dagKey)
	var id int64
	if err := row.Scan(&id); err != nil {
		return 0, wrapTaskDAGError(err, "get_for_update", "task_dag")
	}
	return id, nil
}

func (s *store) deleteDAGDependents(ctx context.Context, dagKey string) error {
	for _, stmt := range []struct {
		sql       string
		operation string
		entity    string
	}{
		{sql: `DELETE FROM task_dag_wakeups WHERE dag_key = $1`, operation: "delete", entity: "task_dag_wakeup"},
		{sql: `DELETE FROM task_dag_nodes WHERE dag_key = $1`, operation: "delete", entity: "task_dag_node"},
		{sql: `DELETE FROM task_dag_runs WHERE dag_key = $1`, operation: "delete", entity: "task_dag_run"},
	} {
		if _, err := sqlcDB(s.q).Exec(ctx, stmt.sql, dagKey); err != nil {
			return wrapTaskDAGError(err, stmt.operation, stmt.entity)
		}
	}
	return nil
}

func (s *store) deleteDAGRow(ctx context.Context, dagKey string) (int64, error) {
	const q = `DELETE FROM task_dags WHERE dag_key = $1`
	tag, err := sqlcDB(s.q).Exec(ctx, q, dagKey)
	if err != nil {
		return 0, wrapTaskDAGError(err, "delete", "task_dag")
	}
	rows := tag.RowsAffected()
	if rows == 0 {
		return 0, pgx.ErrNoRows
	}
	return rows, nil
}
