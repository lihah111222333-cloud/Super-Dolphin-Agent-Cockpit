-- name: UpsertTaskDagNode :one
INSERT INTO task_dag_nodes (dag_key, node_key, title, node_type, assigned_to, depends_on, reads, writes, command_ref, config, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, (CAST(strftime('%s','now') AS INTEGER) * 1000), (CAST(strftime('%s','now') AS INTEGER) * 1000))
ON CONFLICT (dag_key, node_key) WHERE run_id IS NULL DO UPDATE
SET title = EXCLUDED.title,
    node_type = EXCLUDED.node_type,
    assigned_to = EXCLUDED.assigned_to,
    depends_on = EXCLUDED.depends_on,
    reads = EXCLUDED.reads,
    writes = EXCLUDED.writes,
    command_ref = EXCLUDED.command_ref,
    config = EXCLUDED.config,
    updated_at = (CAST(strftime('%s','now') AS INTEGER) * 1000)
RETURNING id, dag_key, node_key, title, node_type, assigned_to, CAST(depends_on AS BLOB) AS depends_on,
          status, command_ref, CAST(config AS BLOB) AS config, CAST(result AS BLOB) AS result, started_at, finished_at,
          created_at, updated_at, active_turn_id, active_wakeup_id,
          last_event_at, run_id, CAST(reads AS BLOB) AS reads, CAST(writes AS BLOB) AS writes, spawning_thread_id;

-- name: PatchTaskDagNodeConfigIfUnchanged :one
UPDATE task_dag_nodes
SET config = sqlc.arg('config'), updated_at = (CAST(strftime('%s','now') AS INTEGER) * 1000)
WHERE dag_key = sqlc.arg('dag_key')
  AND node_key = sqlc.arg('node_key')
  AND run_id = sqlc.arg('run_id')
  AND sqlc.arg('run_id') > 0
  AND config = sqlc.arg('previous_config')
  AND status NOT IN ('done', 'failed', 'cancelled', 'skipped')
RETURNING id, dag_key, node_key, title, node_type, assigned_to, CAST(depends_on AS BLOB) AS depends_on,
          status, command_ref, CAST(config AS BLOB) AS config, CAST(result AS BLOB) AS result, started_at, finished_at,
          created_at, updated_at, active_turn_id, active_wakeup_id,
          last_event_at, run_id, CAST(reads AS BLOB) AS reads, CAST(writes AS BLOB) AS writes, spawning_thread_id;

-- name: DeleteTaskDagNode :execrows
DELETE FROM task_dag_nodes
WHERE dag_key = ?
  AND node_key = ?
  AND run_id IS NULL
  AND status IN ('pending', 'ready');

-- name: AssignTaskDagNode :one
UPDATE task_dag_nodes
SET assigned_to = ?,
    updated_at = (CAST(strftime('%s','now') AS INTEGER) * 1000)
WHERE dag_key = ?
  AND node_key = ?
  AND run_id = ?
  AND status IN ('pending', 'ready')
RETURNING id, dag_key, node_key, title, node_type, assigned_to, CAST(depends_on AS BLOB) AS depends_on,
          status, command_ref, CAST(config AS BLOB) AS config, CAST(result AS BLOB) AS result, started_at, finished_at,
          created_at, updated_at, active_turn_id, active_wakeup_id,
          last_event_at, run_id, CAST(reads AS BLOB) AS reads, CAST(writes AS BLOB) AS writes, spawning_thread_id;

-- name: UpdateTaskDagNodeStatusIfCurrent :one
UPDATE task_dag_nodes
SET status = sqlc.arg('status'), result = sqlc.arg('result'), updated_at = (CAST(strftime('%s','now') AS INTEGER) * 1000)
WHERE dag_key = sqlc.arg('dag_key') AND node_key = sqlc.arg('node_key')
  AND run_id = sqlc.arg('run_id')
  AND sqlc.arg('run_id') > 0
  AND status = sqlc.arg('expected_status')
