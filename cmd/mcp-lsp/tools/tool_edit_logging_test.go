package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-lsp/protocol"
	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
)

func TestEditFormatLogsResultOnlyBehavior(t *testing.T) {
	logs := captureToolEditLogs(t)
	root := t.TempDir()
	path := filepath.Join(root, "main.go")
	if err := os.WriteFile(path, []byte("package main\n"), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	manager := &editRootBoundaryManager{formatEdits: []protocol.TextEdit{{
		Range:   protocol.Range{Start: protocol.Position{Line: 0, Character: 0}, End: protocol.Position{Line: 0, Character: 0}},
		NewText: "// formatted\n",
	}}}
	handler := NewEditHandlerWithRoot(root, &structureTestRegistry{fileManager: manager})
	input, err := json.Marshal(EditRequest{Action: "format", FilePath: path})
	if err != nil {
		t.Fatalf("marshal input: %v", err)
	}

	if _, err := handler(context.Background(), input); err != nil {
		t.Fatalf("format returned error: %v", err)
	}

	output := logs.String()
	for _, want := range []string{
		"mcp-lsp: edit format result",
		"action=format",
		"text_edit_count=1",
		"applied=false",
		"persisted=false",
		"auto_apply_supported=false",
		"result_only=true",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("format log missing %q in:\n%s", want, output)
		}
	}
}

func TestEditCodeActionLogsResultOnlyBehavior(t *testing.T) {
	logs := captureToolEditLogs(t)
	root := t.TempDir()
	path := filepath.Join(root, "main.go")
	if err := os.WriteFile(path, []byte("package main\n"), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	manager := &editRootBoundaryManager{codeActions: []protocol.CodeActionResult{{CodeAction: &protocol.CodeAction{
		Title: "add comment",
		Edit: &protocol.WorkspaceEdit{Changes: map[string][]protocol.TextEdit{
			fileURI(path): {{
				Range:   protocol.Range{Start: protocol.Position{Line: 0, Character: 0}, End: protocol.Position{Line: 0, Character: 0}},
				NewText: "// fixed\n",
			}},
		}},
	}}}}
	handler := NewEditHandlerWithRoot(root, &structureTestRegistry{fileManager: manager})
	input, err := json.Marshal(EditRequest{Action: "code_action", FilePath: path, Line: 1, Column: 1, Only: []string{"quickfix"}})
	if err != nil {
		t.Fatalf("marshal input: %v", err)
	}

	if _, err := handler(context.Background(), input); err != nil {
		t.Fatalf("code_action returned error: %v", err)
	}

	output := logs.String()
	for _, want := range []string{
		"mcp-lsp: edit code_action result",
		"action=code_action",
		"code_action_count=1",
		"actions_with_workspace_edit=1",
		"workspace_text_edit_count=1",
		"applied=false",
		"persisted=false",
		"auto_apply_supported=false",
		"result_only=true",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("code_action log missing %q in:\n%s", want, output)
		}
	}
}

func captureToolEditLogs(t *testing.T) *bytes.Buffer {
	t.Helper()
	previous := pkglogger.Get()
	var logs bytes.Buffer
	pkglogger.SetForTest(pkglogger.New(pkglogger.NewTextHandler(&logs, &pkglogger.HandlerOptions{
		Level: pkglogger.LevelInfo,
	})))
	t.Cleanup(func() {
		pkglogger.SetForTest(previous)
	})
	return &logs
}
