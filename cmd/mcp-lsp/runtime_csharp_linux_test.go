//go:build linux && amd64

package main

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	lspinstaller "github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/installer"
)

func TestLinuxCSharpManagedManifestIsPinnedOfficialAmd64(t *testing.T) {
	manifest := linuxCSharpManagedManifests()
	assertPinnedLinuxCSharpSpec(t, "runtime", manifest.runtime)
	assertPinnedLinuxCSharpSpec(t, "SDK", manifest.sdk)
	assertPinnedLinuxCSharpSpec(t, "csharp-ls", manifest.server)
	if manifest.runtime.Version != linuxDotnetRuntimeVersion {
		t.Fatalf("runtime version = %q, want %q", manifest.runtime.Version, linuxDotnetRuntimeVersion)
	}
	if manifest.sdk.Version != linuxDotnetSDKVersion || manifest.server.Version != linuxCSharpLSVersion {
		t.Fatalf("manifest versions = runtime=%q SDK=%q csharp=%q", manifest.runtime.Version, manifest.sdk.Version, manifest.server.Version)
	}
	if !strings.Contains(manifest.runtime.URL, "dotnet/Runtime/9.0.4/") || !strings.Contains(manifest.sdk.URL, "dotnet/Sdk/9.0.203/") {
		t.Fatalf("manifest does not use official dotnet-install artifacts: runtime=%q SDK=%q", manifest.runtime.URL, manifest.sdk.URL)
	}
	if !strings.Contains(manifest.server.URL, "api.nuget.org/v3-flatcontainer/csharp-ls/0.20.0/") {
		t.Fatalf("manifest does not use the fixed official NuGet package: %q", manifest.server.URL)
	}
}

func assertPinnedLinuxCSharpSpec(t *testing.T, name string, spec lspinstaller.NativeArtifactSpec) {
	t.Helper()
	if spec.Name == "" || spec.Version == "" || !strings.HasPrefix(spec.URL, "https://") {
		t.Fatalf("%s manifest is incomplete: %#v", name, spec)
	}
	if len(spec.SHA256) != sha256.Size*2 || strings.Trim(spec.SHA256, "0123456789abcdef") != "" {
		t.Fatalf("%s SHA256 is not a concrete lowercase digest: %q", name, spec.SHA256)
	}
	if spec.Format == "" || spec.BinaryPath == "" || spec.LauncherName == "" {
		t.Fatalf("%s manifest has incomplete archive layout: %#v", name, spec)
	}
}

func TestLinuxCSharpManagedInstallerCleanPATHAndReuse(t *testing.T) {
	manifest := linuxCSharpManagedManifests()
	marker := filepath.Join(t.TempDir(), "dotnet-argv")
	fakeDotnet := []byte("#!/bin/sh\nprintf '%s\\n' \"$@\" > \"$FAKE_DOTNET_OUTPUT\"\n")
	runtimeArchive := buildLinuxCSharpTarGz(t, "dotnet", fakeDotnet)
	sdkArchive := buildLinuxCSharpTarGz(t, "dotnet", fakeDotnet)
	serverArchive := buildLinuxCSharpNuGet(t, []byte("managed csharp language server"))
	archives := map[string][]byte{
		"/runtime": runtimeArchive,
		"/sdk":     sdkArchive,
		"/server":  serverArchive,
	}
	server, requests := newLinuxCSharpFixtureServer(t, archives)
	t.Cleanup(server.Close)
	manifest.runtime.URL, manifest.runtime.SHA256 = server.URL+"/runtime", sha256HexLinuxCSharp(runtimeArchive)
	manifest.sdk.URL, manifest.sdk.SHA256 = server.URL+"/sdk", sha256HexLinuxCSharp(sdkArchive)
	manifest.server.URL, manifest.server.SHA256 = server.URL+"/server", sha256HexLinuxCSharp(serverArchive)
	root := filepath.Join(t.TempDir(), "managed-csharp")
	provider := lspinstaller.NewProvider()
	if err := registerLinuxCSharpInstallerWithResolver(provider, func() (string, error) { return root, nil }, server.Client(), manifest); err != nil {
		t.Fatalf("registerLinuxCSharpInstallerWithResolver: %v", err)
	}
	verifyLinuxCSharpManagedInstall(t, provider, manifest, root, marker, requests)
}

func newLinuxCSharpFixtureServer(t *testing.T, archives map[string][]byte) (*httptest.Server, map[string]int) {
	t.Helper()
	requests := make(map[string]int)
	failFirstServer := true
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		archive, ok := archives[r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		requests[r.URL.Path]++
		if r.URL.Path == "/server" && failFirstServer {
			failFirstServer = false
			http.Error(w, "transient package failure", http.StatusBadGateway)
			return
		}
		w.Header().Set("Content-Type", "application/octet-stream")
		if _, err := w.Write(archive); err != nil {
			t.Errorf("write fake %s archive: %v", r.URL.Path, err)
		}
	}))
	return server, requests
}

