-- name: AcquireTaskDagWorkerLease :execrows
INSERT INTO task_dag_worker_leases (target_agent_id, owner_id, lease_expires_at, updated_at)
VALUES (?, ?, (CAST(strftime('%s','now') AS INTEGER) * 1000) + sqlc.arg(lease_ms), (CAST(strftime('%s','now') AS INTEGER) * 1000))
ON CONFLICT (target_agent_id) DO UPDATE
SET owner_id = EXCLUDED.owner_id,
    lease_expires_at = EXCLUDED.lease_expires_at,
    updated_at = (CAST(strftime('%s','now') AS INTEGER) * 1000)
WHERE task_dag_worker_leases.lease_expires_at < (CAST(strftime('%s','now') AS INTEGER) * 1000)
   OR task_dag_worker_leases.owner_id = EXCLUDED.owner_id;

-- name: RenewTaskDagWorkerLease :execrows
UPDATE task_dag_worker_leases
SET lease_expires_at = (CAST(strftime('%s','now') AS INTEGER) * 1000) + sqlc.arg(lease_ms),
    updated_at = (CAST(strftime('%s','now') AS INTEGER) * 1000)
WHERE target_agent_id = sqlc.arg(target_agent_id)
  AND owner_id = sqlc.arg(owner_id)
  AND lease_expires_at >= (CAST(strftime('%s','now') AS INTEGER) * 1000);

-- name: ReleaseTaskDagWorkerLease :execrows
DELETE FROM task_dag_worker_leases
WHERE target_agent_id = ? AND owner_id = ?;
