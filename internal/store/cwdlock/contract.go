// Package cwdlock 持久化工作目录锁，防止多个本地实例同时占用同一 cwd。
package cwdlock

import (
	"context"
	"time"
)

// Store 定义 cwd 锁的获取、强制接管、心跳和清理接口。
type Store interface {
	Acquire(ctx context.Context, params AcquireParams) (int64, error)
	ForceAcquire(ctx context.Context, params ForceAcquireParams) (int64, error)
	Release(ctx context.Context, params ReleaseParams) (int64, error)
	Heartbeat(ctx context.Context, params HeartbeatParams) error
	DeleteStale(ctx context.Context) (int64, error)
	GetHolder(ctx context.Context, cwd string) (*LockHolder, error)
}

// AcquireParams 是普通获取锁的输入，PID 和 InstanceID 共同标识持有者。
type AcquireParams struct {
	Cwd        string
	InstanceID string
	PID        int32
}

// ForceAcquireParams 是显式接管锁的输入，HolderPID 用于约束被接管的旧持有者。
type ForceAcquireParams struct {
	Cwd        string
	InstanceID string
	PID        int32
	HolderPID  int32
}

// ReleaseParams 是释放锁的输入，InstanceID 必须与当前持有者匹配。
type ReleaseParams struct {
	Cwd        string
	InstanceID string
}

// HeartbeatParams 是刷新锁存活时间的输入，PID 用于持有者身份校验。
type HeartbeatParams struct {
	Cwd        string
	InstanceID string
	PID        int32
}

// LockHolder 表示当前 cwd 锁持有者快照，HeartbeatAt 用于判断租约是否陈旧。
type LockHolder struct {
	InstanceID  string
	PID         int32
	HeartbeatAt time.Time
}
