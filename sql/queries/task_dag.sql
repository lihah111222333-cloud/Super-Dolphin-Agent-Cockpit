-- name: UpsertTaskDag :one
INSERT INTO task_dags (dag_key, title, description, status, created_by, metadata)
VALUES ($1, $2, $3, $4, $5, $6::jsonb)
ON CONFLICT (dag_key) DO UPDATE
SET title = EXCLUDED.title,
    description = EXCLUDED.description,
    status = EXCLUDED.status,
    created_by = EXCLUDED.created_by,
    metadata = EXCLUDED.metadata,
    updated_at = NOW()
RETURNING id, dag_key, title, description, status, created_by, metadata, started_at, finished_at, created_at, updated_at;

-- name: ListTaskDags :many
SELECT id, dag_key, title, description, status, created_by, metadata, started_at, finished_at, created_at, updated_at
FROM task_dags
WHERE ($1::text = '' OR status = $1)
  AND ($2::text = ''
    OR dag_key ILIKE '%' || $2 || '%'
    OR title ILIKE '%' || $2 || '%'
    OR description ILIKE '%' || $2 || '%')
ORDER BY updated_at DESC, id DESC
LIMIT $3;

-- name: GetTaskDag :one
SELECT id, dag_key, title, description, status, created_by, metadata, started_at, finished_at, created_at, updated_at
FROM task_dags
WHERE dag_key = $1;

-- name: UpsertTaskDagNode :one
INSERT INTO task_dag_nodes (dag_key, node_key, title, node_type, assigned_to, depends_on, command_ref, config)
VALUES ($1, $2, $3, $4, $5, $6::jsonb, $7, $8::jsonb)
ON CONFLICT (dag_key, node_key) DO UPDATE
SET title = EXCLUDED.title,
    node_type = EXCLUDED.node_type,
    assigned_to = EXCLUDED.assigned_to,
    depends_on = EXCLUDED.depends_on,
    command_ref = EXCLUDED.command_ref,
    config = EXCLUDED.config,
    updated_at = NOW()
RETURNING id, dag_key, node_key, title, node_type, assigned_to, depends_on, status, command_ref, config, result, started_at, finished_at, created_at, updated_at, active_turn_id, active_wakeup_id, last_event_at;

-- name: UpdateTaskDagNodeStatus :one
UPDATE task_dag_nodes
SET status = $1,
    result = $2::jsonb,
    updated_at = NOW()
WHERE dag_key = $3 AND node_key = $4
RETURNING id, dag_key, node_key, title, node_type, assigned_to, depends_on, status, command_ref, config, result, started_at, finished_at, created_at, updated_at, active_turn_id, active_wakeup_id, last_event_at;

-- name: ListTaskDagNodes :many
SELECT id, dag_key, node_key, title, node_type, assigned_to, depends_on, status, command_ref, config, result, started_at, finished_at, created_at, updated_at, active_turn_id, active_wakeup_id, last_event_at
FROM task_dag_nodes
WHERE dag_key = $1
ORDER BY created_at;

-- name: ListRunningTaskDagNodesByAssignee :many
SELECT id, dag_key, node_key, title, node_type, assigned_to, depends_on, status, command_ref, config, result, started_at, finished_at, created_at, updated_at, active_turn_id, active_wakeup_id, last_event_at
FROM task_dag_nodes
WHERE assigned_to = $1 AND status = 'running'
ORDER BY created_at;

-- name: GetTaskDagForUpdate :one
SELECT id, dag_key, title, description, status, created_by, metadata, started_at, finished_at, created_at, updated_at
FROM task_dags
WHERE dag_key = $1
FOR UPDATE;

-- name: GetTaskDagNodesForUpdate :many
SELECT id, dag_key, node_key, title, node_type, assigned_to, depends_on, status, command_ref, config, result, started_at, finished_at, created_at, updated_at, active_turn_id, active_wakeup_id, last_event_at
FROM task_dag_nodes
WHERE dag_key = $1
ORDER BY created_at, id
FOR UPDATE;

-- name: BindRunningTaskDagNodeTurn :one
UPDATE task_dag_nodes
SET active_turn_id = $1,
    last_event_at = NOW(),
    updated_at = NOW()
