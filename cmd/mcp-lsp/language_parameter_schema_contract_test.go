package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLSPToolLanguageParameterSchemaNames(t *testing.T) {
	tests := []struct {
		name    string
		schema  schema
		want    string
		removed string
	}{
		{name: "grep", schema: newLSPGrepSchema(), want: "ast_language", removed: "language"},
		{name: "structure", schema: newLSPStructureSchema(), want: "workspace_language", removed: "language"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			properties, ok := test.schema["properties"].(map[string]any)
			if !ok {
				t.Fatalf("schema properties type = %T", test.schema["properties"])
			}
			if _, ok := properties[test.want]; !ok {
				t.Errorf("schema missing %q", test.want)
			}
			if _, ok := properties[test.removed]; ok {
				t.Errorf("schema retains removed field %q", test.removed)
			}
		})
	}
}

func TestLSPToolLanguageParameterActionConditions(t *testing.T) {
	tests := []struct {
		name      string
		schema    schema
		arguments map[string]any
		valid     bool
	}{
		{name: "ast language on ast search", schema: newLSPGrepSchema(), arguments: map[string]any{"action": "ast_search", "query": "func $F()", "paths": []any{"internal"}, "ast_language": "go"}, valid: true},
		{name: "ast language forbidden on text search", schema: newLSPGrepSchema(), arguments: map[string]any{"action": "text_search", "query": "needle", "paths": []any{"internal"}, "ast_language": "go"}},
		{name: "legacy grep language", schema: newLSPGrepSchema(), arguments: map[string]any{"action": "ast_search", "query": "func $F()", "paths": []any{"internal"}, "language": "go"}},
		{name: "workspace language locator", schema: newLSPStructureSchema(), arguments: map[string]any{"action": "workspace_symbol", "query": "Handler", "workspace_language": "go"}, valid: true},
		{name: "file locator with language id override", schema: newLSPStructureSchema(), arguments: map[string]any{"action": "workspace_symbol", "query": "Handler", "file_path": "a.go", "language_id": "go"}, valid: true},
		{name: "workspace language conflicts with file path", schema: newLSPStructureSchema(), arguments: map[string]any{"action": "workspace_symbol", "query": "Handler", "file_path": "a.go", "workspace_language": "go"}},
		{name: "workspace language conflicts with language id", schema: newLSPStructureSchema(), arguments: map[string]any{"action": "workspace_symbol", "query": "Handler", "workspace_language": "go", "language_id": "go"}},
		{name: "workspace language forbidden on document symbol", schema: newLSPStructureSchema(), arguments: map[string]any{"action": "document_symbol", "file_path": "a.go", "workspace_language": "go"}},
		{name: "workspace language forbidden on folding range", schema: newLSPStructureSchema(), arguments: map[string]any{"action": "folding_range", "file_path": "a.go", "workspace_language": "go"}},
		{name: "workspace language forbidden on semantic tokens", schema: newLSPStructureSchema(), arguments: map[string]any{"action": "semantic_tokens", "file_path": "a.go", "workspace_language": "go"}},
		{name: "legacy structure language", schema: newLSPStructureSchema(), arguments: map[string]any{"action": "workspace_symbol", "query": "Handler", "language": "go"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := validateToolArguments(t, test.schema, test.arguments)
			if (err == nil) != test.valid {
				t.Fatalf("schema validation error = %v, valid=%t, arguments=%#v", err, test.valid, test.arguments)
			}
		})
	}
}

func TestLSPToolLanguageParameterSourceGuard(t *testing.T) {
	targets := map[string][]string{
		"schema.go": {
			`"language":`, `forbidFields("language")`, `[]string{"language"}`,
		},
		"tools.go": {
			`"language":"go"`,
		},
		filepath.Join("tools", "tool_grep.go"): {
			`json:"language`,
		},
		filepath.Join("tools", "tool_grep_log.go"): {
			`"language", input.Language`,
		},
		filepath.Join("tools", "tool_structure.go"): {
			`json:"language"`, "file_path or language", "use language for workspace-wide search", "choose-file_path-or-language", "query/language/file_path",
		},
		filepath.Join("..", "..", "AGENTS.md"): {
			`ast_search, query="func ($R) MethodName(", language=`,
		},
		filepath.Join("..", "..", "docs", "internal-notes", "LSP系统提示词.md"): {
			"`language` 与 `file_path` 二选一",
		},
		filepath.Join("..", "..", "docs", "internal-notes", "lsp提示词英文版.md"): {
			"`language` 与 `file_path` 二选一",
		},
		filepath.Join("..", "..", "docs", "doc", "codemap", "03-mcp-lsp-ida.md"): {
			"直接走 `language` 的 tool action", "与 `language` 二选一",
		},
	}
	for path, forbidden := range targets {
		source, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		for _, fragment := range forbidden {
			if strings.Contains(string(source), fragment) {
				t.Errorf("%s retains removed language parameter fragment %q", path, fragment)
			}
		}
	}
	assertNoLegacyLanguageSelectors(t, filepath.Join("tools", "tool_grep.go"), "input")
	assertNoLegacyLanguageSelectors(t, filepath.Join("tools", "tool_structure.go"), "req")
	for path, typeName := range map[string]string{
		filepath.Join("tools", "tool_grep.go"):      "grepToolInput",
		filepath.Join("tools", "tool_structure.go"): "structureParams",
	} {
		for _, field := range namedStructJSONFields(t, path, typeName) {
			if field == "language" {
				t.Errorf("%s.%s retains removed language JSON field", path, typeName)
			}
		}
	}
}

