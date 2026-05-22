package skill

import (
	"runtime"
	"testing"
)

func skipWindowsShortMirrorIntegration(t *testing.T) {
	t.Helper()
	if testing.Short() && runtime.GOOS == "windows" {
		t.Skip("skipping Windows short-mode mirror integration; covered by full test matrix")
	}
}
