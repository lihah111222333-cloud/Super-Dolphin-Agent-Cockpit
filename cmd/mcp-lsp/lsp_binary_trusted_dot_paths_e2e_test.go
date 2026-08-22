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

// TestMcpLSPBinaryTrustedRootParsesDotWorktreesAndHiddenFiles_E2E 证明可信根内的点目录和隐藏源文件可由真实 LSP 解析。
func TestMcpLSPBinaryTrustedRootParsesDotWorktreesAndHiddenFiles_E2E(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping trusted-root dot-path E2E test in short mode")
	}
	requireHostBinariesForE2E(t, []realLSPDiagnosticsCase{
		{languageID: "go", binaries: []string{"gopls"}},
		{languageID: "python", binaries: []string{"pyright-langserver"}},
	})

	trustedRoot, worktrees := writeTrustedRootDotWorktrees(t)
	binary := buildMcpLSPBinaryForTest(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	client := startMcpLSPBinaryForTestWithEnv(t, ctx, binary, trustedRoot, t.TempDir(), []string{
		"AGENT_LSP_SHARED_CACHE_DIR=" + filepath.Join(t.TempDir(), "lsp-cache"),
	})
	defer client.close(t)
	client.call(t, "initialize", map[string]any{"protocolVersion": "2024-11-05"})

	assertTrustedGoParsing(t, client, trustedRoot)
	for _, worktree := range worktrees {
		assertTrustedGoParsing(t, client, worktree)
		assertTrustedHiddenPythonParsing(t, client, worktree)
	}
	assertOutsideTrustedRootRejected(t, client)
}

func writeTrustedRootDotWorktrees(t *testing.T) (string, []string) {
	t.Helper()
	trustedRoot := t.TempDir()
	runRealGoplsGit(t, trustedRoot, "init")
	runRealGoplsGit(t, trustedRoot, "config", "user.name", "可信根 LSP E2E")
	runRealGoplsGit(t, trustedRoot, "config", "user.email", "trusted-root-e2e@example.invalid")
	writeTrustedRootFile(t, filepath.Join(trustedRoot, "go.mod"), "module example.com/trusted-root-e2e\n\ngo 1.25.0\n")
	writeTrustedRootFile(t, filepath.Join(trustedRoot, "main.go"), "package main\n\nfunc TrustedRootSymbol() int { return 42 }\n")
	runRealGoplsGit(t, trustedRoot, "add", "go.mod", "main.go")
	runRealGoplsGit(t, trustedRoot, "commit", "-m", "初始化可信根 E2E 仓库")

	worktrees := make([]string, 0, 2)
	for _, name := range []string{"feature-one", ".feature-two"} {
		worktree := filepath.Join(trustedRoot, ".worktrees", name)
		runRealGoplsGit(t, trustedRoot, "worktree", "add", "--detach", worktree, "HEAD")
		writeTrustedRootFile(t, filepath.Join(worktree, ".hidden.py"), "def hidden_symbol() -> int:\n    return 42\n\nvalue = hidden_symbol()\n")
		worktrees = append(worktrees, worktree)
	}
	return trustedRoot, worktrees
}

func writeTrustedRootFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write trusted-root fixture %s: %v", path, err)
	}
}

func assertTrustedGoParsing(t *testing.T, client *mcpLSPBinaryClient, worktree string) {
	t.Helper()
	target := filepath.Join(worktree, "main.go")
	result := client.callTool(t, "structure", map[string]any{
		"action":      "document_symbol",
		"file_path":   target,
		"language_id": "go",
		"max_results": 10,
		"work_dir":    worktree,
	})
	requireMCPToolSuccess(t, client, result, "trusted dot-worktree Go document symbols")
	requireToolResultContains(t, result, "TrustedRootSymbol", "trusted dot-worktree Go document symbols")
}

func assertTrustedHiddenPythonParsing(t *testing.T, client *mcpLSPBinaryClient, worktree string) {
	t.Helper()
	target := filepath.Join(worktree, ".hidden.py")
	result := client.callTool(t, "structure", map[string]any{
		"action":      "document_symbol",
		"file_path":   target,
		"language_id": "python",
		"max_results": 10,
		"work_dir":    worktree,
	})
	requireMCPToolSuccess(t, client, result, "trusted hidden Python document symbols")
	requireToolResultContains(t, result, "hidden_symbol", "trusted hidden Python document symbols")
	diagnostics := client.callTool(t, "diagnostics", map[string]any{
		"file_path":   target,
		"language_id": "python",
		"work_dir":    worktree,
	})
	requireMCPToolSuccess(t, client, diagnostics, "trusted hidden Python diagnostics")
}

func assertOutsideTrustedRootRejected(t *testing.T, client *mcpLSPBinaryClient) {
	t.Helper()
	outsideRoot := t.TempDir()
	outsideWorktree := filepath.Join(outsideRoot, ".worktrees", ".escape")
	if err := os.MkdirAll(outsideWorktree, 0o700); err != nil {
		t.Fatalf("create outside-root fixture: %v", err)
	}
	target := filepath.Join(outsideWorktree, "main.go")
	writeTrustedRootFile(t, target, "package main\n")
	result := client.callTool(t, "structure", map[string]any{
		"action":      "document_symbol",
		"file_path":   target,
		"language_id": "go",
		"max_results": 10,
		"work_dir":    outsideWorktree,
	})
	if !result.Result.IsError {
		t.Fatalf("outside trusted root unexpectedly succeeded; text=%q", result.Result.ContentText())
	}
	if strings.TrimSpace(string(result.Result.StructuredContent)) != "" {
		t.Fatalf("outside-root error contains deprecated structuredContent: %s", result.Result.StructuredContent)
	}
	if text := result.Result.ContentText(); !strings.Contains(text, "ERROR code=path_outside_workspace retryable=0") {
		t.Fatalf("outside-root error lacks trusted-root error code: %q", text)
	}
}
