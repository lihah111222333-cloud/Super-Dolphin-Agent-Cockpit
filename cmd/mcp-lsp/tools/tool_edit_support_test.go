package tools

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	lspmanager "github.com/anthropic-ai/super-agent-v3/cmd/mcp-lsp/manager"
	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-lsp/multilsp"
	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-lsp/protocol"
	"github.com/anthropic-ai/super-agent-v3/internal/mcpserver/common"
)

func TestReadFileWithModeNormalizesCRLF(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sample.txt")
	raw := "first\r\nsecond\r\n"
	if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	file, err := readFileWithMode(path)
	if err != nil {
		t.Fatalf("read file: %v", err)
	}
	if file.content != "first\nsecond\n" {
		t.Fatalf("content mismatch: %q", file.content)
	}
	if file.raw != raw {
		t.Fatalf("raw mismatch: %q", file.raw)
	}
	if file.lineEnding != lineEndingCRLF {
		t.Fatalf("line ending mismatch: %q", file.lineEnding)
	}
	if restored := file.diskContent(file.content); restored != raw {
		t.Fatalf("restored mismatch: %q", restored)
	}
}

func TestReadFileUsesMetaCWDForExternalAbsolutePath(t *testing.T) {
	mainRoot := t.TempDir()
	externalRoot := t.TempDir()
	externalFile := filepath.Join(externalRoot, "go.mod")
	if err := os.WriteFile(externalFile, []byte("module external.test\n"), 0o600); err != nil {
		t.Fatalf("write external fixture: %v", err)
	}
	handler := NewFileHandler(Config{WorkspaceRoot: mainRoot})
	ctx := context.WithValue(context.Background(), common.CwdContextKey, externalRoot)
	req, err := json.Marshal(fileToolInput{
		Action:   "read_file",
		FilePath: externalFile,
	})
	if err != nil {
		t.Fatalf("marshal read_file input: %v", err)
	}

	got, err := handler(ctx, req)
	if err != nil {
		t.Fatalf("read_file returned error: %v", err)
	}
	text, ok := got.(string)
	if !ok {
		t.Fatalf("read_file result type = %T, want string", got)
	}
	if !strings.Contains(text, "module external.test") {
		t.Fatalf("read_file result = %q, want external file content", text)
	}
}

func TestBuildRangeReplacePlanRestoresCRLF(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sample.txt")
	raw := "a\r\nb\r\nc\r\n"
	if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	file, err := readFileWithMode(path)
	if err != nil {
		t.Fatalf("read file: %v", err)
	}
	plan, err := buildRangeReplacePlan(file.content, EditRequest{
		Line:      2,
		Column:    1,
		EndLine:   2,
		EndColumn: 2,
		NewText:   "x\r\ny",
	})
	if err != nil {
		t.Fatalf("build range plan: %v", err)
	}
	if plan.updatedContent != "a\nx\ny\nc\n" {
		t.Fatalf("updated content mismatch: %q", plan.updatedContent)
	}
	if restored := file.diskContent(plan.updatedContent); restored != "a\r\nx\r\ny\r\nc\r\n" {
		t.Fatalf("restored mismatch: %q", restored)
	}
}

func TestApplyTextEditsNormalizesInsertedCRLF(t *testing.T) {
	content := "a\nb\n"
	updated, err := applyTextEdits(content, []protocol.TextEdit{{
		Range: protocol.Range{
			Start: protocol.Position{Line: 0, Character: 0},
			End:   protocol.Position{Line: 0, Character: 1},
		},
		NewText: "x\r\ny",
	}})
	if err != nil {
		t.Fatalf("apply text edits: %v", err)
	}
	if updated != "x\ny\nb\n" {
		t.Fatalf("updated mismatch: %q", updated)
	}
}

