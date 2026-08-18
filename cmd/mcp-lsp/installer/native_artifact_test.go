package installer

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/klauspost/compress/zstd"
)

func TestNativeArtifactInstallerExtractsDebDataTarZstd(t *testing.T) {
	archive := buildDebArtifact(t, []tarArtifactEntry{{name: "usr/lib/libfixture.so", kind: tar.TypeReg, content: []byte("fixture")}})
	server := newTLSArtifactServer(t, archive)
	defer server.Close()
	root := filepath.Join(t.TempDir(), "managed-lsp")
	result, err := mustNativeInstaller(t, root, server.Client()).InstallArtifact(t.Context(), NativeArtifactSpec{
		Name: "native", Version: "1.0.0", URL: server.URL, SHA256: sha256Hex(archive),
		Format: NativeArtifactFormatDeb, BinaryPath: "usr/lib/libfixture.so",
	})
	if err != nil {
		t.Fatalf("InstallArtifact DEB: %v", err)
	}
	content, err := os.ReadFile(result.BinaryPath)
	if err != nil || string(content) != "fixture" {
		t.Fatalf("read DEB payload: content=%q err=%v", content, err)
	}
}

func TestNativeArtifactInstallerRejectsDebTraversal(t *testing.T) {
	archive := buildDebArtifact(t, []tarArtifactEntry{{name: "../outside", kind: tar.TypeReg, content: []byte("escape")}})
	server := newTLSArtifactServer(t, archive)
	defer server.Close()
	root := filepath.Join(t.TempDir(), "managed-lsp")
	_, err := mustNativeInstaller(t, root, server.Client()).InstallArtifact(t.Context(), NativeArtifactSpec{
		Name: "native", Version: "1.0.0", URL: server.URL, SHA256: sha256Hex(archive),
		Format: NativeArtifactFormatDeb, BinaryPath: "usr/lib/libfixture.so",
	})
	if err == nil || !strings.Contains(err.Error(), "unsafe") {
		t.Fatalf("DEB traversal error = %v, want unsafe path rejection", err)
	}
	assertNoPublishedInstall(t, filepath.Join(root, "native", "1.0.0"))
}

func TestNativeArtifactInstallerInstallsHTTPSZipWithManagedLauncher(t *testing.T) {
	archive := buildZipArtifact(t, []zipArtifactEntry{
		{name: "bin/native-lsp", content: []byte("#!/bin/sh\nexit 0\n")},
	})
	server := newTLSArtifactServer(t, archive)
	defer server.Close()
	root := filepath.Join(t.TempDir(), "managed-lsp")
	installer, err := NewNativeArtifactInstaller(NativeArtifactInstallerConfig{
		InstallRoot: root,
		HTTPClient:  server.Client(),
	})
	if err != nil {
		t.Fatalf("NewNativeArtifactInstaller: %v", err)
	}
	result, err := installer.InstallArtifact(t.Context(), NativeArtifactSpec{
		Name: "native", Version: "1.0.0", URL: server.URL,
		SHA256: sha256Hex(archive), Format: NativeArtifactFormatZip,
		BinaryPath: "bin/native-lsp", LauncherName: "native-lsp",
	})
	if err != nil {
		t.Fatalf("InstallArtifact: %v", err)
	}
	assertNativeInstallPaths(t, result, root)
	assertExecutable(t, result.BinaryPath)
	assertExecutable(t, result.LauncherPath)
	launcher, err := os.ReadFile(result.LauncherPath)
	if err != nil {
		t.Fatalf("read managed launcher: %v", err)
	}
	if !strings.Contains(string(launcher), shellQuote(result.BinaryPath)) {
		t.Fatalf("managed launcher does not contain absolute target %q: %s", result.BinaryPath, launcher)
	}
	assertNoStagingDirectories(t, filepath.Dir(filepath.Dir(result.InstallDir)))
}

