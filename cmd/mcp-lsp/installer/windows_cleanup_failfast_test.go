package installer

// 本测试故意不加 windows build tag：它用可移植故障注入验证 cleanup fail-fast
// 语义，不执行 Win32 API；Windows ACL/reparse 行为由带标签测试覆盖。

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type cleanupFaultFile struct {
	bytes.Buffer
	name     string
	closeErr error
}

func (f *cleanupFaultFile) Name() string { return f.name }

func (f *cleanupFaultFile) Chmod(os.FileMode) error { return nil }

func (f *cleanupFaultFile) Close() error { return f.closeErr }

type cleanupFaultBody struct {
	*bytes.Reader
	closeErr error
}

func (b *cleanupFaultBody) Close() error { return b.closeErr }

type cleanupFaultReader struct{ err error }

func (r cleanupFaultReader) Read([]byte) (int, error) { return 0, r.err }

func TestWindowsInstallerDownloadReportsResponseBodyCloseError(t *testing.T) {
	closeErr := errors.New("injected response body close failure")
	payload := []byte("download-payload")
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode:    http.StatusOK,
			Status:        "200 OK",
			Body:          &cleanupFaultBody{Reader: bytes.NewReader(payload), closeErr: closeErr},
			ContentLength: int64(len(payload)),
			Header:        make(http.Header),
			Request:       &http.Request{},
		}, nil
	})}
	root := t.TempDir()
	cache, err := NewWindowsAssetCache(root, client)
	if err != nil {
		t.Fatal(err)
	}
	destination := filepath.Join(root, "payload")
	asset := WindowsLockedAsset{URL: "https://example.invalid/payload", SHA256: sha256Hex(payload)}
	err = cache.downloadPayload(context.Background(), destination, asset)
	if !errors.Is(err, closeErr) {
		t.Fatalf("downloadPayload() error = %v, want response body close error", err)
	}
}

func TestWindowsInstallerDownloadJoinsReadAndResponseBodyCloseErrors(t *testing.T) {
	readErr := errors.New("injected response body read failure")
	closeErr := errors.New("injected response body close failure")
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Status:     "200 OK",
			Body:       readCloseBody{Reader: cleanupFaultReader{err: readErr}, closeErr: closeErr},
			Header:     make(http.Header),
			Request:    &http.Request{},
		}, nil
	})}
	root := t.TempDir()
	cache, err := NewWindowsAssetCache(root, client)
	if err != nil {
		t.Fatal(err)
	}
	err = cache.downloadPayload(context.Background(), filepath.Join(root, "payload"), WindowsLockedAsset{SHA256: strings.Repeat("0", 64)})
	if !errors.Is(err, readErr) || !errors.Is(err, closeErr) {
		t.Fatalf("downloadPayload() error = %v, want both read and close errors", err)
	}
}

type readCloseBody struct {
	io.Reader
	closeErr error
}

func (b readCloseBody) Close() error { return b.closeErr }

func TestWindowsInstallerDownloadReportsTemporaryCloseError(t *testing.T) {
	closeErr := errors.New("injected download temporary close failure")
	payload := []byte("download-payload")
	root := t.TempDir()
	cache, err := NewWindowsAssetCache(root, &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode:    http.StatusOK,
			Status:        "200 OK",
			Body:          io.NopCloser(bytes.NewReader(payload)),
			ContentLength: int64(len(payload)),
			Header:        make(http.Header),
			Request:       &http.Request{},
		}, nil
	})})
	if err != nil {
		t.Fatal(err)
	}
	originalCreateTemp := createWindowsInstallerTemp
	createWindowsInstallerTemp = func(dir, pattern string) (windowsInstallerFile, error) {
		return &cleanupFaultFile{name: filepath.Join(dir, pattern+"injected"), closeErr: closeErr}, nil
	}
	t.Cleanup(func() { createWindowsInstallerTemp = originalCreateTemp })
	err = cache.downloadPayload(context.Background(), filepath.Join(root, "payload"), WindowsLockedAsset{SHA256: sha256Hex(payload)})
	if !errors.Is(err, closeErr) {
		t.Fatalf("downloadPayload() error = %v, want temporary close error", err)
	}
}

