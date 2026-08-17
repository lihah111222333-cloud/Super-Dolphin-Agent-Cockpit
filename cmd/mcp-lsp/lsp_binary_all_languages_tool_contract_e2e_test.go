//go:build e2e

package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/multilsp"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/mcpserver/common/lineprotocol"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/securefs"
)

// TestMcpLSPBinaryRepresentativeFakeLanguageToolContractCrossPlatformE2E 先用 Python 锁定跨平台 fake-LSP
// binary 与七个 MCP 工具族的控制契约。该测试证明路由、schema、响应结构和写盘同步，
// 不证明任何真实语言服务器的语义质量。
func TestMcpLSPBinaryRepresentativeFakeLanguageToolContractCrossPlatformE2E(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping all-language fake tool contract e2e in short mode")
	}
	binary := buildMcpLSPBinaryForTest(t)
	runAllLanguageToolContractE2E(t, binary, "python", true)
}

// TestMcpLSPBinaryAllRequiresLSPClientLanguageToolContractCrossPlatformE2E 动态覆盖全部
// RequiresLSPClient 语言及公开别名。外部 fake server 只证明七个工具族的路由、控制面和
// schema 闭包，不冒充真实语言服务器语义证明。
func TestMcpLSPBinaryAllRequiresLSPClientLanguageToolContractCrossPlatformE2E(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping all-language fake tool contract e2e in short mode")
	}
	ids := requiresLSPClientLanguageIDs(t)
	if !slices.Equal(ids, expectedRequiresLSPClientLanguageIDs()) {
		t.Fatalf("default language adapter registry returned an unexpected RequiresLSPClient closure: got=%s want=%s", strings.Join(ids, ", "), strings.Join(expectedRequiresLSPClientLanguageIDs(), ", "))
	}
	t.Logf("fake MCP/LSP control-contract coverage: %d RequiresLSPClient language IDs: %s", len(ids), strings.Join(ids, ", "))

	binary := buildMcpLSPBinaryForTest(t)
	for _, languageID := range ids {
		languageID := languageID
		t.Run(languageID, func(t *testing.T) {
			runAllLanguageToolContractE2E(t, binary, languageID, false)
		})
	}
}

// TestMcpLSPBinaryRequiresLSPClientLanguageClosureCrossPlatformE2E 快速锁定全部 42 个外部 language ID，避免数量不变时的别名替换逃过长矩阵。
func TestMcpLSPBinaryRequiresLSPClientLanguageClosureCrossPlatformE2E(t *testing.T) {
	got := requiresLSPClientLanguageIDs(t)
	want := expectedRequiresLSPClientLanguageIDs()
	if !slices.Equal(got, want) {
		t.Fatalf("RequiresLSPClient language closure = %v, want exact cross-platform contract %v", got, want)
	}
}

func expectedRequiresLSPClientLanguageIDs() []string {
	return []string{
		"c", "cpp", "csharp", "css", "dart", "dockerfile",
		"go", "gomod", "gosum", "gowork", "graphql", "html",
		"java", "javascript", "javascriptreact", "json", "kotlin", "lua", "markdown",
		"mq4", "mq5", "mqh", "mql", "mql4", "mql5", "objective-c", "objective-cpp",
		"php", "prisma", "proto", "python", "ruby", "rust", "shellscript", "sql",
		"svelte", "swift", "terraform", "typescript", "typescriptreact", "vue", "yaml",
	}
}

func requiresLSPClientLanguageIDs(t *testing.T) []string {
	t.Helper()
	registry := multilsp.NewDefaultLanguageAdapterRegistry()
	ids := make([]string, 0)
	seen := make(map[string]struct{})
	for _, languageID := range registry.LanguageIDs() {
		adapter, ok := registry.AdapterForLanguage(languageID)
		if !ok {
			t.Fatalf("language %q disappeared during adapter lookup", languageID)
		}
		if adapter.CapabilityPolicy().RequiresLSPClient {
			ids = append(ids, languageID)
			seen[languageID] = struct{}{}
		}
	}
	// MQL language IDs intentionally normalize to the cpp adapter inside the
	// registry. Add every public alias back to the E2E matrix so normalization
	// cannot hide a broken externally accepted language ID.
	for _, languageID := range contract.ClangdLanguageIDs() {
		if _, ok := seen[languageID]; ok {
			continue
		}
		adapter, ok := registry.AdapterForLanguage(languageID)
		if !ok || !adapter.CapabilityPolicy().RequiresLSPClient {
			t.Fatalf("public clangd language alias %q does not resolve to an LSP adapter", languageID)
		}
		ids = append(ids, languageID)
		seen[languageID] = struct{}{}
	}
	slices.Sort(ids)
	return ids
}

