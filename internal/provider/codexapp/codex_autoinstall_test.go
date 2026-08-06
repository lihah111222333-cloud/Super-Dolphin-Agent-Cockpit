package codexapp

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"

	"go.uber.org/fx/fxtest"
)

func ensureCodexCLIAvailable(ctx context.Context) error {
	return newCodexInstaller().ensureCLIAvailable(ctx)
}

const (
	codexReleaseSHA256EnvForTest        = "SUPER_DOLPHIN_CODEX_RELEASE_SHA256"
	codexTrustedReleaseMirrorEnvForTest = "SUPER_DOLPHIN_CODEX_TRUSTED_RELEASE_MIRROR"
	codexFakeHelperEnv                  = "SUPER_DOLPHIN_CODEX_FAKE_HELPER"
	codexFakeSupportsAppServerEnv       = "SUPER_DOLPHIN_CODEX_FAKE_SUPPORTS_APP_SERVER"
	codexFakeProbeEnvFileEnv            = "SUPER_DOLPHIN_CODEX_FAKE_PROBE_ENV_FILE"
	codexFakeAppServerEnvFileEnv        = "SUPER_DOLPHIN_CODEX_FAKE_APP_SERVER_ENV_FILE"
)

type codexReleaseTestOptions struct {
	AssetName string
	Body      []byte
}

func TestMain(m *testing.M) {
	if os.Getenv(codexFakeHelperEnv) == "1" {
		runFakeCodexProcess()
	}
	os.Exit(m.Run())
}

func runFakeCodexProcess() {
	if marker := strings.TrimSpace(os.Getenv("CODEX_FAKE_MARKER")); marker != "" {
		_ = os.WriteFile(marker, []byte("ran"), 0o600)
	}
	if hasArgPair(os.Args[1:], "app-server", "--help") {
		writeFakeCodexEnvFile(codexFakeProbeEnvFileEnv)
		if os.Getenv(codexFakeSupportsAppServerEnv) == "1" {
			os.Exit(0)
		}
		os.Exit(42)
	}
	if hasArgPair(os.Args[1:], "app-server", "--listen") {
		writeFakeCodexEnvFile(codexFakeAppServerEnvFileEnv)
		_, _ = fmt.Fprintln(os.Stderr, "listening on: http://127.0.0.1:49231")
		select {}
	}
	os.Exit(0)
}

func hasArgPair(args []string, first, second string) bool {
	for i := 0; i < len(args)-1; i++ {
		if args[i] == first && args[i+1] == second {
			return true
		}
	}
	return false
}

func writeFakeCodexEnvFile(envKey string) {
	path := strings.TrimSpace(os.Getenv(envKey))
	if path == "" {
		return
	}
	if err := os.WriteFile(path, []byte(strings.Join(os.Environ(), "\n")), 0o600); err != nil {
		os.Exit(2)
	}
}

func TestEnsureCodexCLIAvailableAutoInstallsOfficialReleaseWhenMissing(t *testing.T) {
	skipCodexCLIIntegrationInShortMode(t)
	t.Setenv("PATH", t.TempDir())
	installRoot := t.TempDir()
	body := codexWheelForTest(t)
	server := newCodexReleaseTestServer(t, codexReleaseTestOptions{
		AssetName: codexReleaseAssetNameForTest(t),
		Body:      body,
	})
	setTrustedCodexReleaseMirrorForTest(t, server.URL+"/latest", body)
	t.Setenv(codexInstallRootEnv, installRoot)
	if err := ensureCodexCLIAvailable(context.Background()); err != nil {
		t.Fatalf("ensureCodexCLIAvailable() error = %v", err)
	}

	codexPath, err := exec.LookPath(codexBinaryName)
	if err != nil {
		t.Fatalf("managed codex was not added to PATH: %v", err)
	}
	if !strings.HasPrefix(codexPath, installRoot) {
		t.Fatalf("codex path = %q, want under %q", codexPath, installRoot)
	}
	assertExecutableFile(t, codexPath)
	assertExecutableFile(t, filepath.Join(filepath.Dir(codexPath), "..", "codex-path", "rg"))
}

