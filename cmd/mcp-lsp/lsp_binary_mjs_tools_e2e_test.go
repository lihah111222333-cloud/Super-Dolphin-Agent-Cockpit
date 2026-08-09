//go:build e2e

package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

const fakeJSTSLangserverEnv = "MCP_LSP_FAKE_JSTS_LANGSERVER"

func TestMcpLSPBinaryMJSToolsUseConfiguredTSServerFallback_E2E(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping mcp-lsp binary e2e test in short mode")
	}

	root := t.TempDir()
	target := writeMJSNoTypeScriptWorkspaceFixture(t, root)
	binary := buildMcpLSPBinaryForTest(t)
	fakeJSTSBinDir := writeFakeJSTSLangserver(t)

	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Second)
	defer cancel()
	client := startMcpLSPBinaryForTestWithEnv(t, ctx, binary, root, fakeJSTSBinDir, nil)
	defer client.close(t)

	client.call(t, "initialize", map[string]any{"protocolVersion": "2024-11-05"})

	for _, tc := range mjsClientBackedToolCalls(target) {
		t.Run(tc.name, func(t *testing.T) {
			resp := client.callTool(t, tc.tool, tc.arguments)
			if resp.Result.IsError {
				t.Fatalf("%s returned MCP error; text=%q structured=%s stderr=%s",
					tc.name, resp.Result.ContentText(), resp.Result.StructuredContent, client.stderrString())
			}
		})
	}
}

// super-dolphin-ci: helper
func TestFakeJSTSLangserverHelper(t *testing.T) {
	if os.Getenv(fakeJSTSLangserverEnv) != "1" {
		return
	}
	runFakeJSTSLangserver()
	os.Exit(0)
}

type mjsToolCallCase struct {
	name      string
	tool      string
	arguments map[string]any
}

func mjsClientBackedToolCalls(target string) []mjsToolCallCase {
	pos := target + ":2:16"
	calls := mjsFileToolCalls(target)
	calls = append(calls, mjsStructureToolCalls(target)...)
	calls = append(calls, mjsInspectToolCalls(pos)...)
	calls = append(calls, mjsXrefToolCalls(pos)...)
	calls = append(calls, mjsCompletionAndEditToolCalls(target, pos)...)
	return calls
}

func mjsFileToolCalls(target string) []mjsToolCallCase {
	return []mjsToolCallCase{
		{name: "file open_file", tool: "file", arguments: map[string]any{
			"action":      "open_file",
			"file_path":   target,
			"language_id": "javascript",
		}},
		{name: "file diagnostics", tool: "file", arguments: map[string]any{
			"action":      "diagnostics",
			"file_path":   target,
			"language_id": "javascript",
		}},
	}
}

func mjsStructureToolCalls(target string) []mjsToolCallCase {
	return []mjsToolCallCase{
		{name: "structure document_symbol", tool: "structure", arguments: map[string]any{
			"action":      "document_symbol",
			"file_path":   target,
			"language_id": "javascript",
		}},
		{name: "structure workspace_symbol", tool: "structure", arguments: map[string]any{
			"action":      "workspace_symbol",
			"file_path":   target,
			"query":       "guardTarget",
			"max_results": 10,
		}},
		{name: "structure folding_range", tool: "structure", arguments: map[string]any{
			"action":      "folding_range",
			"file_path":   target,
			"language_id": "javascript",
		}},
		{name: "structure semantic_tokens", tool: "structure", arguments: map[string]any{
			"action":      "semantic_tokens",
			"file_path":   target,
			"language_id": "javascript",
		}},
	}
}

func mjsInspectToolCalls(pos string) []mjsToolCallCase {
	return []mjsToolCallCase{
		{name: "inspect hover", tool: "inspect", arguments: map[string]any{
			"action":      "hover",
			"pos":         pos,
			"language_id": "javascript",
		}},
		{name: "inspect definition", tool: "inspect", arguments: map[string]any{
			"action":      "definition",
			"pos":         pos,
			"language_id": "javascript",
		}},
		{name: "inspect implementation", tool: "inspect", arguments: map[string]any{
			"action":      "implementation",
			"pos":         pos,
			"language_id": "javascript",
		}},
		{name: "inspect type_definition", tool: "inspect", arguments: map[string]any{
			"action":      "type_definition",
			"pos":         pos,
			"language_id": "javascript",
		}},
		{name: "inspect signature_help", tool: "inspect", arguments: map[string]any{
			"action":      "signature_help",
			"pos":         pos,
			"language_id": "javascript",
		}},
	}
}

func mjsXrefToolCalls(pos string) []mjsToolCallCase {
	return []mjsToolCallCase{
		{name: "xref references", tool: "xref", arguments: map[string]any{
			"action":      "references",
			"pos":         pos,
			"language_id": "javascript",
		}},
		{name: "xref call_hierarchy", tool: "xref", arguments: map[string]any{
			"action":      "call_hierarchy",
			"pos":         pos,
			"language_id": "javascript",
			"direction":   "both",
		}},
		{name: "xref type_hierarchy", tool: "xref", arguments: map[string]any{
			"action":      "type_hierarchy",
			"pos":         pos,
			"language_id": "javascript",
			"direction":   "both",
		}},
	}
}

