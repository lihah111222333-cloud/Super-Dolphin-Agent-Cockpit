//go:build e2e
// +build e2e

package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestMcpLSPBinaryShellDiagnosticsUsesShellcheck_E2E(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping mcp-lsp binary e2e test in short mode")
	}
	if os.Getenv("MCP_LSP_REAL_SHELL_E2E") != "1" {
		t.Skip("set MCP_LSP_REAL_SHELL_E2E=1 to run the npm-backed shell diagnostics e2e")
	}

	root := t.TempDir()
	target := filepath.Join(root, "broken.sh")
	if err := os.WriteFile(target, []byte("#!/usr/bin/env bash\nif [ \"$1\" = \"x\" ]; then\necho x\n"), 0o600); err != nil {
		t.Fatalf("write shell diagnostics fixture: %v", err)
	}

	binary := buildMcpLSPBinaryForTest(t)
	shellBinDir := installShellDiagnosticsBinsForE2E(t)
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Second)
	defer cancel()
	client := startMcpLSPBinaryForTestWithEnv(t, ctx, binary, root, shellBinDir, nil)
	defer client.close(t)

	client.call(t, "initialize", map[string]any{"protocolVersion": "2024-11-05"})
	diagnostics := client.callTool(t, "file", map[string]any{
		"action":    "diagnostics",
		"file_path": target,
	})
	requireMCPToolSuccess(t, client, diagnostics, "shell diagnostics")
	payload := decodeDiagnosticsStructuredContent(t, diagnostics.Result.StructuredContent)
	if payload.Total == 0 || !payload.HasFile(target) {
		t.Fatalf("shell diagnostics returned no rows; payload=%s text=%q stderr=%s",
			diagnostics.Result.StructuredContent, diagnostics.Result.ContentText(), client.stderrString())
	}
	if !shellDiagnosticsPayloadHasShellcheckSource(payload, target) {
		t.Fatalf("shell diagnostics missing shellcheck source/code; payload=%s text=%q stderr=%s",
			diagnostics.Result.StructuredContent, diagnostics.Result.ContentText(), client.stderrString())
	}
}

func installShellDiagnosticsBinsForE2E(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("npm"); err != nil {
		t.Skipf("npm is required for shell diagnostics e2e: %v", err)
	}
	prefix := t.TempDir()
	cmd := exec.Command("npm", "install", "--prefix", prefix, "bash-language-server", "shellcheck")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("install shell diagnostics bins: %v\n%s", err, out)
	}
	binDir := filepath.Join(prefix, "node_modules", ".bin")
	shellcheck := filepath.Join(binDir, "shellcheck")
	if out, err := exec.Command(shellcheck, "--version").CombinedOutput(); err != nil {
		t.Fatalf("prewarm shellcheck binary: %v\n%s", err, out)
	}
	return binDir
}

func shellDiagnosticsPayloadHasShellcheckSource(payload diagnosticsPayload, target string) bool {
	for _, table := range payload.Data {
		if table.File != target {
			continue
		}
		for _, row := range table.Rows {
			if len(row) < 6 {
				continue
			}
			source, sourceOK := row[4].(string)
			code, codeOK := row[5].(string)
			if sourceOK && codeOK && source == "shellcheck" && strings.HasPrefix(code, "SC") {
				return true
			}
		}
	}
	return false
}
