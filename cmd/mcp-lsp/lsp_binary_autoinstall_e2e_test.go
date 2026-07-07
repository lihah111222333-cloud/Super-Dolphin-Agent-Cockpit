//go:build e2e

package main

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"
)

type binaryAutoInstallLanguageCase struct {
	languageID       string
	installCommand   string
	installedBinary  string
	requiredSnippets []string
	write            func(t *testing.T, root string) string
}

func TestMcpLSPBinaryDiagnosticsAutoInstallsMissingLanguageServers_E2E(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping mcp-lsp binary e2e test in short mode")
	}
	if runtime.GOOS == "windows" {
		t.Skip("test uses POSIX shell scripts as fake installers")
	}

	binary := buildMcpLSPBinaryForTest(t)
	for _, tc := range binaryAutoInstallLanguageCases(t) {
		t.Run(tc.languageID, func(t *testing.T) {
			root := t.TempDir()
			target := tc.write(t, root)
			binDir := t.TempDir()
			marker := filepath.Join(t.TempDir(), "installer-args")
			writeFakeAutoInstallCommand(t, binDir, marker, tc)

			ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
			defer cancel()
			client := startMcpLSPBinaryForTestWithEnv(t, ctx, binary, root, binDir, []string{
				"PATH=" + binDir,
				"FAKE_AUTO_INSTALL_BIN=" + binDir,
				"FAKE_AUTO_INSTALL_MARKER=" + marker,
			})
			defer client.close(t)

			client.call(t, "initialize", map[string]any{"protocolVersion": "2024-11-05"})
			diagnostics := client.callTool(t, "file", map[string]any{
				"action":    "diagnostics",
				"file_path": target,
			})
			requireMCPToolSuccess(t, client, diagnostics, tc.languageID+" diagnostics after auto-install")
			requireAutoInstallMarker(t, marker, tc)
			requireAutoInstallDiagnostic(t, client, diagnostics, target, tc.languageID)
		})
	}
}