WHERE dag_key = $2
  AND node_key = $3
  AND status = 'running'
  AND active_turn_id IS NULL
  AND active_wakeup_id = $4
RETURNING id, dag_key, node_key, title, node_type, assigned_to, depends_on, status, command_ref, config, result, started_at, finished_at, created_at, updated_at, active_turn_id, active_wakeup_id, last_event_at;

-- name: TouchRunningTaskDagNodeEvent :one
UPDATE task_dag_nodes
SET last_event_at = $1,
    updated_at = NOW()
WHERE dag_key = $2
  AND node_key = $3
  AND status = 'running'
  AND active_turn_id = $4
  AND (last_event_at IS NULL OR last_event_at < $1)
RETURNING id, dag_key, node_key, title, node_type, assigned_to, depends_on, status, command_ref, config, result, started_at, finished_at, created_at, updated_at, active_turn_id, active_wakeup_id, last_event_at;

-- name: UpdateRunningTaskDagNodeStatus :one
UPDATE task_dag_nodes
SET status = $1, result = $2::jsonb, active_turn_id = NULL, active_wakeup_id = $3,
    last_event_at = NULL, started_at = COALESCE(started_at, NOW()), updated_at = NOW()
WHERE dag_key = $4 AND node_key = $5 AND status IN ('pending')
RETURNING id, dag_key, node_key, title, node_type, assigned_to, depends_on, status, command_ref, config, result, started_at, finished_at, created_at, updated_at, active_turn_id, active_wakeup_id, last_event_at;

-- name: UpdateAwaitingVerifyTaskDagNodeStatus :one
UPDATE task_dag_nodes
SET status = $1, result = $2::jsonb, active_turn_id = NULL, active_wakeup_id = NULL, updated_at = NOW()
WHERE dag_key = $3 AND node_key = $4 AND status IN ('running')
RETURNING id, dag_key, node_key, title, node_type, assigned_to, depends_on, status, command_ref, config, result, started_at, finished_at, created_at, updated_at, active_turn_id, active_wakeup_id, last_event_at;

-- name: CompleteTaskDagNode :one
UPDATE task_dag_nodes
SET status = $1, result = $2::jsonb, active_turn_id = NULL, active_wakeup_id = NULL,
    finished_at = COALESCE(finished_at, NOW()), updated_at = NOW()
WHERE dag_key = $3 AND node_key = $4 AND status IN ('running', 'awaiting_verify')
RETURNING id, dag_key, node_key, title, node_type, assigned_to, depends_on, status, command_ref, config, result, started_at, finished_at, created_at, updated_at, active_turn_id, active_wakeup_id, last_event_at;

-- name: UpdateTaskDagNodeStatusFlexible :one
UPDATE task_dag_nodes
SET status = $1, result = $2::jsonb, updated_at = NOW()
WHERE dag_key = $3 AND node_key = $4
RETURNING id, dag_key, node_key, title, node_type, assigned_to, depends_on, status, command_ref, config, result, started_at, finished_at, created_at, updated_at, active_turn_id, active_wakeup_id, last_event_at;

-- name: EnqueueTaskDagWakeup :execrows
INSERT INTO task_dag_wakeups (dag_key, node_key, wakeup_kind, target_agent_id, prompt_payload, idempotency_key)
VALUES ($1, $2, $3, $4, $5::jsonb, $6)
ON CONFLICT (idempotency_key) DO NOTHING;

-- name: ClaimDueTaskDagWakeups :many
UPDATE task_dag_wakeups
SET status = 'dispatching', claimed_at = NOW(), claimed_by = $1,
    lease_expires_at = NOW() + $2::interval, attempt_count = attempt_count + 1, updated_at = NOW()
WHERE id IN (
    SELECT id
    FROM task_dag_wakeups
    WHERE status = 'pending' AND next_retry_at <= NOW()
    ORDER BY next_retry_at, id
    LIMIT $3
    FOR UPDATE SKIP LOCKED
)
RETURNING id, dag_key, node_key, wakeup_kind, target_agent_id, prompt_payload, idempotency_key, status, attempt_count, next_retry_at, claimed_at, claimed_by, lease_expires_at, sent_at, bound_turn_id, turn_bound_at, last_error, created_at, updated_at;

