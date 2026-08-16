//go:build e2e && (darwin || linux || windows)

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/multilsp"
)

const allLanguageToolMatrixTimeout = 30 * time.Minute

func TestAllLanguageToolMatrixTimeoutExceedsTenMinutes(t *testing.T) {
	if allLanguageToolMatrixTimeout <= 10*time.Minute {
		t.Fatalf("all-language tool matrix timeout = %s, want greater than 10 minutes", allLanguageToolMatrixTimeout)
	}
}

const (
	fakeMultilangDiagnosticsEnv        = "MCP_LSP_FAKE_MULTILANG_DIAGNOSTICS"
	fakeMultilangServerEnv             = "MCP_LSP_FAKE_MULTILANG_SERVER"
	fakeMultilangLifecycleJournalEnv   = "MCP_LSP_FAKE_MULTILANG_LIFECYCLE_JOURNAL"
	fakeMultilangPendingRequestGateEnv = "MCP_LSP_FAKE_MULTILANG_PENDING_REQUEST_GATE"
	fakeMultilangDiagnosticDelayEnv    = "MCP_LSP_FAKE_MULTILANG_DIAGNOSTIC_DELAY"
	binaryColdStartDiagnosticsDelay    = 1750 * time.Millisecond
	binaryColdStartDiagnosticsSlack    = 250 * time.Millisecond
)

func TestMcpLSPBinaryFakeServerDiagnosticsColdStartCoversAllLSPClientLanguages_E2E(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping mcp-lsp binary e2e test in short mode")
	}

	binary := buildMcpLSPBinaryForTest(t)
	fakeServersBinDir := writeFakeMultilangDiagnosticsLangservers(t)

	for _, tc := range binaryColdStartLanguageCases(t) {
		t.Run(tc.languageID, func(t *testing.T) {
			root := t.TempDir()
			target := tc.write(t, root)

			ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
			defer cancel()
			client := startMcpLSPBinaryForTestWithEnv(t, ctx, binary, root, fakeServersBinDir, []string{
				fakeMultilangDiagnosticDelayEnv + "=" + binaryColdStartDiagnosticsDelay.String(),
			})
			defer client.close(t)

			client.call(t, "initialize", map[string]any{"protocolVersion": "2024-11-05"})

			startedAt := time.Now()
			diagnostics := client.callTool(t, "file", map[string]any{
				"action":    "diagnostics",
				"file_path": target,
			})
			elapsed := time.Since(startedAt)
			requireMCPToolSuccess(t, client, diagnostics, tc.languageID+" diagnostics")
			if tc.languageID == "sql" {
				payload := decodeDiagnosticsStructuredContent(t, diagnostics.Result.StructuredContent)
				if payload.Total != 0 || payload.HasFile(target) {
					t.Fatalf("valid SQLite fake-server diagnostics = %#v, want no diagnostics before real parser tests", payload)
				}
				return
			}
			if elapsed < binaryColdStartDiagnosticsDelay-binaryColdStartDiagnosticsSlack {
				t.Fatalf("%s diagnostics returned in %s, want it to wait for delayed cold-start diagnostics >= %s; structured=%s stderr=%s",
					tc.languageID, elapsed, binaryColdStartDiagnosticsDelay-binaryColdStartDiagnosticsSlack,
					diagnostics.Result.StructuredContent, client.stderrString())
			}

			payload := decodeDiagnosticsStructuredContent(t, diagnostics.Result.StructuredContent)
			if !payload.HasFile(target) {
				t.Fatalf("%s diagnostics missing target %s: payload=%#v raw=%s text=%q stderr=%s",
					tc.languageID, target, payload, diagnostics.Result.StructuredContent,
					diagnostics.Result.ContentText(), client.stderrString())
			}
			message := payload.FirstMessageForFile(t, target)
			want := "fake cold-start diagnostic for " + tc.languageID
			if !strings.Contains(message, want) {
				t.Fatalf("%s diagnostics message = %q, want %q; payload=%#v raw=%s stderr=%s",
					tc.languageID, message, want, payload, diagnostics.Result.StructuredContent, client.stderrString())
			}
		})
	}
}

// TestMcpLSPBinaryAllToolActionsCoverAllLSPClientLanguages_E2E 锁定每个默认语言
// adapter 都能通过真实 mcp-lsp 二进制完成 7 个工具的全部 LSP action。
func TestMcpLSPBinaryAllToolActionsCoverAllLSPClientLanguages_E2E(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping all-language semantic tool E2E in short mode")
	}
	runMcpLSPBinaryAllToolActionsForAllLSPClientLanguagesE2E(t)
}

