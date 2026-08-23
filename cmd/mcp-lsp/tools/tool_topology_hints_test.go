package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/protocol"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/mcpserver/common"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/mcpserver/common/lineprotocol"
)

type xrefImportTopologyManager struct {
	structureTestManager
	defResults []protocol.LocationResult
	refResults []protocol.LocationResult
}

func (m *xrefImportTopologyManager) Definition(_ context.Context, _ string, _ protocol.Position) ([]protocol.LocationResult, error) {
	return m.defResults, nil
}

func (m *xrefImportTopologyManager) References(_ context.Context, _ string, _ protocol.Position, _ bool) ([]protocol.LocationResult, error) {
	return m.refResults, nil
}

func TestStructureDocumentSymbolEmptyHint(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "empty.go")
	if err := os.WriteFile(filePath, []byte("package empty\n"), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	manager := &structureTestManager{documentSymbols: nil}
	handler := NewStructureHandler(&structureTestRegistry{fileManager: manager})
	input, err := json.Marshal(structureParams{
		Action:   "document_symbol",
		FilePath: filePath,
	})
	if err != nil {
		t.Fatalf("marshal input: %v", err)
	}
	got, err := handler(common.WithToolScope(context.Background(), common.ToolScope{CWD: dir}), input)
	if err != nil {
		t.Fatalf("document_symbol returned error: %v", err)
	}
	resp, ok := got.(documentSymbolListResponse)
	if !ok {
		t.Fatalf("result type = %T, want documentSymbolListResponse", got)
	}
	if !strings.Contains(resp.Hint, "no symbols found in document") {
		t.Fatalf("resp.Hint = %q, want 'no symbols found in document'", resp.Hint)
	}
	plainText := resp.ToPlainText()
	if !strings.Contains(plainText, "HINT\tno symbols found in document") {
		t.Fatalf("plain text missing HINT record: %q", plainText)
	}
}

func TestStructureWorkspaceSymbolZeroResultsHint(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "sample.go")
	if err := os.WriteFile(filePath, []byte("package sample\n"), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	manager := &structureTestManager{workspaceSymbols: nil}
	handler := NewStructureHandler(&structureTestRegistry{fileManager: manager})

	// Exact mode 0-result hint
	inputExact, err := json.Marshal(structureParams{
		Action:    "workspace_symbol",
		FilePath:  filePath,
		Query:     "NonExistentSymbol",
		MatchMode: "exact",
	})
	if err != nil {
		t.Fatalf("marshal exact: %v", err)
	}
	gotExact, err := handler(common.WithToolScope(context.Background(), common.ToolScope{CWD: dir}), inputExact)
	if err != nil {
		t.Fatalf("workspace_symbol exact error: %v", err)
	}
	respExact := gotExact.(workspaceSymbolListResponse)
	if !strings.Contains(respExact.Hint, "no symbols found matching exact query") ||
		!strings.Contains(respExact.Hint, "match_mode=fuzzy") ||
		!strings.Contains(respExact.Hint, "document_symbol") {
		t.Fatalf("exact 0-result hint = %q, want casing/fuzzy/document_symbol guidance", respExact.Hint)
	}

	// Fuzzy mode 0-result hint
	inputFuzzy, err := json.Marshal(structureParams{
		Action:    "workspace_symbol",
		FilePath:  filePath,
		Query:     "NonExistentSymbol",
		MatchMode: "fuzzy",
	})
	if err != nil {
		t.Fatalf("marshal fuzzy: %v", err)
	}
	gotFuzzy, err := handler(common.WithToolScope(context.Background(), common.ToolScope{CWD: dir}), inputFuzzy)
	if err != nil {
		t.Fatalf("workspace_symbol fuzzy error: %v", err)
	}
	respFuzzy := gotFuzzy.(workspaceSymbolListResponse)
	if !strings.Contains(respFuzzy.Hint, "no symbols found") ||
		!strings.Contains(respFuzzy.Hint, "document_symbol") {
		t.Fatalf("fuzzy 0-result hint = %q, want document_symbol guidance", respFuzzy.Hint)
	}
}