func TestEnsureCodexCLIAvailableUsesManagedInstallBeforeNetwork(t *testing.T) {
	skipCodexCLIIntegrationInShortMode(t)
	t.Setenv("PATH", t.TempDir())
	installRoot := t.TempDir()
	target := filepath.Join(installRoot, "rust-v9.9.9")
	binDir := filepath.Join(target, "codex_cli_bin", "bin")
	codexPath := filepath.Join(binDir, codexExecutableFileName())
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	writeFakeCodex(t, codexPath, true)
	sourceSHA256 := strings.Repeat("b", 64)
	writeManagedCodexManifestForTest(t, target, "rust-v9.9.9", sourceSHA256)
	t.Setenv(codexReleaseSHA256EnvForTest, sourceSHA256)
	t.Setenv(codexInstallRootEnv, installRoot)
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
		t.Fatalf("LookPath(codex) = %q, want %q", got, codexPath)
	}
	if requests.Load() != 0 {
		t.Fatalf("release server received %d requests, want 0", requests.Load())
	}
}

func TestEnsureCodexCLIAvailableRejectsManagedCacheWithoutPinnedChecksum(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	installRoot := t.TempDir()
	binDir := filepath.Join(installRoot, "rust-v9.9.9", "codex_cli_bin", "bin")
	codexPath := filepath.Join(binDir, codexExecutableFileName())
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	writeFakeCodex(t, codexPath, true)
	t.Setenv(codexInstallRootEnv, installRoot)

	err := ensureCodexCLIAvailable(context.Background())
	if err == nil {
		t.Fatal("ensureCodexCLIAvailable() error = nil, want pinned checksum failure")
	}
	if !strings.Contains(err.Error(), codexReleaseSHA256EnvForTest) {
		t.Fatalf("ensureCodexCLIAvailable() error missing checksum env name:\n%s", err)
	}
}

func TestEnsureCodexCLIAvailableRejectsManagedCacheWithoutTrustedManifest(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	installRoot := t.TempDir()
	binDir := filepath.Join(installRoot, "rust-v9.9.9", "codex_cli_bin", "bin")
	codexPath := filepath.Join(binDir, codexExecutableFileName())
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	writeFakeCodex(t, codexPath, true)
	t.Setenv(codexInstallRootEnv, installRoot)
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
		t.Fatal("ensureCodexCLIAvailable() error = nil, want managed manifest failure")
	}
	if !strings.Contains(err.Error(), "managed Codex manifest") {
		t.Fatalf("ensureCodexCLIAvailable() error = %v, want managed manifest failure", err)
	}
	if requests.Load() != 0 {
		t.Fatalf("release server received %d requests, want 0", requests.Load())
	}
}

func TestFindManagedCodexBinaryReturnsReadDirErrors(t *testing.T) {
	rootFile := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(rootFile, []byte("not a directory"), 0o600); err != nil {
		t.Fatalf("write read dir fixture: %v", err)
	}

	_, err := findManagedCodexBinary(context.Background(), rootFile, strings.Repeat("a", 64))
	if err == nil {
		t.Fatal("findManagedCodexBinary() error = nil, want os.ReadDir failure")
	}
	for _, want := range []string{"read managed Codex install root", rootFile} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("findManagedCodexBinary() error missing %q:\n%s", want, err)
		}
	}
}

func TestServerManagerStartupDoesNotEnsureCodexCLIAvailable(t *testing.T) {
	lc := fxtest.NewLifecycle(t)
	installer := newCodexInstaller()
	manager, err := NewServerManager(ServerManagerParams{Lifecycle: lc, Installer: installer})
	if err != nil {
		t.Fatalf("NewServerManager() error = %v", err)
	}
	if manager.installer != installer {
		t.Fatal("NewServerManager() did not retain the injected app-scoped installer")
	}
	if _, err := NewServerManager(ServerManagerParams{Lifecycle: lc}); err == nil {
		t.Fatal("NewServerManager() error = nil, want missing installer failure")
	}
}

func TestEnsureCodexCLIAvailableAutoInstallsOfficialTarGzReleaseWhenMissing(t *testing.T) {
	skipCodexCLIIntegrationInShortMode(t)
	t.Setenv("PATH", t.TempDir())
	installRoot := t.TempDir()
	body := codexTarGzForTest(t)
	server := newCodexReleaseTestServer(t, codexReleaseTestOptions{
		AssetName: codexTarGzReleaseAssetNameForTest(t),
		Body:      body,
	})
	setTrustedCodexReleaseMirrorForTest(t, server.URL+"/latest", body)
	t.Setenv(codexInstallRootEnv, installRoot)

	if err := ensureCodexCLIAvailable(context.Background()); err != nil {
		t.Fatalf("ensureCodexCLIAvailable() error = %v", err)
	}
	codexPath, err := exec.LookPath(codexBinaryName)
	if err != nil {
		t.Fatalf("managed codex was not added to PATH: %v", err)
	}
	if !strings.HasPrefix(codexPath, installRoot) {
		t.Fatalf("codex path = %q, want under %q", codexPath, installRoot)
	}
	assertExecutableFile(t, codexPath)
}

