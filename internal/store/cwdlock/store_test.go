package cwdlock

import (
	"context"
	"errors"
	"testing"
	"time"

	platformdb "github.com/anthropic-ai/super-agent-v3/internal/platform/db"
	"github.com/anthropic-ai/super-agent-v3/internal/store/sqlc"
	"github.com/jackc/pgx/v5"
)

type cwdLockQuerierStub struct {
	acquireFn      func(context.Context, sqlc.AcquireCwdLockParams) (int64, error)
	forceAcquireFn func(context.Context, sqlc.ForceAcquireCwdLockParams) (int64, error)
	releaseFn      func(context.Context, sqlc.ReleaseCwdLockParams) (int64, error)
	heartbeatFn    func(context.Context, sqlc.HeartbeatCwdLockParams) error
	deleteStaleFn  func(context.Context) (int64, error)
	getHolderFn    func(context.Context, string) (sqlc.GetCwdLockHolderRow, error)
}

func (s *cwdLockQuerierStub) AcquireCwdLock(ctx context.Context, arg sqlc.AcquireCwdLockParams) (int64, error) {
	if s.acquireFn != nil {
		return s.acquireFn(ctx, arg)
	}
	return 0, nil
}

func (s *cwdLockQuerierStub) ForceAcquireCwdLock(ctx context.Context, arg sqlc.ForceAcquireCwdLockParams) (int64, error) {
	if s.forceAcquireFn != nil {
		return s.forceAcquireFn(ctx, arg)
	}
	return 0, nil
}

func (s *cwdLockQuerierStub) ReleaseCwdLock(ctx context.Context, arg sqlc.ReleaseCwdLockParams) (int64, error) {
	if s.releaseFn != nil {
		return s.releaseFn(ctx, arg)
	}
	return 0, nil
}

func (s *cwdLockQuerierStub) HeartbeatCwdLock(ctx context.Context, arg sqlc.HeartbeatCwdLockParams) error {
	if s.heartbeatFn != nil {
		return s.heartbeatFn(ctx, arg)
	}
	return nil
}

func (s *cwdLockQuerierStub) DeleteStaleCwdLocks(ctx context.Context, arg sqlc.DeleteStaleCwdLocksParams) (int64, error) {
	if s.deleteStaleFn != nil {
		return s.deleteStaleFn(ctx)
	}
	return 0, nil
}

func (s *cwdLockQuerierStub) GetCwdLockHolder(ctx context.Context, arg sqlc.GetCwdLockHolderParams) (sqlc.GetCwdLockHolderRow, error) {
	if s.getHolderFn != nil {
		return s.getHolderFn(ctx, arg.CWD)
	}
	return sqlc.GetCwdLockHolderRow{}, nil
}

func TestAcquireForwardsParamsAndReturnsCount(t *testing.T) {
	t.Parallel()

	var captured sqlc.AcquireCwdLockParams
	s := &store{q: &cwdLockQuerierStub{
		acquireFn: func(_ context.Context, arg sqlc.AcquireCwdLockParams) (int64, error) {
			captured = arg
			return 1, nil
		},
	}}

	count, err := s.Acquire(context.Background(), AcquireParams{Cwd: "/tmp/work", InstanceID: "inst-1", PID: 1234})
	if err != nil {
		t.Fatalf("Acquire() error = %v", err)
	}
	if count != 1 {
		t.Fatalf("Acquire() count = %d, want 1", count)
	}
	if captured.CWD != "/tmp/work" || captured.InstanceID != "inst-1" || captured.Pid != 1234 {
		t.Fatalf("Acquire() forwarded wrong params: %+v", captured)
	}
}

func TestAcquireWrapsQuerierError(t *testing.T) {
	t.Parallel()

	sentinel := errors.New("boom")
	s := &store{q: &cwdLockQuerierStub{
		acquireFn: func(context.Context, sqlc.AcquireCwdLockParams) (int64, error) {
			return 0, sentinel
		},
	}}

	count, err := s.Acquire(context.Background(), AcquireParams{})
	if err == nil {
		t.Fatal("Acquire() expected error, got nil")
	}
	if count != 0 {
		t.Fatalf("Acquire() count = %d on error, want 0", count)
	}
	if !errors.Is(err, sentinel) {
		t.Fatalf("Acquire() error = %v, want wrap of sentinel", err)
	}
	var storeErr *platformdb.StoreError
	if !errors.As(err, &storeErr) {
		t.Fatalf("Acquire() error = %v, want *platformdb.StoreError", err)
	}
	if storeErr.Operation != "acquire" || storeErr.Entity != "cwd_lock" {
		t.Fatalf("Acquire() error metadata = %+v", storeErr)
	}
}