func runMcpLSPBinaryAllToolActionsForAllLSPClientLanguagesE2E(t *testing.T) {
	t.Helper()
	binary := buildMcpLSPBinaryForTest(t)
	fakeServersBinDir := writeFakeMultilangDiagnosticsLangservers(t)
	for _, tc := range binaryColdStartLanguageCases(t) {
		t.Run(tc.languageID, func(t *testing.T) {
			runMcpLSPBinaryAllToolActionsForLanguageE2E(t, binary, fakeServersBinDir, tc)
		})
	}
}

func runMcpLSPBinaryAllToolActionsForLanguageE2E(t *testing.T, binary, fakeServersBinDir string, tc binaryColdStartLanguageCase) {
	root := t.TempDir()
	target := tc.write(t, root)
	ctx, cancel := context.WithTimeout(context.Background(), allLanguageToolMatrixTimeout)
	defer cancel()
	extraEnv := []string{"AGENT_LSP_SHARED_CACHE_DIR=" + filepath.Join(t.TempDir(), "lsp-cache")}
	if serverName, ok := map[string]string{
		"sql":   "sqruff",
		"swift": "sourcekit-lsp",
		"ruby":  "solargraph",
	}[tc.languageID]; ok {
		// 真实 managed server 的安装与能力由独立 E2E 校验；此测试只锁定
		// mcp-lsp 的协议路由，因此显式隔离会抢占 fake PATH 的 ManagedOnly adapter。
		fakeBundleDir := writeFakeProtocolBundle(t, fakeServersBinDir, serverName, tc.languageID)
		extraEnv = append(extraEnv,
			"SUPER_DOLPHIN_LSP_BUNDLE_DIR="+fakeBundleDir,
			"SUPER_DOLPHIN_LSP_MANIFEST="+filepath.Join(fakeBundleDir, "manifest.json"),
		)
	}
	client := startMcpLSPBinaryForTestWithEnv(t, ctx, binary, root, fakeServersBinDir, extraEnv)
	defer client.close(t)
	client.call(t, "initialize", map[string]any{"protocolVersion": "2024-11-05"})
	warmWorkspaceSymbolDocument(t, client, target, tc.languageID)
	current, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("read %s replace_range fixture: %v", tc.languageID, err)
	}
	var editableLine string
	for _, line := range strings.Split(strings.ReplaceAll(string(current), "\r\n", "\n"), "\n") {
		if strings.TrimSpace(line) != "" {
			editableLine = line
			break
		}
	}
	if editableLine == "" {
		t.Fatalf("%s replace_range fixture has no non-empty line", tc.languageID)
	}
	replaceStartedAt := time.Now()
	replaced := client.callTool(t, "patch_edit", map[string]any{
		"action":    "replace_range",
		"file_path": target,
		"patch":     "@@\n-" + editableLine + "\n+" + editableLine + " ",
	})
	t.Logf("language=%s tool=patch_edit action=replace_range elapsed=%s", tc.languageID, time.Since(replaceStartedAt))
	requireMCPToolSuccess(t, client, replaced, tc.languageID+" patch_edit replace_range")
	if payload := replaced.Result.ContentText() + string(replaced.Result.StructuredContent); !strings.Contains(payload, "applied") {
		t.Fatalf("%s replace_range did not report applied: text=%q structured=%s stderr=%s",
			tc.languageID, replaced.Result.ContentText(), replaced.Result.StructuredContent, client.stderrString())
	}

	pos := target + ":1:1"
	checks := []binaryAllLanguageToolCheck{
		{tool: "file", args: map[string]any{"action": "read_file", "file_path": target}},
		{tool: "file", args: map[string]any{"action": "read_file", "file_paths": []string{target}}, want: filepath.Base(target)},
		{tool: "file", args: map[string]any{"action": "diagnostics", "file_path": target}},
		{tool: "file", args: map[string]any{"action": "diagnostics", "file_paths": []string{target}}},
		{tool: "grep", args: map[string]any{"action": "text_search", "query": "fixture", "paths": []string{target}}},
		{tool: "structure", args: map[string]any{"action": "document_symbol", "file_path": target}},
		{tool: "structure", args: map[string]any{"action": "workspace_symbol", "file_path": target, "query": staleWorkspaceSymbolName(tc.languageID)}},
		{tool: "structure", args: map[string]any{"action": "folding_range", "file_path": target}},
		{tool: "structure", args: map[string]any{"action": "semantic_tokens", "file_path": target}},
		{tool: "inspect", args: map[string]any{"action": "hover", "pos": pos}, want: "FakeHover"},
		{tool: "inspect", args: map[string]any{"action": "definition", "pos": pos}, want: filepath.Base(target)},
		{tool: "inspect", args: map[string]any{"action": "implementation", "pos": pos}, want: filepath.Base(target)},
		{tool: "inspect", args: map[string]any{"action": "type_definition", "pos": pos}, want: filepath.Base(target)},
		{tool: "inspect", args: map[string]any{"action": "signature_help", "pos": pos}, want: "FakeSignature"},
		{tool: "xref", args: map[string]any{"action": "references", "pos": pos}, want: filepath.Base(target)},
		{tool: "xref", args: map[string]any{"action": "call_hierarchy", "pos": pos, "direction": "incoming"}},
		{tool: "xref", args: map[string]any{"action": "call_hierarchy", "pos": pos, "direction": "outgoing"}},
		{tool: "xref", args: map[string]any{"action": "call_hierarchy", "pos": pos, "direction": "both"}},
		{tool: "xref", args: map[string]any{"action": "type_hierarchy", "pos": pos, "direction": "supertypes"}},
		{tool: "xref", args: map[string]any{"action": "type_hierarchy", "pos": pos, "direction": "subtypes"}},
		{tool: "xref", args: map[string]any{"action": "type_hierarchy", "pos": pos, "direction": "both"}},
		{tool: "completion", args: map[string]any{"pos": pos}, want: "FakeCompletion"},
		{tool: "patch_edit", args: map[string]any{"action": "code_action", "pos": pos}},
		{tool: "patch_edit", args: map[string]any{"action": "format", "file_path": target}},
		{tool: "patch_edit", args: map[string]any{"action": "rename", "pos": pos, "new_name": "FakeRenamed"}},
	}
	runBinaryAllLanguageToolChecks(t, client, tc.languageID, checks)
	if os.Getenv("MCP_LSP_TRACE_TIMING") == "1" {
		t.Logf("language=%s sidecar timing log:\n%s", tc.languageID, client.stderrString())
	}
}

