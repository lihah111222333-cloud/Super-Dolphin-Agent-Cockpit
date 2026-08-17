//go:build windows

package main

import (
	"errors"
	"testing"

	lspinstaller "github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/installer"
)

// TestSetupInstallerRegistersShellLanguageServer 验证 Windows 生产 shell
// 配置：x64 由真实 Node E2E 覆盖，其他 NativeArch 必须返回 typed optional gap。
func TestSetupInstallerRegistersShellLanguageServer(t *testing.T) {
	platform, err := lspinstaller.DetectWindowsHostPlatform()
	if err != nil {
		t.Fatalf("DetectWindowsHostPlatform(): %v", err)
	}
	cfg, cfgErr := runtimeShellNPMInstallerConfigForProduction("windows")
	if cfgErr != nil {
		t.Fatalf("runtimeShellNPMInstallerConfigForProduction(): %v", cfgErr)
	}
	if platform.NativeArch != lspinstaller.WindowsHostArchX64 {
		var optionalGap *lspinstaller.UnsupportedPlatformError
		if !errors.As(cfg.OptionalUnsupportedPlatform, &optionalGap) {
			t.Fatalf("native %s shellcheck gap = %v, want typed optional UnsupportedPlatformError", platform.NativeArch, cfg.OptionalUnsupportedPlatform)
		}
		if len(cfg.RequiredBinaries) != 0 {
			t.Fatalf("native %s shell required binaries = %#v, want bash-only config", platform.NativeArch, cfg.RequiredBinaries)
		}
		return
	}
	t.Skip("Windows production shell installer is covered by the locked Node E2E; PATH binaries are intentionally ignored")
}

// TestSetupInstallerInstallsShellcheckWhenShellServerAlreadyExists 验证
// Windows 非 x64 NativeArch 的 typed optional gap；x64 由真实 Node E2E 覆盖。
func TestSetupInstallerInstallsShellcheckWhenShellServerAlreadyExists(t *testing.T) {
	platform, err := lspinstaller.DetectWindowsHostPlatform()
	if err != nil {
		t.Fatalf("DetectWindowsHostPlatform(): %v", err)
	}
	if platform.NativeArch == lspinstaller.WindowsHostArchX64 {
		t.Skip("Windows production shell installer is covered by the locked Node E2E; PATH npm is intentionally ignored")
	}
	cfg, cfgErr := runtimeShellNPMInstallerConfigForProduction("windows")
	if cfgErr != nil {
		t.Fatalf("runtimeShellNPMInstallerConfigForProduction(): %v", cfgErr)
	}
	var optionalGap *lspinstaller.UnsupportedPlatformError
	if !errors.As(cfg.OptionalUnsupportedPlatform, &optionalGap) {
		t.Fatalf("Windows native %s shellcheck gap = %v, want typed optional UnsupportedPlatformError", platform.NativeArch, cfg.OptionalUnsupportedPlatform)
	}
	if len(cfg.RequiredBinaries) != 0 {
		t.Fatalf("Windows native %s required binaries = %#v, want bash-only config", platform.NativeArch, cfg.RequiredBinaries)
	}
}
