//go:build !windows

package processobserve_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/internal/processobserve"
)

func assertUnsafeRootRejected(t *testing.T, root string) {
	t.Helper()
	if err := os.Chmod(root, 0o755); err != nil {
		t.Fatalf("chmod root: %v", err)
	}
	if _, err := processobserve.OpenDurableStore(root, processobserve.DurableOptions{TestOnly: true}); err == nil {
		t.Fatal("OpenDurableStore() accepted non-private root")
	}
	if err := os.Chmod(root, 0o700); err != nil {
		t.Fatalf("restore root mode: %v", err)
	}
}

func canonicalTempRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatalf("EvalSymlinks(temp root): %v", err)
	}
	if err := os.Chmod(root, 0o700); err != nil {
		t.Fatalf("chmod temp root: %v", err)
	}
	return root
}
