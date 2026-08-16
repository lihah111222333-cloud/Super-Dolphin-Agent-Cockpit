//go:build e2e

package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type patchEditResidualFixture struct {
	client               *mcpLSPBinaryClient
	goTarget, textTarget string
	oldBulk, newBulk     string
	detailOld, detailNew string
}

// TestMcpLSPBinaryPatchEditResidualContracts_E2E 锁定紧凑回执、参数错误、空 action 提示和格式化无变化文案。
func TestMcpLSPBinaryPatchEditResidualContracts_E2E(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping mcp-lsp binary e2e test in short mode")
	}
	requireHostBinariesForE2E(t, []realLSPDiagnosticsCase{{languageID: "go", binaries: []string{"gopls"}}})
	fixture := newPatchEditResidualFixture(t)
	defer fixture.client.close(t)
	t.Run("default compact receipt", func(t *testing.T) { assertDefaultCompactPatchReceipt(t, fixture) })
	t.Run("full detail is structured only", func(t *testing.T) { assertFullPatchDetailStructuredOnly(t, fixture) })
	t.Run("invalid rename is invalid params", func(t *testing.T) { assertInvalidRenameContract(t, fixture) })
	t.Run("empty quickfix gives executable retry", func(t *testing.T) { assertEmptyQuickfixRetry(t, fixture) })
	t.Run("format no change is action specific", func(t *testing.T) { assertFormatNoChangeText(t, fixture) })
}

func newPatchEditResidualFixture(t *testing.T) patchEditResidualFixture {
	t.Helper()
	root := t.TempDir()
	writeBinaryColdStartFile(t, root, "go.mod", "module example.test/patcheditcontract\n\ngo 1.26.0\n")
	goTarget := writeBinaryColdStartFile(t, root, "main.go", "package main\n\nfunc stableName() int { return 1 }\n\nfunc main() { _ = stableName() }\n")
	oldBulk := "old-" + strings.Repeat("x", 320)
	newBulk := "new-" + strings.Repeat("y", 320)
	detailOld := "detail-old-" + strings.Repeat("a", 160)
	detailNew := "detail-new-" + strings.Repeat("b", 160)
	textTarget := writeBinaryColdStartFile(t, root, "notes.txt", oldBulk+"\n"+detailOld+"\n")

	binary := buildMcpLSPBinaryForTest(t)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	client := startMcpLSPBinaryForTestWithEnv(t, ctx, binary, root, t.TempDir(), []string{
		"AGENT_LSP_SHARED_CACHE_DIR=" + filepath.Join(t.TempDir(), "lsp-cache"),
	})
	t.Cleanup(cancel)
	client.call(t, "initialize", map[string]any{"protocolVersion": "2024-11-05"})
	return patchEditResidualFixture{client: client, goTarget: goTarget, textTarget: textTarget,
		oldBulk: oldBulk, newBulk: newBulk, detailOld: detailOld, detailNew: detailNew}
}

func assertDefaultCompactPatchReceipt(t *testing.T, f patchEditResidualFixture) {
	t.Helper()
	result := f.client.callTool(t, "patch_edit", map[string]any{
		"action":    "replace_range",
		"file_path": f.textTarget,
		"patch":     "@@\n-" + f.oldBulk + "\n+" + f.newBulk + "\n",
	})
	requireMCPToolSuccess(t, f.client, result, "compact patch_edit")
	text := result.Result.ContentText()
	structured := string(result.Result.StructuredContent)
	if size := len(text) + len(structured); size > 1400 {
		t.Fatalf("compact patch_edit response = %d bytes, want <= 1400; text=%q structured=%s", size, text, structured)
	} else {
		t.Logf("compact patch_edit response bytes: text=%d structured=%d total=%d", len(text), len(structured), size)
	}
	for _, forbidden := range []string{f.oldBulk, f.newBulk} {
		if strings.Contains(text, forbidden) || strings.Contains(structured, forbidden) {
			t.Fatalf("default patch_edit duplicated full edit content %q; text=%q structured=%s", forbidden[:16], text, structured)
		}
	}
	for _, required := range []string{"\"replaced_len\"", "\"replacement_len\"", "\"affected_start_line\""} {
		if !strings.Contains(structured, required) {
			t.Fatalf("compact patch_edit structured receipt missing %s: %s", required, structured)
		}
	}
}

func assertFullPatchDetailStructuredOnly(t *testing.T, f patchEditResidualFixture) {
	t.Helper()
	result := f.client.callTool(t, "patch_edit", map[string]any{
		"action":          "replace_range",
		"file_path":       f.textTarget,
		"patch":           "@@\n-" + f.detailOld + "\n+" + f.detailNew + "\n",
		"response_detail": "full",
	})
	requireMCPToolSuccess(t, f.client, result, "full patch_edit")
	text := result.Result.ContentText()
	structured := string(result.Result.StructuredContent)
	for _, required := range []string{f.detailOld, f.detailNew} {
		if !strings.Contains(structured, required) {
			t.Fatalf("full patch_edit structured result missing requested detail %q: %s", required[:16], structured)
		}
		if strings.Contains(text, required) {
			t.Fatalf("full patch_edit duplicated structured detail in text %q: %q", required[:16], text)
		}
	}
}

