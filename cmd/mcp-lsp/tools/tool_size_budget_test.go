package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-lsp/format"
	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-lsp/protocol"
	"github.com/anthropic-ai/super-agent-v3/internal/mcpserver/common"
)

// TestReferencesCompactBudget verifies the default compact rendering for
// xref.references caps at 30 entries and groups them by file. The
// previous test exercised the deprecated "verbosity=full" knob; that
// path was removed because it duplicated max_results without adding
// signal.
func TestReferencesCompactBudget(t *testing.T) {
	root, filePath := writeXRefFixture(t)
	handler := NewXRefHandler(&structureTestRegistry{
		fileManager: &structureTestManager{references: makeLocationResults(60)},
	})

	compact := callXRefTool(t, handler, root, filePath, xrefParams{Action: "references"})
	grouped, ok := compact.(protocol.GroupedLocationResult)
	if !ok {
		t.Fatalf("compact references result = %T, want GroupedLocationResult", compact)
	}
	if grouped.Total != 60 || grouped.Showing != 30 {
		t.Fatalf("compact references total/showing = %d/%d, want 60/30", grouped.Total, grouped.Showing)
	}
}

// TestCompletionCompactBudget mirrors the references coverage but for
// completion results, which now always render via CompactList.
func TestCompletionCompactBudget(t *testing.T) {
	root, filePath := writeXRefFixture(t)
	handler := NewCompletionHandler(&structureTestRegistry{
		fileManager: &structureTestManager{completionItems: makeCompletionItems(60)},
	})

	compact := callCompletionTool(t, handler, root, filePath, completionParams{})
	list, ok := compact.(format.CompactList[format.CompactCompletionItem])
	if !ok {
		t.Fatalf("compact completion result = %T, want CompactList", compact)
	}
	if list.Total != 60 || list.Showing != 20 {
		t.Fatalf("compact completion total/showing = %d/%d, want 60/20", list.Total, list.Showing)
	}
}

// TestWorkspaceSymbolCompactBudget mirrors the references coverage for
// workspace_symbol queries.
func TestWorkspaceSymbolCompactBudget(t *testing.T) {
	manager := &structureTestManager{workspaceSymbols: makeWorkspaceSymbols(60)}
	handler := NewStructureHandler(&structureTestRegistry{languageManager: manager})
	root := t.TempDir()

	compact := callStructureTool(t, handler, root, structureParams{Action: "workspace_symbol", Query: "Symbol", Language: "go"})
	list, ok := compact.(format.CompactList[format.CompactWorkspaceSymbol])
	if !ok {
		t.Fatalf("compact workspace_symbol result = %T, want CompactList", compact)
	}
	if list.Total != 60 || list.Showing != 20 {
		t.Fatalf("compact workspace_symbol total/showing = %d/%d, want 60/20", list.Total, list.Showing)
	}
}

func callXRefTool(t *testing.T, handler ToolHandler, root string, filePath string, params xrefParams) any {
	t.Helper()
	params.Pos = fmt.Sprintf("%s:1:1", filePath)
	return callToolHandler(t, handler, root, params)
}

func callCompletionTool(t *testing.T, handler ToolHandler, root string, filePath string, params completionParams) any {
	t.Helper()
	params.Pos = fmt.Sprintf("%s:1:1", filePath)
	return callToolHandler(t, handler, root, params)
}

func callStructureTool(t *testing.T, handler ToolHandler, root string, params structureParams) any {
	t.Helper()
	return callToolHandler(t, handler, root, params)
}

func callToolHandler(t *testing.T, handler ToolHandler, root string, params any) any {
	t.Helper()
	payload, err := json.Marshal(params)
	if err != nil {
		t.Fatalf("marshal params: %v", err)
	}
	got, err := handler(common.WithToolScope(context.Background(), common.ToolScope{CWD: root}), payload)
	if err != nil {
		t.Fatalf("tool returned error: %v", err)
	}
	return got
}

func makeLocationResults(count int) []protocol.LocationResult {
	out := make([]protocol.LocationResult, 0, count)
	for i := range count {
		location := protocol.Location{
			URI: fileURI(filepath.Join(os.TempDir(), fmt.Sprintf("ref-%02d.go", i))),
			Range: protocol.Range{
				Start: protocol.Position{Line: i, Character: 0},
				End:   protocol.Position{Line: i, Character: 1},
			},
		}
		out = append(out, protocol.LocationResult{Location: &location})
	}
	return out
}

func makeCompletionItems(count int) []protocol.CompletionItem {
	out := make([]protocol.CompletionItem, 0, count)
	for i := range count {
		out = append(out, protocol.CompletionItem{Label: fmt.Sprintf("Candidate%02d", i)})
	}
	return out
}

func makeWorkspaceSymbols(count int) []protocol.WorkspaceSymbolResult {
	out := make([]protocol.WorkspaceSymbolResult, 0, count)
	for i := range count {
		location := protocol.Location{
			URI: fileURI(filepath.Join(os.TempDir(), fmt.Sprintf("symbol-%02d.go", i))),
			Range: protocol.Range{
				Start: protocol.Position{Line: i, Character: 0},
				End:   protocol.Position{Line: i, Character: 1},
			},
		}
		out = append(out, protocol.WorkspaceSymbolResult{SymbolInformation: &protocol.SymbolInformation{
			Name:     fmt.Sprintf("Symbol%02d", i),
			Kind:     protocol.SymbolKindFunction,
			Location: location,
		}})
	}
	return out
}
