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
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	lspinstaller "github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/installer"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
)

type expectedLinuxNativeManifest struct {
	version    string
	url        string
	sha256     string
	format     string
	binaryPath string
	launcher   string
}

func TestLinuxNativeArtifactManifestsArePinnedOfficialAmd64Assets(t *testing.T) {
	clangd := expectedLinuxNativeManifest{
		linuxClangdVersion,
		"https://github.com/clangd/clangd/releases/download/22.1.6/clangd-linux-22.1.6.zip",
		"a9c77443af2e447ed467e84771848d3a6ac1c56f84bcfcde717e66318de77cfa",
		lspinstaller.NativeArtifactFormatZip,
		"clangd_22.1.6/bin/clangd",
		"clangd",
	}
	want := map[string]expectedLinuxNativeManifest{
		"proto":     {linuxBufVersion, "https://github.com/bufbuild/buf/releases/download/v1.72.0/buf-Linux-x86_64.tar.gz", "a9c6186cf6fcf062b247345e1b7b12c26f580c1b2a4bbf4d3fe080abf85ceee8", lspinstaller.NativeArtifactFormatTarGz, "buf/bin/buf", "buf"},
		"lua":       {linuxLuaLSVersion, "https://github.com/LuaLS/lua-language-server/releases/download/3.19.1/lua-language-server-3.19.1-linux-x64.tar.gz", "e9235d2d72ef55bc41cf8c99cda2ed64777682024b4bb81f5dea425060c5cbb8", lspinstaller.NativeArtifactFormatTarGz, "bin/lua-language-server", "lua-language-server"},
		"terraform": {linuxTerraformLSVersion, "https://releases.hashicorp.com/terraform-ls/0.39.0/terraform-ls_0.39.0_linux_amd64.zip", "7750edc736845fd8c04ff0fc6332423c12d8275b358668c8c17e8aedc43ef971", lspinstaller.NativeArtifactFormatZip, "terraform-ls", "terraform-ls"},
		"sql":       {linuxSqruffVersion, "https://github.com/quarylabs/sqruff/releases/download/v0.40.0/sqruff-linux-x86_64-musl.tar.gz", "8a377bdfdfaf46483c33cce46d3b4eb46bcec4b9557f6d0106adc85cc926660e", lspinstaller.NativeArtifactFormatTarGz, "sqruff", "sqruff"},
		"dart":      {linuxDartVersion, "https://storage.googleapis.com/dart-archive/channels/stable/release/3.13.0/sdk/dartsdk-linux-x64-release.zip", "87902573facd8acacac7ee1fe73fa8d0668e06065016068e2ed6c5c99c6b1ee0", lspinstaller.NativeArtifactFormatZip, "dart-sdk/bin/dart", "dart"},
		"rust":      {linuxRustAnalyzerVersion, "https://github.com/rust-lang/rust-analyzer/releases/download/2026-08-10.1/rust-analyzer-x86_64-unknown-linux-gnu.gz", "d42908a7dc7b89250ae881a0919e477296843665c98574ecc8fe16ba60cecefb", lspinstaller.NativeArtifactFormatGzip, "rust-analyzer", "rust-analyzer"},
	}
	for _, language := range contract.ClangdLanguageIDs() {
		want[language] = clangd
	}
	manifests := linuxNativeArtifactManifests()
	if len(manifests) != len(want) {
		t.Fatalf("manifest count = %d, want %d", len(manifests), len(want))
	}
	seen := make(map[string]int, len(manifests))
	for _, manifest := range manifests {
		seen[manifest.language]++
		t.Run(manifest.language, func(t *testing.T) {
			assertLinuxNativeManifest(t, manifest, want)
		})
	}
	assertLinuxNativeManifestLanguages(t, want, seen)
}

// assertLinuxNativeManifestLanguages 校验每种语言恰好注册一个清单。
func assertLinuxNativeManifestLanguages(t *testing.T, want map[string]expectedLinuxNativeManifest, seen map[string]int) {
	t.Helper()
	for language := range want {
		if seen[language] != 1 {
			t.Fatalf("manifest language %q appears %d times, want exactly once", language, seen[language])
		}
	}
}

