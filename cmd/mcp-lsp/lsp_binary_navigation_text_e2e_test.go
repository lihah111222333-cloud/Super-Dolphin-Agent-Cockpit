//go:build e2e

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/mcpserver/common/lineprotocol"
)

const fakeGoplsManyDocumentSymbolsEnv = "MCP_LSP_FAKE_GOPLS_MANY_DOCUMENT_SYMBOLS"
const fakeGoplsWorkspaceSymbolsEnv = "MCP_LSP_FAKE_GOPLS_WORKSPACE_SYMBOLS"
const fakeGoplsWorkspaceSymbolTargetEnv = "MCP_LSP_FAKE_GOPLS_WORKSPACE_SYMBOL_TARGET"
const fakeGoplsWorkspaceSymbolSiblingEnv = "MCP_LSP_FAKE_GOPLS_WORKSPACE_SYMBOL_SIBLING"

// TestMcpLSPBinaryNavigationTextIsReadableAndBounded_E2E 经过真实 stdio MCP 边界锁定调用边文本和文档大纲预算。
func TestMcpLSPBinaryNavigationTextIsReadableAndBounded_E2E(t *testing.T) {
	root := t.TempDir()
	target := writeFakeGoplsGoFixture(t, root)
	binary := buildMcpLSPBinaryForTest(t)
	fakeGoplsBinDir := writeFakeGoplsShutdownWarningLangserver(t)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	client := startMcpLSPBinaryForTestWithEnv(t, ctx, binary, root, fakeGoplsBinDir, []string{
		fakeGoplsManyDocumentSymbolsEnv + "=1",
		"AGENT_LSP_SHARED_CACHE_DIR=" + filepath.Join(t.TempDir(), "lsp-cache"),
	})
	defer client.close(t)
	client.call(t, "initialize", map[string]any{"protocolVersion": "2024-11-05"})

	t.Run("call hierarchy edges", func(t *testing.T) { requireCallHierarchyTextE2E(t, client, target) })
	t.Run("document symbol text and budget", func(t *testing.T) { requireDocumentSymbolTextAndBudgetE2E(t, client, target) })
}

func requireCallHierarchyTextE2E(t *testing.T, client *mcpLSPBinaryClient, target string) {
	t.Helper()
	hierarchy, err := callMCPToolForScopedE2E(client, "xref", map[string]any{
		"action":      "call_hierarchy",
		"pos":         target + ":3:6",
		"language_id": "go",
		"direction":   "both",
	}, client.cmd.Dir, []string{client.cmd.Dir})
	if err != nil {
		t.Fatalf("call scoped call hierarchy: %v", err)
	}
	requireMCPToolSuccess(t, client, hierarchy, "call hierarchy line protocol")
	text := hierarchy.Result.ContentText()
	doc := requireLineProtocolDocumentE2E(t, text)
	requireCallHierarchyHeaderE2E(t, doc.Header, text)
	requireCallHierarchyRowsE2E(t, doc.Records, text)
}

func requireLineProtocolDocumentE2E(t *testing.T, text string) lineprotocol.Document {
	t.Helper()
	doc, err := lineprotocol.Parse(text)
	if err != nil {
		t.Fatalf("parse line protocol: %v; text=%q", err, text)
	}
	if doc.Error != nil {
		t.Fatalf("unexpected line-protocol error: %#v; text=%q", doc.Error, text)
	}
	return doc
}

func requireCallHierarchyHeaderE2E(t *testing.T, header lineprotocol.Header, text string) {
	t.Helper()
	if header.Unit != "edge" {
		t.Fatalf("call hierarchy unit = %q, want edge; text=%q", header.Unit, text)
	}
	if header.Total != 2 {
		t.Fatalf("call hierarchy total = %d, want 2; text=%q", header.Total, text)
	}
	if header.Showing != 2 {
		t.Fatalf("call hierarchy showing = %d, want 2; text=%q", header.Showing, text)
	}
}

