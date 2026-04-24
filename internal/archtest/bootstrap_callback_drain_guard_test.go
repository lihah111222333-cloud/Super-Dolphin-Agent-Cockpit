package archtest

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestBootstrapCallbackOwnedByWaitGroup enforces P22 P2 bootstrap-S1:
// the bootstrap client must not fire application callbacks
// (OnShutdown / OnConfigChanged) fire-and-forget. Every spawn has to
// go through spawnCallback, which registers with callbackWG so
// Close() can drain them via drainCallbacks.
//
// Forbidden shapes in non-test .go under
// internal/mcpserver/common/bootstrap:
//   - `go c.cfg.OnShutdown(`
//   - `go c.cfg.OnConfigChanged(`
//
// Also asserts the drainCallbacks( call stays wired so future
// refactors can't silently drop the drain.
func TestBootstrapCallbackOwnedByWaitGroup(t *testing.T) {
	const dir = "../../internal/mcpserver/common/bootstrap"

	forbidden := []string{
		"go c.cfg.OnShutdown(",
		"go c.cfg.OnConfigChanged(",
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read dir %s: %v", dir, err)
	}
	drainFound := false
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
				t.Errorf("%s: forbidden fire-and-forget callback literal %q present (P22 P2 bootstrap-S1: use spawnCallback so callbackWG can drain)", path, tok)
			}
		}
		if strings.Contains(text, "drainCallbacks(") {
			drainFound = true
		}
	}
	if !drainFound {
		t.Errorf("internal/mcpserver/common/bootstrap: expected drainCallbacks( to remain wired into Close()")
	}
}