func runAllLanguageToolContractE2E(t *testing.T, binary, languageID string, checkManifest bool) {
	t.Helper()
	root := t.TempDir()
	productHome := filepath.Join(root, ".super-dolphin")
	if err := os.MkdirAll(productHome, 0o700); err != nil {
		t.Fatalf("create isolated all-language product home: %v", err)
	}
	if err := securefs.RestrictPrivateOwnerOnly(productHome, 0o700); err != nil {
		t.Fatalf("restrict isolated all-language product home: %v", err)
	}
	fixture := writeAllLanguageToolContractFixture(t, root, languageID)
	fakeBinDir := writeAllLanguageToolContractBinaries(t, root, languageID)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	client := startMcpLSPBinaryForTestWithEnv(t, ctx, binary, root, fakeBinDir, []string{
		"MCP_LSP_CROSS_PLATFORM_ALL_LANGUAGE_CONTRACT_FAKE=1",
		// The fake Go-family server is intentionally much smaller than real
		// gopls. A one-megabyte root-cohort limit exercises the production
		// zero-lease pressure-reclaim path so this matrix does not leave the
		// product's normal 15-minute shared-daemon policy running per alias.
		"AGENT_LSP_GO_RSS_LIMIT_MB=1",
		"AGENT_LSP_SHARED_CACHE_DIR=" + filepath.Join(root, ".lsp-cache"),
		"SUPER_DOLPHIN_HOME=" + productHome,
		"HOME=" + root,
		"USERPROFILE=" + root,
	})
	defer client.close(t)

	client.call(t, "initialize", map[string]any{
		"protocolVersion": "2024-11-05",
		"capabilities":    map[string]any{},
	})
	if checkManifest {
		requireAllLanguageToolSchemas(t, client)
	}

	common := func(args map[string]any) map[string]any {
		args["language_id"] = languageID
		args["work_dir"] = root
		return args
	}
	needleLine, functionLine := 1, 2
	switch languageID {
	case "go":
		needleLine, functionLine = 3, 5
	case "gomod", "gowork":
		needleLine, functionLine = 2, 3
	case "json":
		needleLine, functionLine = 2, 2
	}
	semanticPos := fixture.target + ":" + strconv.Itoa(functionLine) + ":3"

	opened := client.callTool(t, "file", common(map[string]any{
		"action":    "open_file",
		"file_path": fixture.target,
	}))
	requireMCPToolSuccess(t, client, opened, languageID+" file open_file")
	requireAllLanguageToolContains(t, opened, "opened", languageID+" file open_file")

	readSingle := client.callTool(t, "file", common(map[string]any{
		"action":    "read_file",
		"file_path": fixture.target,
		"limit":     300,
	}))
	requireMCPToolSuccess(t, client, readSingle, languageID+" file read_file single")
	requireAllLanguageToolContains(t, readSingle, "contractNeedle", languageID+" file read_file single")

	readBatch := client.callTool(t, "file", common(map[string]any{
		"action":     "read_file",
		"file_paths": []string{fixture.target, fixture.secondary},
		"limit":      300,
	}))
	requireMCPToolSuccess(t, client, readBatch, languageID+" file read_file batch")
	requireAllLanguageToolContains(t, readBatch, filepath.Base(fixture.target), languageID+" file read_file batch target")
	requireAllLanguageToolContains(t, readBatch, filepath.Base(fixture.secondary), languageID+" file read_file batch secondary")

	readLines := client.callTool(t, "file", common(map[string]any{
		"action": "read_file",
		"pos":    fixture.target + ":" + strconv.Itoa(needleLine),
		"scope":  "lines",
		"limit":  1,
	}))
	requireMCPToolSuccess(t, client, readLines, languageID+" file read_file lines")
	requireAllLanguageToolContains(t, readLines, "contractNeedle", languageID+" file read_file lines")

	readFunction := client.callTool(t, "file", common(map[string]any{
		"action": "read_file",
		"pos":    fixture.target + ":" + strconv.Itoa(functionLine),
		"limit":  50,
	}))
	requireMCPToolSuccess(t, client, readFunction, languageID+" file read_file function")
	requireAllLanguageToolContains(t, readFunction, "ContractFunction", languageID+" file read_file function")

	diagnostics := client.callTool(t, "file", common(map[string]any{
		"action":    "diagnostics",
		"file_path": fixture.target,
	}))
	requireMCPToolSuccess(t, client, diagnostics, languageID+" file diagnostics")
	diagnosticsDoc := requireAllLanguageLineProtocol(t, diagnostics, languageID+" file diagnostics")
	if diagnosticsDoc.Header.Unit != "diagnostic" || diagnosticsDoc.Header.Total < 1 {
		t.Fatalf("%s file diagnostics has no content diagnostic rows: header=%+v text=%q stderr=%s",
			languageID, diagnosticsDoc.Header, diagnostics.Result.ContentText(), client.stderrString())
	}
	requireAllLanguageToolContains(t, diagnostics, "CONTRACT-DIAG", languageID+" file diagnostics")

	for _, action := range []string{"hover", "definition", "implementation", "type_definition", "signature_help"} {
		response := client.callTool(t, "inspect", common(map[string]any{
			"action": action,
			"pos":    semanticPos,
		}))
		requireMCPToolSuccess(t, client, response, languageID+" inspect "+action)
		want := "contract"
		if action == "hover" {
			want = "contract-hover"
		} else if action == "signature_help" {
			want = "contract-signature"
		}
		requireAllLanguageToolContains(t, response, want, languageID+" inspect "+action)
	}

	references := client.callTool(t, "xref", common(map[string]any{
		"action":              "references",
		"pos":                 semanticPos,
		"include_declaration": false,
		"max_results":         1,
	}))
	requireMCPToolSuccess(t, client, references, languageID+" xref references")
	requireAllLanguageToolContains(t, references, "contract-secondary", languageID+" xref references")
	requireAllLanguageToolTruncated(t, references, languageID+" xref references max_results")

	for _, direction := range []string{"incoming", "outgoing", "both"} {
		response := client.callTool(t, "xref", common(map[string]any{
			"action":    "call_hierarchy",
			"pos":       semanticPos,
			"direction": direction,
		}))
		requireMCPToolSuccess(t, client, response, languageID+" xref call_hierarchy "+direction)
		requireAllLanguageToolContains(t, response, "contract-call", languageID+" xref call_hierarchy "+direction)
	}
	for _, direction := range []string{"supertypes", "subtypes"} {
		response := client.callTool(t, "xref", common(map[string]any{
			"action":    "type_hierarchy",
			"pos":       semanticPos,
			"direction": direction,
		}))
		requireMCPToolSuccess(t, client, response, languageID+" xref type_hierarchy "+direction)
		requireAllLanguageToolContains(t, response, "contract-type", languageID+" xref type_hierarchy "+direction)
	}

	textSearch := client.callTool(t, "grep", map[string]any{
		"action":         "text_search",
		"query":          "contractNeedle",
		"paths":          []string{root},
		"glob":           "**/*" + filepath.Ext(fixture.target),
		"case_sensitive": false,
		"max_results":    10,
		"work_dir":       root,
	})
	requireMCPToolSuccess(t, client, textSearch, languageID+" grep text_search path/glob")
	requireAllLanguageToolContains(t, textSearch, "contractNeedle", languageID+" grep text_search path/glob")

	regexSearch := client.callTool(t, "grep", map[string]any{
		"action":         "text_search",
		"query":          "[Cc]ontract[A-Z][A-Za-z]+",
		"paths":          []string{fixture.target},
		"regex":          true,
		"case_sensitive": true,
		"max_results":    10,
		"work_dir":       root,
	})
	requireMCPToolSuccess(t, client, regexSearch, languageID+" grep regex")
	requireAllLanguageToolContains(t, regexSearch, "contractNeedle", languageID+" grep regex")

	smartCaseSearch := client.callTool(t, "grep", map[string]any{
		"action":      "text_search",
		"query":       "contractneedle",
		"paths":       []string{filepath.Dir(fixture.target)},
		"max_results": 10,
		"work_dir":    root,
	})
	requireMCPToolSuccess(t, client, smartCaseSearch, languageID+" grep smart-case paths")
	requireAllLanguageToolContains(t, smartCaseSearch, "contractNeedle", languageID+" grep smart-case paths")

	compatSearch := client.callTool(t, "grep", map[string]any{
		"action":      "text_search",
		"query":       "contractNeedle",
		"paths":       []string{fixture.secondary},
		"max_results": 10,
		"work_dir":    root,
	})
	requireMCPToolSuccess(t, client, compatSearch, languageID+" grep paths compatibility")
	requireAllLanguageToolContains(t, compatSearch, "contractNeedle", languageID+" grep paths compatibility")

	grepCap := client.callTool(t, "grep", map[string]any{
		"action":      "text_search",
		"query":       "contractNeedle",
		"paths":       []string{root},
		"glob":        "**/*" + filepath.Ext(fixture.target),
		"max_results": 1,
		"work_dir":    root,
	})
	requireMCPToolSuccess(t, client, grepCap, languageID+" grep cap")
	requireAllLanguageToolTruncated(t, grepCap, languageID+" grep cap")
	requireAllLanguageToolHint(t, grepCap, languageID+" grep cap")

	astSearch := client.callTool(t, "grep", map[string]any{
		"action": "ast_search",
		"query":  "contractNeedle",
		"paths":  []string{fixture.target},
		// grep.ast_search is a workspace search capability, not an LSP adapter
		// selector. Use one grammar supported by the bundled sg for every
		// adapter-routing subtest; language_id coverage is exercised by all LSP
		// tools above.
		"ast_language": "javascript",
		"max_results":  2,
		"work_dir":     root,
	})
	requireMCPToolSuccess(t, client, astSearch, languageID+" grep ast_search")
	requireAllLanguageToolContains(t, astSearch, "CONTRACT-AST", languageID+" grep ast_search")

	documentSymbols := client.callTool(t, "structure", common(map[string]any{
		"action":      "document_symbol",
		"file_path":   fixture.target,
		"max_results": 10,
	}))
	requireMCPToolSuccess(t, client, documentSymbols, languageID+" structure document_symbol")
	requireAllLanguageToolContains(t, documentSymbols, "ContractFunction", languageID+" structure document_symbol")

	workspaceByFile := client.callTool(t, "structure", map[string]any{
		"action":      "workspace_symbol",
		"file_path":   fixture.target,
		"query":       "Contract",
		"max_results": 10,
		"work_dir":    root,
	})
	requireMCPToolSuccess(t, client, workspaceByFile, languageID+" structure workspace_symbol file")
	requireAllLanguageToolContains(t, workspaceByFile, "ContractWorkspace", languageID+" structure workspace_symbol file")

	workspaceByLanguage := client.callTool(t, "structure", map[string]any{
		"action":             "workspace_symbol",
		"workspace_language": languageID,
		"query":              "Contract",
		"max_results":        1,
		"work_dir":           root,
	})
	if languageID == "sql" {
		// SQL 的 language-only 请求无法证明当前 sqlc owner 属于受支持的 SQLite 方言；
		// 上面的 file_path 变体证明合法能力可用，这里同时锁定危险歧义必须 fail-fast。
		requireAllLanguageToolErrorContains(t, workspaceByLanguage, "requires file_path", languageID+" structure workspace_symbol language guard")
	} else {
		requireMCPToolSuccess(t, client, workspaceByLanguage, languageID+" structure workspace_symbol language")
		requireAllLanguageToolContains(t, workspaceByLanguage, "ContractWorkspace", languageID+" structure workspace_symbol language")
		requireAllLanguageToolTruncated(t, workspaceByLanguage, languageID+" structure workspace_symbol max_results")
	}

	folding := client.callTool(t, "structure", common(map[string]any{
		"action":    "folding_range",
		"file_path": fixture.target,
	}))
	requireMCPToolSuccess(t, client, folding, languageID+" structure folding_range")
	requireAllLanguageToolContains(t, folding, "startLine", languageID+" structure folding_range")

	semanticTokens := client.callTool(t, "structure", common(map[string]any{
		"action":      "semantic_tokens",
		"file_path":   fixture.target,
		"max_results": 10,
	}))
	requireMCPToolSuccess(t, client, semanticTokens, languageID+" structure semantic_tokens")
	requireAllLanguageToolContains(t, semanticTokens, "data", languageID+" structure semantic_tokens")

	completion := client.callTool(t, "completion", common(map[string]any{
		"pos":         semanticPos,
		"max_results": 1,
	}))
	requireMCPToolSuccess(t, client, completion, languageID+" completion")
	requireAllLanguageToolContains(t, completion, "contractCompletion", languageID+" completion")
	requireAllLanguageToolTruncated(t, completion, languageID+" completion max_results")

	editFiles := []struct {
		name string
		path string
	}{
		{name: "replace_range", path: fixture.replaceTarget},
		{name: "rename", path: fixture.renameTarget},
		{name: "code_action", path: fixture.codeActionTarget},
		{name: "format", path: fixture.formatTarget},
	}
	for _, editFile := range editFiles {
		openedEdit := client.callTool(t, "file", common(map[string]any{
			"action":    "open_file",
			"file_path": editFile.path,
		}))
		requireMCPToolSuccess(t, client, openedEdit, languageID+" patch_edit open "+editFile.name)
	}

	replacePatch := "@@\n-CONTRACT_REPLACE\n+CONTRACT_REPLACED\n"
	if languageID == "gowork" {
		replacePatch = "@@\n-// CONTRACT_REPLACE\n+// CONTRACT_REPLACED\n"
	}
	replace := client.callTool(t, "patch_edit", common(map[string]any{
		"action":    "replace_range",
		"file_path": fixture.replaceTarget,
		"patch":     replacePatch,
	}))
	requireMCPToolSuccess(t, client, replace, languageID+" patch_edit replace_range")
	requireAllLanguageFileContains(t, fixture.replaceTarget, "CONTRACT_REPLACED", languageID+" patch_edit replace_range disk")
	requireAllLanguageToolBool(t, replace, "lsp_sync", true, languageID+" patch_edit replace_range lsp_sync")

	rename := client.callTool(t, "patch_edit", common(map[string]any{
		"action":   "rename",
		"pos":      fixture.renameTarget + ":1:1",
		"new_name": "CONTRACT_RENAMED",
	}))
	requireMCPToolSuccess(t, client, rename, languageID+" patch_edit rename")
	requireAllLanguageToolContains(t, rename, "CONTRACT_RENAMED", languageID+" patch_edit rename response")
	requireAllLanguageToolPositiveNumber(t, rename, "total_edits", languageID+" patch_edit rename total_edits")
	requireAllLanguageFileContains(t, fixture.renameTarget, "CONTRACT_RENAMED", languageID+" patch_edit rename disk")

	codeAction := client.callTool(t, "patch_edit", common(map[string]any{
		"action": "code_action",
		"pos":    fixture.codeActionTarget + ":1:1",
		"only":   []string{"quickfix"},
	}))
	requireMCPToolSuccess(t, client, codeAction, languageID+" patch_edit code_action")
	requireAllLanguageToolContains(t, codeAction, "CONTRACT_CODE_ACTION", languageID+" patch_edit code_action response")
	requireAllLanguageToolBool(t, codeAction, "lsp_sync", true, languageID+" patch_edit code_action lsp_sync")
	requireAllLanguageFileContains(t, fixture.codeActionTarget, "CONTRACT_CODE_ACTION", languageID+" patch_edit code_action disk")

	format := client.callTool(t, "patch_edit", common(map[string]any{
		"action":    "format",
		"file_path": fixture.formatTarget,
	}))
	requireMCPToolSuccess(t, client, format, languageID+" patch_edit format")
	requireAllLanguageToolPositiveNumber(t, format, "applied_count", languageID+" patch_edit format applied_count")
	requireAllLanguageToolBool(t, format, "lsp_sync", true, languageID+" patch_edit format lsp_sync")
	requireAllLanguageFileContains(t, fixture.formatTarget, "CONTRACT_FORMATTED", languageID+" patch_edit format disk")
}

