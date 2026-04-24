package archtest

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestOrchestrationNoHookConsumerExport enforces P22 P4 §278: the
// cmd/mcp-orch/orchestration subpackage must not export a `HookConsumer`
// interface or `NewHookConsumer` / `ProvideHookConsumer` as a bootstrap-hook
// protocol shell. Root assembly wires the handler via
// contract.BootstrapHookAfterHandler instead.
//
// Any re-introduction of the previous exports is caught here before it
// can become another hidden cross-package protocol surface. Test-file
// references are tolerated so in-package tests can still exercise the
// unexported constructor / types.
func TestOrchestrationNoHookConsumerExport(t *testing.T) {
	const dir = "../../cmd/mcp-orch/orchestration"

	forbidden := []string{
		"\ntype HookConsumer interface",
		"\ntype HookConsumer struct",
		"\nfunc NewHookConsumer(",
		"\nfunc ProvideHookConsumer(",
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
				t.Errorf("%s: forbidden export token %q present (P22 P4 §278: bootstrap-hook protocol shell must not return to cmd/mcp-orch/orchestration)", path, strings.TrimSpace(tok))
			}
		}
	}
}
