//go:build windows

package installer

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

// TestWindowsVCLibsDesktopAssetFactsCoverNativeWindowsArchitectures 验证三种原生 Windows 架构都有固定微软资产且返回值可安全修改。
func TestWindowsVCLibsDesktopAssetFactsCoverNativeWindowsArchitectures(t *testing.T) {
	expected := map[string]struct {
		archive string
		sha256  string
	}{
		WindowsHostArchARM64: {"Microsoft.VCLibs.arm64.14.00.Desktop.appx", "9a7f6d69ea6cf042ea8680b7cd0bfaa9c04f0f6cc89055d43f7f6cd0250508d3"},
		WindowsHostArchX64:   {"Microsoft.VCLibs.x64.14.00.Desktop.appx", "b56a9101f706f9d95f815f5b7fa6efbac972e86573d378b96a07cff5540c5961"},
		WindowsHostArchX86:   {"Microsoft.VCLibs.x86.14.00.Desktop.appx", "a7fb9d76e07b36d868179eb53ffd13740c25242176fa363f154798cf34edd4a9"},
	}
	facts := WindowsVCLibsDesktopAssetFacts()
	if len(facts) != len(expected) {
		t.Fatalf("WindowsVCLibsDesktopAssetFacts() count = %d, want %d", len(facts), len(expected))
	}
	for architecture, want := range expected {
		asset, ok := facts[architecture]
		if !ok {
			t.Fatalf("WindowsVCLibsDesktopAssetFacts() missing %s", architecture)
		}
		if !strings.HasSuffix(asset.URL, "/"+want.archive) || asset.SHA256 != want.sha256 {
			t.Fatalf("Windows VCLibs %s asset = %#v, want archive %s sha256 %s", architecture, asset, want.archive, want.sha256)
		}
		if asset.Version != WindowsVCLibsDesktopPackageVersion || asset.Format != WindowsLockedAssetFormatZip || asset.BinaryPath != "vcruntime140.dll" {
			t.Fatalf("Windows VCLibs %s metadata = %#v", architecture, asset)
		}
	}
	mutated := facts[WindowsHostArchARM64]
	mutated.URL = "https://invalid.example.test/mutated.appx"
	facts[WindowsHostArchARM64] = mutated
	if WindowsVCLibsDesktopAssetFacts()[WindowsHostArchARM64].URL == mutated.URL {
		t.Fatal("WindowsVCLibsDesktopAssetFacts() leaked mutable global state")
	}
}

// TestWindowsVCLibsDesktopAssetForPlatformUsesNativeArchitecture 验证进程架构不会覆盖 ARM64、x64 或 x86 的原生系统架构选择。
func TestWindowsVCLibsDesktopAssetForPlatformUsesNativeArchitecture(t *testing.T) {
	for _, architecture := range []string{WindowsHostArchARM64, WindowsHostArchX64, WindowsHostArchX86} {
		architecture := architecture
		t.Run(architecture, func(t *testing.T) {
			asset, err := WindowsVCLibsDesktopAssetForPlatform(WindowsHostPlatform{
				OS: WindowsHostOSWindows, NativeArch: architecture, ProcessArch: WindowsHostArchX86,
				WindowsVersion: "10.0", WindowsBuild: 26100,
			})
			if err != nil {
				t.Fatalf("WindowsVCLibsDesktopAssetForPlatform(%s) error = %v", architecture, err)
			}
			if asset.Architecture != architecture {
				t.Fatalf("WindowsVCLibsDesktopAssetForPlatform(%s) architecture = %q", architecture, asset.Architecture)
			}
		})
	}
}