RETURNING id, dag_key, node_key, title, node_type, assigned_to, CAST(depends_on AS BLOB) AS depends_on,
          status, command_ref, CAST(config AS BLOB) AS config, CAST(result AS BLOB) AS result, started_at, finished_at,
          created_at, updated_at, active_turn_id, active_wakeup_id,
          last_event_at, run_id, CAST(reads AS BLOB) AS reads, CAST(writes AS BLOB) AS writes, spawning_thread_id;

-- name: ClaimTaskDagNodeOutputMaterialization :one
UPDATE task_dag_nodes
SET result = sqlc.arg('result'), updated_at = (CAST(strftime('%s','now') AS INTEGER) * 1000)
WHERE dag_key = sqlc.arg('dag_key')
  AND node_key = sqlc.arg('node_key')
  AND run_id = sqlc.arg('run_id')
  AND sqlc.arg('run_id') > 0
  AND status IN ('ready', 'running')
RETURNING id, dag_key, node_key, title, node_type, assigned_to, CAST(depends_on AS BLOB) AS depends_on,
          status, command_ref, CAST(config AS BLOB) AS config, CAST(result AS BLOB) AS result, started_at, finished_at,
          created_at, updated_at, active_turn_id, active_wakeup_id,
          last_event_at, run_id, CAST(reads AS BLOB) AS reads, CAST(writes AS BLOB) AS writes, spawning_thread_id;

-- name: FailTaskDagNodeIfNonTerminal :one
UPDATE task_dag_nodes
SET status = sqlc.arg('status'), result = sqlc.arg('result'), updated_at = (CAST(strftime('%s','now') AS INTEGER) * 1000)
WHERE task_dag_nodes.dag_key = sqlc.arg('dag_key')
  AND task_dag_nodes.node_key = sqlc.arg('node_key')
  AND task_dag_nodes.run_id = sqlc.arg('run_id')
  AND sqlc.arg('run_id') > 0
  AND status NOT IN ('done', 'failed', 'cancelled', 'skipped')
  AND (
    CAST(sqlc.arg('wakeup_id') AS INTEGER) = 0
    OR (
      task_dag_nodes.active_wakeup_id = CAST(sqlc.arg('wakeup_id') AS INTEGER)
      AND EXISTS (
        SELECT 1
        FROM task_dag_wakeups w
        WHERE w.id = CAST(sqlc.arg('wakeup_id') AS INTEGER)
          AND w.run_id = task_dag_nodes.run_id
          AND w.dag_key = task_dag_nodes.dag_key
          AND w.node_key = task_dag_nodes.node_key
          AND w.attempt_count = sqlc.arg('wakeup_attempt')
      )
    )
  )
RETURNING id, dag_key, node_key, title, node_type, assigned_to, CAST(depends_on AS BLOB) AS depends_on,
          status, command_ref, CAST(config AS BLOB) AS config, CAST(result AS BLOB) AS result, started_at, finished_at,
          created_at, updated_at, active_turn_id, active_wakeup_id,
          last_event_at, run_id, CAST(reads AS BLOB) AS reads, CAST(writes AS BLOB) AS writes, spawning_thread_id;

-- name: CascadeFailPendingTaskDagNode :execrows
UPDATE task_dag_nodes
SET status = 'failed', result = sqlc.arg('result'), updated_at = (CAST(strftime('%s','now') AS INTEGER) * 1000)
WHERE dag_key = sqlc.arg('dag_key')
  AND node_key = sqlc.arg('node_key')
  AND run_id = sqlc.arg('run_id')
  AND sqlc.arg('run_id') > 0
  AND status = 'pending';

-- name: PromoteSingleNodePendingToReady :execrows
UPDATE task_dag_nodes
SET status = 'ready',
    updated_at = (CAST(strftime('%s','now') AS INTEGER) * 1000)
WHERE dag_key = ?
  AND node_key = ?
  AND run_id = sqlc.arg(run_id)
  AND sqlc.arg(run_id) > 0
  AND status = 'pending';
