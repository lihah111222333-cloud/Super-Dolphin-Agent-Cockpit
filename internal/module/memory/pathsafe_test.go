package memory

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestValidateMemoryRootRejectsRelativePath(t *testing.T) {
	if _, err := ValidateMemoryRoot("relative/path"); !errors.Is(err, ErrInvalidMemoryRoot) {
		t.Fatalf("ValidateMemoryRoot(relative/path) error = %v, want %v", err, ErrInvalidMemoryRoot)
	}
}

func TestValidateMemoryRootNormalizesTrailingSeparator(t *testing.T) {
	root := filepath.Join(newTestMemoryRoot(t), "nested")
	validated, err := ValidateMemoryRoot(root + string(os.PathSeparator))
	if err != nil {
		t.Fatalf("ValidateMemoryRoot() error = %v", err)
	}
	want := filepath.Clean(root) + string(os.PathSeparator)
	if validated != want {
		t.Fatalf("ValidateMemoryRoot() = %q, want %q", validated, want)
	}
}

func TestValidateMemoryWritePathRejectsTraversal(t *testing.T) {
	root := filepath.Join(newTestMemoryRoot(t), "allowed")
	if _, err := ValidateMemoryWritePath(root, filepath.Join("..", "escape.md")); !errors.Is(err, ErrInvalidMemoryWritePath) {
		t.Fatalf("ValidateMemoryWritePath() error = %v, want %v", err, ErrInvalidMemoryWritePath)
	}
}

func TestValidateMemoryWritePathRejectsDanglingSymlink(t *testing.T) {
	root := filepath.Join(newTestMemoryRoot(t), "allowed")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatalf("MkdirAll(%q) error = %v", root, err)
	}
	linkPath := filepath.Join(root, "bad")
	if err := os.Symlink(filepath.Join(root, "missing-target"), linkPath); err != nil {
		t.Skipf("Symlink unsupported: %v", err)
	}
	if _, err := ValidateMemoryWritePath(root, filepath.Join("bad", "entry.md")); !errors.Is(err, ErrInvalidMemoryWritePath) {
		t.Fatalf("ValidateMemoryWritePath(dangling symlink) error = %v, want %v", err, ErrInvalidMemoryWritePath)
	}
}
