//go:build windows

package installer

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

type runtimeCleanupFaultFile struct {
	bytes.Buffer
	name     string
	closeErr error
}

func (f *runtimeCleanupFaultFile) Name() string { return f.name }

func (f *runtimeCleanupFaultFile) Chmod(os.FileMode) error { return nil }

func (f *runtimeCleanupFaultFile) Close() error { return f.closeErr }

func TestWindowsRuntimeDependencyFetcherReportsResponseBodyCloseError(t *testing.T) {
	closeErr := errors.New("injected runtime response body close failure")
	payload := []byte("runtime-asset")
	client := &http.Client{Transport: windowsCleanupRoundTrip(func(request *http.Request) (*http.Response, error) {
		if got := request.UserAgent(); got != windowsAssetHTTPUserAgent {
			t.Errorf("Windows runtime dependency User-Agent = %q, want %q", got, windowsAssetHTTPUserAgent)
		}
		return &http.Response{
			StatusCode:    http.StatusOK,
			Status:        "200 OK",
			Body:          runtimeCleanupBody{Reader: bytes.NewReader(payload), closeErr: closeErr},
			ContentLength: int64(len(payload)),
			Header:        make(http.Header),
			Request:       &http.Request{},
		}, nil
	})}
	fetch := defaultWindowsRuntimeDependencyAssetFetcher(client)
	asset := WindowsRuntimeDependencyAsset{
		Component:         "runtime",
		Version:           "1.0.0",
		URL:               "https://example.invalid/runtime",
		ChecksumAlgorithm: WindowsRuntimeDependencyChecksumSHA256,
		Checksum:          sha256Hex(payload),
	}
	err := fetch(context.Background(), asset, filepath.Join(t.TempDir(), "payload"))
	if !errors.Is(err, closeErr) {
		t.Fatalf("fetch() error = %v, want response body close error", err)
	}
}

func TestWindowsRuntimeDependencyReadyReportsTemporaryCloseError(t *testing.T) {
	closeErr := errors.New("injected runtime ready temporary close failure")
	stage := t.TempDir()
	if err := os.WriteFile(filepath.Join(stage, "runtime.exe"), []byte("runtime"), 0o700); err != nil {
		t.Fatal(err)
	}
	originalCreateTemp := createWindowsInstallerTemp
	createWindowsInstallerTemp = func(dir, pattern string) (windowsInstallerFile, error) {
		return &runtimeCleanupFaultFile{name: filepath.Join(dir, pattern+"injected"), closeErr: closeErr}, nil
	}
	t.Cleanup(func() { createWindowsInstallerTemp = originalCreateTemp })
	err := writeWindowsRuntimeDependencyReady(stage, WindowsRuntimeDependencyCatalogEntry{Product: WindowsRuntimeDependencyProductGoGopls}, WindowsHostArchARM64, "cohort")
	if !errors.Is(err, closeErr) {
		t.Fatalf("writeWindowsRuntimeDependencyReady() error = %v, want temporary close error", err)
	}
}

func TestWindowsRuntimeDependencyCopyJoinsInputAndOutputCloseErrors(t *testing.T) {
	inputCloseErr := errors.New("injected runtime input close failure")
	outputCloseErr := errors.New("injected runtime output close failure")
	source := filepath.Join(t.TempDir(), "source")
	if err := os.WriteFile(source, []byte("configuration"), 0o600); err != nil {
		t.Fatal(err)
	}
	destination := filepath.Join(t.TempDir(), "destination")
	originalOpenInput := openWindowsInstallerInput
	originalOpenOutput := openWindowsInstallerOutput
	openWindowsInstallerInput = func(string) (windowsInstallerFile, error) {
		return &runtimeCleanupFaultFile{Buffer: *bytes.NewBufferString("configuration"), closeErr: inputCloseErr}, nil
	}
	openWindowsInstallerOutput = func(name string, _ int, _ os.FileMode) (windowsInstallerFile, error) {
		return &runtimeCleanupFaultFile{name: name, closeErr: outputCloseErr}, nil
	}
	t.Cleanup(func() {
		openWindowsInstallerInput = originalOpenInput
		openWindowsInstallerOutput = originalOpenOutput
	})
	err := copyWindowsRuntimeDependencyDirectory(filepath.Dir(source), destination)
	if !errors.Is(err, inputCloseErr) || !errors.Is(err, outputCloseErr) {
		t.Fatalf("copyWindowsRuntimeDependencyDirectory() error = %v, want both close errors", err)
	}
}

