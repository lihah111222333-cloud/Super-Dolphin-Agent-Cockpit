package tools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/mcpserver/common"
)

func TestGrepRejectsLegacyLanguageParameter(t *testing.T) {
	handler := NewGrepHandler(Config{WorkspaceRoot: t.TempDir()})
	_, err := callLanguageParameterHandler(t, handler, t.TempDir(), map[string]any{
		"action": "ast_search", "query": "func $F()", "paths": []string{"."}, "language": "go",
	})
	assertCodedToolError(t, err, "invalid_params", false)
	if err == nil || !strings.Contains(err.Error(), `unknown field "language"`) {
		t.Fatalf("legacy grep language error = %v, want strict unknown-field rejection", err)
	}
}

func TestGrepRejectsInvalidExplicitASTLanguage(t *testing.T) {
	root := t.TempDir()
	handler := NewGrepHandler(Config{WorkspaceRoot: root})
	_, err := callLanguageParameterHandler(t, handler, root, map[string]any{
		"action": "ast_search", "query": "func $F()", "paths": []string{"."}, "glob": "*.go", "ast_language": "brainfuck",
	})
	assertCodedToolError(t, err, "invalid_params", false)
	if err == nil || !strings.Contains(err.Error(), `unsupported ast_language "brainfuck"`) {
		t.Fatalf("invalid ast_language error = %v, want unsupported explicit value", err)
	}
}

func TestGrepRejectsExplicitASTLanguageGlobConflict(t *testing.T) {
	root := t.TempDir()
	handler := NewGrepHandler(Config{WorkspaceRoot: root})
	_, err := callLanguageParameterHandler(t, handler, root, map[string]any{
		"action": "ast_search", "query": "func $F()", "paths": []string{"."}, "glob": "*.py", "ast_language": "go",
	})
	assertCodedToolError(t, err, "invalid_params", false)
	if err == nil || !strings.Contains(err.Error(), "ast_language") || !strings.Contains(err.Error(), "glob") {
		t.Fatalf("ast_language/glob conflict error = %v, want explicit conflict", err)
	}
}

func TestStructureAcceptsWorkspaceLanguageParameter(t *testing.T) {
	registry := &structureTestRegistry{languageManager: &structureTestManager{}}
	handler := NewStructureHandler(registry)
	_, err := callLanguageParameterHandler(t, handler, t.TempDir(), map[string]any{
		"action": "workspace_symbol", "query": "Needle", "workspace_language": "go",
	})
	if err != nil {
		t.Fatalf("workspace_language handler error = %v", err)
	}
	if registry.languageCalls != 1 || registry.gotLanguageID != "go" {
		t.Fatalf("workspace language routing = calls:%d language:%q, want 1/go", registry.languageCalls, registry.gotLanguageID)
	}
}

func TestStructureRejectsLegacyLanguageParameter(t *testing.T) {
	registry := &structureTestRegistry{languageManager: &structureTestManager{}}
	handler := NewStructureHandler(registry)
	_, err := callLanguageParameterHandler(t, handler, t.TempDir(), map[string]any{
		"action": "workspace_symbol", "query": "Needle", "language": "go",
	})
	assertCodedToolError(t, err, "invalid_params", false)
	if err == nil || !strings.Contains(err.Error(), `unknown field "language"`) {
		t.Fatalf("legacy structure language error = %v, want strict unknown-field rejection", err)
	}
}

func TestStructureRejectsWorkspaceLanguageOutsideExclusiveLocator(t *testing.T) {
	tests := []struct {
		name      string
		arguments map[string]any
		message   string
	}{
		{name: "with file path", arguments: map[string]any{"action": "workspace_symbol", "query": "Needle", "workspace_language": "go", "file_path": "a.go"}, message: "exactly one of file_path or workspace_language"},
		{name: "with language id", arguments: map[string]any{"action": "workspace_symbol", "query": "Needle", "workspace_language": "go", "language_id": "go"}, message: "language_id is only valid with file_path"},
		{name: "document symbol", arguments: map[string]any{"action": "document_symbol", "file_path": "a.go", "workspace_language": "go"}, message: "workspace_language is only valid for workspace_symbol"},
		{name: "language id without file", arguments: map[string]any{"action": "document_symbol", "language_id": "go"}, message: "language_id is only valid with file_path"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			registry := &structureTestRegistry{languageManager: &structureTestManager{}, fileManager: &structureTestManager{}}
			_, err := callLanguageParameterHandler(t, NewStructureHandler(registry), t.TempDir(), test.arguments)
			assertCodedToolError(t, err, "invalid_params", false)
			if err == nil || !strings.Contains(err.Error(), test.message) {
				t.Fatalf("structure locator error = %v, want %q", err, test.message)
			}
		})
	}
}

func callLanguageParameterHandler(t *testing.T, handler Handler, root string, arguments map[string]any) (any, error) {
	t.Helper()
	raw, err := json.Marshal(arguments)
	if err != nil {
		t.Fatalf("marshal language parameter arguments: %v", err)
	}
	ctx := common.WithToolScope(context.Background(), common.ToolScope{CWD: root, WorkspaceRoots: []string{root}})
	return handler(ctx, raw)
}
