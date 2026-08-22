//go:build e2e

package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestMcpLSPBinaryDiagnosticsUnifiedProtocol_E2E 通过真实 binary 锁定一级 diagnostics 行协议。
func TestMcpLSPBinaryDiagnosticsUnifiedProtocol_E2E(t *testing.T) {
	root, target := writePlainTextContractFixture(t)
	content, err := os.ReadFile(target)
	if err != nil || !bytes.Contains(content, []byte("plain_text_needle")) {
		t.Fatalf("native read/search diagnostics fixture: err=%v", err)
	}
	binary := buildMcpLSPBinaryForTest(t)
	fakeGoplsBinDir := writeFakeGoplsShutdownWarningLangserver(t)
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	client := startMcpLSPBinaryForTestWithEnv(t, ctx, binary, root, fakeGoplsBinDir, []string{
		fakeGoplsPlainTextContractEnv + "=1",
		"AGENT_LSP_SHARED_CACHE_DIR=" + filepath.Join(t.TempDir(), "lsp-cache"),
	})
	defer client.close(t)
	client.call(t, "initialize", map[string]any{"protocolVersion": "2024-11-05"})

	result := client.callTool(t, "diagnostics", map[string]any{"file_path": target})
	text := assertPlainTextOnlyMCPResult(t, result, false)
	if !strings.HasPrefix(text, "OK total=") || !strings.Contains(text, "unit=diagnostic") {
		t.Fatalf("diagnostics tools/call text = %q", text)
	}

	missing := client.callTool(t, "diagnostics", map[string]any{"file_path": filepath.Join(root, "missing.go")})
	errorText := assertPlainTextOnlyMCPResult(t, missing, true)
	errorLines := strings.Split(errorText, "\n")
	if len(errorLines) < 3 || errorLines[0] != "ERROR code=file_not_found retryable=0" ||
		!strings.HasPrefix(errorLines[1], "MESSAGE\t") || !strings.HasPrefix(errorLines[2], "HINT\t") {
		t.Fatalf("diagnostics failure did not use the unified ERROR envelope: %q", errorText)
	}
}
