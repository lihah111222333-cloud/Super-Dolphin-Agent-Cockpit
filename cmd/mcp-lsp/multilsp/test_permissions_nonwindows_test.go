//go:build !windows

package multilsp

import (
	"os"
	"testing"
)

func makeCacheDirUnwritableForTest(t *testing.T, dir string) {
	t.Helper()
	if err := os.RemoveAll(dir); err != nil {
		t.Fatalf("remove cache dir: %v", err)
	}
	if err := os.WriteFile(dir, []byte("cache directory blocker"), 0o600); err != nil {
		t.Fatalf("create cache directory blocker: %v", err)
	}
	t.Cleanup(func() {
		if err := os.Remove(dir); err != nil {
			t.Errorf("remove cache directory blocker: %v", err)
			return
		}
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Errorf("restore cache directory: %v", err)
		}
	})
}

func isSymlinkPrivilegeNotHeld(error) bool {
	return false
}
