package tools

import (
	"context"
	"encoding/json"
	"github.com/anthropic-ai/super-agent-v3/internal/mcpserver/common"
	"testing"

	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-lsp/protocol"
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
	manager := &xrefReferencesManager{}
	handler := NewXRefHandler(&structureTestRegistry{fileManager: manager})
	input, err := json.Marshal(xrefParams{
		Action:   "references",
		FilePath: "/tmp/sample.go",
		Line:     1,
		Column:   1,
	})
	if err != nil {
		t.Fatalf("marshal input: %v", err)
	}

	if _, err := handler(common.WithToolScope(context.Background(), common.ToolScope{CWD: "/"}), input); err != nil {
		t.Fatalf("references returned error: %v", err)
	}
	if !manager.gotIncludeDeclaration {
		t.Fatalf("includeDeclaration = false, want default true")
	}
}

func TestReferencesCanDisableDeclaration(t *testing.T) {
	manager := &xrefReferencesManager{gotIncludeDeclaration: true}
	handler := NewXRefHandler(&structureTestRegistry{fileManager: manager})
	includeDeclaration := false
	input, err := json.Marshal(xrefParams{
		Action:             "references",
		FilePath:           "/tmp/sample.go",
		Line:               1,
		Column:             1,
		IncludeDeclaration: &includeDeclaration,
	})
	if err != nil {
		t.Fatalf("marshal input: %v", err)
	}

	if _, err := handler(common.WithToolScope(context.Background(), common.ToolScope{CWD: "/"}), input); err != nil {
		t.Fatalf("references returned error: %v", err)
	}
	if manager.gotIncludeDeclaration {
		t.Fatalf("includeDeclaration = true, want explicit false")
	}
}
