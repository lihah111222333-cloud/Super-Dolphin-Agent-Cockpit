//go:build e2e

package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestMcpLSPBinaryLanguageParameterRenameContract_E2E 通过真实临时 binary 的 tools/list 与 tools/call 锁定无兼容参数改名。
func TestMcpLSPBinaryLanguageParameterRenameContract_E2E(t *testing.T) {
	client := startBinaryLanguageParameterContractClient(t)
	assertBinaryLanguageParameterSchemas(t, callBinaryToolsList(t, client))
	assertBinaryLegacyLanguageCallsFail(t, client)
	assertBinaryNewLanguageCallsSucceed(t, client)
}

// TestMcpLSPBinaryASTLanguageValidationContract_E2E 通过真实临时 binary 锁定显式 AST language 值域与 glob 冲突。
func TestMcpLSPBinaryASTLanguageValidationContract_E2E(t *testing.T) {
	client := startBinaryLanguageParameterContractClient(t)
	assertBinaryInvalidASTLanguagesFailFast(t, client)
}

// startBinaryLanguageParameterContractClient 构建临时 binary 并启动带可控 gopls 的真实 stdio MCP client。
func startBinaryLanguageParameterContractClient(t *testing.T) *mcpLSPBinaryClient {
	t.Helper()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/languageparams\n\ngo 1.26\n"), 0o600); err != nil {
		t.Fatalf("write go.mod: %v", err)
	}
	target, sibling := writeWorkspaceSymbolScopeFixtures(t, root)
	binary := buildMcpLSPBinaryForTest(t)
	fakeGoplsBinDir := writeFakeGoplsShutdownWarningLangserver(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	t.Cleanup(cancel)
	client := startMcpLSPBinaryForTestWithEnv(t, ctx, binary, root, fakeGoplsBinDir, []string{
		fakeGoplsWorkspaceSymbolsEnv + "=1",
		fakeGoplsWorkspaceSymbolTargetEnv + "=" + target,
		fakeGoplsWorkspaceSymbolSiblingEnv + "=" + sibling,
		"AGENT_LSP_SHARED_CACHE_DIR=" + filepath.Join(t.TempDir(), "lsp-cache"),
	})
	t.Cleanup(func() { client.close(t) })
	client.call(t, "initialize", map[string]any{"protocolVersion": "2024-11-05"})
	return client
}

func assertBinaryLanguageParameterSchemas(t *testing.T, listed []binaryListedTool) {
	t.Helper()
	byName := binaryListedToolsByName(t, listed, 7)
	assertBinaryRenamedSchemaField(t, byName["grep"], "ast_language")
	assertBinaryRenamedSchemaField(t, byName["structure"], "workspace_language")
	if strings.Contains(byName["structure"].Description, `"language"`) {
		t.Errorf("structure description retains legacy language example: %q", byName["structure"].Description)
	}
}

func assertBinaryRenamedSchemaField(t *testing.T, tool binaryListedTool, want string) {
	t.Helper()
	properties, ok := tool.InputSchema["properties"].(map[string]any)
	if !ok {
		t.Fatalf("%s schema properties type = %T", tool.Name, tool.InputSchema["properties"])
	}
	if _, ok := properties[want]; !ok {
		t.Errorf("%s tools/list schema missing %q", tool.Name, want)
	}
	if _, ok := properties["language"]; ok {
		t.Errorf("%s tools/list schema retains legacy language", tool.Name)
	}
}

func assertBinaryLegacyLanguageCallsFail(t *testing.T, client *mcpLSPBinaryClient) {
	t.Helper()
	tests := []struct {
		name      string
		tool      string
		arguments map[string]any
	}{
		{name: "grep", tool: "grep", arguments: map[string]any{"action": "ast_search", "query": "func Needle() {}", "paths": []string{"."}, "language": "go"}},
		{name: "structure", tool: "structure", arguments: map[string]any{"action": "workspace_symbol", "query": "Needle", "language": "go"}},
	}
	for _, test := range tests {
		t.Run("legacy "+test.name+" language", func(t *testing.T) {
			text := assertPlainTextOnlyMCPResult(t, client.callTool(t, test.tool, test.arguments), true)
			if !strings.HasPrefix(text, "ERROR code=invalid_params retryable=0\n") || !strings.Contains(text, `unknown field "language"`) {
				t.Errorf("legacy %s language result = %q, want strict invalid_params", test.name, text)
			}
		})
	}
}

func assertBinaryNewLanguageCallsSucceed(t *testing.T, client *mcpLSPBinaryClient) {
	t.Helper()
	t.Run("ast_language", func(t *testing.T) {
		text := assertPlainTextOnlyMCPResult(t, callToolForExplicitRemovedToolGuardE2E(t, client, "grep", map[string]any{
			"action": "ast_search", "query": "func Needle() {}", "paths": []string{"."}, "glob": "*.go", "ast_language": "go",
		}), true)
		if !strings.Contains(text, "Needle") {
			t.Errorf("ast_language result = %q, want Needle match", text)
		}
	})
	t.Run("workspace_language", func(t *testing.T) {
		text := assertPlainTextOnlyMCPResult(t, client.callTool(t, "structure", map[string]any{
			"action": "workspace_symbol", "query": "Needle", "workspace_language": "go", "max_results": 5,
		}), false)
		if !strings.Contains(text, "Needle") {
			t.Errorf("workspace_language result = %q, want Needle symbol", text)
		}
	})
}

func assertBinaryInvalidASTLanguagesFailFast(t *testing.T, client *mcpLSPBinaryClient) {
	t.Helper()
	tests := []struct {
		name      string
		arguments map[string]any
		want      string
	}{
		{name: "unknown explicit value", arguments: map[string]any{"action": "ast_search", "query": "func Needle() {}", "paths": []string{"."}, "glob": "*.go", "ast_language": "brainfuck"}, want: `unsupported ast_language "brainfuck"`},
		{name: "explicit glob conflict", arguments: map[string]any{"action": "ast_search", "query": "func Needle() {}", "paths": []string{"."}, "glob": "*.py", "ast_language": "go"}, want: "conflicts with glob"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			text := assertPlainTextOnlyMCPResult(t, callToolForExplicitRemovedToolGuardE2E(t, client, "grep", test.arguments), true)
			if !strings.HasPrefix(text, "ERROR code=invalid_params retryable=0\n") || !strings.Contains(text, test.want) {
				t.Errorf("invalid ast_language result = %q, want %q invalid_params", text, test.want)
			}
		})
	}
}
