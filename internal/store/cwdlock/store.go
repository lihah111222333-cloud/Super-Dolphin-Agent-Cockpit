package cwdlock

import (
	"context"

	platformdb "github.com/anthropic-ai/super-agent-v3/internal/platform/db"
	"github.com/anthropic-ai/super-agent-v3/internal/store/sqlc"
)

type store struct {
	q *sqlc.Queries
}

func NewStore(q *sqlc.Queries) Store {
	return &store{q: q}
}

func (s *store) Acquire(ctx context.Context, params AcquireParams) (int64, error) {
	count, err := s.q.AcquireCwdLock(ctx, sqlc.AcquireCwdLockParams{
		Cwd:        params.Cwd,
		InstanceID: params.InstanceID,
		Pid:        params.PID,
	})
	if err != nil {
		return 0, wrapCwdLockError(err, "acquire", "cwd_lock")
	}
	return count, nil
}

func (s *store) ForceAcquire(ctx context.Context, params ForceAcquireParams) (int64, error) {
	count, err := s.q.ForceAcquireCwdLock(ctx, sqlc.ForceAcquireCwdLockParams{
		Cwd:        params.Cwd,
		InstanceID: params.InstanceID,
		Pid:        params.PID,
		Pid_2:      params.HolderPID,
	})
	if err != nil {
		return 0, wrapCwdLockError(err, "force_acquire", "cwd_lock")
	}
	return count, nil
}

func (s *store) Release(ctx context.Context, params ReleaseParams) (int64, error) {
	count, err := s.q.ReleaseCwdLock(ctx, sqlc.ReleaseCwdLockParams{
		Cwd:        params.Cwd,
		InstanceID: params.InstanceID,
	})
	if err != nil {
		return 0, wrapCwdLockError(err, "release", "cwd_lock")
	}
	return count, nil
}

func (s *store) Heartbeat(ctx context.Context, params HeartbeatParams) error {
	return wrapCwdLockError(s.q.HeartbeatCwdLock(ctx, sqlc.HeartbeatCwdLockParams{
		Cwd:        params.Cwd,
		InstanceID: params.InstanceID,
		Pid:        params.PID,
	}), "heartbeat", "cwd_lock")
}

func (s *store) DeleteStale(ctx context.Context) (int64, error) {
	count, err := s.q.DeleteStaleCwdLocks(ctx)
	if err != nil {
		return 0, wrapCwdLockError(err, "delete_stale", "cwd_lock")
	}
	return count, nil
}

func (s *store) GetHolder(ctx context.Context, cwd string) (*LockHolder, error) {
	row, err := s.q.GetCwdLockHolder(ctx, cwd)
	if err != nil {
		return nil, wrapCwdLockError(err, "get_holder", "cwd_lock")
	}
	result := LockHolder{
		InstanceID:  row.InstanceID,
		PID:         row.Pid,
		HeartbeatAt: row.HeartbeatAt,
	}
	return &result, nil
}

func wrapCwdLockError(err error, operation, entity string) error {
	return platformdb.WrapStoreError(err, operation, entity)
}
