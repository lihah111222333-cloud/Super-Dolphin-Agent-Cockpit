//go:build windows

package installer

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestWindowsRustToolchainBootstrapAssetsArePinnedForEachNativeArchitecture(t *testing.T) {
	want := map[string]struct {
		url    string
		sha256 string
	}{
		WindowsHostArchARM64: {url: "https://static.rust-lang.org/rustup/archive/1.28.2/aarch64-pc-windows-msvc/rustup-init.exe", sha256: "de9f7d29ccd39efa59a3dda3ec363b396e09b92681229b9b8f6aaa4c84285e9c"},
		WindowsHostArchX64:   {url: "https://static.rust-lang.org/rustup/archive/1.28.2/x86_64-pc-windows-msvc/rustup-init.exe", sha256: "88d8258dcf6ae4f7a80c7d1088e1f36fa7025a1cfd1343731b4ee6f385121fc0"},
		WindowsHostArchX86:   {url: "https://static.rust-lang.org/rustup/archive/1.28.2/i686-pc-windows-msvc/rustup-init.exe", sha256: "d33375f474f105e529ff3225529a8d6a79a8a4e23f6eab88fba427889e538f34"},
	}
	for arch, expected := range want {
		asset, err := WindowsRustToolchainBootstrapAssetForPlatform(WindowsHostPlatform{
			OS: WindowsHostOSWindows, NativeArch: arch, ProcessArch: arch,
			WindowsVersion: "10.0", WindowsBuild: windowsLSPCatalogMinWindowsBuild,
		})
		if err != nil {
			t.Fatalf("asset %s: %v", arch, err)
		}
		if asset.URL != expected.url || asset.SHA256 != expected.sha256 || asset.Format != WindowsLockedAssetFormatRaw || asset.BinaryPath != "rustup-init.exe" {
			t.Fatalf("asset %s = %#v, want URL/SHA/raw rustup-init", arch, asset)
		}
	}
}

func TestWindowsRustToolchainPathsRequireNativeRustStd(t *testing.T) {
	platform, err := DetectWindowsHostPlatform()
	if err != nil {
		t.Fatalf("DetectWindowsHostPlatform: %v", err)
	}
	root := t.TempDir()
	paths, err := windowsRustToolchainPaths(root, platform.NativeArch)
	if err != nil {
		t.Fatalf("windowsRustToolchainPaths: %v", err)
	}
	target, err := windowsRustTargetTriple(platform.NativeArch)
	if err != nil {
		t.Fatalf("windowsRustTargetTriple: %v", err)
	}
	wantToolchainRoot := filepath.Join(paths.RustupHome, "toolchains", windowsRustToolchainVersion+"-"+target)
	if paths.toolchainRoot != wantToolchainRoot {
		t.Fatalf("toolchainRoot = %q, want %q", paths.toolchainRoot, wantToolchainRoot)
	}
	wantCargoPath := filepath.Join(wantToolchainRoot, "bin", "cargo.exe")
	if paths.CargoPath != wantCargoPath {
		t.Fatalf("CargoPath = %q, want %q", paths.CargoPath, wantCargoPath)
	}
	wantRustcPath := filepath.Join(wantToolchainRoot, "bin", "rustc.exe")
	if paths.RustcPath != wantRustcPath {
		t.Fatalf("RustcPath = %q, want %q", paths.RustcPath, wantRustcPath)
	}
	wantRustStdLibDir := filepath.Join(wantToolchainRoot, "lib", "rustlib", target, "lib")
	if paths.rustStdLibDir != wantRustStdLibDir {
		t.Fatalf("rustStdLibDir = %q, want %q", paths.rustStdLibDir, wantRustStdLibDir)
	}

	writeRustToolchainPEFixture(t, paths.CargoPath)
	writeRustToolchainPEFixture(t, paths.RustcPath)
	if _, err := resolveWindowsRustToolchainForPlatform(root, platform); err == nil || !strings.Contains(strings.ToLower(err.Error()), "std") {
		t.Fatalf("resolveWindowsRustToolchainForPlatform missing rust-std error = %v", err)
	}
	if err := os.MkdirAll(paths.rustStdLibDir, 0o700); err != nil {
		t.Fatalf("MkdirAll rust-std directory: %v", err)
	}
	if err := os.WriteFile(filepath.Join(paths.rustStdLibDir, "libstd-test.rlib"), []byte("rust-std"), 0o600); err != nil {
		t.Fatalf("WriteFile rust-std fixture: %v", err)
	}
	if _, err := resolveWindowsRustToolchainForPlatform(root, platform); err != nil {
		t.Fatalf("resolveWindowsRustToolchainForPlatform with rust-std: %v", err)
	}
}

