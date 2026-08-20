//go:build e2e && (darwin || linux || windows)

package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const allLanguageToolMatrixTimeout = 30 * time.Minute

func TestFakeAllLanguagesProtocolBundleWritesGoplsManifest_E2E(t *testing.T) {
	fakeServers := writeFakeMultilangDiagnosticsLangservers(t)
	bundleDir := writeFakeAllLanguagesProtocolBundle(t, fakeServers)
	manifest, err := os.ReadFile(filepath.Join(bundleDir, "manifest.json"))
	if err != nil {
		t.Fatalf("read all-language fake bundle manifest: %v", err)
	}
	var payload struct {
		SchemaVersion int    `json:"schema_version"`
		BundlePath    string `json:"bundle_path"`
		Profile       string `json:"profile"`
		Servers       map[string]struct {
			Path      string   `json:"path"`
			Version   string   `json:"version"`
			SHA256    string   `json:"sha256"`
			Languages []string `json:"languages"`
		} `json:"servers"`
	}
	if err := json.Unmarshal(manifest, &payload); err != nil {
		t.Fatalf("decode all-language fake bundle manifest: %v", err)
	}
	if payload.SchemaVersion != 1 || payload.BundlePath != "lsp" || payload.Profile != "standard" {
		t.Fatalf("all-language fake bundle manifest envelope = schema=%d bundle_path=%q profile=%q, want schema=1 bundle_path=%q profile=%q",
			payload.SchemaVersion, payload.BundlePath, payload.Profile, "lsp", "standard")
	}
	wantLanguages := map[string]struct{}{
		"c": {}, "cpp": {}, "css": {}, "csharp": {}, "dart": {}, "dockerfile": {},
		"go": {}, "gomod": {}, "gosum": {}, "gowork": {}, "graphql": {}, "html": {}, "java": {},
		"javascript": {}, "javascriptreact": {}, "json": {}, "kotlin": {}, "lua": {}, "markdown": {},
		"mql": {}, "mql4": {}, "mql5": {}, "mq4": {}, "mq5": {}, "mqh": {}, "objective-c": {},
		"objective-cpp": {}, "php": {}, "prisma": {}, "proto": {}, "python": {}, "ruby": {},
		"rust": {}, "shellscript": {}, "sql": {}, "svelte": {}, "swift": {}, "terraform": {},
		"typescript": {}, "typescriptreact": {}, "vue": {}, "yaml": {},
	}
	seenLanguages := make(map[string]struct{}, len(wantLanguages))
	for serverName, server := range payload.Servers {
		if server.Path == "" || server.Version == "" || server.SHA256 == "" {
			t.Fatalf("all-language fake bundle server %q has incomplete metadata: %#v", serverName, server)
		}
		serverPayload, err := os.ReadFile(filepath.Join(bundleDir, filepath.FromSlash(server.Path)))
		if err != nil {
			t.Fatalf("read all-language fake bundle server %q from %q: %v", serverName, server.Path, err)
		}
		digest := sha256.Sum256(serverPayload)
		wantSHA := hex.EncodeToString(digest[:])
		if server.SHA256 != wantSHA {
			t.Fatalf("all-language fake bundle server %q sha256 = %q, want payload sha256 %q", serverName, server.SHA256, wantSHA)
		}
		for _, languageID := range server.Languages {
			if _, ok := wantLanguages[languageID]; !ok {
				t.Fatalf("all-language fake bundle server %q declares unexpected language %q", serverName, languageID)
			}
			if _, duplicate := seenLanguages[languageID]; duplicate {
				t.Fatalf("all-language fake bundle language %q is declared by multiple servers", languageID)
			}
			seenLanguages[languageID] = struct{}{}
		}
	}
	if len(seenLanguages) != len(wantLanguages) {
		t.Fatalf("all-language fake bundle language closure = %d, want %d; seen=%v", len(seenLanguages), len(wantLanguages), seenLanguages)
	}
	clangd, ok := payload.Servers["clangd"]
	if !ok {
		t.Fatalf("all-language fake bundle manifest lacks clangd: %s", manifest)
	}
	for _, languageID := range []string{"mql", "mql4", "mql5", "mq4", "mq5", "mqh"} {
		found := false
		for _, declaredLanguage := range clangd.Languages {
			if declaredLanguage == languageID {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("all-language fake clangd manifest lacks language %q: %#v", languageID, clangd.Languages)
		}
	}
}

func TestAllLanguageToolMatrixTimeoutExceedsTenMinutes(t *testing.T) {
	if allLanguageToolMatrixTimeout <= 10*time.Minute {
		t.Fatalf("all-language tool matrix timeout = %s, want greater than 10 minutes", allLanguageToolMatrixTimeout)
	}
}

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
			client := startFakeMultilangDiagnosticsClientForTest(t, ctx, binary, root, fakeServersBinDir, []string{
				fakeMultilangDiagnosticDelayEnv + "=" + binaryColdStartDiagnosticsDelay.String(),
			}, tc.languageID)
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
				payload := decodeDiagnosticsContentText(t, diagnostics.Result.ContentText())
				if payload.Total != 0 || payload.HasFile(target) {
					t.Fatalf("valid SQLite fake-server diagnostics = %#v, want no diagnostics before real parser tests", payload)
				}
				return
			}
			if elapsed < binaryColdStartDiagnosticsDelay-binaryColdStartDiagnosticsSlack {
				t.Fatalf("%s diagnostics returned in %s, want it to wait for delayed cold-start diagnostics >= %s; text=%q stderr=%s",
					tc.languageID, elapsed, binaryColdStartDiagnosticsDelay-binaryColdStartDiagnosticsSlack,
					diagnostics.Result.ContentText(), client.stderrString())
			}

			payload := decodeDiagnosticsContentText(t, diagnostics.Result.ContentText())
			if !payload.HasFile(target) {
				t.Fatalf("%s diagnostics missing target %s: payload=%#v text=%q stderr=%s",
					tc.languageID, target, payload, diagnostics.Result.ContentText(), client.stderrString())
			}
			message := payload.FirstMessageForFile(t, target)
			want := "fake cold-start diagnostic for " + tc.languageID
			if !strings.Contains(message, want) {
				t.Fatalf("%s diagnostics message = %q, want %q; payload=%#v text=%q stderr=%s",
					tc.languageID, message, want, payload, diagnostics.Result.ContentText(), client.stderrString())
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
	binary := buildMcpLSPBinaryForTestWithTags(t, "mcp_lsp_short_idle_precheck")
	fakeServersBinDir := writeFakeMultilangDiagnosticsLangservers(t)
	for _, tc := range binaryColdStartLanguageCases(t) {
		t.Run(tc.languageID, func(t *testing.T) {
			runMcpLSPBinaryAllToolActionsForLanguageE2E(t, binary, fakeServersBinDir, tc)
		})
	}
}