func TestReplaceRangeAppliesUnsupportedTextFilesWithoutLSPManager(t *testing.T) {
	tests := []struct {
		name    string
		file    string
		content string
		old     string
		new     string
		want    string
	}{
		{
			name:    "markdown",
			file:    "plan.md",
			content: "# Title\n\nold markdown line\n",
			old:     "old markdown line",
			new:     "new markdown line",
			want:    "# Title\n\nnew markdown line\n",
		},
		{
			name:    "plaintext",
			file:    "notes.txt",
			content: "first\nold text\nlast\n",
			old:     "old text",
			new:     "new text",
			want:    "first\nnew text\nlast\n",
		},
		{
			name:    "json",
			file:    "config.json",
			content: "{\n  \"mode\": \"old\"\n}\n",
			old:     "  \"mode\": \"old\"",
			new:     "  \"mode\": \"new\"",
			want:    "{\n  \"mode\": \"new\"\n}\n",
		},
		{
			name:    "yaml",
			file:    "config.yaml",
			content: "server:\n  port: 8080\nfeatures:\n  dag: false\n",
			old:     "  dag: false",
			new:     "  dag: true",
			want:    "server:\n  port: 8080\nfeatures:\n  dag: true\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assertUnsupportedTextReplace(t, tt.file, tt.content, tt.old, tt.new, tt.want)
		})
	}
}

func assertUnsupportedTextReplace(t *testing.T, file, content, oldText, newText, want string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), file)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	handler := NewEditHandler(&structureTestRegistry{fileErr: lspmanager.ErrUnsupportedLanguage})
	input, err := json.Marshal(EditRequest{
		Action:   "replace_range",
		FilePath: path,
		Edits: []ReplaceEdit{{
			OldString: oldText,
			NewString: newText,
		}},
	})
	if err != nil {
		t.Fatalf("marshal input: %v", err)
	}

	got, err := handler(common.WithToolScope(context.Background(), common.ToolScope{CWD: "/"}), input)
	if err != nil {
		t.Fatalf("replace_range returned error: %v", err)
	}
	result := requireReplaceRangeResult(t, got)
	if !strings.Contains(result.Warning, "LSP sync skipped") {
		t.Fatalf("warning = %q, want LSP sync skipped", result.Warning)
	}
	assertFileContent(t, path, want)
}

func requireReplaceRangeResult(t *testing.T, got any) replaceRangeResult {
	t.Helper()
	result, ok := got.(replaceRangeResult)
	if !ok {
		t.Fatalf("result type = %T, want replaceRangeResult", got)
	}
	if !result.Success || !result.Applied || result.Status != "applied" {
		t.Fatalf("unexpected result: %#v", result)
	}
	if result.LSPSync {
		t.Fatalf("LSPSync = true, want false without LSP manager")
	}
	return result
}

func TestRenameRejectsPathOutsideWorkspaceRootBeforeLSPRequest(t *testing.T) {
	root := t.TempDir()
	outsideDir := t.TempDir()
	outside := filepath.Join(outsideDir, "outside.go")
	if err := os.WriteFile(outside, []byte("package main\n\nvar oldName = 1\n"), 0o600); err != nil {
		t.Fatalf("write outside fixture: %v", err)
	}
	registry := &structureTestRegistry{fileManager: &structureTestManager{}}
	handler := NewEditHandlerWithRoot(root, registry)
	input, err := json.Marshal(EditRequest{
		Action:   "rename",
		FilePath: outside,
		Line:     3,
		Column:   5,
		NewName:  "newName",
	})
	if err != nil {
		t.Fatalf("marshal input: %v", err)
	}

	_, err = handler(common.WithToolScope(context.Background(), common.ToolScope{CWD: root}), input)
	if err == nil || !strings.Contains(err.Error(), "outside workspace root") {
		t.Fatalf("rename error = %v, want outside workspace root", err)
	}
	if registry.gotFilePath != "" {
		t.Fatalf("GetManagerForFile called with %q; want no LSP request for outside root", registry.gotFilePath)
	}
}

