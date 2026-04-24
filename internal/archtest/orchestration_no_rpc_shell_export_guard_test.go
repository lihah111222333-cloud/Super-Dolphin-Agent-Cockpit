package archtest

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestOrchestrationNoRPCShellExport enforces P22 P4 §117 / §277: the
// cmd/mcp-orch/orchestration subpackage must not export the previous
// `NewOrchestrationHandlers` constructor as a reusable RPC protocol
// shell. The root assembly (cmd/mcp-orch) consumes the RPC handler
// bundle exclusively through the explicitly-named `ProvideRPCFacade`
// facade, and no other subpackage is permitted to type on the bundle.
//
// If any non-test .go in the subpackage reintroduces a `NewOrchestrationHandlers`
// declaration, this test fails so the regression lands in the same PR
// rather than drifting back into a protocol shell.
func TestOrchestrationNoRPCShellExport(t *testing.T) {
	const dir = "../../cmd/mcp-orch/orchestration"

	forbidden := []string{
		"\nfunc NewOrchestrationHandlers(",
		"\ntype NewOrchestrationHandlers ",
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
				t.Errorf("%s: forbidden export token %q present (P22 P4 §117/§277: handler.Map protocol shell must not return under the old `NewOrchestrationHandlers` name; use ProvideRPCFacade instead)", path, strings.TrimSpace(tok))
			}
		}
	}
}
