package cwdlock

import (
	"context"
	"time"
)

type Store interface {
	Acquire(ctx context.Context, params AcquireParams) (int64, error)
	ForceAcquire(ctx context.Context, params ForceAcquireParams) (int64, error)
	Release(ctx context.Context, params ReleaseParams) (int64, error)
	Heartbeat(ctx context.Context, params HeartbeatParams) error
	DeleteStale(ctx context.Context) (int64, error)
	GetHolder(ctx context.Context, cwd string) (*LockHolder, error)
}

type AcquireParams struct {
	Cwd        string
	InstanceID string
	PID        int32
}

type ForceAcquireParams struct {
	Cwd        string
	InstanceID string
	PID        int32
	HolderPID  int32
}

type ReleaseParams struct {
	Cwd        string
	InstanceID string
}

type HeartbeatParams struct {
	Cwd        string
	InstanceID string
	PID        int32
}

type LockHolder struct {
	InstanceID  string
	PID         int32
	HeartbeatAt time.Time
}