func TestEnsureCodexCLIAvailableReplacesIncompleteInstallTarget(t *testing.T) {
	skipCodexCLIIntegrationInShortMode(t)
	t.Setenv("PATH", t.TempDir())
	installRoot := t.TempDir()
	incompleteTarget := filepath.Join(installRoot, "rust-v0.1.0")
	if err := os.MkdirAll(incompleteTarget, 0o755); err != nil {
		t.Fatalf("MkdirAll incomplete target: %v", err)
	}
	body := codexWheelForTest(t)
	server := newCodexReleaseTestServer(t, codexReleaseTestOptions{
		AssetName: codexReleaseAssetNameForTest(t),
		Body:      body,
	})
	setTrustedCodexReleaseMirrorForTest(t, server.URL+"/latest", body)
	t.Setenv(codexInstallRootEnv, installRoot)

	if err := ensureCodexCLIAvailable(context.Background()); err != nil {
		t.Fatalf("ensureCodexCLIAvailable() error = %v", err)
	}
	assertExecutableFile(t, filepath.Join(incompleteTarget, "codex_cli_bin", "bin", codexExecutableFileName()))
}

func TestFindManagedCodexBinaryUsesSemanticVersionOrder(t *testing.T) {
	skipCodexCLIIntegrationInShortMode(t)
	installRoot := t.TempDir()
	sourceSHA256 := strings.Repeat("c", 64)
	for _, name := range []string{"rust-v0.99.0", "rust-v0.134.0"} {
		target := filepath.Join(installRoot, name)
		binDir := filepath.Join(target, "codex_cli_bin", "bin")
		if err := os.MkdirAll(binDir, 0o755); err != nil {
			t.Fatalf("MkdirAll(%q) error = %v", binDir, err)
		}
		writeFakeCodex(t, filepath.Join(binDir, codexExecutableFileName()), true)
		writeManagedCodexManifestForTest(t, target, name, sourceSHA256)
	}

	got, err := findManagedCodexBinary(context.Background(), installRoot, sourceSHA256)
	if err != nil {
		t.Fatalf("findManagedCodexBinary() error = %v", err)
	}
	want := filepath.Join(installRoot, "rust-v0.134.0", "codex_cli_bin", "bin", codexExecutableFileName())
	if got != want {
		t.Fatalf("findManagedCodexBinary() = %q, want %q", got, want)
	}
}

func TestCodexPathAvailableRequiresAppServerSupport(t *testing.T) {
	skipCodexCLIIntegrationInShortMode(t)
	binDir := t.TempDir()
	writeFakeCodex(t, filepath.Join(binDir, codexExecutableFileName()), false)
	t.Setenv("PATH", binDir)

	if codexPathAvailable(context.Background()) {
		t.Fatal("codexPathAvailable() = true, want false for codex without app-server support")
	}
}

func TestCodexValidationCommandAppliesProcessAttrsBeforeRun(t *testing.T) {
	source, err := os.ReadFile("codex_autoinstall.go")
	if err != nil {
		t.Fatalf("read codex_autoinstall.go: %v", err)
	}
	text := string(source)
	commandIdx := strings.Index(text, `cmd := exec.CommandContext(checkCtx, path, codexAppServerCommand, "--help")`)
	attrsIdx := strings.Index(text, "setCodexProcessAttrs(cmd)")
	runIdx := strings.Index(text, "return cmd.Run() == nil")
	if commandIdx < 0 || attrsIdx < 0 || runIdx < 0 {
		t.Fatalf("validCodexCLI command path missing expected process attr hook")
	}
	if !(commandIdx < attrsIdx && attrsIdx < runIdx) {
		t.Fatalf("validCodexCLI must apply process attrs before running validation command")
	}
}

func TestExtractCodexWheelRejectsOversizedEntry(t *testing.T) {
	wheel := codexWheelWithLargeEntryForTest(t)

	err := extractCodexWheelWithLimits(wheel, filepath.Join(t.TempDir(), "extract"), codexExtractLimits{maxFileBytes: 8, maxTotalBytes: 16})
	if err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("extractCodexWheelWithLimits() error = %v, want size limit error", err)
	}
}

