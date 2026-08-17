package installer

// 本测试故意不加 windows build tag：它验证共享缓存/归档契约在所有平台保持一致，
// 不启动 Windows 可执行文件，也不把非 Windows 结果计作 Windows 原生 E2E。

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ulikunitz/xz"
)

func TestSnapshotAssetTreeContextCancellation(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "payload.bin")
	if err := os.WriteFile(path, []byte("context-aware hash"), 0o600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := snapshotAssetTreeContext(ctx, root); !errors.Is(err, context.Canceled) {
		t.Fatalf("snapshotAssetTreeContext() error = %v, want context.Canceled", err)
	}
	if _, err := fileSHA256Context(ctx, path); !errors.Is(err, context.Canceled) {
		t.Fatalf("fileSHA256Context() error = %v, want context.Canceled", err)
	}
}

func TestContextArchiveReaderCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	started := make(chan struct{})
	reader := contextArchiveReader{ctx: ctx, input: readerFunc(func([]byte) (int, error) {
		close(started)
		<-ctx.Done()
		return 0, ctx.Err()
	})}
	done := make(chan error, 1)
	go func() {
		buffer := make([]byte, 32*1024)
		_, err := reader.Read(buffer)
		done <- err
	}()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("context archive reader did not enter the underlying read")
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("context archive reader error = %v, want context.Canceled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("context archive reader did not return after cancellation")
	}
}

type readerFunc func([]byte) (int, error)

func (f readerFunc) Read(p []byte) (int, error) { return f(p) }