func binaryAutoInstallLanguageCases(t *testing.T) []binaryAutoInstallLanguageCase {
	t.Helper()
	cases := []binaryAutoInstallLanguageCase{
		{languageID: "c", installCommand: "brew", installedBinary: "clangd", requiredSnippets: []string{"install", "llvm"}, write: writeBinaryColdStartCFixture},
		{languageID: "cpp", installCommand: "brew", installedBinary: "clangd", requiredSnippets: []string{"install", "llvm"}, write: writeBinaryColdStartCPPFixture},
		{languageID: "css", installCommand: "npm", installedBinary: "vscode-css-language-server", requiredSnippets: []string{"install", "-g", "vscode-langservers-extracted", "vscode-markdown-languageservice@0.5.0-alpha.11"}, write: writeBinaryColdStartCSSFixture},
		{languageID: "csharp", installCommand: "dotnet", installedBinary: "csharp-ls", requiredSnippets: []string{"tool", "install", "--global", "csharp-ls"}, write: writeBinaryColdStartCSharpFixture},
		{languageID: "dart", installCommand: "brew", installedBinary: "dart", requiredSnippets: []string{"install", "dart-sdk"}, write: writeBinaryColdStartDartFixture},
		{languageID: "dockerfile", installCommand: "npm", installedBinary: "docker-langserver", requiredSnippets: []string{"install", "-g", "dockerfile-language-server-nodejs"}, write: writeBinaryColdStartDockerFixture},
		{languageID: "go", installCommand: "go", installedBinary: "gopls", requiredSnippets: []string{"install", "golang.org/x/tools/gopls@latest"}, write: writeBinaryColdStartGoFixture},
		{languageID: "gomod", installCommand: "go", installedBinary: "gopls", requiredSnippets: []string{"install", "golang.org/x/tools/gopls@latest"}, write: writeBinaryColdStartGoModFixture},
		{languageID: "gosum", installCommand: "go", installedBinary: "gopls", requiredSnippets: []string{"install", "golang.org/x/tools/gopls@latest"}, write: writeBinaryColdStartGoSumFixture},
		{languageID: "gowork", installCommand: "go", installedBinary: "gopls", requiredSnippets: []string{"install", "golang.org/x/tools/gopls@latest"}, write: writeBinaryColdStartGoWorkFixture},
		{languageID: "graphql", installCommand: "npm", installedBinary: "graphql-lsp", requiredSnippets: []string{"install", "-g", "graphql-language-service-cli"}, write: writeBinaryColdStartGraphQLFixture},
		{languageID: "html", installCommand: "npm", installedBinary: "vscode-html-language-server", requiredSnippets: []string{"install", "-g", "vscode-langservers-extracted", "vscode-markdown-languageservice@0.5.0-alpha.11"}, write: writeBinaryColdStartHTMLFixture},
		{languageID: "java", installCommand: "brew", installedBinary: "jdtls", requiredSnippets: []string{"install", "jdtls"}, write: writeBinaryColdStartJavaFixture},
		{languageID: "javascript", installCommand: "npm", installedBinary: "typescript-language-server", requiredSnippets: []string{"install", "-g", "typescript-language-server", "typescript"}, write: writeBinaryColdStartJavaScriptFixture},
		{languageID: "javascriptreact", installCommand: "npm", installedBinary: "typescript-language-server", requiredSnippets: []string{"install", "-g", "typescript-language-server", "typescript"}, write: writeBinaryColdStartJavaScriptReactFixture},
		{languageID: "json", installCommand: "npm", installedBinary: "vscode-json-language-server", requiredSnippets: []string{"install", "-g", "vscode-langservers-extracted", "vscode-markdown-languageservice@0.5.0-alpha.11"}, write: writeBinaryColdStartJSONFixture},
		{languageID: "kotlin", installCommand: "brew", installedBinary: "kotlin-language-server", requiredSnippets: []string{"install", "kotlin-language-server"}, write: writeBinaryColdStartKotlinFixture},
		{languageID: "lua", installCommand: "brew", installedBinary: "lua-language-server", requiredSnippets: []string{"install", "lua-language-server"}, write: writeBinaryColdStartLuaFixture},
		{languageID: "markdown", installCommand: "npm", installedBinary: "vscode-markdown-language-server", requiredSnippets: []string{"install", "-g", "vscode-langservers-extracted", "vscode-markdown-languageservice@0.5.0-alpha.11"}, write: writeBinaryColdStartMarkdownFixture},
		{languageID: "objective-c", installCommand: "brew", installedBinary: "clangd", requiredSnippets: []string{"install", "llvm"}, write: writeBinaryColdStartObjectiveCFixture},
		{languageID: "objective-cpp", installCommand: "brew", installedBinary: "clangd", requiredSnippets: []string{"install", "llvm"}, write: writeBinaryColdStartObjectiveCPPFixture},
		{languageID: "php", installCommand: "npm", installedBinary: "intelephense", requiredSnippets: []string{"install", "-g", "intelephense"}, write: writeBinaryColdStartPHPFixture},
		{languageID: "prisma", installCommand: "npm", installedBinary: "prisma-language-server", requiredSnippets: []string{"install", "-g", "@prisma/language-server"}, write: writeBinaryColdStartPrismaFixture},
		{languageID: "python", installCommand: "npm", installedBinary: "pyright-langserver", requiredSnippets: []string{"install", "-g", "pyright"}, write: writeBinaryColdStartPythonFixture},
		{languageID: "ruby", installCommand: "brew", installedBinary: "solargraph", requiredSnippets: []string{"install", "solargraph"}, write: writeBinaryColdStartRubyFixture},
		{languageID: "rust", installCommand: "rustup", installedBinary: "rust-analyzer", requiredSnippets: []string{"component", "add", "rust-analyzer"}, write: writeBinaryColdStartRustFixture},
		{languageID: "shellscript", installCommand: "npm", installedBinary: "bash-language-server", requiredSnippets: []string{"install", "-g", "bash-language-server", "shellcheck"}, write: writeBinaryColdStartShellFixture},
		{languageID: "sql", installCommand: "npm", installedBinary: "sql-language-server", requiredSnippets: []string{"install", "-g", "sql-language-server", "vscode-languageserver-protocol@3.17.5", "vscode-jsonrpc@8.2.0"}, write: writeBinaryColdStartSQLFixture},
		{languageID: "svelte", installCommand: "npm", installedBinary: "svelteserver", requiredSnippets: []string{"install", "-g", "svelte-language-server"}, write: writeBinaryColdStartSvelteFixture},
		{languageID: "swift", installCommand: "brew", installedBinary: "sourcekit-lsp", requiredSnippets: []string{"install", "swift"}, write: writeBinaryColdStartSwiftFixture},
		{languageID: "terraform", installCommand: "brew", installedBinary: "terraform-ls", requiredSnippets: []string{"install", "hashicorp/tap/terraform-ls"}, write: writeBinaryColdStartTerraformFixture},
		{languageID: "typescript", installCommand: "npm", installedBinary: "typescript-language-server", requiredSnippets: []string{"install", "-g", "typescript-language-server", "typescript"}, write: writeBinaryColdStartTypeScriptFixture},
		{languageID: "typescriptreact", installCommand: "npm", installedBinary: "typescript-language-server", requiredSnippets: []string{"install", "-g", "typescript-language-server", "typescript"}, write: writeBinaryColdStartTypeScriptReactFixture},
		{languageID: "vue", installCommand: "npm", installedBinary: "vue-language-server", requiredSnippets: []string{"install", "-g", "@vue/language-server"}, write: writeBinaryColdStartVueFixture},
		{languageID: "yaml", installCommand: "npm", installedBinary: "yaml-language-server", requiredSnippets: []string{"install", "-g", "yaml-language-server"}, write: writeBinaryColdStartYAMLFixture},
	}
	assertAutoInstallCasesCoverDefaultLSPClientLanguages(t, cases)
	return cases
}

func assertAutoInstallCasesCoverDefaultLSPClientLanguages(t *testing.T, cases []binaryAutoInstallLanguageCase) {
	t.Helper()
	got := make([]string, 0, len(cases))
	for _, tc := range cases {
		got = append(got, tc.languageID)
	}
	slices.Sort(got)
	want := defaultBinaryLSPClientLanguageIDs(t)
	if !slices.Equal(got, want) {
		t.Fatalf("auto-install language coverage = %#v, want default LSP client languages %#v", got, want)
	}
}

