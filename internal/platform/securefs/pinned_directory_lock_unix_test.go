//go:build darwin || linux

package securefs

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

const testPinnedSentinel = ".owner.lock"

// TestPinnedDirectoryLockSecondOwnerBusy 验证同一目录不能取得第二个 owner。
func TestPinnedDirectoryLockSecondOwnerBusy(t *testing.T) {
	root := newPinnedLockTestRoot(t)
	first := acquirePinnedLockForTest(t, root)
	defer closePinnedLockForTest(t, first)

	second, err := AcquirePinnedDirectoryLock(root, testPinnedSentinel)
	if second != nil {
		closePinnedLockForTest(t, second)
	}
	if !errors.Is(err, ErrLockBusy) {
		t.Fatalf("second AcquirePinnedDirectoryLock() error = %v, want ErrLockBusy", err)
	}
}

// TestPinnedDirectoryLockAliasSameInode 验证 symlink alias canonicalize 后仍竞争同一 inode。
func TestPinnedDirectoryLockAliasSameInode(t *testing.T) {
	root := newPinnedLockTestRoot(t)
	alias := filepath.Join(t.TempDir(), "root-alias")
	if err := os.Symlink(root, alias); err != nil {
		t.Fatalf("Symlink() error = %v", err)
	}
	first := acquirePinnedLockForTest(t, root)
	defer closePinnedLockForTest(t, first)

	second, err := AcquirePinnedDirectoryLock(alias, testPinnedSentinel)
	if second != nil {
		closePinnedLockForTest(t, second)
	}
	if !errors.Is(err, ErrLockBusy) {
		t.Fatalf("alias AcquirePinnedDirectoryLock() error = %v, want ErrLockBusy", err)
	}
}

// TestPinnedDirectoryLockRenameRecreate 验证原路径改指新目录后不再冒充持锁身份。
func TestPinnedDirectoryLockRenameRecreate(t *testing.T) {
	parent := t.TempDir()
	root := filepath.Join(parent, "root")
	if err := os.Mkdir(root, 0o700); err != nil {
		t.Fatalf("Mkdir() error = %v", err)
	}
	lock := acquirePinnedLockForTest(t, root)
	defer closePinnedLockForTest(t, lock)

	moved := filepath.Join(parent, "moved")
	if err := os.Rename(root, moved); err != nil {
		t.Fatalf("Rename() error = %v", err)
	}
	if err := os.Mkdir(root, 0o700); err != nil {
		t.Fatalf("recreate root error = %v", err)
	}
	if err := lock.Validate(); !errors.Is(err, ErrLockIdentityChanged) {
		t.Fatalf("Validate() error = %v, want ErrLockIdentityChanged", err)
	}
}

// TestPinnedDirectoryLockPermissionDrift 验证目录权限放宽后立即使身份校验失效。
func TestPinnedDirectoryLockPermissionDrift(t *testing.T) {
	root := newPinnedLockTestRoot(t)
	lock := acquirePinnedLockForTest(t, root)
	defer closePinnedLockForTest(t, lock)
	if err := os.Chmod(root, 0o750); err != nil {
		t.Fatalf("Chmod() error = %v", err)
	}
	if err := lock.Validate(); !errors.Is(err, ErrLockIdentityChanged) {
		t.Fatalf("Validate() error = %v, want ErrLockIdentityChanged", err)
	}
}

// TestPinnedDirectoryLockValidateAfterClose 验证关闭幂等且关闭后校验返回稳定错误。
func TestPinnedDirectoryLockValidateAfterClose(t *testing.T) {
	root := newPinnedLockTestRoot(t)
	lock := acquirePinnedLockForTest(t, root)
	closePinnedLockForTest(t, lock)
	if err := lock.Close(); err != nil {
		t.Fatalf("second Close() error = %v", err)
	}
	if err := lock.Validate(); !errors.Is(err, ErrLockClosed) {
		t.Fatalf("Validate() error = %v, want ErrLockClosed", err)
	}
	if _, err := os.Stat(filepath.Join(root, testPinnedSentinel)); err != nil {
		t.Fatalf("sentinel was removed by Close(): %v", err)
	}
	second := acquirePinnedLockForTest(t, root)
	closePinnedLockForTest(t, second)
}

// TestPinnedDirectoryLockSentinelReplacement 验证替换 marker 不会绕开目录 inode 锁。
func TestPinnedDirectoryLockSentinelReplacement(t *testing.T) {
	root := newPinnedLockTestRoot(t)
	lock := acquirePinnedLockForTest(t, root)
	defer closePinnedLockForTest(t, lock)
	sentinelPath := filepath.Join(root, testPinnedSentinel)
	if err := os.Remove(sentinelPath); err != nil {
		t.Fatalf("Remove() error = %v", err)
	}
	if err := os.WriteFile(sentinelPath, nil, 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	second, err := AcquirePinnedDirectoryLock(root, testPinnedSentinel)
	if second != nil {
		closePinnedLockForTest(t, second)
	}
	if !errors.Is(err, ErrLockBusy) {
		t.Fatalf("replacement AcquirePinnedDirectoryLock() error = %v, want ErrLockBusy", err)
	}
	if err := lock.Validate(); !errors.Is(err, ErrLockIdentityChanged) {
		t.Fatalf("Validate() error = %v, want ErrLockIdentityChanged", err)
	}
}

// newPinnedLockTestRoot 创建严格 owner-only 的测试目录。
func newPinnedLockTestRoot(t *testing.T) string {
	t.Helper()
	root := filepath.Join(t.TempDir(), "private")
	if err := os.Mkdir(root, 0o700); err != nil {
		t.Fatalf("Mkdir() error = %v", err)
	}
	return root
}

// acquirePinnedLockForTest 取得测试锁并要求初始完整校验通过。
func acquirePinnedLockForTest(t *testing.T, root string) *PinnedDirectoryLock {
	t.Helper()
	lock, err := AcquirePinnedDirectoryLock(root, testPinnedSentinel)
	if err != nil {
		t.Fatalf("AcquirePinnedDirectoryLock() error = %v", err)
	}
	if err := lock.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
	return lock
}

// closePinnedLockForTest 关闭测试锁并把资源释放失败计入测试结果。
func closePinnedLockForTest(t *testing.T, lock *PinnedDirectoryLock) {
	t.Helper()
	if err := lock.Close(); err != nil {
		t.Errorf("Close() error = %v", err)
	}
}