func requireAllLanguageToolSchemas(t *testing.T, client *mcpLSPBinaryClient) {
	t.Helper()
	response := callAllLanguageToolRaw(t, client, "tools/list", map[string]any{})
	result, ok := response["result"].(map[string]any)
	if !ok {
		t.Fatalf("tools/list result is not an object: %#v", response)
	}
	tools, ok := result["tools"].([]any)
	if !ok {
		t.Fatalf("tools/list result has no tools array: %#v", result)
	}
	want := []string{"completion", "file", "grep", "inspect", "patch_edit", "structure", "xref"}
	got := make([]string, 0, len(tools))
	for _, rawTool := range tools {
		tool, ok := rawTool.(map[string]any)
		if !ok {
			t.Fatalf("tools/list contains non-object tool: %#v", rawTool)
		}
		name, _ := tool["name"].(string)
		got = append(got, name)
	}
	slices.Sort(got)
	if !slices.Equal(got, want) {
		t.Fatalf("tools/list names = %v, want %v; response=%#v", got, want, response)
	}
	for _, rawTool := range tools {
		tool := rawTool.(map[string]any)
		schema, _ := tool["inputSchema"].(map[string]any)
		properties, _ := schema["properties"].(map[string]any)
		if len(properties) == 0 {
			t.Fatalf("tool %s has no input schema properties: %#v", tool["name"], schema)
		}
	}
}