func TestWindowsRuntimeDependencyProvisionJoinsStageRemoveAllError(t *testing.T) {
	removeErr := errors.New("injected runtime staging remove failure")
	runErr := errors.New("injected runtime command failure")
	platform := WindowsHostPlatform{OS: WindowsHostOSWindows, NativeArch: WindowsHostArchARM64, ProcessArch: WindowsHostArchARM64, WindowsVersion: "10.0", WindowsBuild: 26100}
	originalRemoveAll := removeWindowsInstallerAll
	removeWindowsInstallerAll = func(path string) error {
		if strings.Contains(filepath.Base(path), ".staging-") {
			return removeErr
		}
		return originalRemoveAll(path)
	}
	t.Cleanup(func() { removeWindowsInstallerAll = originalRemoveAll })
	fetch := func(ctx context.Context, asset WindowsRuntimeDependencyAsset, destination string) error {
		if asset.Component == "gopls" {
			return writeWindowsRuntimeDependencyTestZip(destination, map[string]string{"gopls@v0.23.0/go.mod": "module gopls"})
		}
		return writeWindowsRuntimeDependencyTestZip(destination, map[string]string{"go/bin/go.exe": "go-runtime"})
	}
	_, err := ProvisionWindowsRuntimeDependencyWithOptions(context.Background(), WindowsRuntimeDependencyProductGoGopls, WindowsRuntimeDependencyProvisionOptions{
		CacheRoot:  t.TempDir(),
		Platform:   &platform,
		FetchAsset: fetch,
		RunCommand: func(context.Context, string, string, []string, []string) error { return runErr },
	})
	if !errors.Is(err, runErr) || !errors.Is(err, removeErr) {
		t.Fatalf("ProvisionWindowsRuntimeDependencyWithOptions() error = %v, want command and staging cleanup errors", err)
	}
}

func TestWindowsRuntimeDependencyProvisionRemovesInvalidPublishedRoot(t *testing.T) {
	validationErr := errors.New("injected published runtime dependency validation failure")
	platform := WindowsHostPlatform{OS: WindowsHostOSWindows, NativeArch: WindowsHostArchARM64, ProcessArch: WindowsHostArchARM64, WindowsVersion: "10.0", WindowsBuild: 26100}
	root := t.TempDir()
	entry, err := WindowsRuntimeDependencyCatalogEntryForProduct(WindowsRuntimeDependencyProductGoGopls)
	if err != nil {
		t.Fatal(err)
	}
	cohort := runtimeDependencyCohort(entry, WindowsHostArchARM64)
	finalRoot := runtimeDependencyFinalRoot(root, entry.Product, WindowsHostArchARM64, cohort)
	originalValidator := runtimeDependencyPostPublishValidator
	runtimeDependencyPostPublishValidator = func(WindowsRuntimeDependencyCatalogEntry, WindowsHostPlatform, string, string, string) (WindowsRuntimeDependencyProvisionResult, error) {
		return WindowsRuntimeDependencyProvisionResult{RootPath: finalRoot, CacheHit: true}, validationErr
	}
	t.Cleanup(func() { runtimeDependencyPostPublishValidator = originalValidator })
	result, err := ProvisionWindowsRuntimeDependencyWithOptions(context.Background(), entry.Product, WindowsRuntimeDependencyProvisionOptions{
		CacheRoot:  root,
		Platform:   &platform,
		FetchAsset: windowsRuntimeDependencyCleanupTestFetch,
		RunCommand: windowsRuntimeDependencyCleanupTestRun,
	})
	if !errors.Is(err, validationErr) {
		t.Fatalf("ProvisionWindowsRuntimeDependencyWithOptions() error = %v, want published validation error", err)
	}
	if result.RootPath != "" || result.CacheHit || result.ServerPath != "" {
		t.Fatalf("provision result = %+v, want cleared result after invalid publish", result)
	}
	if _, statErr := os.Lstat(finalRoot); !os.IsNotExist(statErr) {
		t.Fatalf("published runtime dependency root = %q, want removed; stat error = %v", finalRoot, statErr)
	}
}

