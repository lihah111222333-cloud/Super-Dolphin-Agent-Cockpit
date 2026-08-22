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

// TestMcpLSPBinaryWorkspaceSymbolRefreshesExternalDiskChangeForAllLSPClientLanguages_E2E 是 fake-server lifecycle E2E。
// 它保留各语言原始 fixture 并用相应注释或结构化字段写入标记；严格 wire 顺序由 multilsp unit fake 锁定。
func TestMcpLSPBinaryWorkspaceSymbolRefreshesExternalDiskChangeForAllLSPClientLanguages_E2E(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping mcp-lsp binary e2e test in short mode")
	}

	binary := buildMcpLSPBinaryForTest(t)
	fakeServersBinDir := writeFakeMultilangDiagnosticsLangservers(t)
	for _, tc := range binaryColdStartLanguageCases(t) {
		t.Run(tc.languageID, func(t *testing.T) {
			runWorkspaceSymbolExternalDiskRefreshCase(t, binary, fakeServersBinDir, tc)
		})
	}
}

func runWorkspaceSymbolExternalDiskRefreshCase(t *testing.T, binary, fakeServersBinDir string, tc binaryColdStartLanguageCase) {
	t.Helper()
	root := t.TempDir()
	target := tc.write(t, root)
	writeWorkspaceSymbolBootstrapFile(t, target)

	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	client := startMcpLSPBinaryForTestWithEnv(t, ctx, binary, root, fakeServersBinDir, nil)
	defer client.close(t)
	client.call(t, "initialize", map[string]any{"protocolVersion": "2024-11-05"})
	warmWorkspaceSymbolDocument(t, client, target, tc.languageID)

	fresh := freshWorkspaceSymbolName(tc.languageID)
	staleContent, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("read stale %s fixture: %v", tc.languageID, err)
	}
	if err := os.WriteFile(target, []byte(freshWorkspaceSymbolContent(tc.languageID, string(staleContent), fresh)), 0o600); err != nil {
		t.Fatalf("externally rewrite %s fixture: %v", tc.languageID, err)
	}
	workspaceSymbols := client.callTool(t, "structure", workspaceSymbolRequest(tc.languageID, fresh))
	assertWorkspaceSymbolExternalDiskRefresh(t, tc.languageID, target, fresh, workspaceSymbols)
}

func warmWorkspaceSymbolDocument(t *testing.T, client *mcpLSPBinaryClient, target, languageID string) {
	t.Helper()
	warmup := client.callTool(t, "structure", map[string]any{
		"action":      "document_symbol",
		"file_path":   target,
		"language_id": languageID,
	})
	requireMCPToolSuccess(t, client, warmup, languageID+" stale-cache warmup")
}

func workspaceSymbolRequest(languageID, query string) map[string]any {
	return map[string]any{
		"action":             "workspace_symbol",
		"workspace_language": languageID,
		"query":              query,
		"max_results":        10,
	}
}

func assertWorkspaceSymbolExternalDiskRefresh(t *testing.T, languageID, target, fresh string, response mcpLSPBinaryResponse) {
	t.Helper()
	if languageOnlyWorkspaceSymbolUnsupported(languageID) {
		assertWorkspaceLanguageOnlySymbolFailFast(t, languageID, response)
		return
	}
	if response.Result.IsError {
		t.Fatalf("%s workspace_symbol after external rewrite returned MCP error: %q", languageID, response.Result.ContentText())
	}
	requireToolResultContains(t, response, fresh, languageID+" workspace symbol after external rewrite")
	requireToolResultContains(t, response, target, languageID+" workspace symbol target URI")
	if stale := staleWorkspaceSymbolName(languageID); strings.Contains(response.Result.ContentText(), stale) {
		t.Fatalf("%s workspace_symbol target %s retained stale symbol %q: %s", languageID, target, stale, response.Result.ContentText())
	}
}

func writeWorkspaceSymbolBootstrapFile(t *testing.T, target string) {
	t.Helper()
	extension := filepath.Ext(target)
	if extension == "" {
		return
	}
	path := filepath.Join(filepath.Dir(target), "000-workspace-bootstrap"+extension)
	if err := os.WriteFile(path, []byte("workspace bootstrap\n"), 0o600); err != nil {
		t.Fatalf("write workspace bootstrap file %s: %v", path, err)
	}
}

func freshWorkspaceSymbolName(languageID string) string {
	return "FreshWorkspaceNeedle-" + languageID
}

func staleWorkspaceSymbolName(languageID string) string {
	return "StaleWorkspaceNeedle-" + languageID
}

func freshWorkspaceSymbolContent(languageID, original, fresh string) string {
	trimmed := strings.TrimSpace(original)
	switch languageID {
	case "json":
		if withoutClosingBrace, ok := strings.CutSuffix(trimmed, "}"); ok {
			return withoutClosingBrace + `,"freshWorkspaceNeedle":"` + fresh + `"}` + "\n"
		}
		return original
	case "css":
		return original + "\n/* " + fresh + " */\n"
	case "html", "markdown", "svelte", "vue":
		return original + "\n<!-- " + fresh + " -->\n"
	case "dockerfile", "graphql", "python", "ruby", "shellscript", "terraform", "yaml":
		return original + "\n# " + fresh + "\n"
	case "lua", "sql":
		return original + "\n-- " + fresh + "\n"
	default:
		return original + "\n// " + fresh + "\n"
	}
}

func languageOnlyWorkspaceSymbolUnsupported(languageID string) bool {
	switch languageID {
	case "json", "markdown", "sql", "yaml":
		return true
	default:
		return false
	}
}

func assertWorkspaceLanguageOnlySymbolFailFast(t *testing.T, languageID string, response mcpLSPBinaryResponse) {
	t.Helper()
	if languageID == "sql" {
		if !response.Result.IsError || !strings.Contains(response.Result.ContentText(), "requires file_path") {
			t.Fatalf("sql workspace_language-only workspace_symbol = error=%v text=%q, want file_path requirement", response.Result.IsError, response.Result.ContentText())
		}
		return
	}
	if response.Result.IsError {
		t.Fatalf("%s workspace_language-only workspace_symbol returned MCP error: %q", languageID, response.Result.ContentText())
	}
	if !strings.Contains(response.Result.ContentText(), "not available for "+languageID) {
		t.Fatalf("%s workspace_language-only workspace_symbol = %q, want explicit unsupported message", languageID, response.Result.ContentText())
	}
}
