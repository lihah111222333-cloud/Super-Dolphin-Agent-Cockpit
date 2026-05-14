-- name: EnqueueTaskDagWakeup :execrows
INSERT INTO task_dag_wakeups (dag_key, node_key, run_id, wakeup_kind, target_agent_id, prompt_payload, idempotency_key)
VALUES ($1, $2, $3::bigint, $4, $5, $6::jsonb, $7)
ON CONFLICT (idempotency_key) DO NOTHING;

-- name: ClaimDueTaskDagWakeups :many
UPDATE task_dag_wakeups
SET status = 'dispatching', claimed_at = NOW(), claimed_by = $1,
    lease_expires_at = NOW() + $2::interval, attempt_count = attempt_count + 1, updated_at = NOW()
WHERE id IN (
    SELECT w.id
    FROM task_dag_wakeups w
    WHERE w.status = 'pending'
      AND w.next_retry_at <= NOW()
      AND (
        BTRIM(w.dag_key) = ''
        OR BTRIM(w.node_key) = ''
        OR EXISTS (
          SELECT 1
          FROM task_dag_runs r
          WHERE r.id = w.run_id
            AND r.dag_key = w.dag_key
            AND r.status = 'running'
        )
      )
    ORDER BY next_retry_at, id
    LIMIT $3
    FOR UPDATE SKIP LOCKED
)
RETURNING id, dag_key, node_key, run_id, wakeup_kind, target_agent_id, prompt_payload, idempotency_key, status, attempt_count, next_retry_at, claimed_at, claimed_by, lease_expires_at, sent_at, bound_turn_id, turn_bound_at, last_error, created_at, updated_at;

-- name: MarkTaskDagWakeupSent :execrows
UPDATE task_dag_wakeups
SET status = 'sent', sent_at = NOW(), updated_at = NOW()
WHERE id = $1
  AND status = 'dispatching'
  AND claimed_at = $2
  AND claimed_by = $3
  AND lease_expires_at = $4
  AND lease_expires_at >= NOW();

-- name: BindTaskDagWakeupTurn :execrows
UPDATE task_dag_wakeups
SET bound_turn_id = $1, turn_bound_at = NOW(), updated_at = NOW()
WHERE id = $2 AND status = 'sent' AND sent_at IS NOT NULL AND bound_turn_id IS NULL;

-- name: RetryTaskDagWakeup :execrows
UPDATE task_dag_wakeups
SET status = 'pending', next_retry_at = NOW() + $1::interval, last_error = $2,
    claimed_at = NULL, claimed_by = '', lease_expires_at = NULL, updated_at = NOW()
WHERE id = $3
  AND status = 'dispatching'
  AND claimed_at = $4
  AND claimed_by = $5
  AND lease_expires_at = $6
  AND lease_expires_at >= NOW()
  AND attempt_count < 8;

-- name: FailTaskDagWakeup :execrows
UPDATE task_dag_wakeups
SET status = 'failed', last_error = $1, claimed_at = NULL, claimed_by = '', lease_expires_at = NULL, updated_at = NOW()
WHERE id = $2
  AND status = 'dispatching'
  AND claimed_at = $3
  AND claimed_by = $4
  AND lease_expires_at = $5
  AND lease_expires_at >= NOW();
