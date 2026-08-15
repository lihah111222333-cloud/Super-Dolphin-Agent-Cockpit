//go:build linux && e2e

package main

import (
	"context"
	"os/exec"
	"strings"
	"testing"
)

// TestMcpLSPBinaryWSLAcceptsWindowsWorkDir_E2E 锁定 Windows 主控传入的工作区路径可由 Linux sidecar 使用。
func TestMcpLSPBinaryWSLAcceptsWindowsWorkDir_E2E(t *testing.T) {
	if !runningUnderWSL(t) {
		t.Skip("Windows work_dir bridge requires WSL")
	}
	root := repoRootForMcpLSPBinaryTest(t)
	windowsRoot := windowsWorkDirForWSLE2E(t, root)
	binary := buildMcpLSPBinaryForTest(t)
	client := startMcpLSPBinaryForTest(t, context.Background(), binary, root, "")
	defer client.close(t)
	client.call(t, "initialize", map[string]any{"protocolVersion": "2024-11-05"})
	result := client.callTool(t, "grep", map[string]any{
		"action": "text_search", "query": "normalizeExplicitWorkDir",
		"path": "cmd/mcp-lsp/tools", "max_results": 2, "work_dir": windowsRoot,
	})
	requireMCPToolSuccess(t, client, result, "Windows work_dir grep")
	if !strings.Contains(result.Result.ContentText(), "factory_scope.go") {
		t.Fatalf("Windows work_dir grep missed factory_scope.go: %s", result.Result.ContentText())
	}
}

// windowsWorkDirForWSLE2E 使用 WSL 官方转换器生成 Windows 主控实际发送的路径。
func windowsWorkDirForWSLE2E(t *testing.T, root string) string {
	t.Helper()
	output, err := exec.Command("wslpath", "-w", root).CombinedOutput()
	if err != nil {
		t.Fatalf("wslpath -w %q: %v; output=%s", root, err, output)
	}
	return strings.TrimSpace(string(output))
}
