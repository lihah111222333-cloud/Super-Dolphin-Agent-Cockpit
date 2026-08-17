//go:build e2e

package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/multilsp"
)

// 这些 E2E 辅助只构造协议测试 fixture、语言覆盖表和伪服务启动脚本，
// 不读取或分派宿主平台行为，因此在所有 e2e 目标上共享。
const (
	fakeMultilangDiagnosticsEnv        = "MCP_LSP_FAKE_MULTILANG_DIAGNOSTICS"
	fakeMultilangServerEnv             = "MCP_LSP_FAKE_MULTILANG_SERVER"
	fakeMultilangLifecycleJournalEnv   = "MCP_LSP_FAKE_MULTILANG_LIFECYCLE_JOURNAL"
	fakeMultilangPendingRequestGateEnv = "MCP_LSP_FAKE_MULTILANG_PENDING_REQUEST_GATE"
	fakeMultilangDiagnosticDelayEnv    = "MCP_LSP_FAKE_MULTILANG_DIAGNOSTIC_DELAY"
	binaryColdStartDiagnosticsDelay    = 1750 * time.Millisecond
	binaryColdStartDiagnosticsSlack    = 250 * time.Millisecond
)

type realLSPDiagnosticsCase struct {
	languageID string
	binaries   []string
	write      func(t *testing.T, root string) string
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

type binaryColdStartLanguageCase struct {
	languageID string
	write      func(t *testing.T, root string) string
}

func binaryColdStartLanguageCases(t *testing.T) []binaryColdStartLanguageCase {
	t.Helper()
	cases := []binaryColdStartLanguageCase{
		{languageID: "css", write: writeBinaryColdStartCSSFixture},
		{languageID: "c", write: writeBinaryColdStartCFixture},
		{languageID: "cpp", write: writeBinaryColdStartCPPFixture},
		{languageID: "csharp", write: writeBinaryColdStartCSharpFixture},
		{languageID: "dart", write: writeBinaryColdStartDartFixture},
		{languageID: "dockerfile", write: writeBinaryColdStartDockerFixture},
		{languageID: "go", write: writeBinaryColdStartGoFixture},
		{languageID: "gomod", write: writeBinaryColdStartGoModFixture},
		{languageID: "gosum", write: writeBinaryColdStartGoSumFixture},
		{languageID: "gowork", write: writeBinaryColdStartGoWorkFixture},
		{languageID: "graphql", write: writeBinaryColdStartGraphQLFixture},
		{languageID: "html", write: writeBinaryColdStartHTMLFixture},
		{languageID: "java", write: writeBinaryColdStartJavaFixture},
		{languageID: "javascript", write: writeBinaryColdStartJavaScriptFixture},
		{languageID: "javascriptreact", write: writeBinaryColdStartJavaScriptReactFixture},
		{languageID: "json", write: writeBinaryColdStartJSONFixture},
		{languageID: "kotlin", write: writeBinaryColdStartKotlinFixture},
		{languageID: "lua", write: writeBinaryColdStartLuaFixture},
		{languageID: "markdown", write: writeBinaryColdStartMarkdownFixture},
		{languageID: "objective-c", write: writeBinaryColdStartObjectiveCFixture},
		{languageID: "objective-cpp", write: writeBinaryColdStartObjectiveCPPFixture},
		{languageID: "php", write: writeBinaryColdStartPHPFixture},
		{languageID: "prisma", write: writeBinaryColdStartPrismaFixture},
		{languageID: "proto", write: writeBinaryColdStartProtoFixture},
		{languageID: "python", write: writeBinaryColdStartPythonFixture},
		{languageID: "ruby", write: writeBinaryColdStartRubyFixture},
		{languageID: "rust", write: writeBinaryColdStartRustFixture},
		{languageID: "shellscript", write: writeBinaryColdStartShellFixture},
		{languageID: "sql", write: writeBinaryColdStartSQLFixture},
		{languageID: "svelte", write: writeBinaryColdStartSvelteFixture},
		{languageID: "swift", write: writeBinaryColdStartSwiftFixture},
		{languageID: "terraform", write: writeBinaryColdStartTerraformFixture},
		{languageID: "typescript", write: writeBinaryColdStartTypeScriptFixture},
		{languageID: "typescriptreact", write: writeBinaryColdStartTypeScriptReactFixture},
		{languageID: "vue", write: writeBinaryColdStartVueFixture},
		{languageID: "yaml", write: writeBinaryColdStartYAMLFixture},
	}
	assertBinaryColdStartCasesCoverDefaultLSPClientLanguages(t, cases)
	return cases
}

func assertBinaryColdStartCasesCoverDefaultLSPClientLanguages(t *testing.T, cases []binaryColdStartLanguageCase) {
	t.Helper()
	got := make([]string, 0, len(cases))
	for _, tc := range cases {
		got = append(got, tc.languageID)
	}
	slices.Sort(got)
	want := defaultBinaryLSPClientLanguageIDs(t)
	if !slices.Equal(got, want) {
		t.Fatalf("binary cold-start language coverage = %#v, want default LSP client languages %#v", got, want)
	}
}

func defaultBinaryLSPClientLanguageIDs(t *testing.T) []string {
	t.Helper()
	registry := multilsp.NewDefaultLanguageAdapterRegistry()
	ids := make([]string, 0)
	for _, languageID := range registry.LanguageIDs() {
		adapter, ok := registry.AdapterForLanguage(languageID)
		if !ok {
			t.Fatalf("missing adapter for default language %q", languageID)
		}
		if adapter.CapabilityPolicy().RequiresLSPClient {
			ids = append(ids, languageID)
		}
	}
	slices.Sort(ids)
	return ids
}

func writeFakeMultilangDiagnosticsLangservers(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	for _, name := range []string{
		"bash-language-server", "buf", "clangd", "csharp-ls", "dart", "docker-langserver",
		"graphql-lsp", "gopls", "intelephense", "jdtls", "kotlin-language-server",
		"lua-language-server", "pyright-langserver", "prisma-language-server", "rust-analyzer",
		"shellcheck", "sqruff", "sourcekit-lsp", "solargraph", "svelteserver", "terraform-ls",
		"typescript-language-server", "vscode-css-language-server", "vscode-html-language-server",
		"vscode-json-language-server", "vscode-markdown-language-server", "vue-language-server",
		"yaml-language-server",
	} {
		script := "#!/bin/sh\n" +
			fakeMultilangDiagnosticsEnv + "=1 " + fakeMultilangServerEnv + "=" + shellQuote(name) +
			" exec " + shellQuote(os.Args[0]) +
			" -test.run=TestFakeMultilangDiagnosticsLangserverHelper -- \"$@\"\n"
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte(script), 0o700); err != nil {
			t.Fatalf("write fake %s: %v", name, err)
		}
	}
	return dir
}

// super-dolphin-ci: helper
func TestFakeMultilangDiagnosticsLangserverHelper(t *testing.T) {
	if os.Getenv(fakeMultilangDiagnosticsEnv) != "1" {
		return
	}
	runFakeMultilangDiagnosticsLangserver()
	os.Exit(0)
}
