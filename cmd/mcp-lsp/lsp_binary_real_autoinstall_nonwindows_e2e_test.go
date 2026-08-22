//go:build !windows && e2e

package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// TestMcpLSPBinaryDiagnosticsAutoInstallsNPMBackedLanguageServersWithRealPackages_E2E
// 使用 POSIX PATH 隔离验证旧的真实 npm recipe；Windows 生产 cohort 由 Windows 专用测试证明。
func TestMcpLSPBinaryDiagnosticsAutoInstallsNPMBackedLanguageServersWithRealPackages_E2E(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping mcp-lsp binary e2e test in short mode")
	}
	binary := buildMcpLSPBinaryForTest(t)
	root := t.TempDir()
	npmPrefix := t.TempDir()
	npmBin := filepath.Join(npmPrefix, "bin")
	if err := os.MkdirAll(npmBin, 0o755); err != nil {
		t.Fatalf("mkdir npm prefix bin: %v", err)
	}
	toolBin := symlinkHostToolsForE2E(t, "node", "npm")
	path := npmBin + string(os.PathListSeparator) + toolBin + string(os.PathListSeparator) + "/usr/bin:/bin"
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()
	client := startMcpLSPBinaryForTestWithEnv(t, ctx, binary, root, t.TempDir(), []string{"PATH=" + path, "NPM_CONFIG_PREFIX=" + npmPrefix})
	defer client.close(t)
	client.call(t, "initialize", map[string]any{"protocolVersion": "2024-11-05"})
	for _, tc := range realNPMBackedDiagnosticsCases() {
		t.Run(tc.languageID, func(t *testing.T) {
			target := tc.write(t, filepath.Join(root, tc.languageID))
			diagnostics := client.callTool(t, "diagnostics", map[string]any{"file_path": target})
			requireMCPToolSuccess(t, client, diagnostics, "real "+tc.languageID+" diagnostics")
			requireRealInstalledBinaries(t, npmBin, tc.binaries)
		})
	}
}

// TestMcpLSPBinaryCompletionAutoInstallsRealCSSHTMLLanguageServers_E2E 锁定真实 npm
// 自动安装后的 CSS/HTML 启动契约：安装目录为空时，completion 首次请求必须触发生产
// recipe，随后 completion、hover 和 document_symbol 都不能返回 capability_unsupported。
func TestMcpLSPBinaryCompletionAutoInstallsRealCSSHTMLLanguageServers_E2E(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping real CSS/HTML completion auto-install e2e in short mode")
	}
	binary := buildMcpLSPBinaryForTest(t)
	root := t.TempDir()
	npmPrefix := t.TempDir()
	npmBin := filepath.Join(npmPrefix, "bin")
	if err := os.MkdirAll(npmBin, 0o755); err != nil {
		t.Fatalf("mkdir npm prefix bin: %v", err)
	}
	toolBin := symlinkHostToolsForE2E(t, "node", "npm")
	path := npmBin + string(os.PathListSeparator) + toolBin + string(os.PathListSeparator) + "/usr/bin:/bin"
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()
	client := startMcpLSPBinaryForTestWithEnv(t, ctx, binary, root, t.TempDir(), []string{"PATH=" + path, "NPM_CONFIG_PREFIX=" + npmPrefix})
	defer client.close(t)
	client.call(t, "initialize", map[string]any{"protocolVersion": "2024-11-05"})

	cases := []struct {
		name     string
		language string
		source   string
		line     int
		column   int
		binary   string
	}{
		{name: "css", language: "css", source: "css/styles/style.css", line: 1, column: 2, binary: "vscode-css-language-server"},
		{name: "html", language: "html", source: "html/index.html", line: 2, column: 2, binary: "vscode-html-language-server"},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			target := copyRealCSSHTMLCompletionFixture(t, root, tc.source)
			completion := client.callTool(t, "structure", map[string]any{"action": "document_symbol", "file_path": target})
			requireRealCSSHTMLCompletionSuccess(t, client, completion, tc.language+" completion after npm auto-install")
			hover := client.callTool(t, "structure", map[string]any{"action": "document_symbol", "file_path": target})
			requireRealCSSHTMLCompletionSuccess(t, client, hover, tc.language+" hover after npm auto-install")
			symbols := client.callTool(t, "structure", map[string]any{"action": "document_symbol", "file_path": target})
			requireRealCSSHTMLCompletionSuccess(t, client, symbols, tc.language+" document_symbol after npm auto-install")
			requireRealInstalledBinaries(t, npmBin, []string{tc.binary})
		})
	}
}

// TestMcpLSPBinaryDiagnosticsAutoInstallsGoplsWithRealGo_E2E 使用非 Windows 的 POSIX PATH recipe。
func TestMcpLSPBinaryDiagnosticsAutoInstallsGoplsWithRealGo_E2E(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping mcp-lsp binary e2e test in short mode")
	}
	binary := buildMcpLSPBinaryForTest(t)
	root := t.TempDir()
	goBin := t.TempDir()
	toolBin := symlinkHostToolsForE2E(t, "go")
	path := goBin + string(os.PathListSeparator) + toolBin + string(os.PathListSeparator) + "/usr/bin:/bin"
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	client := startMcpLSPBinaryForTestWithEnv(t, ctx, binary, root, t.TempDir(), []string{"PATH=" + path, "GOBIN=" + goBin})
	defer client.close(t)
	client.call(t, "initialize", map[string]any{"protocolVersion": "2024-11-05"})
	for _, tc := range []realLSPDiagnosticsCase{
		{"go", []string{"gopls"}, writeBinaryColdStartGoFixture},
		{"gomod", []string{"gopls"}, writeBinaryColdStartGoModFixture},
		{"gosum", []string{"gopls"}, writeBinaryColdStartGoSumFixture},
		{"gowork", []string{"gopls"}, writeBinaryColdStartGoWorkFixture},
	} {
		t.Run(tc.languageID, func(t *testing.T) {
			target := tc.write(t, filepath.Join(root, tc.languageID))
			diagnostics := client.callTool(t, "diagnostics", map[string]any{"file_path": target})
			requireMCPToolSuccess(t, client, diagnostics, "real "+tc.languageID+" diagnostics")
			requireRealInstalledBinaries(t, goBin, tc.binaries)
		})
	}
}

