package memshared_test

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	memory "github.com/anthropic-ai/super-agent-v3/internal/module/memory"
	shared "github.com/anthropic-ai/super-agent-v3/internal/module/memory/shared"
)

func TestValidateMemoryRootRejectsRelativePath(t *testing.T) {
	if _, err := shared.ValidateMemoryRoot("relative/path"); !errors.Is(err, shared.ErrInvalidMemoryRoot) {
		t.Fatalf("ValidateMemoryRoot(relative/path) error = %v, want %v", err, shared.ErrInvalidMemoryRoot)
	}
}

func TestValidateMemoryRootNormalizesTrailingSeparator(t *testing.T) {
	root := filepath.Join(newTestMemoryRoot(t), "nested")
	validated, err := shared.ValidateMemoryRoot(root + string(os.PathSeparator))
	if err != nil {
		t.Fatalf("ValidateMemoryRoot() error = %v", err)
	}
	want := filepath.Clean(root) + string(os.PathSeparator)
	if validated != want {
		t.Fatalf("ValidateMemoryRoot() = %q, want %q", validated, want)
	}
}

func TestValidateMemoryRootRejectsUNCLikeDoubleSlash(t *testing.T) {
	if _, err := shared.ValidateMemoryRoot("//server/share"); !errors.Is(err, shared.ErrInvalidMemoryRoot) {
		t.Fatalf("ValidateMemoryRoot(//server/share) error = %v, want %v", err, shared.ErrInvalidMemoryRoot)
	}
}

func TestValidateMemoryWritePathRejectsTraversal(t *testing.T) {
	root := filepath.Join(newTestMemoryRoot(t), "allowed")
	if _, err := memory.ValidateMemoryWritePath(root, filepath.Join("..", "escape.md")); !errors.Is(err, memory.ErrInvalidMemoryWritePath) {
		t.Fatalf("ValidateMemoryWritePath() error = %v, want %v", err, memory.ErrInvalidMemoryWritePath)
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
	if _, err := memory.ValidateMemoryWritePath(root, filepath.Join("bad", "entry.md")); !errors.Is(err, memory.ErrInvalidMemoryWritePath) {
		t.Fatalf("ValidateMemoryWritePath(dangling symlink) error = %v, want %v", err, memory.ErrInvalidMemoryWritePath)
	}
}

func TestValidateMemoryReadPathRejectsTraversal(t *testing.T) {
	root := filepath.Join(newTestMemoryRoot(t), "allowed")
	if _, err := memory.ValidateMemoryReadPath(root, filepath.Join("..", "escape.md")); !errors.Is(err, memory.ErrInvalidMemoryReadPath) {
		t.Fatalf("ValidateMemoryReadPath() error = %v, want %v", err, memory.ErrInvalidMemoryReadPath)
	}
}

func TestValidateMemoryReadPathRejectsSymlinkEscape(t *testing.T) {
	root := filepath.Join(newTestMemoryRoot(t), "allowed")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatalf("MkdirAll(%q) error = %v", root, err)
	}
	outside := filepath.Join(t.TempDir(), "outside.md")
	if err := os.WriteFile(outside, []byte("outside"), 0o644); err != nil {
		t.Fatalf("WriteFile(%q) error = %v", outside, err)
	}
	linkPath := filepath.Join(root, "escaped.md")
	if err := os.Symlink(outside, linkPath); err != nil {
		t.Skipf("Symlink unsupported: %v", err)
	}
	if _, err := memory.ValidateMemoryReadPath(root, linkPath); !errors.Is(err, memory.ErrInvalidMemoryReadPath) {
		t.Fatalf("ValidateMemoryReadPath(symlink escape) error = %v, want %v", err, memory.ErrInvalidMemoryReadPath)
	}
}

func newTestMemoryRoot(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), "memory-root")
}
