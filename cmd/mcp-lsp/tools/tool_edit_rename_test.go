package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	lspmanager "github.com/anthropic-ai/super-agent-v3/cmd/mcp-lsp/manager"
	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-lsp/protocol"
)

type renameTestManager struct {
	structureTestManager
	renameResult *protocol.WorkspaceEdit
	renameErr    error
}

func (m *renameTestManager) Rename(_ context.Context, _ string, _ protocol.Position, _ string) (*protocol.WorkspaceEdit, error) {
	return m.renameResult, m.renameErr
}

func TestEditRenameHappyPath(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "main.go")
	if err := os.WriteFile(target, []byte("package main\n\nfunc oldName() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	manager := &renameTestManager{
		renameResult: &protocol.WorkspaceEdit{
			Changes: map[string][]protocol.TextEdit{
				fileURI(target): {
					{Range: protocol.Range{
						Start: protocol.Position{Line: 2, Character: 5},
						End:   protocol.Position{Line: 2, Character: 12},
					}, NewText: "newName"},
				},
			},
		},
	}
	handler := NewEditHandlerWithRoot(root, &structureTestRegistry{fileManager: manager})

	params, _ := json.Marshal(map[string]any{
		"action":   "rename",
		"pos":      "main.go:3:6",
		"new_name": "newName",
	})

	result, err := handler(testToolContext(root), params)
	if err != nil {
		t.Fatalf("handleRename() error = %v", err)
	}
	resp, ok := result.(renameResult)
	if !ok {
		t.Fatalf("result type = %T, want renameResult", result)
	}
	if !resp.Success {
		t.Fatalf("rename success = false, want true")
	}
	if len(resp.AffectedFiles) != 1 {
		t.Fatalf("affected files = %d, want 1", len(resp.AffectedFiles))
	}
	got, _ := os.ReadFile(target)
	if !strings.Contains(string(got), "newName") {
		t.Fatalf("file content after rename = %q, want containing 'newName'", got)
	}
}
func TestEditRenameMultiFile(t *testing.T) {
	root := t.TempDir()
	file1 := filepath.Join(root, "a.go")
	file2 := filepath.Join(root, "b.go")
	os.WriteFile(file1, []byte("package main\n\nfunc oldName() {}\n"), 0o644)
	os.WriteFile(file2, []byte("package main\n\nvar x = oldName\n"), 0o644)

	manager := &renameTestManager{
		renameResult: &protocol.WorkspaceEdit{
			Changes: map[string][]protocol.TextEdit{
				fileURI(file1): {{Range: protocol.Range{
					Start: protocol.Position{Line: 2, Character: 5},
					End:   protocol.Position{Line: 2, Character: 12},
				}, NewText: "newName"}},
				fileURI(file2): {{Range: protocol.Range{
					Start: protocol.Position{Line: 2, Character: 8},
					End:   protocol.Position{Line: 2, Character: 15},
				}, NewText: "newName"}},
			},
		},
	}
	handler := NewEditHandlerWithRoot(root, &structureTestRegistry{fileManager: manager})

	params, _ := json.Marshal(map[string]any{
		"action":   "rename",
		"pos":      "a.go:3:6",
		"new_name": "newName",
	})
	result, err := handler(testToolContext(root), params)
	if err != nil {
		t.Fatalf("multi-file rename error = %v", err)
	}
	resp := result.(renameResult)
	if len(resp.AffectedFiles) != 2 {
		t.Fatalf("affected files = %d, want 2", len(resp.AffectedFiles))
	}
}

func TestEditRenameEmptyEdit(t *testing.T) {
	root := t.TempDir()
	os.WriteFile(filepath.Join(root, "main.go"), []byte("package main\n"), 0o644)

	manager := &renameTestManager{
		renameResult: &protocol.WorkspaceEdit{},
	}
	handler := NewEditHandlerWithRoot(root, &structureTestRegistry{fileManager: manager})

	params, _ := json.Marshal(map[string]any{
		"action":   "rename",
		"pos":      "main.go:1:9",
		"new_name": "foo",
	})
	result, err := handler(testToolContext(root), params)
	if err != nil {
		t.Fatalf("empty rename error = %v", err)
	}
	resp := result.(renameResult)
	if !resp.Success {
		t.Fatal("expected success for empty edit")
	}
}
func TestEditRenameRequiresNewName(t *testing.T) {
	root := t.TempDir()
	handler := NewEditHandlerWithRoot(root, &structureTestRegistry{fileErr: lspmanager.ErrUnsupportedLanguage})

	params, _ := json.Marshal(map[string]any{
		"action": "rename",
		"pos":    "main.go:3:6",
	})
	_, err := handler(testToolContext(root), params)
	if err == nil || !strings.Contains(err.Error(), "rename requires new_name") {
		t.Fatalf("expected 'rename requires new_name' error, got: %v", err)
	}
}

func TestEditRenameRequiresPos(t *testing.T) {
	root := t.TempDir()
	handler := NewEditHandlerWithRoot(root, &structureTestRegistry{fileErr: lspmanager.ErrUnsupportedLanguage})

	params, _ := json.Marshal(map[string]any{
		"action":   "rename",
		"new_name": "foo",
	})
	_, err := handler(testToolContext(root), params)
	if err == nil || !strings.Contains(err.Error(), "rename requires pos") {
		t.Fatalf("expected 'rename requires pos' error, got: %v", err)
	}
}

func TestEditRenameRollbackOnWriteFailure(t *testing.T) {
	root := t.TempDir()
	file1 := filepath.Join(root, "a.go")
	os.WriteFile(file1, []byte("package main\n\nfunc old() {}\n"), 0o644)
	file2 := filepath.Join(root, "nonexistent", "b.go")

	manager := &renameTestManager{
		renameResult: &protocol.WorkspaceEdit{
			Changes: map[string][]protocol.TextEdit{
				"file://" + file1: {{Range: protocol.Range{
					Start: protocol.Position{Line: 2, Character: 5},
					End:   protocol.Position{Line: 2, Character: 8},
				}, NewText: "new"}},
				"file://" + file2: {{Range: protocol.Range{
					Start: protocol.Position{Line: 0, Character: 0},
					End:   protocol.Position{Line: 0, Character: 3},
				}, NewText: "new"}},
			},
		},
	}
	handler := NewEditHandlerWithRoot(root, &structureTestRegistry{fileManager: manager})

	params, _ := json.Marshal(map[string]any{
		"action":   "rename",
		"pos":      "a.go:3:6",
		"new_name": "new",
	})
	_, err := handler(testToolContext(root), params)
	if err == nil {
		t.Fatal("expected error for write failure")
	}
	got, _ := os.ReadFile(file1)
	if strings.Contains(string(got), "func new()") {
		t.Fatal("file1 should have been rolled back but contains new content")
	}
}
