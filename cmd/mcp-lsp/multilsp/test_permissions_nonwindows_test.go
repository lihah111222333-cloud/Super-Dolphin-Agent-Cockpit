//go:build !windows

package multilsp

import (
	"os"
	"testing"
)

func makeCacheDirUnwritableForTest(t *testing.T, dir string) {
	t.Helper()
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatalf("chmod cache dir: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })
}

func isSymlinkPrivilegeNotHeld(error) bool {
	return false
}