func TestReplaceRangeRejectsPathOutsideWorkspaceRoot(t *testing.T) {
	root := t.TempDir()
	outsideDir := t.TempDir()
	outside := filepath.Join(outsideDir, "outside.txt")
	if err := os.WriteFile(outside, []byte("old\n"), 0o600); err != nil {
		t.Fatalf("write outside fixture: %v", err)
	}
	handler := NewEditHandlerWithRoot(root, &structureTestRegistry{fileErr: lspmanager.ErrUnsupportedLanguage})
	input, err := json.Marshal(EditRequest{
		Action:   "replace_range",
		FilePath: outside,
		Edits:    []ReplaceEdit{{OldString: "old", NewString: "new"}},
	})
	if err != nil {
		t.Fatalf("marshal input: %v", err)
	}

	_, err = handler(common.WithToolScope(context.Background(), common.ToolScope{CWD: root}), input)
	if err == nil || !strings.Contains(err.Error(), "outside workspace root") {
		t.Fatalf("replace_range error = %v, want outside workspace root", err)
	}
	raw, readErr := os.ReadFile(outside)
	if readErr != nil {
		t.Fatalf("read outside fixture: %v", readErr)
	}
	if string(raw) != "old\n" {
		t.Fatalf("outside file was modified: %q", raw)
	}
}

func TestReplaceRangeRejectsSymlinkEscapingWorkspaceRoot(t *testing.T) {
	root := t.TempDir()
	outsideDir := t.TempDir()
	outside := filepath.Join(outsideDir, "target.txt")
	if err := os.WriteFile(outside, []byte("old\n"), 0o600); err != nil {
		t.Fatalf("write outside fixture: %v", err)
	}
	link := filepath.Join(root, "link.txt")
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	handler := NewEditHandlerWithRoot(root, &structureTestRegistry{fileErr: lspmanager.ErrUnsupportedLanguage})
	input, err := json.Marshal(EditRequest{
		Action:   "replace_range",
		FilePath: link,
		Edits:    []ReplaceEdit{{OldString: "old", NewString: "new"}},
	})
	if err != nil {
		t.Fatalf("marshal input: %v", err)
	}

	_, err = handler(common.WithToolScope(context.Background(), common.ToolScope{CWD: root}), input)
	if err == nil || !strings.Contains(err.Error(), "outside workspace root") {
		t.Fatalf("replace_range error = %v, want outside workspace root", err)
	}
	raw, readErr := os.ReadFile(outside)
	if readErr != nil {
		t.Fatalf("read outside fixture: %v", readErr)
	}
	if string(raw) != "old\n" {
		t.Fatalf("outside symlink target was modified: %q", raw)
	}
}

func TestEditForceDoesNotBypassTrustedScopeOrPathSafety(t *testing.T) {
	root := t.TempDir()
	handler := NewEditHandlerWithRoot(root, &structureTestRegistry{fileErr: lspmanager.ErrUnsupportedLanguage})

	assertForceCannotEscapeWorkspaceRoot(t, handler)
	assertForceInvalidPatchLeavesFile(t, root, handler)
}

func assertForceCannotEscapeWorkspaceRoot(t *testing.T, handler ToolHandler) {
	t.Helper()
	outside := filepath.Join(t.TempDir(), "outside.txt")
	if err := os.WriteFile(outside, []byte("old\n"), 0o600); err != nil {
		t.Fatalf("write outside fixture: %v", err)
	}
	input := json.RawMessage(`{"action":"replace_range","file_path":` + strconv.Quote(outside) + `,"edits":[{"old_string":"old","new_string":"new"}],"force":true}`)

	_, err := handler(common.WithToolScope(context.Background(), common.ToolScope{CWD: t.TempDir()}), input)
	if err == nil || !strings.Contains(err.Error(), "outside workspace root") {
		t.Fatalf("force outside-root error = %v, want workspace root rejection", err)
	}
	assertFileContent(t, outside, "old\n")
}

func assertForceInvalidPatchLeavesFile(t *testing.T, root string, handler ToolHandler) {
	t.Helper()
	inside := filepath.Join(root, "inside.txt")
	if err := os.WriteFile(inside, []byte("old\n"), 0o600); err != nil {
		t.Fatalf("write inside fixture: %v", err)
	}
	badPatch := json.RawMessage(`{"action":"replace_range","file_path":` + strconv.Quote(inside) + `,"patch":"not a patch","force":true}`)
	got, err := handler(common.WithToolScope(context.Background(), common.ToolScope{CWD: "/"}), badPatch)
	if err != nil {
		t.Fatalf("force invalid patch returned transport error: %v", err)
	}
	failure, ok := got.(replaceRangeFailure)
	if !ok {
		t.Fatalf("force invalid patch result type = %T, want replaceRangeFailure", got)
	}
	if failure.Success || !strings.Contains(strings.ToLower(failure.Error), "patch") {
		t.Fatalf("force invalid patch result = %#v, want patch grammar failure", failure)
	}
	assertFileContent(t, inside, "old\n")
}