func assertInvalidRenameContract(t *testing.T, f patchEditResidualFixture) {
	t.Helper()
	diagnostics := f.client.callTool(t, "file", map[string]any{
		"action":      "diagnostics",
		"file_path":   f.goTarget,
		"language_id": "go",
	})
	requireMCPToolSuccess(t, f.client, diagnostics, "warm gopls diagnostics")
	before, err := os.ReadFile(f.goTarget)
	if err != nil {
		t.Fatalf("read rename fixture before invalid rename: %v", err)
	}
	result := f.client.callTool(t, "patch_edit", map[string]any{
		"action":      "rename",
		"pos":         f.goTarget + ":3:8",
		"new_name":    "123-invalid",
		"language_id": "go",
	})
	if !result.Result.IsError {
		t.Fatalf("invalid rename returned success; text=%q structured=%s", result.Result.ContentText(), result.Result.StructuredContent)
	}
	if code := toolErrorCode(result.Result.StructuredContent); code != "invalid_params" {
		t.Fatalf("invalid rename code = %q, want invalid_params; text=%q structured=%s", code, result.Result.ContentText(), result.Result.StructuredContent)
	}
	after, err := os.ReadFile(f.goTarget)
	if err != nil {
		t.Fatalf("read rename fixture after invalid rename: %v", err)
	}
	if string(after) != string(before) {
		t.Fatalf("invalid rename changed file\nbefore=%q\nafter=%q", before, after)
	}
}

func assertEmptyQuickfixRetry(t *testing.T, f patchEditResidualFixture) {
	t.Helper()
	result := f.client.callTool(t, "patch_edit", map[string]any{
		"action":      "code_action",
		"pos":         f.goTarget + ":3:8",
		"language_id": "go",
		"only":        []string{"quickfix"},
	})
	requireMCPToolSuccess(t, f.client, result, "empty quickfix")
	combined := strings.ToLower(result.Result.ContentText() + "\n" + string(result.Result.StructuredContent))
	for _, required := range []string{"no code actions found", "retry", "without only"} {
		if !strings.Contains(combined, required) {
			t.Fatalf("empty quickfix response missing %q; text=%q structured=%s", required, result.Result.ContentText(), result.Result.StructuredContent)
		}
	}
}

func assertFormatNoChangeText(t *testing.T, f patchEditResidualFixture) {
	t.Helper()
	result := f.client.callTool(t, "patch_edit", map[string]any{
		"action":      "format",
		"file_path":   f.goTarget,
		"language_id": "go",
	})
	requireMCPToolSuccess(t, f.client, result, "format no change")
	text := strings.ToLower(result.Result.ContentText())
	if !strings.Contains(text, "no_change") || !strings.Contains(text, "format returned no changes") {
		t.Fatalf("format no-change text is not action specific: %q", result.Result.ContentText())
	}
	if strings.Contains(text, "patch matched") {
		t.Fatalf("format no-change text contains replace_range wording: %q", result.Result.ContentText())
	}
}

type realPatchEditLanguageCase struct {
	languageID string
	binaries   []string
	files      map[string]string
	target     string
	pos        string
	oldName    string
	newName    string
}

// TestMcpLSPBinaryPatchEditRealCommonLanguages_E2E 用真实语言服务器验证五种常用语言的语义重命名写入和同步。
func TestMcpLSPBinaryPatchEditRealCommonLanguages_E2E(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping mcp-lsp binary real-language e2e test in short mode")
	}
	cases := realPatchEditLanguageCases()
	requirements := make([]realLSPDiagnosticsCase, 0, len(cases))
	for _, tc := range cases {
		requirements = append(requirements, realLSPDiagnosticsCase{languageID: tc.languageID, binaries: tc.binaries})
	}
	requireHostBinariesForE2E(t, requirements)
	binary := buildMcpLSPBinaryForTest(t)
	for _, tc := range cases {
		t.Run(tc.languageID, func(t *testing.T) { runRealPatchEditLanguage(t, binary, tc) })
	}
}