func TestAssetTreesEqualStillValidatesFullTree(t *testing.T) {
	left := t.TempDir()
	right := t.TempDir()
	for _, root := range []string{left, right} {
		if err := os.WriteFile(filepath.Join(root, "critical.bin"), []byte("same"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, "non-swift-companion.dll"), []byte("same companion"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	equal, err := assetTreesEqual(left, right)
	if err != nil || !equal {
		t.Fatalf("assetTreesEqual() equal result=%v error=%v, want true/nil", equal, err)
	}
	if err := os.WriteFile(filepath.Join(right, "non-swift-companion.dll"), []byte("tampered"), 0o600); err != nil {
		t.Fatal(err)
	}
	equal, err = assetTreesEqual(left, right)
	if err != nil {
		t.Fatalf("assetTreesEqual() tampered error = %v", err)
	}
	if equal {
		t.Fatal("assetTreesEqual() accepted tampered non-Swift companion")
	}
}

func TestSelectLockedAssetUsesNativeArchitectureAndWindowsBuild(t *testing.T) {
	manifest := testLockedManifest(t, WindowsLockedAsset{
		Architecture:      WindowsHostArchX64,
		Version:           "clangd-18.1.8",
		Format:            WindowsLockedAssetFormatRaw,
		BinaryPath:        "clangd.exe",
		MinWindowsVersion: "10.0",
		MinWindowsBuild:   19041,
	})
	asset, err := SelectWindowsLockedAsset(manifest, WindowsHostPlatform{
		OS: WindowsHostOSWindows, NativeArch: WindowsHostArchX64, ProcessArch: WindowsHostArchX86,
		WindowsVersion: "10.0", WindowsBuild: 22631,
	})
	if err != nil {
		t.Fatalf("SelectWindowsLockedAsset() error = %v", err)
	}
	if asset.Architecture != WindowsHostArchX64 {
		t.Fatalf("SelectWindowsLockedAsset() architecture = %q, want %q", asset.Architecture, WindowsHostArchX64)
	}
}

func TestSelectLockedAssetRejectsMissingArchitectureAndOldWindows(t *testing.T) {
	manifest := testLockedManifest(t, WindowsLockedAsset{
		Architecture:      WindowsHostArchX64,
		Version:           "clangd-18.1.8",
		Format:            WindowsLockedAssetFormatRaw,
		BinaryPath:        "clangd.exe",
		MinWindowsVersion: "10.0",
		MinWindowsBuild:   19041,
	})
	_, err := SelectWindowsLockedAsset(manifest, WindowsHostPlatform{OS: WindowsHostOSWindows, NativeArch: WindowsHostArchARM64, WindowsVersion: "10.0", WindowsBuild: 22631})
	var unsupported *WindowsUnsupportedAssetArchitectureError
	if !errors.As(err, &unsupported) || !errors.Is(err, ErrWindowsUnsupportedAssetArchitecture) {
		t.Fatalf("architecture error = %v, want typed unsupported architecture", err)
	}
	_, err = SelectWindowsLockedAsset(manifest, WindowsHostPlatform{OS: WindowsHostOSWindows, NativeArch: WindowsHostArchX64, WindowsVersion: "10.0", WindowsBuild: 18362})
	var oldWindows *WindowsUnsupportedWindowsVersionError
	if !errors.As(err, &oldWindows) || !errors.Is(err, ErrWindowsUnsupportedWindowsVersion) {
		t.Fatalf("Windows version error = %v, want typed old-version error", err)
	}
	_, err = SelectWindowsLockedAsset(manifest, WindowsHostPlatform{NativeArch: WindowsHostArchX64, WindowsVersion: "10.0", WindowsBuild: 22631})
	if err == nil || !errors.Is(err, ErrWindowsInvalidLockedAsset) {
		t.Fatalf("missing OS error = %v, want fail-fast invalid platform", err)
	}
}

func TestAssetCacheRawDownloadsOnceAndPublishesNestedExecutable(t *testing.T) {
	payload := []byte("MZ-lock-test-binary")
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		requests.Add(1)
		if got := request.UserAgent(); got != windowsAssetHTTPUserAgent {
			t.Errorf("locked Windows asset User-Agent = %q, want %q", got, windowsAssetHTTPUserAgent)
		}
		time.Sleep(100 * time.Millisecond)
		_, _ = writer.Write(payload)
	}))
	defer server.Close()
	manifest := testLockedManifestWithURL(t, server.URL+"/clangd.exe", WindowsLockedAsset{
		Architecture: WindowsHostArchARM64,
		Version:      "clangd-18.1.8",
		Format:       WindowsLockedAssetFormatRaw,
		BinaryPath:   "bin/clangd.exe",
		SHA256:       windowsAssetSHA256Hex(payload),
	})
	cache, err := NewWindowsLSPAssetCache(t.TempDir(), server.Client())
	if err != nil {
		t.Fatalf("NewWindowsLSPAssetCache() error = %v", err)
	}
	if !strings.HasSuffix(cache.Root(), filepath.Join("cache", WindowsLSPAssetCacheSubdir)) {
		t.Fatalf("cache root = %q, want product cache subdirectory", cache.Root())
	}
	ctx := context.Background()
	paths := make([]string, 8)
	errs := make(chan error, len(paths))
	var group sync.WaitGroup
	for index := range paths {
		index := index
		group.Go(func() {
			path, ensureErr := cache.ensureForArchitecture(ctx, manifest, WindowsHostArchARM64)
			paths[index] = path
			if ensureErr != nil {
				errs <- ensureErr
			}
		})
	}
	group.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("concurrent ensureForArchitecture() error = %v", err)
	}
	if got := requests.Load(); got != 1 {
		t.Fatalf("HTTP request count = %d, want exactly one", got)
	}
	for _, path := range paths {
		if path == "" {
			t.Fatal("concurrent ensureForArchitecture() returned empty path")
		}
		if !strings.HasSuffix(path, filepath.Join("ready", "bin", "clangd.exe")) {
			t.Fatalf("published path = %q, want nested ready executable", path)
		}
		got, readErr := os.ReadFile(path)
		if readErr != nil || !bytes.Equal(got, payload) {
			t.Fatalf("published executable read error=%v bytes=%q", readErr, got)
		}
	}
	if _, err := cache.ensureForArchitecture(ctx, manifest, WindowsHostArchARM64); err != nil {
		t.Fatalf("cached ensureForArchitecture() error = %v", err)
	}
	if got := requests.Load(); got != 1 {
		t.Fatalf("cached HTTP request count = %d, want one", got)
	}
}