func TestStructureWorkspaceSymbolTruncationNarrowingHint(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "symbols.go")
	if err := os.WriteFile(filePath, []byte("package symbols\n"), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	symbols := make([]protocol.WorkspaceSymbolResult, 10)
	for i := 0; i < 10; i++ {
		symbols[i] = protocol.WorkspaceSymbolResult{
			SymbolInformation: &protocol.SymbolInformation{
				Name: fmt.Sprintf("TargetSymbol%d", i),
				Kind: protocol.SymbolKindFunction,
				Location: protocol.Location{
					URI: "file://" + filePath,
					Range: protocol.Range{
						Start: protocol.Position{Line: i, Character: 0},
						End:   protocol.Position{Line: i, Character: 10},
					},
				},
			},
		}
	}
	manager := &structureTestManager{workspaceSymbols: symbols}
	handler := NewStructureHandler(&structureTestRegistry{fileManager: manager})

	// Fuzzy truncated hint
	inputFuzzy, err := json.Marshal(structureParams{
		Action:     "workspace_symbol",
		FilePath:   filePath,
		Query:      "TargetSymbol",
		MatchMode:  "fuzzy",
		MaxResults: 3,
	})
	if err != nil {
		t.Fatalf("marshal fuzzy: %v", err)
	}
	gotFuzzy, err := handler(common.WithToolScope(context.Background(), common.ToolScope{CWD: dir}), inputFuzzy)
	if err != nil {
		t.Fatalf("workspace_symbol fuzzy error: %v", err)
	}
	respFuzzy := gotFuzzy.(workspaceSymbolListResponse)
	if !respFuzzy.Truncated {
		t.Fatalf("respFuzzy.Truncated = false, want true")
	}
	if !strings.Contains(respFuzzy.Hint, "workspace_symbol results truncated") ||
		!strings.Contains(respFuzzy.Hint, "match_mode=exact") ||
		!strings.Contains(respFuzzy.Hint, "container/class prefix") {
		t.Fatalf("fuzzy truncated hint = %q, want container prefix or exact guidance", respFuzzy.Hint)
	}
}

func TestStructureFoldingAndSemanticTokensEmptyHint(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "empty.go")
	if err := os.WriteFile(filePath, []byte("package empty\n"), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	manager := &structureTestManager{}
	handler := NewStructureHandler(&structureTestRegistry{fileManager: manager})

	// Folding range empty
	inputFolding, err := json.Marshal(structureParams{
		Action:   "folding_range",
		FilePath: filePath,
	})
	if err != nil {
		t.Fatalf("marshal folding: %v", err)
	}
	gotFolding, err := handler(common.WithToolScope(context.Background(), common.ToolScope{CWD: dir}), inputFolding)
	if err != nil {
		t.Fatalf("folding_range error: %v", err)
	}
	respFolding := gotFolding.(foldingRangeListResponse)
	if respFolding.Hint != "no folding ranges found in file" {
		t.Fatalf("folding range hint = %q, want 'no folding ranges found in file'", respFolding.Hint)
	}
}

func TestXRefReferencesZeroResultsHint(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "app.go")
	if err := os.WriteFile(filePath, []byte("package app\nfunc Run() {}\n"), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	manager := &xrefImportTopologyManager{
		defResults: nil,
		refResults: nil,
	}
	handler := NewXRefHandler(&structureTestRegistry{fileManager: manager})
	input, err := json.Marshal(xrefParams{
		Action: "references",
		Pos:    filePath + ":2:6",
	})
	if err != nil {
		t.Fatalf("marshal input: %v", err)
	}
	got, err := handler(common.WithToolScope(context.Background(), common.ToolScope{CWD: dir}), input)
	if err != nil {
		t.Fatalf("references returned error: %v", err)
	}
	grouped := got.(protocol.GroupedLocationResult)
	if !strings.Contains(grouped.Hint, "no references found") || !strings.Contains(grouped.Hint, "document_symbol") {
		t.Fatalf("0-result references hint = %q, want document_symbol guidance", grouped.Hint)
	}
}

