//go:build windows

package lspplatform

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/windows"
)

// TestCanonicalWindowsDirectoryPathResolvesJunction 验证目录 junction 被解析为其最终目录。
func TestCanonicalWindowsDirectoryPathResolvesJunction(t *testing.T) {
	target := filepath.Join(t.TempDir(), "target")
	if err := os.Mkdir(target, 0o700); err != nil {
		t.Fatalf("create junction target: %v", err)
	}
	alias := filepath.Join(t.TempDir(), "junction")
	createWindowsDirectoryJunction(t, alias, target)

	got, err := CanonicalDirectoryPath(alias)
	if err != nil {
		t.Fatalf("CanonicalDirectoryPath() on junction error = %v", err)
	}
	assertCanonicalSameFile(t, target, got)
	if strings.EqualFold(filepath.Clean(alias), filepath.Clean(got)) {
		t.Fatalf("CanonicalDirectoryPath() kept junction alias %q", got)
	}
}

// TestCanonicalWindowsExistingPathResolvesSymlink 验证文件符号链接被解析为其最终文件。
func TestCanonicalWindowsExistingPathResolvesSymlink(t *testing.T) {
	target := filepath.Join(t.TempDir(), "target.txt")
	if err := os.WriteFile(target, []byte("fixture"), 0o600); err != nil {
		t.Fatalf("write symlink target: %v", err)
	}
	alias := filepath.Join(t.TempDir(), "symlink.txt")
	if err := os.Symlink(target, alias); err != nil {
		if errors.Is(err, windows.ERROR_PRIVILEGE_NOT_HELD) {
			t.Skipf("Windows symbolic-link privilege unavailable: %v", err)
		}
		t.Fatalf("create Windows symlink: %v", err)
	}
	t.Cleanup(func() {
		if err := os.Remove(alias); err != nil && !os.IsNotExist(err) {
			t.Errorf("remove Windows symlink: %v", err)
		}
	})

	got, err := CanonicalExistingPath(alias)
	if err != nil {
		t.Fatalf("CanonicalExistingPath() on symlink error = %v", err)
	}
	assertCanonicalSameFile(t, target, got)
	if strings.EqualFold(filepath.Clean(alias), filepath.Clean(got)) {
		t.Fatalf("CanonicalExistingPath() kept symlink alias %q", got)
	}
}

func createWindowsDirectoryJunction(t *testing.T, alias, target string) {
	t.Helper()
	output, err := exec.Command("cmd.exe", "/d", "/c", "mklink", "/J", alias, target).CombinedOutput()
	if err != nil {
		t.Fatalf("create Windows junction %q -> %q: %v (%s)", alias, target, err, strings.TrimSpace(string(output)))
	}
	t.Cleanup(func() {
		if err := os.Remove(alias); err != nil && !os.IsNotExist(err) {
			t.Errorf("remove Windows junction: %v", err)
		}
	})
}
