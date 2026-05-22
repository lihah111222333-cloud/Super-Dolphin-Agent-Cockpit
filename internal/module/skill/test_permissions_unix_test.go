//go:build !windows

package skill

import (
	"os"
	"testing"
)

func makeOwnerOnlyFileBroadForTest(t *testing.T, path string) {
	t.Helper()
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatalf("Chmod policy: %v", err)
	}
}