type binaryAllLanguageToolCheck struct {
	tool string
	args map[string]any
	want string
}

func runBinaryAllLanguageToolChecks(t *testing.T, client *mcpLSPBinaryClient, languageID string, checks []binaryAllLanguageToolCheck) {
	t.Helper()
	for _, check := range checks {
		startedAt := time.Now()
		result := client.callTool(t, check.tool, check.args)
		t.Logf("language=%s tool=%s action=%v elapsed=%s", languageID, check.tool, check.args["action"], time.Since(startedAt))
		requireMCPToolSuccess(t, client, result, languageID+" "+check.tool)
		payload := result.Result.ContentText() + string(result.Result.StructuredContent)
		if check.want != "" && !strings.Contains(payload, check.want) {
			t.Fatalf("%s %s payload missing %q: text=%q structured=%s stderr=%s", languageID, check.tool, check.want, result.Result.ContentText(), result.Result.StructuredContent, client.stderrString())
		}
	}
}

// TestMcpLSPBinaryGoAndGoSumSemanticActionsExposeLifecycleTiming_E2E isolates
// semantic requests so their elapsed time can be correlated with fake gopls
// process starts/stops and the sidecar owner logs.
func TestMcpLSPBinaryGoAndGoSumSemanticActionsExposeLifecycleTiming_E2E(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping focused Go semantic timing E2E in short mode")
	}
	binary := buildMcpLSPBinaryForTest(t)
	fakeServersBinDir := writeFakeMultilangDiagnosticsLangservers(t)
	cases := []binaryColdStartLanguageCase{
		{languageID: "go", write: writeBinaryColdStartGoFixture},
		{languageID: "gosum", write: writeBinaryColdStartGoSumFixture},
	}
	for _, tc := range cases {
		t.Run(tc.languageID, func(t *testing.T) {
			root := t.TempDir()
			target := tc.write(t, root)
			journalPath := filepath.Join(t.TempDir(), "fake-gopls-lifecycle.jsonl")
			ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
			defer cancel()
			client := startMcpLSPBinaryForTestWithEnv(t, ctx, binary, root, fakeServersBinDir, []string{
				fakeMultilangLifecycleJournalEnv + "=" + journalPath,
			})
			defer client.close(t)
			client.call(t, "initialize", map[string]any{"protocolVersion": "2024-11-05"})
			pos := target + ":1:1"
			checks := []struct {
				name string
				tool string
				args map[string]any
			}{
				{name: "document_symbol", tool: "structure", args: map[string]any{"action": "document_symbol", "file_path": target}},
				{name: "hover", tool: "inspect", args: map[string]any{"action": "hover", "pos": pos}},
				{name: "definition", tool: "inspect", args: map[string]any{"action": "definition", "pos": pos}},
				{name: "references", tool: "xref", args: map[string]any{"action": "references", "pos": pos}},
			}
			for _, check := range checks {
				started := time.Now()
				result := client.callTool(t, check.tool, check.args)
				elapsed := time.Since(started)
				journal, _ := os.ReadFile(journalPath)
				stderr := strings.TrimSpace(client.stderrString())
				t.Logf("language=%s action=%s elapsed=%s sidecar_pid=%d fake_gopls_lifecycle=%s sidecar_stderr=%s",
					tc.languageID, check.name, elapsed, client.cmd.Process.Pid, strings.TrimSpace(string(journal)), stderr)
				if result.Result.IsError {
					t.Fatalf("%s %s returned MCP error: text=%q structured=%s stderr=%s",
						tc.languageID, check.name, result.Result.ContentText(), result.Result.StructuredContent, client.stderrString())
				}
			}
		})
	}
}

