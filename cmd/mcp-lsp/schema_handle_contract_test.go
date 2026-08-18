package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/mcpserver/common"
)

func TestHandleToolCallRejectsManifestSchemaViolationsBeforeHandler(t *testing.T) {
	tests := []struct {
		name string
		tool string
		args string
	}{
		{
			name: "grep ast_search rejects regex",
			tool: "grep",
			args: `{"action":"ast_search","query":"func $F()","paths":["cmd"],"regex":true}`,
		},
		{
			name: "grep text_search rejects ast_language",
			tool: "grep",
			args: `{"action":"text_search","query":"needle","paths":["cmd"],"ast_language":"go"}`,
		},
		{
			name: "xref references rejects direction",
			tool: "xref",
			args: `{"action":"references","pos":"cmd/mcp-lsp/tools.go:20:1","direction":"both"}`,
		},
		{
			name: "xref call_hierarchy rejects include_declaration",
			tool: "xref",
			args: `{"action":"call_hierarchy","pos":"cmd/mcp-lsp/tools.go:20:1","include_declaration":true}`,
		},
		{
			name: "inspect definition rejects file_path",
			tool: "inspect",
			args: `{"action":"definition","pos":"cmd/mcp-lsp/tools.go:20:1","file_path":"cmd/mcp-lsp/tools.go"}`,
		},
		{
			name: "structure workspace_symbol rejects language_id",
			tool: "structure",
			args: `{"action":"workspace_symbol","query":"ToolManifest","workspace_language":"go","language_id":"go"}`,
		},
		{
			name: "file read_file rejects conflicting locators",
			tool: "file",
			args: `{"action":"read_file","file_path":"cmd/mcp-lsp/tools.go","file_paths":["cmd/mcp-lsp/fx.go"]}`,
		},
		{
			name: "patch_edit replace_range rejects pos",
			tool: "patch_edit",
			args: `{"action":"replace_range","file_path":"cmd/mcp-lsp/tools.go","patch":"@@","pos":"cmd/mcp-lsp/tools.go:20:1"}`,
		},
		{
			name: "unknown field is rejected",
			tool: "completion",
			args: `{"pos":"cmd/mcp-lsp/tools.go:20:1","typo":"must fail"}`,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			called := false
			defs := toolDefinitions(ToolHandlers{
				test.tool: func(context.Context, json.RawMessage) (any, error) {
					called = true
					return map[string]any{"unexpected": true}, nil
				},
			})

			_, err := handleToolCall(context.Background(), defs, test.tool, json.RawMessage(test.args))
			var coded *common.CodedToolError
			if !errors.As(err, &coded) || coded.Code != "invalid_params" || coded.Retryable {
				t.Fatalf("handleToolCall(%s) error = %T %v, want non-retryable invalid_params", test.tool, err, err)
			}
			if called {
				t.Fatal("handler ran before manifest schema validation")
			}
		})
	}
}

func TestToolsCallRejectsManifestSchemaViolationAsContentOnlyError(t *testing.T) {
	root := canonicalToolTestRoot(t, t.TempDir())
	called := false
	defs := toolDefinitions(ToolHandlers{
		"grep": func(context.Context, json.RawMessage) (any, error) {
			called = true
			return map[string]any{"unexpected": true}, nil
		},
	})
	request := mustDirectToolCallRequest(t, root, "grep", map[string]any{
		"action": "ast_search",
		"query":  "func $F()",
		"paths":  []string{"cmd"},
		"regex":  true,
	})
	response := runDirectToolCallForPlainText(t, request, defs)
	if !response.Result.IsError {
		t.Fatal("tools/call isError = false, want true")
	}
	if len(bytes.TrimSpace(response.Result.StructuredContent)) != 0 {
		t.Fatalf("mcp-lsp tools/call returned structuredContent: %s", response.Result.StructuredContent)
	}
	if len(response.Result.Content) != 1 {
		t.Fatalf("content items = %d, want 1", len(response.Result.Content))
	}
	content := response.Result.Content[0].Text
	for _, want := range []string{"ERROR code=invalid_params retryable=0", "HINT\t", "ATTR\ttool=grep"} {
		if !strings.Contains(content, want) {
			t.Fatalf("content = %q, missing %q", content, want)
		}
	}
	if called {
		t.Fatal("handler ran before manifest schema validation")
	}
}

