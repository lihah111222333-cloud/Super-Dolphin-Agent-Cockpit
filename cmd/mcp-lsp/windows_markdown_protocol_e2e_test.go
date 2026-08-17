//go:build windows && e2e

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/installer"
)

const realMarkdownWindowsE2EEnv = "SUPER_DOLPHIN_RUN_MARKDOWN_WINDOWS_E2E"

// TestMcpLSPBinaryRealNodeMarkdownWindowsE2E 通过生产 mcp-lsp binary 运行
// Markdown 单语言 36-action 矩阵；所有 markdown/* server requests 和 watcher
// onChange 都只能由真实 Markdown client protocol handler 回答。
func TestMcpLSPBinaryRealNodeMarkdownWindowsE2E(t *testing.T) {
	if os.Getenv(realMarkdownWindowsE2EEnv) != "1" {
		t.Skip("set SUPER_DOLPHIN_RUN_MARKDOWN_WINDOWS_E2E=1 to run the targeted Windows Markdown protocol e2e")
	}
	started := time.Now()
	root := realNodeRepoRoot(t)
	realNodeProvisionWindowsVCLibsDesktopAppLocal(t)
	nodeDist, npmBin := realNodeBundle(t, root)
	pins := realNodeScriptPins(t, root)
	installDir := t.TempDir()
	registerRealMCPTempRootCleanup(t, installDir)
	if stringsContainsPath(installDir, "lsp-all-npm") {
		t.Fatalf("real Markdown npm install unexpectedly uses forbidden shared cache: %s", installDir)
	}
	realNodeInstall(t, npmBin, nodeDist, installDir, pins)
	realNodeVerifyInstall(t, installDir)
	realNodeVerifyNativeAstGrepRuntime(t, installDir)
	markdownPackage := filepath.Join(installDir, "node_modules", "markdown-it", "package.json")
	if !fileExists(markdownPackage) {
		t.Fatalf("locked markdown-it %s is missing from the exact Windows npm cohort", runtimeMarkdownItInstallVersion)
	}
	t.Logf("Markdown protocol npm cohort version=%s package=%s installDir=%s", runtimeMarkdownItInstallVersion, markdownPackage, installDir)

	servers := realNodeServerCasesForLanguage("markdown")
	requireRealNodeServerCaseIdentities(t, servers)
	if len(servers) != 1 {
		t.Fatalf("targeted Markdown server cases = %d, want exactly 1", len(servers))
	}
	host, err := installer.DetectWindowsHostPlatform()
	if err != nil {
		t.Fatalf("detect Windows native/process architecture for Markdown E2E: %v", err)
	}
	platform := fmt.Sprintf("windows-native-%s-process-%s", host.NativeArch, host.ProcessArch)
	for _, server := range servers {
		server := server
		t.Run(platform+"/"+server.name+"-raw-lsp", func(t *testing.T) {
			runRealNodeServer(t, root, nodeDist, installDir, server)
		})
	}

	binary := buildRealMcpLSPBinary(t, root)
	t.Logf("Markdown wire/action ledger: exact methods markdown/parse, markdown/fs/readFile, markdown/fs/stat, markdown/fs/readDirectory, markdown/findMarkdownFilesInWorkspace, markdown/fs/watcher/create, markdown/fs/watcher/delete, markdown/fs/watcher/onChange; matrix=%d actions; process ledger is emitted by runRealMCPToolCoverageForServers", realMCPExpectedActionCount)
	runRealMCPToolCoverageForServers(t, root, binary, nodeDist, installDir, servers, 1)
	t.Logf("targeted Windows Markdown protocol E2E completed in %s; platform=%s; action ledger has exact callable/empty/unsupported/error classification and shutdown residual proof", time.Since(started).Round(time.Millisecond), platform)
}

func stringsContainsPath(path, needle string) bool {
	return filepath.IsAbs(path) && len(needle) > 0 && strings.Contains(strings.ToLower(filepath.ToSlash(path)), strings.ToLower(filepath.ToSlash(needle)))
}