func mjsCompletionAndEditToolCalls(target, pos string) []mjsToolCallCase {
	return []mjsToolCallCase{
		{name: "completion", tool: "completion", arguments: map[string]any{
			"pos":         pos,
			"language_id": "javascript",
			"max_results": 10,
		}},
		{name: "patch_edit code_action", tool: "patch_edit", arguments: map[string]any{
			"action":      "code_action",
			"pos":         pos,
			"language_id": "javascript",
		}},
		{name: "patch_edit format", tool: "patch_edit", arguments: map[string]any{
			"action":      "format",
			"file_path":   target,
			"language_id": "javascript",
		}},
		{name: "patch_edit rename", tool: "patch_edit", arguments: map[string]any{
			"action":      "rename",
			"pos":         pos,
			"language_id": "javascript",
			"new_name":    "renamedGuardTarget",
		}},
	}
}

func writeMJSNoTypeScriptWorkspaceFixture(t *testing.T, root string) string {
	t.Helper()
	if err := os.WriteFile(filepath.Join(root, "package.json"), []byte(`{"name":"mjs-no-typescript"}`), 0o600); err != nil {
		t.Fatalf("write package.json: %v", err)
	}
	target := filepath.Join(root, "scripts", "frontend_code_guard.mjs")
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		t.Fatalf("mkdir mjs fixture: %v", err)
	}
	content := strings.Join([]string{
		"export function guardTarget(value) {",
		"  return value?.kind === 'contract' ? value.name : 'missing';",
		"}",
		"",
		"guardTarget({ kind: 'contract', name: 'ok' });",
		"",
	}, "\n")
	if err := os.WriteFile(target, []byte(content), 0o600); err != nil {
		t.Fatalf("write mjs fixture: %v", err)
	}
	return target
}

func writeFakeJSTSLangserver(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	script := "#!/bin/sh\n" + fakeJSTSLangserverEnv + "=1 exec " + shellQuote(os.Args[0]) + " -test.run=TestFakeJSTSLangserverHelper -- \"$@\"\n"
	for _, name := range []string{"typescript-language-server", "tsserver"} {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte(script), 0o700); err != nil {
			t.Fatalf("write fake %s: %v", name, err)
		}
	}
	return dir
}

type fakeJSTSServer struct {
	writer *fakeLSPWriter
}

func runFakeJSTSLangserver() {
	reader := bufio.NewReader(os.Stdin)
	var goroutines sync.WaitGroup
	defer goroutines.Wait()
	server := &fakeJSTSServer{writer: &fakeLSPWriter{w: os.Stdout, goroutines: &goroutines}}
	for {
		raw, err := readFakeLSPFramedMessage(reader)
		if err != nil {
			return
		}
		var req fakeLSPRequest
		if err := json.Unmarshal(raw, &req); err != nil {
			continue
		}
		if req.Method == "exit" {
			return
		}
		if server.handleNotification(req) {
			continue
		}
		if len(bytes.TrimSpace(req.ID)) == 0 {
			continue
		}
		if req.Method == "initialize" && !fakeJSTSInitializeHasTSServerFallback(req.Params) {
			_ = server.writer.writeError(req.ID, -32002, "Could not find a valid TypeScript installation; missing tsserver.fallbackPath")
			continue
		}
		_ = server.writer.writeResponse(req.ID, fakeJSTSResult(req))
	}
}

func (s *fakeJSTSServer) handleNotification(req fakeLSPRequest) bool {
	if len(bytes.TrimSpace(req.ID)) != 0 {
		return false
	}
	return req.Method == "initialized" || req.Method == "textDocument/didOpen" || req.Method == "textDocument/didChange"
}

func (w *fakeLSPWriter) writeError(id json.RawMessage, code int, message string) error {
	return w.write(map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"error": map[string]any{
			"code":    code,
			"message": message,
		},
	})
}

func fakeJSTSInitializeHasTSServerFallback(raw json.RawMessage) bool {
	var params struct {
		InitializationOptions struct {
			TSServer struct {
				Path         string `json:"path"`
				FallbackPath string `json:"fallbackPath"`
			} `json:"tsserver"`
		} `json:"initializationOptions"`
	}
	if err := json.Unmarshal(raw, &params); err != nil {
		return false
	}
	return strings.TrimSpace(params.InitializationOptions.TSServer.Path) != "" ||
		strings.TrimSpace(params.InitializationOptions.TSServer.FallbackPath) != ""
}