func TestNativeArtifactInstallerReusesCompletePublishedArtifact(t *testing.T) {
	archive := buildZipArtifact(t, []zipArtifactEntry{
		{name: "bin/native-lsp", content: []byte("binary")},
	})
	var requests atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		w.Header().Set("Content-Type", "application/octet-stream")
		if _, err := io.Copy(w, bytes.NewReader(archive)); err != nil {
			t.Errorf("write fake artifact: %v", err)
		}
	}))
	defer server.Close()
	root := filepath.Join(t.TempDir(), "managed-lsp")
	installer := mustNativeInstaller(t, root, server.Client())
	spec := validNativeSpec(server.URL, archive, NativeArtifactFormatZip)
	spec.LauncherName = "native-lsp"

	first, err := installer.InstallArtifact(t.Context(), spec)
	if err != nil {
		t.Fatalf("first InstallArtifact: %v", err)
	}
	second, err := installer.InstallArtifact(t.Context(), spec)
	if err != nil {
		t.Fatalf("second InstallArtifact: %v", err)
	}
	if got := requests.Load(); got != 1 {
		t.Fatalf("artifact requests = %d, want exactly one request", got)
	}
	if second != first {
		t.Fatalf("reused result = %#v, first result = %#v", second, first)
	}
	assertExecutable(t, second.BinaryPath)
	assertExecutable(t, second.LauncherPath)
}

func TestNativeArtifactInstallerRejectsHTTPAndInvalidRoot(t *testing.T) {
	root := filepath.Join(t.TempDir(), "managed-lsp")
	if _, err := NewNativeArtifactInstaller(NativeArtifactInstallerConfig{InstallRoot: "relative"}); err == nil {
		t.Fatal("relative install root was accepted")
	}
	installer, err := NewNativeArtifactInstaller(NativeArtifactInstallerConfig{InstallRoot: root})
	if err != nil {
		t.Fatalf("NewNativeArtifactInstaller: %v", err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "must not be requested")
	}))
	defer server.Close()
	_, err = installer.Install(t.Context(), NativeArtifactSpec{
		Name: "native", Version: "1.0.0", URL: server.URL,
		SHA256: strings.Repeat("0", 64), Format: NativeArtifactFormatZip,
		BinaryPath: "bin/native-lsp",
	})
	if err == nil || !strings.Contains(err.Error(), "HTTPS") {
		t.Fatalf("HTTP artifact URL error = %v, want HTTPS rejection", err)
	}
}

func TestNativeArtifactInstallerRejectsSHA256MismatchWithoutPublishing(t *testing.T) {
	archive := buildZipArtifact(t, []zipArtifactEntry{
		{name: "bin/native-lsp", content: []byte("binary")},
	})
	server := newTLSArtifactServer(t, archive)
	defer server.Close()
	root := filepath.Join(t.TempDir(), "managed-lsp")
	installer := mustNativeInstaller(t, root, server.Client())
	_, err := installer.Install(t.Context(), NativeArtifactSpec{
		Name: "native", Version: "1.0.0", URL: server.URL,
		SHA256: strings.Repeat("0", 64), Format: NativeArtifactFormatZip,
		BinaryPath: "bin/native-lsp",
	})
	if err == nil || !strings.Contains(err.Error(), "SHA256") {
		t.Fatalf("SHA256 mismatch error = %v", err)
	}
	assertNoPublishedInstall(t, filepath.Join(root, "native", "1.0.0"))
	assertNoStagingDirectories(t, filepath.Join(root, "native"))
}

func TestNativeArtifactInstallerRejectsZipPathTraversal(t *testing.T) {
	archive := buildZipArtifact(t, []zipArtifactEntry{
		{name: "../outside", content: []byte("escape")},
	})
	server := newTLSArtifactServer(t, archive)
	defer server.Close()
	root := filepath.Join(t.TempDir(), "managed-lsp")
	installer := mustNativeInstaller(t, root, server.Client())
	_, err := installer.Install(t.Context(), validNativeSpec(server.URL, archive, NativeArtifactFormatZip))
	if err == nil || !strings.Contains(err.Error(), "unsafe") {
		t.Fatalf("ZIP traversal error = %v", err)
	}
	assertNoPublishedInstall(t, filepath.Join(root, "native", "1.0.0"))
	assertNoStagingDirectories(t, filepath.Join(root, "native"))
}

