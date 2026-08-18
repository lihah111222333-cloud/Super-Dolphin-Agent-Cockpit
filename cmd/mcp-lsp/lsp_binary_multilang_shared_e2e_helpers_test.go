//go:build e2e

package main

import (
	"os"
	"os/exec"
	"slices"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/multilsp"
)

// 这些 E2E 辅助只构造协议测试 fixture 和语言覆盖表。
// 平台相关的伪服务启动器位于带平台 build tag 的文件中。
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
		{languageID: "mq4", write: writeBinaryColdStartMQ4Fixture},
		{languageID: "mq5", write: writeBinaryColdStartMQ5Fixture},
		{languageID: "mqh", write: writeBinaryColdStartMQHFixture},
		{languageID: "mql", write: writeBinaryColdStartMQLFixture},
		{languageID: "mql4", write: writeBinaryColdStartMQL4Fixture},
		{languageID: "mql5", write: writeBinaryColdStartMQL5Fixture},
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
	assertBinaryColdStartCasesCoverRequiresLSPClientLanguages(t, cases)
	return cases
}

func assertBinaryColdStartCasesCoverRequiresLSPClientLanguages(t *testing.T, cases []binaryColdStartLanguageCase) {
	t.Helper()
	got := make([]string, 0, len(cases))
	for _, tc := range cases {
		got = append(got, tc.languageID)
	}
	slices.Sort(got)
	want := requiresLSPClientLanguageIDs(t)
	if !slices.Equal(got, want) {
		t.Fatalf("binary cold-start language coverage = %#v, want RequiresLSPClient registry %#v", got, want)
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

var fakeMultilangDiagnosticsLangserverNames = [...]string{
	"bash-language-server", "buf", "clangd", "csharp-ls", "dart", "docker-langserver",
	"graphql-lsp", "gopls", "intelephense", "jdtls", "kotlin-language-server",
	"lua-language-server", "pyright-langserver", "prisma-language-server", "rust-analyzer",
	"shellcheck", "sqruff", "sourcekit-lsp", "solargraph", "svelteserver", "terraform-ls",
	"typescript-language-server", "vscode-css-language-server", "vscode-html-language-server",
	"vscode-json-language-server", "vscode-markdown-language-server", "vue-language-server",
	"yaml-language-server",
}

// super-dolphin-ci: helper
func TestFakeMultilangDiagnosticsLangserverHelper(t *testing.T) {
	if os.Getenv(fakeMultilangDiagnosticsEnv) != "1" {
		return
	}
	runFakeMultilangDiagnosticsLangserver()
	os.Exit(0)
}

// TestBinaryColdStartLanguageCasesMatchRequiresLSPClientRegistry keeps the
// platform-neutral binary matrix aligned with every public LSP client alias.
func TestBinaryColdStartLanguageCasesMatchRequiresLSPClientRegistry(t *testing.T) {
	cases := binaryColdStartLanguageCases(t)
	got := make([]string, 0, len(cases))
	for _, tc := range cases {
		got = append(got, tc.languageID)
	}
	slices.Sort(got)
	want := requiresLSPClientLanguageIDs(t)
	if !slices.Equal(got, want) {
		t.Fatalf("binary cold-start language coverage = %#v, want RequiresLSPClient registry %#v", got, want)
	}
}

// The public MQL IDs all route through clangd. Keep their fixtures on the
// contract extensions: mq4/mql/mql4 use .mq4, mq5/mql5 use .mq5, and mqh uses .mqh.
func writeBinaryColdStartMQ4Fixture(t *testing.T, root string) string {
	return writeBinaryColdStartMQLWithExtensionFixture(t, root, ".mq4")
}

func writeBinaryColdStartMQ5Fixture(t *testing.T, root string) string {
	return writeBinaryColdStartMQLWithExtensionFixture(t, root, ".mq5")
}

func writeBinaryColdStartMQHFixture(t *testing.T, root string) string {
	return writeBinaryColdStartMQLWithExtensionFixture(t, root, ".mqh")
}

func writeBinaryColdStartMQLFixture(t *testing.T, root string) string {
	return writeBinaryColdStartMQLWithExtensionFixture(t, root, ".mq4")
}

func writeBinaryColdStartMQL4Fixture(t *testing.T, root string) string {
	return writeBinaryColdStartMQLWithExtensionFixture(t, root, ".mq4")
}

func writeBinaryColdStartMQL5Fixture(t *testing.T, root string) string {
	return writeBinaryColdStartMQLWithExtensionFixture(t, root, ".mq5")
}

func writeBinaryColdStartMQLWithExtensionFixture(t *testing.T, root, extension string) string {
	t.Helper()
	writeBinaryColdStartFile(t, root, "compile_flags.txt", "-Wall\\n")
	return writeBinaryColdStartFile(t, root, "main"+extension, "#property strict\\nint OnInit() { return 0; }\\n")
}