func TestDirectToolsCallAllLSPToolsUseContentOnlyBoundary(t *testing.T) {
	root := canonicalToolTestRoot(t, t.TempDir())
	tests := []struct {
		name string
		args map[string]any
	}{
		{name: "file", args: map[string]any{"action": "read_file", "file_path": "cmd/mcp-lsp/tools.go"}},
		{name: "inspect", args: map[string]any{"action": "hover", "pos": "cmd/mcp-lsp/tools.go:54:6"}},
		{name: "xref", args: map[string]any{"action": "references", "pos": "cmd/mcp-lsp/tools.go:54:6"}},
		{name: "grep", args: map[string]any{"action": "text_search", "query": "ToolManifest", "paths": []string{"cmd/mcp-lsp"}}},
		{name: "structure", args: map[string]any{"action": "document_symbol", "file_path": "cmd/mcp-lsp/tools.go"}},
		{name: "patch_edit", args: map[string]any{"action": "format", "file_path": "cmd/mcp-lsp/tools.go"}},
		{name: "completion", args: map[string]any{"pos": "cmd/mcp-lsp/tools.go:54:6"}},
	}
	called := make(map[string]bool, len(tests))
	handlers := make(ToolHandlers, len(tests))
	for _, test := range tests {
		name := test.name
		handlers[name] = func(context.Context, json.RawMessage) (any, error) {
			called[name] = true
			return "OK\\ttool=" + name, nil
		}
	}
	defs := toolDefinitions(handlers)
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			response := runDirectToolCallForPlainText(t, mustDirectToolCallRequest(t, root, test.name, test.args), defs)
			if response.Result.IsError {
				t.Fatalf("tools/call(%s) unexpectedly returned error: %q", test.name, response.Result.Content[0].Text)
			}
			if len(bytes.TrimSpace(response.Result.StructuredContent)) != 0 {
				t.Fatalf("tools/call(%s) returned structuredContent: %s", test.name, response.Result.StructuredContent)
			}
			if len(response.Result.Content) != 1 || !strings.Contains(response.Result.Content[0].Text, "OK\\ttool="+test.name) {
				t.Fatalf("tools/call(%s) content = %#v, want one plain-text item", test.name, response.Result.Content)
			}
			if !called[test.name] {
				t.Fatalf("tools/call(%s) did not execute its handler", test.name)
			}
		})
	}
}

func TestToolDefinitionAssemblyCompilesEveryManifestValidatorAndAction(t *testing.T) {
	manifests := newLSPToolManifests()
	defs, err := compileToolDefinitions(manifests, nil)
	if err != nil {
		t.Fatalf("compileToolDefinitions() error = %v", err)
	}
	if len(defs) != len(manifests) {
		t.Fatalf("compiled definitions = %d, want %d", len(defs), len(manifests))
	}
	for _, def := range defs {
		if def.validator == nil {
			t.Fatalf("tool %q has no assembled schema validator", def.Manifest.Name)
		}
	}
}

func TestToolDefinitionAssemblyRejectsStaleManifestActionCondition(t *testing.T) {
	manifest := ToolManifest{
		Name: "stale",
		Schema: schema{
			"type": "object",
			"properties": map[string]any{
				"action": map[string]any{"type": "string", "enum": []string{"current"}},
			},
			"allOf": []any{actionCondition("stale", schema{})},
		},
	}
	_, err := compileToolDefinitions([]ToolManifest{manifest}, nil)
	if err == nil || !strings.Contains(err.Error(), "missing from action enum") {
		t.Fatalf("compileToolDefinitions() error = %v, want stale action guard failure", err)
	}
}
