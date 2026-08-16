package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/format"
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/protocol"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/mcpserver/common"
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
	payload := mustMarshalObject(t, grouped)
	if payload["truncated"] != true {
		t.Fatalf("compact references truncated = %#v, want true", payload["truncated"])
	}
}

func TestInspectLocationResultUsesDataEnvelope(t *testing.T) {
	root, filePath := writeXRefFixture(t)
	handler := NewInspectHandler(&structureTestRegistry{
		fileManager: &structureTestManager{definitions: []protocol.LocationResult{makeLocationResult(filePath, 1, 1)}},
	})

	got := callInspectTool(t, handler, root, filePath, inspectParams{Action: "definition"})
	payload := mustMarshalObject(t, got)
	data, ok := payload["data"].(map[string]any)
	if !ok {
		t.Fatalf("inspect data = %#v, want grouped location map", payload["data"])
	}
	if _, ok := data["sample.go"]; !ok {
		t.Fatalf("inspect data keys = %#v, want relative file key sample.go", data)
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
	payload := mustMarshalObject(t, list)
	if payload["truncated"] != true {
		t.Fatalf("compact completion truncated = %#v, want true", payload["truncated"])
	}
	if hint, _ := payload["hint"].(string); !strings.Contains(hint, "max_results") || !strings.Contains(hint, "cursor") {
		t.Fatalf("compact completion hint = %q, want max_results/cursor guidance", hint)
	}
}

// TestWorkspaceSymbolCompactBudget mirrors the references coverage for
// workspace_symbol queries.
func TestWorkspaceSymbolCompactBudget(t *testing.T) {
	manager := &structureTestManager{workspaceSymbols: makeWorkspaceSymbols(60)}
	handler := NewStructureHandler(&structureTestRegistry{languageManager: manager})
	root := t.TempDir()

	compact := callStructureTool(t, handler, root, structureParams{Action: "workspace_symbol", Query: "Symbol", Language: "go", MatchMode: "fuzzy"})
	list, ok := compact.(format.CompactList[format.CompactWorkspaceSymbol])
	if !ok {
		t.Fatalf("compact workspace_symbol result = %T, want CompactList", compact)
	}
	if list.Total != 60 || list.Showing != 20 {
		t.Fatalf("compact workspace_symbol total/showing = %d/%d, want 60/20", list.Total, list.Showing)
	}
	payload := mustMarshalObject(t, list)
	if payload["truncated"] != true {
		t.Fatalf("compact workspace_symbol truncated = %#v, want true", payload["truncated"])
	}
	if hint, _ := payload["hint"].(string); !strings.Contains(hint, "max_results") || !strings.Contains(hint, "query") {
		t.Fatalf("compact workspace_symbol hint = %q, want max_results/query guidance", hint)
	}
}

func TestCallHierarchyResultUsesCompactLocations(t *testing.T) {
	root, filePath := writeXRefFixture(t)
	handler := NewXRefHandler(&structureTestRegistry{
		fileManager: &structureTestManager{callHierarchy: makeCallHierarchyResults(filePath)},
	})

	got := callXRefTool(t, handler, root, filePath, xrefParams{Action: "call_hierarchy", Direction: "outgoing"})
	response, ok := got.(hierarchyEdgeListResponse)
	if !ok || response.Total != 1 || len(response.Rows) != 1 {
		t.Fatalf("call_hierarchy result = %#v, want one compact edge", got)
	}
	row := response.Rows[0]
	assertHierarchyEdgeLocation(t, row, "outgoing", "sample.go", 6, 7)
	assertHierarchySingleSite(t, row, "sample.go", 8, 9)
}

func assertHierarchyEdgeLocation(t *testing.T, row hierarchyEdgeRow, direction, file string, line, col int) {
	t.Helper()
	if row.Direction != direction || row.Item.File != file || row.Item.Line != line || row.Item.Col != col {
		t.Fatalf("call_hierarchy edge = %#v, want %s %s:%d:%d", row, direction, file, line, col)
	}
}

func assertHierarchySingleSite(t *testing.T, row hierarchyEdgeRow, file string, line, col int) {
	t.Helper()
	if row.SitesTotal != 1 || len(row.Sites) != 1 || row.Sites[0].File != file || row.Sites[0].Line != line || row.Sites[0].Col != col {
		t.Fatalf("call_hierarchy call site = %#v, want %s:%d:%d", row.Sites, file, line, col)
	}
}

func callXRefTool(t *testing.T, handler ToolHandler, root string, filePath string, params xrefParams) any {
	t.Helper()
	params.Pos = fmt.Sprintf("%s:1:1", filePath)
	return callToolHandler(t, handler, root, params)
}

func callInspectTool(t *testing.T, handler ToolHandler, root string, filePath string, params inspectParams) any {
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
		out = append(out, makeLocationResult(filepath.Join(os.TempDir(), fmt.Sprintf("ref-%02d.go", i)), i+1, 1))
	}
	return out
}

func makeLocationResult(filePath string, line int, col int) protocol.LocationResult {
	location := protocol.Location{URI: fileURI(filePath), Range: makeRange(line, col)}
	return protocol.LocationResult{Location: &location}
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

func makeRange(line int, col int) protocol.Range {
	start := protocol.Position{Line: line - 1, Character: col - 1}
	end := protocol.Position{Line: start.Line, Character: start.Character + 1}
	return protocol.Range{Start: start, End: end}
}

func makeCallHierarchyResults(filePath string) []protocol.CallHierarchyResult {
	return []protocol.CallHierarchyResult{{
		Item: makeCallHierarchyItem("root", filePath, 1, 1),
		Incoming: []protocol.CallHierarchyIncomingCall{{
			From:       makeCallHierarchyItem("caller", filePath, 2, 3),
			FromRanges: []protocol.Range{makeRange(4, 5)},
		}},
		Outgoing: []protocol.CallHierarchyOutgoingCall{{
			To:         makeCallHierarchyItem("callee", filePath, 6, 7),
			FromRanges: []protocol.Range{makeRange(8, 9)},
		}},
	}}
}

func makeCallHierarchyItem(name string, filePath string, line int, col int) protocol.CallHierarchyItem {
	return protocol.CallHierarchyItem{
		Name:           name,
		Kind:           int(protocol.SymbolKindFunction),
		URI:            fileURI(filePath),
		Range:          makeRange(line, col),
		SelectionRange: makeRange(line, col),
	}
}
