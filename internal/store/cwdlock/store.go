package cwdlock

import (
	"context"
	"time"

	platformdb "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/db"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/store/sqlc"
)

// querier 是 cwdlock store 依赖的 sqlc 查询子集，所有写入都通过 SQL 原子条件约束持有者。
type querier interface {
	AcquireCwdLock(ctx context.Context, arg sqlc.AcquireCwdLockParams) (int64, error)
	ForceAcquireCwdLock(ctx context.Context, arg sqlc.ForceAcquireCwdLockParams) (int64, error)
	ReleaseCwdLock(ctx context.Context, arg sqlc.ReleaseCwdLockParams) (int64, error)
	HeartbeatCwdLock(ctx context.Context, arg sqlc.HeartbeatCwdLockParams) error
	DeleteStaleCwdLocks(ctx context.Context, arg sqlc.DeleteStaleCwdLocksParams) (int64, error)
	GetCwdLockHolder(ctx context.Context, arg sqlc.GetCwdLockHolderParams) (sqlc.GetCwdLockHolderRow, error)
}

// staleHeartbeatThreshold 是锁心跳过期阈值，超过该窗口的持有者可被普通 Acquire 接管。
const staleHeartbeatThreshold = 45 * time.Second

// store 实现 cwd 锁持久化，所有时间比较都由 store 生成统一阈值。
type store struct {
	q querier
}

// NewStore 使用生产 sqlc 查询对象创建 cwdlock Store。
func NewStore(q *sqlc.Queries) Store {
	return &store{q: q}
}

// Acquire 获取 cwd 锁；当前锁为空或心跳过期时 SQL 才会写入新持有者。
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

// ForceAcquire 在调用方确认旧 holder PID 后强制接管锁，避免误抢仍活跃实例。
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

// Release 仅在 InstanceID 匹配时释放锁，返回受影响行数供调用方判断是否持有。
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

// Heartbeat 刷新当前持有者心跳，InstanceID 和 PID 不匹配时由 SQL 拒绝更新。
func (s *store) Heartbeat(ctx context.Context, params HeartbeatParams) error {
	return wrapCwdLockError(s.q.HeartbeatCwdLock(ctx, sqlc.HeartbeatCwdLockParams{
		CWD:        params.Cwd,
		InstanceID: params.InstanceID,
		Pid:        int64(params.PID),
	}), "heartbeat", "cwd_lock")
}

// DeleteStale 删除超过心跳阈值的锁记录，供后台清理释放僵尸持有者。
func (s *store) DeleteStale(ctx context.Context) (int64, error) {
	staleThreshold := platformdb.Millis(time.Now().Add(-staleHeartbeatThreshold))
	count, err := s.q.DeleteStaleCwdLocks(ctx, sqlc.DeleteStaleCwdLocksParams{HeartbeatAt: staleThreshold})
	if err != nil {
		return 0, wrapCwdLockError(err, "delete_stale", "cwd_lock")
	}
	return count, nil
}

// GetHolder 读取指定 cwd 的当前 holder 快照，时间戳在 store 层转换成 time.Time。
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

// wrapCwdLockError 统一包装 cwd lock store 错误，保留 operation 和 entity 便于排查。
func wrapCwdLockError(err error, operation, entity string) error {
	return platformdb.WrapStoreError(err, operation, entity)
}
