package toolbridge

import (
	"testing"

	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/mcp"
)

func TestClassifyToolCanonicalAndLegacyLSPNames(t *testing.T) {
	for _, name := range []string{"file", "grep", "inspect", "xref", "structure", "edit", "format_preview", "completion", "lsp_file", "lsp_grep", "lsp_edit", "lsp_format_preview", "lsp_hover"} {
		if got := classifyTool(name); got != dto.ClientKindLSP {
			t.Fatalf("classifyTool(%q) = %q, want %q", name, got, dto.ClientKindLSP)
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

func TestResolveToolClientKindAcceptsCanonicalAndLegacyLSPNames(t *testing.T) {
	for _, name := range []string{"grep", "lsp_grep", "format_preview", "lsp_format_preview"} {
		got, err := resolveToolClientKind(ToolCallRequest{Name: name, ClientKind: dto.ClientKindLSP})
		if err != nil {
			t.Fatalf("resolveToolClientKind(%q) error = %v", name, err)
		}
		if got != dto.ClientKindLSP {
			t.Fatalf("resolveToolClientKind(%q) = %q, want %q", name, got, dto.ClientKindLSP)
		}
	}
}
