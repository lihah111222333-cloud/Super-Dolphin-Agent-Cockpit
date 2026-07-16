//go:build unix

package localci

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestSchedulerDaemonLockIsExclusive(t *testing.T) {
	t.Parallel()

	path := filepath.Join(canonicalPrivateTempDir(t), "scheduler.lock")
	identity := mustDaemonIdentity(t, "daemon-lock")
	first, err := acquireSchedulerLock(path, identity)
	if err != nil {
		t.Fatalf("acquire first lock: %v", err)
	}
	t.Cleanup(func() {
		if err := first.close(); err != nil {
			t.Errorf("close first lock: %v", err)
		}
	})
	if _, err := acquireSchedulerLock(path, identity); !errors.Is(err, errSchedulerOwned) {
		t.Fatalf("second lock error=%v want=%v", err, errSchedulerOwned)
	}
}

func TestSchedulerDaemonLockRejectsIdentityMismatch(t *testing.T) {
	t.Parallel()

	path := filepath.Join(canonicalPrivateTempDir(t), "scheduler.lock")
	first, err := acquireSchedulerLock(path, mustDaemonIdentity(t, "daemon-first"))
	if err != nil {
		t.Fatalf("acquire first lock: %v", err)
	}
	if err := first.close(); err != nil {
		t.Fatalf("close first lock: %v", err)
	}
	if _, err := acquireSchedulerLock(path, mustDaemonIdentity(t, "daemon-second")); err == nil {
		t.Fatal("expected lock identity mismatch to fail")
	}
}

func TestSchedulerDaemonLockRejectsRelativePath(t *testing.T) {
	t.Parallel()

	if _, err := validateCurrentUIDPrivatePath("scheduler.lock", os.Geteuid()); err == nil {
		t.Fatal("expected relative lock path to fail")
	}
}

func TestSchedulerDaemonLockRejectsSymlink(t *testing.T) {
	t.Parallel()

	privateDir := canonicalPrivateTempDir(t)
	target := filepath.Join(privateDir, "lock-target")
	if err := os.WriteFile(target, nil, 0o600); err != nil {
		t.Fatalf("write lock target: %v", err)
	}
	link := filepath.Join(privateDir, "lock-link")
	if err := os.Symlink(target, link); err != nil {
		t.Fatalf("create lock symlink: %v", err)
	}
	expectSchedulerLockRejected(t, link, mustDaemonIdentity(t, "daemon-symlink-lock"))
}

func TestSchedulerDaemonLockRejectsSharedParent(t *testing.T) {
	t.Parallel()

	parent := filepath.Join(canonicalPrivateTempDir(t), "shared")
	if err := os.Mkdir(parent, 0o755); err != nil {
		t.Fatalf("create shared parent: %v", err)
	}
	if err := os.Chmod(parent, 0o755); err != nil {
		t.Fatalf("set shared parent mode: %v", err)
	}
	expectSchedulerLockRejected(t, filepath.Join(parent, "scheduler.lock"), mustDaemonIdentity(t, "daemon-shared-parent"))
}

func TestSchedulerDaemonLockRejectsSharedFile(t *testing.T) {
	t.Parallel()

	path := filepath.Join(canonicalPrivateTempDir(t), "shared.lock")
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatalf("write shared lock: %v", err)
	}
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatalf("set shared lock mode: %v", err)
	}
	expectSchedulerLockRejected(t, path, mustDaemonIdentity(t, "daemon-shared-file"))
}

func TestSchedulerDaemonLockRejectsNonRegularFile(t *testing.T) {
	t.Parallel()

	path := filepath.Join(canonicalPrivateTempDir(t), "lock-directory")
	if err := os.Mkdir(path, 0o700); err != nil {
		t.Fatalf("create lock directory: %v", err)
	}
	expectSchedulerLockRejected(t, path, mustDaemonIdentity(t, "daemon-non-regular"))
}

func TestSchedulerDaemonLockRejectsPeerOwner(t *testing.T) {
	t.Parallel()

	peer := mustDaemonIdentity(t, "daemon-peer-owner")
	peer.ownerUID = os.Geteuid() + 1
	expectSchedulerLockRejected(t, filepath.Join(canonicalPrivateTempDir(t), "peer.lock"), peer)
}

func expectSchedulerLockRejected(t *testing.T, path string, identity daemonIdentity) {
	t.Helper()

	if _, err := acquireSchedulerLock(path, identity); err == nil {
		t.Fatal("expected scheduler lock acquisition to fail")
	}
}