func TestWorkspaceEditRejectsPathOutsideWorkspaceRoot(t *testing.T) {
	root := t.TempDir()
	outsideDir := t.TempDir()
	outside := filepath.Join(outsideDir, "outside.txt")
	if err := os.WriteFile(outside, []byte("old\n"), 0o600); err != nil {
		t.Fatalf("write outside fixture: %v", err)
	}
	edit := &protocol.WorkspaceEdit{Changes: map[string][]protocol.TextEdit{
		fileURI(outside): {{
			Range: protocol.Range{
				Start: protocol.Position{Line: 0, Character: 0},
				End:   protocol.Position{Line: 0, Character: 3},
			},
			NewText: "new",
		}},
	}}

	_, _, err := loadWorkspaceEditUpdates(root, edit)
	if err == nil || !strings.Contains(err.Error(), "outside workspace root") {
		t.Fatalf("load workspace edit error = %v, want outside workspace root", err)
	}
}

func TestWorkspaceEditRejectsSymlinkEscapingWorkspaceRoot(t *testing.T) {
	root := t.TempDir()
	outsideDir := t.TempDir()
	outside := filepath.Join(outsideDir, "target.txt")
	if err := os.WriteFile(outside, []byte("old\n"), 0o600); err != nil {
		t.Fatalf("write outside fixture: %v", err)
	}
	link := filepath.Join(root, "link.txt")
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	edit := &protocol.WorkspaceEdit{Changes: map[string][]protocol.TextEdit{
		fileURI(link): {{
			Range: protocol.Range{
				Start: protocol.Position{Line: 0, Character: 0},
				End:   protocol.Position{Line: 0, Character: 3},
			},
			NewText: "new",
		}},
	}}

	_, _, err := loadWorkspaceEditUpdates(root, edit)
	if err == nil || !strings.Contains(err.Error(), "outside workspace root") {
		t.Fatalf("load workspace edit error = %v, want outside workspace root", err)
	}
	raw, readErr := os.ReadFile(outside)
	if readErr != nil {
		t.Fatalf("read outside fixture: %v", readErr)
	}
	if string(raw) != "old\n" {
		t.Fatalf("outside symlink target was modified: %q", raw)
	}
}

type editFailureManager struct {
	structureTestManager
	didChangeErr   error
	didChangeHook  func(uri string)
	didChangeCalls int
}

func (m *editFailureManager) DidChange(_ context.Context, uri string, _ int, _ []protocol.TextDocumentContentChangeEvent) error {
	m.didChangeCalls++
	if m.didChangeHook != nil {
		m.didChangeHook(uri)
	}
	return m.didChangeErr
}

func TestEditFailureAfterDeadClientReturnsRetryableWithoutAutoReplay(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "sample.go")
	original := "package main\n\nfunc f() { old() }\n"
	if err := os.WriteFile(path, []byte(original), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	manager := &editFailureManager{
		didChangeErr: errors.New("LSP client closed after transport failure"),
	}
	handler := NewEditHandlerWithRoot(root, &structureTestRegistry{fileManager: manager})
	input, err := json.Marshal(EditRequest{
		Action:    "replace_range",
		FilePath:  path,
		Line:      3,
		Column:    12,
		EndLine:   3,
		EndColumn: 17,
		NewText:   "new()",
	})
	if err != nil {
		t.Fatalf("marshal input: %v", err)
	}

	got, err := handler(common.WithToolScope(context.Background(), common.ToolScope{CWD: "/"}), input)
	if err != nil {
		t.Fatalf("replace_range returned transport error: %v", err)
	}
	result, ok := got.(replaceRangeFailure)
	if !ok {
		t.Fatalf("result type = %T, want replaceRangeFailure", got)
	}
	if result.Success {
		t.Fatalf("Success = true, want false for dead-client edit sync")
	}
	assertDeadClientRollbackState(t, path, original, manager)
	envelope := newToolErrorEnvelope("edit", "go", errors.Join(multilsp.ErrTransportClosed, manager.didChangeErr))
	if !envelope.Retryable || envelope.Code != "lsp_client_closed" {
		t.Fatalf("dead-client envelope = %#v, want retryable lsp_client_closed", envelope)
	}
}

