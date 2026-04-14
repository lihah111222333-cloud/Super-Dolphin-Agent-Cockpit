package prompt

import (
	"context"
	"strings"
	"testing"
)

func TestMCPInstructionsProviderResolveBuildsServerBlocks(t *testing.T) {
	provider := MCPInstructionsProvider{}
	text, err := provider.Resolve(context.Background(), SectionContext{BuildCtx: BuildCtx{MCPSnapshot: MCPSnapshot{
		Servers: []string{"orch", "lsp"},
		Tools: []string{
			"mcp__lsp__lsp_grep",
			"mcp__orch__task_get_dag",
			"shared_tool",
			"mcp__lsp__lsp_file",
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
		"  - mcp__lsp__lsp_file",
		"  - mcp__lsp__lsp_grep",
		"## orch",
		"  - mcp__orch__task_get_dag",
		"## additional_tools",
		"  - shared_tool",
	}
	for _, check := range checks {
		if !strings.Contains(*text, check) {
			t.Fatalf("Resolve() = %q, want substring %q", *text, check)
		}
	}
	if strings.Index(*text, "## lsp") > strings.Index(*text, "## orch") {
		t.Fatalf("Resolve() = %q, want servers sorted", *text)
	}
}

func TestMCPInstructionsProviderResolveSkipsEmptySnapshot(t *testing.T) {
	provider := MCPInstructionsProvider{}
	text, err := provider.Resolve(context.Background(), SectionContext{})
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if text != nil {
		t.Fatalf("Resolve() = %q, want nil", *text)
	}
}
