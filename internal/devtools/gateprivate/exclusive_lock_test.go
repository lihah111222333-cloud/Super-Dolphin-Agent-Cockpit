package gateprivate

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestExclusiveFileLockSerializesProcessesAndHonorsCancellation(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(root, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "calibration.lock")
	first, err := AcquireExclusiveFileLock(context.Background(), path)
	if err != nil {
		t.Fatalf("AcquireExclusiveFileLock(first) error = %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if _, err := AcquireExclusiveFileLock(ctx, path); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("AcquireExclusiveFileLock(contended) error = %v", err)
	}
	if err := first.Release(); err != nil {
		t.Fatalf("Release(first) error = %v", err)
	}
	second, err := AcquireExclusiveFileLock(context.Background(), path)
	if err != nil {
		t.Fatalf("AcquireExclusiveFileLock(second) error = %v", err)
	}
	if err := second.Release(); err != nil {
		t.Fatalf("Release(second) error = %v", err)
	}
}

func TestTryAcquireExclusiveFileLockDoesNotWaitForOwner(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(root, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "try.lock")
	owner, acquired, err := TryAcquireExclusiveFileLock(path)
	if err != nil || !acquired {
		t.Fatalf("TryAcquireExclusiveFileLock(winner) acquired=%t error=%v", acquired, err)
	}
	defer func() { _ = owner.Release() }()

	result := make(chan struct {
		lock     *ExclusiveFileLock
		acquired bool
		err      error
	}, 1)
	go func() {
		lock, acquired, err := TryAcquireExclusiveFileLock(path)
		result <- struct {
			lock     *ExclusiveFileLock
			acquired bool
			err      error
		}{lock, acquired, err}
	}()
	select {
	case contender := <-result:
		if contender.err != nil || contender.acquired || contender.lock != nil {
			t.Fatalf("TryAcquireExclusiveFileLock(contender) lock=%v acquired=%t error=%v", contender.lock, contender.acquired, contender.err)
		}
	case <-time.After(time.Second):
		t.Fatal("TryAcquireExclusiveFileLock waited for an owned lock")
	}
}
