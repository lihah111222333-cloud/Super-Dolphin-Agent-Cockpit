//go:build windows

package main

import (
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/installer"
)

func TestWindowsProductionInstallActionsUseExtendedTimeout(t *testing.T) {
	if windowsProductionInstallTimeout < 45*time.Minute {
		t.Fatalf("Windows production install timeout = %s, want at least 45m", windowsProductionInstallTimeout)
	}

	spec := runtimeNPMInstallerSpecsForPlatform("windows")[0]
	packages, err := runtimeNPMExactPackages(spec.args)
	if err != nil {
		t.Fatalf("runtimeNPMExactPackages(%v): %v", spec.args, err)
	}
	nodeCfg := windowsNodeRuntimeInstallerConfig("C:\\windows-test-product", nil, spec, packages, nil)
	if nodeCfg.InstallTimeout != windowsProductionInstallTimeout {
		t.Fatalf("Windows Node InstallTimeout = %s, want %s", nodeCfg.InstallTimeout, windowsProductionInstallTimeout)
	}

	nativeCfg := windowsNativeCatalogInstallerConfig("C:\\windows-test-product", nil, windowsNativeCatalogInstallerSpec{
		product: installer.WindowsLSPProductClangd,
		binary:  "clangd.exe",
	})
	if nativeCfg.InstallTimeout != windowsProductionInstallTimeout {
		t.Fatalf("Windows native catalog InstallTimeout = %s, want %s", nativeCfg.InstallTimeout, windowsProductionInstallTimeout)
	}

	shellCfg := windowsShellRuntimeInstallerConfig("C:\\windows-test-product", nil)
	if shellCfg.InstallTimeout != windowsProductionInstallTimeout {
		t.Fatalf("Windows shell InstallTimeout = %s, want %s", shellCfg.InstallTimeout, windowsProductionInstallTimeout)
	}
}

func TestWindowsSQLInstallerUsesProductOwnedGoSQLS(t *testing.T) {
	cfg := windowsSQLInstallerConfig("C:/windows-test-product", nil)
	if cfg.BinaryName != installer.WindowsGoSQLSBinaryName {
		t.Fatalf("Windows SQL BinaryName = %q, want %q", cfg.BinaryName, installer.WindowsGoSQLSBinaryName)
	}
	if cfg.InstallCmd != "" || len(cfg.InstallArgs) != 0 {
		t.Fatalf("Windows SQL installer exposes host command fields: cmd=%q args=%v", cfg.InstallCmd, cfg.InstallArgs)
	}
	if cfg.InstallTimeout != windowsProductionInstallTimeout {
		t.Fatalf("Windows SQL InstallTimeout = %s, want %s", cfg.InstallTimeout, windowsProductionInstallTimeout)
	}
	if cfg.InstallAction == nil {
		t.Fatal("Windows SQL installer InstallAction is nil")
	}
	if cfg.InstalledBinaryPathResolver == nil {
		t.Fatal("Windows SQL installer InstalledBinaryPathResolver is nil")
	}
	if cfg.InstalledReadinessValidator == nil {
		t.Fatal("Windows SQL installer InstalledReadinessValidator is nil")
	}
	if cfg.InstallLockKey != runtimeWindowsSQLSInstallLockKey {
		t.Fatalf("Windows SQL InstallLockKey = %q, want %q", cfg.InstallLockKey, runtimeWindowsSQLSInstallLockKey)
	}
}
