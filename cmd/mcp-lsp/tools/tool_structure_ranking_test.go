package tools

import (
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/protocol"
)

func TestStructureWorkspaceSymbolSmartRankingWeights(t *testing.T) {
	root := t.TempDir()
	target := writeStructureTestFile(t, root, "app.py", "def target(): pass\n")
	manager := &structureTestManager{
		workspaceSymbols: []protocol.WorkspaceSymbolResult{
			{WorkspaceSymbol: &protocol.WorkspaceSymbol{Name: "nontargetable", Location: protocol.WorkspaceSymbolLocation{URI: fileURI(target)}}},
			{WorkspaceSymbol: &protocol.WorkspaceSymbol{Name: "sub_target", Location: protocol.WorkspaceSymbolLocation{URI: fileURI(target)}}},
			{WorkspaceSymbol: &protocol.WorkspaceSymbol{Name: "target_handler", Location: protocol.WorkspaceSymbolLocation{URI: fileURI(target)}}},
			{WorkspaceSymbol: &protocol.WorkspaceSymbol{Name: "target", Location: protocol.WorkspaceSymbolLocation{URI: fileURI(target)}}},
		},
	}
	registry := &structureTestRegistry{fileManager: manager}
	handler := NewStructureHandler(registry)

	got, err := handler(testToolContext(root), marshalStructureParams(t, structureParams{
		Action:   "workspace_symbol",
		FilePath: target,
		Query:    "target",
	}))
	if err != nil {
		t.Fatalf("workspace_symbol returned error: %v", err)
	}
	resp, ok := got.(workspaceSymbolListResponse)
	if !ok {
		t.Fatalf("result type = %T, want workspaceSymbolListResponse", got)
	}
	if len(resp.Data) != 4 {
		t.Fatalf("got %d symbols, want 4", len(resp.Data))
	}
	assertWorkspaceSymbolOrder(t, resp, []string{"target", "target_handler", "sub_target", "nontargetable"})
	if resp.Hint != "" {
		t.Fatalf("resp.Hint = %q, want empty for non-truncated matching results", resp.Hint)
	}
}

func assertWorkspaceSymbolOrder(t *testing.T, resp workspaceSymbolListResponse, want []string) {
	t.Helper()
	for index, name := range want {
		if resp.Data[index].Name != name {
			t.Errorf("symbol[%d] = %q, want %q (smart ranking order)", index, resp.Data[index].Name, name)
		}
	}
}

func TestStructureWorkspaceSymbolPyrightPrefixedLeafSmartRanking(t *testing.T) {
	root := t.TempDir()
	target := writeStructureTestFile(t, root, "service.py", "class Service:\n  def calculate_total(self): pass\n")
	manager := &structureTestManager{
		workspaceSymbols: []protocol.WorkspaceSymbolResult{
			{WorkspaceSymbol: &protocol.WorkspaceSymbol{Name: "do_calculate_total", Location: protocol.WorkspaceSymbolLocation{URI: fileURI(target)}}},
			{WorkspaceSymbol: &protocol.WorkspaceSymbol{Name: "Service.calculate_total", Location: protocol.WorkspaceSymbolLocation{URI: fileURI(target)}}},
			{WorkspaceSymbol: &protocol.WorkspaceSymbol{Name: "calculate_total_helper", Location: protocol.WorkspaceSymbolLocation{URI: fileURI(target)}}},
		},
	}
	registry := &structureTestRegistry{fileManager: manager}
	handler := NewStructureHandler(registry)

	got, err := handler(testToolContext(root), marshalStructureParams(t, structureParams{
		Action:   "workspace_symbol",
		FilePath: target,
		Query:    "calculate_total",
	}))
	if err != nil {
		t.Fatalf("workspace_symbol returned error: %v", err)
	}
	resp := got.(workspaceSymbolListResponse)
	if len(resp.Data) != 3 {
		t.Fatalf("got %d symbols, want 3", len(resp.Data))
	}
	assertWorkspaceSymbolOrder(t, resp, []string{"Service.calculate_total", "calculate_total_helper", "do_calculate_total"})
}

func TestStructureWorkspaceSymbolExactModeFiltersNonExact(t *testing.T) {
	root := t.TempDir()
	target := writeStructureTestFile(t, root, "app.go", "package app\nfunc Target() {}\n")
	manager := &structureTestManager{
		workspaceSymbols: []protocol.WorkspaceSymbolResult{
			{WorkspaceSymbol: &protocol.WorkspaceSymbol{Name: "Target", Location: protocol.WorkspaceSymbolLocation{URI: fileURI(target)}}},
			{WorkspaceSymbol: &protocol.WorkspaceSymbol{Name: "TargetHelper", Location: protocol.WorkspaceSymbolLocation{URI: fileURI(target)}}},
		},
	}
	registry := &structureTestRegistry{fileManager: manager}
	handler := NewStructureHandler(registry)

	got, err := handler(testToolContext(root), marshalStructureParams(t, structureParams{
		Action:    "workspace_symbol",
		FilePath:  target,
		Query:     "Target",
		MatchMode: "exact",
	}))
	if err != nil {
		t.Fatalf("workspace_symbol exact returned error: %v", err)
	}
	resp := got.(workspaceSymbolListResponse)
	if len(resp.Data) != 1 || resp.Data[0].Name != "Target" {
		t.Fatalf("exact mode data = %#v, want only Target", resp.Data)
	}
	if resp.Hint != "" {
		t.Fatalf("exact mode non-truncated hint = %q, want empty", resp.Hint)
	}
}