func TestWindowsRuntimeDependencyProvisionPublishedCleanupJoinsRemoveAllError(t *testing.T) {
	validationErr := errors.New("injected published runtime dependency validation failure")
	removeErr := errors.New("injected published runtime dependency remove failure")
	platform := WindowsHostPlatform{OS: WindowsHostOSWindows, NativeArch: WindowsHostArchARM64, ProcessArch: WindowsHostArchARM64, WindowsVersion: "10.0", WindowsBuild: 26100}
	root := t.TempDir()
	entry, err := WindowsRuntimeDependencyCatalogEntryForProduct(WindowsRuntimeDependencyProductGoGopls)
	if err != nil {
		t.Fatal(err)
	}
	cohort := runtimeDependencyCohort(entry, WindowsHostArchARM64)
	finalRoot := runtimeDependencyFinalRoot(root, entry.Product, WindowsHostArchARM64, cohort)
	originalValidator := runtimeDependencyPostPublishValidator
	originalRemoveAll := removeWindowsInstallerAll
	runtimeDependencyPostPublishValidator = func(WindowsRuntimeDependencyCatalogEntry, WindowsHostPlatform, string, string, string) (WindowsRuntimeDependencyProvisionResult, error) {
		return WindowsRuntimeDependencyProvisionResult{RootPath: finalRoot, CacheHit: true}, validationErr
	}
	removeWindowsInstallerAll = func(path string) error {
		if path == finalRoot {
			return removeErr
		}
		return originalRemoveAll(path)
	}
	t.Cleanup(func() {
		runtimeDependencyPostPublishValidator = originalValidator
		removeWindowsInstallerAll = originalRemoveAll
	})
	result, err := ProvisionWindowsRuntimeDependencyWithOptions(context.Background(), entry.Product, WindowsRuntimeDependencyProvisionOptions{
		CacheRoot:  root,
		Platform:   &platform,
		FetchAsset: windowsRuntimeDependencyCleanupTestFetch,
		RunCommand: windowsRuntimeDependencyCleanupTestRun,
	})
	if !errors.Is(err, validationErr) || !errors.Is(err, removeErr) {
		t.Fatalf("ProvisionWindowsRuntimeDependencyWithOptions() error = %v, want validation and published cleanup errors", err)
	}
	if result.RootPath != "" || result.CacheHit || result.ServerPath != "" {
		t.Fatalf("provision result = %+v, want cleared result after cleanup failure", result)
	}
}

func TestWindowsEnsureDirectoryNoSymlinkRejectsJunction(t *testing.T) {
	root := t.TempDir()
	externalRoot := t.TempDir()
	junction := filepath.Join(root, "junction")
	createWindowsTestJunction(t, junction, externalRoot)
	sentinel := filepath.Join(externalRoot, "sentinel.txt")
	if err := os.WriteFile(sentinel, []byte("keep"), 0o600); err != nil {
		t.Fatalf("write external sentinel: %v", err)
	}

	err := ensureDirectoryNoSymlink(filepath.Join(junction, "created"))
	if err == nil {
		t.Fatal("ensureDirectoryNoSymlink() accepted a junction")
	}
	if _, statErr := os.Stat(filepath.Join(externalRoot, "created")); !os.IsNotExist(statErr) {
		t.Fatalf("junction target was modified: stat error = %v", statErr)
	}
	assertWindowsTestSentinelUnchanged(t, sentinel)
}

func TestWindowsReadyBinaryPathRejectsJunction(t *testing.T) {
	readyRoot := filepath.Join(t.TempDir(), "ready")
	externalRoot := t.TempDir()
	createWindowsTestJunction(t, readyRoot, externalRoot)
	binaryPath := filepath.Join(externalRoot, "bin", "runtime.exe")
	if err := os.MkdirAll(filepath.Dir(binaryPath), 0o700); err != nil {
		t.Fatalf("create external binary directory: %v", err)
	}
	if err := os.WriteFile(binaryPath, []byte("runtime"), 0o700); err != nil {
		t.Fatalf("write external binary: %v", err)
	}
	sentinel := filepath.Join(externalRoot, "sentinel.txt")
	if err := os.WriteFile(sentinel, []byte("keep"), 0o600); err != nil {
		t.Fatalf("write external sentinel: %v", err)
	}

	_, ok := readyBinaryPath(readyRoot, WindowsLockedAsset{Format: WindowsLockedAssetFormatRaw, BinaryPath: "bin/runtime.exe"})
	if ok {
		t.Fatal("readyBinaryPath() accepted a junction-backed executable")
	}
	assertWindowsTestSentinelUnchanged(t, sentinel)
}