func callAllLanguageToolRaw(t *testing.T, client *mcpLSPBinaryClient, method string, params map[string]any) map[string]any {
	t.Helper()
	request := map[string]any{
		"jsonrpc": "2.0",
		"id":      time.Now().UnixNano(),
		"method":  method,
		"params":  params,
	}
	rawRequest, err := json.Marshal(request)
	if err != nil {
		t.Fatalf("marshal %s request: %v", method, err)
	}
	if _, err := client.stdin.Write(append(rawRequest, '\n')); err != nil {
		t.Fatalf("write %s request: %v; stderr=%s", method, err, client.stderrString())
	}
	line, err := client.stdout.ReadBytes('\n')
	if err != nil {
		t.Fatalf("read %s response: %v; stderr=%s", method, err, client.stderrString())
	}
	var response map[string]any
	if err := json.Unmarshal(line, &response); err != nil {
		t.Fatalf("unmarshal %s response: %v; raw=%s", method, err, line)
	}
	if response["error"] != nil {
		t.Fatalf("%s returned JSON-RPC error: %#v", method, response["error"])
	}
	return response
}

func requireAllLanguageToolSuccess(t *testing.T, response mcpLSPBinaryResponse, label string) {
	t.Helper()
	if response.Result.IsError {
		t.Fatalf("%s returned MCP error result: text=%q", label, response.Result.ContentText())
	}
	if len(bytes.TrimSpace(response.Result.StructuredContent)) != 0 {
		t.Fatalf("%s returned deprecated structuredContent: content-only contract requires empty structuredContent", label)
	}
}

// requireAllLanguageLineProtocol 统一读取远程 content-only 文本；结构化结果只允许为空。
func requireAllLanguageLineProtocol(t *testing.T, response mcpLSPBinaryResponse, label string) lineprotocol.Document {
	t.Helper()
	requireAllLanguageToolSuccess(t, response, label)
	doc, err := lineprotocol.Parse(response.Result.ContentText())
	if err != nil {
		t.Fatalf("%s content is not line protocol: %v; text=%q", label, err, response.Result.ContentText())
	}
	return doc
}

// requireAllLanguageToolErrorContains 证明预期的 fail-fast 分支既返回 MCP 错误，
// 又保留可操作的稳定原因，避免测试只看到“失败”却无法区分权限、方言或能力问题。
func requireAllLanguageToolErrorContains(t *testing.T, response mcpLSPBinaryResponse, want, label string) {
	t.Helper()
	if !response.Result.IsError {
		t.Fatalf("%s unexpectedly succeeded: text=%q", label, response.Result.ContentText())
	}
	if !strings.Contains(response.Result.ContentText(), want) {
		t.Fatalf("%s error missing %q: text=%q", label, want, response.Result.ContentText())
	}
}

func requireAllLanguageToolContains(t *testing.T, response mcpLSPBinaryResponse, want, label string) {
	t.Helper()
	requireAllLanguageToolSuccess(t, response, label)
	if !strings.Contains(response.Result.ContentText(), want) {
		t.Fatalf("%s missing %q: text=%q", label, want, response.Result.ContentText())
	}
}

func requireAllLanguageToolTruncated(t *testing.T, response mcpLSPBinaryResponse, label string) {
	t.Helper()
	doc := requireAllLanguageLineProtocol(t, response, label)
	if !doc.Header.Truncated {
		t.Fatalf("%s missing truncated=true in content header: text=%q", label, response.Result.ContentText())
	}
}

func requireAllLanguageToolHint(t *testing.T, response mcpLSPBinaryResponse, label string) {
	t.Helper()
	doc := requireAllLanguageLineProtocol(t, response, label)
	for _, record := range doc.Records {
		if record.Kind == "HINT" && strings.Contains(record.Value, "max_results") {
			return
		}
	}
	t.Fatalf("%s missing max_results hint in content: text=%q", label, response.Result.ContentText())
}

