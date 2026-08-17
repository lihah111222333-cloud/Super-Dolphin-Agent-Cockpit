//go:build windows && e2e

package installer

import (
	"fmt"
	"strings"
	"testing"
)

// windowsE2EPlatformCaseName 为 Windows 长测生成可审计名称，使宿主、原生架构、进程架构和产品一眼可见。
func windowsE2EPlatformCaseName(platform WindowsHostPlatform, product string) (string, error) {
	if platform.OS != WindowsHostOSWindows {
		return "", fmt.Errorf("Windows E2E case requires os=%q, got %q", WindowsHostOSWindows, platform.OS)
	}
	nativeArch, err := NormalizeWindowsArchitectureAlias(platform.NativeArch)
	if err != nil {
		return "", fmt.Errorf("normalize Windows E2E native architecture: %w", err)
	}
	processArch, err := NormalizeWindowsArchitectureAlias(platform.ProcessArch)
	if err != nil {
		return "", fmt.Errorf("normalize Windows E2E process architecture: %w", err)
	}
	product = strings.TrimSpace(product)
	if product == "" || strings.ContainsAny(product, `/\\`) {
		return "", fmt.Errorf("Windows E2E product must be a non-empty path-free name, got %q", product)
	}
	return fmt.Sprintf("windows-native-%s-process-%s-%s", nativeArch, processArch, product), nil
}

func TestWindowsE2EPlatformCaseNameShowsWindowsNativeAndProcessArchitecture(t *testing.T) {
	testCases := []struct {
		name     string
		platform WindowsHostPlatform
		want     string
	}{
		{
			name:     "arm64-native",
			platform: WindowsHostPlatform{OS: WindowsHostOSWindows, NativeArch: "arm64", ProcessArch: "arm64"},
			want:     "windows-native-arm64-process-arm64-clangd",
		},
		{
			name:     "x64-native-x86-process",
			platform: WindowsHostPlatform{OS: WindowsHostOSWindows, NativeArch: "amd64", ProcessArch: "ia32"},
			want:     "windows-native-x64-process-x86-clangd",
		},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			got, err := windowsE2EPlatformCaseName(testCase.platform, "clangd")
			if err != nil {
				t.Fatal(err)
			}
			if got != testCase.want {
				t.Fatalf("windowsE2EPlatformCaseName() = %q, want %q", got, testCase.want)
			}
		})
	}
}

func TestWindowsE2EPlatformCaseNameRejectsIncompleteOrPathLikeInputs(t *testing.T) {
	testCases := []struct {
		name     string
		platform WindowsHostPlatform
		product  string
	}{
		{name: "non-Windows", platform: WindowsHostPlatform{OS: "linux", NativeArch: "arm64", ProcessArch: "arm64"}, product: "clangd"},
		{name: "unknown native", platform: WindowsHostPlatform{OS: WindowsHostOSWindows, NativeArch: "", ProcessArch: "arm64"}, product: "clangd"},
		{name: "unknown process", platform: WindowsHostPlatform{OS: WindowsHostOSWindows, NativeArch: "arm64", ProcessArch: ""}, product: "clangd"},
		{name: "empty product", platform: WindowsHostPlatform{OS: WindowsHostOSWindows, NativeArch: "arm64", ProcessArch: "arm64"}},
		{name: "path product", platform: WindowsHostPlatform{OS: WindowsHostOSWindows, NativeArch: "arm64", ProcessArch: "arm64"}, product: `catalog/clangd`},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			if got, err := windowsE2EPlatformCaseName(testCase.platform, testCase.product); err == nil || got != "" {
				t.Fatalf("windowsE2EPlatformCaseName() = %q, %v; want empty result and error", got, err)
			}
		})
	}
}