func TestMcpLSPBinaryDiagnosticsReopensChangedFileBeforeReturning_E2E(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping mcp-lsp binary e2e test in short mode")
	}

	binary := buildMcpLSPBinaryForTest(t)
	fakeServersBinDir := writeFakeMultilangDiagnosticsLangservers(t)
	root := t.TempDir()
	writeBinaryColdStartFile(t, root, "package.json", `{"name":"diagnostics-reopen"}`)
	target := writeBinaryColdStartFile(t, root, "app.js", "function staleName() { return 1 }\n")

	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	client := startMcpLSPBinaryForTestWithEnv(t, ctx, binary, root, fakeServersBinDir, nil)
	defer client.close(t)
	client.call(t, "initialize", map[string]any{"protocolVersion": "2024-11-05"})

	first := client.callTool(t, "file", map[string]any{
		"action":    "diagnostics",
		"file_path": target,
	})
	requireMCPToolSuccess(t, client, first, "initial stale-name diagnostics")
	firstMessage := decodeDiagnosticsStructuredContent(t, first.Result.StructuredContent).FirstMessageForFile(t, target)
	if !strings.Contains(firstMessage, "staleName") {
		t.Fatalf("initial diagnostics message = %q, want staleName; stderr=%s", firstMessage, client.stderrString())
	}

	if err := os.WriteFile(target, []byte("function freshName() { return 2 }\n"), 0o600); err != nil {
		t.Fatalf("rewrite diagnostics target: %v", err)
	}
	second := client.callTool(t, "file", map[string]any{
		"action":    "diagnostics",
		"file_path": target,
	})
	requireMCPToolSuccess(t, client, second, "fresh-name diagnostics after rewrite")
	secondMessage := decodeDiagnosticsStructuredContent(t, second.Result.StructuredContent).FirstMessageForFile(t, target)
	if !strings.Contains(secondMessage, "freshName") || strings.Contains(secondMessage, "staleName") {
		t.Fatalf("diagnostics after rewrite = %q, want freshName without staleName; stderr=%s", secondMessage, client.stderrString())
	}
}

