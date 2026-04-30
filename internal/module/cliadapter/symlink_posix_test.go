//go:build !windows

package cliadapter

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSetupWorkspaceSkills_CreatesSymlinkPOSIX(t *testing.T) {
	workspace := t.TempDir()
	cache := t.TempDir()
	sentinel := filepath.Join(cache, "marker")
	if err := os.WriteFile(sentinel, []byte("ok"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := SetupWorkspaceSkills(workspace, cache); err != nil {
		t.Fatalf("SetupWorkspaceSkills: %v", err)
	}
	via := filepath.Join(workspace, ".claude", "skills", "marker")
	b, err := os.ReadFile(via)
	if err != nil {
		t.Fatalf("read via symlink: %v", err)
	}
	if string(b) != "ok" {
		t.Errorf("read = %q, want ok", string(b))
	}
	info, err := os.Lstat(filepath.Join(workspace, ".claude", "skills"))
	if err != nil {
		t.Fatalf("lstat: %v", err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		t.Errorf(".claude/skills is not a symlink")
	}
}

func TestSetupWorkspaceSkills_ReplacesExistingEntry(t *testing.T) {
	workspace := t.TempDir()
	cache := t.TempDir()
	pre := filepath.Join(workspace, ".claude", "skills")
	if err := os.MkdirAll(pre, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pre, "stale"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := SetupWorkspaceSkills(workspace, cache); err != nil {
		t.Fatalf("SetupWorkspaceSkills: %v", err)
	}
	if _, err := os.Stat(filepath.Join(pre, "stale")); !os.IsNotExist(err) {
		t.Errorf("stale entry not removed: %v", err)
	}
	info, _ := os.Lstat(pre)
	if info.Mode()&os.ModeSymlink == 0 {
		t.Errorf(".claude/skills should now be a symlink")
	}
}

func TestSetupWorkspaceSkills_CreatesCacheIfMissing(t *testing.T) {
	workspace := t.TempDir()
	cache := filepath.Join(t.TempDir(), "not-yet")
	if err := SetupWorkspaceSkills(workspace, cache); err != nil {
		t.Fatalf("SetupWorkspaceSkills: %v", err)
	}
	if _, err := os.Stat(cache); err != nil {
		t.Errorf("cacheDir not created: %v", err)
	}
}