func assertLinuxNativeManifest(t *testing.T, manifest linuxNativeArtifactManifest, want map[string]expectedLinuxNativeManifest) {
	t.Helper()
	expected, ok := want[manifest.language]
	if !ok {
		t.Fatalf("unexpected manifest language %q", manifest.language)
	}
	if manifest.artifact.Version != expected.version || manifest.artifact.URL != expected.url || manifest.artifact.SHA256 != expected.sha256 {
		t.Fatalf("manifest artifact = %#v, want version=%q URL=%q SHA256=%q", manifest.artifact, expected.version, expected.url, expected.sha256)
	}
	if manifest.artifact.Format != expected.format || manifest.artifact.BinaryPath != expected.binaryPath || manifest.artifact.LauncherName != expected.launcher {
		t.Fatalf("manifest layout = %#v, want %#v", manifest.artifact, expected)
	}
	assertLinuxNativeManifestTransport(t, manifest.artifact)
}

// assertLinuxNativeManifestTransport 校验下载地址与摘要格式。
func assertLinuxNativeManifestTransport(t *testing.T, artifact lspinstaller.NativeArtifactSpec) {
	t.Helper()
	if len(artifact.SHA256) != sha256.Size*2 || strings.Trim(artifact.SHA256, "0123456789abcdef") != "" {
		t.Fatalf("manifest SHA256 is not a concrete lowercase digest: %q", artifact.SHA256)
	}
	if strings.Contains(artifact.SHA256, "000000") {
		t.Fatalf("manifest SHA256 is not a concrete lowercase digest: %q", artifact.SHA256)
	}
	if !strings.HasPrefix(artifact.URL, "https://") {
		t.Fatalf("manifest URL is not HTTPS: %q", artifact.URL)
	}
}

func TestLinuxNativeArtifactRegistrationDownloadsThroughFakeTLS(t *testing.T) {
	root := filepath.Join(t.TempDir(), "native-artifacts")
	manifests := linuxNativeArtifactManifests()
	archives := make(map[string][]byte, len(manifests))
	for _, manifest := range manifests {
		content := []byte("#!/bin/sh\nexit 0\n")
		if manifest.language == "sql" {
			content = []byte("#!/bin/sh\necho 'sqruff 0.40.0'\n")
		}
		archives[manifest.language] = buildLinuxNativeTestArchive(t, manifest.artifact, content)
	}
	requests := make(map[string]int, len(archives))
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		language := strings.TrimPrefix(request.URL.Path, "/")
		archive, ok := archives[language]
		if !ok {
			http.NotFound(w, request)
			return
		}
		requests[language]++
		w.Header().Set("Content-Type", "application/octet-stream")
		if _, err := w.Write(archive); err != nil {
			t.Errorf("write fake %s artifact: %v", language, err)
		}
	}))
	t.Cleanup(server.Close)

	manifestCopies := make([]linuxNativeArtifactManifest, 0, len(manifests))
	for _, manifest := range manifests {
		archive := archives[manifest.language]
		manifest.artifact.URL = server.URL + "/" + manifest.language
		manifest.artifact.SHA256 = linuxNativeTestSHA256(archive)
		manifestCopies = append(manifestCopies, manifest)
	}

	provider := lspinstaller.NewProvider()
	if err := registerLinuxNativeArtifactInstallersWithResolver(
		provider,
		func() (string, error) { return root, nil },
		server.Client(),
		manifestCopies...,
	); err != nil {
		t.Fatalf("registerLinuxNativeArtifactInstallersWithResolver: %v", err)
	}
	t.Setenv("PATH", t.TempDir())
	ctx := lspinstaller.WithInstallCommandCapability(context.Background())
	installedArtifacts := make(map[string]struct{}, len(manifestCopies))
	for _, manifest := range manifestCopies {
		installLinuxNativeManifestForTest(t, provider, ctx, root, manifest, installedArtifacts, requests)
	}
}

func installLinuxNativeManifestForTest(t *testing.T, provider *lspinstaller.Provider, ctx context.Context, root string, manifest linuxNativeArtifactManifest, installed map[string]struct{}, requests map[string]int) {
	t.Helper()
	want := filepath.Join(root, manifest.artifact.Name, manifest.artifact.Version, "launcher", manifest.artifact.LauncherName)
	artifactKey := filepath.Join(manifest.artifact.Name, manifest.artifact.Version)
	wantStatus, wantRequests := lspinstaller.InstallStatusInstalledPath, 1
	if _, exists := installed[artifactKey]; exists {
		wantStatus, wantRequests = lspinstaller.InstallStatusPathFound, 0
	} else {
		installed[artifactKey] = struct{}{}
	}
	result, err := provider.EnsureInstalledDetailed(ctx, manifest.language)
	if err != nil || result.Path != want || result.Status != wantStatus {
		t.Fatalf("EnsureInstalledDetailed(%s) = %#v err=%v, want %q %s", manifest.language, result, err, want, wantStatus)
	}
	second, err := provider.EnsureInstalledDetailed(ctx, manifest.language)
	if err != nil || second.Path != want || second.Status != lspinstaller.InstallStatusPathFound {
		t.Fatalf("cached EnsureInstalledDetailed(%s) = %#v err=%v", manifest.language, second, err)
	}
	if requests[manifest.language] != wantRequests {
		t.Fatalf("fake TLS request count for %s = %d, want %d", manifest.language, requests[manifest.language], wantRequests)
	}
}

