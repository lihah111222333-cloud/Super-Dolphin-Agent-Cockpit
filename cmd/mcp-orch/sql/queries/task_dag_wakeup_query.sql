-- name: ReclaimStaleDispatchingTaskDagWakeups :execrows
UPDATE task_dag_wakeups
SET status = 'pending', claimed_at = NULL, claimed_by = '', lease_expires_at = NULL, updated_at = (CAST(strftime('%s','now') AS INTEGER) * 1000)
WHERE status = 'dispatching'
  AND lease_expires_at < (CAST(strftime('%s','now') AS INTEGER) * 1000)
  AND id NOT IN (
    SELECT n.active_wakeup_id
    FROM task_dag_nodes n
    WHERE n.run_id = task_dag_wakeups.run_id
      AND n.dag_key = task_dag_wakeups.dag_key
      AND n.node_key = task_dag_wakeups.node_key
      AND n.status = 'running'
      AND n.active_wakeup_id IS NOT NULL
  );

-- name: ListSentUnboundTaskDagWakeups :many
SELECT id, dag_key, node_key, wakeup_kind, target_agent_id,
       idempotency_key, status, attempt_count, next_retry_at, claimed_at,
       claimed_by, lease_expires_at, sent_at, bound_turn_id, turn_bound_at,
       last_error, created_at, updated_at, run_id
FROM task_dag_wakeups
WHERE target_agent_id = :target_agent_id AND status = 'sent' AND sent_at IS NOT NULL AND bound_turn_id IS NULL
ORDER BY sent_at DESC, id DESC;

-- name: ListPendingOrDispatchingTaskDagWakeups :many
SELECT id, dag_key, node_key, wakeup_kind, target_agent_id,
       idempotency_key, status, attempt_count, next_retry_at, claimed_at,
       claimed_by, lease_expires_at, sent_at, bound_turn_id, turn_bound_at,
       last_error, created_at, updated_at, run_id
FROM task_dag_wakeups
WHERE status IN ('pending', 'dispatching')
ORDER BY next_retry_at, id;

-- name: HasPendingOrDispatchingTaskDagWakeupForNode :one
SELECT CAST(COUNT(*) AS BOOLEAN)
FROM task_dag_wakeups
WHERE run_id = :run_id
  AND dag_key = :dag_key
  AND node_key = :node_key
  AND (
    status IN ('pending', 'dispatching')
    OR (status = 'sent' AND sent_at IS NOT NULL AND bound_turn_id IS NULL)
  );

-- name: GetTaskDagWakeup :one
SELECT id, dag_key, node_key, wakeup_kind, target_agent_id, prompt_payload,
       idempotency_key, status, attempt_count, next_retry_at, claimed_at,
       claimed_by, lease_expires_at, sent_at, bound_turn_id, turn_bound_at,
       last_error, created_at, updated_at, run_id
FROM task_dag_wakeups
WHERE id = :id;

-- name: GetTaskDagWakeupByIdempotencyKey :one
SELECT id, dag_key, node_key, wakeup_kind, target_agent_id, prompt_payload,
       idempotency_key, status, attempt_count, next_retry_at, claimed_at,
       claimed_by, lease_expires_at, sent_at, bound_turn_id, turn_bound_at,
       last_error, created_at, updated_at, run_id
FROM task_dag_wakeups
WHERE idempotency_key = :idempotency_key;

-- name: GetLatestTaskDagWakeupClaimedAtForWorker :one
SELECT claimed_at
FROM task_dag_wakeups
WHERE claimed_by = :worker_id
  AND claimed_at IS NOT NULL
ORDER BY claimed_at DESC
LIMIT 1;
