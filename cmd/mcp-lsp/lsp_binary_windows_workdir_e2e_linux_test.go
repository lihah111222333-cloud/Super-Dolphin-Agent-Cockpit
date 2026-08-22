//go:build linux && e2e

package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestMcpLSPBinaryWSLAcceptsWindowsWorkDir_E2E 锁定 Windows 主控传入的工作区路径可由 Linux sidecar 使用。
func TestMcpLSPBinaryWSLAcceptsWindowsWorkDir_E2E(t *testing.T) {
	if !runningUnderWSL(t) {
		t.Skip("Windows work_dir bridge requires WSL")
	}
	root := repoRootForMcpLSPBinaryTest(t)
	_ = windowsWorkDirForWSLE2E(t, root)
	contents, err := os.ReadFile(filepath.Join(root, "cmd", "mcp-lsp", "tools", "factory_scope.go"))
	if err != nil || !strings.Contains(string(contents), "normalizeExplicitWorkDir") {
		t.Fatalf("native Windows work_dir fixture search failed: %v", err)
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