func TestAssetCacheRepairsTamperedReadyExecutable(t *testing.T) {
	payload := []byte("MZ-authenticated-binary")
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		requests.Add(1)
		_, _ = writer.Write(payload)
	}))
	defer server.Close()
	manifest := testLockedManifestWithURL(t, server.URL+"/server.exe", WindowsLockedAsset{
		Architecture: WindowsHostArchX64,
		Version:      "tamper-test",
		Format:       WindowsLockedAssetFormatRaw,
		BinaryPath:   "bin/server.exe",
		SHA256:       windowsAssetSHA256Hex(payload),
	})
	cache, err := NewWindowsAssetCache(t.TempDir(), server.Client())
	if err != nil {
		t.Fatalf("NewWindowsAssetCache() error = %v", err)
	}
	path, err := cache.ensureForArchitecture(context.Background(), manifest, WindowsHostArchX64)
	if err != nil {
		t.Fatalf("initial ensureForArchitecture() error = %v", err)
	}
	if err := os.WriteFile(path, []byte("tampered"), 0o700); err != nil {
		t.Fatalf("tamper ready executable: %v", err)
	}
	repaired, err := cache.ensureForArchitecture(context.Background(), manifest, WindowsHostArchX64)
	if err != nil {
		t.Fatalf("repair ensureForArchitecture() error = %v", err)
	}
	got, err := os.ReadFile(repaired)
	if err != nil {
		t.Fatalf("read repaired executable: %v", err)
	}
	if !bytes.Equal(got, payload) {
		t.Fatalf("repaired executable = %q, want %q", got, payload)
	}
	if got := requests.Load(); got != 1 {
		t.Fatalf("repair HTTP request count = %d, want one verified payload download", got)
	}
}

func TestAssetCacheRepairsTamperedArchiveExecutable(t *testing.T) {
	payload := testZipArchive(t, "bin/server.exe", []byte("archive-authenticated-binary"))
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		requests.Add(1)
		_, _ = writer.Write(payload)
	}))
	defer server.Close()
	manifest := testLockedManifestWithURL(t, server.URL+"/server.zip", WindowsLockedAsset{
		Architecture: WindowsHostArchX64,
		Version:      "archive-tamper-test",
		Format:       WindowsLockedAssetFormatZip,
		BinaryPath:   "bin/server.exe",
		SHA256:       windowsAssetSHA256Hex(payload),
	})
	cache, err := NewWindowsAssetCache(t.TempDir(), server.Client())
	if err != nil {
		t.Fatalf("NewWindowsAssetCache() error = %v", err)
	}
	path, err := cache.ensureForArchitecture(context.Background(), manifest, WindowsHostArchX64)
	if err != nil {
		t.Fatalf("initial archive ensureForArchitecture() error = %v", err)
	}
	if err := os.WriteFile(path, []byte("tampered archive executable"), 0o700); err != nil {
		t.Fatalf("tamper archive executable: %v", err)
	}
	repaired, err := cache.ensureForArchitecture(context.Background(), manifest, WindowsHostArchX64)
	if err != nil {
		t.Fatalf("repair archive ensureForArchitecture() error = %v", err)
	}
	got, err := os.ReadFile(repaired)
	if err != nil {
		t.Fatalf("read repaired archive executable: %v", err)
	}
	if !bytes.Equal(got, []byte("archive-authenticated-binary")) {
		t.Fatalf("repaired archive executable = %q", got)
	}
	if got := requests.Load(); got != 1 {
		t.Fatalf("archive repair HTTP request count = %d, want one verified payload download", got)
	}
}