func TestEnsureCodexCLIAvailableRejectsFallbackWithoutPinnedChecksum(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	body := codexWheelForTest(t)
	server := newCodexReleaseTestServer(t, codexReleaseTestOptions{
		AssetName: codexReleaseAssetNameForTest(t),
		Body:      body,
	})
	t.Setenv(codexTrustedReleaseMirrorEnvForTest, "1")
	t.Setenv(codexReleaseAPIURLEnv, server.URL+"/latest")
	t.Setenv(codexInstallRootEnv, t.TempDir())

	err := ensureCodexCLIAvailable(context.Background())
	if err == nil {
		t.Fatal("ensureCodexCLIAvailable() error = nil, want checksum requirement failure")
	}
	if !strings.Contains(err.Error(), codexReleaseSHA256EnvForTest) {
		t.Fatalf("ensureCodexCLIAvailable() error missing checksum env name:\n%s", err)
	}
}

func TestEnsureCodexCLIAvailableRejectsUntrustedReleaseOverride(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	body := codexWheelForTest(t)
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		newCodexReleaseTestHandler(t, codexReleaseTestOptions{
			AssetName: codexReleaseAssetNameForTest(t),
			Body:      body,
		}).ServeHTTP(w, r)
	}))
	t.Cleanup(server.Close)
	t.Setenv(codexReleaseAPIURLEnv, server.URL+"/latest")
	t.Setenv(codexReleaseSHA256EnvForTest, codexTestSHA256(body))
	t.Setenv(codexInstallRootEnv, t.TempDir())

	err := ensureCodexCLIAvailable(context.Background())
	if err == nil {
		t.Fatal("ensureCodexCLIAvailable() error = nil, want untrusted source failure")
	}
	if !strings.Contains(err.Error(), "untrusted Codex release API URL") {
		t.Fatalf("ensureCodexCLIAvailable() error = %v, want untrusted source failure", err)
	}
	if requests.Load() != 0 {
		t.Fatalf("release server received %d requests, want 0", requests.Load())
	}
}

func TestEnsureCodexCLIAvailableRejectsChecksumMismatchWithoutInstalling(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	installRoot := t.TempDir()
	body := codexWheelForTest(t)
	server := newCodexReleaseTestServer(t, codexReleaseTestOptions{
		AssetName: codexReleaseAssetNameForTest(t),
		Body:      body,
	})
	t.Setenv(codexTrustedReleaseMirrorEnvForTest, "1")
	t.Setenv(codexReleaseAPIURLEnv, server.URL+"/latest")
	t.Setenv(codexReleaseSHA256EnvForTest, strings.Repeat("0", 64))
	t.Setenv(codexInstallRootEnv, installRoot)

	err := ensureCodexCLIAvailable(context.Background())
	if err == nil {
		t.Fatal("ensureCodexCLIAvailable() error = nil, want checksum mismatch")
	}
	if !strings.Contains(err.Error(), "checksum mismatch") {
		t.Fatalf("ensureCodexCLIAvailable() error = %v, want checksum mismatch", err)
	}
	if codexPath := filepath.Join(installRoot, "rust-v0.1.0", "codex_cli_bin", "bin", codexExecutableFileName()); isExecutable(codexPath) {
		t.Fatalf("checksum mismatch installed executable codex at %q", codexPath)
	}
}

func newCodexReleaseTestServer(t *testing.T, opts codexReleaseTestOptions) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(newCodexReleaseTestHandler(t, opts))
	t.Cleanup(server.Close)
	return server
}

func newCodexReleaseTestHandler(t *testing.T, opts codexReleaseTestOptions) http.Handler {
	t.Helper()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/latest":
			w.Header().Set("Content-Type", "application/json")
			assets := []map[string]string{}
			if opts.AssetName != "" {
				assets = append(assets, map[string]string{
					"name":                 opts.AssetName,
					"browser_download_url": "http://" + r.Host + "/assets/" + opts.AssetName,
				})
			}
			if err := json.NewEncoder(w).Encode(map[string]any{
				"tag_name": "rust-v0.1.0",
				"assets":   assets,
			}); err != nil {
				t.Fatalf("encode release: %v", err)
			}
		case "/assets/" + opts.AssetName:
			w.Header().Set("Content-Type", "application/octet-stream")
			_, _ = w.Write(opts.Body)
		default:
			http.NotFound(w, r)
		}
	})
}