func requireAllLanguageToolBool(t *testing.T, response mcpLSPBinaryResponse, key string, want bool, label string) {
	t.Helper()
	doc := requireAllLanguageLineProtocol(t, response, label)
	for _, record := range doc.Records {
		if value, ok := record.Fields[key]; ok {
			got, err := strconv.ParseBool(value)
			if err == nil && got == want {
				return
			}
			t.Fatalf("%s %s=%q, want %t: text=%q", label, key, value, want, response.Result.ContentText())
		}
	}
	t.Fatalf("%s missing %s=%t in content: text=%q", label, key, want, response.Result.ContentText())
}

func requireAllLanguageToolPositiveNumber(t *testing.T, response mcpLSPBinaryResponse, key, label string) {
	t.Helper()
	doc := requireAllLanguageLineProtocol(t, response, label)
	for _, record := range doc.Records {
		if value, ok := record.Fields[key]; ok {
			got, err := strconv.ParseFloat(value, 64)
			if err == nil && got > 0 {
				return
			}
			t.Fatalf("%s %s=%q, want positive number: text=%q", label, key, value, response.Result.ContentText())
		}
	}
	t.Fatalf("%s missing positive %s in content: text=%q", label, key, response.Result.ContentText())
}

func requireAllLanguageFileContains(t *testing.T, path, want, label string) {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%s read %s: %v", label, path, err)
	}
	if !bytes.Contains(raw, []byte(want)) {
		t.Fatalf("%s file %s missing %q: %q", label, path, want, raw)
	}
}

type allLanguageToolContractFixture struct {
	target, secondary              string
	replaceTarget, renameTarget    string
	codeActionTarget, formatTarget string
}

func writeAllLanguageToolContractFixture(t *testing.T, root, languageID string) allLanguageToolContractFixture {
	t.Helper()
	for name, content := range map[string]string{
		"go.mod":         "module contract.example/fake\n\ngo 1.22\n",
		"go.sum":         "contract.example/fake v0.0.0 h1:contract\n",
		"go.work":        "go 1.22\n\nuse .\n",
		"package.json":   "{\"name\":\"all-language-contract\",\"private\":true}\n",
		"tsconfig.json":  "{\"compilerOptions\":{\"strict\":true,\"noEmit\":true}}\n",
		"pyproject.toml": "[project]\nname=\"all-language-contract\"\nversion=\"0.0.0\"\n",
		"Cargo.toml":     "[package]\nname=\"all-language-contract\"\nversion=\"0.0.0\"\nedition=\"2021\"\n",
		"pom.xml":        "<project><modelVersion>4.0.0</modelVersion></project>\n",
	} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(content), 0o600); err != nil {
			t.Fatalf("write language marker %s: %v", name, err)
		}
	}
	if contract.IsMQLLanguageID(languageID) {
		if err := os.WriteFile(filepath.Join(root, "compile_flags.txt"), []byte("-x\nc++\n"), 0o600); err != nil {
			t.Fatalf("write MQL clangd compile flags: %v", err)
		}
	}
	srcDir := filepath.Join(root, "src")
	if err := os.MkdirAll(srcDir, 0o755); err != nil {
		t.Fatalf("mkdir contract fixture src: %v", err)
	}
	ext := allLanguageToolContractExtension(languageID)
	fixture := allLanguageToolContractFixture{
		target:           allLanguageToolContractFixturePath(srcDir, "contract", languageID, ext),
		secondary:        allLanguageToolContractFixturePath(srcDir, "contract-secondary", languageID, ext),
		replaceTarget:    allLanguageToolContractFixturePath(srcDir, "contract-replace", languageID, ext),
		renameTarget:     allLanguageToolContractFixturePath(srcDir, "contract-rename", languageID, ext),
		codeActionTarget: allLanguageToolContractFixturePath(srcDir, "contract-code-action", languageID, ext),
		formatTarget:     allLanguageToolContractFixturePath(srcDir, "contract-format", languageID, ext),
	}
	for _, path := range []string{
		fixture.target, fixture.secondary, fixture.replaceTarget,
		fixture.renameTarget, fixture.codeActionTarget, fixture.formatTarget,
	} {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("mkdir source fixture parent %s: %v", filepath.Dir(path), err)
		}
	}
	source := allLanguageToolContractSource(languageID)
	for _, path := range []string{fixture.target, fixture.secondary} {
		if err := os.WriteFile(path, []byte(source), 0o600); err != nil {
			t.Fatalf("write source fixture %s: %v", path, err)
		}
	}
	editContents := map[string]string{
		fixture.replaceTarget:    "CONTRACT_REPLACE\n",
		fixture.renameTarget:     "contractRename\n",
		fixture.codeActionTarget: "contractCodeAction\n",
		fixture.formatTarget:     "contractFormat\n",
	}
	if languageID == "gowork" {
		for path, marker := range editContents {
			editContents[path] = "go 1.22\n\n// " + strings.TrimSpace(marker) + "\nuse ../..\n"
		}
	}
	for path, content := range editContents {
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatalf("write edit fixture %s: %v", path, err)
		}
	}
	return fixture
}

func allLanguageToolContractFixturePath(srcDir, label, languageID, ext string) string {
	if base, ok := allLanguageToolContractFixedBasename(languageID); ok {
		// Go module-family language IDs are selected from exact basenames, not
		// extensions. Separate directories keep every edit fixture independent
		// while preserving the same explicit and inferred language route.
		return filepath.Join(srcDir, label, base)
	}
	return filepath.Join(srcDir, label+ext)
}

func allLanguageToolContractFixedBasename(languageID string) (string, bool) {
	switch languageID {
	case "gomod":
		return "go.mod", true
	case "gosum":
		return "go.sum", true
	case "gowork":
		return "go.work", true
	default:
		return "", false
	}
}

func allLanguageToolContractExtension(languageID string) string {
	switch languageID {
	case "go":
		return ".go"
	case "gomod", "gosum", "gowork":
		return ""
	case "javascript", "javascriptreact":
		if languageID == "javascriptreact" {
			return ".jsx"
		}
		return ".js"
	case "typescript", "typescriptreact":
		if languageID == "typescriptreact" {
			return ".tsx"
		}
		return ".ts"
	case "python":
		return ".py"
	case "css":
		return ".css"
	case "html":
		return ".html"
	case "json":
		return ".json"
	case "yaml":
		return ".yaml"
	case "markdown":
		return ".md"
	case "vue":
		return ".vue"
	case "svelte":
		return ".svelte"
	case "c":
		return ".c"
	case "cpp":
		return ".cpp"
	case "objective-c":
		return ".m"
	case "objective-cpp":
		return ".mm"
	case "mql", "mql4", "mq4":
		return ".mq4"
	case "mql5", "mq5":
		return ".mq5"
	case "mqh":
		return ".mqh"
	case "swift":
		return ".swift"
	case "csharp":
		return ".cs"
	case "php":
		return ".php"
	case "ruby":
		return ".rb"
	case "kotlin":
		return ".kt"
	case "dart":
		return ".dart"
	case "lua":
		return ".lua"
	case "dockerfile":
		return ".dockerfile"
	case "terraform":
		return ".tf"
	case "graphql":
		return ".graphql"
	case "prisma":
		return ".prisma"
	case "rust":
		return ".rs"
	case "java":
		return ".java"
	case "shellscript":
		return ".sh"
	case "proto":
		return ".proto"
	case "sql":
		return ".sql"
	default:
		return ".txt"
	}
}

