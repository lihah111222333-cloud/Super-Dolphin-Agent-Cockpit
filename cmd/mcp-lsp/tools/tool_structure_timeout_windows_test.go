//go:build windows

package tools

import (
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/protocol"
)

// TestStructureDocumentSymbolDefersDeadlineToWindowsColdInstallAndLSPTransport 证明 Windows
// 自动安装不会被公共工具层 deadline 提前截断，具体安装与传输仍各自负责超时。
func TestStructureDocumentSymbolDefersDeadlineToWindowsColdInstallAndLSPTransport(t *testing.T) {
	root := t.TempDir()
	target := writeStructureTestFile(t, root, "frontend/big-store.js", "export const clientStore = {};\n")
	manager := &structureTestManager{documentSymbols: []protocol.DocumentSymbol{reproDocumentSymbol("clientStore")}}
	handler := NewStructureHandler(&structureTestRegistry{fileManager: manager})

	if _, err := handler(testToolContext(root), marshalStructureParams(t, structureParams{
		Action: "document_symbol", FilePath: target, MaxResults: 50,
	})); err != nil {
		t.Fatalf("document_symbol returned error: %v", err)
	}
	if manager.documentContext == nil {
		t.Fatal("DocumentSymbol was not called")
	}
	if deadline, ok := manager.documentContext.Deadline(); ok {
		t.Fatalf("DocumentSymbol context has shared outer deadline %s, want Windows cold install and LSP transport to own their timeouts", deadline)
	}
}
