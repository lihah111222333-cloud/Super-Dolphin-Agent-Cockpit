//go:build e2e && windows

package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/installer"
)

// realNodeProvisionWindowsVCLibsDesktopAppLocal 自动选择当前 Windows 原生架构的微软 Appx，在测试私有缓存中安装应用本地 VC++ DLL。
func realNodeProvisionWindowsVCLibsDesktopAppLocal(t *testing.T) {
	t.Helper()
	productRoot := t.TempDir()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	runtimeRoot, err := installer.ProvisionWindowsVCLibsDesktopAppLocal(ctx, productRoot, nil)
	if err != nil {
		t.Fatalf("automatically provision Windows VCLibs Desktop app-local runtime: %v", err)
	}
	resolvedRoot, err := installer.ResolveWindowsVCLibsDesktopAppLocalPath(productRoot)
	if err != nil {
		t.Fatalf("read-only verify complete Windows VCLibs Appx tree: %v", err)
	}
	if filepath.Clean(resolvedRoot) != filepath.Clean(runtimeRoot) {
		t.Fatalf("read-only Windows VCLibs root = %q, provisioned root = %q", resolvedRoot, runtimeRoot)
	}
	processRoot, err := installer.ResolveWindowsVCLibsDesktopAppLocalProcessPath(productRoot)
	if err != nil {
		t.Fatalf("resolve rooted Windows VCLibs 8.3 process path: %v", err)
	}
	canonicalInfo, err := os.Stat(runtimeRoot)
	if err != nil {
		t.Fatalf("stat canonical Windows VCLibs root: %v", err)
	}
	processInfo, err := os.Stat(processRoot)
	if err != nil {
		t.Fatalf("stat Windows VCLibs process root: %v", err)
	}
	if !os.SameFile(canonicalInfo, processInfo) {
		t.Fatal("Windows VCLibs 8.3 process root changed ready-tree identity")
	}
	platform, err := installer.DetectWindowsHostPlatform()
	if err != nil {
		t.Fatalf("detect Windows platform after VCLibs Desktop provision: %v", err)
	}
	asset, err := installer.WindowsVCLibsDesktopAssetForPlatform(platform)
	if err != nil {
		t.Fatalf("select Windows VCLibs Desktop receipt asset: %v", err)
	}
	t.Setenv("SUPER_DOLPHIN_MSVC_RUNTIME_DIR", processRoot)
	t.Logf("Windows VCLibs Desktop app-local ready: os=%s native_arch=%s process_arch=%s version=%s url=%s sha256=%s root=%s process_root=%s",
		platform.OS, platform.NativeArch, platform.ProcessArch, asset.Version, asset.URL, asset.SHA256, runtimeRoot, processRoot)
}
