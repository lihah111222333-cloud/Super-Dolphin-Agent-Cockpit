-- name: UpdateTaskDagNodeSpawningThread :execrows
UPDATE task_dag_nodes
SET spawning_thread_id = :spawning_thread_id,
    updated_at = (CAST(strftime('%s','now') AS INTEGER) * 1000)
WHERE dag_key = :dag_key
  AND node_key = :node_key
  AND run_id = :run_id
  AND :run_id > 0
  AND status NOT IN ('done', 'failed', 'cancelled', 'skipped')
  AND (
      spawning_thread_id IS NULL
      OR TRIM(spawning_thread_id) = ''
      OR TRIM(spawning_thread_id) = TRIM(COALESCE(:spawning_thread_id, ''))
  );
