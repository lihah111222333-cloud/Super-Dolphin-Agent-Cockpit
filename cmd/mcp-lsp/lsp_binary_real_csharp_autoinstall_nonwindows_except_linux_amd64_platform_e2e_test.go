//go:build !windows && (!linux || !amd64) && e2e

package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestMcpLSPBinaryDiagnosticsAutoInstallsCSharpLSWithRealDotnet_E2E 验证旧的 POSIX
// dotnet tool recipe；Windows 与 Linux/amd64 使用各自锁定的生产 installer，因此编译期排除。
func TestMcpLSPBinaryDiagnosticsAutoInstallsCSharpLSWithRealDotnet_E2E(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping mcp-lsp binary e2e test in short mode")
	}
	binary := buildMcpLSPBinaryForTest(t)
	root := t.TempDir()
	dotnetHome := filepath.Join(t.TempDir(), "dotnet-cli")
	dotnetTools := filepath.Join(dotnetHome, ".dotnet", "tools")
	toolBin := symlinkHostToolsForE2E(t, "dotnet")
	path := dotnetTools + string(os.PathListSeparator) + toolBin + string(os.PathListSeparator) + "/usr/bin:/bin"
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	client := startMcpLSPBinaryForTestWithEnv(t, ctx, binary, root, t.TempDir(), []string{
		"PATH=" + path,
		"DOTNET_CLI_HOME=" + dotnetHome,
		"HOME=" + filepath.Join(t.TempDir(), "home"),
	})
	defer client.close(t)
	client.call(t, "initialize", map[string]any{"protocolVersion": "2024-11-05"})
	target := writeBinaryColdStartCSharpFixture(t, root)
	diagnostics := client.callTool(t, "file", map[string]any{"action": "diagnostics", "file_path": target})
	requireMCPToolSuccess(t, client, diagnostics, "real csharp diagnostics")
	requireRealInstalledBinaries(t, dotnetTools, []string{"csharp-ls"})
}
