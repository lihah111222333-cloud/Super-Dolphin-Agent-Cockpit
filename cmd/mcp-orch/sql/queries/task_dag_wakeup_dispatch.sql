-- name: EnqueueTaskDagWakeup :execrows
INSERT INTO task_dag_wakeups (dag_key, node_key, run_id, wakeup_kind, target_agent_id, prompt_payload, idempotency_key, next_retry_at, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, (CAST(strftime('%s','now') AS INTEGER) * 1000), (CAST(strftime('%s','now') AS INTEGER) * 1000), (CAST(strftime('%s','now') AS INTEGER) * 1000))
ON CONFLICT (idempotency_key) DO NOTHING;

-- name: ClaimDueTaskDagWakeups :many
UPDATE task_dag_wakeups
SET status = 'dispatching',
    claimed_at = (CAST(strftime('%s','now') AS INTEGER) * 1000),
    claimed_by = sqlc.arg(worker_id),
    lease_expires_at = (CAST(strftime('%s','now') AS INTEGER) * 1000) + sqlc.arg(lease_ms),
    attempt_count = attempt_count + 1,
    updated_at = (CAST(strftime('%s','now') AS INTEGER) * 1000)
WHERE id IN (
    SELECT w.id
    FROM task_dag_wakeups w
    WHERE w.status = 'pending'
      AND w.next_retry_at <= (CAST(strftime('%s','now') AS INTEGER) * 1000)
      AND (
        trim(w.dag_key) = ''
        OR trim(w.node_key) = ''
        OR EXISTS (
          SELECT 1
          FROM task_dag_runs r
          WHERE r.id = w.run_id
            AND r.dag_key = w.dag_key
            AND r.status = 'running'
        )
      )
    ORDER BY next_retry_at, id
    LIMIT sqlc.arg(limit_count)
)
RETURNING id, dag_key, node_key, wakeup_kind, target_agent_id, prompt_payload,
          idempotency_key, status, attempt_count, next_retry_at, claimed_at,
          claimed_by, lease_expires_at, sent_at, bound_turn_id, turn_bound_at,
          last_error, created_at, updated_at, run_id;

-- name: MarkTaskDagWakeupSent :execrows
UPDATE task_dag_wakeups
SET status = 'sent', sent_at = (CAST(strftime('%s','now') AS INTEGER) * 1000), updated_at = (CAST(strftime('%s','now') AS INTEGER) * 1000)
WHERE id = sqlc.arg(id)
  AND status = 'dispatching'
  AND claimed_at = sqlc.arg(claimed_at)
  AND claimed_by = sqlc.arg(claimed_by)
  AND lease_expires_at = sqlc.arg(lease_expires_at)
  AND lease_expires_at >= (CAST(strftime('%s','now') AS INTEGER) * 1000);

-- name: BindTaskDagWakeupTurn :execrows
UPDATE task_dag_wakeups
SET bound_turn_id = ?, turn_bound_at = (CAST(strftime('%s','now') AS INTEGER) * 1000), updated_at = (CAST(strftime('%s','now') AS INTEGER) * 1000)
WHERE id = ? AND status = 'sent' AND sent_at IS NOT NULL AND bound_turn_id IS NULL;

-- name: RetryTaskDagWakeup :execrows
UPDATE task_dag_wakeups
SET status = 'pending',
    next_retry_at = (CAST(strftime('%s','now') AS INTEGER) * 1000) + sqlc.arg(delay_ms),
    last_error = sqlc.arg(last_error),
    claimed_at = NULL,
    claimed_by = '',
    lease_expires_at = NULL,
    updated_at = (CAST(strftime('%s','now') AS INTEGER) * 1000)
WHERE id = sqlc.arg(id)
  AND status = 'dispatching'
  AND claimed_at = sqlc.arg(claimed_at)
  AND claimed_by = sqlc.arg(claimed_by)
  AND lease_expires_at = sqlc.arg(lease_expires_at)
  AND lease_expires_at >= (CAST(strftime('%s','now') AS INTEGER) * 1000)
  AND attempt_count < sqlc.arg(max_attempts);

-- name: FailTaskDagWakeup :execrows
UPDATE task_dag_wakeups
SET status = 'failed', last_error = sqlc.arg(last_error), claimed_at = NULL, claimed_by = '', lease_expires_at = NULL, updated_at = (CAST(strftime('%s','now') AS INTEGER) * 1000)
WHERE id = sqlc.arg(id)
  AND status = 'dispatching'
  AND claimed_at = sqlc.arg(claimed_at)
  AND claimed_by = sqlc.arg(claimed_by)
  AND lease_expires_at = sqlc.arg(lease_expires_at)
  AND lease_expires_at >= (CAST(strftime('%s','now') AS INTEGER) * 1000);
