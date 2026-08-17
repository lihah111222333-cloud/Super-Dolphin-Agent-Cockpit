//go:build windows

package installer

import (
	"errors"
	"strings"
	"testing"
)

func TestWindowsASTGrepAssetFactsAreLockedAndNative(t *testing.T) {
	facts := WindowsASTGrepAssetFacts()
	for _, architecture := range []string{WindowsHostArchARM64, WindowsHostArchX64} {
		fact, ok := facts[architecture]
		if !ok {
			t.Fatalf("ast-grep asset facts missing %q", architecture)
		}
		if fact.Architecture != architecture || fact.NativePackage == "" || fact.NativePackageURL == "" {
			t.Fatalf("ast-grep asset fact incomplete for %q: %#v", architecture, fact)
		}
		if fact.NativePackageSHA256 == "" || fact.ExecutableSHA256 == "" || fact.PEMachine == WindowsImageFileMachineUnknown {
			t.Fatalf("ast-grep asset identity incomplete for %q: %#v", architecture, fact)
		}
	}
	manifest := WindowsASTGrepNativeManifest()
	if err := manifest.Validate(); err != nil {
		t.Fatalf("native ast-grep manifest is invalid: %v", err)
	}
	for architecture, asset := range manifest.Assets {
		if asset.URL != facts[architecture].NativePackageURL || asset.SHA256 != facts[architecture].NativePackageSHA256 {
			t.Fatalf("native manifest drift for %q: %#v", architecture, asset)
		}
		if asset.Format != WindowsLockedAssetFormatTarGz || asset.BinaryPath != WindowsASTGrepNativeBinaryPath {
			t.Fatalf("native manifest extraction contract drift for %q: %#v", architecture, asset)
		}
	}
}

func TestWindowsASTGrepAssetRejectsUnsupportedArchitecture(t *testing.T) {
	if _, err := WindowsASTGrepAssetForPlatform(WindowsHostPlatform{OS: WindowsHostOSWindows, NativeArch: WindowsHostArchX86}); err == nil {
		t.Fatal("x86 ast-grep asset selection succeeded, want typed unsupported architecture")
	}
	if _, err := WindowsASTGrepNativeExecutablePath(`C:\product`, WindowsHostArchX86); err == nil {
		t.Fatal("x86 ast-grep native path succeeded, want unsupported architecture")
	} else if errors.Is(err, ErrWindowsUnsupportedAssetArchitecture) {
		t.Logf("x86 rejected as expected: %v", err)
	}
}

func TestWindowsASTGrepNativeExecutablePathIsStableAndExplicit(t *testing.T) {
	path, err := WindowsASTGrepNativeExecutablePath(`C:\product`, WindowsHostArchARM64)
	if err != nil {
		t.Fatal(err)
	}
	for _, part := range []string{"cache", WindowsLSPAssetCacheSubdir, WindowsASTGrepNativeAssetName, WindowsASTGrepVersion, WindowsHostArchARM64, "ready", "package", "ast-grep.exe"} {
		if !strings.Contains(strings.ToLower(path), strings.ToLower(part)) {
			t.Fatalf("native ast-grep path %q misses %q", path, part)
		}
	}
}
