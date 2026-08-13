//go:build !windows

package securefs

import (
	"os"
	"path/filepath"
	"testing"
)

// TestCheckPrivateOwnerOnlyRejectsGroupRead 验证严格私有文件不能向 group 暴露只读权限。
func TestCheckPrivateOwnerOnlyRejectsGroupRead(t *testing.T) {
	path := filepath.Join(t.TempDir(), "lease.json")
	if err := os.WriteFile(path, []byte("{}"), 0o640); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	info, err := os.Lstat(path)
	if err != nil {
		t.Fatalf("lstat fixture: %v", err)
	}
	if err := CheckPrivateOwnerOnly(path, info); err == nil {
		t.Fatal("CheckPrivateOwnerOnly() accepted group-readable file")
	}
}

// TestRestrictPrivateOwnerOnlyAppliesExactMode 验证严格权限收敛后文件可通过校验。
func TestRestrictPrivateOwnerOnlyAppliesExactMode(t *testing.T) {
	path := filepath.Join(t.TempDir(), "lease.json")
	if err := os.WriteFile(path, []byte("{}"), 0o666); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	if err := RestrictPrivateOwnerOnly(path, 0o600); err != nil {
		t.Fatalf("RestrictPrivateOwnerOnly() error = %v", err)
	}
	info, err := os.Lstat(path)
	if err != nil {
		t.Fatalf("lstat restricted fixture: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("restricted mode = %#o, want 0600", got)
	}
}