func TestAssetCacheRepairsTamperedArchiveCompanion(t *testing.T) {
	payload := testZipArchiveFiles(t, map[string][]byte{
		"bin/server.exe":  []byte("archive-authenticated-binary"),
		"lib/runtime.dll": []byte("companion-dll"),
	})
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		requests.Add(1)
		_, _ = writer.Write(payload)
	}))
	defer server.Close()
	manifest := testLockedManifestWithURL(t, server.URL+"/server.zip", WindowsLockedAsset{
		Architecture: WindowsHostArchX64,
		Version:      "archive-companion-tamper-test",
		Format:       WindowsLockedAssetFormatZip,
		BinaryPath:   "bin/server.exe",
		SHA256:       windowsAssetSHA256Hex(payload),
	})
	cache, err := NewWindowsAssetCache(t.TempDir(), server.Client())
	if err != nil {
		t.Fatalf("NewWindowsAssetCache() error = %v", err)
	}
	path, err := cache.ensureForArchitecture(context.Background(), manifest, WindowsHostArchX64)
	if err != nil {
		t.Fatalf("initial archive ensureForArchitecture() error = %v", err)
	}
	readyDir := filepath.Dir(filepath.Dir(path))
	companion := filepath.Join(readyDir, "lib", "runtime.dll")
	if err := os.WriteFile(companion, []byte("tampered-dll"), 0o700); err != nil {
		t.Fatalf("tamper archive companion: %v", err)
	}
	repaired, err := cache.ensureForArchitecture(context.Background(), manifest, WindowsHostArchX64)
	if err != nil {
		t.Fatalf("repair archive companion ensureForArchitecture() error = %v", err)
	}
	got, err := os.ReadFile(filepath.Join(filepath.Dir(filepath.Dir(repaired)), "lib", "runtime.dll"))
	if err != nil {
		t.Fatalf("read repaired archive companion: %v", err)
	}
	if !bytes.Equal(got, []byte("companion-dll")) {
		t.Fatalf("repaired archive companion = %q, want %q", got, "companion-dll")
	}
	if got := requests.Load(); got != 1 {
		t.Fatalf("archive companion repair HTTP request count = %d, want one verified payload download", got)
	}
}

func TestAssetCacheRejectsHTTPStatusAndChecksum(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/status" {
			http.Error(writer, "no", http.StatusBadGateway)
			return
		}
		_, _ = writer.Write([]byte("wrong"))
	}))
	defer server.Close()
	cache, err := NewWindowsAssetCache(t.TempDir(), server.Client())
	if err != nil {
		t.Fatalf("NewWindowsAssetCache() error = %v", err)
	}
	statusManifest := testLockedManifestWithURL(t, server.URL+"/status", WindowsLockedAsset{
		Architecture: WindowsHostArchX86, Version: "v1", Format: WindowsLockedAssetFormatRaw, BinaryPath: "server.exe",
	})
	if _, err := cache.ensureForArchitecture(context.Background(), statusManifest, WindowsHostArchX86); !errors.Is(err, ErrWindowsAssetHTTPStatus) {
		t.Fatalf("HTTP status error = %v, want ErrWindowsAssetHTTPStatus", err)
	}
	checksumManifest := testLockedManifestWithURL(t, server.URL+"/checksum", WindowsLockedAsset{
		Architecture: WindowsHostArchX86, Version: "v2", Format: WindowsLockedAssetFormatRaw, BinaryPath: "server.exe", SHA256: strings.Repeat("0", 64),
	})
	if _, err := cache.ensureForArchitecture(context.Background(), checksumManifest, WindowsHostArchX86); !errors.Is(err, ErrWindowsAssetChecksumMismatch) {
		t.Fatalf("checksum error = %v, want ErrWindowsAssetChecksumMismatch", err)
	}
}

