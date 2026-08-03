package main

import (
	"strings"
	"testing"
)

const (
	mcpLSPResourceCohortE2ETarget = "test-e2e-mcp-lsp-resource-cohort"
	mcpLSPResourceCohortE2ERun    = "^TestMcpLSPBinary(LinkedWorktreesResourceCohortRecycleAndRecover|ResourceCohortMalformedReportQuarantine)_E2E$"
)

func TestAIMaintenanceGateSelectsMcpLSPResourceCohortE2E(t *testing.T) {
	makefile := readRepoFile(t, "../Makefile")
	guardScript := readRepoFile(t, "test_with_guard.sh")

	assertScriptContains(t, makefile, "test-e2e: test-e2e-rpc-runtime "+mcpLSPResourceCohortE2ETarget)
	assertScriptContains(t, makefile, mcpLSPResourceCohortE2ETarget+":")
	assertScriptContains(t, makefile, "--quick-guard -tags=e2e ./cmd/mcp-lsp")
	assertScriptContains(t, makefile, "-run '"+mcpLSPResourceCohortE2ERun+"$'")

	runner := requireMcpLSPResourceCohortGateSection(
		t, guardScript, "run_mcp_lsp_resource_cohort_e2e() {", "\nresolve_canonical_backend_packages() {",
	)
	assertScriptContains(t, runner, "-tags=e2e ./cmd/mcp-lsp")
	assertScriptContains(t, runner, `-run "$MCP_LSP_RESOURCE_COHORT_E2E_RUN"`)
	assertScriptContains(t, guardScript, "MCP_LSP_RESOURCE_COHORT_E2E_RUN='"+mcpLSPResourceCohortE2ERun+"'")

	canonical := requireMcpLSPResourceCohortGateSection(
		t, guardScript, "run_canonical_backend() {", "\nrun_race_only() {",
	)
	assertScriptContains(t, canonical, `run_mcp_lsp_resource_cohort_e2e "$real_go"`)
	if strings.Count(guardScript, `run_mcp_lsp_resource_cohort_e2e "$real_go"`) != 1 {
		t.Fatal("resource cohort E2E must be selected only by canonical backend mode")
	}
}

func requireMcpLSPResourceCohortGateSection(
	t *testing.T,
	content, start, end string,
) string {
	t.Helper()
	startIndex := strings.Index(content, start)
	if startIndex < 0 {
		t.Fatalf("gate script missing section start %q", start)
	}
	endIndex := strings.Index(content[startIndex:], end)
	if endIndex < 0 {
		t.Fatalf("gate script missing section end %q after %q", end, start)
	}
	return content[startIndex : startIndex+endIndex]
}
