//go:build windows

package installer

import (
	"strings"
	"testing"
)

func TestWindowsRustfmtAssetsArePinnedForEachNativeArchitecture(t *testing.T) {
	for _, platform := range []WindowsHostPlatform{
		{OS: WindowsHostOSWindows, NativeArch: WindowsHostArchARM64, ProcessArch: WindowsHostArchARM64, WindowsVersion: "10.0", WindowsBuild: 26100},
		{OS: WindowsHostOSWindows, NativeArch: WindowsHostArchX64, ProcessArch: WindowsHostArchX64, WindowsVersion: "10.0", WindowsBuild: 26100},
		{OS: WindowsHostOSWindows, NativeArch: WindowsHostArchX86, ProcessArch: WindowsHostArchX86, WindowsVersion: "10.0", WindowsBuild: 26100},
	} {
		asset, err := WindowsRustfmtAssetForPlatform(platform)
		if err != nil {
			t.Fatalf("asset for %s: %v", platform.NativeArch, err)
		}
		if asset.Version != windowsRustfmtVersion || !strings.Contains(asset.BinaryPath, "/rustfmt-preview/bin/rustfmt.exe") || asset.Format != WindowsLockedAssetFormatTarXz {
			t.Fatalf("asset for %s = %#v", platform.NativeArch, asset)
		}
		if err := (WindowsLockedAssetManifest{Name: "rustfmt", Assets: map[string]WindowsLockedAsset{asset.Architecture: asset}}).Validate(); err != nil {
			t.Fatalf("asset manifest for %s: %v", platform.NativeArch, err)
		}
	}
}

func TestResolveWindowsRustfmtPathDoesNotUseExternalPATH(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	if _, err := ResolveWindowsRustfmtPath(t.TempDir()); err == nil {
		t.Fatal("ResolveWindowsRustfmtPath unexpectedly used external PATH")
	}
}
