package main

import "testing"

func TestMcpLSPWorkloadFilesRequireCatalogAndQuickRoundTrip(t *testing.T) {
	for _, file := range []string{
		"scripts/mcp_lsp_workload_catalog/catalog.go",
		"scripts/mcp_lsp_workload_catalog.json",
		"scripts/mcp_lsp_workload_runner/main.go",
		"scripts/mcp_lsp_workload_guard/main.go",
		"scripts/mcp_lsp_workload_catalog_guard_test.go",
		".github/workflows/ci.yml",
		".github/workflows/release.yml",
		"scripts/ai_maintenance/main.go",
		"scripts/ai_maintenance/owned_gate_execution.go",
		"scripts/ai_maintenance/evidence.go",
		".githooks/README.md",
		"Makefile",
	} {
		gates := map[string]bool{}
		newGatePlanPolicy().applyOwnedGateRules(file, gates)
		if !gates["mcp-lsp:catalog"] || !gates["mcp-lsp:idle-quick"] {
			t.Fatalf("%q gates = %#v, want catalog and idle-quick roundtrip", file, gates)
		}
	}
}
