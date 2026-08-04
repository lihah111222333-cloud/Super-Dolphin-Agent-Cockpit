package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/scripts/mcp_lsp_workload_catalog"
)

func TestValidateCatalogTestSelectorsRejectsRenamedTest(t *testing.T) {
	repoRoot := t.TempDir()
	packageDirectory := filepath.Join(repoRoot, "cmd", "mcp-lsp", "fake")
	if err := os.MkdirAll(packageDirectory, 0o755); err != nil {
		t.Fatal(err)
	}
	testSource := "package fake\n\nfunc TestExisting() {}\n"
	if err := os.WriteFile(filepath.Join(packageDirectory, "fake_test.go"), []byte(testSource), 0o644); err != nil {
		t.Fatal(err)
	}
	document := catalog.Catalog{Workloads: []catalog.Workload{{
		ID: "mcp-lsp-mutated", ImplementationStatus: "implemented",
		Command: []string{"go", "test", "./cmd/mcp-lsp/fake", "-run", "^Test(Renamed)$"},
	}}}
	err := validateCatalogTestSelectors(repoRoot, document)
	if err == nil || !strings.Contains(err.Error(), "missing test \"TestRenamed\"") {
		t.Fatalf("validateCatalogTestSelectors() error = %v, want missing renamed test", err)
	}
}
