//go:build windows

package main

import (
	"errors"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/installer"
)

func TestWindowsLuaBinarySelectionIsArchitectureExact(t *testing.T) {
	cases := []struct {
		architecture string
		want         string
	}{
		{architecture: installer.WindowsHostArchARM64, want: installer.WindowsEmmyLuaBinaryName},
		{architecture: installer.WindowsHostArchX64, want: "lua-language-server.exe"},
		{architecture: installer.WindowsHostArchX86, want: "lua-language-server.exe"},
	}
	for _, tc := range cases {
		got, err := windowsLuaBinaryForArchitecture(tc.architecture)
		if err != nil {
			t.Errorf("windowsLuaBinaryForArchitecture(%q) error = %v", tc.architecture, err)
			continue
		}
		if got != tc.want {
			t.Errorf("windowsLuaBinaryForArchitecture(%q) = %q, want %q", tc.architecture, got, tc.want)
		}
	}
	if _, err := windowsLuaBinaryForArchitecture("mips64"); !errors.Is(err, installer.ErrUnsupportedWindowsHostArchitecture) {
		t.Fatalf("unknown architecture error = %v, want typed unsupported architecture", err)
	}
}

func TestWindowsEmmyLuaInstallerUsesRealBinaryName(t *testing.T) {
	cfg := windowsEmmyLuaInstallerConfig(t.TempDir(), nil)
	if cfg.BinaryName != installer.WindowsEmmyLuaBinaryName {
		t.Fatalf("EmmyLua installer BinaryName = %q, want %q", cfg.BinaryName, installer.WindowsEmmyLuaBinaryName)
	}
	if cfg.InstallLockKey != "windows-native-"+string(installer.WindowsLSPProductEmmyLua) {
		t.Fatalf("EmmyLua installer lock key = %q, want independent product lock", cfg.InstallLockKey)
	}
	if cfg.InstallAction == nil || cfg.InstalledBinaryPathResolver == nil || cfg.InstalledReadinessValidator == nil {
		t.Fatal("EmmyLua installer must declare action, explicit path resolver, and PE readiness validator")
	}
}

func TestWindowsLuaInstallerDoesNotFallbackOnProductRootError(t *testing.T) {
	host, err := installer.DetectWindowsHostPlatform()
	if err != nil {
		t.Fatalf("DetectWindowsHostPlatform() error = %v", err)
	}
	cfg := windowsLuaInstallerConfig(t.TempDir(), errors.New("synthetic product-root failure"))
	if host.NativeArch == installer.WindowsHostArchARM64 {
		if cfg.BinaryName != installer.WindowsEmmyLuaBinaryName {
			t.Fatalf("ARM64 Lua installer BinaryName = %q, want independent EmmyLua binary despite product-root error", cfg.BinaryName)
		}
		if cfg.InstallLockKey != "windows-native-"+string(installer.WindowsLSPProductEmmyLua) {
			t.Fatalf("ARM64 Lua installer lock key = %q, want EmmyLua lock", cfg.InstallLockKey)
		}
		return
	}
	if cfg.BinaryName != "lua-language-server.exe" {
		t.Fatalf("non-ARM64 Lua installer BinaryName = %q, want LuaLS", cfg.BinaryName)
	}
}
