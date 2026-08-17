//go:build windows

package installer

import (
	"errors"
	"reflect"
	"testing"
)

func TestWindowsEmmyLuaManifestLocksOfficialARM64Facts(t *testing.T) {
	manifest := WindowsEmmyLuaManifest()
	if err := manifest.Validate(); err != nil {
		t.Fatalf("WindowsEmmyLuaManifest().Validate() error = %v", err)
	}
	if manifest.Name != string(WindowsLSPProductEmmyLua) {
		t.Fatalf("manifest name = %q, want %q", manifest.Name, WindowsLSPProductEmmyLua)
	}
	if len(manifest.Assets) != 1 {
		t.Fatalf("manifest asset count = %d, want one ARM64 asset", len(manifest.Assets))
	}
	asset, ok := manifest.Assets[WindowsHostArchARM64]
	if !ok {
		t.Fatal("manifest has no ARM64 asset")
	}
	if asset.Version != WindowsEmmyLuaVersion || asset.URL != WindowsEmmyLuaArchiveURL || asset.SHA256 != WindowsEmmyLuaArchiveSHA256 {
		t.Fatalf("manifest asset facts = %#v, want locked release facts", asset)
	}
	if asset.BinaryPath != WindowsEmmyLuaBinaryName || asset.Format != WindowsLockedAssetFormatZip {
		t.Fatalf("manifest executable facts = %#v, want %q zip entry", asset, WindowsEmmyLuaBinaryName)
	}
	for _, entry := range WindowsLSPCatalog() {
		if entry.Product == WindowsLSPProductEmmyLua {
			t.Fatalf("EmmyLua must remain outside LuaLS catalog: %#v", entry)
		}
	}
}

func TestWindowsEmmyLuaRejectsNonARM64WithoutFallback(t *testing.T) {
	if _, err := WindowsEmmyLuaAssetForArchitecture(WindowsHostArchX64); !errors.Is(err, ErrWindowsUnsupportedAssetArchitecture) {
		t.Fatalf("x64 selection error = %v, want typed unsupported architecture", err)
	}
	if _, err := WindowsEmmyLuaAssetForArchitecture(WindowsHostArchX86); !errors.Is(err, ErrWindowsUnsupportedAssetArchitecture) {
		t.Fatalf("x86 selection error = %v, want typed unsupported architecture", err)
	}
	platform := WindowsHostPlatform{
		OS:             WindowsHostOSWindows,
		NativeArch:     WindowsHostArchARM64,
		ProcessArch:    WindowsHostArchX64,
		WindowsVersion: "10.0",
		WindowsBuild:   windowsLSPCatalogMinWindowsBuild,
	}
	asset, err := WindowsEmmyLuaAssetForPlatform(platform)
	if err != nil {
		t.Fatalf("x64 process on ARM64 host must still select native ARM64 EmmyLua: %v", err)
	}
	if asset.Architecture != WindowsHostArchARM64 || asset.BinaryPath != WindowsEmmyLuaBinaryName {
		t.Fatalf("x64 process on ARM64 host selected asset = %#v, want ARM64 EmmyLua", asset)
	}
	platform.NativeArch = WindowsHostArchX64
	platform.ProcessArch = WindowsHostArchARM64
	if _, err := WindowsEmmyLuaAssetForPlatform(platform); !errors.Is(err, ErrWindowsEmmyLuaRequiresARM64) {
		t.Fatalf("x64 native host with ARM64 process error = %v, want native-architecture rejection", err)
	}
}

func TestWindowsEmmyLuaProvisionContractUsesRealBinaryAndStdioArgs(t *testing.T) {
	platform := WindowsHostPlatform{
		OS:             WindowsHostOSWindows,
		NativeArch:     WindowsHostArchARM64,
		ProcessArch:    WindowsHostArchARM64,
		WindowsVersion: "10.0",
		WindowsBuild:   windowsLSPCatalogMinWindowsBuild,
	}
	asset, err := WindowsEmmyLuaAssetForPlatform(platform)
	if err != nil {
		t.Fatalf("WindowsEmmyLuaAssetForPlatform() error = %v", err)
	}
	if asset.Architecture != WindowsHostArchARM64 || asset.BinaryPath != WindowsEmmyLuaBinaryName {
		t.Fatalf("selected EmmyLua asset = %#v, want ARM64 %q", asset, WindowsEmmyLuaBinaryName)
	}
	wantArgs := []string{"--communication", "stdio", "--log-level", "error", "--resources-path", "none"}
	if got := WindowsEmmyLuaCommandArguments(); !reflect.DeepEqual(got, wantArgs) {
		t.Fatalf("WindowsEmmyLuaCommandArguments() = %#v, want %#v", got, wantArgs)
	}
	if err := ValidateWindowsEmmyLuaExecutable(t.TempDir() + "\\missing\\" + WindowsEmmyLuaBinaryName); !errors.Is(err, ErrWindowsEmmyLuaBinaryInvalid) {
		t.Fatalf("missing EmmyLua executable error = %v, want locked-identity sentinel", err)
	}
}