func TestLegacyLanguageSelectorGuardDetectsFixture(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy_selector.go")
	fixture := []byte("package fixture\nfunc use(req struct{ Language string }) { _ = req.Language }\n")
	if err := os.WriteFile(path, fixture, 0o600); err != nil {
		t.Fatalf("write legacy selector fixture: %v", err)
	}
	selectors, err := legacyLanguageSelectors(path, "req")
	if err != nil {
		t.Fatalf("scan legacy selector fixture: %v", err)
	}
	if len(selectors) != 1 || selectors[0] != "req.Language" {
		t.Fatalf("legacy selector fixture matches = %v, want [req.Language]", selectors)
	}
}

func TestWorkspaceSymbolE2EUsesLineProtocolSourceGuard(t *testing.T) {
	path := "lsp_binary_navigation_text_e2e_test.go"
	source, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	text := string(source)
	for _, forbidden := range []string{
		`json.Unmarshal(raw, &payload)`,
	} {
		if strings.Contains(text, forbidden) {
			t.Errorf("%s retains workspace symbol JSON parser fragment %q", path, forbidden)
		}
	}
	for _, required := range []string{
		`decodeWorkspaceSymbolRows(t, result.Result.ContentText())`,
		`lineprotocol.Parse(text)`,
	} {
		if !strings.Contains(text, required) {
			t.Errorf("%s missing workspace symbol line protocol parser fragment %q", path, required)
		}
	}
	assertContentOnlyWorkspaceResultSelectors(t, path)
}

func TestWorkspaceSymbolE2EContentOnlySelectorGuardDetectsFixture(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy_result_selector.go")
	fixture := []byte("package fixture\nfunc use(result struct{ Result struct{ Auxiliary string } }) { _ = result.Result.Auxiliary }\n")
	if err := os.WriteFile(path, fixture, 0o600); err != nil {
		t.Fatalf("write legacy result selector fixture: %v", err)
	}
	violations, err := contentOnlyWorkspaceResultSelectorViolations(path)
	if err != nil {
		t.Fatalf("scan legacy result selector fixture: %v", err)
	}
	if len(violations) != 1 || violations[0] != "Auxiliary" {
		t.Fatalf("legacy result selector fixture violations = %v, want [Auxiliary]", violations)
	}
}

func assertContentOnlyWorkspaceResultSelectors(t *testing.T, path string) {
	t.Helper()
	violations, err := contentOnlyWorkspaceResultSelectorViolations(path)
	if err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	for _, field := range violations {
		t.Errorf("%s reads result.Result.%s instead of the content-only contract", path, field)
	}
}

func contentOnlyWorkspaceResultSelectorViolations(path string) ([]string, error) {
	source, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
	if err != nil {
		return nil, err
	}
	violations := make([]string, 0)
	ast.Inspect(source, func(node ast.Node) bool {
		selector, ok := node.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		result, ok := selector.X.(*ast.SelectorExpr)
		if !ok || result.Sel.Name != "Result" {
			return true
		}
		resultValue, ok := result.X.(*ast.Ident)
		if !ok || resultValue.Name != "result" {
			return true
		}
		switch selector.Sel.Name {
		case "ContentText", "IsError":
			return true
		default:
			violations = append(violations, selector.Sel.Name)
			return true
		}
	})
	return violations, nil
}

func assertNoLegacyLanguageSelectors(t *testing.T, path, receiver string) {
	t.Helper()
	selectors, err := legacyLanguageSelectors(path, receiver)
	if err != nil {
		t.Fatalf("scan %s selectors: %v", path, err)
	}
	if len(selectors) != 0 {
		t.Errorf("%s retains removed selectors %v", path, selectors)
	}
}

func legacyLanguageSelectors(path, receiver string) ([]string, error) {
	parsed, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
	if err != nil {
		return nil, err
	}
	selectors := make([]string, 0)
	ast.Inspect(parsed, func(node ast.Node) bool {
		selector, ok := node.(*ast.SelectorExpr)
		if !ok || selector.Sel.Name != "Language" {
			return true
		}
		identifier, ok := selector.X.(*ast.Ident)
		if ok && identifier.Name == receiver {
			selectors = append(selectors, receiver+".Language")
		}
		return true
	})
	return selectors, nil
}
