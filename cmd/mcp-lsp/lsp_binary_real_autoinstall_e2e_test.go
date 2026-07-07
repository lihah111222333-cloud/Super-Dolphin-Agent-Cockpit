//go:build e2e

package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

type realLSPDiagnosticsCase struct {
	languageID string
	binaries   []string
	write      func(t *testing.T, root string) string
}

func TestMcpLSPBinaryDiagnosticsAutoInstallsNPMBackedLanguageServersWithRealPackages_E2E(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping mcp-lsp binary e2e test in short mode")
	}
	if runtime.GOOS == "windows" {
		t.Skip("test uses POSIX PATH isolation")
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
	client := startMcpLSPBinaryForTestWithEnv(t, ctx, binary, root, t.TempDir(), []string{
		"PATH=" + path,
		"NPM_CONFIG_PREFIX=" + npmPrefix,
	})
	defer client.close(t)
	client.call(t, "initialize", map[string]any{"protocolVersion": "2024-11-05"})

	for _, tc := range realNPMBackedDiagnosticsCases() {
		t.Run(tc.languageID, func(t *testing.T) {
			targetRoot := filepath.Join(root, tc.languageID)
			target := tc.write(t, targetRoot)
			diagnostics := client.callTool(t, "file", map[string]any{
				"action":    "diagnostics",
				"file_path": target,
			})
			requireMCPToolSuccess(t, client, diagnostics, "real "+tc.languageID+" diagnostics")
			requireRealInstalledBinaries(t, npmBin, tc.binaries)
		})
	}
}

func TestMcpLSPBinaryDiagnosticsAutoInstallsGoplsWithRealGo_E2E(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping mcp-lsp binary e2e test in short mode")
	}
	if runtime.GOOS == "windows" {
		t.Skip("test uses POSIX PATH isolation")
	}

	binary := buildMcpLSPBinaryForTest(t)
	root := t.TempDir()
	goBin := t.TempDir()
	toolBin := symlinkHostToolsForE2E(t, "go")
	path := goBin + string(os.PathListSeparator) + toolBin + string(os.PathListSeparator) + "/usr/bin:/bin"

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	client := startMcpLSPBinaryForTestWithEnv(t, ctx, binary, root, t.TempDir(), []string{
		"PATH=" + path,
		"GOBIN=" + goBin,
	})
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
			diagnostics := client.callTool(t, "file", map[string]any{
				"action":    "diagnostics",
				"file_path": target,
			})
			requireMCPToolSuccess(t, client, diagnostics, "real "+tc.languageID+" diagnostics")
			requireRealInstalledBinaries(t, goBin, tc.binaries)
		})
	}
}

func TestMcpLSPBinaryDiagnosticsAutoInstallsCSharpLSWithRealDotnet_E2E(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping mcp-lsp binary e2e test in short mode")
	}
	if runtime.GOOS == "windows" {
		t.Skip("test uses POSIX PATH isolation")
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
	diagnostics := client.callTool(t, "file", map[string]any{
		"action":    "diagnostics",
		"file_path": target,
	})
	requireMCPToolSuccess(t, client, diagnostics, "real csharp diagnostics")
	requireRealInstalledBinaries(t, dotnetTools, []string{"csharp-ls"})
}

func TestMcpLSPBinaryDiagnosticsAutoInstallsBrewBackedLanguageServersWithRealBrew_E2E(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping mcp-lsp binary e2e test in short mode")
	}
	if runtime.GOOS == "windows" {
		t.Skip("Homebrew e2e is only meaningful on POSIX hosts")
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
			diagnostics := client.callTool(t, "file", map[string]any{
				"action":    "diagnostics",
				"file_path": target,
			})
			requireMCPToolSuccess(t, client, diagnostics, "real "+tc.languageID+" diagnostics")
			requireHostBinariesForE2E(t, []realLSPDiagnosticsCase{tc})
		})
	}
}

func TestMcpLSPBinaryDiagnosticsWithRealSystemLanguageServers_E2E(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping mcp-lsp binary e2e test in short mode")
	}

	binary := buildMcpLSPBinaryForTest(t)
	root := t.TempDir()
	cases := []realLSPDiagnosticsCase{
		{"c", []string{"clangd"}, writeBinaryColdStartCFixture},
		{"cpp", []string{"clangd"}, writeBinaryColdStartCPPFixture},
		{"objective-c", []string{"clangd"}, writeBinaryColdStartObjectiveCFixture},
		{"objective-cpp", []string{"clangd"}, writeBinaryColdStartObjectiveCPPFixture},
		{"swift", []string{"sourcekit-lsp"}, writeBinaryColdStartSwiftFixture},
		{"rust", []string{"rust-analyzer"}, writeBinaryColdStartRustFixture},
		{"java", []string{"jdtls"}, writeBinaryColdStartJavaFixture},
	}
	requireHostBinariesForE2E(t, cases)

	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
	defer cancel()
	client := startMcpLSPBinaryForTestWithEnv(t, ctx, binary, root, t.TempDir(), nil)
	defer client.close(t)
	client.call(t, "initialize", map[string]any{"protocolVersion": "2024-11-05"})

	for _, tc := range cases {
		t.Run(tc.languageID, func(t *testing.T) {
			target := tc.write(t, filepath.Join(root, tc.languageID))
			diagnostics := client.callTool(t, "file", map[string]any{
				"action":    "diagnostics",
				"file_path": target,
			})
			requireMCPToolSuccess(t, client, diagnostics, "real "+tc.languageID+" diagnostics")
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
		{"sql", []string{"sql-language-server"}, writeBinaryColdStartSQLFixture},
	}
}

func requireHostBinariesForE2E(t *testing.T, cases []realLSPDiagnosticsCase) {
	t.Helper()
	seen := map[string]struct{}{}
	for _, tc := range cases {
		for _, binary := range tc.binaries {
			if _, ok := seen[binary]; ok {
				continue
			}
			seen[binary] = struct{}{}
			if _, err := exec.LookPath(binary); err != nil {
				t.Fatalf("real system e2e requires %s in PATH: %v", binary, err)
			}
		}
	}
}

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