// fakeProtocolServerForLanguage 将语言 ID 固定映射到 fake LSP server，平台文件只负责构造 bundle。
func fakeProtocolServerForLanguage(languageID string) (string, bool) {
	switch languageID {
	case "c", "cpp", "objective-c", "objective-cpp", "mql", "mql4", "mql5", "mq4", "mq5", "mqh":
		return "clangd", true
	case "go", "gomod", "gosum", "gowork":
		return "gopls", true
	case "javascript", "javascriptreact", "typescript", "typescriptreact":
		return "typescript-language-server", true
	case "css":
		return "vscode-css-language-server", true
	case "csharp":
		return "csharp-ls", true
	case "dart":
		return "dart", true
	case "dockerfile":
		return "docker-langserver", true
	case "graphql":
		return "graphql-lsp", true
	case "html":
		return "vscode-html-language-server", true
	case "java":
		return "jdtls", true
	case "json":
		return "vscode-json-language-server", true
	case "kotlin":
		return "kotlin-language-server", true
	case "lua":
		return "lua-language-server", true
	case "markdown":
		return "vscode-markdown-language-server", true
	case "php":
		return "intelephense", true
	case "prisma":
		return "prisma-language-server", true
	case "proto":
		return "buf", true
	case "python":
		return "pyright-langserver", true
	case "ruby":
		return "solargraph", true
	case "rust":
		return "rust-analyzer", true
	case "shellscript":
		return "bash-language-server", true
	case "sql":
		return "sqruff", true
	case "svelte":
		return "svelteserver", true
	case "swift":
		return "sourcekit-lsp", true
	case "terraform":
		return "terraform-ls", true
	case "vue":
		return "vue-language-server", true
	case "yaml":
		return "yaml-language-server", true
	default:
		return "", false
	}
}

