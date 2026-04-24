package archtest

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestGoplsCacheStoreNoBackgroundLoop enforces P22 P2 gopls-S2: the
// lspCacheStore must not reintroduce a constructor-launched cleanup
// goroutine or its stopCh/cleanupLoop/cleanupInterval scaffolding.
// Cleanup is amortised on access via maybeCleanup, keeping shutdown
// ctx-driven and the constructor pure.
//
// The guard scans non-test .go under cmd/mcp-lsp/gopls and fails if
// any of the forbidden shapes reappears.
func TestGoplsCacheStoreNoBackgroundLoop(t *testing.T) {
	const dir = "../../cmd/mcp-lsp/gopls"

	forbidden := []string{
		"go store.cleanupLoop()",
		"go s.cleanupLoop()",
		"func (s *lspCacheStore) cleanupLoop(",
		"lspCacheCleanupInterval",
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read dir %s: %v", dir, err)
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		path := filepath.Join(dir, name)
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		text := string(data)
		for _, tok := range forbidden {
			if strings.Contains(text, tok) {
				t.Errorf("%s: forbidden cache-loop literal %q present (P22 P2 gopls-S2: rely on amortised maybeCleanup on access)", path, tok)
			}
		}
	}
}