func TestWindowsInstallerEnsureReportsTemporaryReadyRemoveAllError(t *testing.T) {
	removeErr := errors.New("injected temporary ready remove failure")
	payload := testZipArchive(t, "other.exe", []byte("not-the-requested-binary"))
	server := newTestAssetServer(payload)
	defer server.Close()
	manifest := testLockedManifestWithURL(t, server.URL+"/asset.zip", WindowsLockedAsset{
		Architecture: WindowsHostArchX64,
		Version:      "cleanup-ready-test",
		Format:       WindowsLockedAssetFormatZip,
		BinaryPath:   "wanted.exe",
		SHA256:       sha256Hex(payload),
	})
	cache, err := NewWindowsAssetCache(t.TempDir(), server.Client())
	if err != nil {
		t.Fatal(err)
	}
	originalRemoveAll := removeWindowsInstallerAll
	removeWindowsInstallerAll = func(path string) error {
		if strings.Contains(filepath.Base(path), ".ready-") {
			return removeErr
		}
		return originalRemoveAll(path)
	}
	t.Cleanup(func() { removeWindowsInstallerAll = originalRemoveAll })
	_, err = cache.ensureForArchitecture(context.Background(), manifest, WindowsHostArchX64)
	if !errors.Is(err, removeErr) {
		t.Fatalf("ensureForArchitecture() error = %v, want temporary ready cleanup error", err)
	}
}

func TestWindowsInstallerPublishedReadyCleanupOnPathResolutionFailure(t *testing.T) {
	pathErr := errors.New("injected published ready path resolution failure")
	payload := []byte("published-raw-binary")
	server := newTestAssetServer(payload)
	defer server.Close()
	asset := WindowsLockedAsset{
		Architecture: WindowsHostArchX64,
		Version:      "published-ready-path-test",
		Format:       WindowsLockedAssetFormatRaw,
		BinaryPath:   "server.exe",
		SHA256:       sha256Hex(payload),
	}
	manifest := testLockedManifestWithURL(t, server.URL+"/server.exe", asset)
	cache, err := NewWindowsAssetCache(t.TempDir(), server.Client())
	if err != nil {
		t.Fatal(err)
	}
	originalRel := relWindowsInstallerPath
	relWindowsInstallerPath = func(string, string) (string, error) { return "", pathErr }
	t.Cleanup(func() { relWindowsInstallerPath = originalRel })
	result, err := cache.ensureForArchitecture(context.Background(), manifest, WindowsHostArchX64)
	if !errors.Is(err, pathErr) {
		t.Fatalf("ensureForArchitecture() error = %v, want published path error", err)
	}
	if result != "" {
		t.Fatalf("ensureForArchitecture() result = %q, want cleared result after publish failure", result)
	}
	readyDir := filepath.Join(cache.Root(), cacheSegment(manifest.Name), cacheSegment(asset.Version), WindowsHostArchX64, strings.ToLower(asset.SHA256), "ready")
	if _, statErr := os.Lstat(readyDir); !os.IsNotExist(statErr) {
		t.Fatalf("published ready directory = %q, want removed; stat error = %v", readyDir, statErr)
	}
}

func TestWindowsInstallerPublishedReadyCleanupJoinsRemoveAllError(t *testing.T) {
	pathErr := errors.New("injected published ready path resolution failure")
	removeErr := errors.New("injected published ready remove failure")
	payload := []byte("published-raw-binary")
	server := newTestAssetServer(payload)
	defer server.Close()
	asset := WindowsLockedAsset{
		Architecture: WindowsHostArchX64,
		Version:      "published-ready-remove-test",
		Format:       WindowsLockedAssetFormatRaw,
		BinaryPath:   "server.exe",
		SHA256:       sha256Hex(payload),
	}
	manifest := testLockedManifestWithURL(t, server.URL+"/server.exe", asset)
	cache, err := NewWindowsAssetCache(t.TempDir(), server.Client())
	if err != nil {
		t.Fatal(err)
	}
	originalRel := relWindowsInstallerPath
	originalRemoveAll := removeWindowsInstallerAll
	relWindowsInstallerPath = func(string, string) (string, error) { return "", pathErr }
	removeWindowsInstallerAll = func(path string) error {
		if filepath.Base(path) == "ready" {
			return removeErr
		}
		return originalRemoveAll(path)
	}
	t.Cleanup(func() {
		relWindowsInstallerPath = originalRel
		removeWindowsInstallerAll = originalRemoveAll
	})
	result, err := cache.ensureForArchitecture(context.Background(), manifest, WindowsHostArchX64)
	if !errors.Is(err, pathErr) || !errors.Is(err, removeErr) {
		t.Fatalf("ensureForArchitecture() error = %v, want path and published cleanup errors", err)
	}
	if result != "" {
		t.Fatalf("ensureForArchitecture() result = %q, want cleared result after cleanup failure", result)
	}
}

