-- name: InsertTaskDagWakeup :execrows
INSERT INTO task_dag_wakeups (dag_key, node_key, run_id, wakeup_kind, target_agent_id, prompt_payload, idempotency_key, next_retry_at, created_at, updated_at)
VALUES (:dag_key, :node_key, :run_id, :wakeup_kind, :target_agent_id, :prompt_payload, :idempotency_key, (CAST(strftime('%s','now') AS INTEGER) * 1000), (CAST(strftime('%s','now') AS INTEGER) * 1000), (CAST(strftime('%s','now') AS INTEGER) * 1000));

-- name: ClaimDueTaskDagWakeups :execrows
UPDATE task_dag_wakeups
SET status = 'dispatching',
    claimed_at = :claimed_at,
    claimed_by = :worker_id,
    lease_expires_at = :lease_expires_at,
    attempt_count = attempt_count + 1,
    updated_at = (CAST(strftime('%s','now') AS INTEGER) * 1000)
WHERE
  id IN (
    SELECT w.id
    FROM task_dag_wakeups w
    WHERE w.status = 'pending'
      AND w.next_retry_at <= (CAST(strftime('%s','now') AS INTEGER) * 1000)
      AND (
        trim(w.dag_key) = ''
        OR trim(w.node_key) = ''
        OR w.run_id IN (
          SELECT r.id
          FROM task_dag_runs r
          WHERE r.id = w.run_id
            AND r.dag_key = w.dag_key
            AND r.status = 'running'
        )
      )
    ORDER BY next_retry_at, id
    LIMIT :limit_count
);

-- name: ListTaskDagWakeupsByClaimFence :many
SELECT id, dag_key, node_key, wakeup_kind, target_agent_id, prompt_payload,
       idempotency_key, status, attempt_count, next_retry_at, claimed_at,
       claimed_by, lease_expires_at, sent_at, bound_turn_id, turn_bound_at,
       last_error, created_at, updated_at, run_id
FROM task_dag_wakeups
WHERE status = 'dispatching'
  AND claimed_by = :worker_id
  AND claimed_at = :claimed_at
  AND lease_expires_at = :lease_expires_at
ORDER BY next_retry_at, id;

-- name: RenewTaskDagWakeupLease :execrows
UPDATE task_dag_wakeups
SET lease_expires_at = (CAST(strftime('%s','now') AS INTEGER) * 1000) + CAST(:lease_ms AS INTEGER),
    updated_at = (CAST(strftime('%s','now') AS INTEGER) * 1000)
WHERE id = :id
  AND status = 'dispatching'
  AND claimed_at = :claimed_at
  AND claimed_by = :claimed_by
  AND lease_expires_at = :lease_expires_at
  AND lease_expires_at >= (CAST(strftime('%s','now') AS INTEGER) * 1000);

-- name: MarkTaskDagWakeupSent :execrows
UPDATE task_dag_wakeups
SET status = 'sent', sent_at = (CAST(strftime('%s','now') AS INTEGER) * 1000), updated_at = (CAST(strftime('%s','now') AS INTEGER) * 1000)
WHERE id = :id
  AND status = 'dispatching'
  AND claimed_at = :claimed_at
  AND claimed_by = :claimed_by
  AND lease_expires_at = :lease_expires_at
  AND lease_expires_at >= (CAST(strftime('%s','now') AS INTEGER) * 1000);

-- name: BindTaskDagWakeupTurn :execrows
UPDATE task_dag_wakeups
SET bound_turn_id = :bound_turn_id, turn_bound_at = (CAST(strftime('%s','now') AS INTEGER) * 1000), updated_at = (CAST(strftime('%s','now') AS INTEGER) * 1000)
WHERE id = :id AND status = 'sent' AND sent_at IS NOT NULL AND bound_turn_id IS NULL;

-- name: RetryTaskDagWakeup :execrows
UPDATE task_dag_wakeups
SET status = 'pending',
    next_retry_at = (CAST(strftime('%s','now') AS INTEGER) * 1000) + CAST(:delay_ms AS INTEGER),
    last_error = :last_error,
    claimed_at = NULL,
    claimed_by = '',
    lease_expires_at = NULL,
    updated_at = (CAST(strftime('%s','now') AS INTEGER) * 1000)
WHERE id = :id
  AND status = 'dispatching'
  AND claimed_at = :claimed_at
  AND claimed_by = :claimed_by
  AND lease_expires_at = :lease_expires_at
  AND lease_expires_at >= (CAST(strftime('%s','now') AS INTEGER) * 1000)
  AND attempt_count < :max_attempts;

-- name: FailTaskDagWakeup :execrows
UPDATE task_dag_wakeups
SET status = 'failed', last_error = :last_error, claimed_at = NULL, claimed_by = '', lease_expires_at = NULL, updated_at = (CAST(strftime('%s','now') AS INTEGER) * 1000)
WHERE id = :id
  AND status = 'dispatching'
  AND claimed_at = :claimed_at
  AND claimed_by = :claimed_by
  AND lease_expires_at = :lease_expires_at
  AND lease_expires_at >= (CAST(strftime('%s','now') AS INTEGER) * 1000);
