package toolbridge

import (
	"testing"

	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/mcp"
)

func TestClassifyToolCanonicalLSPNames(t *testing.T) {
	for _, name := range []string{"file", "grep", "inspect", "xref", "structure", "patch_edit", "completion"} {
		if got := classifyTool(name); got != dto.ClientKindLSP {
			t.Fatalf("classifyTool(%q) = %q, want %q", name, got, dto.ClientKindLSP)
		}
	}
}

func TestClassifyToolRejectsLegacyLSPNames(t *testing.T) {
	for _, name := range []string{"lsp_file", "lsp_grep", "lsp_edit", "lsp_completion", "lsp_format_preview", "lsp_hover", "format_preview"} {
		if got := classifyTool(name); got == dto.ClientKindLSP {
			t.Fatalf("classifyTool(%q) = %q, want non-LSP after legacy alias removal", name, got)
		}
	}
}

func TestClassifyToolIDANames(t *testing.T) {
	for _, name := range []string{"ida_ping", "ida_decompile", "ida_frida_attach"} {
		if got := classifyTool(name); got != dto.ClientKindIDA {
			t.Fatalf("classifyTool(%q) = %q, want %q", name, got, dto.ClientKindIDA)
		}
	}
}

func TestValidateProxyToolFamilyIDA(t *testing.T) {
	if err := validateProxyToolFamily("ida", "ida_decompile"); err != nil {
		t.Fatalf("validateProxyToolFamily() error = %v", err)
	}
	if err := validateProxyToolFamily("ida", "grep"); err == nil {
		t.Fatalf("validateProxyToolFamily() error = nil, want family mismatch")
	}
}

func TestResolveToolClientKindAcceptsCanonicalLSPNames(t *testing.T) {
	for _, name := range []string{"grep", "patch_edit", "completion"} {
		got, err := resolveToolClientKind(ToolCallRequest{Name: name, ClientKind: dto.ClientKindLSP})
		if err != nil {
			t.Fatalf("resolveToolClientKind(%q) error = %v", name, err)
		}
		if got != dto.ClientKindLSP {
			t.Fatalf("resolveToolClientKind(%q) = %q, want %q", name, got, dto.ClientKindLSP)
		}
	}
}

func TestResolveToolClientKindRejectsLegacyLSPNames(t *testing.T) {
	for _, name := range []string{"lsp_grep", "lsp_edit", "lsp_completion", "lsp_format_preview", "format_preview"} {
		if _, err := resolveToolClientKind(ToolCallRequest{Name: name, ClientKind: dto.ClientKindLSP}); err == nil {
			t.Fatalf("resolveToolClientKind(%q) error = nil, want family mismatch", name)
		}
	}
}
