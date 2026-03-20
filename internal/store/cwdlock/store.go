package cwdlock

import (
	"context"

	"github.com/anthropic-ai/super-agent-v3/internal/store/sqlc"
)

type store struct {
	q *sqlc.Queries
}

func NewStore(q *sqlc.Queries) Store {
	return &store{q: q}
}

func (s *store) Acquire(ctx context.Context, params AcquireParams) (int64, error) {
	return s.q.AcquireCwdLock(ctx, sqlc.AcquireCwdLockParams{
		Cwd:        params.Cwd,
		InstanceID: params.InstanceID,
		PID:        params.PID,
	})
}

func (s *store) ForceAcquire(ctx context.Context, params ForceAcquireParams) (int64, error) {
	return s.q.ForceAcquireCwdLock(ctx, sqlc.ForceAcquireCwdLockParams{
		Cwd:        params.Cwd,
		InstanceID: params.InstanceID,
		PID:        params.PID,
		HolderPID:  params.HolderPID,
	})
}

func (s *store) Release(ctx context.Context, params ReleaseParams) (int64, error) {
	return s.q.ReleaseCwdLock(ctx, sqlc.ReleaseCwdLockParams{
		Cwd:        params.Cwd,
		InstanceID: params.InstanceID,
	})
}

func (s *store) Heartbeat(ctx context.Context, params HeartbeatParams) error {
	return s.q.HeartbeatCwdLock(ctx, sqlc.HeartbeatCwdLockParams{
		Cwd:        params.Cwd,
		InstanceID: params.InstanceID,
		PID:        params.PID,
	})
}

func (s *store) DeleteStale(ctx context.Context) (int64, error) {
	return s.q.DeleteStaleCwdLocks(ctx)
}

func (s *store) GetHolder(ctx context.Context, cwd string) (*LockHolder, error) {
	row, err := s.q.GetCwdLockHolder(ctx, cwd)
	if err != nil {
		return nil, err
	}
	result := LockHolder{
		InstanceID:  row.InstanceID,
		PID:         row.PID,
		HeartbeatAt: row.HeartbeatAt,
	}
	return &result, nil
}
