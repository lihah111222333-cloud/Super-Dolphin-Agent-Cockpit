//go:build !windows

package memory

import (
	"os"
	"path/filepath"
	"testing"
)

func makeAbsorbedEntryDeleteFail(t *testing.T, absorbedPath string) {
	t.Helper()
	absorbedDir := filepath.Dir(absorbedPath)
	if err := os.Chmod(absorbedDir, 0o555); err != nil {
		t.Fatalf("Chmod(absorbedDir read-only) error = %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(absorbedDir, 0o755) })
}
