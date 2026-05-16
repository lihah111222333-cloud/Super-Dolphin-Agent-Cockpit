package tools

import (
	"context"
	"encoding/json"
	"github.com/anthropic-ai/super-agent-v3/internal/mcpserver/common"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-lsp/protocol"
)

type editRootBoundaryManager struct {
	structureTestManager
	renameEdit         *protocol.WorkspaceEdit
	codeActions        []protocol.CodeActionResult
	formatEdits        []protocol.TextEdit
	gotCodeActionRange protocol.Range
}

func (m *editRootBoundaryManager) Rename(context.Context, string, protocol.Position, string) (*protocol.WorkspaceEdit, error) {
	return m.renameEdit, nil
}

func (m *editRootBoundaryManager) CodeAction(_ context.Context, _ string, rng protocol.Range, _ []string) ([]protocol.CodeActionResult, error) {
	m.gotCodeActionRange = rng
	return m.codeActions, nil
}

func (m *editRootBoundaryManager) Format(context.Context, string, protocol.FormattingOptions) ([]protocol.TextEdit, error) {
	return m.formatEdits, nil
}

func TestCodeActionRejectsPathOutsideWorkspaceRootBeforeLSPRequest(t *testing.T) {
	root := t.TempDir()
	outsideDir := t.TempDir()
	outside := filepath.Join(outsideDir, "outside.go")
	if err := os.WriteFile(outside, []byte("package main\n"), 0o600); err != nil {
		t.Fatalf("write outside fixture: %v", err)
	}
	registry := &structureTestRegistry{fileManager: &structureTestManager{}}
	handler := NewEditHandlerWithRoot(root, registry)
	input, err := json.Marshal(EditRequest{Action: "code_action", FilePath: outside, Line: 1, Column: 1})
	if err != nil {
		t.Fatalf("marshal input: %v", err)
	}

	_, err = handler(common.WithToolScope(context.Background(), common.ToolScope{CWD: root}), input)
	if err == nil || !strings.Contains(err.Error(), "outside workspace root") {
		t.Fatalf("code_action error = %v, want outside workspace root", err)
	}
	if registry.gotFilePath != "" {
		t.Fatalf("GetManagerForFile called with %q; want no LSP request for outside root", registry.gotFilePath)
	}
}

func TestFormatRejectsPathOutsideWorkspaceRootBeforeLSPRequest(t *testing.T) {
	root := t.TempDir()
	outsideDir := t.TempDir()
	outside := filepath.Join(outsideDir, "outside.go")
	if err := os.WriteFile(outside, []byte("package main\n"), 0o600); err != nil {
		t.Fatalf("write outside fixture: %v", err)
	}
	registry := &structureTestRegistry{fileManager: &structureTestManager{}}
	handler := NewEditHandlerWithRoot(root, registry)
	input, err := json.Marshal(EditRequest{Action: "format", FilePath: outside})
	if err != nil {
		t.Fatalf("marshal input: %v", err)
	}

	_, err = handler(common.WithToolScope(context.Background(), common.ToolScope{CWD: root}), input)
	if err == nil || !strings.Contains(err.Error(), "outside workspace root") {
		t.Fatalf("format error = %v, want outside workspace root", err)
	}
	if registry.gotFilePath != "" {
		t.Fatalf("GetManagerForFile called with %q; want no LSP request for outside root", registry.gotFilePath)
	}
}

func TestPreparedRenameRejectsWorkspaceEditOutsideWorkspaceRoot(t *testing.T) {
	root := t.TempDir()
	inside := filepath.Join(root, "inside.go")
	if err := os.WriteFile(inside, []byte("package main\n\nvar oldName = 1\n"), 0o600); err != nil {
		t.Fatalf("write inside fixture: %v", err)
	}
	outsideDir := t.TempDir()
	outside := filepath.Join(outsideDir, "outside.go")
	if err := os.WriteFile(outside, []byte("package main\n"), 0o600); err != nil {
		t.Fatalf("write outside fixture: %v", err)
	}
	persist := false
	manager := &editRootBoundaryManager{renameEdit: &protocol.WorkspaceEdit{Changes: map[string][]protocol.TextEdit{
		fileURI(outside): {{
			Range:   protocol.Range{Start: protocol.Position{Line: 0, Character: 0}, End: protocol.Position{Line: 0, Character: 0}},
			NewText: "// unsafe\n",
		}},
	}}}
	handler := NewEditHandlerWithRoot(root, &structureTestRegistry{fileManager: manager})
	input, err := json.Marshal(EditRequest{Action: "rename", FilePath: inside, Line: 3, Column: 5, NewName: "newName", PersistToDisk: &persist})
	if err != nil {
		t.Fatalf("marshal input: %v", err)
	}

	_, err = handler(common.WithToolScope(context.Background(), common.ToolScope{CWD: root}), input)
	if err == nil || !strings.Contains(err.Error(), "outside workspace root") {
		t.Fatalf("prepared rename error = %v, want outside workspace root", err)
	}
}

