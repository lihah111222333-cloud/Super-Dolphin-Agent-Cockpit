package toolbridge

import (
	"testing"

	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/mcp"
)

func TestClassifyToolCanonicalAndLegacyLSPNames(t *testing.T) {
	for _, name := range []string{"file", "grep", "inspect", "xref", "structure", "edit", "completion", "lsp_file", "lsp_grep", "lsp_edit", "lsp_hover", "code_run", "code_run_test"} {
		if got := classifyTool(name); got != dto.ClientKindLSP {
			t.Fatalf("classifyTool(%q) = %q, want %q", name, got, dto.ClientKindLSP)
		}
	}
}

func TestResolveToolClientKindAcceptsCanonicalAndLegacyLSPNames(t *testing.T) {
	for _, name := range []string{"grep", "lsp_grep"} {
		got, err := resolveToolClientKind(ToolCallRequest{Name: name, ClientKind: dto.ClientKindLSP})
		if err != nil {
			t.Fatalf("resolveToolClientKind(%q) error = %v", name, err)
		}
		if got != dto.ClientKindLSP {
			t.Fatalf("resolveToolClientKind(%q) = %q, want %q", name, got, dto.ClientKindLSP)
		}
	}
}
