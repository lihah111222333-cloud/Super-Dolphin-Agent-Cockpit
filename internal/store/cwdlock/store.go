package cwdlock

import (
	"context"
	"time"

	platformdb "github.com/anthropic-ai/super-agent-v3/internal/platform/db"
	"github.com/anthropic-ai/super-agent-v3/internal/store/sqlc"
)

// querier is the narrow subset of sqlc.Queries this store depends on.
type querier interface {
	AcquireCwdLock(ctx context.Context, arg sqlc.AcquireCwdLockParams) (int64, error)
	ForceAcquireCwdLock(ctx context.Context, arg sqlc.ForceAcquireCwdLockParams) (int64, error)
	ReleaseCwdLock(ctx context.Context, arg sqlc.ReleaseCwdLockParams) (int64, error)
	HeartbeatCwdLock(ctx context.Context, arg sqlc.HeartbeatCwdLockParams) error
	DeleteStaleCwdLocks(ctx context.Context, arg sqlc.DeleteStaleCwdLocksParams) (int64, error)
	GetCwdLockHolder(ctx context.Context, arg sqlc.GetCwdLockHolderParams) (sqlc.GetCwdLockHolderRow, error)
}

const staleHeartbeatThreshold = 45 * time.Second

type store struct {
	q querier
}

// NewStore 创建存储。
func NewStore(q *sqlc.Queries) Store {
	return &store{q: q}
}

// Acquire 获取锁或租约。
func (s *store) Acquire(ctx context.Context, params AcquireParams) (int64, error) {
	count, err := s.q.AcquireCwdLock(ctx, sqlc.AcquireCwdLockParams{
		CWD:            params.Cwd,
		InstanceID:     params.InstanceID,
		Pid:            int64(params.PID),
		StaleThreshold: platformdb.Millis(time.Now().Add(-staleHeartbeatThreshold)),
	})
	if err != nil {
		return 0, wrapCwdLockError(err, "acquire", "cwd_lock")
	}
	return count, nil
}

// ForceAcquire 处理强制acquire。
func (s *store) ForceAcquire(ctx context.Context, params ForceAcquireParams) (int64, error) {
	count, err := s.q.ForceAcquireCwdLock(ctx, sqlc.ForceAcquireCwdLockParams{
		CWD:        params.Cwd,
		InstanceID: params.InstanceID,
		Pid:        int64(params.PID),
		HolderPid:  int64(params.HolderPID),
	})
	if err != nil {
		return 0, wrapCwdLockError(err, "force_acquire", "cwd_lock")
	}
	return count, nil
}

// Release 释放锁、租约或资源。
func (s *store) Release(ctx context.Context, params ReleaseParams) (int64, error) {
	count, err := s.q.ReleaseCwdLock(ctx, sqlc.ReleaseCwdLockParams{
		CWD:        params.Cwd,
		InstanceID: params.InstanceID,
	})
	if err != nil {
		return 0, wrapCwdLockError(err, "release", "cwd_lock")
	}
	return count, nil
}

// Heartbeat 刷新锁或租约的存活时间。
func (s *store) Heartbeat(ctx context.Context, params HeartbeatParams) error {
	return wrapCwdLockError(s.q.HeartbeatCwdLock(ctx, sqlc.HeartbeatCwdLockParams{
		CWD:        params.Cwd,
		InstanceID: params.InstanceID,
		Pid:        int64(params.PID),
	}), "heartbeat", "cwd_lock")
}

// DeleteStale 删除stale。
func (s *store) DeleteStale(ctx context.Context) (int64, error) {
	staleThreshold := platformdb.Millis(time.Now().Add(-staleHeartbeatThreshold))
	count, err := s.q.DeleteStaleCwdLocks(ctx, sqlc.DeleteStaleCwdLocksParams{HeartbeatAt: staleThreshold})
	if err != nil {
		return 0, wrapCwdLockError(err, "delete_stale", "cwd_lock")
	}
	return count, nil
}

// GetHolder 读取holder。
func (s *store) GetHolder(ctx context.Context, cwd string) (*LockHolder, error) {
	row, err := s.q.GetCwdLockHolder(ctx, sqlc.GetCwdLockHolderParams{CWD: cwd})
	if err != nil {
		return nil, wrapCwdLockError(err, "get_holder", "cwd_lock")
	}
	result := LockHolder{
		InstanceID:  row.InstanceID,
		PID:         int32(row.Pid),
		HeartbeatAt: platformdb.TimeFromMillis(row.HeartbeatAt),
	}
	return &result, nil
}

func wrapCwdLockError(err error, operation, entity string) error {
	return platformdb.WrapStoreError(err, operation, entity)
}