func TestNativeArtifactInstallerRejectsTarPathTraversalAndSymlink(t *testing.T) {
	cases := []struct {
		name    string
		archive []byte
		want    string
	}{
		{name: "traversal", archive: buildTarArtifact(t, []tarArtifactEntry{
			{name: "../../outside", kind: tar.TypeReg, content: []byte("escape")},
		}), want: "unsafe"},
		{name: "symlink", archive: buildTarArtifact(t, []tarArtifactEntry{
			{name: "bin/native-lsp", kind: tar.TypeSymlink, link: "/etc/passwd"},
		}), want: "unsupported type"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			server := newTLSArtifactServer(t, tc.archive)
			defer server.Close()
			root := filepath.Join(t.TempDir(), "managed-lsp")
			installer := mustNativeInstaller(t, root, server.Client())
			_, err := installer.Install(t.Context(), validNativeSpec(server.URL, tc.archive, NativeArtifactFormatTar))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("tar rejection error = %v, want %q", err, tc.want)
			}
			assertNoPublishedInstall(t, filepath.Join(root, "native", "1.0.0"))
		})
	}
}

func TestNativeArtifactInstallerInstallsTarGz(t *testing.T) {
	archive := buildTarGzArtifact(t, []tarArtifactEntry{
		{name: "native-lsp", kind: tar.TypeReg, content: []byte("binary")},
	})
	server := newTLSArtifactServer(t, archive)
	defer server.Close()
	root := filepath.Join(t.TempDir(), "managed-lsp")
	installer := mustNativeInstaller(t, root, server.Client())
	result, err := installer.Install(t.Context(), NativeArtifactSpec{
		Name: "native", Version: "1.0.0", URL: server.URL,
		SHA256: sha256Hex(archive), Format: NativeArtifactFormatTarGz,
		BinaryPath: "native-lsp",
	})
	if err != nil {
		t.Fatalf("Install tar.gz artifact: %v", err)
	}
	assertExecutable(t, result.BinaryPath)
	assertExecutable(t, result.LauncherPath)
}

func TestNativeArtifactInstallerSkipsPOSIXTarMetadata(t *testing.T) {
	var archive bytes.Buffer
	gzipWriter := gzip.NewWriter(&archive)
	tarWriter := tar.NewWriter(gzipWriter)
	header := &tar.Header{
		Name: "bin/native-lsp", Typeflag: tar.TypeReg, Mode: 0o755,
		Size: 6, PAXRecords: map[string]string{"comment": "managed-artifact-test"},
	}
	if err := tarWriter.WriteHeader(header); err != nil {
		t.Fatalf("write PAX tar header: %v", err)
	}
	if _, err := tarWriter.Write([]byte("binary")); err != nil {
		t.Fatalf("write PAX tar payload: %v", err)
	}
	if err := tarWriter.Close(); err != nil {
		t.Fatalf("close PAX tar: %v", err)
	}
	if err := gzipWriter.Close(); err != nil {
		t.Fatalf("close PAX gzip: %v", err)
	}
	server := newTLSArtifactServer(t, archive.Bytes())
	defer server.Close()
	root := filepath.Join(t.TempDir(), "managed-lsp")
	installer := mustNativeInstaller(t, root, server.Client())
	result, err := installer.Install(t.Context(), NativeArtifactSpec{
		Name: "native", Version: "pax", URL: server.URL,
		SHA256: sha256Hex(archive.Bytes()), Format: NativeArtifactFormatTarGz,
		BinaryPath: "bin/native-lsp",
	})
	if err != nil {
		t.Fatalf("Install PAX tar.gz artifact: %v", err)
	}
	assertExecutable(t, result.BinaryPath)
}