-- name: MarkTaskDagWakeupSent :execrows
UPDATE task_dag_wakeups
SET status = 'sent', sent_at = NOW(), updated_at = NOW()
WHERE id = $1 AND status = 'dispatching' AND claimed_at = $2;

-- name: BindTaskDagWakeupTurn :execrows
UPDATE task_dag_wakeups
SET bound_turn_id = $1, turn_bound_at = NOW(), updated_at = NOW()
WHERE id = $2 AND status = 'sent' AND sent_at IS NOT NULL AND bound_turn_id IS NULL;

-- name: RetryTaskDagWakeup :execrows
UPDATE task_dag_wakeups
SET status = 'pending', next_retry_at = NOW() + $1::interval, last_error = $2,
    claimed_at = NULL, claimed_by = '', lease_expires_at = NULL, updated_at = NOW()
WHERE id = $3 AND status = 'dispatching' AND claimed_at = $4 AND attempt_count < 8;

-- name: FailTaskDagWakeup :execrows
UPDATE task_dag_wakeups
SET status = 'failed', last_error = $1, claimed_at = NULL, claimed_by = '', lease_expires_at = NULL, updated_at = NOW()
WHERE id = $2 AND status = 'dispatching' AND claimed_at = $3;

-- name: AcquireTaskDagWorkerLease :execrows
INSERT INTO task_dag_worker_leases (target_agent_id, owner_id, lease_expires_at, updated_at)
VALUES ($1, $2, NOW() + $3::interval, NOW())
ON CONFLICT (target_agent_id) DO UPDATE
SET owner_id = EXCLUDED.owner_id,
    lease_expires_at = EXCLUDED.lease_expires_at,
    updated_at = NOW()
WHERE task_dag_worker_leases.lease_expires_at < NOW()
   OR task_dag_worker_leases.owner_id = EXCLUDED.owner_id;

-- name: RenewTaskDagWorkerLease :execrows
UPDATE task_dag_worker_leases
SET lease_expires_at = NOW() + $1::interval,
    updated_at = NOW()
WHERE target_agent_id = $2 AND owner_id = $3 AND lease_expires_at >= NOW();

-- name: ReleaseTaskDagWorkerLease :exec
DELETE FROM task_dag_worker_leases
WHERE target_agent_id = $1 AND owner_id = $2;

-- name: ReclaimStaleDispatchingTaskDagWakeups :execrows
UPDATE task_dag_wakeups
SET status = 'pending', claimed_at = NULL, claimed_by = '', lease_expires_at = NULL, updated_at = NOW()
WHERE status = 'dispatching' AND lease_expires_at < NOW();

-- name: ListSentUnboundTaskDagWakeups :many
SELECT id, dag_key, node_key, wakeup_kind, target_agent_id, prompt_payload, idempotency_key, status, attempt_count, next_retry_at, claimed_at, claimed_by, lease_expires_at, sent_at, bound_turn_id, turn_bound_at, last_error, created_at, updated_at
FROM task_dag_wakeups
WHERE target_agent_id = $1 AND status = 'sent' AND sent_at IS NOT NULL AND bound_turn_id IS NULL
ORDER BY sent_at DESC, id DESC;

-- name: ListPendingOrDispatchingTaskDagWakeups :many
SELECT id, dag_key, node_key, wakeup_kind, target_agent_id, prompt_payload, idempotency_key, status, attempt_count, next_retry_at, claimed_at, claimed_by, lease_expires_at, sent_at, bound_turn_id, turn_bound_at, last_error, created_at, updated_at
FROM task_dag_wakeups
WHERE status IN ('pending', 'dispatching')
ORDER BY next_retry_at, id;

-- name: GetTaskDagWakeup :one
SELECT id, dag_key, node_key, wakeup_kind, target_agent_id, prompt_payload, idempotency_key, status, attempt_count, next_retry_at, claimed_at, claimed_by, lease_expires_at, sent_at, bound_turn_id, turn_bound_at, last_error, created_at, updated_at
FROM task_dag_wakeups
WHERE id = $1;
