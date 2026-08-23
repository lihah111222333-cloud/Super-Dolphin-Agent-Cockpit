package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/mcpserver/common"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/protocol"
)

type xrefReferencesManager struct {
	structureTestManager
	gotIncludeDeclaration bool
	gotPosition           protocol.Position
	gotFilePath           string
}

func (m *xrefReferencesManager) References(_ context.Context, filePath string, position protocol.Position, includeDeclaration bool) ([]protocol.LocationResult, error) {
	m.gotIncludeDeclaration = includeDeclaration
	m.gotPosition = position
	m.gotFilePath = filePath
	return nil, nil
}

func TestReferencesDefaultIncludesDeclaration(t *testing.T) {
	dir, filePath := writeXRefFixture(t)
	manager := &xrefReferencesManager{}
	handler := NewXRefHandler(&structureTestRegistry{fileManager: manager})
	input, err := json.Marshal(xrefParams{
		Action: "references",
		Pos:    filePath + ":1:1",
	})
	if err != nil {
		t.Fatalf("marshal input: %v", err)
	}

	if _, err := handler(common.WithToolScope(context.Background(), common.ToolScope{CWD: dir}), input); err != nil {
		t.Fatalf("references returned error: %v", err)
	}
	if !manager.gotIncludeDeclaration {
		t.Fatalf("includeDeclaration = false, want default true")
	}
}

func TestReferencesCanDisableDeclaration(t *testing.T) {
	dir, filePath := writeXRefFixture(t)
	manager := &xrefReferencesManager{gotIncludeDeclaration: true}
	handler := NewXRefHandler(&structureTestRegistry{fileManager: manager})
	includeDeclaration := false
	input, err := json.Marshal(xrefParams{
		Action:             "references",
		Pos:                filePath + ":1:1",
		IncludeDeclaration: &includeDeclaration,
	})
	if err != nil {
		t.Fatalf("marshal input: %v", err)
	}

	if _, err := handler(common.WithToolScope(context.Background(), common.ToolScope{CWD: dir}), input); err != nil {
		t.Fatalf("references returned error: %v", err)
	}
	if manager.gotIncludeDeclaration {
		t.Fatalf("includeDeclaration = true, want explicit false")
	}
}

func TestCallHierarchyEmptyTypeScriptResultExplainsBootstrapOrCursor(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "app.ts")
	if err := os.WriteFile(filePath, []byte("export function greet(name: string) { return name }\n"), 0o644); err != nil {
		t.Fatalf("write xref fixture: %v", err)
	}
	manager := &structureTestManager{}
	handler := NewXRefHandler(&structureTestRegistry{fileManager: manager})
	input, err := json.Marshal(xrefParams{
		Action:     "call_hierarchy",
		Pos:        filePath + ":1:17",
		LanguageID: "typescript",
		Direction:  "outgoing",
	})
	if err != nil {
		t.Fatalf("marshal input: %v", err)
	}

	got, err := handler(common.WithToolScope(context.Background(), common.ToolScope{CWD: dir}), input)
	if err != nil {
		t.Fatalf("call_hierarchy returned error: %v", err)
	}
	payload, err := json.Marshal(got)
	if err != nil {
		t.Fatalf("marshal result: %v", err)
	}
	var decoded struct {
		Hint string `json:"hint"`
	}
	if err := json.Unmarshal(payload, &decoded); err != nil {
		t.Fatalf("decode result: %v; raw=%s", err, payload)
	}
	hint := strings.ToLower(decoded.Hint)
	for _, want := range []string{"js/ts", "bootstrap", "cursor"} {
		if !strings.Contains(hint, want) {
			t.Fatalf("empty call hierarchy hint = %q, want %q; raw=%s", decoded.Hint, want, payload)
		}
	}
}

func writeXRefFixture(t *testing.T) (string, string) {
	t.Helper()
	dir := t.TempDir()
	filePath := filepath.Join(dir, "sample.go")
	if err := os.WriteFile(filePath, []byte("package sample\n"), 0o644); err != nil {
		t.Fatalf("write xref fixture: %v", err)
	}
	return dir, filePath
}

func TestXRefReferencesAutoSnapTwoPartPos(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "app.go")
	if err := os.WriteFile(filePath, []byte("package app\nfunc CalculateSum() int { return 0 }\n"), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	manager := &xrefReferencesManager{}
	handler := NewXRefHandler(&structureTestRegistry{fileManager: manager})
	input, err := json.Marshal(xrefParams{
		Action: "references",
		Pos:    filePath + ":2",
	})
	if err != nil {
		t.Fatalf("marshal input: %v", err)
	}

	if _, err := handler(common.WithToolScope(context.Background(), common.ToolScope{CWD: dir}), input); err != nil {
		t.Fatalf("references with two-part pos error: %v", err)
	}
	// "func CalculateSum() int" -> "CalculateSum" starts at char 5
	want := protocol.Position{Line: 1, Character: 5}
	if manager.gotPosition != want {
		t.Fatalf("manager position = %#v, want %#v (auto-snapped from two-part pos)", manager.gotPosition, want)
	}
}

func TestXRefReferencesAutoSnapIndentationAndKeywords(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "service.py")
	if err := os.WriteFile(filePath, []byte("    def process_order(order_id):\n        pass\n"), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	manager := &xrefReferencesManager{}
	handler := NewXRefHandler(&structureTestRegistry{fileManager: manager})

	// Pass pos pointing to line 1 col 1 (leading indentation)
	input, err := json.Marshal(xrefParams{
		Action: "references",
		Pos:    filePath + ":1:1",
	})
	if err != nil {
		t.Fatalf("marshal input: %v", err)
	}

	if _, err := handler(common.WithToolScope(context.Background(), common.ToolScope{CWD: dir}), input); err != nil {
		t.Fatalf("references error: %v", err)
	}
	// "    def process_order" -> "process_order" starts at char 8
	want := protocol.Position{Line: 0, Character: 8}
	if manager.gotPosition != want {
		t.Fatalf("manager position = %#v, want %#v (auto-snapped from indentation)", manager.gotPosition, want)
	}
}

func TestXRefReferencesTruncationHint(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "app.go")
	if err := os.WriteFile(filePath, []byte("package app\nfunc Run() {}\n"), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	results := make([]protocol.LocationResult, 20)
	for i := 0; i < 20; i++ {
		results[i] = protocol.LocationResult{
			Location: &protocol.Location{
				URI: "file://" + filePath,
				Range: protocol.Range{
					Start: protocol.Position{Line: i, Character: 0},
					End:   protocol.Position{Line: i, Character: 5},
				},
			},
		}
	}
	manager := &xrefImportTopologyManager{
		refResults: results,
	}
	handler := NewXRefHandler(&structureTestRegistry{fileManager: manager})

	input, err := json.Marshal(xrefParams{
		Action:     "references",
		Pos:        filePath + ":2:6",
		MaxResults: 15, // truncate at 15
	})
	if err != nil {
		t.Fatalf("marshal input: %v", err)
	}
	got, err := handler(common.WithToolScope(context.Background(), common.ToolScope{CWD: dir}), input)
	if err != nil {
		t.Fatalf("references returned error: %v", err)
	}
	grouped := got.(protocol.GroupedLocationResult)
	if !grouped.Truncated {
		t.Fatalf("expected truncated results")
	}
	if !strings.Contains(grouped.Hint, "next: increase max_results or narrow query with file_path") {
		t.Fatalf("truncated hint = %q, want 'next: increase max_results or narrow query with file_path'", grouped.Hint)
	}
}
