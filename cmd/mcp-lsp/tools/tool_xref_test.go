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
}

func (m *xrefReferencesManager) References(_ context.Context, _ string, _ protocol.Position, includeDeclaration bool) ([]protocol.LocationResult, error) {
	m.gotIncludeDeclaration = includeDeclaration
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
