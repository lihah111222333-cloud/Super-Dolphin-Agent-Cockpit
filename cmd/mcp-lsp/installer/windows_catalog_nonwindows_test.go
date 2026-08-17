//go:build !windows

package installer

import (
	"errors"
	"os"
	"testing"
)

func TestWindowsCatalogProductionBranchIsNotRegisteredOnNonWindows(t *testing.T) {
	cache, err := NewWindowsAssetCache(t.TempDir(), nil)
	if err != nil {
		t.Fatalf("NewWindowsAssetCache() error = %v", err)
	}
	err = ensureWindowsCatalogProductionBranch()
	if !errors.Is(err, ErrUnsupportedWindowsHostPlatform) {
		t.Fatalf("ensureWindowsCatalogProductionBranch() error = %v, want ErrUnsupportedWindowsHostPlatform", err)
	}
	entries, err := os.ReadDir(cache.Root())
	if err != nil {
		t.Fatalf("read non-Windows guard cache root: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("non-Windows guard created cache entries: %v", entries)
	}
}