func realPatchEditLanguageCases() []realPatchEditLanguageCase {
	return []realPatchEditLanguageCase{
		{languageID: "go", binaries: []string{"gopls"}, files: map[string]string{
			"go.mod":  "module example.test/renamego\n\ngo 1.26.0\n",
			"main.go": "package main\n\nfunc sharedName() int { return 1 }\n\nfunc main() { _ = sharedName() }\n",
		}, target: "main.go", pos: "3:8", oldName: "sharedName", newName: "renamedGo"},
		{languageID: "typescript", binaries: []string{"typescript-language-server"}, files: map[string]string{
			"package.json":  `{\"private\":true,\"devDependencies\":{\"typescript\":\"*\"}}`,
			"tsconfig.json": `{\"compilerOptions\":{\"strict\":true},\"include\":[\"*.ts\"]}`,
			"main.ts":       "export function sharedName(): number { return 1; }\nexport const value = sharedName();\n",
		}, target: "main.ts", pos: "1:20", oldName: "sharedName", newName: "renamedTypeScript"},
		{languageID: "python", binaries: []string{"pyright-langserver"}, files: map[string]string{
			"pyproject.toml": "[project]\nname = \"rename-python\"\nversion = \"0.0.0\"\n",
			"main.py":        "def shared_name():\n    return 1\n\nvalue = shared_name()\n",
		}, target: "main.py", pos: "1:6", oldName: "shared_name", newName: "renamed_python"},
		{languageID: "rust", binaries: []string{"rust-analyzer"}, files: map[string]string{
			"Cargo.toml":  "[package]\nname = \"rename-rust\"\nversion = \"0.1.0\"\nedition = \"2024\"\n",
			"src/main.rs": "fn main() {\n    let shared_value = 1;\n    let value = shared_value;\n    println!(\"{}\", value);\n}\n",
		}, target: "src/main.rs", pos: "2:10", oldName: "shared_value", newName: "renamed_rust"},
		{languageID: "java", binaries: []string{"jdtls"}, files: map[string]string{
			"Main.java": "public class Main {\n    static int sharedName() { return 1; }\n    public static void main(String[] args) { System.out.println(sharedName()); }\n}\n",
		}, target: "Main.java", pos: "2:18", oldName: "sharedName", newName: "renamedJava"},
	}
}

func runRealPatchEditLanguage(t *testing.T, binary string, tc realPatchEditLanguageCase) {
	t.Helper()
	root := t.TempDir()
	for name, content := range tc.files {
		writeBinaryColdStartFile(t, root, name, content)
	}
	target := filepath.Join(root, filepath.FromSlash(tc.target))
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	client := startMcpLSPBinaryForTestWithEnv(t, ctx, binary, root, t.TempDir(), []string{
		"AGENT_LSP_SHARED_CACHE_DIR=" + filepath.Join(t.TempDir(), "lsp-cache"),
	})
	defer client.close(t)
	client.call(t, "initialize", map[string]any{"protocolVersion": "2024-11-05"})

	warm := client.callTool(t, "file", map[string]any{
		"action":      "diagnostics",
		"file_path":   target,
		"language_id": tc.languageID,
	})
	requireMCPToolSuccess(t, client, warm, tc.languageID+" warm diagnostics")
	warmPatchEditRenameSymbol(t, client, target, tc)
	if tc.languageID == "rust" {
		waitForPatchEditRenameReferences(t, client, target+":"+tc.pos, tc.languageID)
	}
	rename := client.callTool(t, "patch_edit", map[string]any{
		"action":      "rename",
		"pos":         target + ":" + tc.pos,
		"new_name":    tc.newName,
		"language_id": tc.languageID,
	})
	requireMCPToolSuccess(t, client, rename, tc.languageID+" rename")
	raw, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("read %s renamed fixture: %v", tc.languageID, err)
	}
	content := string(raw)
	if strings.Contains(content, tc.oldName) || strings.Count(content, tc.newName) != 2 {
		t.Fatalf("%s semantic rename did not update definition and reference: %q", tc.languageID, content)
	}
	post := client.callTool(t, "file", map[string]any{
		"action":      "diagnostics",
		"file_path":   target,
		"language_id": tc.languageID,
	})
	requireMCPToolSuccess(t, client, post, tc.languageID+" post-rename diagnostics")
	if !strings.Contains(post.Result.ContentText(), "No diagnostics found") {
		t.Fatalf("%s post-rename diagnostics are not zero: text=%q structured=%s", tc.languageID, post.Result.ContentText(), post.Result.StructuredContent)
	}
}

func warmPatchEditRenameSymbol(t *testing.T, client *mcpLSPBinaryClient, target string, tc realPatchEditLanguageCase) {
	t.Helper()
	if tc.languageID == "java" || tc.languageID == "rust" {
		return
	}
	args := map[string]any{
		"action":      "document_symbol",
		"file_path":   target,
		"language_id": tc.languageID,
		"max_results": 10,
	}
	result := client.callTool(t, "structure", args)
	requireMCPToolSuccess(t, client, result, tc.languageID+" rename symbol readiness")
	requireToolResultContains(t, result, tc.oldName, tc.languageID+" rename symbol readiness")
}

func waitForPatchEditRenameReferences(t *testing.T, client *mcpLSPBinaryClient, pos string, languageID string) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for {
		result := client.callTool(t, "xref", map[string]any{
			"action":              "references",
			"pos":                 pos,
			"language_id":         languageID,
			"include_declaration": true,
			"max_results":         10,
		})
		if !result.Result.IsError && structuredResultTotal(result.Result.StructuredContent) > 0 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s rename references did not become ready at %s; text=%q structured=%s", languageID, pos, result.Result.ContentText(), result.Result.StructuredContent)
		}
		time.Sleep(250 * time.Millisecond)
	}
}

func structuredResultTotal(raw json.RawMessage) int {
	var payload struct {
		Total int `json:"total"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return 0
	}
	return payload.Total
}

func toolErrorCode(raw json.RawMessage) string {
	var payload struct {
		Code string `json:"code"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return ""
	}
	return payload.Code
}
