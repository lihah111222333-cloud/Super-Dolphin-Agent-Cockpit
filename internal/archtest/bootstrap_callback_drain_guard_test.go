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
		if scanBootstrapCallbackFile(t, dir, e, forbidden) {
			drainFound = true
		}
	}
	if !drainFound {
		t.Errorf("internal/mcpserver/common/bootstrap: expected drainCallbacks( to remain wired into Close()")
	}
}

func scanBootstrapCallbackFile(t *testing.T, dir string, e os.DirEntry, forbidden []string) bool {
	t.Helper()

	if e.IsDir() {
		return false
	}
	name := e.Name()
	if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
		return false
	}
	path := filepath.Join(dir, name)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	text := string(data)
	assertBootstrapCallbackForbiddenTokens(t, path, text, forbidden)
	return strings.Contains(text, "drainCallbacks(")
}

func assertBootstrapCallbackForbiddenTokens(t *testing.T, path, text string, forbidden []string) {
	t.Helper()

	for _, tok := range forbidden {
		if strings.Contains(text, tok) {
			t.Errorf("%s: forbidden fire-and-forget callback literal %q present (P22 P2 bootstrap-S1: use spawnCallback so callbackWG can drain)", path, tok)
		}
	}
}