func verifyLinuxCSharpManagedInstall(t *testing.T, provider *lspinstaller.Provider, manifest linuxCSharpManagedManifest, root, marker string, requests map[string]int) {
	t.Helper()
	emptyPath := filepath.Join(t.TempDir(), "empty-path")
	if err := os.Mkdir(emptyPath, 0o700); err != nil {
		t.Fatalf("create empty PATH: %v", err)
	}
	t.Setenv("PATH", emptyPath)
	ctx := lspinstaller.WithInstallCommandCapability(context.Background())
	if _, err := provider.EnsureInstalledDetailed(ctx, "csharp"); err == nil {
		t.Fatal("first managed C# install unexpectedly succeeded after transient package failure")
	}
	result, err := provider.EnsureInstalledDetailed(ctx, "csharp")
	if err != nil {
		t.Fatalf("cold-start managed C# install: %v", err)
	}
	wantLauncher := filepath.Join(root, manifest.server.Name, manifest.server.Version, "launcher", manifest.server.LauncherName)
	if result.Path != wantLauncher || result.Status != lspinstaller.InstallStatusInstalledPath {
		t.Fatalf("cold-start result = %#v, want managed launcher %q", result, wantLauncher)
	}
	dotnetRoot := filepath.Join(root, manifest.sdk.Name, manifest.sdk.Version, "payload")
	assertLinuxCSharpLauncher(t, result.Path, dotnetRoot)
	command := exec.Command(result.Path, "--stdio")
	command.Env = []string{"PATH=" + emptyPath, "FAKE_DOTNET_OUTPUT=" + marker}
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("run managed csharp launcher with empty PATH: %v; output=%s", err, output)
	}
	assertLinuxCSharpArgv(t, marker)
	second, err := provider.EnsureInstalledDetailed(ctx, "csharp")
	if err != nil {
		t.Fatalf("warm managed C# install: %v", err)
	}
	if second.Path != wantLauncher || second.Status != lspinstaller.InstallStatusPathFound {
		t.Fatalf("warm result = %#v, want path_found at %q", second, wantLauncher)
	}
	assertLinuxCSharpRequestCounts(t, requests)
}

func assertLinuxCSharpLauncher(t *testing.T, path, dotnetRoot string) {
	t.Helper()
	launcher, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read managed C# launcher: %v", err)
	}
	content := string(launcher)
	if !strings.Contains(content, "DOTNET_MULTILEVEL_LOOKUP=0") || !strings.Contains(content, "DOTNET_ROOT='"+dotnetRoot+"'") || strings.Contains(content, "exec dotnet ") {
		t.Fatalf("launcher is not an absolute managed dotnet launcher: %s", launcher)
	}
}

func assertLinuxCSharpArgv(t *testing.T, marker string) {
	t.Helper()
	argv, err := os.ReadFile(marker)
	if err != nil {
		t.Fatalf("read fake dotnet argv: %v", err)
	}
	args := strings.Split(strings.TrimSpace(string(argv)), "\n")
	if len(args) != 2 || !filepath.IsAbs(args[0]) || !strings.HasSuffix(args[0], "CSharpLanguageServer.dll") || args[1] != "--stdio" {
		t.Fatalf("fake dotnet argv = %#v, want absolute CSharpLanguageServer.dll and --stdio", args)
	}
}

func assertLinuxCSharpRequestCounts(t *testing.T, requests map[string]int) {
	t.Helper()
	for path, count := range requests {
		want := 1
		if path == "/server" {
			want = 2
		}
		if count != want {
			t.Fatalf("fake artifact request count for %s = %d, want %d", path, count, want)
		}
	}
}

func buildLinuxCSharpTarGz(t *testing.T, binaryPath string, content []byte) []byte {
	t.Helper()
	var output bytes.Buffer
	gzipWriter := gzip.NewWriter(&output)
	tarWriter := tar.NewWriter(gzipWriter)
	if err := tarWriter.WriteHeader(&tar.Header{Name: "./", Mode: 0o700, Typeflag: tar.TypeDir}); err != nil {
		t.Fatalf("write tar root: %v", err)
	}
	if err := tarWriter.WriteHeader(&tar.Header{Name: "./" + binaryPath, Mode: 0o755, Size: int64(len(content)), Typeflag: tar.TypeReg}); err != nil {
		t.Fatalf("write tar binary header: %v", err)
	}
	if _, err := tarWriter.Write(content); err != nil {
		t.Fatalf("write tar binary: %v", err)
	}
	if err := tarWriter.Close(); err != nil {
		t.Fatalf("close tar: %v", err)
	}
	if err := gzipWriter.Close(); err != nil {
		t.Fatalf("close gzip: %v", err)
	}
	return output.Bytes()
}

func buildLinuxCSharpNuGet(t *testing.T, content []byte) []byte {
	t.Helper()
	var output bytes.Buffer
	writer := zip.NewWriter(&output)
	entry, err := writer.Create("tools/net9.0/any/CSharpLanguageServer.dll")
	if err != nil {
		t.Fatalf("create fake NuGet server entry: %v", err)
	}
	if _, err := entry.Write(content); err != nil {
		t.Fatalf("write fake NuGet server entry: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close fake NuGet package: %v", err)
	}
	return output.Bytes()
}

func sha256HexLinuxCSharp(payload []byte) string {
	digest := sha256.Sum256(payload)
	return hex.EncodeToString(digest[:])
}
