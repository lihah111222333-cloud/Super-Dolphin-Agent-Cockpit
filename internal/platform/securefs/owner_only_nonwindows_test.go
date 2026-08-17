//go:build !windows

package securefs

import (
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

func TestSyncDirectoryNonWindows(t *testing.T) {
	dir := t.TempDir()
	if err := SyncDirectory(dir); err != nil {
		t.Fatalf("SyncDirectory(dir) error = %v", err)
	}
}

func TestWrapErrorForPathNonWindowsOnlyRedactsRawPermissionError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "private-state.db")
	raw := &os.PathError{Op: "open", Path: path, Err: syscall.Errno(5)}
	wrapped := WrapErrorForPath(raw, path)
	var permissionErr *WindowsPermissionError
	if errors.As(wrapped, &permissionErr) {
		t.Fatalf("non-Windows WrapErrorForPath() promoted raw errno: %#v", permissionErr)
	}
	if containsSecurefsPathToken(wrapped.Error(), path, filepath.Dir(path)) {
		t.Fatalf("non-Windows wrapped error leaked path: %q", wrapped.Error())
	}
}


// TestRestrictOwnerOnlyNonWindowsEnforcesOwnerOnly 证明非 Windows 只使用 mode bits
// 实现 RestrictOwnerOnly，不依赖 Windows ACL 或运行时 GOOS 分支。
func TestRestrictOwnerOnlyNonWindowsEnforcesOwnerOnly(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.db")
	if err := os.WriteFile(path, []byte("state"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0o666); err != nil {
		t.Fatal(err)
	}
	if err := RestrictOwnerOnly(path, 0o600); err != nil {
		t.Fatalf("RestrictOwnerOnly() error = %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("RestrictOwnerOnly() mode = %04o, want 0600", got)
	}
}

// TestCheckExistingOwnerOnlyNonWindowsRejectsGroupWritable 证明非 Windows 检查
// 对 group/world 可写路径 fail-fast，且不把它误当作 owner-only。
func TestCheckExistingOwnerOnlyNonWindowsRejectsGroupWritable(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.db")
	if err := os.WriteFile(path, []byte("state"), 0o660); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0o660); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := CheckExistingOwnerOnly(path, info); err == nil {
		t.Fatal("CheckExistingOwnerOnly() = nil for group-writable path")
	}
}