func TestWindowsRuntimeDependencyStaleCacheRejectsJunctionBeforeDelete(t *testing.T) {
	cacheRoot := t.TempDir()
	externalRoot := t.TempDir()
	finalRoot := filepath.Join(cacheRoot, "runtime-dependencies", "product", "arm64", "cohort")
	if err := os.MkdirAll(filepath.Dir(finalRoot), 0o700); err != nil {
		t.Fatalf("create stale cache parent: %v", err)
	}
	createWindowsTestJunction(t, finalRoot, externalRoot)
	sentinel := filepath.Join(externalRoot, "sentinel.txt")
	if err := os.WriteFile(sentinel, []byte("keep"), 0o600); err != nil {
		t.Fatalf("write external sentinel: %v", err)
	}

	if err := removeInvalidWindowsRuntimeDependencyCache(finalRoot); err == nil {
		t.Fatal("removeInvalidWindowsRuntimeDependencyCache() accepted a junction")
	}
	assertWindowsTestSentinelUnchanged(t, sentinel)
}

func TestWindowsRuntimeDependencyCopyRejectsJunctionDestination(t *testing.T) {
	sourceRoot := t.TempDir()
	if err := os.WriteFile(filepath.Join(sourceRoot, "config.ini"), []byte("config"), 0o600); err != nil {
		t.Fatalf("write source configuration: %v", err)
	}
	externalRoot := t.TempDir()
	destination := filepath.Join(t.TempDir(), "destination")
	createWindowsTestJunction(t, destination, externalRoot)
	sentinel := filepath.Join(externalRoot, "sentinel.txt")
	if err := os.WriteFile(sentinel, []byte("keep"), 0o600); err != nil {
		t.Fatalf("write external sentinel: %v", err)
	}

	if err := copyWindowsRuntimeDependencyDirectory(sourceRoot, destination); err == nil {
		t.Fatal("copyWindowsRuntimeDependencyDirectory() accepted a junction destination")
	}
	if _, statErr := os.Stat(filepath.Join(externalRoot, "config.ini")); !os.IsNotExist(statErr) {
		t.Fatalf("junction target was modified: stat error = %v", statErr)
	}
	assertWindowsTestSentinelUnchanged(t, sentinel)
}

func createWindowsTestJunction(t *testing.T, junction, target string) {
	t.Helper()
	if err := os.MkdirAll(target, 0o700); err != nil {
		t.Fatalf("create junction target: %v", err)
	}
	output, err := exec.Command("cmd.exe", "/d", "/c", "mklink", "/J", junction, target).CombinedOutput()
	if err != nil {
		t.Fatalf("create junction %q -> %q: %v (%s)", junction, target, err, strings.TrimSpace(string(output)))
	}
	t.Cleanup(func() {
		if err := os.Remove(junction); err != nil && !os.IsNotExist(err) {
			t.Errorf("remove test junction %q: %v", junction, err)
		}
	})
}

func assertWindowsTestSentinelUnchanged(t *testing.T, path string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read external sentinel %q: %v", path, err)
	}
	if string(data) != "keep" {
		t.Fatalf("external sentinel %q changed to %q", path, data)
	}
}

func windowsRuntimeDependencyCleanupTestFetch(ctx context.Context, asset WindowsRuntimeDependencyAsset, destination string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if asset.Component == "gopls" {
		return writeWindowsRuntimeDependencyTestZip(destination, map[string]string{"gopls@v0.23.0/go.mod": "module gopls"})
	}
	return writeWindowsRuntimeDependencyTestZip(destination, map[string]string{"go/bin/go.exe": "go-runtime"})
}

func windowsRuntimeDependencyCleanupTestRun(_ context.Context, _, workingDir string, _, _ []string) error {
	if err := os.MkdirAll(filepath.Join(workingDir, "bin"), 0o700); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(workingDir, "bin", "gopls.exe"), []byte("gopls-runtime"), 0o700)
}

type runtimeCleanupBody struct {
	io.Reader
	closeErr error
}

func (b runtimeCleanupBody) Close() error { return b.closeErr }

type windowsCleanupRoundTrip func(*http.Request) (*http.Response, error)

func (f windowsCleanupRoundTrip) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}
