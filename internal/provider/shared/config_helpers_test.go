package shared

import (
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

var resolverStubMu sync.Mutex

func TestResolveBinaryDirPrefersExplicitConfig(t *testing.T) {
	t.Parallel()

	if got := ResolveBinaryDir("/tmp/cwd", map[string]any{"binary_dir": "/tmp/explicit"}); got != "/tmp/explicit" {
		t.Fatalf("ResolveBinaryDir() = %q, want explicit binary_dir", got)
	}
}

func TestResolveBinaryDirUsesExecutableDirWhenManagedBinaryPresent(t *testing.T) {
	t.Parallel()

	exeDir := t.TempDir()
	cwd := t.TempDir()
	writeDummyBinary(t, exeDir, "mcp-lsp")
	writeDummyBinary(t, cwd, "mcp-orch")
	stubBinaryResolvers(t,
		func() (string, error) { return filepath.Join(exeDir, "super-agent-debug"), nil },
		func(string) (string, error) { return "", errors.New("not found") },
	)

	if got := ResolveBinaryDir(cwd, nil); got != exeDir {
		t.Fatalf("ResolveBinaryDir() = %q, want executable dir %q", got, exeDir)
	}
}

func TestResolveBinaryDirFallsBackToCWDWhenExecutableDirMissesManagedBinary(t *testing.T) {
	t.Parallel()

	exeDir := t.TempDir()
	cwd := t.TempDir()
	writeDummyBinary(t, cwd, "mcp-lsp")
	stubBinaryResolvers(t,
		func() (string, error) { return filepath.Join(exeDir, "super-agent-debug"), nil },
		func(string) (string, error) { return "", errors.New("not found") },
	)

	if got := ResolveBinaryDir(cwd, nil); got != cwd {
		t.Fatalf("ResolveBinaryDir() = %q, want cwd %q", got, cwd)
	}
}

func TestResolveBinaryDirFallsBackToLookPathDir(t *testing.T) {
	t.Parallel()

	lookPathDir := t.TempDir()
	writeDummyBinary(t, lookPathDir, "mcp-orch")
	stubBinaryResolvers(t,
		func() (string, error) { return filepath.Join(t.TempDir(), "super-agent-debug"), nil },
		func(name string) (string, error) {
			if name == "mcp-orch" {
				return filepath.Join(lookPathDir, name), nil
			}
			return "", errors.New("not found")
		},
	)

	if got := ResolveBinaryDir("", nil); got != lookPathDir {
		t.Fatalf("ResolveBinaryDir() = %q, want LookPath dir %q", got, lookPathDir)
	}
}

func stubBinaryResolvers(t *testing.T, exe func() (string, error), lp func(string) (string, error)) {
	t.Helper()
	resolverStubMu.Lock()
	origExe, origLookPath := executablePath, lookPath
	executablePath = exe
	lookPath = lp
	t.Cleanup(func() {
		executablePath = origExe
		lookPath = origLookPath
		resolverStubMu.Unlock()
	})
}

func writeDummyBinary(t *testing.T, dir, name string) {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatalf("WriteFile(%q) error = %v", path, err)
	}
}
