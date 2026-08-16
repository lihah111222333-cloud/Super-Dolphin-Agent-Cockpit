package multilsp

import (
	"os"
	"path/filepath"
	"testing"
)

func TestGoToolchainSelectionCacheKeyTracksBinaryWorkspaceAndEnvironment(t *testing.T) {
	root := t.TempDir()
	binary := filepath.Join(root, "go")
	if err := os.WriteFile(binary, []byte("first"), 0o755); err != nil {
		t.Fatalf("write fake Go binary: %v", err)
	}
	base := goToolchainSelectionKey("1.24", root, []string{"PATH=" + root, "GOTOOLCHAIN=auto"})
	same := goToolchainSelectionKey("1.24", root, []string{"PATH=" + root, "GOTOOLCHAIN=auto"})
	if same != base {
		t.Fatal("unchanged toolchain selection did not produce a stable cache key")
	}
	changedEnv := goToolchainSelectionKey("1.24", root, []string{"PATH=" + root, "GOTOOLCHAIN=local"})
	if changedEnv == base {
		t.Fatal("GOTOOLCHAIN change did not invalidate toolchain selection cache")
	}
	if err := os.WriteFile(binary, []byte("second-version"), 0o755); err != nil {
		t.Fatalf("replace fake Go binary: %v", err)
	}
	selected := GoToolchainInfo{RequiredVersion: "1.24", SelectedVersion: "1.24.1", BinDir: root}
	caches := &goResolverCaches{}
	storeGoToolchainSelection(caches, base, selected, binary)
	got, found := loadGoToolchainSelection(caches, base)
	if !found || got != selected {
		t.Fatalf("cached toolchain = %#v, %v; want %#v, true", got, found, selected)
	}
	if err := os.WriteFile(binary, []byte("third-version-with-new-size"), 0o755); err != nil {
		t.Fatalf("replace cached Go binary: %v", err)
	}
	if _, found := loadGoToolchainSelection(caches, base); found {
		t.Fatal("Go binary identity change did not invalidate toolchain selection cache")
	}
}