func allLanguageToolContractSource(languageID string) string {
	switch languageID {
	case "go":
		return "package contract\n\nconst ContractNeedle = \"contractNeedle\"\n\nfunc ContractFunction() string {\n\treturn ContractNeedle\n}\n\nfunc ContractCaller() string {\n\treturn ContractFunction()\n}\n"
	case "gomod":
		return "module contract.example/fake\n// contractNeedle\n// function ContractFunction() { return contractNeedle; }\n// function ContractCaller() { return ContractFunction(); }\n\ngo 1.22\n"
	case "gowork":
		return "go 1.22\n// contractNeedle\n// function ContractFunction() { return contractNeedle; }\n// function ContractCaller() { return ContractFunction(); }\n\nuse ../..\n"
	case "json":
		// JSON/Markdown/YAML 的 document_symbol 由产品内置静态解析器负责；
		// 使用合法原生语法证明该 fallback，同时其余能力仍由 fake LSP 证明路由。
		return "{\n  \"ContractFunction\": \"contractNeedle\",\n  \"ContractCaller\": \"ContractFunction\"\n}\n"
	case "markdown":
		return "# contractNeedle\n## ContractFunction\n### ContractCaller\n"
	case "yaml":
		return "contractNeedle: contractNeedle\nContractFunction: contractNeedle\nContractCaller: ContractFunction\n"
	}
	return "contractNeedle = \"contractNeedle\"\nfunction ContractFunction() { return contractNeedle; }\nfunction ContractCaller() { return ContractFunction(); }\n"
}

// writeAllLanguageToolContractBinaries 只负责复制共享 fake binary；命名、执行权限和 URI 路径归一化由平台 companion 提供。
func writeAllLanguageToolContractBinaries(t *testing.T, root, languageID string) string {
	t.Helper()
	dir := t.TempDir()
	source, err := filepath.Abs(os.Args[0])
	if err != nil {
		t.Fatalf("resolve test binary: %v", err)
	}
	binaryBytes, err := os.ReadFile(source)
	if err != nil {
		t.Fatalf("read test binary for native fake executable: %v", err)
	}
	// Node-backed adapters still perform a runtime version probe before they
	// launch their configured server. This native shim answers that probe; it is
	// not used as an LSP implementation.
	names := []string{"sg", "node"}
	registry := multilsp.NewDefaultLanguageAdapterRegistry()
	for _, id := range requiresLSPClientLanguageIDs(t) {
		adapter, ok := registry.AdapterForLanguage(id)
		if !ok {
			t.Fatalf("language %q missing from default registry while provisioning fake binary", id)
		}
		command, err := adapter.ServerCommand(context.Background(), multilsp.ResolvedLanguageScope{
			LanguageID:            id,
			WorkspaceRoot:         root,
			LanguageWorkspaceRoot: root,
			ProjectRoot:           root,
		})
		if err != nil {
			t.Fatalf("resolve fake server command for %s: %v", id, err)
		}
		names = append(names, filepath.Base(command.Executable))
	}
	seen := map[string]struct{}{}
	for _, name := range names {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		if _, exists := seen[name]; exists {
			continue
		}
		seen[name] = struct{}{}
		destName := allLanguageToolContractExecutableName(name)
		dest := filepath.Join(dir, destName)
		if err := writeAllLanguageToolContractFile(dest, binaryBytes); err != nil {
			t.Fatalf("copy native fake executable %s: %v", dest, err)
		}
		if err := allLanguageToolContractPrepareExecutable(dest); err != nil {
			t.Fatalf("prepare native fake executable %s: %v", dest, err)
		}
	}
	return dir
}

func writeAllLanguageToolContractFile(path string, content []byte) error {
	if err := os.WriteFile(path, content, 0o700); err != nil {
		return err
	}
	return nil
}

// init 让复制后的 Go 测试二进制直接作为跨平台原生 fake LSP 子进程启动，不经过 shell；
// 必须在 testing.Main 运行无关测试前识别受控环境变量和 -listen 参数。
func init() {
	base := strings.ToLower(filepath.Base(os.Args[0]))
	if base == "node" || base == "node.exe" {
		// Keep the probe deterministic and independent of the host PATH. The
		// copied test binary is otherwise never entered as a language server.
		_, _ = fmt.Fprintln(os.Stdout, "v20.11.1")
		os.Exit(0)
	}
	if os.Getenv("MCP_LSP_CROSS_PLATFORM_ALL_LANGUAGE_CONTRACT_FAKE") != "1" && !allLanguageToolContractFakeListenInvocation() {
		return
	}
	if filepath.Base(os.Args[0]) == "sg" || filepath.Base(os.Args[0]) == "sg.exe" {
		runAllLanguageToolContractFakeSG()
	} else {
		runAllLanguageToolContractFakeServer()
	}
	os.Exit(0)
}

func allLanguageToolContractFakeListenInvocation() bool {
	for _, arg := range os.Args[1:] {
		if strings.HasPrefix(arg, "-listen=") {
			return true
		}
	}
	return false
}

// TestMcpLSPAllLanguageToolContractFakeLanguageServerProcessCrossPlatform 保留可由 Go 测试运行器显式启动的跨平台 fake LSP 进程入口。
func TestMcpLSPAllLanguageToolContractFakeLanguageServerProcessCrossPlatform(t *testing.T) {
	if os.Getenv("MCP_LSP_CROSS_PLATFORM_ALL_LANGUAGE_CONTRACT_FAKE") != "1" {
		return
	}
	if filepath.Base(os.Args[0]) == "sg" || filepath.Base(os.Args[0]) == "sg.exe" {
		runAllLanguageToolContractFakeSG()
	} else {
		runAllLanguageToolContractFakeServer()
	}
	os.Exit(0)
}

type allLanguageToolContractFakeRequest struct {
	JSONRPC string
	ID      json.RawMessage
	Method  string
	Params  json.RawMessage
}