func TestXRefReferencesImportedSymbolTopologyHint(t *testing.T) {
	dir := t.TempDir()
	callerPath := filepath.Join(dir, "caller.go")
	defPath := filepath.Join(dir, "pkg", "dep.go")
	if err := os.MkdirAll(filepath.Dir(defPath), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(callerPath, []byte("package main\nimport \"dep\"\nfunc Main() { dep.Helper() }\n"), 0o644); err != nil {
		t.Fatalf("write caller: %v", err)
	}
	if err := os.WriteFile(defPath, []byte("package dep\nfunc Helper() {}\n"), 0o644); err != nil {
		t.Fatalf("write dep: %v", err)
	}

	defLoc := protocol.LocationResult{
		Location: &protocol.Location{
			URI: "file://" + defPath,
			Range: protocol.Range{
				Start: protocol.Position{Line: 1, Character: 5},
				End:   protocol.Position{Line: 1, Character: 11},
			},
		},
	}
	refLoc := protocol.LocationResult{
		Location: &protocol.Location{
			URI: "file://" + callerPath,
			Range: protocol.Range{
				Start: protocol.Position{Line: 2, Character: 18},
				End:   protocol.Position{Line: 2, Character: 24},
			},
		},
	}

	manager := &xrefImportTopologyManager{
		defResults: []protocol.LocationResult{defLoc},
		refResults: []protocol.LocationResult{refLoc},
	}
	handler := NewXRefHandler(&structureTestRegistry{fileManager: manager})
	input, err := json.Marshal(xrefParams{
		Action: "references",
		Pos:    callerPath + ":3:19",
	})
	if err != nil {
		t.Fatalf("marshal input: %v", err)
	}
	got, err := handler(common.WithToolScope(context.Background(), common.ToolScope{CWD: dir}), input)
	if err != nil {
		t.Fatalf("references error: %v", err)
	}
	grouped := got.(protocol.GroupedLocationResult)
	if !strings.Contains(grouped.Hint, "symbol imported from") || !strings.Contains(grouped.Hint, "dep.go") {
		t.Fatalf("imported symbol hint = %q, want 'symbol imported from ... dep.go'", grouped.Hint)
	}
	plainText := formatGroupedLocationsContract(grouped)
	if !strings.Contains(plainText, "HINT\tsymbol imported from") {
		t.Fatalf("plain text missing imported symbol HINT: %q", plainText)
	}
}

func TestXRefHierarchyZeroResultsHints(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "app.go")
	if err := os.WriteFile(filePath, []byte("package app\n"), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	manager := &structureTestManager{}
	handler := NewXRefHandler(&structureTestRegistry{fileManager: manager})

	// Call hierarchy 0 results
	inputCall, err := json.Marshal(xrefParams{
		Action:     "call_hierarchy",
		Pos:        filePath + ":1:1",
		LanguageID: "go",
		Direction:  "incoming",
	})
	if err != nil {
		t.Fatalf("marshal call: %v", err)
	}
	gotCall, err := handler(common.WithToolScope(context.Background(), common.ToolScope{CWD: dir}), inputCall)
	if err != nil {
		t.Fatalf("call_hierarchy error: %v", err)
	}
	respCall := gotCall.(hierarchyEdgeListResponse)
	if !strings.Contains(respCall.Hint, "no call hierarchy found") || !strings.Contains(respCall.Hint, "document_symbol") {
		t.Fatalf("call hierarchy 0-result hint = %q, want document_symbol guidance", respCall.Hint)
	}

	// Type hierarchy 0 results
	inputType, err := json.Marshal(xrefParams{
		Action:    "type_hierarchy",
		Pos:       filePath + ":1:1",
		Direction: "supertypes",
	})
	if err != nil {
		t.Fatalf("marshal type: %v", err)
	}
	gotType, err := handler(common.WithToolScope(context.Background(), common.ToolScope{CWD: dir}), inputType)
	if err != nil {
		t.Fatalf("type_hierarchy error: %v", err)
	}
	respType := gotType.(hierarchyEdgeListResponse)
	if !strings.Contains(respType.Hint, "no type hierarchy found") || !strings.Contains(respType.Hint, "document_symbol") {
		t.Fatalf("type hierarchy 0-result hint = %q, want document_symbol guidance", respType.Hint)
	}
}

func TestDiagnosticsDiffHunkFocusHint(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "broken.go")
	if err := os.WriteFile(filePath, []byte("package main\n"), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	targetURI := canonicalFileURI(t, filePath)
	registry := &diagnosticsTestRegistry{
		diagnosticsByURI: map[string][]protocol.Diagnostic{
			targetURI: {
				{
					Range: protocol.Range{
						Start: protocol.Position{Line: 1, Character: 1},
						End:   protocol.Position{Line: 1, Character: 5},
					},
					Severity: protocol.SeverityError,
					Source:   "compiler",
					Message:  "syntax error",
				},
			},
		},
	}
	handler := NewDiagnosticsHandler(Config{WorkspaceRoot: dir, Registry: registry})
	input, err := json.Marshal(diagnosticsInput{FilePath: "broken.go"})
	if err != nil {
		t.Fatalf("marshal input: %v", err)
	}

	got, err := handler(common.WithToolScope(context.Background(), common.ToolScope{CWD: dir}), input)
	if err != nil {
		t.Fatalf("diagnostics handler returned error: %v", err)
	}
	resp, ok := got.(diagnosticsResponse)
	if !ok {
		t.Fatalf("result type = %T, want diagnosticsResponse", got)
	}
	if !strings.Contains(resp.Hint, "active diff/patch hunks") || !strings.Contains(resp.Hint, "apply_patch") {
		t.Fatalf("diagnostics hint = %q, want diff hunk and apply_patch guidance", resp.Hint)
	}
	plainText := resp.ToPlainText()
	doc, err := lineprotocol.Parse(plainText)
	if err != nil {
		t.Fatalf("parse diagnostics plain text: %v", err)
	}
	if doc.Header.Total != 1 || doc.Header.Unit != "diagnostic" {
		t.Fatalf("unexpected header: %#v", doc.Header)
	}
	var hintVal string
	for _, record := range doc.Records {
		if record.Kind == "HINT" {
			hintVal = record.Value
		}
	}
	if !strings.Contains(hintVal, "active diff/patch hunks") || !strings.Contains(hintVal, "apply_patch") {
		t.Fatalf("diagnostics record HINT = %q, want diff hunk and apply_patch guidance", hintVal)
	}
}

func formatGroupedLocationsContract(result protocol.GroupedLocationResult) string {
	text, _ := FormatToPlainText(result)
	return text
}