func writeRustToolchainPEFixture(t *testing.T, path string) {
	t.Helper()
	source, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable: %v", err)
	}
	payload, err := os.ReadFile(source)
	if err != nil {
		t.Fatalf("ReadFile executable: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("MkdirAll %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, payload, 0o700); err != nil {
		t.Fatalf("WriteFile %s: %v", path, err)
	}
}

func TestResolveWindowsRustToolchainDoesNotUseExternalPATH(t *testing.T) {
	t.Setenv("PATH", filepath.Join(t.TempDir(), "foreign-rust"))
	_, err := ResolveWindowsRustToolchain(t.TempDir())
	if err == nil {
		t.Fatal("ResolveWindowsRustToolchain unexpectedly used external PATH")
	}
	if !strings.Contains(err.Error(), "product-owned Rust toolchain") {
		t.Fatalf("ResolveWindowsRustToolchain error = %v", err)
	}
}

func TestWindowsRustToolchainEnvironmentIsProductScoped(t *testing.T) {
	root := t.TempDir()
	paths := WindowsRustToolchainPaths{
		CargoHome:  filepath.Join(root, "cargo-home"),
		RustupHome: filepath.Join(root, "rustup-home"),
		CargoPath:  filepath.Join(root, "cargo-home", "bin", "cargo.exe"),
		RustcPath:  filepath.Join(root, "cargo-home", "bin", "rustc.exe"),
	}
	got := WindowsRustToolchainEnvironment([]string{"PATH=C:\\Windows", "CARGO_HOME=C:\\foreign", "RUSTUP_HOME=C:\\foreign"}, paths)
	wantPath := filepath.Dir(paths.CargoPath) + string(os.PathListSeparator) + `C:\Windows`
	if value := windowsRustToolchainEnvironmentValue(got, "PATH"); value != wantPath {
		t.Fatalf("PATH = %q, want %q", value, wantPath)
	}
	if value := windowsRustToolchainEnvironmentValue(got, "CARGO_HOME"); value != paths.CargoHome {
		t.Fatalf("CARGO_HOME = %q, want %q", value, paths.CargoHome)
	}
	if value := windowsRustToolchainEnvironmentValue(got, "RUSTUP_HOME"); value != paths.RustupHome {
		t.Fatalf("RUSTUP_HOME = %q, want %q", value, paths.RustupHome)
	}
}

// TestWindowsRustToolchainInstallLockSerializesSharedHomes 验证不同安装入口共享同一产品锁。
func TestWindowsRustToolchainInstallLockSerializesSharedHomes(t *testing.T) {
	platform, err := DetectWindowsHostPlatform()
	if err != nil {
		t.Fatalf("DetectWindowsHostPlatform: %v", err)
	}
	root := t.TempDir()
	lockPath, err := windowsRustToolchainInstallLockPath(root, platform.NativeArch)
	if err != nil {
		t.Fatalf("windowsRustToolchainInstallLockPath: %v", err)
	}
	wantLockPath := filepath.Join(root, "cache", windowsRustToolchainStateRoot, windowsRustToolchainVersion, platform.NativeArch, windowsRustToolchainInstallLockFile)
	if lockPath != wantLockPath {
		t.Fatalf("install lock path = %q, want %q", lockPath, wantLockPath)
	}

	first, err := acquireWindowsRustToolchainInstallLock(context.Background(), root, platform.NativeArch)
	if err != nil {
		t.Fatalf("acquire first Rust toolchain install lock: %v", err)
	}
	secondContext, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	if _, err := acquireWindowsRustToolchainInstallLock(secondContext, root, platform.NativeArch); err == nil || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("second Rust toolchain install lock error = %v, want context deadline while first lock is held", err)
	}
	if err := first.Close(); err != nil {
		t.Fatalf("release first Rust toolchain install lock: %v", err)
	}
	third, err := acquireWindowsRustToolchainInstallLock(context.Background(), root, platform.NativeArch)
	if err != nil {
		t.Fatalf("acquire released Rust toolchain install lock: %v", err)
	}
	if err := third.Close(); err != nil {
		t.Fatalf("release released Rust toolchain install lock: %v", err)
	}
}

// TestWindowsRustToolchainRejectsInvalidProductRoot 验证 Ensure/Resolve 不从 cwd 推导产品根。
func TestWindowsRustToolchainRejectsInvalidProductRoot(t *testing.T) {
	for _, productRoot := range []string{"", "relative-product-root"} {
		t.Run(productRoot, func(t *testing.T) {
			if _, err := EnsureWindowsRustToolchain(context.Background(), productRoot, nil); err == nil {
				t.Fatalf("EnsureWindowsRustToolchain(%q) unexpectedly accepted invalid product root", productRoot)
			}
			if _, err := ResolveWindowsRustToolchain(productRoot); err == nil {
				t.Fatalf("ResolveWindowsRustToolchain(%q) unexpectedly accepted invalid product root", productRoot)
			}
		})
	}
}
