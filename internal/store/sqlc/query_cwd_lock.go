package sqlc

import "context"

const (
	acquireCwdLockSQL      = `INSERT INTO cwd_instance_locks (cwd, instance_id, pid, acquired_at, heartbeat_at) VALUES ($1, $2, $3, NOW(), NOW()) ON CONFLICT (cwd) DO UPDATE SET instance_id = EXCLUDED.instance_id, pid = EXCLUDED.pid, acquired_at = NOW(), heartbeat_at = NOW() WHERE cwd_instance_locks.instance_id = EXCLUDED.instance_id OR cwd_instance_locks.heartbeat_at < NOW() - INTERVAL '45 seconds';`
	forceAcquireCwdLockSQL = `INSERT INTO cwd_instance_locks (cwd, instance_id, pid, acquired_at, heartbeat_at) VALUES ($1, $2, $3, NOW(), NOW()) ON CONFLICT (cwd) DO UPDATE SET instance_id = EXCLUDED.instance_id, pid = EXCLUDED.pid, acquired_at = NOW(), heartbeat_at = NOW() WHERE cwd_instance_locks.pid = $4;`
	releaseCwdLockSQL      = `DELETE FROM cwd_instance_locks WHERE cwd = $1 AND instance_id = $2;`
	heartbeatCwdLockSQL    = `UPDATE cwd_instance_locks SET heartbeat_at = NOW(), pid = $3 WHERE cwd = $1 AND instance_id = $2;`
	deleteStaleCwdLocksSQL = `DELETE FROM cwd_instance_locks WHERE heartbeat_at < NOW() - INTERVAL '45 seconds';`
	getCwdLockHolderSQL    = `SELECT instance_id, pid, heartbeat_at FROM cwd_instance_locks WHERE cwd = $1;`
)

func scanCwdLockHolderRow(row rowScanner) (CwdLockHolderRow, error) {
	var item CwdLockHolderRow
	err := row.Scan(&item.InstanceID, &item.PID, &item.HeartbeatAt)
	return item, err
}

func (q *Queries) AcquireCwdLock(ctx context.Context, arg AcquireCwdLockParams) (int64, error) {
	return q.execRows(ctx, acquireCwdLockSQL, arg.Cwd, arg.InstanceID, arg.PID)
}

func (q *Queries) ForceAcquireCwdLock(ctx context.Context, arg ForceAcquireCwdLockParams) (int64, error) {
	return q.execRows(ctx, forceAcquireCwdLockSQL, arg.Cwd, arg.InstanceID, arg.PID, arg.HolderPID)
}

func (q *Queries) ReleaseCwdLock(ctx context.Context, arg ReleaseCwdLockParams) (int64, error) {
	return q.execRows(ctx, releaseCwdLockSQL, arg.Cwd, arg.InstanceID)
}

func (q *Queries) HeartbeatCwdLock(ctx context.Context, arg HeartbeatCwdLockParams) error {
	return q.exec(ctx, heartbeatCwdLockSQL, arg.Cwd, arg.InstanceID, arg.PID)
}

func (q *Queries) DeleteStaleCwdLocks(ctx context.Context) (int64, error) {
	return q.execRows(ctx, deleteStaleCwdLocksSQL)
}

func (q *Queries) GetCwdLockHolder(ctx context.Context, cwd string) (CwdLockHolderRow, error) {
	return queryOne(ctx, q, getCwdLockHolderSQL, scanCwdLockHolderRow, cwd)
}
