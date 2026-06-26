package archtest

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestMultiLSPCacheStoreNoBackgroundLoop 防止 lspCacheStore 重新引入构造期启动的清理 goroutine。
// 清理应通过 maybeCleanup 在访问时摊销执行，避免 stopCh/cleanupLoop/cleanupInterval 再次进入生命周期。
//
// The guard scans non-test .go under cmd/mcp-lsp/multilsp and fails if
// any of the forbidden shapes reappears.
func TestMultiLSPCacheStoreNoBackgroundLoop(t *testing.T) {
	const dir = "../../cmd/mcp-lsp/multilsp"

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
				t.Errorf("%s: forbidden cache-loop literal %q present (cache cleanup must stay amortised on access)", path, tok)
			}
		}
	}
}
