//go:build e2e

package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

const fakeJSTSLangserverEnv = "MCP_LSP_FAKE_JSTS_LANGSERVER"
const fakeJSTSCrashMethodEnv = "MCP_LSP_FAKE_JSTS_CRASH_METHOD"
const fakeJSTSCrashMarkerEnv = "MCP_LSP_FAKE_JSTS_CRASH_MARKER"
const fakeJSTSInstanceFileEnv = "MCP_LSP_FAKE_JSTS_INSTANCE_FILE"
const fakeJSTSGrandchildCrashEnv = "MCP_LSP_FAKE_JSTS_GRANDCHILD_CRASH"

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
	content, err := os.ReadFile(target)
	if err != nil || !bytes.Contains(content, []byte("guardTarget")) {
		t.Fatalf("native read/search MJS fixture: err=%v", err)
	}

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
func TestMcpLSPBinaryMJSDiagnosticsPropagatesTSServerCrashAndRebuilds_E2E(t *testing.T) {
	root := t.TempDir()
	target := writeMJSNoTypeScriptWorkspaceFixture(t, root)
	binary := buildMcpLSPBinaryForTest(t)
	fakeJSTSBinDir := writeFakeJSTSLangserver(t)
	crashMarker := filepath.Join(t.TempDir(), "crashed")
	instanceFile := filepath.Join(t.TempDir(), "instances")
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	client := startMcpLSPBinaryForTestWithEnv(t, ctx, binary, root, fakeJSTSBinDir, []string{
		fakeJSTSCrashMethodEnv + "=textDocument/didChange",
		fakeJSTSCrashMarkerEnv + "=" + crashMarker,
		fakeJSTSInstanceFileEnv + "=" + instanceFile,
		fakeJSTSGrandchildCrashEnv + "=1",
	})
	defer client.close(t)
	client.call(t, "initialize", map[string]any{"protocolVersion": "2024-11-05"})
	first := client.callTool(t, "diagnostics", map[string]any{"file_path": target, "language_id": "javascript"})
	if !first.Result.IsError || !strings.Contains(first.Result.ContentText(), "transport closed") {
		t.Fatalf("diagnostics did not propagate tsserver crash: text=%q structured=%s stderr=%s", first.Result.ContentText(), first.Result.StructuredContent, client.stderrString())
	}
	waitForFakeJSTSMarker(t, crashMarker)
	second := client.callTool(t, "diagnostics", map[string]any{"file_path": target, "language_id": "javascript"})
	if second.Result.IsError || !strings.Contains(second.Result.ContentText(), "OK total=0") {
		t.Fatalf("diagnostics did not recover on rebuilt language server: text=%q structured=%s stderr=%s", second.Result.ContentText(), second.Result.StructuredContent, client.stderrString())
	}
	waitForFakeJSTSInstanceCount(t, instanceFile, 2)
	waitForFakeJSTSReclaim(t, instanceFile)
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
	calls := mjsDiagnosticsToolCalls(target)
	calls = append(calls, mjsStructureToolCalls(target)...)
	calls = append(calls, mjsXrefToolCalls(pos)...)
	return calls
}

