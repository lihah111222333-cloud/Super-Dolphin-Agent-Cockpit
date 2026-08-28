package main

import (
	"path/filepath"
	"strings"
	"testing"

	catalog "github.com/lihah111222333-cloud/super-dolphin-agent/scripts/mcp_lsp_workload_catalog"
)

func TestCatalogGuardHostSupportRejectsWindows(t *testing.T) {
	if catalogGuardHostSupported("windows") || !catalogGuardHostSupported("linux") {
		t.Fatal("catalog guard host support must fail closed on Windows and allow the trusted POSIX host")
	}
}

func TestMcpLSPWorkloadFilesRequireCatalogAndQuickRoundTrip(t *testing.T) {
	document, err := catalog.Load(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	policy := newGatePlanPolicy()
	policy.mcpLSPWorkloadExactFiles = workloadGateInputFileSet(document)
	for _, file := range []string{
		"scripts/mcp_lsp_workload_catalog/catalog.go",
		"scripts/mcp_lsp_workload_catalog.json",
		"scripts/mcp_lsp_workload_runner/main.go",
		"internal/devtools/mcplspguardcore/guard.go",
		"scripts/mcp_lsp_workload_catalog_guard_test.go",
		"scripts/ai_maintenance/main.go",
		"scripts/ai_maintenance/owned_gate_execution.go",
		".githooks/README.md",
		"Makefile",
	} {
		gates := map[string]bool{}
		policy.applyOwnedGateRules(file, gates)
		if !gates["mcp-lsp:catalog"] || !gates["mcp-lsp:idle-quick"] {
			t.Fatalf("%q gates = %#v, want catalog and idle-quick roundtrip", file, gates)
		}
	}
}

func TestMcpLSPWorkloadRoutingCoversCanonicalConsumerRegistry(t *testing.T) {
	policy := newGatePlanPolicy()
	for _, file := range catalog.WorkloadConsumerPaths() {
		gates := map[string]bool{}
		policy.applyOwnedGateRules(file, gates)
		if !gates["mcp-lsp:catalog"] || !gates["mcp-lsp:idle-quick"] {
			t.Fatalf("canonical consumer %q gates = %#v, want both MCP-LSP roles", file, gates)
		}
	}
}

func TestCandidateProducerWorkflowChangeRoutesBothMcpLSPGates(t *testing.T) {
	const producer = ".github/workflows/new-mcp-lsp.yml"
	current, err := catalog.Load(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	if len(current.Workloads) == 0 {
		t.Fatal("candidate catalog has no workload fixture")
	}
	current.Workloads[0].ProducerImplementationStatus = "implemented"
	current.Workloads[0].ProducerWorkflowPath = producer
	policy := newGatePlanPolicy()
	policy.mcpLSPWorkloadExactFiles = workloadGateInputFileSet(current)
	gates := map[string]bool{}
	policy.applyOwnedGateRules(producer, gates)
	if !gates["mcp-lsp:catalog"] || !gates["mcp-lsp:idle-quick"] {
		t.Fatalf("candidate producer workflow gates = %#v, want both MCP-LSP roles", gates)
	}
}

func TestMcpLSPQuickReleaseAuthorityCannotUseLocalReceipt(t *testing.T) {
	workload := canonicalIdleQuickWorkload(t)
	err := requireMcpLSPQuickReleaseAuthority(workload)
	if err == nil || !strings.Contains(err.Error(), "N/V") {
		t.Fatalf("quick release authority error = %v, want remote authority N/V", err)
	}
}

func TestMcpLSPQuickReleaseAuthorityRejectsLocalRoleMutation(t *testing.T) {
	workload := canonicalIdleQuickWorkload(t)
	workload.RunnerTarget = "local-go-test"
	err := requireMcpLSPQuickReleaseAuthority(workload)
	if err == nil || !strings.Contains(err.Error(), "release role") {
		t.Fatalf("quick local role error = %v, want release role rejection", err)
	}
}

func canonicalIdleQuickWorkload(t *testing.T) catalog.Workload {
	t.Helper()
	document, err := catalog.Load(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	workload, err := document.Find("mcp-lsp-idle-quick")
	if err != nil {
		t.Fatal(err)
	}
	return workload
}
