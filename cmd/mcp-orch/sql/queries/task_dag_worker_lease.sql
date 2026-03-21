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
