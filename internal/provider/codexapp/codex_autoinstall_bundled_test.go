package codexapp

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

func skipCodexCLIIntegrationInShortMode(t *testing.T) {
	t.Helper()
	if testing.Short() {
		t.Skip("skipping Codex CLI integration test in short mode")
	}
}

func TestEnsureCodexCLIAvailableUsesBundledPeerBinBeforeNetwork(t *testing.T) {
	skipCodexCLIIntegrationInShortMode(t)
	t.Setenv("PATH", t.TempDir())
	resourcesRoot := t.TempDir()
	peerDir := filepath.Join(resourcesRoot, "bin")
	if err := os.MkdirAll(peerDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(%q) error = %v", peerDir, err)
	}
	codexPath := filepath.Join(peerDir, codexExecutableFileName())
	writeFakeCodex(t, codexPath, true)
	writeBundledCodexManifestForTest(t, resourcesRoot, "")
	t.Setenv("GO_AGENT_PEER_BIN_DIR", peerDir)
	t.Setenv(codexInstallRootEnv, t.TempDir())
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		http.Error(w, "network fallback must not run when bundled codex exists", http.StatusInternalServerError)
	}))
	t.Cleanup(server.Close)
	t.Setenv(codexReleaseAPIURLEnv, server.URL+"/latest")

	if err := ensureCodexCLIAvailable(context.Background()); err != nil {
		t.Fatalf("ensureCodexCLIAvailable() error = %v", err)
	}
	got, err := exec.LookPath(codexBinaryName)
	if err != nil {
		t.Fatalf("LookPath(codex) error = %v", err)
	}
	if got != codexPath {
		t.Fatalf("LookPath(codex) = %q, want bundled %q", got, codexPath)
	}
	if requests.Load() != 0 {
		t.Fatalf("release server received %d requests, want 0", requests.Load())
	}
}

func TestEnsureCodexCLIAvailableFailsFastWhenBundledCodexIsNotExecutable(t *testing.T) {
	peerDir := t.TempDir()
	brokenBundledCodex := filepath.Join(peerDir, codexExecutableFileName())
	if err := os.WriteFile(brokenBundledCodex, []byte("#!/bin/sh\n"), 0o644); err != nil {
		t.Fatalf("write bundled codex: %v", err)
	}
	userDir := t.TempDir()
	writeFakeCodex(t, filepath.Join(userDir, codexExecutableFileName()), true)
	t.Setenv("PATH", userDir)
	t.Setenv("GO_AGENT_PEER_BIN_DIR", peerDir)
	t.Setenv(codexInstallRootEnv, t.TempDir())
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		requests.Add(1)
	}))
	t.Cleanup(server.Close)
	t.Setenv(codexReleaseAPIURLEnv, server.URL+"/latest")

	err := ensureCodexCLIAvailable(context.Background())
	if err == nil {
		t.Fatal("ensureCodexCLIAvailable() error = nil, want bundled asset failure")
	}
	for _, want := range []string{"bundled Codex CLI", "not executable", brokenBundledCodex} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("ensureCodexCLIAvailable() error missing %q:\n%s", want, err)
		}
	}
	if requests.Load() != 0 {
		t.Fatalf("release server received %d requests, want 0", requests.Load())
	}
}

func TestEnsureCodexCLIAvailablePackagedRuntimeMissingBundledCodexDoesNotFallbackToUserPath(t *testing.T) {
	peerDir := t.TempDir()
	userDir := t.TempDir()
	writeFakeCodex(t, filepath.Join(userDir, codexExecutableFileName()), true)
	t.Setenv("PATH", userDir)
	t.Setenv("GO_AGENT_PEER_BIN_DIR", peerDir)
	t.Setenv("SUPER_DOLPHIN_REQUIRE_BUNDLED_CODEX", "1")
	t.Setenv(codexInstallRootEnv, t.TempDir())
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		requests.Add(1)
	}))
	t.Cleanup(server.Close)
	t.Setenv(codexReleaseAPIURLEnv, server.URL+"/latest")

	err := ensureCodexCLIAvailable(context.Background())
	if err == nil {
		t.Fatal("ensureCodexCLIAvailable() error = nil, want packaged bundled Codex failure")
	}
	for _, want := range []string{"bundled Codex CLI", "required"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("ensureCodexCLIAvailable() error missing %q:\n%s", want, err)
		}
	}
	if requests.Load() != 0 {
		t.Fatalf("release server received %d requests, want 0", requests.Load())
	}
}

func TestEnsureCodexCLIAvailablePackagedRuntimeMissingBundledCodexIgnoresInheritedPeerBinDir(t *testing.T) {
	packagedPeerDir := t.TempDir()
	inheritedPeerDir := t.TempDir()
	marker := filepath.Join(t.TempDir(), "fake-codex-ran")
	writeFakeCodexWithMarker(t, filepath.Join(inheritedPeerDir, codexExecutableFileName()))
	t.Setenv("CODEX_FAKE_MARKER", marker)
	t.Setenv("PATH", t.TempDir())
	t.Setenv("GO_AGENT_PEER_BIN_DIR", strings.Join([]string{packagedPeerDir, inheritedPeerDir}, string(os.PathListSeparator)))
	t.Setenv("SUPER_DOLPHIN_REQUIRE_BUNDLED_CODEX", "1")
	t.Setenv(codexInstallRootEnv, t.TempDir())

	err := ensureCodexCLIAvailable(context.Background())
	if err == nil {
		t.Fatal("ensureCodexCLIAvailable() error = nil, want packaged bundled Codex failure")
	}
	for _, want := range []string{"bundled Codex CLI", "required", packagedPeerDir} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("ensureCodexCLIAvailable() error missing %q:\n%s", want, err)
		}
	}
	if _, statErr := os.Stat(marker); statErr == nil || !os.IsNotExist(statErr) {
		t.Fatalf("inherited peer codex executed; marker stat error = %v", statErr)
	}
}
