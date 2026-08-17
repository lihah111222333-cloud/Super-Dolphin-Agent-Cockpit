//go:build windows

package installer

import (
	"testing"
)

func TestWindowsTerraformCLIAssetsArePinnedForEachNativeArchitecture(t *testing.T) {
	for _, platform := range []WindowsHostPlatform{
		{OS: WindowsHostOSWindows, NativeArch: WindowsHostArchARM64, ProcessArch: WindowsHostArchARM64, WindowsVersion: "10.0", WindowsBuild: 22621},
		{OS: WindowsHostOSWindows, NativeArch: WindowsHostArchX64, ProcessArch: WindowsHostArchX64, WindowsVersion: "10.0", WindowsBuild: 22621},
		{OS: WindowsHostOSWindows, NativeArch: WindowsHostArchX86, ProcessArch: WindowsHostArchX86, WindowsVersion: "10.0", WindowsBuild: 22621},
	} {
		asset, err := WindowsTerraformCLIAssetForPlatform(platform)
		if err != nil {
			t.Fatalf("asset for %s: %v", platform.NativeArch, err)
		}
		if asset.Version != windowsTerraformCLIVersion || asset.BinaryPath != "terraform.exe" || asset.Format != WindowsLockedAssetFormatZip {
			t.Fatalf("asset for %s = %#v", platform.NativeArch, asset)
		}
		if err := (WindowsLockedAssetManifest{Name: "terraform-cli", Assets: map[string]WindowsLockedAsset{asset.Architecture: asset}}).Validate(); err != nil {
			t.Fatalf("asset manifest for %s: %v", platform.NativeArch, err)
		}
	}
}

func TestResolveWindowsTerraformCLIPathDoesNotUseExternalPATH(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	if _, err := ResolveWindowsTerraformCLIPath(t.TempDir()); err == nil {
		t.Fatal("ResolveWindowsTerraformCLIPath unexpectedly used external PATH")
	}
}
