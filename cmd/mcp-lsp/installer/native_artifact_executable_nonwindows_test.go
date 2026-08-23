//go:build !windows

package installer

import (
	"os"
	"testing"
)

func TestNativeArtifactExecutableModeRequiresExecuteBitOnNonWindows(t *testing.T) {
	if nativeArtifactExecutableModeValid(os.FileMode(0o600)) {
		t.Fatal("non-Windows executable mode accepted a regular file without execute bits")
	}
	if !nativeArtifactExecutableModeValid(os.FileMode(0o700)) {
		t.Fatal("non-Windows executable mode rejected an owner-executable regular file")
	}
}
