package standalone

import (
	"os"
	"path/filepath"
	"testing"
)

func testInstanceDatabaseFreshDatabaseIsBoundToRoot(t *testing.T) {
	dir := privateStateDir(t)
	lock := acquireTestInstance(t, dir)
	path := filepath.Join(lock.CanonicalStateDir(), "orch.sqlite")
	if err := lock.WithDatabasePath(path, func() error { return os.WriteFile(path, []byte("db"), 0o600) }); err != nil {
		t.Fatalf("fresh database guard = %v", err)
	}

}

func testInstanceDatabaseDatabaseSymlinkIsRejected(t *testing.T) {
	dir := privateStateDir(t)
	outside := filepath.Join(t.TempDir(), "outside.sqlite")
	if err := os.WriteFile(outside, []byte("outside"), 0o600); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "orch.sqlite")
	if err := os.Symlink(outside, path); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	lock := acquireTestInstance(t, dir)
	called := false
	if err := lock.WithDatabasePath(path, func() error { called = true; return nil }); err == nil || called {
		t.Fatalf("database symlink accepted: err=%v callback=%v", err, called)
	}

}

func testInstanceDatabaseDatabaseSidecarSymlinkIsRejected(t *testing.T) {
	dir := privateStateDir(t)
	lock := acquireTestInstance(t, dir)
	path := filepath.Join(lock.CanonicalStateDir(), "orch.sqlite")
	if err := os.WriteFile(path, []byte("db"), 0o600); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "outside-wal")
	if err := os.WriteFile(outside, []byte("outside"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, path+"-wal"); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	if err := lock.WithDatabasePath(path, func() error { return nil }); err == nil {
		t.Fatal("database sidecar symlink accepted")
	}

}

func testInstanceDatabaseDatabaseLockInodeAliasIsRejected(t *testing.T) {
	dir := privateStateDir(t)
	lockSentinel := filepath.Join(dir, instanceLockName)
	if err := os.WriteFile(lockSentinel, []byte("lock"), 0o600); err != nil {
		t.Fatal(err)
	}
	lock := acquireTestInstance(t, dir)
	path := filepath.Join(lock.CanonicalStateDir(), "orch.sqlite")
	if err := os.Link(lockSentinel, path); err != nil {
		t.Skipf("hard links unavailable: %v", err)
	}
	if err := lock.WithDatabasePath(path, func() error { return nil }); err == nil {
		t.Fatal("database hardlink to instance lock accepted")
	}

}

func testInstanceDatabaseDatabaseParentAliasIsRejected(t *testing.T) {
	dir := privateStateDir(t)
	alias := filepath.Join(t.TempDir(), "state-alias")
	if err := os.Symlink(dir, alias); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	lock := acquireTestInstance(t, dir)
	path := filepath.Join(alias, "orch.sqlite")
	if err := lock.WithDatabasePath(path, func() error { return nil }); err == nil {
		t.Fatal("database parent alias accepted")
	}

}

func testInstanceDatabaseDatabaseReplacementIsRejected(t *testing.T) {
	dir := privateStateDir(t)
	lock := acquireTestInstance(t, dir)
	path := filepath.Join(lock.CanonicalStateDir(), "orch.sqlite")
	if err := os.WriteFile(path, []byte("original"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := lock.WithDatabasePath(path, func() error {
		if err := os.Remove(path); err != nil {
			return err
		}
		return os.WriteFile(path, []byte("replacement"), 0o600)
	}); err == nil {
		t.Fatal("database replacement accepted")
	}

}

func testInstanceDatabaseStateRootReplacementIsRejected(t *testing.T) {
	dir := privateStateDir(t)
	moved := filepath.Join(t.TempDir(), "moved-state")
	lock := acquireTestInstance(t, dir)
	path := filepath.Join(lock.CanonicalStateDir(), "orch.sqlite")
	if err := lock.WithDatabasePath(path, func() error { return os.Rename(dir, moved) }); err == nil {
		t.Fatal("state root replacement accepted")
	}

}

func testInstanceDatabaseStateRootRenameRecreateIsRejected(t *testing.T) {
	dir := privateStateDir(t)
	moved := filepath.Join(t.TempDir(), "moved-state")
	lock := acquireTestInstance(t, dir)
	path := filepath.Join(lock.CanonicalStateDir(), "orch.sqlite")
	if err := lock.WithDatabasePath(path, func() error {
		if err := os.Rename(dir, moved); err != nil {
			return err
		}
		return os.Mkdir(dir, 0o700)
	}); err == nil {
		t.Fatal("state root rename/recreate accepted")
	}

}

func testInstanceDatabaseStateRootLifetimeGuardDetectsRenameRecreate(t *testing.T) {
	dir := privateStateDir(t)
	moved := filepath.Join(t.TempDir(), "moved-state")
	lock := acquireTestInstance(t, dir)
	if err := os.Rename(dir, moved); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := lock.ValidateStateDir(); err == nil {
		t.Fatal("lifetime state-root guard accepted replacement")
	}

}

func testInstanceDatabaseStateRootPermissionDriftIsRejected(t *testing.T) {
	dir := privateStateDir(t)
	lock := acquireTestInstance(t, dir)
	path := filepath.Join(lock.CanonicalStateDir(), "orch.sqlite")
	if err := lock.WithDatabasePath(path, func() error { return os.Chmod(dir, 0o755) }); err == nil {
		t.Fatal("state root permission drift accepted")
	}

}
