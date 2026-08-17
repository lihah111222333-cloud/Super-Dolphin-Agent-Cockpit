//go:build windows && arm64 && e2e

package installer

import (
	"os"
	"testing"
)

const windowsHostIdentityE2EEnv = "MCP_LSP_WINDOWS_HOST_IDENTITY_E2E"

// TestDetectWindowsHostPlatformCurrentARM64Build26100E2E 在指定 Windows ARM64 证明主机上校验真实 NativeArch、ProcessArch、版本和 build。
func TestDetectWindowsHostPlatformCurrentARM64Build26100E2E(t *testing.T) {
	if os.Getenv(windowsHostIdentityE2EEnv) != "1" {
		t.Skip("set MCP_LSP_WINDOWS_HOST_IDENTITY_E2E=1 on the designated Windows ARM64 proof host")
	}
	platform, err := DetectWindowsHostPlatform()
	if err != nil {
		t.Fatalf("DetectWindowsHostPlatform() error = %v", err)
	}
	if platform.NativeArch != WindowsHostArchARM64 || platform.ProcessArch != WindowsHostArchARM64 {
		t.Fatalf("DetectWindowsHostPlatform() = process=%q native=%q, want arm64/arm64", platform.ProcessArch, platform.NativeArch)
	}
	if platform.WindowsVersion != "10.0" || platform.WindowsBuild != 26100 {
		t.Fatalf("DetectWindowsHostPlatform() version/build = %q/%d, want 10.0/26100", platform.WindowsVersion, platform.WindowsBuild)
	}
	t.Logf("NON_PASS TARGETED_DIAGNOSTIC/NON_LIFECYCLE Windows host identity fact native=%s process=%s build=%d; no server lifecycle asserted", platform.NativeArch, platform.ProcessArch, platform.WindowsBuild)
}
