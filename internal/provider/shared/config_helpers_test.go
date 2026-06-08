package shared

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestResolveBinaryDirPrefersExplicitConfig(t *testing.T) {
	t.Parallel()

	if got := ResolveBinaryDir("/tmp/cwd", map[string]any{"binary_dir": "/tmp/explicit"}); got != "/tmp/explicit" {
		t.Fatalf("ResolveBinaryDir() = %q, want explicit binary_dir", got)
	}
}

func TestResolveBinaryDirPrefersPeerBinDirEnv(t *testing.T) {
	peerDir := t.TempDir()
	exeDir := t.TempDir()
	cwd := t.TempDir()
	writeDummyBinary(t, peerDir, "mcp-lsp")
	writeDummyBinary(t, exeDir, "mcp-orch")
	t.Setenv("GO_AGENT_PEER_BIN_DIR", peerDir)
	resolver := newStubBinaryResolver(
		func() (string, error) { return filepath.Join(exeDir, "super-agent-debug"), nil },
		func(string) (string, error) { return "", errors.New("not found") },
	)

	if got := resolver.ResolveBinaryDir(cwd, nil); got != peerDir {
		t.Fatalf("ResolveBinaryDir() = %q, want peer bin dir %q", got, peerDir)
	}
}

func TestResolveBinaryDirPackagedRuntimeUsesOnlyBundleBin(t *testing.T) {
	projectRoot := t.TempDir()
	bundleDir := filepath.Join(projectRoot, "bin")
	inheritedDir := t.TempDir()
	if err := os.MkdirAll(bundleDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(%q) error = %v", bundleDir, err)
	}
	writeRuntimeManifest(t, projectRoot)
	writeDummyBinary(t, inheritedDir, "mcp-lsp")
	t.Setenv("PROJECT_ROOT", projectRoot)
	t.Setenv("GO_AGENT_PEER_BIN_DIR", strings.Join([]string{bundleDir, inheritedDir}, string(os.PathListSeparator)))
	resolver := newStubBinaryResolver(
		func() (string, error) { return filepath.Join(t.TempDir(), "super-agent-debug"), nil },
		func(name string) (string, error) { return filepath.Join(inheritedDir, name), nil },
	)

	got := resolver.ResolveBinaryDir(t.TempDir(), map[string]any{"binary_dir": inheritedDir})
	if got != bundleDir {
		t.Fatalf("ResolveBinaryDir() = %q, want packaged bundle bin %q", got, bundleDir)
	}
}

func TestResolveBinaryDirPackagedRuntimeDoesNotFallbackWhenBundleSidecarMissing(t *testing.T) {
	projectRoot := t.TempDir()
	bundleDir := filepath.Join(projectRoot, "bin")
	exeDir := t.TempDir()
	cwd := t.TempDir()
	lookPathDir := t.TempDir()
	if err := os.MkdirAll(bundleDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(%q) error = %v", bundleDir, err)
	}
	writeRuntimeManifest(t, projectRoot)
	writeDummyBinary(t, exeDir, "mcp-lsp")
	writeDummyBinary(t, cwd, "mcp-orch")
	writeDummyBinary(t, lookPathDir, "mcp-lsp")
	t.Setenv("PROJECT_ROOT", projectRoot)
	t.Setenv("GO_AGENT_PEER_BIN_DIR", strings.Join([]string{bundleDir, lookPathDir}, string(os.PathListSeparator)))
	resolver := newStubBinaryResolver(
		func() (string, error) { return filepath.Join(exeDir, "super-agent-debug"), nil },
		func(name string) (string, error) { return filepath.Join(lookPathDir, name), nil },
	)

	got := resolver.ResolveBinaryDir(cwd, nil)
	if got != bundleDir {
		t.Fatalf("ResolveBinaryDir() = %q, want missing packaged sidecar to stay on bundle bin %q", got, bundleDir)
	}
}

func TestResolveBinaryDirUsesExecutableDirWhenManagedBinaryPresent(t *testing.T) {
	t.Parallel()

	exeDir := t.TempDir()
	cwd := t.TempDir()
	writeDummyBinary(t, exeDir, "mcp-lsp")
	writeDummyBinary(t, cwd, "mcp-orch")
	resolver := newStubBinaryResolver(
		func() (string, error) { return filepath.Join(exeDir, "super-agent-debug"), nil },
		func(string) (string, error) { return "", errors.New("not found") },
	)

	if got := resolver.ResolveBinaryDir(cwd, nil); got != exeDir {
		t.Fatalf("ResolveBinaryDir() = %q, want executable dir %q", got, exeDir)
	}
}

func TestResolveBinaryDirFallsBackToCWDWhenExecutableDirMissesManagedBinary(t *testing.T) {
	t.Parallel()

	exeDir := t.TempDir()
	cwd := t.TempDir()
	writeDummyBinary(t, cwd, "mcp-lsp")
	resolver := newStubBinaryResolver(
		func() (string, error) { return filepath.Join(exeDir, "super-agent-debug"), nil },
		func(string) (string, error) { return "", errors.New("not found") },
	)

	if got := resolver.ResolveBinaryDir(cwd, nil); got != cwd {
		t.Fatalf("ResolveBinaryDir() = %q, want cwd %q", got, cwd)
	}
}

func TestResolveBinaryDirFallsBackToLookPathDir(t *testing.T) {
	t.Parallel()

	lookPathDir := t.TempDir()
	writeDummyBinary(t, lookPathDir, "mcp-orch")
	resolver := newStubBinaryResolver(
		func() (string, error) { return filepath.Join(t.TempDir(), "super-agent-debug"), nil },
		func(name string) (string, error) {
			if name == "mcp-orch" {
				return filepath.Join(lookPathDir, name), nil
			}
			return "", errors.New("not found")
		},
	)

	if got := resolver.ResolveBinaryDir("", nil); got != lookPathDir {
		t.Fatalf("ResolveBinaryDir() = %q, want LookPath dir %q", got, lookPathDir)
	}
}

func TestConfigStringDropsObjectArtifacts(t *testing.T) {
	t.Parallel()

	cfg := map[string]any{
		"model":    "[object Object]",
		"fallback": "gpt-5.5",
	}
	if got := ConfigString(cfg, "model", "fallback"); got != "gpt-5.5" {
		t.Fatalf("ConfigString() = %q, want fallback", got)
	}
}

func newStubBinaryResolver(exe func() (string, error), lp func(string) (string, error)) binaryDirResolver {
	return binaryDirResolver{
		executablePath: exe,
		lookPath:       lp,
	}
}

func writeDummyBinary(t *testing.T, dir, name string) {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatalf("WriteFile(%q) error = %v", path, err)
	}
}

func writeRuntimeManifest(t *testing.T, projectRoot string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(projectRoot, "runtime-manifest.json"), []byte("{}\n"), 0o600); err != nil {
		t.Fatalf("WriteFile(runtime-manifest.json) error = %v", err)
	}
}