// TestProvisionWindowsVCLibsDesktopAppLocalValidatesIdentityAndReusesCache 验证下载、Appx 身份复验和缓存命中不会产生第二次网络请求。
func TestProvisionWindowsVCLibsDesktopAppLocalValidatesIdentityAndReusesCache(t *testing.T) {
	payload := makeWindowsVCLibsDesktopTestAppx(t, "arm64")
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		_, _ = writer.Write(payload)
	}))
	defer server.Close()

	asset := WindowsLockedAsset{
		Architecture:      WindowsHostArchARM64,
		Version:           WindowsVCLibsDesktopPackageVersion,
		URL:               server.URL + "/Microsoft.VCLibs.arm64.14.00.Desktop.appx",
		SHA256:            windowsVCLibsDesktopTestSHA256(payload),
		Format:            WindowsLockedAssetFormatZip,
		BinaryPath:        "vcruntime140.dll",
		MinWindowsVersion: "10.0",
		MinWindowsBuild:   10042,
	}
	manifest := WindowsLockedAssetManifest{Name: "windows-vclibs-desktop-app-local-test", Assets: map[string]WindowsLockedAsset{WindowsHostArchARM64: asset}}
	productRoot := t.TempDir()
	cache, err := NewWindowsLSPAssetCache(productRoot, server.Client())
	if err != nil {
		t.Fatalf("NewWindowsLSPAssetCache() error = %v", err)
	}
	platform := WindowsHostPlatform{OS: WindowsHostOSWindows, NativeArch: WindowsHostArchARM64, ProcessArch: WindowsHostArchX86, WindowsVersion: "10.0", WindowsBuild: 26100}
	first, err := provisionWindowsVCLibsDesktopAppLocalForPlatform(context.Background(), cache, platform, manifest)
	if err != nil {
		t.Fatalf("first provisionWindowsVCLibsDesktopAppLocalForPlatform() error = %v", err)
	}
	second, err := provisionWindowsVCLibsDesktopAppLocalForPlatform(context.Background(), cache, platform, manifest)
	if err != nil {
		t.Fatalf("cached provisionWindowsVCLibsDesktopAppLocalForPlatform() error = %v", err)
	}
	if first != second || !filepath.IsAbs(first) {
		t.Fatalf("Windows VCLibs ready roots = %q and %q, want identical absolute paths", first, second)
	}
	parentPath := os.Getenv("PATH")
	resolved, err := resolveWindowsVCLibsDesktopAppLocalPathForPlatform(productRoot, platform, manifest)
	if err != nil {
		t.Fatalf("read-only Windows VCLibs resolver error = %v", err)
	}
	if resolved != first {
		t.Fatalf("read-only Windows VCLibs resolver = %q, want %q", resolved, first)
	}
	processRoot, err := WindowsShortProcessPathWithinRoot(productRoot, resolved)
	if err != nil {
		t.Fatalf("WindowsShortProcessPathWithinRoot(VCLibs) error = %v", err)
	}
	resolvedInfo, err := os.Stat(resolved)
	if err != nil {
		t.Fatal(err)
	}
	processInfo, err := os.Stat(processRoot)
	if err != nil {
		t.Fatal(err)
	}
	if !os.SameFile(resolvedInfo, processInfo) {
		t.Fatal("Windows VCLibs process root changed ready-directory identity")
	}
	if os.Getenv("PATH") != parentPath {
		t.Fatal("read-only Windows VCLibs resolver changed parent PATH")
	}
	if got := requests.Load(); got != 1 {
		t.Fatalf("Windows VCLibs Appx requests = %d, want 1", got)
	}
	extraDLL := filepath.Join(resolved, "vcruntime140_1.dll")
	if err := os.Remove(extraDLL); err != nil {
		t.Fatalf("remove non-minimal Windows VCLibs DLL fixture: %v", err)
	}
	if _, err := resolveWindowsVCLibsDesktopAppLocalPathForPlatform(productRoot, platform, manifest); !errors.Is(err, ErrWindowsVCLibsDesktopInstallCacheMiss) {
		t.Fatalf("deleted non-minimal Windows VCLibs DLL resolver error = %v, want typed cache miss", err)
	}
	if err := os.WriteFile(extraDLL, []byte("MZ-vcruntime140_1.dll"), 0o600); err != nil {
		t.Fatalf("restore non-minimal Windows VCLibs DLL fixture: %v", err)
	}
	if _, err := resolveWindowsVCLibsDesktopAppLocalPathForPlatform(productRoot, platform, manifest); err != nil {
		t.Fatalf("restored non-minimal Windows VCLibs DLL resolver error = %v", err)
	}
	if err := os.WriteFile(extraDLL, []byte("tampered-extra"), 0o600); err != nil {
		t.Fatalf("tamper non-minimal Windows VCLibs DLL fixture: %v", err)
	}
	if _, err := resolveWindowsVCLibsDesktopAppLocalPathForPlatform(productRoot, platform, manifest); !errors.Is(err, ErrWindowsVCLibsDesktopInstallCacheMiss) {
		t.Fatalf("tampered non-minimal Windows VCLibs DLL resolver error = %v, want typed cache miss", err)
	}
	if err := os.WriteFile(extraDLL, []byte("MZ-vcruntime140_1.dll"), 0o600); err != nil {
		t.Fatalf("restore tampered non-minimal Windows VCLibs DLL fixture: %v", err)
	}
	if err := os.WriteFile(filepath.Join(resolved, "vcruntime140.dll"), []byte("tampered"), 0o600); err != nil {
		t.Fatalf("tamper Windows VCLibs ready DLL fixture: %v", err)
	}
	if _, err := resolveWindowsVCLibsDesktopAppLocalPathForPlatform(productRoot, platform, manifest); !errors.Is(err, ErrWindowsVCLibsDesktopInstallCacheMiss) {
		t.Fatalf("tampered Windows VCLibs resolver error = %v, want typed cache miss", err)
	}
}

