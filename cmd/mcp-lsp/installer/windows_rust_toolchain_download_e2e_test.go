//go:build windows && e2e

package installer

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const (
	windowsRustToolchainDownloadE2EEnv     = "MCP_LSP_WINDOWS_RUST_TOOLCHAIN_DOWNLOAD_E2E"
	windowsRustToolchainProductRootE2EEnv  = "MCP_LSP_WINDOWS_RUST_TOOLCHAIN_PRODUCT_ROOT"
	windowsRustToolchainDownloadE2ETimeout = 45 * time.Minute
)

// TestWindowsNativeArchitectureRustToolchainDownloadInstallE2E 证明当前 Windows
// NativeArch 能下载并安装锁定 Rust 工具链；完整缓存已存在时只做只读复验，不重复下载。
func TestWindowsNativeArchitectureRustToolchainDownloadInstallE2E(t *testing.T) {
	if os.Getenv(windowsRustToolchainDownloadE2EEnv) != "1" {
		t.Skipf("set %s=1 to run the Windows NativeArch Rust toolchain download/install proof", windowsRustToolchainDownloadE2EEnv)
	}
	platform, err := DetectWindowsHostPlatform()
	if err != nil {
		t.Fatalf("detect Windows host platform: %v", err)
	}
	productRoot := strings.TrimSpace(os.Getenv(windowsRustToolchainProductRootE2EEnv))
	if productRoot == "" {
		wd, err := os.Getwd()
		if err != nil {
			t.Fatalf("get installer package working directory: %v", err)
		}
		productRoot = filepath.Join(wd, "..", "..", "..", ".super-dolphin")
	}
	productRoot, err = filepath.Abs(productRoot)
	if err != nil {
		t.Fatalf("resolve Windows Rust toolchain product root: %v", err)
	}

	_, cachedErr := ResolveWindowsRustToolchain(productRoot)
	started := time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), windowsRustToolchainDownloadE2ETimeout)
	defer cancel()
	installed, err := EnsureWindowsRustToolchain(ctx, productRoot, nil)
	if err != nil {
		t.Fatalf("ensure Windows %s Rust toolchain under product root: %v", platform.NativeArch, err)
	}
	resolved, err := ResolveWindowsRustToolchain(productRoot)
	if err != nil {
		t.Fatalf("resolve installed Windows %s Rust toolchain: %v", platform.NativeArch, err)
	}
	if installed != resolved {
		t.Fatalf("installed paths differ from read-only resolver: installed=%#v resolved=%#v", installed, resolved)
	}
	mode := "download_install"
	if cachedErr == nil {
		mode = "cache_hit_no_download"
	}
	t.Logf("platform=windows native_arch=%s process_arch=%s windows_version=%s windows_build=%d mode=%s duration=%s cargo=%s rustc=%s",
		platform.NativeArch,
		platform.ProcessArch,
		platform.WindowsVersion,
		platform.WindowsBuild,
		mode,
		time.Since(started).Round(time.Millisecond),
		filepath.Base(resolved.CargoPath),
		filepath.Base(resolved.RustcPath),
	)
}
