package search

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/internal/lspplatform"
)

func TestResolvePathInRootsAllowsAbsolutePathInAdditionalRoot(t *testing.T) {
	primary := t.TempDir()
	extra := t.TempDir()
	target := filepath.Join(extra, "pkg", "file.go")
	if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
		t.Fatalf("mkdir target parent: %v", err)
	}
	if err := os.WriteFile(target, []byte("package pkg\n"), 0o600); err != nil {
		t.Fatalf("write target: %v", err)
	}

	got, err := ResolvePathInRoots(primary, []string{extra}, target)
	if err != nil {
		t.Fatalf("ResolvePathInRoots() error = %v", err)
	}
	if got.Root != cleanRealPath(t, extra) {
		t.Fatalf("Root = %q, want %q", got.Root, cleanRealPath(t, extra))
	}
	if got.AbsPath != cleanRealPath(t, target) {
		t.Fatalf("AbsPath = %q, want %q", got.AbsPath, cleanRealPath(t, target))
	}
}

func TestResolvePathInRootsKeepsRelativePathsUnderPrimaryRoot(t *testing.T) {
	primary := t.TempDir()
	extra := t.TempDir()
	primaryTarget := filepath.Join(primary, "same.txt")
	extraTarget := filepath.Join(extra, "same.txt")
	if err := os.WriteFile(primaryTarget, []byte("primary\n"), 0o600); err != nil {
		t.Fatalf("write primary target: %v", err)
	}
	if err := os.WriteFile(extraTarget, []byte("extra\n"), 0o600); err != nil {
		t.Fatalf("write extra target: %v", err)
	}

	got, err := ResolvePathInRoots(primary, []string{extra}, "same.txt")
	if err != nil {
		t.Fatalf("ResolvePathInRoots() error = %v", err)
	}
	if got.Root != cleanRealPath(t, primary) {
		t.Fatalf("Root = %q, want primary root %q", got.Root, cleanRealPath(t, primary))
	}
	if got.AbsPath != cleanRealPath(t, primaryTarget) {
		t.Fatalf("AbsPath = %q, want %q", got.AbsPath, cleanRealPath(t, primaryTarget))
	}
}

func TestResolvePathInRootsRejectsPathOutsideTrustedRoots(t *testing.T) {
	primary := t.TempDir()
	extra := t.TempDir()
	outside := filepath.Join(t.TempDir(), "outside.txt")
	if err := os.WriteFile(outside, []byte("outside\n"), 0o600); err != nil {
		t.Fatalf("write outside target: %v", err)
	}

	_, err := ResolvePathInRoots(primary, []string{extra}, outside)
	if err == nil {
		t.Fatal("ResolvePathInRoots() error = nil, want outside roots error")
	}
	if !strings.Contains(err.Error(), outside) || !strings.Contains(err.Error(), cleanRealPath(t, primary)) || !strings.Contains(err.Error(), cleanRealPath(t, extra)) {
		t.Fatalf("error = %q, want requested path and allowed roots", err.Error())
	}
}

func cleanRealPath(t *testing.T, path string) string {
	t.Helper()
	// 使用与生产路径解析相同的平台 canonicalizer，避免 Windows 受限 token 下 EvalSymlinks 误报 Access Denied。
	real, err := lspplatform.CanonicalExistingPath(path)
	if err != nil {
		t.Fatalf("canonicalize path %q: %v", path, err)
	}
	return filepath.Clean(real)
}