// TestWindowsVCLibsReadOnlyResolverRejectsReadyJunction 验证内容完全相同也不能借
// junction 把 ready 根导向缓存外；resolver 必须在读取 DLL 前返回 typed cache miss。
func TestWindowsVCLibsReadOnlyResolverRejectsReadyJunction(t *testing.T) {
	payload := makeWindowsVCLibsDesktopTestAppx(t, "arm64")
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) { _, _ = writer.Write(payload) }))
	defer server.Close()
	asset := WindowsLockedAsset{
		Architecture: WindowsHostArchARM64,
		Version:      WindowsVCLibsDesktopPackageVersion,
		URL:          server.URL + "/Microsoft.VCLibs.arm64.14.00.Desktop.appx",
		SHA256:       windowsVCLibsDesktopTestSHA256(payload),
		Format:       WindowsLockedAssetFormatZip,
		BinaryPath:   "vcruntime140.dll",
	}
	manifest := WindowsLockedAssetManifest{Name: "windows-vclibs-ready-junction-test", Assets: map[string]WindowsLockedAsset{WindowsHostArchARM64: asset}}
	productRoot := t.TempDir()
	cache, err := NewWindowsLSPAssetCache(productRoot, server.Client())
	if err != nil {
		t.Fatalf("NewWindowsLSPAssetCache() error = %v", err)
	}
	platform := WindowsHostPlatform{OS: WindowsHostOSWindows, NativeArch: WindowsHostArchARM64, WindowsVersion: "10.0", WindowsBuild: 26100}
	readyRoot, err := provisionWindowsVCLibsDesktopAppLocalForPlatform(context.Background(), cache, platform, manifest)
	if err != nil {
		t.Fatalf("provision Windows VCLibs junction fixture: %v", err)
	}
	externalRoot := filepath.Join(productRoot, "external-ready-copy")
	if err := os.Rename(readyRoot, externalRoot); err != nil {
		t.Fatalf("move Windows VCLibs ready tree outside asset root: %v", err)
	}
	createWindowsTestJunction(t, readyRoot, externalRoot)
	_, err = resolveWindowsVCLibsDesktopAppLocalPathForPlatform(productRoot, platform, manifest)
	if !errors.Is(err, ErrWindowsVCLibsDesktopInstallCacheMiss) || !strings.Contains(strings.ToLower(err.Error()), "reparse") {
		t.Fatalf("junction-backed Windows VCLibs resolver error = %v, want typed cache miss mentioning reparse", err)
	}
}

// TestProvisionWindowsVCLibsDesktopAppLocalRejectsManifestArchitectureMismatch 验证包内架构与原生 Windows 架构不一致时立即失败。
func TestProvisionWindowsVCLibsDesktopAppLocalRejectsManifestArchitectureMismatch(t *testing.T) {
	payload := makeWindowsVCLibsDesktopTestAppx(t, "x64")
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) { _, _ = writer.Write(payload) }))
	defer server.Close()
	manifest := WindowsLockedAssetManifest{Name: "windows-vclibs-desktop-identity-mismatch-test", Assets: map[string]WindowsLockedAsset{
		WindowsHostArchARM64: {
			Architecture: WindowsHostArchARM64,
			Version:      WindowsVCLibsDesktopPackageVersion,
			URL:          server.URL + "/mismatch.appx",
			SHA256:       windowsVCLibsDesktopTestSHA256(payload),
			Format:       WindowsLockedAssetFormatZip,
			BinaryPath:   "vcruntime140.dll",
		},
	}}
	cache, err := NewWindowsLSPAssetCache(t.TempDir(), server.Client())
	if err != nil {
		t.Fatalf("NewWindowsLSPAssetCache() error = %v", err)
	}
	_, err = provisionWindowsVCLibsDesktopAppLocalForPlatform(context.Background(), cache, WindowsHostPlatform{
		OS: WindowsHostOSWindows, NativeArch: WindowsHostArchARM64, WindowsVersion: "10.0", WindowsBuild: 26100,
	}, manifest)
	if err == nil || !strings.Contains(err.Error(), "identity mismatch") {
		t.Fatalf("architecture mismatch error = %v, want Appx identity mismatch", err)
	}
}

func makeWindowsVCLibsDesktopTestAppx(t *testing.T, manifestArchitecture string) []byte {
	t.Helper()
	var buffer bytes.Buffer
	writer := zip.NewWriter(&buffer)
	manifest := fmt.Sprintf(`<Package xmlns="http://schemas.microsoft.com/appx/manifest/foundation/windows10"><Identity Name="%s" Publisher="%s" Version="%s" ProcessorArchitecture="%s" /></Package>`,
		WindowsVCLibsDesktopPackageIdentity, WindowsVCLibsDesktopPackagePublisher, WindowsVCLibsDesktopPackageVersion, manifestArchitecture)
	entries := map[string][]byte{"AppxManifest.xml": []byte(manifest)}
	for _, name := range windowsVCLibsDesktopRequiredDLLs {
		entries[name] = []byte("MZ-" + name)
	}
	// 该文件故意不在 required DLL 最小清单中，用于证明只读 resolver 会核对
	// Appx 的完整树，而不是只核对能启动的七个最小 DLL。
	entries["vcruntime140_1.dll"] = []byte("MZ-vcruntime140_1.dll")
	for name, content := range entries {
		entry, err := writer.Create(name)
		if err != nil {
			t.Fatalf("create Windows VCLibs test Appx entry %s: %v", name, err)
		}
		if _, err := entry.Write(content); err != nil {
			t.Fatalf("write Windows VCLibs test Appx entry %s: %v", name, err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close Windows VCLibs test Appx: %v", err)
	}
	return buffer.Bytes()
}

func windowsVCLibsDesktopTestSHA256(payload []byte) string {
	digest := sha256.Sum256(payload)
	return hex.EncodeToString(digest[:])
}
