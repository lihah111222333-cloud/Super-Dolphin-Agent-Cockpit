//go:build e2e

package main

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// TestMcpLSPBinaryOpenFileUnifiedProtocol_E2E 通过真实 binary tools/call 锁定 open_file 成功行协议。
func TestMcpLSPBinaryOpenFileUnifiedProtocol_E2E(t *testing.T) {
	root, target := writePlainTextContractFixture(t)
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

	result := client.callTool(t, "file", map[string]any{
		"action":    "open_file",
		"file_path": target,
	})
	text := assertPlainTextOnlyMCPResult(t, result, false)
	info, err := os.Stat(target)
	if err != nil {
		t.Fatalf("stat open_file target: %v", err)
	}
	want := "OK total=1 showing=1 truncated=0 unit=file\n" +
		"ROW\tfile=" + filepath.ToSlash(target) + "\tbytes=" + strconv.FormatInt(info.Size(), 10) + "\tstatus=opened"
	if text != want {
		t.Fatalf("open_file tools/call text = %q, want %q", text, want)
	}

	missing := client.callTool(t, "file", map[string]any{
		"action":    "open_file",
		"file_path": filepath.Join(root, "missing.go"),
	})
	errorText := assertPlainTextOnlyMCPResult(t, missing, true)
	errorLines := strings.Split(errorText, "\n")
	if len(errorLines) < 3 || errorLines[0] != "ERROR code=file_not_found retryable=0" ||
		!strings.HasPrefix(errorLines[1], "MESSAGE\t") || !strings.HasPrefix(errorLines[2], "HINT\t") {
		t.Fatalf("open_file failure did not use the unified ERROR envelope: %q", errorText)
	}
}
