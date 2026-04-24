package archtest

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestGoplsPoolRecyclerOwnedByRunner enforces P22 P2 gopls-S1:
// NewManagerPool must not launch the recycler loop from its
// constructor, and poolRecycler must not expose an ad-hoc
// Start()/Stop() pair or a self-spawned loop goroutine. The runtime
// owner is platformrunner.Runner via `group:"runners"`, so the only
// allowed entry point is the ctx-driven Run(ctx) method.
//
// The guard scans non-test .go files under cmd/mcp-lsp/gopls for the
// forbidden shapes (`pool.recycler.Start(`, `p.recycler.Start(`,
// `r.recycler.Stop(`, `go r.loop()`, `func (r *poolRecycler) Start(`
// and `func (r *poolRecycler) Stop(`) and fails if any of them
// reappears.
func TestGoplsPoolRecyclerOwnedByRunner(t *testing.T) {
	const dir = "../../cmd/mcp-lsp/gopls"

	forbidden := []string{
		"pool.recycler.Start(",
		"p.recycler.Start(",
		"p.recycler.Stop(",
		"go r.loop()",
		"func (r *poolRecycler) Start(",
		"func (r *poolRecycler) Stop(",
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
				t.Errorf("%s: forbidden recycler lifecycle literal %q present (P22 P2 gopls-S1: use Run(ctx) as Runner owner, not self-launched goroutine / Start()/Stop() pair)", path, tok)
			}
		}
	}
}