func TestLinuxNativeArtifactRootResolverFailsWithoutFallback(t *testing.T) {
	wantErr := errors.New("cache unavailable")
	provider := lspinstaller.NewProvider()
	err := registerLinuxNativeArtifactInstallersWithResolver(provider, func() (string, error) {
		return "", wantErr
	}, nil)
	if err == nil || !errors.Is(err, wantErr) {
		t.Fatalf("registerLinuxNativeArtifactInstallersWithResolver error = %v, want cache resolver error", err)
	}
}

func TestResolveLinuxNativeArtifactInstallRootUsesPrivateUserCache(t *testing.T) {
	cacheDir := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", cacheDir)
	root, err := resolveLinuxNativeArtifactInstallRoot()
	if err != nil {
		t.Fatalf("resolveLinuxNativeArtifactInstallRoot: %v", err)
	}
	want := filepath.Join(cacheDir, "super-agent-v3", "mcp-lsp", "native-artifacts")
	if root != want {
		t.Fatalf("resolveLinuxNativeArtifactInstallRoot = %q, want %q", root, want)
	}
}

func buildLinuxNativeTestArchive(t *testing.T, spec lspinstaller.NativeArtifactSpec, content []byte) []byte {
	t.Helper()
	var output bytes.Buffer
	switch spec.Format {
	case lspinstaller.NativeArtifactFormatZip:
		writeLinuxNativeTestZip(t, &output, spec.BinaryPath, content)
	case lspinstaller.NativeArtifactFormatTarGz:
		writeLinuxNativeTestTarGz(t, &output, spec.BinaryPath, content)
	case lspinstaller.NativeArtifactFormatGzip:
		gzipWriter := gzip.NewWriter(&output)
		if _, err := gzipWriter.Write(content); err != nil {
			t.Fatalf("write fake gzip: %v", err)
		}
		if err := gzipWriter.Close(); err != nil {
			t.Fatalf("close fake gzip: %v", err)
		}
	default:
		t.Fatalf("unsupported fake test format %q", spec.Format)
	}
	return output.Bytes()
}

// writeLinuxNativeTestZip 写入单文件 ZIP 测试工件。
func writeLinuxNativeTestZip(t *testing.T, output *bytes.Buffer, path string, content []byte) {
	t.Helper()
	writer := zip.NewWriter(output)
	entry, err := writer.Create(path)
	if err != nil {
		t.Fatalf("create fake ZIP entry: %v", err)
	}
	if _, err := entry.Write(content); err != nil {
		t.Fatalf("write fake ZIP entry: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close fake ZIP: %v", err)
	}
}

// writeLinuxNativeTestTarGz 写入单文件 tar.gz 测试工件。
func writeLinuxNativeTestTarGz(t *testing.T, output *bytes.Buffer, path string, content []byte) {
	t.Helper()
	gzipWriter := gzip.NewWriter(output)
	tarWriter := tar.NewWriter(gzipWriter)
	if err := tarWriter.WriteHeader(&tar.Header{Name: path, Mode: 0o755, Size: int64(len(content)), Typeflag: tar.TypeReg}); err != nil {
		t.Fatalf("create fake tar header: %v", err)
	}
	if _, err := tarWriter.Write(content); err != nil {
		t.Fatalf("write fake tar entry: %v", err)
	}
	if err := tarWriter.Close(); err != nil {
		t.Fatalf("close fake tar: %v", err)
	}
	if err := gzipWriter.Close(); err != nil {
		t.Fatalf("close fake gzip: %v", err)
	}
}

func linuxNativeTestSHA256(payload []byte) string {
	digest := sha256.Sum256(payload)
	return hex.EncodeToString(digest[:])
}