func TestForceAcquireForwardsParamsAndReturnsCount(t *testing.T) {
	t.Parallel()

	var captured sqlc.ForceAcquireCwdLockParams
	s := &store{q: &cwdLockQuerierStub{
		forceAcquireFn: func(_ context.Context, arg sqlc.ForceAcquireCwdLockParams) (int64, error) {
			captured = arg
			return 2, nil
		},
	}}

	count, err := s.ForceAcquire(context.Background(), ForceAcquireParams{Cwd: "/var", InstanceID: "inst-2", PID: 42, HolderPID: 99})
	if err != nil {
		t.Fatalf("ForceAcquire() error = %v", err)
	}
	if count != 2 {
		t.Fatalf("ForceAcquire() count = %d, want 2", count)
	}
	if captured.CWD != "/var" || captured.InstanceID != "inst-2" || captured.Pid != 42 {
		t.Fatalf("ForceAcquire() forwarded wrong params: %+v", captured)
	}
}

func TestForceAcquireWrapsQuerierError(t *testing.T) {
	t.Parallel()

	sentinel := errors.New("kaboom")
	s := &store{q: &cwdLockQuerierStub{
		forceAcquireFn: func(context.Context, sqlc.ForceAcquireCwdLockParams) (int64, error) {
			return 0, sentinel
		},
	}}

	_, err := s.ForceAcquire(context.Background(), ForceAcquireParams{})
	if !errors.Is(err, sentinel) {
		t.Fatalf("ForceAcquire() error = %v, want wrap of sentinel", err)
	}
	var storeErr *platformdb.StoreError
	if !errors.As(err, &storeErr) || storeErr.Operation != "force_acquire" {
		t.Fatalf("ForceAcquire() error = %v, want operation=force_acquire", err)
	}
}

func TestReleaseForwardsParamsAndReturnsCount(t *testing.T) {
	t.Parallel()

	var captured sqlc.ReleaseCwdLockParams
	s := &store{q: &cwdLockQuerierStub{
		releaseFn: func(_ context.Context, arg sqlc.ReleaseCwdLockParams) (int64, error) {
			captured = arg
			return 3, nil
		},
	}}

	count, err := s.Release(context.Background(), ReleaseParams{Cwd: "/repo", InstanceID: "inst-3"})
	if err != nil {
		t.Fatalf("Release() error = %v", err)
	}
	if count != 3 {
		t.Fatalf("Release() count = %d, want 3", count)
	}
	if captured.CWD != "/repo" || captured.InstanceID != "inst-3" {
		t.Fatalf("Release() forwarded wrong params: %+v", captured)
	}
}

func TestReleaseWrapsQuerierError(t *testing.T) {
	t.Parallel()

	sentinel := errors.New("nope")
	s := &store{q: &cwdLockQuerierStub{
		releaseFn: func(context.Context, sqlc.ReleaseCwdLockParams) (int64, error) {
			return 0, sentinel
		},
	}}

	_, err := s.Release(context.Background(), ReleaseParams{})
	if !errors.Is(err, sentinel) {
		t.Fatalf("Release() error = %v, want wrap of sentinel", err)
	}
}

func TestHeartbeatForwardsParamsAndReturnsNilOnSuccess(t *testing.T) {
	t.Parallel()

	var captured sqlc.HeartbeatCwdLockParams
	s := &store{q: &cwdLockQuerierStub{
		heartbeatFn: func(_ context.Context, arg sqlc.HeartbeatCwdLockParams) error {
			captured = arg
			return nil
		},
	}}

	if err := s.Heartbeat(context.Background(), HeartbeatParams{Cwd: "/h", InstanceID: "inst-4", PID: 7}); err != nil {
		t.Fatalf("Heartbeat() error = %v", err)
	}
	if captured.CWD != "/h" || captured.InstanceID != "inst-4" || captured.Pid != 7 {
		t.Fatalf("Heartbeat() forwarded wrong params: %+v", captured)
	}
}

