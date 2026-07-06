package memory

import (
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestConsolidationLockAcquireRelease(t *testing.T) {
	root := newTestMemoryRoot(t)
	now := time.Date(2026, 4, 15, 9, 30, 0, 0, time.UTC)
	guard, err := acquireConsolidationLock(root, consolidationLockOptions{
		Now: func() time.Time { return now },
		PID: 4242,
	})
	if err != nil {
		t.Fatalf("acquireConsolidationLock() error = %v", err)
	}
	path, err := consolidationLockPath(root)
	if err != nil {
		t.Fatalf("consolidationLockPath() error = %v", err)
	}
	record, modTime, err := loadConsolidationLockRecord(path)
	if err != nil {
		t.Fatalf("loadConsolidationLockRecord() error = %v", err)
	}
	if record.PID != 4242 {
		t.Fatalf("lock PID = %d, want 4242", record.PID)
	}
	if !modTime.Equal(now) {
		t.Fatalf("lock mtime = %s, want %s", modTime, now)
	}
	if _, err := acquireConsolidationLock(root, consolidationLockOptions{Now: func() time.Time { return now.Add(time.Minute) }}); !errors.Is(err, ErrConsolidationLocked) {
		t.Fatalf("second acquire error = %v, want %v", err, ErrConsolidationLocked)
	}
	if err := guard.Release(); err != nil {
		t.Fatalf("Release() error = %v", err)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("Stat(lock) error = %v, want not-exist", err)
	}
}

func TestConsolidationLockRollbackRestoresMtime(t *testing.T) {
	root := newTestMemoryRoot(t)
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatalf("MkdirAll(root) error = %v", err)
	}
	path, err := consolidationLockPath(root)
	if err != nil {
		t.Fatalf("consolidationLockPath() error = %v", err)
	}
	previous := time.Date(2026, 4, 14, 9, 0, 0, 0, time.UTC)
	if err := os.WriteFile(path, []byte(`{"pid":1}`), 0o644); err != nil {
		t.Fatalf("WriteFile(lock) error = %v", err)
	}
	if err := os.Chtimes(path, previous, previous); err != nil {
		t.Fatalf("Chtimes(lock) error = %v", err)
	}
	guard, err := acquireConsolidationLock(root, consolidationLockOptions{
		Now: func() time.Time { return previous.Add(2 * defaultConsolidationLockTTL) },
		PID: 5252,
	})
	if err != nil {
		t.Fatalf("acquireConsolidationLock(stale) error = %v", err)
	}
	if err := guard.RollbackMtime(); err != nil {
		t.Fatalf("RollbackMtime() error = %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat(lock after rollback) error = %v", err)
	}
	if !info.ModTime().Equal(previous) {
		t.Fatalf("lock mtime after rollback = %s, want %s", info.ModTime(), previous)
	}
	if err := guard.Release(); err != nil {
		t.Fatalf("Release() error = %v", err)
	}
}

func TestConsolidationLockRollbackRemovesNewLock(t *testing.T) {
	root := newTestMemoryRoot(t)
	guard, err := acquireConsolidationLock(root, consolidationLockOptions{})
	if err != nil {
		t.Fatalf("acquireConsolidationLock() error = %v", err)
	}
	path, err := consolidationLockPath(root)
	if err != nil {
		t.Fatalf("consolidationLockPath() error = %v", err)
	}
	if err := guard.RollbackMtime(); err != nil {
		t.Fatalf("RollbackMtime() error = %v", err)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("Stat(lock after rollback) error = %v, want not-exist", err)
	}
}

func TestConsolidationLockIndependentFromDiskStoreLock(t *testing.T) {
	root := newTestMemoryRoot(t)
	store, err := newDiskStore(root, nil)
	if err != nil {
		t.Fatalf("newDiskStore() error = %v", err)
	}
	started := make(chan struct{})
	release := make(chan struct{})
	errCh := make(chan error, 1)
	var wg sync.WaitGroup
	wg.Go(func() {
		errCh <- store.locks.withDiskStoreLock(root, func() error {
			close(started)
			<-release
			return nil
		})
	})
	<-started
	guard, err := acquireConsolidationLock(root, consolidationLockOptions{})
	if err != nil {
		t.Fatalf("acquireConsolidationLock() while disk lock held error = %v", err)
	}
	close(release)
	if err := <-errCh; err != nil {
		t.Fatalf("withDiskStoreLock() error = %v", err)
	}
	wg.Wait()
	if err := guard.Release(); err != nil {
		t.Fatalf("Release() error = %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, diskStoreLockFileName)); err != nil && !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("Stat(disk lock file) error = %v", err)
	}
}
