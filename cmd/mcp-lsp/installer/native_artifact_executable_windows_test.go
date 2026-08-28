//go:build windows

package installer

import (
	"os"
	"testing"
)

func TestNativeArtifactExecutableModeDoesNotRequirePOSIXBitsOnWindows(t *testing.T) {
	if !nativeArtifactExecutableModeValid(os.FileMode(0o600)) {
		t.Fatal("Windows executable mode rejected a regular file because POSIX execute bits were absent")
	}
}
