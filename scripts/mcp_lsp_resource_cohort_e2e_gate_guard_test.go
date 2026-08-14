package main

import (
	"strings"
	"testing"
)

const (
	mcpLSPResourceCohortE2ETarget = "test-e2e-mcp-lsp-resource-cohort"
	mcpLSPResourceCohortE2ERun    = "^TestMcpLSPBinary(LinkedWorktreesResourceCohortRecycleAndRecover|ResourceCohortMalformedReportQuarantine)_E2E$"
	mcpLSPGoplsDaemonE2ETarget    = "test-e2e-gopls-daemon-lifecycle"
	mcpLSPGoplsDaemonE2EScript    = "run_mcp_lsp_gopls_daemon_e2e.sh"
	mcpLSPGoplsDaemonE2ERun       = "^TestMcpLSPBinaryRealGoplsDaemonExitsAfterLastForwarder_E2E$"
)

func TestAIMaintenanceGateSelectsMcpLSPResourceCohortE2E(t *testing.T) {
	makefile := readRepoFile(t, "../Makefile")
	guardScript := readRepoFile(t, "test_with_guard.sh")

	assertScriptContains(t, makefile, "test-e2e:\n\t$(TEST_WITH_GUARD) --make-e2e-suite")
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
	if strings.Count(guardScript, `run_mcp_lsp_resource_cohort_e2e "$real_go"`) != 2 {
		t.Fatal("resource cohort E2E must be selected by exactly the make aggregate and canonical backend modes")
	}
}

func TestMcpLSPGoplsDaemonE2EEntryPinsLongGoTestTimeout(t *testing.T) {
	makefile := readRepoFile(t, "../Makefile")
	script := readRepoFile(t, mcpLSPGoplsDaemonE2EScript)

	assertScriptContains(t, makefile, mcpLSPGoplsDaemonE2ETarget+":\n\t./scripts/"+mcpLSPGoplsDaemonE2EScript)
	assertScriptContains(t, script, "./scripts/test_with_guard.sh --quick-guard -tags=e2e ./cmd/mcp-lsp")
	assertScriptContains(t, script, "-run '"+mcpLSPGoplsDaemonE2ERun+"'")
	assertScriptContains(t, script, "-timeout 20m")
	assertScriptContains(t, script, "-count=1")
}

func TestMcpLSPRealGoplsE2EsFailFastWhenBinaryMissing(t *testing.T) {
	daemon := readRepoFile(t, "../cmd/mcp-lsp/lsp_binary_gopls_daemon_e2e_test.go")
	assertScriptContains(t, daemon, `t.Skip("skipping real gopls daemon lifecycle e2e test in short mode")`)
	assertScriptContains(t, daemon, `t.Fatalf("gopls is required for real daemon lifecycle e2e: %v", err)`)
	if strings.Contains(daemon, `t.Skipf("gopls is not installed:`) {
		t.Fatal("real gopls daemon E2E must not skip when gopls is unavailable")
	}

	worktree := readRepoFile(t, "../cmd/mcp-lsp/lsp_binary_go_worktree_e2e_test.go")
	assertScriptContains(t, worktree, `t.Fatalf("gopls is required for real Go worktree diagnostics e2e: %v", err)`)
	if strings.Contains(worktree, `t.Skipf("gopls is required for real Go worktree diagnostics e2e:`) {
		t.Fatal("real Go worktree E2E must not skip when gopls is unavailable")
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
