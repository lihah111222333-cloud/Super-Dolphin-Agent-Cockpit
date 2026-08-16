//go:build windows && e2e

package lspplatform

import (
	"os"
	"path/filepath"
	"testing"
)

// TestWindowsGoplsDirectoryIdentityDetectsPathReplacementE2E 验证 Windows
// 目录身份绑定内核 File ID，而不是可被重建复用的路径字符串。
func TestWindowsGoplsDirectoryIdentityDetectsPathReplacementE2E(t *testing.T) {
	root := filepath.Join(t.TempDir(), "root")
	if err := os.Mkdir(root, 0o700); err != nil {
		t.Fatalf("create root: %v", err)
	}
	first := requireWindowsDirectoryIdentity(t, root)
	if err := os.Rename(root, root+"-old"); err != nil {
		t.Fatalf("rename root: %v", err)
	}
	if err := os.Mkdir(root, 0o700); err != nil {
		t.Fatalf("replace root: %v", err)
	}
	if second := requireWindowsDirectoryIdentity(t, root); second == first {
		t.Fatalf("directory identity survived path replacement: %q", first)
	}
}

func requireWindowsDirectoryIdentity(t *testing.T, path string) string {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat root: %v", err)
	}
	identity, err := StableDirectoryIdentity(path, info)
	if err != nil || identity == "" {
		t.Fatalf("StableDirectoryIdentity() = %q, %v", identity, err)
	}
	return identity
}