type allLanguageToolContractFakeState struct {
	primaryURI     string
	primaryVersion int
}

func runAllLanguageToolContractFakeServer() {
	if endpoint := allLanguageToolContractListenEndpoint(); endpoint != "" {
		runAllLanguageToolContractFakeTCPServer(endpoint)
		return
	}
	runAllLanguageToolContractFakeLSPStream(os.Stdin, os.Stdout)
}

func allLanguageToolContractListenEndpoint() string {
	for _, arg := range os.Args[1:] {
		if strings.HasPrefix(arg, "-listen=") {
			return strings.TrimPrefix(arg, "-listen=")
		}
	}
	return ""
}

func runAllLanguageToolContractFakeTCPServer(endpoint string) {
	address, ok := strings.CutPrefix(endpoint, "tcp;")
	if !ok || address == "" {
		return
	}
	listener, err := net.Listen("tcp4", address)
	if err != nil {
		return
	}
	defer listener.Close()
	for {
		connection, err := listener.Accept()
		if err != nil {
			return
		}
		handled := runAllLanguageToolContractFakeLSPStream(connection, connection)
		_ = connection.Close()
		if handled {
			return
		}
	}
}

func runAllLanguageToolContractFakeLSPStream(input io.Reader, output io.Writer) bool {
	reader := bufio.NewReader(input)
	state := &allLanguageToolContractFakeState{}
	handled := false
	for {
		raw, err := readAllLanguageToolContractFrame(reader)
		if err != nil {
			return handled
		}
		var request allLanguageToolContractFakeRequest
		if err := json.Unmarshal(raw, &request); err != nil {
			continue
		}
		handled = true
		if request.Method == "exit" {
			return true
		}
		if len(bytes.TrimSpace(request.ID)) == 0 {
			if request.Method == "textDocument/didOpen" {
				uri := allLanguageToolContractRequestURI(request.Params)
				if uri != "" {
					state.primaryURI = uri
					state.primaryVersion = allLanguageToolContractRequestVersion(request.Params)
					_ = writeAllLanguageToolContractDiagnosticsTo(output, uri, state.primaryVersion)
				}
			}
			continue
		}
		result := allLanguageToolContractResult(request, state)
		_ = writeAllLanguageToolContractResponseTo(output, request.ID, result)
	}
}

func writeAllLanguageToolContractDiagnosticsTo(output io.Writer, uri string, version int) error {
	params := map[string]any{
		"uri": uri,
		"diagnostics": []any{map[string]any{
			"range":    allLanguageToolContractRange(),
			"severity": 1,
			"source":   "contract-fake",
			"code":     "CONTRACT-DIAG",
			"message":  "CONTRACT-DIAG",
		}},
	}
	if version > 0 {
		params["version"] = version
	}
	return writeAllLanguageToolContractMessageTo(output, map[string]any{
		"jsonrpc": "2.0",
		"method":  "textDocument/publishDiagnostics",
		"params":  params,
	})
}

func readAllLanguageToolContractFrame(reader *bufio.Reader) (json.RawMessage, error) {
	length := -1
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			return nil, err
		}
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			break
		}
		name, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		if strings.EqualFold(strings.TrimSpace(name), "Content-Length") {
			parsed, err := strconv.Atoi(strings.TrimSpace(value))
			if err != nil {
				return nil, err
			}
			length = parsed
		}
	}
	if length < 0 {
		return nil, errors.New("fake LSP message missing Content-Length")
	}
	body := make([]byte, length)
	if _, err := io.ReadFull(reader, body); err != nil {
		return nil, err
	}
	return json.RawMessage(body), nil
}

func writeAllLanguageToolContractResponse(id json.RawMessage, result any) error {
	return writeAllLanguageToolContractResponseTo(os.Stdout, id, result)
}

func writeAllLanguageToolContractResponseTo(output io.Writer, id json.RawMessage, result any) error {
	return writeAllLanguageToolContractMessageTo(output, map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"result":  result,
	})
}

func writeAllLanguageToolContractMessage(payload map[string]any) error {
	return writeAllLanguageToolContractMessageTo(os.Stdout, payload)
}

func writeAllLanguageToolContractMessageTo(output io.Writer, payload map[string]any) error {
	raw, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(output, "Content-Length: %d\r\n\r\n%s", len(raw), raw)
	return err
}