func writeFakeAutoInstallCommand(t *testing.T, binDir, marker string, tc binaryAutoInstallLanguageCase) {
	t.Helper()
	script := autoInstallCommandScript(t, marker, tc)
	path := filepath.Join(binDir, tc.installCommand)
	if err := os.WriteFile(path, []byte(script), 0o700); err != nil {
		t.Fatalf("write fake %s installer: %v", tc.installCommand, err)
	}
}

func autoInstallCommandScript(t *testing.T, marker string, tc binaryAutoInstallLanguageCase) string {
	t.Helper()
	var checks strings.Builder
	for _, snippet := range tc.requiredSnippets {
		checks.WriteString("case \" $* \" in\n")
		checks.WriteString("  *\" ")
		checks.WriteString(snippet)
		checks.WriteString(" \"*) ;;\n")
		checks.WriteString("  *) echo \"missing install arg ")
		checks.WriteString(snippet)
		checks.WriteString(": $*\" >&2; exit 1 ;;\n")
		checks.WriteString("esac\n")
	}
	return "#!/bin/sh\nset -eu\n" +
		autoInstallGoCommandBranches() +
		checks.String() +
		"printf '%s\\n' \"$*\" > " + shellQuote(marker) + "\n" +
		autoInstallWriteLSPBinaryScript(t, tc.installedBinary) +
		autoInstallWriteShellcheckScript(tc)
}

func autoInstallGoCommandBranches() string {
	return "if [ \"${1:-}\" = \"env\" ]; then\n" +
		"  printf '%s\\n' \"${GOBIN:-}\" \"${GOPATH:-}\"\n" +
		"  exit 0\n" +
		"fi\n" +
		"if [ \"${1:-}\" = \"version\" ]; then\n" +
		"  echo \"go version go1.25.6 darwin/arm64\"\n" +
		"  exit 0\n" +
		"fi\n" +
		"if [ \"${1:-}\" = \"work\" ]; then\n" +
		"  echo '{\"Use\":[{\"DiskPath\":\"./module\"}]}'\n" +
		"  exit 0\n" +
		"fi\n"
}

func autoInstallWriteLSPBinaryScript(t *testing.T, binary string) string {
	t.Helper()
	return "/bin/cat > \"$FAKE_AUTO_INSTALL_BIN/" + binary + "\" <<'EOF'\n" +
		"#!/bin/sh\n" +
		"if [ \"${1:-}\" = \"--version\" ]; then\n" +
		"  echo \"fake language server\"\n" +
		"  exit 0\n" +
		"fi\n" +
		"MCP_LSP_FAKE_MULTILANG_DIAGNOSTICS=1 exec " + shellQuote(os.Args[0]) + " -test.run=TestFakeMultilangDiagnosticsLangserverHelper -- \"$@\"\n" +
		"EOF\n" +
		"/bin/chmod +x \"$FAKE_AUTO_INSTALL_BIN/" + binary + "\"\n"
}

func autoInstallWriteShellcheckScript(tc binaryAutoInstallLanguageCase) string {
	if tc.languageID != "shellscript" {
		return ""
	}
	return "/bin/cat > \"$FAKE_AUTO_INSTALL_BIN/shellcheck\" <<'EOF'\n" +
		"#!/bin/sh\n" +
		"echo \"ShellCheck - fake\"\n" +
		"exit 0\n" +
		"EOF\n" +
		"/bin/chmod +x \"$FAKE_AUTO_INSTALL_BIN/shellcheck\"\n"
}

func requireAutoInstallMarker(t *testing.T, marker string, tc binaryAutoInstallLanguageCase) {
	t.Helper()
	raw, err := os.ReadFile(marker)
	if err != nil {
		t.Fatalf("%s installer marker missing: %v", tc.languageID, err)
	}
	args := string(raw)
	for _, snippet := range tc.requiredSnippets {
		if !strings.Contains(args, snippet) {
			t.Fatalf("%s installer args = %q, missing %q", tc.languageID, args, snippet)
		}
	}
}

func requireAutoInstallDiagnostic(t *testing.T, client *mcpLSPBinaryClient, diagnostics mcpLSPBinaryResponse, target, languageID string) {
	t.Helper()
	payload := decodeDiagnosticsStructuredContent(t, diagnostics.Result.StructuredContent)
	if !payload.HasFile(target) {
		t.Fatalf("%s diagnostics missing target %s: payload=%#v raw=%s text=%q stderr=%s",
			languageID, target, payload, diagnostics.Result.StructuredContent,
			diagnostics.Result.ContentText(), client.stderrString())
	}
	message := payload.FirstMessageForFile(t, target)
	if !strings.Contains(message, "fake cold-start diagnostic for "+languageID) {
		t.Fatalf("%s diagnostics message = %q, want fake diagnostic; raw=%s stderr=%s",
			languageID, message, diagnostics.Result.StructuredContent, client.stderrString())
	}
}