// super-dolphin-ci: helper
func TestFakeMultilangDiagnosticsLangserverHelper(t *testing.T) {
	if os.Getenv(fakeMultilangDiagnosticsEnv) != "1" {
		return
	}
	runFakeMultilangDiagnosticsLangserver()
	os.Exit(0)
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
		"bash-language-server",
		"buf",
		"clangd",
		"csharp-ls",
		"dart",
		"docker-langserver",
		"graphql-lsp",
		"gopls",
		"intelephense",
		"jdtls",
		"kotlin-language-server",
		"lua-language-server",
		"pyright-langserver",
		"prisma-language-server",
		"rust-analyzer",
		"shellcheck",
		"sqruff",
		"sourcekit-lsp",
		"solargraph",
		"svelteserver",
		"terraform-ls",
		"typescript-language-server",
		"vscode-css-language-server",
		"vscode-html-language-server",
		"vscode-json-language-server",
		"vscode-markdown-language-server",
		"vue-language-server",
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

func writeFakeProtocolBundle(t *testing.T, fakeServersBinDir, serverName, languageID string) string {
	t.Helper()
	bundleDir := t.TempDir()
	bundleBinDir := filepath.Join(bundleDir, "bin")
	if err := os.MkdirAll(bundleBinDir, 0o755); err != nil {
		t.Fatalf("create fake SQL protocol bundle bin dir: %v", err)
	}
	fakeServer, err := os.ReadFile(filepath.Join(fakeServersBinDir, serverName))
	if err != nil {
		t.Fatalf("read fake %s server: %v", serverName, err)
	}
	if err := os.WriteFile(filepath.Join(bundleBinDir, serverName), fakeServer, 0o700); err != nil {
		t.Fatalf("write fake %s protocol bundle server: %v", serverName, err)
	}
	manifest := []byte(fmt.Sprintf("{\n  \"servers\": {\n    %q: {\"path\": %q, \"languages\": [%q]}\n  }\n}\n",
		serverName, "bin/"+serverName, languageID))
	if err := os.WriteFile(filepath.Join(bundleDir, "manifest.json"), manifest, 0o644); err != nil {
		t.Fatalf("write fake SQL protocol bundle manifest: %v", err)
	}
	return bundleDir
}

func writeFakeAllLanguagesProtocolBundle(t *testing.T, fakeServersBinDir string) string {
	t.Helper()
	servers := map[string][]string{
		"vscode-css-language-server":      {"css"},
		"clangd":                          {"c", "cpp", "objective-c", "objective-cpp"},
		"csharp-ls":                       {"csharp"},
		"dart":                            {"dart"},
		"docker-langserver":               {"dockerfile"},
		"gopls":                           {"go", "gomod", "gosum", "gowork"},
		"graphql-lsp":                     {"graphql"},
		"vscode-html-language-server":     {"html"},
		"jdtls":                           {"java"},
		"typescript-language-server":      {"javascript", "javascriptreact", "typescript", "typescriptreact"},
		"vscode-json-language-server":     {"json"},
		"kotlin-language-server":          {"kotlin"},
		"lua-language-server":             {"lua"},
		"vscode-markdown-language-server": {"markdown"},
		"intelephense":                    {"php"},
		"prisma-language-server":          {"prisma"},
		"buf":                             {"proto"},
		"pyright-langserver":              {"python"},
		"solargraph":                      {"ruby"},
		"rust-analyzer":                   {"rust"},
		"bash-language-server":            {"shellscript"},
		"sqruff":                          {"sql"},
		"svelteserver":                    {"svelte"},
		"sourcekit-lsp":                   {"swift"},
		"terraform-ls":                    {"terraform"},
		"vue-language-server":             {"vue"},
		"yaml-language-server":            {"yaml"},
	}
	bundleDir := t.TempDir()
	bundleBinDir := filepath.Join(bundleDir, "bin")
	if err := os.MkdirAll(bundleBinDir, 0o755); err != nil {
		t.Fatalf("create all-language fake bundle: %v", err)
	}
	manifestServers := make(map[string]any, len(servers))
	for serverName, languages := range servers {
		payload, err := os.ReadFile(filepath.Join(fakeServersBinDir, serverName))
		if err != nil {
			t.Fatalf("read fake bundled %s: %v", serverName, err)
		}
		if err := os.WriteFile(filepath.Join(bundleBinDir, serverName), payload, 0o700); err != nil {
			t.Fatalf("write fake bundled %s: %v", serverName, err)
		}
		manifestServers[serverName] = map[string]any{"path": "bin/" + serverName, "languages": languages}
	}
	manifest, err := json.MarshalIndent(map[string]any{"servers": manifestServers}, "", "  ")
	if err != nil {
		t.Fatalf("marshal all-language fake bundle manifest: %v", err)
	}
	if err := os.WriteFile(filepath.Join(bundleDir, "manifest.json"), append(manifest, '\n'), 0o644); err != nil {
		t.Fatalf("write all-language fake bundle manifest: %v", err)
	}
	return bundleDir
}
