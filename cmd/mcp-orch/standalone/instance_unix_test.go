//go:build darwin || linux

package standalone

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestInstanceLockPathSafety(t *testing.T) {
	t.Run("aliases_share_owner", testInstanceAlias)
	t.Run("alias_binds_canonical_state_dir", testInstanceCanonicalAlias)
	t.Run("lock_symlink_is_not_authority", testInstanceLockSymlink)
	t.Run("shared_state_directory_rejected", testInstanceSharedDir)
}

func TestInstanceLockDatabasePathSafety(t *testing.T) {
	t.Run("fresh_database_is_bound_to_root", testInstanceDatabaseFreshDatabaseIsBoundToRoot)
	t.Run("database_symlink_is_rejected", testInstanceDatabaseDatabaseSymlinkIsRejected)
	t.Run("database_sidecar_symlink_is_rejected", testInstanceDatabaseDatabaseSidecarSymlinkIsRejected)
	t.Run("database_lock_inode_alias_is_rejected", testInstanceDatabaseDatabaseLockInodeAliasIsRejected)
	t.Run("database_parent_alias_is_rejected", testInstanceDatabaseDatabaseParentAliasIsRejected)
	t.Run("database_replacement_is_rejected", testInstanceDatabaseDatabaseReplacementIsRejected)
	t.Run("state_root_replacement_is_rejected", testInstanceDatabaseStateRootReplacementIsRejected)
	t.Run("state_root_rename_recreate_is_rejected", testInstanceDatabaseStateRootRenameRecreateIsRejected)
	t.Run("state_root_lifetime_guard_detects_rename_recreate", testInstanceDatabaseStateRootLifetimeGuardDetectsRenameRecreate)
	t.Run("state_root_permission_drift_is_rejected", testInstanceDatabaseStateRootPermissionDriftIsRejected)
}

// TestInstanceLockFileReplacementDoesNotCreateSecondOwner 复现锁文件替换形成双 owner。
func TestInstanceLockFileReplacementDoesNotCreateSecondOwner(t *testing.T) {
	dir := privateStateDir(t)
	marker := filepath.Join(dir, instanceLockName)
	if err := os.WriteFile(marker, []byte("original"), 0o600); err != nil {
		t.Fatal(err)
	}
	owner := acquireTestInstance(t, dir)
	if err := os.Remove(marker); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(marker, []byte("replacement"), 0o600); err != nil {
		t.Fatal(err)
	}
	contender, err := AcquireInstance(dir)
	if contender != nil {
		closeTestInstance(t, contender)
	}
	if !errors.Is(err, ErrAlreadyRunning) {
		t.Fatalf("replacing a lock file created a second owner: %v", err)
	}
	closeTestInstance(t, owner)
}

// TestInstanceLockFileDeletionDoesNotCreateSecondOwner 验证删除哨兵不会绕过持有目录句柄的 owner。
func TestInstanceLockFileDeletionDoesNotCreateSecondOwner(t *testing.T) {
	dir := privateStateDir(t)
	marker := filepath.Join(dir, instanceLockName)
	if err := os.WriteFile(marker, []byte("sentinel"), 0o600); err != nil {
		t.Fatal(err)
	}
	owner := acquireTestInstance(t, dir)
	if err := os.Remove(marker); err != nil {
		t.Fatal(err)
	}
	contender, err := AcquireInstance(dir)
	if contender != nil {
		closeTestInstance(t, contender)
	}
	if !errors.Is(err, ErrAlreadyRunning) {
		t.Fatalf("deleting lock file created a second owner: %v", err)
	}
	if _, err := os.Lstat(marker); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("contender recreated deleted lock file: %v", err)
	}
	closeTestInstance(t, owner)
}

func testInstanceAlias(t *testing.T) {
	t.Helper()
	dir := privateStateDir(t)
	alias := filepath.Join(t.TempDir(), "state-alias")
	if err := os.Symlink(dir, alias); err != nil {
		t.Fatal(err)
	}
	owner := acquireTestInstance(t, dir)
	if lock, err := AcquireInstance(alias); lock != nil || !errors.Is(err, ErrAlreadyRunning) {
		t.Fatalf("alias created another owner: lock=%v, err=%v", lock, err)
	}
	closeTestInstance(t, owner)
}

func testInstanceCanonicalAlias(t *testing.T) {
	t.Helper()
	dir := privateStateDir(t)
	alias := filepath.Join(t.TempDir(), "state-alias")
	if err := os.Symlink(dir, alias); err != nil {
		t.Fatal(err)
	}
	owner := acquireTestInstance(t, alias)
	defer closeTestInstance(t, owner)
	want, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got := owner.CanonicalStateDir(); got != want {
		t.Fatalf("CanonicalStateDir() = %q, want physical root %q", got, want)
	}
	if got, err := CanonicalStateDir(alias); err != nil || got != want {
		t.Fatalf("CanonicalStateDir(alias) = %q, %v; want %q", got, err, want)
	}
}

func testInstanceLockSymlink(t *testing.T) {
	t.Helper()
	dir := privateStateDir(t)
	outside := filepath.Join(t.TempDir(), "foreign.lock")
	if err := os.Symlink(outside, filepath.Join(dir, instanceLockName)); err != nil {
		t.Fatal(err)
	}
	owner := acquireTestInstance(t, dir)
	defer closeTestInstance(t, owner)
	if _, err := os.Stat(outside); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("foreign target was touched: %v", err)
	}
}

func testInstanceSharedDir(t *testing.T) {
	t.Helper()
	dir := privateStateDir(t)
	if err := os.Chmod(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if lock, err := AcquireInstance(dir); lock != nil || err == nil {
		t.Fatalf("shared directory accepted: lock=%v, err=%v", lock, err)
	}
}