func requireCallHierarchyRowsE2E(t *testing.T, records []lineprotocol.Record, text string) {
	t.Helper()
	rows := callHierarchyRowsE2E(records)
	if rows["incoming"] != "caller" {
		t.Fatalf("call hierarchy incoming = %q, want caller; text=%q", rows["incoming"], text)
	}
	if rows["outgoing"] != "callee" {
		t.Fatalf("call hierarchy outgoing = %q, want callee; text=%q", rows["outgoing"], text)
	}
}

func callHierarchyRowsE2E(records []lineprotocol.Record) map[string]string {
	rows := make(map[string]string, len(records))
	for _, record := range records {
		if record.Kind != "ROW" {
			continue
		}
		rows[record.Fields["direction"]] = record.Fields["name"]
	}
	return rows
}

func requireDocumentSymbolTextAndBudgetE2E(t *testing.T, client *mcpLSPBinaryClient, target string) {
	t.Helper()
	outline := client.callTool(t, "structure", map[string]any{
		"action":      "document_symbol",
		"file_path":   target,
		"language_id": "go",
	})
	requireMCPToolSuccess(t, client, outline, "document symbol readable text")
	requireReadableDocumentSymbolTextE2E(t, outline.Result.ContentText())
	requireDocumentSymbolBudgetE2E(t, outline.Result.ContentText())
}

func requireReadableDocumentSymbolTextE2E(t *testing.T, text string) {
	t.Helper()
	doc, err := lineprotocol.Parse(text)
	if err != nil || doc.Error != nil || doc.Header.Unit != "symbol" {
		t.Fatalf("document symbol line protocol = err:%v error:%#v header:%#v text=%q", err, doc.Error, doc.Header, text)
	}
	for _, record := range doc.Records {
		if record.Kind == "ROW" && record.Fields["name"] == "Symbol00" {
			return
		}
	}
	t.Fatalf("document symbol rows omit Symbol00: %q", text)
}

func requireDocumentSymbolBudgetE2E(t *testing.T, text string) {
	t.Helper()
	doc, err := lineprotocol.Parse(text)
	if err != nil {
		t.Fatalf("parse document symbol line protocol: %v; text=%q", err, text)
	}
	if doc.Error != nil || doc.Header.Total != 60 || doc.Header.Showing != 20 || !doc.Header.Truncated {
		t.Fatalf("document symbol budget = error:%#v total:%d showing:%d truncated:%v, want success/60/20/true; text=%q",
			doc.Error, doc.Header.Total, doc.Header.Showing, doc.Header.Truncated, text)
	}
}

