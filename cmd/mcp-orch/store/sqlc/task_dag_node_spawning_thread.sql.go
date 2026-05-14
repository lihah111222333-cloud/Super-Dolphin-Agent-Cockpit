// Manually authored to match sqlc v1.30 layout for the F1.5 spawning_thread_id
// write entry-point (ADR-009). Mirrors the shape sqlc generate would emit so
// future regeneration produces identical output.

package sqlc

import (
	"context"

	"github.com/jackc/pgx/v5/pgtype"
)

const updateTaskDagNodeSpawningThread = `-- name: UpdateTaskDagNodeSpawningThread :one
WITH old AS (
  SELECT spawning_thread_id AS previous_spawning_thread_id
  FROM task_dag_nodes
  WHERE dag_key = $2 AND node_key = $3
    AND run_id = $4
    AND $4::bigint > 0
),
updated AS (
  UPDATE task_dag_nodes
  SET spawning_thread_id = $1,
      updated_at = NOW()
  WHERE dag_key = $2 AND node_key = $3
    AND run_id = $4
    AND $4::bigint > 0
  RETURNING id, dag_key, node_key, run_id, title, node_type, assigned_to, depends_on,
            status, command_ref, config, result, started_at, finished_at,
            created_at, updated_at, active_turn_id, active_wakeup_id,
            last_event_at, spawning_thread_id
)
SELECT updated.id, updated.dag_key, updated.node_key, updated.run_id, updated.title,
       updated.node_type, updated.assigned_to, updated.depends_on,
       updated.status, updated.command_ref, updated.config, updated.result,
       updated.started_at, updated.finished_at, updated.created_at,
       updated.updated_at, updated.active_turn_id, updated.active_wakeup_id,
       updated.last_event_at, updated.spawning_thread_id,
       old.previous_spawning_thread_id
FROM updated LEFT JOIN old ON TRUE
`

// UpdateTaskDagNodeSpawningThreadParams binds the (new thread id, dag key,
// node key) parameters for the CTE-based UPDATE in
// task_dag_node_spawning_thread.sql.
type UpdateTaskDagNodeSpawningThreadParams struct {
	SpawningThreadID pgtype.Text `json:"spawning_thread_id"`
	DagKey           string      `json:"dag_key"`
	NodeKey          string      `json:"node_key"`
	RunID            int64       `json:"run_id"`
}

// UpdateTaskDagNodeSpawningThreadRow is the row returned by the CTE: full
// node columns plus the previous spawning_thread_id captured before the
// UPDATE took effect. PreviousSpawningThreadID is the value the caller uses
// to decide whether to emit a `node_spawn` event into task_dag_runs.events.
type UpdateTaskDagNodeSpawningThreadRow struct {
	ID                       int64              `json:"id"`
	DagKey                   string             `json:"dag_key"`
	NodeKey                  string             `json:"node_key"`
	RunID                    pgtype.Int8        `json:"run_id"`
	Title                    string             `json:"title"`
	NodeType                 string             `json:"node_type"`
	AssignedTo               string             `json:"assigned_to"`
	DependsOn                []byte             `json:"depends_on"`
	Status                   string             `json:"status"`
	CommandRef               string             `json:"command_ref"`
	Config                   []byte             `json:"config"`
	Result                   []byte             `json:"result"`
	StartedAt                pgtype.Timestamptz `json:"started_at"`
	FinishedAt               pgtype.Timestamptz `json:"finished_at"`
	CreatedAt                pgtype.Timestamptz `json:"created_at"`
	UpdatedAt                pgtype.Timestamptz `json:"updated_at"`
	ActiveTurnID             pgtype.Text        `json:"active_turn_id"`
	ActiveWakeupID           pgtype.Int8        `json:"active_wakeup_id"`
	LastEventAt              pgtype.Timestamptz `json:"last_event_at"`
	SpawningThreadID         pgtype.Text        `json:"spawning_thread_id"`
	PreviousSpawningThreadID pgtype.Text        `json:"previous_spawning_thread_id"`
}

// UpdateTaskDagNodeSpawningThread overrides task_dag_nodes.spawning_thread_id
// for (dag_key, node_key) and returns the full updated row plus the prior
// value captured by the CTE. Returns pgx.ErrNoRows when the node row does not
// exist (caller wraps via wrapTaskDAGError in the store layer).
func (q *Queries) UpdateTaskDagNodeSpawningThread(ctx context.Context, arg UpdateTaskDagNodeSpawningThreadParams) (UpdateTaskDagNodeSpawningThreadRow, error) {
	row := q.db.QueryRow(ctx, updateTaskDagNodeSpawningThread,
		arg.SpawningThreadID,
		arg.DagKey,
		arg.NodeKey,
		arg.RunID,
	)
	var i UpdateTaskDagNodeSpawningThreadRow
	err := row.Scan(
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
		&i.PreviousSpawningThreadID,
	)
	return i, err
}