var fakeJSTSResultHandlers = map[string]func(fakeLSPRequest) any{
	"initialize":                        func(fakeLSPRequest) any { return fakeJSTSInitializeResult() },
	"textDocument/hover":                fakeJSTSHoverResult,
	"textDocument/definition":           fakeJSTSLocationsResult,
	"textDocument/implementation":       fakeJSTSLocationsResult,
	"textDocument/typeDefinition":       fakeJSTSLocationsResult,
	"textDocument/references":           fakeJSTSLocationsResult,
	"textDocument/documentSymbol":       fakeJSTSDocumentSymbolResult,
	"workspace/symbol":                  fakeJSTSWorkspaceSymbolResult,
	"textDocument/foldingRange":         func(fakeLSPRequest) any { return []map[string]any{{"startLine": 0, "endLine": 2}} },
	"textDocument/semanticTokens/full":  func(fakeLSPRequest) any { return map[string]any{"data": []int{}} },
	"textDocument/completion":           fakeJSTSCompletionResult,
	"textDocument/signatureHelp":        fakeJSTSSignatureHelpResult,
	"textDocument/prepareCallHierarchy": func(fakeLSPRequest) any { return []any{} },
	"textDocument/prepareTypeHierarchy": func(fakeLSPRequest) any { return []any{} },
	"textDocument/codeAction":           func(fakeLSPRequest) any { return []any{} },
	"textDocument/formatting":           func(fakeLSPRequest) any { return []any{} },
	"textDocument/rename":               func(fakeLSPRequest) any { return map[string]any{"changes": map[string]any{}} },
	"textDocument/diagnostic":           func(fakeLSPRequest) any { return map[string]any{"kind": "full", "items": []any{}} },
	"shutdown":                          func(fakeLSPRequest) any { return nil },
}

func fakeJSTSResult(req fakeLSPRequest) any {
	handler := fakeJSTSResultHandlers[req.Method]
	if handler == nil {
		return nil
	}
	return handler(req)
}

func fakeJSTSInitializeResult() any {
	return map[string]any{
		"capabilities": map[string]any{
			"textDocumentSync":           1,
			"hoverProvider":              true,
			"definitionProvider":         true,
			"implementationProvider":     true,
			"typeDefinitionProvider":     true,
			"referencesProvider":         true,
			"documentSymbolProvider":     true,
			"workspaceSymbolProvider":    true,
			"foldingRangeProvider":       true,
			"renameProvider":             true,
			"codeActionProvider":         true,
			"documentFormattingProvider": true,
			"completionProvider":         map[string]any{"triggerCharacters": []string{"."}},
			"signatureHelpProvider":      map[string]any{"triggerCharacters": []string{"("}},
			"semanticTokensProvider": map[string]any{
				"legend": map[string]any{
					"tokenTypes":     []string{"variable"},
					"tokenModifiers": []string{},
				},
				"full": true,
			},
			"callHierarchyProvider": true,
			"typeHierarchyProvider": true,
			"diagnosticProvider": map[string]any{
				"interFileDependencies": false,
				"workspaceDiagnostics":  false,
			},
		},
	}
}

func fakeJSTSHoverResult(fakeLSPRequest) any {
	return map[string]any{"contents": map[string]any{"kind": "markdown", "value": "```javascript\nguardTarget(value: object): string\n```"}}
}

func fakeJSTSLocationsResult(req fakeLSPRequest) any {
	return []map[string]any{fakeJSTSLocation(req)}
}

func fakeJSTSDocumentSymbolResult(fakeLSPRequest) any {
	return []map[string]any{{
		"name":           "guardTarget",
		"kind":           12,
		"range":          fakeJSTSRange(0, 0, 2, 1),
		"selectionRange": fakeJSTSRange(0, 16, 0, 27),
	}}
}

func fakeJSTSWorkspaceSymbolResult(req fakeLSPRequest) any {
	return []map[string]any{{
		"name": "guardTarget",
		"kind": 12,
		"location": map[string]any{
			"uri":   fakeJSTSTargetURI(req),
			"range": fakeJSTSRange(0, 0, 2, 1),
		},
	}}
}

func fakeJSTSCompletionResult(fakeLSPRequest) any {
	return map[string]any{
		"isIncomplete": false,
		"items": []map[string]any{{
			"label":  "guardTarget",
			"kind":   3,
			"detail": "function",
		}},
	}
}

func fakeJSTSSignatureHelpResult(fakeLSPRequest) any {
	return map[string]any{"signatures": []map[string]any{{"label": "guardTarget(value)"}}}
}

func fakeJSTSLocation(req fakeLSPRequest) map[string]any {
	return map[string]any{
		"uri":   fakeJSTSTargetURI(req),
		"range": fakeJSTSRange(0, 16, 0, 27),
	}
}

func fakeJSTSTargetURI(req fakeLSPRequest) string {
	var params struct {
		TextDocument struct {
			URI string `json:"uri"`
		} `json:"textDocument"`
	}
	_ = json.Unmarshal(req.Params, &params)
	return params.TextDocument.URI
}

func fakeJSTSRange(startLine, startChar, endLine, endChar int) map[string]any {
	return map[string]any{
		"start": map[string]any{"line": startLine, "character": startChar},
		"end":   map[string]any{"line": endLine, "character": endChar},
	}
}
