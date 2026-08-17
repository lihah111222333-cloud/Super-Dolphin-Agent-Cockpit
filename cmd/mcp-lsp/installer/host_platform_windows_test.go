//go:build windows

package installer

import (
	"slices"
	"testing"
)

func TestDetectHostPlatformWindows(t *testing.T) {
	platform, err := DetectWindowsHostPlatform()
	if err != nil {
		t.Fatalf("DetectWindowsHostPlatform() error = %v", err)
	}
	if platform.OS != WindowsHostOSWindows {
		t.Fatalf("DetectWindowsHostPlatform().OS = %q, want %q", platform.OS, WindowsHostOSWindows)
	}
	supported := []string{WindowsHostArchARM64, WindowsHostArchX64, WindowsHostArchX86}
	if !slices.Contains(supported, platform.NativeArch) {
		t.Fatalf("DetectWindowsHostPlatform().NativeArch = %q, want one of %v", platform.NativeArch, supported)
	}
	if !slices.Contains(supported, platform.ProcessArch) {
		t.Fatalf("DetectWindowsHostPlatform().ProcessArch = %q, want one of %v", platform.ProcessArch, supported)
	}
	if platform.Arch != platform.NativeArch {
		t.Fatalf("DetectWindowsHostPlatform().Arch = %q, want native architecture %q", platform.Arch, platform.NativeArch)
	}
	if platform.WindowsVersion == "" || platform.WindowsBuild == 0 {
		t.Fatalf("DetectWindowsHostPlatform() version/build = %q/%d, want non-empty", platform.WindowsVersion, platform.WindowsBuild)
	}
}

func TestDetectWindowsArchitecturesUsesNativeProcessFacts(t *testing.T) {
	processArch, nativeArch, err := detectWindowsArchitectures()
	if err != nil {
		t.Fatalf("detectWindowsArchitectures() error = %v", err)
	}
	supported := []string{WindowsHostArchARM64, WindowsHostArchX64, WindowsHostArchX86}
	if !slices.Contains(supported, nativeArch) || !slices.Contains(supported, processArch) {
		t.Fatalf("detectWindowsArchitectures() = process=%q native=%q, want supported architecture facts", processArch, nativeArch)
	}
}
