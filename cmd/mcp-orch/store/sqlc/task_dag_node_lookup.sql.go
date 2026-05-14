// Manually authored to match sqlc v1.30 layout for the ADR-017 v1.2 §2.2
// reverse-lookup entry-point (DAG turn.completed subscriber). Mirrors the
// shape sqlc generate would emit so future regeneration produces identical
// output. See cmd/mcp-orch/sqlc.yaml HAND-MAINTAINED SQLC OUTPUT note for
// the rationale (migration 0083 DO $$ blocks not parseable by sqlc v1.30).

package sqlc

import (
	"context"
)

const lookupNodesBySpawningThread = `-- name: LookupNodesBySpawningThread :many
SELECT id, dag_key, node_key, run_id, title, node_type, assigned_to, depends_on, status, command_ref, config, result, started_at, finished_at, created_at, updated_at, active_turn_id, active_wakeup_id, last_event_at, spawning_thread_id
FROM task_dag_nodes
WHERE spawning_thread_id = $1
  AND spawning_thread_id IS NOT NULL
ORDER BY updated_at DESC, id DESC
`

// LookupNodesBySpawningThread reverses task_dag_nodes.spawning_thread_id back
// to the set of nodes that spawned the given child thread id. Empty result
// slice (not pgx.ErrNoRows) means no node currently carries this thread id;
// N>1 results are a normal occurrence on retry / recovery chains since the
// partial index idx_task_dag_nodes_spawning_thread_id has no UNIQUE clause.
// Callers (ADR-017 §2.2) iterate and attempt status advancement on every row.
func (q *Queries) LookupNodesBySpawningThread(ctx context.Context, spawningThreadID string) ([]TaskDagNode, error) {
	rows, err := q.db.Query(ctx, lookupNodesBySpawningThread, spawningThreadID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []TaskDagNode{}
	for rows.Next() {
		var i TaskDagNode
		if err := rows.Scan(
			&i.ID,
			&i.DagKey,
			&i.NodeKey,
			&i.RunID,
			&i.Title,
			&i.NodeType,
			&i.AssignedTo,
			&i.DependsOn,
			&i.Status,
			&i.CommandRef,
			&i.Config,
			&i.Result,
			&i.StartedAt,
			&i.FinishedAt,
			&i.CreatedAt,
			&i.UpdatedAt,
			&i.ActiveTurnID,
			&i.ActiveWakeupID,
			&i.LastEventAt,
			&i.SpawningThreadID,
		); err != nil {
			return nil, err
		}
		items = append(items, i)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return items, nil
}
