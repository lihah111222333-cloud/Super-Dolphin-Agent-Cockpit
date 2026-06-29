package tools

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-lsp/format"
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

func TestRenameRejectsWorkspaceEditOutsideRoots(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "main.go")
	targetOriginal := "package main\n\nfunc oldName() {}\n"
	if err := os.WriteFile(target, []byte(targetOriginal), 0o644); err != nil {
		t.Fatal(err)
	}
	outsideRoot := t.TempDir()
	outside := filepath.Join(outsideRoot, "outside.go")
	outsideOriginal := "package outside\n\nfunc oldName() {}\n"
	if err := os.WriteFile(outside, []byte(outsideOriginal), 0o644); err != nil {
		t.Fatal(err)
	}

	outsideURI := fileURI(outside)
	manager := &renameTestManager{
		renameResult: &protocol.WorkspaceEdit{
			Changes: map[string][]protocol.TextEdit{
				fileURI(target): {{
					Range: protocol.Range{
						Start: protocol.Position{Line: 2, Character: 5},
						End:   protocol.Position{Line: 2, Character: 12},
					},
					NewText: "newName",
				}},
				outsideURI: {{
					Range: protocol.Range{
						Start: protocol.Position{Line: 2, Character: 5},
						End:   protocol.Position{Line: 2, Character: 12},
					},
					NewText: "newName",
				}},
			},
		},
	}
	handler := NewEditHandlerWithRoot(root, &structureTestRegistry{fileManager: manager})

	params, _ := json.Marshal(map[string]any{
		"action":   "rename",
		"pos":      "main.go:3:6",
		"new_name": "newName",
	})
	_, err := handler(testToolContext(root), params)
	if err == nil {
		t.Fatal("rename error = nil, want root validation rejection")
	}
	if !strings.Contains(err.Error(), "outside workspace roots") || !strings.Contains(err.Error(), outsideURI) {
		t.Fatalf("rename error = %v, want outside workspace roots error containing %s", err, outsideURI)
	}
	gotTarget, _ := os.ReadFile(target)
	if string(gotTarget) != targetOriginal {
		t.Fatalf("target content = %q, want unchanged %q", gotTarget, targetOriginal)
	}
	gotOutside, _ := os.ReadFile(outside)
	if string(gotOutside) != outsideOriginal {
		t.Fatalf("outside content = %q, want unchanged %q", gotOutside, outsideOriginal)
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

func TestEditRenameRollbackSyncsPreviouslyWrittenLSPBuffers(t *testing.T) {
	root := t.TempDir()
	file1 := filepath.Join(root, "a.go")
	file2 := filepath.Join(root, "b.go")
	fixtures := map[string]string{
		file1: "package main\n\nfunc oldA() {}\n",
		file2: "package main\n\nfunc oldB() {}\n",
	}
	original := make(map[string]string, len(fixtures))
	for path, content := range fixtures {
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatalf("write fixture %s: %v", path, err)
		}
		original[canonicalRenameURIPath(t, path)] = content
	}

	manager := &renameRollbackSyncManager{
		original: original,
		renameResult: &protocol.WorkspaceEdit{Changes: map[string][]protocol.TextEdit{
			fileURI(file1): {{Range: protocol.Range{
				Start: protocol.Position{Line: 2, Character: 5},
				End:   protocol.Position{Line: 2, Character: 9},
			}, NewText: "newA"}},
			fileURI(file2): {{Range: protocol.Range{
				Start: protocol.Position{Line: 2, Character: 5},
				End:   protocol.Position{Line: 2, Character: 9},
			}, NewText: "newB"}},
		}},
	}
	handler := NewEditHandlerWithRoot(root, &structureTestRegistry{fileManager: manager})

	params, _ := json.Marshal(map[string]any{
		"action":   "rename",
		"pos":      "a.go:3:6",
		"new_name": "new",
	})
	_, err := handler(testToolContext(root), params)
	if err == nil || !strings.Contains(err.Error(), "second file sync failed") {
		t.Fatalf("rename error = %v, want second file sync failure", err)
	}
	for path, want := range fixtures {
		assertFileContent(t, path, want)
	}
	if !manager.sawRollbackForFirstSuccess() {
		t.Fatalf("DidChange calls = %#v, want rollback sync for first successful file %q", manager.didChanges, manager.firstSuccessPath)
	}
}

type renameRollbackSyncManager struct {
	structureTestManager
	renameResult     *protocol.WorkspaceEdit
	original         map[string]string
	firstSuccessPath string
	didChanges       []renameRollbackDidChange
}

type renameRollbackDidChange struct {
	path string
	text string
}

func (m *renameRollbackSyncManager) Rename(context.Context, string, protocol.Position, string) (*protocol.WorkspaceEdit, error) {
	return m.renameResult, nil
}

func (m *renameRollbackSyncManager) DidChange(_ context.Context, uri string, _ int, changes []protocol.TextDocumentContentChangeEvent) error {
	path, err := format.AbsolutePathFromURI(uri)
	if err != nil {
		return err
	}
	text := ""
	if len(changes) > 0 {
		text = changes[0].Text
	}
	m.didChanges = append(m.didChanges, renameRollbackDidChange{path: path, text: text})
	if text == m.original[path] {
		return nil
	}
	if m.firstSuccessPath == "" {
		m.firstSuccessPath = path
		return nil
	}
	return errors.New("second file sync failed")
}

func (m *renameRollbackSyncManager) sawRollbackForFirstSuccess() bool {
	if m.firstSuccessPath == "" {
		return false
	}
	for _, change := range m.didChanges {
		if change.path == m.firstSuccessPath && change.text == m.original[m.firstSuccessPath] {
			return true
		}
	}
	return false
}

func canonicalRenameURIPath(t *testing.T, path string) string {
	t.Helper()
	canonical, err := format.AbsolutePathFromURI(fileURI(path))
	if err != nil {
		t.Fatalf("canonical path for %s: %v", path, err)
	}
	return canonical
}