func setTrustedCodexReleaseMirrorForTest(t *testing.T, releaseAPIURL string, body []byte) {
	t.Helper()
	t.Setenv(codexTrustedReleaseMirrorEnvForTest, "1")
	t.Setenv(codexReleaseAPIURLEnv, releaseAPIURL)
	t.Setenv(codexReleaseSHA256EnvForTest, codexTestSHA256(body))
}

func codexTestSHA256(body []byte) string {
	return fmt.Sprintf("%x", sha256.Sum256(body))
}

func codexTestFileSHA256(t *testing.T, path string) string {
	t.Helper()
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return codexTestSHA256(body)
}

func writeManagedCodexManifestForTest(t *testing.T, target, version, sourceSHA256 string) {
	t.Helper()
	manifest := map[string]any{"codex": map[string]string{
		"path":           filepath.ToSlash(filepath.Join("codex_cli_bin", "bin", codexExecutableFileName())),
		"version":        version,
		"source_sha256":  strings.ToLower(sourceSHA256),
		"package_sha256": codexTestFileSHA256(t, filepath.Join(target, "codex_cli_bin", "bin", codexExecutableFileName())),
	}}
	raw, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		t.Fatalf("marshal managed codex manifest: %v", err)
	}
	if err := os.WriteFile(filepath.Join(target, "codex-manifest.json"), append(raw, '\n'), 0o600); err != nil {
		t.Fatalf("write managed codex manifest: %v", err)
	}
}

func writeBundledCodexManifestForTest(t *testing.T, resourcesRoot, packageSHA256 string) {
	t.Helper()
	if packageSHA256 == "" {
		packageSHA256 = codexTestFileSHA256(t, filepath.Join(resourcesRoot, "bin", codexExecutableFileName()))
	}
	manifest := map[string]any{"codex": map[string]string{
		"path":           filepath.ToSlash(filepath.Join("bin", codexExecutableFileName())),
		"version":        "rust-v9.9.9",
		"source_sha256":  strings.Repeat("d", 64),
		"package_sha256": strings.ToLower(packageSHA256),
	}}
	raw, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		t.Fatalf("marshal bundled codex manifest: %v", err)
	}
	if err := os.WriteFile(filepath.Join(resourcesRoot, codexManagedManifestName), append(raw, '\n'), 0o600); err != nil {
		t.Fatalf("write bundled codex manifest: %v", err)
	}
}

func writeFakeCodex(t *testing.T, path string, supportsAppServer bool) {
	t.Helper()
	if runtime.GOOS == "windows" {
		writeFakeCodexExecutableHelper(t, path, supportsAppServer)
		return
	}
	body := "#!/bin/sh\n"
	if supportsAppServer {
		body += "if [ \"$1\" = \"app-server\" ] && [ \"$2\" = \"--help\" ]; then exit 0; fi\nexit 0\n"
	} else {
		body += "if [ \"$1\" = \"app-server\" ]; then exit 42; fi\nexit 0\n"
	}
	if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
		t.Fatalf("write fake codex: %v", err)
	}
}

func writeFakeCodexWithMarker(t *testing.T, path string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		writeFakeCodexExecutableHelper(t, path, true)
		return
	}
	body := "#!/bin/sh\n" +
		"if [ -n \"$CODEX_FAKE_MARKER\" ]; then printf ran > \"$CODEX_FAKE_MARKER\"; fi\n" +
		"if [ \"$1\" = \"app-server\" ] && [ \"$2\" = \"--help\" ]; then exit 0; fi\n" +
		"exit 0\n"
	if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
		t.Fatalf("write fake codex: %v", err)
	}
}

func writeFakeCodexExecutableHelper(t *testing.T, path string, supportsAppServer bool) {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatalf("locate test executable: %v", err)
	}
	body, err := os.ReadFile(exe)
	if err != nil {
		t.Fatalf("read test executable: %v", err)
	}
	if err := os.WriteFile(path, body, 0o755); err != nil {
		t.Fatalf("write fake codex executable: %v", err)
	}
	t.Setenv(codexFakeHelperEnv, "1")
	if supportsAppServer {
		t.Setenv(codexFakeSupportsAppServerEnv, "1")
	} else {
		t.Setenv(codexFakeSupportsAppServerEnv, "0")
	}
}

func assertExecutableFile(t *testing.T, path string) {
	t.Helper()
	info, err := os.Stat(filepath.Clean(path))
	if err != nil {
		t.Fatalf("stat %q: %v", path, err)
	}
	if info.IsDir() {
		t.Fatalf("%q is a directory, want file", path)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm()&0o111 == 0 {
		t.Fatalf("%q is not executable: mode %s", path, info.Mode())
	}
}
