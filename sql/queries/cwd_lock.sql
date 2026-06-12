-- name: AcquireCwdLock :execrows
INSERT INTO cwd_instance_locks (cwd, instance_id, pid, acquired_at, heartbeat_at)
VALUES (?, ?, ?, (CAST(strftime('%s','now') AS INTEGER) * 1000), (CAST(strftime('%s','now') AS INTEGER) * 1000))
ON CONFLICT (cwd) DO UPDATE
SET instance_id = EXCLUDED.instance_id,
    pid = EXCLUDED.pid,
    acquired_at = (CAST(strftime('%s','now') AS INTEGER) * 1000),
    heartbeat_at = (CAST(strftime('%s','now') AS INTEGER) * 1000)
WHERE cwd_instance_locks.instance_id = EXCLUDED.instance_id
   OR cwd_instance_locks.heartbeat_at < ?;

-- name: ForceAcquireCwdLock :execrows
INSERT INTO cwd_instance_locks (cwd, instance_id, pid, acquired_at, heartbeat_at)
VALUES (?, ?, ?, (CAST(strftime('%s','now') AS INTEGER) * 1000), (CAST(strftime('%s','now') AS INTEGER) * 1000))
ON CONFLICT (cwd) DO UPDATE
SET instance_id = EXCLUDED.instance_id,
    pid = EXCLUDED.pid,
    acquired_at = (CAST(strftime('%s','now') AS INTEGER) * 1000),
    heartbeat_at = (CAST(strftime('%s','now') AS INTEGER) * 1000)
WHERE cwd_instance_locks.pid = ?;

-- name: ReleaseCwdLock :execrows
DELETE FROM cwd_instance_locks
WHERE cwd = ? AND instance_id = ?;

-- name: HeartbeatCwdLock :exec
UPDATE cwd_instance_locks
SET heartbeat_at = (CAST(strftime('%s','now') AS INTEGER) * 1000),
    pid = ?
WHERE cwd = ? AND instance_id = ?;

-- name: DeleteStaleCwdLocks :execrows
DELETE FROM cwd_instance_locks
WHERE heartbeat_at < ?;

-- name: GetCwdLockHolder :one
SELECT instance_id, pid, heartbeat_at
FROM cwd_instance_locks
WHERE cwd = ?;