func TestAssetCacheHonorsHTTPContextCancellation(t *testing.T) {
	started := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		close(started)
		<-request.Context().Done()
	}))
	defer server.Close()
	cache, err := NewWindowsAssetCache(t.TempDir(), server.Client())
	if err != nil {
		t.Fatalf("NewWindowsAssetCache() error = %v", err)
	}
	manifest := testLockedManifestWithURL(t, server.URL+"/blocked", WindowsLockedAsset{
		Architecture: WindowsHostArchX86,
		Version:      "context-test",
		Format:       WindowsLockedAssetFormatRaw,
		BinaryPath:   "server.cmd",
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	errCh := make(chan error, 1)
	go func() {
		_, ensureErr := cache.ensureForArchitecture(ctx, manifest, WindowsHostArchX86)
		errCh <- ensureErr
	}()
	<-started
	cancel()
	if err := <-errCh; !errors.Is(err, context.Canceled) {
		t.Fatalf("context cancellation error = %v, want context.Canceled", err)
	}
}

func TestAssetOSLockHonorsCancellationAndReleasesOnClose(t *testing.T) {
	lockPath := filepath.Join(t.TempDir(), "asset.lock")
	first, err := acquireAssetOSLock(context.Background(), lockPath)
	if err != nil {
		t.Fatalf("acquire first asset lock: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 75*time.Millisecond)
	defer cancel()
	if _, err := acquireAssetOSLock(ctx, lockPath); err == nil || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("second asset lock error = %v, want context deadline", err)
	}
	if err := first.Close(); err != nil {
		t.Fatalf("close first asset lock: %v", err)
	}
	third, err := acquireAssetOSLock(context.Background(), lockPath)
	if err != nil {
		t.Fatalf("acquire released asset lock: %v", err)
	}
	if err := third.Close(); err != nil {
		t.Fatalf("close released asset lock: %v", err)
	}
}

type fakeAssetOSLocker struct {
	err    error
	closed chan struct{}
}

func (l *fakeAssetOSLocker) Close() error {
	if l.closed != nil {
		close(l.closed)
	}
	return l.err
}

func TestReleaseAssetLockReportsCloseErrorAndReleasesToken(t *testing.T) {
	closeErr := errors.New("simulated OS lock close failure")
	fake := &fakeAssetOSLocker{err: closeErr, closed: make(chan struct{})}
	lock := &assetLock{ready: make(chan struct{}, 1)}
	lock.ready <- struct{}{}
	lease := &assetLease{lock: lock, shared: fake}

	released := make(chan error, 1)
	go func() {
		released <- releaseAssetLock(lease)
	}()

	var err error
	select {
	case err = <-released:
	case <-time.After(time.Second):
		t.Fatal("releaseAssetLock deadlocked after OS lock close failure")
	}
	if !errors.Is(err, closeErr) {
		t.Fatalf("releaseAssetLock() error = %v, want close error", err)
	}
	select {
	case <-fake.closed:
	case <-time.After(time.Second):
		t.Fatal("fake OS lock was not closed")
	}
	select {
	case lock.ready <- struct{}{}:
	default:
		t.Fatal("releaseAssetLock did not return the process lock token")
	}
}

func TestJoinAssetReleaseErrorPreservesOperationAndCloseErrors(t *testing.T) {
	operationErr := errors.New("simulated install failure")
	closeErr := errors.New("simulated OS lock close failure")
	err := joinAssetReleaseError(operationErr, closeErr, "release lock")
	if !errors.Is(err, operationErr) {
		t.Fatalf("joined error = %v, want operation error", err)
	}
	if !errors.Is(err, closeErr) {
		t.Fatalf("joined error = %v, want OS lock close error", err)
	}
}

func TestReleaseAssetLockSuccessKeepsExistingPath(t *testing.T) {
	fake := &fakeAssetOSLocker{}
	lock := &assetLock{ready: make(chan struct{}, 1)}
	lock.ready <- struct{}{}
	if err := releaseAssetLock(&assetLease{lock: lock, shared: fake}); err != nil {
		t.Fatalf("releaseAssetLock() error = %v, want nil", err)
	}
	select {
	case lock.ready <- struct{}{}:
	default:
		t.Fatal("successful release did not return the process lock token")
	}
}

func TestAssetCacheExtractsRawZipTarGzAndTarXz(t *testing.T) {
	fixtures := []struct {
		name    string
		format  WindowsLockedAssetFormat
		payload []byte
		path    string
	}{
		{name: "raw", format: WindowsLockedAssetFormatRaw, payload: []byte("raw-executable"), path: "bin/server.exe"},
		{name: "zip", format: WindowsLockedAssetFormatZip, payload: testZipArchive(t, "bin/server.exe", []byte("zip-executable")), path: "bin/server.exe"},
		{name: "tar.gz", format: WindowsLockedAssetFormatTarGz, payload: testTarArchive(t, "bin/server.exe", []byte("tar-gz-executable"), false), path: "bin/server.exe"},
		{name: "tar.xz", format: WindowsLockedAssetFormatTarXz, payload: testTarArchive(t, "bin/server.exe", []byte("tar-xz-executable"), true), path: "bin/server.exe"},
	}
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		requests.Add(1)
		name := strings.TrimPrefix(request.URL.Path, "/")
		for _, fixture := range fixtures {
			if fixture.name == name {
				_, _ = writer.Write(fixture.payload)
				return
			}
		}
		http.NotFound(writer, request)
	}))
	defer server.Close()
	cache, err := NewWindowsAssetCache(t.TempDir(), server.Client())
	if err != nil {
		t.Fatalf("NewWindowsAssetCache() error = %v", err)
	}
	for _, fixture := range fixtures {
		manifest := testLockedManifestWithURL(t, server.URL+"/"+fixture.name, WindowsLockedAsset{
			Architecture: WindowsHostArchX64, Version: "v-" + fixture.name, Format: fixture.format, BinaryPath: fixture.path,
			SHA256: windowsAssetSHA256Hex(fixture.payload),
		})
		path, ensureErr := cache.ensureForArchitecture(context.Background(), manifest, WindowsHostArchX64)
		if ensureErr != nil {
			t.Fatalf("%s ensureForArchitecture() error = %v", fixture.name, ensureErr)
		}
		got, readErr := os.ReadFile(path)
		if readErr != nil {
			t.Fatalf("%s read executable: %v", fixture.name, readErr)
		}
		want := fixture.payload
		if fixture.format != WindowsLockedAssetFormatRaw {
			want = []byte(strings.TrimSuffix(strings.TrimSuffix(fixture.name, ".gz"), ".xz") + "-executable")
		}
		if fixture.name == "zip" {
			want = []byte("zip-executable")
		} else if fixture.name == "tar.gz" {
			want = []byte("tar-gz-executable")
		} else if fixture.name == "tar.xz" {
			want = []byte("tar-xz-executable")
		}
		if !bytes.Equal(got, want) {
			t.Fatalf("%s executable = %q, want %q", fixture.name, got, want)
		}
	}
	if got := requests.Load(); got != int32(len(fixtures)) {
		t.Fatalf("archive HTTP request count = %d, want %d", got, len(fixtures))
	}
}

