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
	probe := filepath.Join(absorbedDir, ".probe_delete")
	_ = os.WriteFile(probe, []byte("test"), 0o600)
	if err := os.Remove(probe); err == nil {
		t.Skip("environment ignores read-only directory delete permissions (e.g. running as root)")
	}
}
