//go:build e2e && windows && arm64

package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestMcpLSPCachedRubyDiagnosticsAndDocumentSymbol_E2E(t *testing.T) {
	binary := strings.TrimSpace(os.Getenv("MCP_LSP_CACHED_PRODUCT_BINARY"))
	if binary == "" {
		t.Skip("set MCP_LSP_CACHED_PRODUCT_BINARY to the compiled mcp-lsp binary")
	}
	if _, err := os.Stat(binary); err != nil {
		t.Fatalf("cached product mcp-lsp binary is unavailable: %v", err)
	}
	repoRoot := repoRootForMcpLSPBinaryTest(t)
	productRoot := filepath.Join(repoRoot, ".super-dolphin")
	target := filepath.Join(repoRoot, "bin", "LSP", "test", "ruby", "lib", "rake.rb")
	if _, err := os.Stat(target); err != nil {
		t.Fatalf("Ruby target is unavailable: %v", err)
	}
	cacheRoot := filepath.Join(productRoot, "cache", "lsp-assets")
	cohortRoot := filepath.Join(cacheRoot, "runtime-dependencies", "ruby-lsp", "arm64", "language-server-protocol-3.17.0.0_ruby-4.0.5-1_ruby-lsp-0.26.10")
	if _, err := os.Stat(cohortRoot); err != nil {
		t.Fatalf("cached Ruby cohort is unavailable: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	root := filepath.Join(repoRoot, "bin", "LSP", "test", "ruby")
	client := startMcpLSPBinaryForTestWithEnv(t, ctx, binary, root, t.TempDir(), []string{
		"SUPER_DOLPHIN_HOME=" + productRoot,
		"SUPER_DOLPHIN_RUNTIME_RESOURCES_DIR=" + repoRoot,
		"SUPER_DOLPHIN_SIDECAR_OWNER_ID=cached-ruby-semantic",
	})
	defer client.close(t)
	client.call(t, "initialize", map[string]any{"protocolVersion": "2024-11-05"})
	client.call(t, "tools/list", map[string]any{})

	diagnostics := client.callTool(t, "file", map[string]any{
		"action":      "diagnostics",
		"file_path":   target,
		"language_id": "ruby",
	})
	requireMCPToolSuccess(t, client, diagnostics, "cached Ruby diagnostics")

	symbols := client.callTool(t, "structure", map[string]any{
		"action":      "document_symbol",
		"file_path":   target,
		"language_id": "ruby",
		"max_results": 100,
	})
	requireMCPToolSuccess(t, client, symbols, "cached Ruby document_symbol")
	if !strings.Contains(symbols.Result.ContentText(), "Rake") {
		t.Fatalf("cached Ruby document_symbol omitted Rake: %q; stderr=%s", symbols.Result.ContentText(), client.stderrString())
	}
}