func TestAssetCacheArchiveLimitCanBeRaisedAndKeepsExactBinaryPath(t *testing.T) {
	payload := testTarArchive(t, "bin/server.cmd", []byte("cmd-wrapper"), false)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		_, _ = writer.Write(payload)
	}))
	defer server.Close()
	manifest := testLockedManifestWithURL(t, server.URL+"/server.tar.gz", WindowsLockedAsset{
		Architecture: WindowsHostArchX64,
		Version:      "limit-test",
		Format:       WindowsLockedAssetFormatTarGz,
		BinaryPath:   "bin/server.cmd",
		SHA256:       windowsAssetSHA256Hex(payload),
	})
	limited, err := NewWindowsAssetCacheWithOptions(t.TempDir(), WindowsAssetCacheOptions{
		HTTPClient:      server.Client(),
		MaxArchiveBytes: 4,
	})
	if err != nil {
		t.Fatalf("NewWindowsAssetCacheWithOptions() limited error = %v", err)
	}
	if _, err := limited.ensureForArchitecture(context.Background(), manifest, WindowsHostArchX64); err == nil {
		t.Fatal("limited archive unexpectedly succeeded")
	}
	raised, err := NewWindowsAssetCacheWithOptions(t.TempDir(), WindowsAssetCacheOptions{
		HTTPClient:      server.Client(),
		MaxArchiveBytes: 32,
	})
	if err != nil {
		t.Fatalf("NewWindowsAssetCacheWithOptions() raised error = %v", err)
	}
	path, err := raised.ensureForArchitecture(context.Background(), manifest, WindowsHostArchX64)
	if err != nil {
		t.Fatalf("raised archive ensureForArchitecture() error = %v", err)
	}
	if !strings.HasSuffix(path, filepath.Join("ready", "bin", "server.cmd")) {
		t.Fatalf("published wrapper path = %q, want exact .cmd path", path)
	}
	got, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(got, []byte("cmd-wrapper")) {
		t.Fatalf("published wrapper read error=%v bytes=%q", err, got)
	}
}

