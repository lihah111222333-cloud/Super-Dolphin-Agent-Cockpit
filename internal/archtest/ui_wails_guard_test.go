package archtest

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestUIWailsNoDirectUIStateImport is the P22 P4 S1b import-direction
// guard: internal/ui/wails MUST NOT directly import
// internal/module/uistate. UI frontends depend on the narrow contract
// facade (contract.UIProjectStateFacade) and the shared contract DTOs,
// not the module's private Service.
//
// P4 plan §1 target architecture: "只依赖 rpc.Server.Dispatch、公共
// contract 或专用 facade". P4 §147 lists ui/wails as a first-wave
// implementation lane; this test locks the invariant so the facade
// seam cannot silently regress.
//
// The guard scans every non-test .go file under internal/ui/wails and
// fails if any carries the forbidden import. Test files are out of
// scope (stubs sometimes need to reach into the module to construct
// realistic fixtures — see e.g. the pre-S1b `code_scope_test.go`
// snapshot). Once S1b lands, even test files are clean but we only
// enforce the production contract here.
func TestUIWailsNoDirectUIStateImport(t *testing.T) {
	t.Parallel()
	root := repoRootForGuardTests(t)
	wailsDir := filepath.Join(root, "internal", "ui", "wails")

	entries, err := os.ReadDir(wailsDir)
	if err != nil {
		t.Fatalf("read %s: %v", wailsDir, err)
	}

	const forbiddenImport = `"github.com/anthropic-ai/super-agent-v3/internal/module/uistate"`
	var hits []string
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		path := filepath.Join(wailsDir, name)
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		if strings.Contains(string(data), forbiddenImport) {
			hits = append(hits, name)
		}
	}
	if len(hits) > 0 {
		t.Fatalf("internal/ui/wails reintroduced direct uistate imports (P4 §1 dependency-direction violation); offending files: %v", hits)
	}
}

// TestUIWailsActiveAgentPredicateFromContract locks the P22 P4 §74
// hidden-contract fix: the "is agent active?" predicate now lives in
// the contract package (contract.IsActiveAgentState), not as a local
// helper in ui/wails. Keeping the check here prevents future drift where
// someone re-introduces `isActiveAgentState` privately and lets
// ui/wails and orchestration diverge on what "active" means.
func TestUIWailsActiveAgentPredicateFromContract(t *testing.T) {
	t.Parallel()
	root := repoRootForGuardTests(t)
	modulePath := filepath.Join(root, "internal", "ui", "wails", "module.go")
	data, err := os.ReadFile(modulePath)
	if err != nil {
		t.Fatalf("read %s: %v", modulePath, err)
	}
	src := string(data)
	forbidden := []string{
		"func isActiveAgentState(",
	}
	required := []string{
		"contract.IsActiveAgentState(",
	}
	for _, token := range forbidden {
		if strings.Contains(src, token) {
			t.Errorf("ui/wails/module.go reintroduced private active-agent predicate; forbidden token: %q", token)
		}
	}
	for _, token := range required {
		if !strings.Contains(src, token) {
			t.Errorf("ui/wails/module.go must call contract.IsActiveAgentState; missing token: %q", token)
		}
	}
}
