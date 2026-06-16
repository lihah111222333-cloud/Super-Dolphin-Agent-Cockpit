package skillhash

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestContentReturnsSHA256Hex verifies content hashes are stable lowercase SHA-256 values.
func TestContentReturnsSHA256Hex(t *testing.T) {
	t.Parallel()

	const want = "2cf24dba5fb0a30e26e83b2ac5b9e29e1b161e5c1fa7425e73043362938b9824"
	if got := Content("hello"); got != want {
		t.Fatalf("Content() = %q, want %q", got, want)
	}
}

// TestDirHashesSortedRegularFilesAndSkipsSymlinks verifies directory hashes ignore traversal order.
func TestDirHashesSortedRegularFilesAndSkipsSymlinks(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "z.txt"), "last")
	mustWrite(t, filepath.Join(root, "a.txt"), "first")
	if err := os.Symlink(filepath.Join(root, "a.txt"), filepath.Join(root, "link.txt")); err != nil {
		t.Logf("skipping symlink part of test: %v", err)
	}

	got, err := Dir(root)
	if err != nil {
		t.Fatalf("Dir() error = %v", err)
	}
	parts := []string{
		"a.txt\x00" + Content("first"),
		"z.txt\x00" + Content("last"),
	}
	want := Content(strings.Join(parts, "\x00"))
	if got != want {
		t.Fatalf("Dir() = %q, want %q", got, want)
	}
}

// TestExistingDirReturnsEmptyHashForMissingPath verifies absent skill directories are not errors.
func TestExistingDirReturnsEmptyHashForMissingPath(t *testing.T) {
	t.Parallel()

	got, err := ExistingDir(filepath.Join(t.TempDir(), "missing"))
	if err != nil {
		t.Fatalf("ExistingDir() error = %v", err)
	}
	if got != "" {
		t.Fatalf("ExistingDir() = %q, want empty hash", got)
	}
}

func mustWrite(t *testing.T, path, content string) {
	t.Helper()

	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
