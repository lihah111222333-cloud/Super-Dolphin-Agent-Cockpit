-- name: AcquireCwdLock :execrows
INSERT INTO cwd_instance_locks (cwd, instance_id, pid, acquired_at, heartbeat_at)
VALUES ($1, $2, $3, NOW(), NOW())
ON CONFLICT (cwd) DO UPDATE
SET instance_id = EXCLUDED.instance_id,
    pid = EXCLUDED.pid,
    acquired_at = NOW(),
    heartbeat_at = NOW()
WHERE cwd_instance_locks.instance_id = EXCLUDED.instance_id
   OR cwd_instance_locks.heartbeat_at < NOW() - INTERVAL '45 seconds';

-- name: ForceAcquireCwdLock :execrows
INSERT INTO cwd_instance_locks (cwd, instance_id, pid, acquired_at, heartbeat_at)
VALUES ($1, $2, $3, NOW(), NOW())
ON CONFLICT (cwd) DO UPDATE
SET instance_id = EXCLUDED.instance_id,
    pid = EXCLUDED.pid,
    acquired_at = NOW(),
    heartbeat_at = NOW()
WHERE cwd_instance_locks.pid = $4;

-- name: ReleaseCwdLock :execrows
DELETE FROM cwd_instance_locks
WHERE cwd = $1 AND instance_id = $2;

-- name: HeartbeatCwdLock :exec
UPDATE cwd_instance_locks
SET heartbeat_at = NOW(),
    pid = $3
WHERE cwd = $1 AND instance_id = $2;

-- name: DeleteStaleCwdLocks :execrows
DELETE FROM cwd_instance_locks
WHERE heartbeat_at < NOW() - INTERVAL '45 seconds';

-- name: GetCwdLockHolder :one
SELECT instance_id, pid, heartbeat_at
FROM cwd_instance_locks
WHERE cwd = $1;
