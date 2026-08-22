//go:build e2e

package main

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
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
	diagnostics := client.callTool(t, "diagnostics", map[string]any{
		"file_path": target,
	})
	requireMCPToolSuccess(t, client, diagnostics, "shell diagnostics")
	payload := decodeDiagnosticsContentText(t, diagnostics.Result.ContentText())
	if payload.Total == 0 || !payload.HasFile(target) {
		t.Fatalf("shell diagnostics returned no rows; text=%q stderr=%s",
			diagnostics.Result.ContentText(), client.stderrString())
	}
	if !shellDiagnosticsPayloadHasShellcheckSource(payload, target) {
		t.Fatalf("shell diagnostics missing shellcheck source/code; text=%q stderr=%s",
			diagnostics.Result.ContentText(), client.stderrString())
	}
}

func TestMcpLSPBinaryGitHookDiagnosticsRoutesExtensionlessShellHook_E2E(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping mcp-lsp binary e2e test in short mode")
	}

	root := t.TempDir()
	hookDir := filepath.Join(root, ".githooks")
	if err := os.MkdirAll(hookDir, 0o755); err != nil {
		t.Fatalf("mkdir git hook fixture: %v", err)
	}
	target := filepath.Join(hookDir, "pre-commit")
	if err := os.WriteFile(target, []byte("#!/usr/bin/env bash\nif [ \"$1\" = \"x\" ]; then\necho x\n"), 0o700); err != nil {
		t.Fatalf("write git hook fixture: %v", err)
	}

	binary := buildMcpLSPBinaryForTest(t)
	shellBinDir := writeFakeShellDiagnosticsBinsForE2E(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	client := startMcpLSPBinaryForTestWithEnv(t, ctx, binary, root, shellBinDir, nil)
	defer client.close(t)

	client.call(t, "initialize", map[string]any{"protocolVersion": "2024-11-05"})
	diagnostics := client.callTool(t, "diagnostics", map[string]any{
		"file_path": target,
	})
	requireMCPToolSuccess(t, client, diagnostics, "git hook shell diagnostics")
	payload := decodeDiagnosticsContentText(t, diagnostics.Result.ContentText())
	if payload.Total == 0 || !payload.HasFile(target) {
		t.Fatalf("git hook diagnostics returned no rows; text=%q stderr=%s",
			diagnostics.Result.ContentText(), client.stderrString())
	}
	if got := payload.FirstMessageForFile(t, target); got != "fake shell hook diagnostic" {
		t.Fatalf("git hook diagnostics message = %q, want fake shell hook diagnostic; text=%q stderr=%s",
			got, diagnostics.Result.ContentText(), client.stderrString())
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

// super-dolphin-ci: helper
func TestFakeBashLanguageServerHelper(t *testing.T) {
	if os.Getenv("MCP_LSP_FAKE_BASHLS") != "1" {
		return
	}
	runFakeBashLanguageServer()
	os.Exit(0)
}

func writeFakeShellDiagnosticsBinsForE2E(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	serverPath := filepath.Join(dir, "bash-language-server")
	serverScript := "#!/bin/sh\nMCP_LSP_FAKE_BASHLS=1 exec " + shellQuote(os.Args[0]) + " -test.run=TestFakeBashLanguageServerHelper -- \"$@\"\n"
	if err := os.WriteFile(serverPath, []byte(serverScript), 0o700); err != nil {
		t.Fatalf("write fake bash-language-server: %v", err)
	}
	shellcheckPath := filepath.Join(dir, "shellcheck")
	shellcheckScript := "#!/bin/sh\nif [ \"$1\" = \"--version\" ]; then\n  echo 'ShellCheck - shell script analysis tool'\nfi\nexit 0\n"
	if err := os.WriteFile(shellcheckPath, []byte(shellcheckScript), 0o700); err != nil {
		t.Fatalf("write fake shellcheck: %v", err)
	}
	return dir
}

func runFakeBashLanguageServer() {
	reader := bufio.NewReader(os.Stdin)
	var goroutines sync.WaitGroup
	defer goroutines.Wait()
	writer := &fakeLSPWriter{w: os.Stdout, goroutines: &goroutines}
	for {
		raw, err := readFakeLSPFramedMessage(reader)
		if err != nil {
			return
		}
		var req fakeLSPRequest
		if err := json.Unmarshal(raw, &req); err != nil {
			continue
		}
		if req.Method == "exit" {
			return
		}
		if fakeBashLSHandleNotification(writer, req) {
			continue
		}
		if strings.TrimSpace(string(req.ID)) == "" {
			continue
		}
		_ = writer.writeResponse(req.ID, fakeBashLSResult(req))
	}
}

func fakeBashLSHandleNotification(writer *fakeLSPWriter, req fakeLSPRequest) bool {
	if strings.TrimSpace(string(req.ID)) != "" {
		return false
	}
	if req.Method != "textDocument/didOpen" {
		return false
	}
	var params fakeLSPDidOpenParams
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return true
	}
	uri := strings.TrimSpace(params.TextDocument.URI)
	if uri == "" {
		return true
	}
	_ = writer.writeNotification("textDocument/publishDiagnostics", fakeBashLSDiagnostics(uri))
	return true
}

func fakeBashLSResult(req fakeLSPRequest) any {
	switch req.Method {
	case "initialize":
		return map[string]any{
			"capabilities": map[string]any{
				"textDocumentSync": 1,
			},
		}
	case "shutdown":
		return nil
	default:
		return nil
	}
}

func fakeBashLSDiagnostics(uri string) map[string]any {
	return map[string]any{
		"uri": uri,
		"diagnostics": []map[string]any{{
			"range": map[string]any{
				"start": map[string]any{"line": 1, "character": 0},
				"end":   map[string]any{"line": 1, "character": 2},
			},
			"severity": 1,
			"source":   "shellcheck",
			"message":  "fake shell hook diagnostic",
			"code":     "SC1009",
		}},
	}
}