func TestAssetCacheRejectsArchiveTraversalAndLinks(t *testing.T) {
	fixtures := []struct {
		name    string
		format  WindowsLockedAssetFormat
		payload []byte
	}{
		{name: "zip-traversal", format: WindowsLockedAssetFormatZip, payload: testZipArchive(t, "../escape.exe", []byte("escape"))},
		{name: "tar-symlink", format: WindowsLockedAssetFormatTarGz, payload: testTarSymlinkArchive(t)},
		{name: "tar-xz-symlink", format: WindowsLockedAssetFormatTarXz, payload: testTarSymlinkArchiveXz(t)},
	}
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		for _, fixture := range fixtures {
			if strings.TrimPrefix(request.URL.Path, "/") == fixture.name {
				_, _ = writer.Write(fixture.payload)
				return
			}
		}
		http.NotFound(writer, request)
	}))
	defer server.Close()
	cache, err := NewWindowsAssetCache(t.TempDir(), server.Client())
	if err != nil {
		t.Fatalf("NewWindowsAssetCache() error = %v", err)
	}
	for _, fixture := range fixtures {
		manifest := testLockedManifestWithURL(t, server.URL+"/"+fixture.name, WindowsLockedAsset{
			Architecture: WindowsHostArchX64, Version: "unsafe-" + fixture.name, Format: fixture.format, BinaryPath: "server.exe",
			SHA256: windowsAssetSHA256Hex(fixture.payload),
		})
		_, err := cache.ensureForArchitecture(context.Background(), manifest, WindowsHostArchX64)
		if !errors.Is(err, ErrWindowsUnsafeAssetArchive) {
			t.Fatalf("%s error = %v, want ErrWindowsUnsafeAssetArchive", fixture.name, err)
		}
	}
}

func testLockedManifest(t *testing.T, asset WindowsLockedAsset) WindowsLockedAssetManifest {
	return testLockedManifestWithURL(t, "http://127.0.0.1:1/asset", asset)
}

func testLockedManifestWithURL(t *testing.T, rawURL string, asset WindowsLockedAsset) WindowsLockedAssetManifest {
	t.Helper()
	if asset.SHA256 == "" {
		payload := []byte("asset")
		hash := sha256.Sum256(payload)
		asset.SHA256 = hex.EncodeToString(hash[:])
	}
	asset.URL = rawURL
	if err := asset.validateForArchitecture(asset.Architecture); err != nil {
		t.Fatalf("test asset invalid: %v", err)
	}
	return WindowsLockedAssetManifest{Name: "test-lsp", Assets: map[string]WindowsLockedAsset{asset.Architecture: asset}}
}