func TestNativeArtifactInstallerInstallsGzipSingleBinary(t *testing.T) {
	archive := buildGzipArtifact(t, []byte("binary"))
	server := newTLSArtifactServer(t, archive)
	defer server.Close()
	root := filepath.Join(t.TempDir(), "managed-lsp")
	installer := mustNativeInstaller(t, root, server.Client())
	result, err := installer.Install(t.Context(), NativeArtifactSpec{
		Name: "rust-analyzer", Version: "1.0.0", URL: server.URL,
		SHA256: sha256Hex(archive), Format: NativeArtifactFormatGzip,
		BinaryPath: "rust-analyzer", LauncherName: "rust-analyzer",
	})
	if err != nil {
		t.Fatalf("Install gzip artifact: %v", err)
	}
	assertExecutable(t, result.BinaryPath)
	assertExecutable(t, result.LauncherPath)
	content, err := os.ReadFile(result.BinaryPath)
	if err != nil {
		t.Fatalf("read extracted gzip binary: %v", err)
	}
	if string(content) != "binary" {
		t.Fatalf("extracted gzip binary = %q, want binary", content)
	}
}

func validNativeSpec(url string, archive []byte, format string) NativeArtifactSpec {
	return NativeArtifactSpec{
		Name: "native", Version: "1.0.0", URL: url,
		SHA256: sha256Hex(archive), Format: format, BinaryPath: "bin/native-lsp",
	}
}

func mustNativeInstaller(t *testing.T, root string, client *http.Client) *NativeArtifactInstaller {
	t.Helper()
	installer, err := NewNativeArtifactInstaller(NativeArtifactInstallerConfig{
		InstallRoot: root, HTTPClient: client,
	})
	if err != nil {
		t.Fatalf("NewNativeArtifactInstaller: %v", err)
	}
	return installer
}

func newTLSArtifactServer(t *testing.T, archive []byte) *httptest.Server {
	t.Helper()
	return httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/octet-stream")
		if _, err := io.Copy(w, bytes.NewReader(archive)); err != nil {
			t.Errorf("write fake artifact: %v", err)
		}
	}))
}

type zipArtifactEntry struct {
	name    string
	content []byte
}