func TestWindowsInstallerVerifyReportsTemporaryRootRemoveAllError(t *testing.T) {
	removeErr := errors.New("injected verification root remove failure")
	payload := testZipArchive(t, "server.exe", []byte("archive-binary"))
	root := t.TempDir()
	payloadPath := filepath.Join(root, "payload.zip")
	if err := os.WriteFile(payloadPath, payload, 0o600); err != nil {
		t.Fatal(err)
	}
	readyDir := filepath.Join(root, "ready")
	if err := os.MkdirAll(readyDir, 0o700); err != nil {
		t.Fatal(err)
	}
	asset := WindowsLockedAsset{Format: WindowsLockedAssetFormatZip, BinaryPath: "server.exe", SHA256: sha256Hex(payload)}
	if err := extractZipAsset(payloadPath, readyDir, asset.BinaryPath, 1<<20); err != nil {
		t.Fatal(err)
	}
	originalRemoveAll := removeWindowsInstallerAll
	removeWindowsInstallerAll = func(path string) error {
		if strings.Contains(filepath.Base(path), ".verify-") {
			return removeErr
		}
		return originalRemoveAll(path)
	}
	t.Cleanup(func() { removeWindowsInstallerAll = originalRemoveAll })
	verifiedPath, verified, err := (&WindowsAssetCache{root: root, maxArchiveBytes: 1 << 20}).verifyReadyAsset(context.Background(), payloadPath, readyDir, asset)
	if !errors.Is(err, removeErr) {
		t.Fatalf("verifyReadyAsset() error = %v, want verification cleanup error", err)
	}
	if verifiedPath != "" || verified {
		t.Fatalf("verifyReadyAsset() result = (%q, %v), want cleared result after cleanup failure", verifiedPath, verified)
	}
}

func TestWindowsInstallerRemoveAllCheckedRefusesRoot(t *testing.T) {
	root := t.TempDir()
	sentinel := filepath.Join(root, "sentinel")
	if err := os.WriteFile(sentinel, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := removeWindowsInstallerAllChecked(root, root); err == nil {
		t.Fatal("removeWindowsInstallerAllChecked() accepted the cache root")
	}
	if _, err := os.Stat(sentinel); err != nil {
		t.Fatalf("cache root sentinel was removed: %v", err)
	}
}

func TestWindowsInstallerArchiveReportsOutputCloseError(t *testing.T) {
	closeErr := errors.New("injected archive output close failure")
	payload := testZipArchive(t, "server.exe", []byte("archive-binary"))
	payloadPath := filepath.Join(t.TempDir(), "payload.zip")
	if err := os.WriteFile(payloadPath, payload, 0o600); err != nil {
		t.Fatal(err)
	}
	originalOpenOutput := openWindowsInstallerOutput
	openWindowsInstallerOutput = func(name string, _ int, _ os.FileMode) (windowsInstallerFile, error) {
		return &cleanupFaultFile{name: name, closeErr: closeErr}, nil
	}
	t.Cleanup(func() { openWindowsInstallerOutput = originalOpenOutput })
	err := extractZipAsset(payloadPath, t.TempDir(), "server.exe", 1<<20)
	if !errors.Is(err, closeErr) {
		t.Fatalf("extractZipAsset() error = %v, want output close error", err)
	}
}

func TestWindowsInstallerCachedPayloadReportsInputCloseError(t *testing.T) {
	closeErr := errors.New("injected cached payload close failure")
	payload := []byte("cached-payload")
	path := filepath.Join(t.TempDir(), "payload")
	if err := os.WriteFile(path, payload, 0o600); err != nil {
		t.Fatal(err)
	}
	originalOpenInput := openWindowsInstallerInput
	openWindowsInstallerInput = func(name string) (windowsInstallerFile, error) {
		return &cleanupFaultFile{Buffer: *bytes.NewBuffer(payload), name: name, closeErr: closeErr}, nil
	}
	t.Cleanup(func() { openWindowsInstallerInput = originalOpenInput })
	valid, err := verifyAssetPayload(path, sha256Hex(payload))
	if !errors.Is(err, closeErr) {
		t.Fatalf("verifyAssetPayload() error = %v, want input close error", err)
	}
	if valid {
		t.Fatal("verifyAssetPayload() reported valid after input close failure")
	}
}

func TestWindowsInstallerFileSHA256ReportsInputCloseError(t *testing.T) {
	closeErr := errors.New("injected hash input close failure")
	path := filepath.Join(t.TempDir(), "payload")
	if err := os.WriteFile(path, []byte("hash-payload"), 0o600); err != nil {
		t.Fatal(err)
	}
	originalOpenInput := openWindowsInstallerInput
	openWindowsInstallerInput = func(name string) (windowsInstallerFile, error) {
		return &cleanupFaultFile{Buffer: *bytes.NewBufferString("hash-payload"), name: name, closeErr: closeErr}, nil
	}
	t.Cleanup(func() { openWindowsInstallerInput = originalOpenInput })
	hash, err := fileSHA256(path)
	if !errors.Is(err, closeErr) {
		t.Fatalf("fileSHA256() error = %v, want input close error", err)
	}
	if hash != "" {
		t.Fatalf("fileSHA256() hash = %q, want cleared hash after input close failure", hash)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) { return f(request) }

func newTestAssetServer(payload []byte) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		_, _ = writer.Write(payload)
	}))
}

var _ io.ReadCloser = (*cleanupFaultBody)(nil)
