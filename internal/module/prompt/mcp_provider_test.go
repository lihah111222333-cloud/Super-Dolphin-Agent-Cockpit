package prompt

import (
	"context"
	"strings"
	"testing"
)

func TestMCPInstructionsProviderResolveBuildsServerBlocks(t *testing.T) {
	provider := newMCPInstructionsProvider()
	text, err := provider.Resolve(context.Background(), SectionContext{BuildCtx: BuildCtx{MCPSnapshot: MCPSnapshot{
		Servers: []string{"orch", "lsp"},
		Instructions: map[string]string{
			"orch": "Use DAG tools for orchestration state.",
			"lsp":  "Use the LSP MCP first.",
		},
	}}})
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if text == nil {
		t.Fatal("Resolve() = nil, want content")
	}
	checks := []string{
		"# MCP Server Instructions",
		"## lsp",
		"Use the LSP MCP first.",
		"## orch",
		"Use DAG tools for orchestration state.",
	}
	for _, check := range checks {
		if !strings.Contains(*text, check) {
			t.Fatalf("Resolve() = %q, want substring %q", *text, check)
		}
	}
	if strings.Contains(*text, "mcp__") {
		t.Fatalf("Resolve() = %q, want instructions-only output", *text)
	}
	if strings.Index(*text, "## lsp") > strings.Index(*text, "## orch") {
		t.Fatalf("Resolve() = %q, want servers sorted", *text)
	}
}

func TestMCPInstructionsProviderResolveSkipsServersWithoutInstructions(t *testing.T) {
	provider := newMCPInstructionsProvider()
	text, err := provider.Resolve(context.Background(), SectionContext{BuildCtx: BuildCtx{MCPSnapshot: MCPSnapshot{
		Servers: []string{"orch"},
		Instructions: map[string]string{
			"lsp": "Use the LSP MCP first.",
		},
	}}})
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if text != nil {
		t.Fatalf("Resolve() = %q, want nil when no connected server has instructions", *text)
	}
}
