-- name: InsertTaskDagWorkerLease :execrows
INSERT INTO task_dag_worker_leases (target_agent_id, owner_id, lease_expires_at, updated_at)
VALUES (:target_agent_id, :owner_id, (CAST(strftime('%s','now') AS INTEGER) * 1000) + CAST(:lease_ms AS INTEGER), (CAST(strftime('%s','now') AS INTEGER) * 1000));

-- name: UpdateAcquirableTaskDagWorkerLease :execrows
UPDATE task_dag_worker_leases
SET owner_id = :owner_id,
    lease_expires_at = (CAST(strftime('%s','now') AS INTEGER) * 1000) + CAST(:lease_ms AS INTEGER),
    updated_at = (CAST(strftime('%s','now') AS INTEGER) * 1000)
WHERE target_agent_id = :target_agent_id
  AND (lease_expires_at < (CAST(strftime('%s','now') AS INTEGER) * 1000)
       OR owner_id = :owner_id);

-- name: HasTaskDagWorkerLease :one
SELECT CAST(COUNT(*) AS BOOLEAN)
FROM task_dag_worker_leases
WHERE target_agent_id = :target_agent_id;

-- name: RenewTaskDagWorkerLease :execrows
UPDATE task_dag_worker_leases
SET lease_expires_at = (CAST(strftime('%s','now') AS INTEGER) * 1000) + CAST(:lease_ms AS INTEGER),
    updated_at = (CAST(strftime('%s','now') AS INTEGER) * 1000)
WHERE target_agent_id = :target_agent_id
  AND owner_id = :owner_id
  AND lease_expires_at >= (CAST(strftime('%s','now') AS INTEGER) * 1000);

-- name: ReleaseTaskDagWorkerLease :execrows
DELETE FROM task_dag_worker_leases
WHERE target_agent_id = :target_agent_id AND owner_id = :owner_id;
