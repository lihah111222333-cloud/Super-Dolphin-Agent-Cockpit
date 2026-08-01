package multilsp

import (
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/protocol"
)

func TestLSPCompatEmptyStructMethodAllowlist(t *testing.T) {
	allowed := []string{
		LSPCompatMethodClientRegisterCapability,
		LSPCompatMethodClientUnregisterCapability,
		LSPCompatMethodWindowWorkDoneProgressCreate,
		LSPCompatMethodWorkspaceSemanticTokensRefresh,
		LSPCompatMethodWorkspaceCodeLensRefresh,
		LSPCompatMethodWorkspaceInlayHintRefresh,
		LSPCompatMethodWorkspaceDiagnosticRefresh,
	}
	for _, method := range allowed {
		if !isLSPCompatEmptyStructMethod(method) {
			t.Errorf("isLSPCompatEmptyStructMethod(%q) = false, want true", method)
		}
	}
	for _, method := range []string{"", LSPCompatMethodWorkspaceConfiguration, "unknown/method"} {
		if isLSPCompatEmptyStructMethod(method) {
			t.Errorf("isLSPCompatEmptyStructMethod(%q) = true, want false", method)
		}
	}
}

func TestTypeScriptNavigationSymbolKindMapping(t *testing.T) {
	tests := map[string]protocol.SymbolKind{
		" CLASS ":        protocol.SymbolKindClass,
		"const":          protocol.SymbolKindConstant,
		"type":           protocol.SymbolKindStruct,
		"type parameter": protocol.SymbolKindTypeParameter,
		"directory":      protocol.SymbolKindPackage,
		"string":         protocol.SymbolKindString,
		"unrecognized":   protocol.SymbolKindVariable,
	}
	for kind, want := range tests {
		if got := typeScriptNavigationSymbolKind(kind); got != want {
			t.Errorf("typeScriptNavigationSymbolKind(%q) = %d, want %d", kind, got, want)
		}
	}
}