func assertDeadClientRollbackState(t *testing.T, path, original string, manager *editFailureManager) {
	t.Helper()
	if manager.didChangeCalls != 2 {
		t.Fatalf("DidChange calls = %d, want original sync plus rollback sync without auto-replaying edit", manager.didChangeCalls)
	}
	assertFileContent(t, path, original)
}

func assertFileContent(t *testing.T, path, want string) {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read file %s: %v", path, err)
	}
	if string(raw) != want {
		t.Fatalf("file content mismatch for %s:\nwant %q\ngot  %q", path, want, string(raw))
	}
}

func TestReplaceRangeSyncFailureReportsRollbackFailure(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "sample.go")
	if err := os.WriteFile(path, []byte("package main\n\nfunc f() { old() }\n"), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	manager := &editFailureManager{
		didChangeErr: errors.New("lsp sync boom"),
		didChangeHook: func(uri string) {
			if err := os.Remove(uri); err != nil {
				t.Fatalf("remove updated file before rollback: %v", err)
			}
			if err := os.Mkdir(uri, 0o700); err != nil {
				t.Fatalf("replace file with directory before rollback: %v", err)
			}
		},
	}
	handler := NewEditHandlerWithRoot(root, &structureTestRegistry{fileManager: manager})
	input, err := json.Marshal(EditRequest{
		Action:    "replace_range",
		FilePath:  path,
		Line:      3,
		Column:    12,
		EndLine:   3,
		EndColumn: 17,
		NewText:   "new()",
	})
	if err != nil {
		t.Fatalf("marshal input: %v", err)
	}

	got, err := handler(common.WithToolScope(context.Background(), common.ToolScope{CWD: "/"}), input)
	if err != nil {
		t.Fatalf("replace_range returned transport error: %v", err)
	}
	result, ok := got.(replaceRangeFailure)
	if !ok {
		t.Fatalf("result type = %T, want replaceRangeFailure", got)
	}
	if result.Success {
		t.Fatalf("Success = true, want false")
	}
	if !strings.Contains(result.Error, "lsp sync boom") || !strings.Contains(result.Error, "rollback failed") {
		t.Fatalf("error = %q, want sync and rollback failure details", result.Error)
	}
}

func TestApplyWorkspaceEditSyncFailureReportsRollbackFailure(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "sample.go")
	if err := os.WriteFile(path, []byte("old\n"), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	manager := &editFailureManager{
		didChangeErr: errors.New("lsp sync boom"),
		didChangeHook: func(uri string) {
			if err := os.Remove(uri); err != nil {
				t.Fatalf("remove updated file before rollback: %v", err)
			}
			if err := os.Mkdir(uri, 0o700); err != nil {
				t.Fatalf("replace file with directory before rollback: %v", err)
			}
		},
	}
	edit := &protocol.WorkspaceEdit{Changes: map[string][]protocol.TextEdit{
		fileURI(path): {{
			Range: protocol.Range{
				Start: protocol.Position{Line: 0, Character: 0},
				End:   protocol.Position{Line: 0, Character: 3},
			},
			NewText: "new",
		}},
	}}

	_, err := (EditHandler{root: root}).applyWorkspaceEdit(common.WithToolScope(context.Background(), common.ToolScope{CWD: "/"}), manager, edit, defaultEditVersion)
	if err == nil {
		t.Fatalf("applyWorkspaceEdit error = nil, want sync/rollback failure")
	}
	if !strings.Contains(err.Error(), "lsp sync boom") || !strings.Contains(err.Error(), "rollback failed") {
		t.Fatalf("error = %q, want sync and rollback failure details", err.Error())
	}
}