func allLanguageToolContractResult(request allLanguageToolContractFakeRequest, state *allLanguageToolContractFakeState) any {
	uri := allLanguageToolContractRequestURI(request.Params)
	if uri == "" {
		uri = state.primaryURI
	}
	if uri == "" {
		uri = "file:///contract-fake"
	}
	location := map[string]any{"uri": uri, "range": allLanguageToolContractRange()}
	item := map[string]any{
		"name":           "contract-call",
		"kind":           12,
		"uri":            uri,
		"range":          allLanguageToolContractRange(),
		"selectionRange": allLanguageToolContractRange(),
		"detail":         "contract-fake",
	}
	switch request.Method {
	case "initialize":
		return map[string]any{
			"capabilities": map[string]any{
				"textDocumentSync":       1,
				"hoverProvider":          true,
				"definitionProvider":     true,
				"implementationProvider": true,
				"typeDefinitionProvider": true,
				"signatureHelpProvider":  map[string]any{"triggerCharacters": []string{"("}},
				"referencesProvider":     true,
				"callHierarchyProvider":  true,
				"typeHierarchyProvider":  true,
				// Advertise pull diagnostics with the protocol's DiagnosticOptions
				// object; the fake also publishes push diagnostics on didOpen.
				"diagnosticProvider":      map[string]any{"interFileDependencies": false, "workspaceDiagnostics": false},
				"documentSymbolProvider":  true,
				"workspaceSymbolProvider": true,
				"foldingRangeProvider":    true,
				"semanticTokensProvider": map[string]any{
					"legend": map[string]any{"tokenTypes": []string{"variable"}, "tokenModifiers": []string{}},
					"full":   true,
				},
				"completionProvider":         map[string]any{"triggerCharacters": []string{"."}},
				"renameProvider":             true,
				"codeActionProvider":         true,
				"documentFormattingProvider": true,
			},
		}
	case "shutdown":
		return nil
	case "textDocument/hover":
		return map[string]any{"contents": map[string]any{"kind": "markdown", "value": "contract-hover"}}
	case "textDocument/definition", "textDocument/implementation", "textDocument/typeDefinition":
		return []any{location}
	case "textDocument/signatureHelp":
		return map[string]any{
			"signatures":      []any{map[string]any{"label": "contract-signature()"}},
			"activeSignature": 0,
			"activeParameter": 0,
		}
	case "textDocument/diagnostic":
		return map[string]any{
			"kind": "full",
			"items": []any{map[string]any{
				"range":    allLanguageToolContractRange(),
				"severity": 1,
				"source":   "contract-fake",
				"code":     "CONTRACT-DIAG",
				"message":  "CONTRACT-DIAG",
			}},
		}
	case "textDocument/references":
		return []any{map[string]any{"uri": allLanguageToolContractSecondaryURI(uri), "range": allLanguageToolContractRange()}, location}
	case "textDocument/prepareCallHierarchy", "textDocument/prepareTypeHierarchy":
		return []any{item}
	case "callHierarchy/incomingCalls":
		return []any{map[string]any{"from": item, "fromRanges": []any{allLanguageToolContractRange()}}}
	case "callHierarchy/outgoingCalls":
		return []any{map[string]any{"to": item, "fromRanges": []any{allLanguageToolContractRange()}}}
	case "typeHierarchy/supertypes", "typeHierarchy/subtypes":
		item["name"] = "contract-type"
		return []any{item}
	case "textDocument/documentSymbol":
		return []any{
			map[string]any{"name": "ContractFunction", "kind": 12, "range": allLanguageToolContractRange(), "selectionRange": allLanguageToolContractRange()},
			map[string]any{"name": "ContractDocument", "kind": 5, "range": allLanguageToolContractRange(), "selectionRange": allLanguageToolContractRange()},
		}
	case "workspace/symbol":
		return []any{
			map[string]any{"name": "ContractWorkspace", "kind": 12, "location": location},
			map[string]any{"name": "ContractWorkspaceSecondary", "kind": 12, "location": location},
		}
	case "textDocument/foldingRange":
		return []any{map[string]any{"startLine": 0, "startCharacter": 0, "endLine": 2, "endCharacter": 1}}
	case "textDocument/semanticTokens/full":
		return map[string]any{"data": []int{0, 0, 14, 1, 0}}
	case "textDocument/completion":
		return map[string]any{
			"isIncomplete": false,
			"items": []any{
				map[string]any{"label": "contractCompletion", "kind": 6},
				map[string]any{"label": "contractCompletionSecondary", "kind": 6},
				map[string]any{"label": "contractCompletionThird", "kind": 6},
			},
		}
	case "textDocument/prepareRename":
		return map[string]any{"range": allLanguageToolContractRange(), "placeholder": "contractRename"}
	case "textDocument/rename":
		return map[string]any{"changes": map[string]any{
			uri: []any{map[string]any{"range": allLanguageToolContractInsertRange(), "newText": allLanguageToolContractInsertedText(uri, "CONTRACT_RENAMED")}},
		}}
	case "textDocument/codeAction":
		return []any{map[string]any{
			"title": "CONTRACT_CODE_ACTION",
			"kind":  "quickfix",
			"edit": map[string]any{"changes": map[string]any{
				uri: []any{map[string]any{"range": allLanguageToolContractInsertRange(), "newText": allLanguageToolContractInsertedText(uri, "CONTRACT_CODE_ACTION")}},
			}},
		}}
	case "textDocument/formatting":
		return []any{map[string]any{"range": allLanguageToolContractInsertRange(), "newText": allLanguageToolContractInsertedText(uri, "CONTRACT_FORMATTED")}}
	default:
		return nil
	}
}

func allLanguageToolContractInsertedText(uri, marker string) string {
	if path := allLanguageToolContractPathFromURI(uri); filepath.Base(path) == "go.work" {
		return "// " + marker + "\n"
	}
	return marker + "\n"
}

func allLanguageToolContractRequestURI(raw json.RawMessage) string {
	var params map[string]any
	if json.Unmarshal(raw, &params) != nil {
		return ""
	}
	if document, ok := params["textDocument"].(map[string]any); ok {
		if uri, ok := document["uri"].(string); ok {
			return uri
		}
	}
	if item, ok := params["item"].(map[string]any); ok {
		if uri, ok := item["uri"].(string); ok {
			return uri
		}
	}
	return ""
}

func allLanguageToolContractRequestVersion(raw json.RawMessage) int {
	var params map[string]any
	if json.Unmarshal(raw, &params) != nil {
		return 0
	}
	document, ok := params["textDocument"].(map[string]any)
	if !ok {
		return 0
	}
	version, ok := document["version"].(float64)
	if !ok || version <= 0 || version != float64(int(version)) {
		return 0
	}
	return int(version)
}

func allLanguageToolContractRange() map[string]any {
	return map[string]any{
		"start": map[string]any{"line": 1, "character": 0},
		"end":   map[string]any{"line": 3, "character": 40},
	}
}

func allLanguageToolContractInsertRange() map[string]any {
	return map[string]any{
		"start": map[string]any{"line": 0, "character": 0},
		"end":   map[string]any{"line": 0, "character": 0},
	}
}

func allLanguageToolContractSecondaryURI(uri string) string {
	path := allLanguageToolContractPathFromURI(uri)
	if path == "" {
		return uri + "-secondary"
	}
	if base := filepath.Base(path); base == "go.mod" || base == "go.sum" || base == "go.work" {
		return allLanguageToolContractFileURI(filepath.Join(filepath.Dir(filepath.Dir(path)), "contract-secondary", base))
	}
	return allLanguageToolContractFileURI(filepath.Join(filepath.Dir(path), "contract-secondary"+filepath.Ext(path)))
}

func allLanguageToolContractPathFromURI(raw string) string {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme != "file" {
		return ""
	}
	path := parsed.Path
	return allLanguageToolContractNativePathFromURIPath(path)
}

func allLanguageToolContractFileURI(path string) string {
	return (&url.URL{Scheme: "file", Path: filepath.ToSlash(path)}).String()
}

func runAllLanguageToolContractFakeSG() {
	args := os.Args
	target := ""
	for i := len(args) - 1; i >= 0; i-- {
		if args[i] == "" || args[i] == "--" || strings.HasPrefix(args[i], "-") || args[i] == "run" || args[i] == "scan" {
			continue
		}
		target = args[i]
		break
	}
	if target == "" {
		return
	}
	match := map[string]any{
		"file":  target,
		"text":  "CONTRACT-AST",
		"lines": "CONTRACT-AST",
		"range": map[string]any{"start": map[string]any{"line": 0, "column": 0}},
	}
	if slices.Contains(args, "scan") {
		raw, _ := json.Marshal([]any{match})
		_, _ = os.Stdout.Write(raw)
		return
	}
	raw, _ := json.Marshal(match)
	_, _ = os.Stdout.Write(append(raw, '\n'))
}
