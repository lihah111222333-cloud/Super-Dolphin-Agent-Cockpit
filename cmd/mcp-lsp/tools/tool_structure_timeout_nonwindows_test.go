//go:build !windows

package tools

import (
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/middleware"
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/protocol"
)

// TestStructureDocumentSymbolKeepsNonWindowsSlowDeadline 证明非 Windows 平台继续沿用远程既有的
// slow-tier 工具 deadline，Windows 冷安装策略不会改变其他平台行为。
func TestStructureDocumentSymbolUsesSlowDeadlineBudget(t *testing.T) {
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
	deadline, ok := manager.documentContext.Deadline()
	if !ok {
		t.Fatal("DocumentSymbol context has no deadline")
	}
	if remaining := time.Until(deadline); remaining < middleware.TierSlow-5*time.Second {
		t.Fatalf("DocumentSymbol deadline budget = %s, want non-Windows slow tier near %s", remaining.Round(time.Second), middleware.TierSlow)
	}
}