func TestHeartbeatWrapsQuerierError(t *testing.T) {
	t.Parallel()

	sentinel := errors.New("timeout")
	s := &store{q: &cwdLockQuerierStub{
		heartbeatFn: func(context.Context, sqlc.HeartbeatCwdLockParams) error {
			return sentinel
		},
	}}

	err := s.Heartbeat(context.Background(), HeartbeatParams{})
	if !errors.Is(err, sentinel) {
		t.Fatalf("Heartbeat() error = %v, want wrap of sentinel", err)
	}
	var storeErr *platformdb.StoreError
	if !errors.As(err, &storeErr) || storeErr.Operation != "heartbeat" || storeErr.Entity != "cwd_lock" {
		t.Fatalf("Heartbeat() error metadata = %+v", err)
	}
}

func TestDeleteStaleReturnsCount(t *testing.T) {
	t.Parallel()

	s := &store{q: &cwdLockQuerierStub{
		deleteStaleFn: func(context.Context) (int64, error) { return 5, nil },
	}}
	count, err := s.DeleteStale(context.Background())
	if err != nil {
		t.Fatalf("DeleteStale() error = %v", err)
	}
	if count != 5 {
		t.Fatalf("DeleteStale() count = %d, want 5", count)
	}
}

func TestDeleteStaleWrapsQuerierError(t *testing.T) {
	t.Parallel()

	sentinel := errors.New("db gone")
	s := &store{q: &cwdLockQuerierStub{
		deleteStaleFn: func(context.Context) (int64, error) { return 0, sentinel },
	}}
	_, err := s.DeleteStale(context.Background())
	if !errors.Is(err, sentinel) {
		t.Fatalf("DeleteStale() error = %v, want wrap of sentinel", err)
	}
}

func TestGetHolderMapsRow(t *testing.T) {
	t.Parallel()

	heartbeat := time.Unix(1700000000, 0).UTC()
	var capturedCwd string
	s := &store{q: &cwdLockQuerierStub{
		getHolderFn: func(_ context.Context, cwd string) (sqlc.GetCwdLockHolderRow, error) {
			capturedCwd = cwd
			return sqlc.GetCwdLockHolderRow{InstanceID: "inst-x", Pid: 321, HeartbeatAt: heartbeat.UnixMilli()}, nil
		},
	}}

	holder, err := s.GetHolder(context.Background(), "/mine")
	if err != nil {
		t.Fatalf("GetHolder() error = %v", err)
	}
	if capturedCwd != "/mine" {
		t.Fatalf("GetHolder() forwarded cwd = %q, want /mine", capturedCwd)
	}
	if holder == nil {
		t.Fatal("GetHolder() returned nil, want non-nil *LockHolder")
	}
	if holder.InstanceID != "inst-x" || holder.PID != 321 || !holder.HeartbeatAt.Equal(heartbeat) {
		t.Fatalf("GetHolder() = %+v", holder)
	}
}

func TestGetHolderWrapsPgxErrNoRowsAsNotFound(t *testing.T) {
	t.Parallel()

	s := &store{q: &cwdLockQuerierStub{
		getHolderFn: func(context.Context, string) (sqlc.GetCwdLockHolderRow, error) {
			return sqlc.GetCwdLockHolderRow{}, pgx.ErrNoRows
		},
	}}

	holder, err := s.GetHolder(context.Background(), "/missing")
	if err == nil {
		t.Fatal("GetHolder() expected error, got nil")
	}
	if holder != nil {
		t.Fatalf("GetHolder() holder = %+v, want nil on error", holder)
	}
	if !errors.Is(err, platformdb.ErrNotFound) {
		t.Fatalf("GetHolder() error = %v, want wrap of ErrNotFound", err)
	}
	var storeErr *platformdb.StoreError
	if !errors.As(err, &storeErr) || storeErr.Operation != "get_holder" {
		t.Fatalf("GetHolder() error metadata = %+v", err)
	}
}