func mjsDiagnosticsToolCalls(target string) []mjsToolCallCase {
	return []mjsToolCallCase{
		{name: "diagnostics", tool: "diagnostics", arguments: map[string]any{
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
	recordFakeJSTSInstance()
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
		if fakeJSTSCrashMethodMatches(req) {
			emitFakeJSTSChildCrash()
		}
	}
}

func recordFakeJSTSInstance() {
	path := strings.TrimSpace(os.Getenv(fakeJSTSInstanceFileEnv))
	if path == "" {
		return
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to open instance file: %v\n", err)
		os.Exit(125)
	}
	if _, err := fmt.Fprintf(file, "%d\n", os.Getpid()); err != nil {
		_ = file.Close()
		fmt.Fprintf(os.Stderr, "failed to record instance: %v\n", err)
		os.Exit(125)
	}
	if err := file.Close(); err != nil {
		fmt.Fprintf(os.Stderr, "failed to close instance file: %v\n", err)
		os.Exit(125)
	}
}
func waitForFakeJSTSMarker(t *testing.T, path string) {
	t.Helper()
	ticker := time.NewTicker(10 * time.Millisecond)
	timeout := time.NewTimer(10 * time.Second)
	defer ticker.Stop()
	defer timeout.Stop()
	for {
		select {
		case <-ticker.C:
			data, err := os.ReadFile(path)
			if err == nil && strings.Contains(string(data), "crashed") {
				return
			}
		case <-timeout.C:
			t.Fatalf("timed out waiting for fake tsserver crash marker %s", path)
		}
	}
}
func waitForFakeJSTSInstanceCount(t *testing.T, path string, want int) {
	t.Helper()
	ticker := time.NewTicker(10 * time.Millisecond)
	timeout := time.NewTimer(10 * time.Second)
	defer ticker.Stop()
	defer timeout.Stop()
	for {
		select {
		case <-ticker.C:
			data, err := os.ReadFile(path)
			if err == nil && len(strings.Fields(string(data))) >= want {
				return
			}
		case <-timeout.C:
			t.Fatalf("timed out waiting for %d fake tsserver instances in %s", want, path)
		}
	}
}

func waitForFakeJSTSReclaim(t *testing.T, path string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read fake tsserver instances for reclaim: %v", err)
	}
	fields := strings.Fields(string(data))
	if len(fields) == 0 {
		t.Fatal("fake tsserver instance file was empty after rebuild")
	}
	pid, err := strconv.Atoi(fields[0])
	if err != nil {
		t.Fatalf("parse old fake tsserver pid %q: %v", fields[0], err)
	}
	ticker := time.NewTicker(10 * time.Millisecond)
	timeout := time.NewTimer(10 * time.Second)
	defer ticker.Stop()
	defer timeout.Stop()
	for {
		select {
		case <-ticker.C:
			alive, err := processAliveForE2E(pid)
			if err != nil {
				t.Fatalf("check old fake tsserver pid %d: %v", pid, err)
			}
			if !alive {
				return
			}
		case <-timeout.C:
			t.Fatalf("old fake tsserver pid %d remained alive after rebuild", pid)
		}
	}
}
func (s *fakeJSTSServer) handleNotification(req fakeLSPRequest) bool {
	if len(bytes.TrimSpace(req.ID)) != 0 {
		return false
	}
	if fakeJSTSCrashMethodMatches(req) {
		emitFakeJSTSChildCrash()
	}
	return req.Method == "initialized" || req.Method == "textDocument/didOpen" || req.Method == "textDocument/didChange"
}

func fakeJSTSCrashMethodMatches(req fakeLSPRequest) bool {
	crashMethod := strings.TrimSpace(os.Getenv(fakeJSTSCrashMethodEnv))
	return crashMethod != "" && req.Method == crashMethod
}

func emitFakeJSTSChildCrash() {
	marker := strings.TrimSpace(os.Getenv(fakeJSTSCrashMarkerEnv))
	if marker != "" && fakeJSTSMarkerExists(marker) {
		return
	}
	if marker != "" {
		if err := os.WriteFile(marker, []byte("crashed\n"), 0o600); err != nil {
			fmt.Fprintf(os.Stderr, "failed to write crash marker: %v\n", err)
		}
	}
	fmt.Fprintln(os.Stderr, "FATAL ERROR: JavaScript heap out of memory")
	fmt.Fprintln(os.Stderr, "[tsserver] Exited. Code: null. Signal: SIGABRT")
	if os.Getenv(fakeJSTSGrandchildCrashEnv) != "1" {
		os.Exit(134)
	}
}

func fakeJSTSMarkerExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
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