func buildZipArtifact(t *testing.T, entries []zipArtifactEntry) []byte {
	t.Helper()
	var output bytes.Buffer
	writer := zip.NewWriter(&output)
	for _, entry := range entries {
		header := &zip.FileHeader{Name: entry.name, Method: zip.Store}
		header.SetMode(0o644)
		file, err := writer.CreateHeader(header)
		if err != nil {
			t.Fatalf("create ZIP entry %q: %v", entry.name, err)
		}
		if _, err := file.Write(entry.content); err != nil {
			t.Fatalf("write ZIP entry %q: %v", entry.name, err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close ZIP artifact: %v", err)
	}
	return output.Bytes()
}

type tarArtifactEntry struct {
	name    string
	kind    byte
	link    string
	content []byte
}

func buildTarArtifact(t *testing.T, entries []tarArtifactEntry) []byte {
	t.Helper()
	var output bytes.Buffer
	writer := tar.NewWriter(&output)
	for _, entry := range entries {
		header := &tar.Header{
			Name: entry.name, Typeflag: entry.kind,
			Mode: 0o644, Size: int64(len(entry.content)), Linkname: entry.link,
		}
		if entry.kind == tar.TypeSymlink {
			header.Size = 0
		}
		if err := writer.WriteHeader(header); err != nil {
			t.Fatalf("create tar entry %q: %v", entry.name, err)
		}
		if entry.kind == tar.TypeReg {
			if _, err := writer.Write(entry.content); err != nil {
				t.Fatalf("write tar entry %q: %v", entry.name, err)
			}
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close tar artifact: %v", err)
	}
	return output.Bytes()
}

func buildTarGzArtifact(t *testing.T, entries []tarArtifactEntry) []byte {
	t.Helper()
	var output bytes.Buffer
	gzipWriter := gzip.NewWriter(&output)
	tarWriter := tar.NewWriter(gzipWriter)
	for _, entry := range entries {
		header := &tar.Header{
			Name: entry.name, Typeflag: entry.kind,
			Mode: 0o644, Size: int64(len(entry.content)), Linkname: entry.link,
		}
		if err := tarWriter.WriteHeader(header); err != nil {
			t.Fatalf("create tar.gz entry %q: %v", entry.name, err)
		}
		if entry.kind == tar.TypeReg {
			if _, err := tarWriter.Write(entry.content); err != nil {
				t.Fatalf("write tar.gz entry %q: %v", entry.name, err)
			}
		}
	}
	if err := tarWriter.Close(); err != nil {
		t.Fatalf("close tar.gz tar writer: %v", err)
	}
	if err := gzipWriter.Close(); err != nil {
		t.Fatalf("close tar.gz gzip writer: %v", err)
	}
	return output.Bytes()
}

func buildGzipArtifact(t *testing.T, content []byte) []byte {
	t.Helper()
	var output bytes.Buffer
	writer := gzip.NewWriter(&output)
	if _, err := writer.Write(content); err != nil {
		t.Fatalf("write gzip artifact: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close gzip artifact: %v", err)
	}
	return output.Bytes()
}

func buildDebArtifact(t *testing.T, entries []tarArtifactEntry) []byte {
	t.Helper()
	tarPayload := buildTarArtifact(t, entries)
	var compressed bytes.Buffer
	encoder, err := zstd.NewWriter(&compressed)
	if err != nil {
		t.Fatalf("create zstd writer: %v", err)
	}
	if _, err := encoder.Write(tarPayload); err != nil {
		t.Fatalf("write zstd payload: %v", err)
	}
	if err := encoder.Close(); err != nil {
		t.Fatalf("close zstd writer: %v", err)
	}
	var archive bytes.Buffer
	archive.WriteString("!<arch>\n")
	writeArEntry := func(name string, payload []byte) {
		header := []byte(fmt.Sprintf("%-16s%-12d%-6d%-6d%-8o%-10d`\n", name+"/", 0, 0, 0, 0o100644, len(payload)))
		if len(header) != 60 {
			t.Fatalf("ar header length = %d, want 60", len(header))
		}
		archive.Write(header)
		archive.Write(payload)
		if len(payload)%2 != 0 {
			archive.WriteByte('\n')
		}
	}
	writeArEntry("debian-binary", []byte("2.0\n"))
	writeArEntry("data.tar.zst", compressed.Bytes())
	return archive.Bytes()
}

func sha256Hex(payload []byte) string {
	digest := sha256.Sum256(payload)
	return hex.EncodeToString(digest[:])
}

func assertNativeInstallPaths(t *testing.T, result NativeInstallResult, root string) {
	t.Helper()
	if !filepath.IsAbs(result.InstallDir) || !filepath.IsAbs(result.BinaryPath) || !filepath.IsAbs(result.LauncherPath) {
		t.Fatalf("install result contains non-absolute path: %#v", result)
	}
	wantDir := filepath.Join(root, "native", "1.0.0")
	if result.InstallDir != wantDir {
		t.Fatalf("InstallDir = %q, want %q", result.InstallDir, wantDir)
	}
	if _, err := os.Stat(result.BinaryPath); err != nil {
		t.Fatalf("stat installed binary: %v", err)
	}
	if _, err := os.Stat(result.LauncherPath); err != nil {
		t.Fatalf("stat managed launcher: %v", err)
	}
}

func assertNoPublishedInstall(t *testing.T, filename string) {
	t.Helper()
	if _, err := os.Lstat(filename); !os.IsNotExist(err) {
		t.Fatalf("unexpected published install %s, err=%v", filename, err)
	}
}

func assertNoStagingDirectories(t *testing.T, parent string) {
	t.Helper()
	entries, err := os.ReadDir(parent)
	if os.IsNotExist(err) {
		return
	}
	if err != nil {
		t.Fatalf("read staging parent %s: %v", parent, err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".native-artifact-") {
			t.Fatalf("staging directory remains after failed install: %s", entry.Name())
		}
	}
}
