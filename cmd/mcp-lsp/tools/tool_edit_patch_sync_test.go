package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	lspmanager "github.com/anthropic-ai/super-agent-v3/cmd/mcp-lsp/manager"
	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-lsp/protocol"
)

func TestEditRequiresAction(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "sample.go")
	original := "package main\n\nfunc f() { old() }\n"
	if err := os.WriteFile(path, []byte(original), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	handler := NewEditHandlerWithRoot(root, &structureTestRegistry{fileErr: lspmanager.ErrUnsupportedLanguage})

	input, err := json.Marshal(EditRequest{FilePath: path})
	if err != nil {
		t.Fatalf("marshal input: %v", err)
	}
	_, err = handler(testToolContext(root), input)
	if err == nil || !strings.Contains(err.Error(), "patch_edit requires action") {
		t.Fatalf("edit error = %v, want containing 'patch_edit requires action'", err)
	}
	assertFileContent(t, path, original)
}

func TestEditRejectsInvalidActionAndMissingFields(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "sample.go")
	if err := os.WriteFile(path, []byte("package main\n"), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	handler := NewEditHandlerWithRoot(root, &structureTestRegistry{fileErr: lspmanager.ErrUnsupportedLanguage})

	tests := []struct {
		name    string
		raw     string
		wantErr string
	}{
		{
			name:    "missing_action",
			raw:     `{"file_path":` + quoteJSON(t, path) + `,"patch":" x\n-old\n+new"}`,
			wantErr: "patch_edit requires action",
		},
		{
			name:    "invalid_action",
			raw:     `{"action":"delete","file_path":` + quoteJSON(t, path) + `}`,
			wantErr: "unsupported patch_edit action",
		},
		{
			name:    "replace_range_missing_patch",
			raw:     `{"action":"replace_range","file_path":` + quoteJSON(t, path) + `}`,
			wantErr: "replace_range requires patch",
		},
		{
			name:    "replace_range_missing_file_path",
			raw:     `{"action":"replace_range","patch":" x\n-old\n+new"}`,
			wantErr: "replace_range requires file_path",
		},
		{
			name:    "rename_missing_new_name",
			raw:     `{"action":"rename","pos":"main.go:1:1"}`,
			wantErr: "rename requires new_name",
		},
		{
			name:    "rename_missing_pos",
			raw:     `{"action":"rename","new_name":"foo"}`,
			wantErr: "rename requires pos",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			input := json.RawMessage(tt.raw)
			_, err := handler(testToolContext(root), input)
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("patch_edit %s error = %v, want containing %q", tt.name, err, tt.wantErr)
			}
		})
	}
}

func quoteJSON(t *testing.T, value string) string {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("quote JSON: %v", err)
	}
	return string(raw)
}

type editRereadSyncManager struct {
	structureTestManager
	bootstrapContent string
	rewriteContent   string
	didChangeText    string
}

func (m *editRereadSyncManager) BootstrapDocument(_ context.Context, uri string) error {
	raw, err := os.ReadFile(uri)
	if err != nil {
		return err
	}
	m.bootstrapContent = string(raw)
	if m.rewriteContent != "" {
		if err := os.WriteFile(uri, []byte(m.rewriteContent), 0o600); err != nil {
			return err
		}
	}
	return nil
}

func (m *editRereadSyncManager) DidChange(_ context.Context, _ string, _ int, changes []protocol.TextDocumentContentChangeEvent) error {
	if len(changes) != 1 {
		return fmt.Errorf("DidChange changes = %d, want 1", len(changes))
	}
	m.didChangeText = changes[0].Text
	return nil
}

func TestReplaceRangeSyncUsesDirectDidChangeWithoutBootstrap(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "sample.go")
	original := "package main\n\nfunc f() { old() }\n"
	if err := os.WriteFile(path, []byte(original), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	patched := "package main\n\nfunc f() { new() }\n"
	diskAfterBootstrap := "package main\n\nfunc f() { disk() }\n"
	manager := &editRereadSyncManager{rewriteContent: diskAfterBootstrap}
	handler := NewEditHandlerWithRoot(root, &structureTestRegistry{fileManager: manager})
	input, err := json.Marshal(EditRequest{
		Action:   "replace_range",
		FilePath: path,
		Patch: strings.Join([]string{
			"@@",
			"-func f() { old() }",
			"+func f() { new() }",
			"",
		}, "\n"),
	})
	if err != nil {
		t.Fatalf("marshal input: %v", err)
	}

	got, err := handler(testToolContext(root), input)
	if err != nil {
		t.Fatalf("edit returned error: %v", err)
	}
	requireSyncedReplaceRangeResult(t, got)
	if manager.bootstrapContent != "" {
		t.Fatalf("BootstrapDocument content = %q, want direct DidChange without bootstrap", manager.bootstrapContent)
	}
	if manager.didChangeText != patched {
		t.Fatalf("DidChange text = %q, want patched content %q", manager.didChangeText, patched)
	}
}

func TestReplaceRangeDoesNotWarnForLargeFullDocumentSync(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "sample.go")
	original := strings.Join(append([]string{
		"package main",
		"",
		"func f() {",
		"\told()",
		"}",
	}, repeatString("// filler", 210)...), "\n") + "\n"
	if err := os.WriteFile(path, []byte(original), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	manager := &editRereadSyncManager{}
	handler := NewEditHandlerWithRoot(root, &structureTestRegistry{fileManager: manager})
	input, err := json.Marshal(EditRequest{
		Action:   "replace_range",
		FilePath: path,
		Patch: strings.Join([]string{
			"@@",
			"-\told()",
			"+\tnew()",
			"",
		}, "\n"),
	})
	if err != nil {
		t.Fatalf("marshal input: %v", err)
	}

	got, err := handler(testToolContext(root), input)
	if err != nil {
		t.Fatalf("edit returned error: %v", err)
	}
	result := requireSyncedReplaceRangeResult(t, got)
	if result.Warning != "" {
		t.Fatalf("warning = %q, want empty for successful full-document sync", result.Warning)
	}
	if !strings.Contains(manager.didChangeText, "\tnew()\n") {
		t.Fatalf("DidChange text does not contain replacement: %q", manager.didChangeText)
	}
}

func requireSyncedReplaceRangeResult(t *testing.T, got any) replaceRangeResult {
	t.Helper()
	result, ok := got.(replaceRangeResult)
	if !ok {
		t.Fatalf("result type = %T, want replaceRangeResult", got)
	}
	if result.Status != "applied" || !result.Persisted || !result.LSPSync {
		t.Fatalf("unexpected result: %#v", result)
	}
	return result
}

func repeatString(value string, count int) []string {
	out := make([]string, count)
	for idx := range out {
		out[idx] = value
	}
	return out
}