func TestCodeActionRejectsReturnedWorkspaceEditOutsideWorkspaceRoot(t *testing.T) {
	root := t.TempDir()
	inside := filepath.Join(root, "inside.go")
	if err := os.WriteFile(inside, []byte("package main\n"), 0o600); err != nil {
		t.Fatalf("write inside fixture: %v", err)
	}
	outsideDir := t.TempDir()
	outside := filepath.Join(outsideDir, "outside.go")
	if err := os.WriteFile(outside, []byte("package main\n"), 0o600); err != nil {
		t.Fatalf("write outside fixture: %v", err)
	}
	manager := &editRootBoundaryManager{codeActions: []protocol.CodeActionResult{{CodeAction: &protocol.CodeAction{
		Title: "unsafe",
		Edit: &protocol.WorkspaceEdit{Changes: map[string][]protocol.TextEdit{
			fileURI(outside): {{
				Range:   protocol.Range{Start: protocol.Position{Line: 0, Character: 0}, End: protocol.Position{Line: 0, Character: 0}},
				NewText: "// unsafe\n",
			}},
		}},
	}}}}
	handler := NewEditHandlerWithRoot(root, &structureTestRegistry{fileManager: manager})
	input, err := json.Marshal(EditRequest{Action: "code_action", FilePath: inside, Line: 1, Column: 1})
	if err != nil {
		t.Fatalf("marshal input: %v", err)
	}

	_, err = handler(common.WithToolScope(context.Background(), common.ToolScope{CWD: root}), input)
	if err == nil || !strings.Contains(err.Error(), "outside workspace root") {
		t.Fatalf("code_action returned edit error = %v, want outside workspace root", err)
	}
}

func TestCodeActionUsesEndLineEndColumnRange(t *testing.T) {
	root := t.TempDir()
	inside := filepath.Join(root, "inside.go")
	if err := os.WriteFile(inside, []byte("package main\n\nfunc f() {}\n"), 0o600); err != nil {
		t.Fatalf("write inside fixture: %v", err)
	}
	manager := &editRootBoundaryManager{}
	handler := NewEditHandlerWithRoot(root, &structureTestRegistry{fileManager: manager})
	input, err := json.Marshal(EditRequest{
		Action:    "code_action",
		FilePath:  inside,
		Line:      2,
		Column:    3,
		EndLine:   4,
		EndColumn: 5,
	})
	if err != nil {
		t.Fatalf("marshal input: %v", err)
	}

	if _, err := handler(common.WithToolScope(context.Background(), common.ToolScope{CWD: root}), input); err != nil {
		t.Fatalf("code_action returned error: %v", err)
	}
	want := protocol.Range{
		Start: protocol.Position{Line: 1, Character: 2},
		End:   protocol.Position{Line: 3, Character: 4},
	}
	if manager.gotCodeActionRange != want {
		t.Fatalf("code_action range = %#v, want %#v", manager.gotCodeActionRange, want)
	}
}

func TestCodeActionDefaultsEndToStart(t *testing.T) {
	root := t.TempDir()
	inside := filepath.Join(root, "inside.go")
	if err := os.WriteFile(inside, []byte("package main\n"), 0o600); err != nil {
		t.Fatalf("write inside fixture: %v", err)
	}
	manager := &editRootBoundaryManager{}
	handler := NewEditHandlerWithRoot(root, &structureTestRegistry{fileManager: manager})
	input, err := json.Marshal(EditRequest{Action: "code_action", FilePath: inside, Line: 1, Column: 1})
	if err != nil {
		t.Fatalf("marshal input: %v", err)
	}

	if _, err := handler(common.WithToolScope(context.Background(), common.ToolScope{CWD: root}), input); err != nil {
		t.Fatalf("code_action returned error: %v", err)
	}
	want := protocol.Range{Start: protocol.Position{Line: 0, Character: 0}, End: protocol.Position{Line: 0, Character: 0}}
	if manager.gotCodeActionRange != want {
		t.Fatalf("code_action range = %#v, want %#v", manager.gotCodeActionRange, want)
	}
}

func TestCodeActionRejectsPartialEndRange(t *testing.T) {
	root := t.TempDir()
	inside := filepath.Join(root, "inside.go")
	if err := os.WriteFile(inside, []byte("package main\n"), 0o600); err != nil {
		t.Fatalf("write inside fixture: %v", err)
	}
	handler := NewEditHandlerWithRoot(root, &structureTestRegistry{fileManager: &editRootBoundaryManager{}})
	input, err := json.Marshal(EditRequest{Action: "code_action", FilePath: inside, Line: 1, Column: 1, EndLine: 2})
	if err != nil {
		t.Fatalf("marshal input: %v", err)
	}

	_, err = handler(common.WithToolScope(context.Background(), common.ToolScope{CWD: root}), input)
	if err == nil || !strings.Contains(err.Error(), "end_line and end_column") {
		t.Fatalf("code_action error = %v, want partial end range rejection", err)
	}
}

func TestReplaceRangeEditsAllowEmptyNewStringDeletion(t *testing.T) {
	content := "alpha\nremove me\nomega\n"
	plan, err := buildEditsReplacePlan(content, []ReplaceEdit{{OldString: "remove me\n", NewString: ""}})
	if err != nil {
		t.Fatalf("build edits plan: %v", err)
	}
	if plan.updatedContent != "alpha\nomega\n" {
		t.Fatalf("updated content = %q, want deletion", plan.updatedContent)
	}
	if plan.replacement != "" {
		t.Fatalf("replacement = %q, want empty deletion replacement", plan.replacement)
	}
}

func TestReplaceRangeCoordinatesAllowEmptyNewTextDeletion(t *testing.T) {
	content := "alpha\nremove me\nomega\n"
	plan, err := buildReplacePlan(content, EditRequest{
		Line:      2,
		Column:    1,
		EndLine:   3,
		EndColumn: 1,
		NewText:   "",
	})
	if err != nil {
		t.Fatalf("build range plan: %v", err)
	}
	if plan.updatedContent != "alpha\nomega\n" {
		t.Fatalf("updated content = %q, want deletion", plan.updatedContent)
	}
}