// TestMcpLSPBinaryDocumentSymbolPreviewAndDiagnosticsScope_E2E 锁定文档大纲文本预览和空诊断范围。
func TestMcpLSPBinaryDocumentSymbolPreviewAndDiagnosticsScope_E2E(t *testing.T) {
	root := t.TempDir()
	target := writeFakeGoplsGoFixture(t, root)
	sibling := filepath.Join(root, "sibling.go")
	if err := os.WriteFile(sibling, []byte("package main\n\nfunc sibling() {}\n"), 0o600); err != nil {
		t.Fatalf("write diagnostics sibling: %v", err)
	}
	binary := buildMcpLSPBinaryForTest(t)
	fakeGoplsBinDir := writeFakeGoplsShutdownWarningLangserver(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	client := startMcpLSPBinaryForTestWithEnv(t, ctx, binary, root, fakeGoplsBinDir, []string{
		fakeGoplsManyDocumentSymbolsEnv + "=1",
		"AGENT_LSP_SHARED_CACHE_DIR=" + filepath.Join(t.TempDir(), "lsp-cache"),
	})
	defer client.close(t)
	client.call(t, "initialize", map[string]any{"protocolVersion": "2024-11-05"})

	t.Run("document symbol text preview", func(t *testing.T) { requireDocumentSymbolPreviewE2E(t, client, target) })
	t.Run("empty diagnostics names scope", func(t *testing.T) { requireEmptyDiagnosticsScopeE2E(t, client, target) })
	t.Run("empty batch diagnostics names scope", func(t *testing.T) { requireEmptyBatchDiagnosticsScopeE2E(t, client, target, sibling) })
}

func requireDocumentSymbolPreviewE2E(t *testing.T, client *mcpLSPBinaryClient, target string) {
	t.Helper()
	result := client.callTool(t, "structure", map[string]any{
		"action":      "document_symbol",
		"file_path":   target,
		"language_id": "go",
		"max_results": 10,
	})
	requireMCPToolSuccess(t, client, result, "document symbol text preview")
	text := result.Result.ContentText()
	doc := requireLineProtocolDocumentE2E(t, text)
	requireDocumentSymbolPreviewHeaderE2E(t, doc.Header, text)
	requireDocumentSymbolNamesE2E(t, doc.Records, text, "Symbol00", "Symbol09")
}

func requireDocumentSymbolPreviewHeaderE2E(t *testing.T, header lineprotocol.Header, text string) {
	t.Helper()
	if header.Total != 60 {
		t.Fatalf("document symbol preview total = %d, want 60; text=%q", header.Total, text)
	}
	if header.Showing != 10 {
		t.Fatalf("document symbol preview showing = %d, want 10; text=%q", header.Showing, text)
	}
	if !header.Truncated {
		t.Fatalf("document symbol preview should be truncated; text=%q", text)
	}
}

func requireDocumentSymbolNamesE2E(t *testing.T, records []lineprotocol.Record, text string, wanted ...string) {
	t.Helper()
	found := make(map[string]bool, len(wanted))
	for _, record := range records {
		if record.Kind != "ROW" {
			continue
		}
		found[record.Fields["name"]] = true
	}
	for _, want := range wanted {
		if !found[want] {
			t.Fatalf("document symbol rows omit %q: %q", want, text)
		}
	}
}

func requireEmptyDiagnosticsScopeE2E(t *testing.T, client *mcpLSPBinaryClient, target string) {
	t.Helper()
	result := client.callTool(t, "file", map[string]any{
		"action":      "diagnostics",
		"file_path":   target,
		"language_id": "go",
	})
	requireMCPToolSuccess(t, client, result, "empty diagnostics scope")
	text := result.Result.ContentText()
	doc := requireLineProtocolDocumentE2E(t, text)
	requireEmptyDiagnosticsHeaderE2E(t, doc.Header, text)
	requireLineProtocolMessageE2E(t, doc.Records, "Checked file: "+target, text)
}

func requireEmptyBatchDiagnosticsScopeE2E(t *testing.T, client *mcpLSPBinaryClient, target, sibling string) {
	t.Helper()
	result := client.callTool(t, "file", map[string]any{
		"action":      "diagnostics",
		"file_paths":  []string{target, sibling},
		"language_id": "go",
	})
	requireMCPToolSuccess(t, client, result, "empty batch diagnostics scope")
	text := result.Result.ContentText()
	doc := requireLineProtocolDocumentE2E(t, text)
	requireEmptyDiagnosticsHeaderE2E(t, doc.Header, text)
	requireLineProtocolMessageE2E(t, doc.Records, "Checked 2 files: "+target+", "+sibling, text)
}

func requireEmptyDiagnosticsHeaderE2E(t *testing.T, header lineprotocol.Header, text string) {
	t.Helper()
	if header.Unit != "diagnostic" {
		t.Fatalf("diagnostics unit = %q, want diagnostic; text=%q", header.Unit, text)
	}
	if header.Total != 0 {
		t.Fatalf("diagnostics total = %d, want 0; text=%q", header.Total, text)
	}
	if header.Showing != 0 {
		t.Fatalf("diagnostics showing = %d, want 0; text=%q", header.Showing, text)
	}
}

func requireLineProtocolMessageE2E(t *testing.T, records []lineprotocol.Record, want, text string) {
	t.Helper()
	for _, record := range records {
		if record.Kind == "MESSAGE" && record.Value == want {
			return
		}
	}
	t.Fatalf("line-protocol messages omit %q: %q", want, text)
}

// TestMcpLSPBinaryWorkspaceSymbolWalkLimitSuggestsFileScope_E2E 锁定语言级遍历超限后的可执行收窄提示。
func TestMcpLSPBinaryWorkspaceSymbolWalkLimitSuggestsFileScope_E2E(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/walklimit\n\ngo 1.26\n"), 0o600); err != nil {
		t.Fatalf("write go.mod: %v", err)
	}
	for i := range 10_005 {
		path := filepath.Join(root, fmt.Sprintf("entry-%05d.txt", i))
		if err := os.WriteFile(path, nil, 0o600); err != nil {
			t.Fatalf("write walk-limit fixture %d: %v", i, err)
		}
	}

	binary := buildMcpLSPBinaryForTest(t)
	fakeGoplsBinDir := writeFakeGoplsShutdownWarningLangserver(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	client := startMcpLSPBinaryForTestWithEnv(t, ctx, binary, root, fakeGoplsBinDir, []string{
		"AGENT_LSP_SHARED_CACHE_DIR=" + filepath.Join(t.TempDir(), "lsp-cache"),
		fakeGoplsWorkspaceSymbolsEnv + "=1",
	})
	defer client.close(t)
	client.call(t, "initialize", map[string]any{"protocolVersion": "2024-11-05"})

	result := client.callTool(t, "structure", map[string]any{
		"action":             "workspace_symbol",
		"workspace_language": "go",
		"query":              "Needle",
	})
	if !result.Result.IsError {
		t.Fatalf("workspace symbol walk limit = success, want line-protocol error; text=%q",
			result.Result.ContentText())
	}
	text := result.Result.ContentText()
	for _, want := range []string{"structure action=workspace_symbol", "file_path=", "query="} {
		if !strings.Contains(text, want) {
			t.Fatalf("workspace symbol walk-limit text = %q, want executable scope hint containing %q", text, want)
		}
	}
	if strings.Contains(text, "query/path/glob") {
		t.Fatalf("workspace symbol walk-limit text advertises unsupported path/glob arguments: %q", text)
	}
}

// TestMcpLSPBinaryWorkspaceSymbolScopesRanksAndBoundsPayload_E2E 锁定文件范围、显式模糊扩展和行协议预算。
func TestMcpLSPBinaryWorkspaceSymbolScopesRanksAndBoundsPayload_E2E(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/symbolscope\n\ngo 1.26\n"), 0o600); err != nil {
		t.Fatalf("write go.mod: %v", err)
	}
	target, sibling := writeWorkspaceSymbolScopeFixtures(t, root)

	binary := buildMcpLSPBinaryForTest(t)
	fakeGoplsBinDir := writeFakeGoplsShutdownWarningLangserver(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	client := startMcpLSPBinaryForTestWithEnv(t, ctx, binary, root, fakeGoplsBinDir, []string{
		fakeGoplsWorkspaceSymbolsEnv + "=1",
		fakeGoplsWorkspaceSymbolTargetEnv + "=" + target,
		fakeGoplsWorkspaceSymbolSiblingEnv + "=" + sibling,
		"AGENT_LSP_SHARED_CACHE_DIR=" + filepath.Join(t.TempDir(), "lsp-cache"),
	})
	defer client.close(t)
	client.call(t, "initialize", map[string]any{"protocolVersion": "2024-11-05"})

	exact := client.callTool(t, "structure", map[string]any{
		"action":      "workspace_symbol",
		"file_path":   target,
		"query":       "Needle",
		"max_results": 10,
	})
	requireMCPToolSuccess(t, client, exact, "file-scoped exact workspace symbol")
	assertExactWorkspaceSymbolScope(t, exact, target)

	fuzzy := client.callTool(t, "structure", map[string]any{
		"action":      "workspace_symbol",
		"file_path":   target,
		"query":       "Needle",
		"match_mode":  "fuzzy",
		"max_results": 10,
	})
	requireMCPToolSuccess(t, client, fuzzy, "file-scoped fuzzy workspace symbol")
	assertFuzzyWorkspaceSymbolScopeAndPayload(t, fuzzy, target)
}

type workspaceSymbolE2ERow struct {
	Name string `json:"name"`
	File string `json:"file"`
}

func writeWorkspaceSymbolScopeFixtures(t *testing.T, root string) (string, string) {
	t.Helper()
	target := filepath.Join(root, "target.go")
	sibling := filepath.Join(root, "sibling.go")
	for path, content := range map[string]string{
		target:  "package symbolscope\n\nfunc Needle() {}\n",
		sibling: "package symbolscope\n\nfunc NeedleSibling() {}\n",
	} {
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
	}
	return target, sibling
}

func assertExactWorkspaceSymbolScope(t *testing.T, result mcpLSPBinaryResponse, target string) {
	t.Helper()
	rows := decodeWorkspaceSymbolRows(t, result.Result.ContentText())
	if len(rows) != 1 || rows[0].Name != "Needle" || rows[0].File != target {
		t.Fatalf("exact file-scoped rows = %#v, want only Needle in %s; text=%q", rows, target, result.Result.ContentText())
	}
	if !strings.Contains(result.Result.ContentText(), "match_mode=fuzzy") {
		t.Fatalf("exact workspace symbol text = %q, want explicit fuzzy expansion hint", result.Result.ContentText())
	}
}

func assertFuzzyWorkspaceSymbolScopeAndPayload(t *testing.T, result mcpLSPBinaryResponse, target string) {
	t.Helper()
	rows := decodeWorkspaceSymbolRows(t, result.Result.ContentText())
	if len(rows) != 10 || rows[0].Name != "Needle" {
		t.Fatalf("fuzzy rows = %#v, want 10 target rows with exact match first", rows)
	}
	for _, row := range rows {
		if row.File != target {
			t.Fatalf("fuzzy file scope leaked %s, want %s; rows=%#v", row.File, target, rows)
		}
	}
	if !strings.Contains(result.Result.ContentText(), "Needle09") {
		t.Fatalf("workspace symbol line protocol lost the final bounded row: text=%q", result.Result.ContentText())
	}
}

func decodeWorkspaceSymbolRows(t *testing.T, text string) []workspaceSymbolE2ERow {
	t.Helper()
	doc, err := lineprotocol.Parse(text)
	if err != nil {
		t.Fatalf("decode workspace symbol line protocol: %v; text=%q", err, text)
	}
	if doc.Error != nil || doc.Header.Unit != "symbol" {
		t.Fatalf("workspace symbol line protocol header = %#v error=%#v, want OK unit=symbol", doc.Header, doc.Error)
	}
	rows := make([]workspaceSymbolE2ERow, 0, doc.Header.Showing)
	for _, record := range doc.Records {
		if record.Kind != "ROW" {
			continue
		}
		rows = append(rows, workspaceSymbolE2ERow{
			Name: record.Fields["name"],
			File: record.Fields["file"],
		})
	}
	return rows
}

func fakeGoplsDocumentSymbols() []map[string]any {
	if os.Getenv(fakeGoplsManyDocumentSymbolsEnv) != "1" {
		return fakeGoplsNamedDocumentSymbols("main", 1)
	}
	return fakeGoplsNamedDocumentSymbols("Symbol", 60)
}

func fakeGoplsNamedDocumentSymbols(prefix string, count int) []map[string]any {
	results := make([]map[string]any, 0, count)
	for i := range count {
		name := prefix
		if count > 1 {
			name = fmt.Sprintf("%s%02d", prefix, i)
		}
		results = append(results, map[string]any{
			"name": name,
			"kind": 12,
			"range": map[string]any{
				"start": map[string]any{"line": i + 2, "character": 0},
				"end":   map[string]any{"line": i + 2, "character": 14},
			},
			"selectionRange": map[string]any{
				"start": map[string]any{"line": i + 2, "character": 5},
				"end":   map[string]any{"line": i + 2, "character": 9},
			},
		})
	}
	return results
}

func fakeGoplsCapabilities() map[string]any {
	capabilities := map[string]any{
		"textDocumentSync":       1,
		"hoverProvider":          true,
		"documentSymbolProvider": true,
		"callHierarchyProvider":  true,
	}
	if os.Getenv(fakeGoplsPlainTextContractEnv) == "1" {
		capabilities["foldingRangeProvider"] = true
		capabilities["semanticTokensProvider"] = map[string]any{
			"legend": map[string]any{
				"tokenTypes":     []string{"namespace", "type", "function", "variable"},
				"tokenModifiers": []string{"declaration", "readonly"},
			},
			"full": true,
		}
	}
	if os.Getenv(fakeGoplsWorkspaceSymbolsEnv) == "1" {
		capabilities["workspaceSymbolProvider"] = true
	}
	if os.Getenv(fakeGoplsSuppressDiagnosticProviderEnv) != "1" {
		capabilities["diagnosticProvider"] = map[string]any{
			"interFileDependencies": true,
			"workspaceDiagnostics":  false,
		}
	}
	return capabilities
}

func fakeGoplsWorkspaceSymbolResults() []map[string]any {
	target := os.Getenv(fakeGoplsWorkspaceSymbolTargetEnv)
	sibling := os.Getenv(fakeGoplsWorkspaceSymbolSiblingEnv)
	if target == "" || sibling == "" {
		return nil
	}
	results := make([]map[string]any, 0, 14)
	for i := range 12 {
		name := fmt.Sprintf("Needle%02d", i)
		if i == 0 {
			name = "Needle"
		}
		results = append(results, fakeGoplsWorkspaceSymbol(name, target, i))
	}
	results = append(results,
		fakeGoplsWorkspaceSymbol("Needle", sibling, 20),
		fakeGoplsWorkspaceSymbol("NeedleSibling", sibling, 21),
	)
	return results
}

func fakeGoplsWorkspaceSymbol(name, path string, line int) map[string]any {
	return map[string]any{
		"name": name,
		"kind": 12,
		"location": map[string]any{
			"uri": (&url.URL{Scheme: "file", Path: path}).String(),
			"range": map[string]any{
				"start": map[string]any{"line": line, "character": 0},
				"end":   map[string]any{"line": line, "character": len(name)},
			},
		},
	}
}

func fakeGoplsCallHierarchyCalls(req fakeLSPRequest) []map[string]any {
	if os.Getenv(fakeGoplsPlainTextContractEnv) == "1" {
		return fakeGoplsPlainTextHierarchyCalls(req)
	}
	var params struct {
		Item struct {
			URI string `json:"uri"`
		} `json:"item"`
	}
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return nil
	}
	item := map[string]any{
		"name": "caller",
		"kind": 12,
		"uri":  params.Item.URI,
		"range": map[string]any{
			"start": map[string]any{"line": 0, "character": 0},
			"end":   map[string]any{"line": 0, "character": 10},
		},
		"selectionRange": map[string]any{
			"start": map[string]any{"line": 0, "character": 5},
			"end":   map[string]any{"line": 0, "character": 9},
		},
	}
	key := "from"
	if strings.HasSuffix(req.Method, "outgoingCalls") {
		key = "to"
		item["name"] = "callee"
	}
	return []map[string]any{{
		key: item,
		"fromRanges": []map[string]any{{
			"start": map[string]any{"line": 2, "character": 5},
			"end":   map[string]any{"line": 2, "character": 9},
		}},
	}}
}