func runMcpLSPBinaryAllToolActionsForLanguageE2E(t *testing.T, binary, fakeServersBinDir string, tc binaryColdStartLanguageCase) {
	root := t.TempDir()
	target := tc.write(t, root)
	ctx, cancel := context.WithTimeout(context.Background(), allLanguageToolMatrixTimeout)
	defer cancel()
	journalPath := filepath.Join(t.TempDir(), "fake-multilang-lifecycle.jsonl")
	extraEnv := []string{
		"AGENT_LSP_SHARED_CACHE_DIR=" + filepath.Join(t.TempDir(), "lsp-cache"),
		fakeMultilangLifecycleJournalEnv + "=" + journalPath,
	}
	serverName, ok := fakeProtocolServerForLanguage(tc.languageID)
	if !ok {
		t.Fatalf("missing fake protocol bundle mapping for language %q", tc.languageID)
	}
	// 真实 managed server 的安装与能力由独立 E2E 校验；此测试只锁定
	// mcp-lsp 的协议路由，因此每个语言都显式绑定对应 fake bundle，禁止下载或 PATH 抢占。
	fakeBundle := writeFakeProtocolBundle(t, binary, fakeServersBinDir, serverName, tc.languageID)
	extraEnv = append(extraEnv,
		"SUPER_DOLPHIN_LSP_BUNDLE_DIR="+fakeBundle.bundleDir,
		"SUPER_DOLPHIN_LSP_MANIFEST="+fakeBundle.manifestPath,
	)
	extraEnv = append(extraEnv, fakeBundle.extraEnv...)
	client := startFakeProtocolBundleClientForTest(t, ctx, fakeBundle, root, fakeServersBinDir, extraEnv, serverName)
	defer client.close(t)
	client.call(t, "initialize", map[string]any{"protocolVersion": "2024-11-05"})
	warmWorkspaceSymbolDocument(t, client, target, tc.languageID)
	current, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("read %s replace_range fixture: %v", tc.languageID, err)
	}
	var editableLine string
	for line := range strings.SplitSeq(strings.ReplaceAll(string(current), "\r\n", "\n"), "\n") {
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
	if payload := replaced.Result.ContentText(); !strings.Contains(payload, "applied") {
		t.Fatalf("%s replace_range did not report applied: text=%q stderr=%s",
			tc.languageID, replaced.Result.ContentText(), client.stderrString())
	}

	batchName := "batch-" + filepath.Base(target)
	switch tc.languageID {
	case "dockerfile", "gomod", "gosum", "gowork":
		batchName = "batch." + tc.languageID
	}
	batchTarget := filepath.Join(root, "batch", batchName)
	if err := os.MkdirAll(filepath.Dir(batchTarget), 0o700); err != nil {
		t.Fatalf("create %s batch diagnostics directory: %v", tc.languageID, err)
	}
	if err := os.WriteFile(batchTarget, current, 0o600); err != nil {
		t.Fatalf("write %s batch diagnostics fixture: %v", tc.languageID, err)
	}

	pos := target + ":1:1"
	// ast_search is a workspace capability, so this per-language matrix does not vary its AST semantics.
	batchDiagnostics := client.callTool(t, "file", map[string]any{
		"action":      "diagnostics",
		"file_paths":  []string{target, batchTarget},
		"language_id": tc.languageID,
		"limit":       100,
	})
	requireMCPToolSuccess(t, client, batchDiagnostics, tc.languageID+" file diagnostics batch")
	batchPayload := decodeDiagnosticsContentText(t, batchDiagnostics.Result.ContentText())
	if !hasDiagnosticsFile(batchPayload, target) || !hasDiagnosticsFile(batchPayload, batchTarget) {
		journal, journalErr := os.ReadFile(journalPath)
		t.Fatalf("%s batch diagnostics must include both absolute targets %s and %s: payload=%#v text=%q journal_error=%v journal=%s stderr=%s",
			tc.languageID, target, batchTarget, batchPayload, batchDiagnostics.Result.ContentText(), journalErr, strings.TrimSpace(string(journal)), client.stderrString())
	}

	checks := []binaryAllLanguageToolCheck{
		{tool: "file", args: map[string]any{"action": "read_file", "file_path": target}},
		{tool: "file", args: map[string]any{"action": "read_file", "file_paths": []string{target}}, want: filepath.Base(target)},
		{tool: "file", args: map[string]any{"action": "diagnostics", "file_path": target}},
		{tool: "grep", args: map[string]any{"action": "text_search", "query": "fixture", "paths": []string{target}}},
		{tool: "structure", args: map[string]any{"action": "document_symbol", "file_path": target}},
		{tool: "structure", args: map[string]any{"action": "workspace_symbol", "file_path": target, "query": "StaleWorkspaceNeedle", "match_mode": "fuzzy"}, want: staleWorkspaceSymbolName(workspaceSymbolLanguageID(tc.languageID))},
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
		{tool: "xref", args: map[string]any{"action": "type_hierarchy", "pos": pos, "direction": "supertypes"}, want: "FakeSuperType"},
		{tool: "xref", args: map[string]any{"action": "type_hierarchy", "pos": pos, "direction": "subtypes"}, want: "FakeSubType"},
		{tool: "xref", args: map[string]any{"action": "type_hierarchy", "pos": pos, "direction": "both"}, wants: []string{"FakeSuperType", "FakeSubType"}},
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

// workspaceSymbolLanguageID 反映公共 MQL alias 在 clangd 文档生命周期中的规范语言 ID。
func workspaceSymbolLanguageID(languageID string) string {
	switch languageID {
	case "mql", "mql4", "mql5", "mq4", "mq5", "mqh":
		return "cpp"
	default:
		return languageID
	}
}

type binaryAllLanguageToolCheck struct {
	tool  string
	args  map[string]any
	want  string
	wants []string
}

func hasDiagnosticsFile(payload diagnosticsPayload, path string) bool {
	want := filepath.ToSlash(filepath.Clean(path))
	for _, table := range payload.Data {
		got := filepath.ToSlash(filepath.Clean(table.File))
		if strings.EqualFold(got, want) {
			return true
		}
	}
	return false
}

func runBinaryAllLanguageToolChecks(t *testing.T, client *mcpLSPBinaryClient, languageID string, checks []binaryAllLanguageToolCheck) {
	t.Helper()
	for _, check := range checks {
		if check.tool != "grep" && !(check.tool == "file" && check.args["action"] == "read_file") {
			// 该矩阵证明全部公共 language_id，而不是只证明同扩展名归一后的底层 adapter。
			check.args["language_id"] = languageID
		}
		startedAt := time.Now()
		result := client.callTool(t, check.tool, check.args)
		t.Logf("language=%s tool=%s action=%v elapsed=%s", languageID, check.tool, check.args["action"], time.Since(startedAt))
		requireMCPToolSuccess(t, client, result, languageID+" "+check.tool)
		payload := result.Result.ContentText()
		for _, want := range check.wants {
			if !strings.Contains(payload, want) {
				t.Fatalf("%s %s payload missing %q: text=%q stderr=%s", languageID, check.tool, want, result.Result.ContentText(), client.stderrString())
			}
		}
		if check.want != "" && !strings.Contains(payload, check.want) {
			t.Fatalf("%s %s payload missing %q: text=%q stderr=%s", languageID, check.tool, check.want, result.Result.ContentText(), client.stderrString())
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
			client := startFakeMultilangDiagnosticsClientForTest(t, ctx, binary, root, fakeServersBinDir, []string{
				fakeMultilangLifecycleJournalEnv + "=" + journalPath,
			}, tc.languageID)
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
					t.Fatalf("%s %s returned MCP error: text=%q stderr=%s",
						tc.languageID, check.name, result.Result.ContentText(), client.stderrString())
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
	client := startFakeMultilangDiagnosticsClientForTest(t, ctx, binary, root, fakeServersBinDir, nil, "javascript")
	defer client.close(t)
	client.call(t, "initialize", map[string]any{"protocolVersion": "2024-11-05"})

	first := client.callTool(t, "file", map[string]any{
		"action":    "diagnostics",
		"file_path": target,
	})
	requireMCPToolSuccess(t, client, first, "initial stale-name diagnostics")
	firstMessage := decodeDiagnosticsContentText(t, first.Result.ContentText()).FirstMessageForFile(t, target)
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
	secondMessage := decodeDiagnosticsContentText(t, second.Result.ContentText()).FirstMessageForFile(t, target)
	if !strings.Contains(secondMessage, "freshName") || strings.Contains(secondMessage, "staleName") {
		t.Fatalf("diagnostics after rewrite = %q, want freshName without staleName; stderr=%s", secondMessage, client.stderrString())
	}
}

func writeFakeAllLanguagesProtocolBundle(t *testing.T, fakeServersBinDir string, explicitBundleDir ...string) string {
	t.Helper()
	servers := map[string][]string{
		"vscode-css-language-server":      {"css"},
		"clangd":                          {"c", "cpp", "objective-c", "objective-cpp", "mql", "mql4", "mql5", "mq4", "mq5", "mqh"},
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
	bundleDir := ""
	if len(explicitBundleDir) > 1 {
		t.Fatalf("all-language fake bundle accepts at most one explicit destination")
	}
	if len(explicitBundleDir) == 1 {
		bundleDir = filepath.Clean(explicitBundleDir[0])
		if !filepath.IsAbs(bundleDir) {
			t.Fatalf("all-language fake bundle destination must be absolute: %q", bundleDir)
		}
		if err := os.MkdirAll(bundleDir, 0o700); err != nil {
			t.Fatalf("create explicit all-language fake bundle root: %v", err)
		}
	} else {
		bundleDir = t.TempDir()
	}
	bundleBinDir := filepath.Join(bundleDir, "bin")
	if err := os.MkdirAll(bundleBinDir, 0o755); err != nil {
		t.Fatalf("create all-language fake bundle: %v", err)
	}
	manifestServers := make(map[string]any, len(servers))
	for serverName, languages := range servers {
		executableName := allLanguageToolContractExecutableName(serverName)
		payload, err := os.ReadFile(filepath.Join(fakeServersBinDir, executableName))
		if err != nil {
			t.Fatalf("read fake bundled %s: %v", serverName, err)
		}
		if err := os.WriteFile(filepath.Join(bundleBinDir, executableName), payload, 0o700); err != nil {
			t.Fatalf("write fake bundled %s: %v", serverName, err)
		}
		digest := sha256.Sum256(payload)
		manifestServers[serverName] = map[string]any{
			"path":      "bin/" + executableName,
			"version":   "v24.12.0",
			"sha256":    hex.EncodeToString(digest[:]),
			"languages": languages,
		}
	}
	manifest, err := json.MarshalIndent(map[string]any{
		"schema_version": 1,
		"bundle_path":    "lsp",
		"profile":        "standard",
		"servers":        manifestServers,
	}, "", "  ")
	if err != nil {
		t.Fatalf("marshal all-language fake bundle manifest: %v", err)
	}
	if err := os.WriteFile(filepath.Join(bundleDir, "manifest.json"), append(manifest, '\n'), 0o644); err != nil {
		t.Fatalf("write all-language fake bundle manifest: %v", err)
	}
	return bundleDir
}
