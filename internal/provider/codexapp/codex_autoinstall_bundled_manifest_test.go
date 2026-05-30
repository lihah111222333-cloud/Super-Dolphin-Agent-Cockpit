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

func TestEnsureCodexCLIAvailableRejectsBundledManifestDigestMismatch(t *testing.T) {
	resourcesRoot := t.TempDir()
	peerDir := filepath.Join(resourcesRoot, "bin")
	if err := os.MkdirAll(peerDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(%q) error = %v", peerDir, err)
	}
	codexPath := filepath.Join(peerDir, codexExecutableFileName())
	writeFakeCodex(t, codexPath, true)
	writeBundledCodexManifestForTest(t, resourcesRoot, strings.Repeat("0", 64))
	userDir := t.TempDir()
	marker := filepath.Join(t.TempDir(), "user-codex-ran")
	writeFakeCodexWithMarker(t, filepath.Join(userDir, codexExecutableFileName()))
	t.Setenv("CODEX_FAKE_MARKER", marker)
	t.Setenv("PATH", userDir)
	t.Setenv("GO_AGENT_PEER_BIN_DIR", peerDir)
	t.Setenv(codexInstallRootEnv, t.TempDir())
	t.Setenv(codexReleaseSHA256EnvForTest, strings.Repeat("a", 64))
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		requests.Add(1)
	}))
	t.Cleanup(server.Close)
	t.Setenv(codexTrustedReleaseMirrorEnvForTest, "1")
	t.Setenv(codexReleaseAPIURLEnv, server.URL+"/latest")

	err := ensureCodexCLIAvailable(context.Background())
	if err == nil {
		t.Fatal("ensureCodexCLIAvailable() error = nil, want bundled manifest digest failure")
	}
	for _, want := range []string{"bundled Codex", "digest mismatch", codexPath} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("ensureCodexCLIAvailable() error missing %q:\n%s", want, err)
		}
	}
	if requests.Load() != 0 {
		t.Fatalf("release server received %d requests, want 0", requests.Load())
	}
	if _, statErr := os.Stat(marker); statErr == nil || !os.IsNotExist(statErr) {
		t.Fatalf("user PATH codex executed; marker stat error = %v", statErr)
	}
}

func TestEnsureCodexCLIAvailableRejectsBundledMissingOrInvalidManifest(t *testing.T) {
	t.Run("missing", func(t *testing.T) {
		assertBundledManifestRejected(t, nil, "read bundled Codex manifest")
	})
	t.Run("invalid", func(t *testing.T) {
		assertBundledManifestRejected(t, writeInvalidBundledCodexManifestForTest, "decode bundled Codex manifest")
	})
}

func writeInvalidBundledCodexManifestForTest(t *testing.T, resourcesRoot string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(resourcesRoot, codexManagedManifestName), []byte("{"), 0o600); err != nil {
		t.Fatalf("write invalid bundled manifest: %v", err)
	}
}

func assertBundledManifestRejected(t *testing.T, writeManifest func(*testing.T, string), want string) {
	t.Helper()
	resourcesRoot := t.TempDir()
	peerDir := filepath.Join(resourcesRoot, "bin")
	if err := os.MkdirAll(peerDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(%q) error = %v", peerDir, err)
	}
	writeFakeCodex(t, filepath.Join(peerDir, codexExecutableFileName()), true)
	if writeManifest != nil {
		writeManifest(t, resourcesRoot)
	}
	userDir := t.TempDir()
	marker := filepath.Join(t.TempDir(), "user-codex-ran")
	writeFakeCodexWithMarker(t, filepath.Join(userDir, codexExecutableFileName()))
	t.Setenv("CODEX_FAKE_MARKER", marker)
	t.Setenv("PATH", userDir)
	t.Setenv("GO_AGENT_PEER_BIN_DIR", peerDir)
	t.Setenv(codexInstallRootEnv, t.TempDir())
	t.Setenv(codexReleaseSHA256EnvForTest, strings.Repeat("a", 64))
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		requests.Add(1)
	}))
	t.Cleanup(server.Close)
	t.Setenv(codexTrustedReleaseMirrorEnvForTest, "1")
	t.Setenv(codexReleaseAPIURLEnv, server.URL+"/latest")

	err := ensureCodexCLIAvailable(context.Background())
	if err == nil {
		t.Fatal("ensureCodexCLIAvailable() error = nil, want bundled manifest failure")
	}
	for _, substring := range []string{"bundled Codex", want} {
		if !strings.Contains(err.Error(), substring) {
			t.Fatalf("ensureCodexCLIAvailable() error missing %q:\n%s", substring, err)
		}
	}
	if requests.Load() != 0 {
		t.Fatalf("release server received %d requests, want 0", requests.Load())
	}
	if _, statErr := os.Stat(marker); statErr == nil || !os.IsNotExist(statErr) {
		t.Fatalf("user PATH codex executed; marker stat error = %v", statErr)
	}
}

func TestEnsureCodexCLIAvailableAcceptsBundledManifestWithoutVendorFields(t *testing.T) {
	resourcesRoot := t.TempDir()
	peerDir := filepath.Join(resourcesRoot, "bin")
	if err := os.MkdirAll(peerDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(%q) error = %v", peerDir, err)
	}
	codexPath := filepath.Join(peerDir, codexExecutableFileName())
	writeFakeCodex(t, codexPath, true)
	writeBundledCodexManifestForTest(t, resourcesRoot, "")
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
