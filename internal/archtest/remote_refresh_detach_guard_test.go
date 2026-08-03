package archtest_test

import (
	"os"
	"path/filepath"
	"testing"
)

// TestRemoteRefreshExecutorIsAbsent prevents a detached repository worker from
// reintroducing a successor ImageCache execution path.
func TestRemoteRefreshExecutorIsAbsent(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("resolve repository root: %v", err)
	}
	for _, relativePath := range []string{
		"cmd/super-dolphin-gate/remote_refresh.go",
		"cmd/super-dolphin-gate/remote_refresh_process.go",
	} {
		path := filepath.Join(root, filepath.FromSlash(relativePath))
		if _, err := os.Stat(path); err == nil {
			t.Errorf("repository successor refresh executor must be deleted: %s", relativePath)
		} else if !os.IsNotExist(err) {
			t.Fatalf("stat %s: %v", relativePath, err)
		}
	}
}