func windowsAssetSHA256Hex(payload []byte) string {
	hash := sha256.Sum256(payload)
	return hex.EncodeToString(hash[:])
}

func testZipArchive(t *testing.T, name string, payload []byte) []byte {
	return testZipArchiveFiles(t, map[string][]byte{name: payload})
}

func testZipArchiveFiles(t *testing.T, files map[string][]byte) []byte {
	t.Helper()
	var buffer bytes.Buffer
	writer := zip.NewWriter(&buffer)
	for name, payload := range files {
		entry, err := writer.Create(name)
		if err != nil {
			t.Fatalf("create ZIP fixture: %v", err)
		}
		if _, err := entry.Write(payload); err != nil {
			t.Fatalf("write ZIP fixture: %v", err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close ZIP fixture: %v", err)
	}
	return buffer.Bytes()
}

func testTarArchive(t *testing.T, name string, payload []byte, xzFormat bool) []byte {
	t.Helper()
	var buffer bytes.Buffer
	var output io.Writer = &buffer
	var gzipWriter *gzip.Writer
	var xzWriter io.WriteCloser
	if xzFormat {
		var err error
		xzWriter, err = xz.NewWriter(&buffer)
		if err != nil {
			t.Fatalf("create xz fixture: %v", err)
		}
		output = xzWriter
	} else {
		gzipWriter = gzip.NewWriter(&buffer)
		output = gzipWriter
	}
	tarWriter := tar.NewWriter(output)
	if err := tarWriter.WriteHeader(&tar.Header{Name: name, Mode: 0o700, Size: int64(len(payload)), Typeflag: tar.TypeReg}); err != nil {
		t.Fatalf("write tar header fixture: %v", err)
	}
	if _, err := tarWriter.Write(payload); err != nil {
		t.Fatalf("write tar fixture: %v", err)
	}
	if err := tarWriter.Close(); err != nil {
		t.Fatalf("close tar fixture: %v", err)
	}
	if xzFormat {
		if err := xzWriter.Close(); err != nil {
			t.Fatalf("close xz fixture: %v", err)
		}
	} else if err := gzipWriter.Close(); err != nil {
		t.Fatalf("close gzip fixture: %v", err)
	}
	return buffer.Bytes()
}

func testTarSymlinkArchive(t *testing.T) []byte {
	t.Helper()
	var buffer bytes.Buffer
	gzipWriter := gzip.NewWriter(&buffer)
	tarWriter := tar.NewWriter(gzipWriter)
	if err := tarWriter.WriteHeader(&tar.Header{Name: "server.exe", Typeflag: tar.TypeSymlink, Linkname: "../../escape.exe"}); err != nil {
		t.Fatalf("write symlink tar fixture: %v", err)
	}
	if err := tarWriter.Close(); err != nil {
		t.Fatalf("close symlink tar fixture: %v", err)
	}
	if err := gzipWriter.Close(); err != nil {
		t.Fatalf("close symlink gzip fixture: %v", err)
	}
	return buffer.Bytes()
}

func testTarSymlinkArchiveXz(t *testing.T) []byte {
	t.Helper()
	var buffer bytes.Buffer
	xzWriter, err := xz.NewWriter(&buffer)
	if err != nil {
		t.Fatalf("create symlink xz fixture: %v", err)
	}
	tarWriter := tar.NewWriter(xzWriter)
	if err := tarWriter.WriteHeader(&tar.Header{Name: "server.exe", Typeflag: tar.TypeSymlink, Linkname: "../../escape.exe"}); err != nil {
		t.Fatalf("write symlink xz tar fixture: %v", err)
	}
	if err := tarWriter.Close(); err != nil {
		t.Fatalf("close symlink xz tar fixture: %v", err)
	}
	if err := xzWriter.Close(); err != nil {
		t.Fatalf("close symlink xz fixture: %v", err)
	}
	return buffer.Bytes()
}
