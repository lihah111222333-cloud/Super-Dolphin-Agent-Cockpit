package archtest_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestRemoteRefreshDetachDoesNotDependOnExternalSetsid guards the cross-platform worker launch boundary.
func TestRemoteRefreshDetachDoesNotDependOnExternalSetsid(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("resolve repository root: %v", err)
	}
	for _, relativePath := range []string{
		"cmd/super-dolphin-gate/remote_refresh.go",
		"cmd/super-dolphin-gate/remote_refresh_process.go",
	} {
		source, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(relativePath)))
		if err != nil {
			t.Fatalf("read %s: %v", relativePath, err)
		}
		if strings.Contains(string(source), "setsid") {
			t.Fatalf("%s must not invoke or depend on external setsid", relativePath)
		}
	}

	refreshSource, err := os.ReadFile(filepath.Join(root, "cmd", "super-dolphin-gate", "remote_refresh.go"))
	if err != nil {
		t.Fatalf("read refresh source: %v", err)
	}
	if !strings.Contains(string(refreshSource), "command.Process.Release()") {
		t.Fatal("detached refresh worker must release the started process handle")
	}
}