// TestMcpLSPBinaryDiagnosticsAutoInstallsBrewBackedLanguageServersWithRealBrew_E2E
// 只在非 Windows 源码矩阵中验证 Homebrew recipe。
func TestMcpLSPBinaryDiagnosticsAutoInstallsBrewBackedLanguageServersWithRealBrew_E2E(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping mcp-lsp binary e2e test in short mode")
	}
	if _, err := exec.LookPath("brew"); err != nil {
		t.Fatalf("real brew-backed e2e requires brew in PATH: %v", err)
	}
	binary := buildMcpLSPBinaryForTest(t)
	root := t.TempDir()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()
	client := startMcpLSPBinaryForTestWithEnv(t, ctx, binary, root, t.TempDir(), nil)
	defer client.close(t)
	client.call(t, "initialize", map[string]any{"protocolVersion": "2024-11-05"})
	for _, tc := range realBrewBackedDiagnosticsCases() {
		t.Run(tc.languageID, func(t *testing.T) {
			target := tc.write(t, filepath.Join(root, tc.languageID))
			diagnostics := client.callTool(t, "diagnostics", map[string]any{"file_path": target})
			requireMCPToolSuccess(t, client, diagnostics, "real "+tc.languageID+" diagnostics")
			requireHostBinariesForE2E(t, []realLSPDiagnosticsCase{tc})
		})
	}
}

func realBrewBackedDiagnosticsCases() []realLSPDiagnosticsCase {
	return []realLSPDiagnosticsCase{
		{"ruby", []string{"solargraph"}, writeBinaryColdStartRubyFixture},
		{"kotlin", []string{"kotlin-language-server"}, writeBinaryColdStartKotlinFixture},
		{"dart", []string{"dart"}, writeBinaryColdStartDartFixture},
		{"lua", []string{"lua-language-server"}, writeBinaryColdStartLuaFixture},
		{"terraform", []string{"terraform-ls"}, writeBinaryColdStartTerraformFixture},
	}
}

func realNPMBackedDiagnosticsCases() []realLSPDiagnosticsCase {
	return []realLSPDiagnosticsCase{
		{"javascript", []string{"typescript-language-server"}, writeBinaryColdStartJavaScriptFixture},
		{"javascriptreact", []string{"typescript-language-server"}, writeBinaryColdStartJavaScriptReactFixture},
		{"typescript", []string{"typescript-language-server"}, writeBinaryColdStartTypeScriptFixture},
		{"typescriptreact", []string{"typescript-language-server"}, writeBinaryColdStartTypeScriptReactFixture},
		{"python", []string{"pyright-langserver"}, writeBinaryColdStartPythonFixture},
		{"css", []string{"vscode-css-language-server"}, writeBinaryColdStartCSSFixture},
		{"html", []string{"vscode-html-language-server"}, writeBinaryColdStartHTMLFixture},
		{"json", []string{"vscode-json-language-server"}, writeBinaryColdStartJSONFixture},
		{"markdown", []string{"vscode-markdown-language-server"}, writeBinaryColdStartMarkdownFixture},
		{"yaml", []string{"yaml-language-server"}, writeBinaryColdStartYAMLFixture},
		{"vue", []string{"vue-language-server"}, writeBinaryColdStartVueFixture},
		{"svelte", []string{"svelteserver"}, writeBinaryColdStartSvelteFixture},
		{"php", []string{"intelephense"}, writeBinaryColdStartPHPFixture},
		{"dockerfile", []string{"docker-langserver"}, writeBinaryColdStartDockerFixture},
		{"graphql", []string{"graphql-lsp"}, writeBinaryColdStartGraphQLFixture},
		{"prisma", []string{"prisma-language-server"}, writeBinaryColdStartPrismaFixture},
		{"shellscript", []string{"bash-language-server", "shellcheck"}, writeBinaryColdStartShellFixture},
	}
}

// symlinkHostToolsForE2E 构造非 Windows POSIX PATH 隔离目录。
func symlinkHostToolsForE2E(t *testing.T, names ...string) string {
	t.Helper()
	dir := t.TempDir()
	for _, name := range names {
		path, err := exec.LookPath(name)
		if err != nil {
			t.Fatalf("host tool %s is required for real e2e: %v", name, err)
		}
		link := filepath.Join(dir, name)
		if err := os.Symlink(path, link); err != nil {
			t.Fatalf("symlink %s -> %s: %v", link, path, err)
		}
	}
	return dir
}

// requireRealInstalledBinaries 校验非 Windows recipe 的可执行位与安装闭包。
func requireRealInstalledBinaries(t *testing.T, binDir string, names []string) {
	t.Helper()
	for _, name := range names {
		path := filepath.Join(binDir, mcpLSPExecutableFileName(name))
		info, err := os.Stat(path)
		if err != nil {
			t.Fatalf("real installed binary %s missing at %s: %v", name, path, err)
		}
		if info.IsDir() || info.Mode()&0111 == 0 {
			t.Fatalf("real installed binary %s at %s is not executable: mode=%s", name, path, info.Mode())
		}
	}
}
