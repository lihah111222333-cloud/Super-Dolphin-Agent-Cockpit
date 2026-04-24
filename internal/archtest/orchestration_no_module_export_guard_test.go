package archtest

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestOrchestrationNoModuleExport is the P22 P4 S4c1 guard preregistered
// at P4 §TDD line 257. The orchestration subpackage must not export a
// package-level `Module` fx.Option; root assembly (cmd/mcp-orch/fx.go
// buildOrchestrationOptions) is authoritative for which pieces get
// wired together. Subpackage must only expose the constituent building
// blocks (ProvideService / ProvideServiceInterface /
// ProvideHookAfterHandler / ProvideRPCFacade / RegisterTurnLifecycle
// / RegisterApprovalLifecycle) — not a bundled `Module`.
//
// P4 plan §278 rationale: "Module 退回 cmd/mcp-orch 根入口组装". Hiding
// the Module forces any consumer to go through the root entry for
// assembly choices, preventing `cmd/mcp-*` subpackages from growing
// opaque protocol-shell exports again.
//
// The guard scans every non-test .go file in
// cmd/mcp-orch/orchestration and fails if it re-declares `var Module =`
// or `func Module(` at top level. Comment mentions of the pre-S4c1
// shape are allowed (the migration note in service.go documents the
// removal for future readers).
func TestOrchestrationNoModuleExport(t *testing.T) {
	t.Parallel()
	root := repoRootForGuardTests(t)

	dir := filepath.Join(root, "cmd", "mcp-orch", "orchestration")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read %s: %v", dir, err)
	}

	// Two token shapes to forbid: `var Module` at statement start (the
	// pre-S4c1 shape) and `func Module(` (some authors rewrite a var
	// into an equivalent helper to dodge a var-only guard).
	forbidden := []string{
		"\nvar Module ",
		"\nvar Module=",
		"\nfunc Module(",
	}

	var hits []string
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		path := filepath.Join(dir, name)
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		// Normalize leading file: prepend a newline so the first line
		// also matches the "\nvar Module " pattern if a var declaration
		// lives at line 1.
		src := "\n" + string(data)
		for _, token := range forbidden {
			if strings.Contains(src, token) {
				hits = append(hits, name+" ["+strings.TrimSpace(token)+"]")
			}
		}
	}

	if len(hits) > 0 {
		t.Fatalf("cmd/mcp-orch/orchestration reintroduced a package-level Module export (P4 §278 violation); offending files/tokens: %v", hits)
	}
}
