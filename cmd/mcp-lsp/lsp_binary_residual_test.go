package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/mcpserver/common/lineprotocol"
)

func TestLSPBinaryPromptDocsUseReadFilePosContract(t *testing.T) {
	skipLSPBinaryResidualE2EInShortMode(t)
	repoRoot := lspBinaryRepoRoot(t)
	client := startLSPBinaryClient(t, repoRoot)

	result := client.callTool(t, "grep", map[string]any{
		"action":      "text_search",
		"query":       `offset=|read_file\([^)]*offset|read_file, offset`,
		"paths":       []string{filepath.Join(repoRoot, "internal/platform/shared/builtinprompts/assets")},
		"glob":        "*.md",
		"regex":       true,
		"max_results": 10,
	})
	if result.IsError {
		t.Fatalf("grep returned tool error: %s", result.ContentText())
	}
	payload := decodeLSPBinaryGrepContent(t, result.ContentText())
	if payload.Total != 0 {
		t.Fatalf("builtin prompt docs still mention removed read_file offset contract: total=%d content=%s", payload.Total, result.ContentText())
	}
}

func TestLSPBinaryGrepTruncatedTextSearchIncludesHint(t *testing.T) {
	skipLSPBinaryResidualE2EInShortMode(t)
	root := canonicalToolTestRoot(t, t.TempDir())
	writeLSPBinaryFixture(t, filepath.Join(root, "sample.txt"), strings.Repeat("needle\n", 6))
	client := startLSPBinaryClient(t, root)

	result := client.callTool(t, "grep", map[string]any{
		"action":      "text_search",
		"query":       "needle",
		"paths":       []string{root},
		"glob":        "*.txt",
		"max_results": 5,
	})
	if result.IsError {
		t.Fatalf("grep returned tool error: %s", result.ContentText())
	}
	payload := decodeLSPBinaryGrepContent(t, result.ContentText())
	if !payload.Truncated || payload.Total != 6 || payload.Showing != 5 {
		t.Fatalf("grep truncation payload = total:%d showing:%d truncated:%t, want 6/5/true; content=%s", payload.Total, payload.Showing, payload.Truncated, result.ContentText())
	}
	if strings.TrimSpace(payload.Hint) == "" {
		t.Fatalf("truncated grep response missing hint; content=%s", result.ContentText())
	}
	lowerHint := strings.ToLower(payload.Hint)
	if !strings.Contains(lowerHint, "max_results") || (!strings.Contains(lowerHint, "paths") && !strings.Contains(lowerHint, "glob")) {
		t.Fatalf("grep truncation hint = %q, want guidance to raise max_results or narrow paths/glob", payload.Hint)
	}
}

func TestLSPBinaryGrepSearchesWhitespaceSeparatedPaths(t *testing.T) {
	skipLSPBinaryResidualE2EInShortMode(t)
	root := canonicalToolTestRoot(t, t.TempDir())
	writeLSPBinaryFixture(t, filepath.Join(root, "first", "one.txt"), "needle from first\n")
	writeLSPBinaryFixture(t, filepath.Join(root, "second", "two.txt"), "needle from second\n")
	writeLSPBinaryFixture(t, filepath.Join(root, "third", "skip.txt"), "needle outside requested scopes\n")
	client := startLSPBinaryClient(t, root)

	result := client.callTool(t, "grep", map[string]any{
		"action":      "text_search",
		"query":       "needle",
		"paths":       []string{"first second"},
		"glob":        "*.txt",
		"max_results": 10,
	})
	if result.IsError {
		t.Fatalf("grep returned tool error for whitespace-separated paths: %s; stderr=%s", result.ContentText(), client.stderr.String())
	}
	payload := decodeLSPBinaryGrepContent(t, result.ContentText())
	if payload.Total != 2 || payload.Showing != 2 {
		t.Fatalf("grep whitespace-separated path payload = total:%d showing:%d, want 2/2; content=%s",
			payload.Total, payload.Showing, result.ContentText())
	}
	got := map[string]bool{}
	for path := range payload.Data {
		rel, err := filepath.Rel(root, path)
		if err != nil {
			t.Fatalf("relative grep path %q: %v", path, err)
		}
		got[filepath.ToSlash(rel)] = true
	}
	for _, want := range []string{"first/one.txt", "second/two.txt"} {
		if !got[want] {
			t.Fatalf("grep paths = %#v, missing %s; content=%s", got, want, result.ContentText())
		}
	}
	if got["third/skip.txt"] {
		t.Fatalf("grep paths = %#v, searched path outside requested scopes; content=%s", got, result.ContentText())
	}
}

func TestLSPBinaryGrepRejectsExternalPatchWithoutTrustedScope(t *testing.T) {
	skipLSPBinaryResidualE2EInShortMode(t)
	parent := t.TempDir()
	staleRoot := canonicalToolTestRoot(t, filepath.Join(parent, "stale"))
	currentRoot := canonicalToolTestRoot(t, filepath.Join(parent, "current"))
	relPath := filepath.Join("docs", "li", "p15", "TASKS", "TN-integration.md")
	stalePath := filepath.Join(staleRoot, relPath)
	currentPath := filepath.Join(currentRoot, relPath)
	writeLSPBinaryFixture(t, stalePath, "existing TN integration notes\n")
	writeLSPBinaryFixture(t, currentPath, "existing TN integration notes\n")
	client := startLSPBinaryClientWithoutEnvRoots(t, staleRoot)

	needle := "BenchmarkTickAppendStrictParallel"
	writeLSPBinaryFixture(t, currentPath, "existing TN integration notes\n"+needle+"\n")

	result := client.callToolWithoutTrustedScope(t, "grep", map[string]any{
		"action":      "text_search",
		"query":       needle,
		"paths":       []string{relPath},
		"max_results": 5,
	})
	if !result.IsError {
		t.Fatalf("grep without trusted scope returned success after external patch; content=%s stderr=%s",
			result.ContentText(), client.stderr.String())
	}
	if !strings.Contains(result.ContentText(), "stale workspace root") {
		t.Fatalf("grep without trusted scope error = %q, want stale workspace root guidance; stderr=%s",
			result.ContentText(), client.stderr.String())
	}
}

func TestLSPBinaryXrefIdentifierMissClassifiesCursorError(t *testing.T) {
	skipLSPBinaryResidualE2EInShortMode(t)
	root := canonicalToolTestRoot(t, t.TempDir())
	writeLSPBinaryFixture(t, filepath.Join(root, "go.mod"), "module example.com/lspbinary\n\ngo 1.25\n")
	target := filepath.Join(root, "main.go")
	writeLSPBinaryFixture(t, target, strings.Join([]string{
		"package main",
		"",
		"func handleFile() {",
		"\tprintln(\"ok\")",
		"}",
		"",
		"func main() {",
		"\thandleFile()",
		"}",
		"",
	}, "\n"))
	client := startLSPBinaryClient(t, root)

	good := client.callTool(t, "xref", map[string]any{
		"action":      "call_hierarchy",
		"direction":   "outgoing",
		"pos":         target + ":3:6",
		"max_results": 8,
	})
	if good.IsError {
		t.Fatalf("xref at identifier returned tool error: %s", good.ContentText())
	}

	bad := client.callTool(t, "xref", map[string]any{
		"action":      "call_hierarchy",
		"direction":   "outgoing",
		"pos":         target + ":3:5",
		"max_results": 8,
	})
	if !bad.IsError {
		t.Fatalf("xref at whitespace returned success, want cursor-position error; content=%s", bad.ContentText())
	}
	doc := parseLSPBinaryContent(t, bad.ContentText())
	if doc.Error == nil {
		t.Fatalf("xref cursor miss is not an ERROR line-protocol result: %s", bad.ContentText())
	}
	switch doc.Error.Code {
	case "identifier_not_found", "invalid_position", "position_invalid":
	default:
		t.Fatalf("xref cursor miss code = %q, want identifier_not_found or invalid_position; content=%s", doc.Error.Code, bad.ContentText())
	}
	if doc.Error.Code == "file_not_found" {
		t.Fatalf("xref cursor miss was misclassified as file_not_found; content=%s", bad.ContentText())
	}
	lowerHint := strings.ToLower(lineProtocolRecordValue(doc, "HINT"))
	if !strings.Contains(lowerHint, "identifier") || !strings.Contains(lowerHint, "column") {
		t.Fatalf("xref cursor miss hint = %q, want guidance to move column onto an identifier", lowerHint)
	}
}

func TestLSPBinaryXrefIdentifierMissSuggestsImplementationMethodColumn(t *testing.T) {
	skipLSPBinaryResidualE2EInShortMode(t)
	root := canonicalToolTestRoot(t, t.TempDir())
	writeLSPBinaryFixture(t, filepath.Join(root, "go.mod"), "module example.com/lspbinary\n\ngo 1.25\n")
	target := filepath.Join(root, "adapter.go")
	writeLSPBinaryFixture(t, target, strings.Join([]string{
		"package lspbinary",
		"",
		"type projectLanguageAdapter struct{}",
		"",
		"func (a projectLanguageAdapter) ResolveRoot() {}",
		"",
		"func resolveRoot() {",
		"\tprojectLanguageAdapter{}.ResolveRoot()",
		"}",
		"",
	}, "\n"))
	client := startLSPBinaryClient(t, root)

	result := client.callTool(t, "xref", map[string]any{
		"action":      "references",
		"pos":         target + ":5:32",
		"max_results": 8,
	})
	if !result.IsError {
		t.Fatalf("xref at declaration whitespace returned success, want identifier_not_found; content=%s", result.ContentText())
	}
	doc := parseLSPBinaryContent(t, result.ContentText())
	if doc.Error == nil || doc.Error.Code != "identifier_not_found" {
		got := ""
		if doc.Error != nil {
			got = doc.Error.Code
		}
		t.Fatalf("xref declaration whitespace code = %q, want identifier_not_found; content=%s", got, result.ContentText())
	}
	if hint := strings.ToLower(lineProtocolRecordValue(doc, "HINT")); !strings.Contains(hint, "column") {
		t.Fatalf("xref declaration whitespace hint = %q, want column guidance", hint)
	}
}

func TestLSPBinaryRustDetachedFileExplainsLimitedWorkspace(t *testing.T) {
	skipLSPBinaryResidualE2EInShortMode(t)
	requireRealRustAnalyzerToolchain(t)
	root := canonicalToolTestRoot(t, t.TempDir())
	target := filepath.Join(root, "docs", "li", "lsp_probe_eval.rs")
	writeLSPBinaryFixture(t, target, strings.Join([]string{
		"struct ProbeUser {",
		"    name: String,",
		"}",
		"",
		"fn describe_user(user: &ProbeUser) -> String {",
		"    user.name.clone()",
		"}",
		"",
	}, "\n"))
	client := startLSPBinaryClient(t, root)

	open := client.callTool(t, "file", map[string]any{
		"action":    "open_file",
		"file_path": target,
	})
	if open.IsError {
		t.Fatalf("open_file returned tool error: %s", open.ContentText())
	}
	read := client.callTool(t, "file", map[string]any{
		"action": "read_file",
		"pos":    target + ":5",
		"limit":  20,
	})
	if read.IsError {
		t.Fatalf("read_file returned tool error: %s", read.ContentText())
	}
	outline := client.callTool(t, "structure", map[string]any{
		"action":    "document_symbol",
		"file_path": target,
	})
	if outline.IsError {
		t.Fatalf("document_symbol returned tool error: %s", outline.ContentText())
	}
	if text := outline.ContentText(); !strings.Contains(text, "ProbeUser") || !strings.Contains(text, "describe_user") {
		t.Fatalf("document_symbol content = %s, want ProbeUser and describe_user", text)
	}

	diagnostics := client.callTool(t, "file", map[string]any{
		"action":    "diagnostics",
		"file_path": target,
	})
	parseLSPBinaryContent(t, diagnostics.ContentText())
	requireRustDetachedExplanation(t, diagnostics.ContentText())

	hover := client.callTool(t, "inspect", map[string]any{
		"action": "hover",
		"pos":    target + ":5:4",
	})
	requireRustEmptyResultMessage(t, hover, "hover")

	completion := client.callTool(t, "completion", map[string]any{
		"pos":         target + ":6:10",
		"max_results": 5,
	})
	requireRustEmptyResultMessage(t, completion, "completion")
}

func skipLSPBinaryResidualE2EInShortMode(t *testing.T) {
	t.Helper()
	if testing.Short() {
		t.Skip("skipping mcp-lsp binary e2e test in short mode")
	}
}

type lspBinaryClient struct {
	cmd     *exec.Cmd
	stdin   io.WriteCloser
	decoder *json.Decoder
	cancel  context.CancelFunc
	done    chan error
	stderr  *bytes.Buffer
	nextID  int
	root    string
}

type lspBinaryRPCResponse struct {
	ID     int                 `json:"id"`
	Result lspBinaryToolResult `json:"result"`
	Error  *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

type lspBinaryToolResult struct {
	Content []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content"`
	StructuredContent json.RawMessage `json:"structuredContent"`
	IsError           bool            `json:"isError"`
}

func (r lspBinaryToolResult) ContentText() string {
	if len(r.Content) == 0 {
		return ""
	}
	return r.Content[0].Text
}

type lspBinaryGrepResponse struct {
	Data      map[string]lspBinaryGrepFileRows
	Total     int
	Showing   int
	Truncated bool
	Hint      string
}

type lspBinaryGrepFileRows struct {
	Rows [][]any
}

func startLSPBinaryClient(t *testing.T, root string) *lspBinaryClient {
	return startLSPBinaryClientWithEnv(t, root, lspBinaryEnv(t, root))
}

func startLSPBinaryClientWithoutEnvRoots(t *testing.T, root string) *lspBinaryClient {
	return startLSPBinaryClientWithEnv(t, root, lspBinaryEnvWithoutRoots(t, root))
}

func startLSPBinaryClientWithEnv(t *testing.T, root string, env []string) *lspBinaryClient {
	t.Helper()
	binary := buildLSPBinary(t)
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	cmd := exec.CommandContext(ctx, binary)
	cmd.Dir = root
	cmd.Env = env
	stdin, err := cmd.StdinPipe()
	if err != nil {
		cancel()
		t.Fatalf("open mcp-lsp stdin: %v", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		cancel()
		t.Fatalf("open mcp-lsp stdout: %v", err)
	}
	stderr := &bytes.Buffer{}
	cmd.Stderr = stderr
	if err := cmd.Start(); err != nil {
		cancel()
		t.Fatalf("start mcp-lsp binary: %v", err)
	}
	goroutines := newTestGoroutineGroup(t)
	client := &lspBinaryClient{
		cmd:     cmd,
		stdin:   stdin,
		decoder: json.NewDecoder(stdout),
		cancel:  cancel,
		done:    make(chan error, 1),
		stderr:  stderr,
		root:    root,
	}
	goroutines.Go(func() {
		client.done <- cmd.Wait()
	})
	t.Cleanup(func() {
		client.close()
	})
	client.initialize(t)
	return client
}

func (c *lspBinaryClient) initialize(t *testing.T) {
	t.Helper()
	id := c.nextRequestID()
	c.send(t, map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"method":  "initialize",
		"params": map[string]any{
			"protocolVersion": "2024-11-05",
		},
	})
	resp := c.recv(t)
	if resp.ID != id || resp.Error != nil {
		t.Fatalf("initialize response = %#v stderr=%s", resp, c.stderr.String())
	}
	c.send(t, map[string]any{
		"jsonrpc": "2.0",
		"method":  "notifications/initialized",
		"params":  map[string]any{},
	})
}

func (c *lspBinaryClient) callTool(t *testing.T, name string, arguments map[string]any) lspBinaryToolResult {
	t.Helper()
	id := c.nextRequestID()
	c.send(t, map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"method":  "tools/call",
		"params": map[string]any{
			"name":            name,
			"arguments":       arguments,
			"_cwd":            c.root,
			"_workspaceRoots": []string{c.root},
		},
	})
	resp := c.recv(t)
	if resp.ID != id {
		t.Fatalf("tools/call response id = %d, want %d; stderr=%s", resp.ID, id, c.stderr.String())
	}
	if resp.Error != nil {
		t.Fatalf("tools/call RPC error = %d %s; stderr=%s", resp.Error.Code, resp.Error.Message, c.stderr.String())
	}
	return resp.Result
}

func (c *lspBinaryClient) callToolWithoutTrustedScope(t *testing.T, name string, arguments map[string]any) lspBinaryToolResult {
	t.Helper()
	id := c.nextRequestID()
	c.send(t, map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"method":  "tools/call",
		"params": map[string]any{
			"name":      name,
			"arguments": arguments,
		},
	})
	resp := c.recv(t)
	if resp.ID != id {
		t.Fatalf("tools/call response id = %d, want %d; stderr=%s", resp.ID, id, c.stderr.String())
	}
	if resp.Error != nil {
		t.Fatalf("tools/call RPC error = %d %s; stderr=%s", resp.Error.Code, resp.Error.Message, c.stderr.String())
	}
	return resp.Result
}

func (c *lspBinaryClient) close() {
	c.cancel()
	_ = c.stdin.Close()
	select {
	case <-c.done:
	case <-time.After(5 * time.Second):
		if c.cmd.Process != nil {
			_ = c.cmd.Process.Kill()
		}
		<-c.done
	}
}

func (c *lspBinaryClient) nextRequestID() int {
	c.nextID++
	return c.nextID
}

func (c *lspBinaryClient) send(t *testing.T, payload map[string]any) {
	t.Helper()
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal MCP request: %v", err)
	}
	if _, err := c.stdin.Write(append(raw, '\n')); err != nil {
		t.Fatalf("write MCP request: %v stderr=%s", err, c.stderr.String())
	}
}

func (c *lspBinaryClient) recv(t *testing.T) lspBinaryRPCResponse {
	t.Helper()
	var resp lspBinaryRPCResponse
	if err := c.decoder.Decode(&resp); err != nil {
		select {
		case waitErr := <-c.done:
			t.Fatalf("read MCP response: %v; process exited: %v; stderr=%s", err, waitErr, c.stderr.String())
		default:
			t.Fatalf("read MCP response: %v; stderr=%s", err, c.stderr.String())
		}
	}
	return resp
}

func buildLSPBinary(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	binary := filepath.Join(dir, lspBinaryExecutableNameForTest())
	cmd := exec.Command("go", "build", "-o", binary, ".")
	cmd.Dir = lspBinaryPackageDir(t)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("build mcp-lsp binary: %v\n%s", err, string(output))
	}
	return binary
}

func lspBinaryExecutableNameForTest() string {
	if runtime.GOOS == "windows" {
		return "mcp-lsp.exe"
	}
	return "mcp-lsp"
}

func lspBinaryPackageDir(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if filepath.Base(wd) == "mcp-lsp" {
		return wd
	}
	return filepath.Join(lspBinaryRepoRoot(t), "cmd", "mcp-lsp")
}

func lspBinaryRepoRoot(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	root := filepath.Clean(filepath.Join(wd, "..", ".."))
	if filepath.Base(wd) != "mcp-lsp" {
		root = wd
	}
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatalf("resolve repo root %s: %v", root, err)
	}
	return root
}

func lspBinaryEnv(t *testing.T, root string) []string {
	t.Helper()
	repoRoot := lspBinaryRepoRoot(t)
	env := make([]string, 0, len(os.Environ())+5)
	skip := lspBinaryEnvSkipKeys()
	for _, item := range os.Environ() {
		if skip[lspBinaryEnvKey(item)] {
			continue
		}
		env = append(env, item)
	}
	roots, _ := json.Marshal([]string{root})
	env = append(env,
		"GO_AGENT_LSP_ROOT="+root,
		"GO_AGENT_LSP_ROOTS="+string(roots),
		"PROJECT_ROOT="+repoRoot,
		"SUPER_DOLPHIN_RUNTIME_MODE=dev",
		"SUPER_DOLPHIN_RUNTIME_RESOURCES_DIR="+repoRoot,
		"SUPER_DOLPHIN_DEPENDENCY_PROFILE=production",
	)
	return env
}

func lspBinaryEnvWithoutRoots(t *testing.T, root string) []string {
	t.Helper()
	repoRoot := lspBinaryRepoRoot(t)
	env := make([]string, 0, len(os.Environ())+5)
	skip := lspBinaryEnvSkipKeys()
	for _, item := range os.Environ() {
		if skip[lspBinaryEnvKey(item)] {
			continue
		}
		env = append(env, item)
	}
	env = append(env,
		"GO_AGENT_LSP_ROOT="+root,
		"PROJECT_ROOT="+repoRoot,
		"SUPER_DOLPHIN_RUNTIME_MODE=dev",
		"SUPER_DOLPHIN_RUNTIME_RESOURCES_DIR="+repoRoot,
		"SUPER_DOLPHIN_DEPENDENCY_PROFILE=production",
	)
	return env
}

func lspBinaryEnvSkipKeys() map[string]bool {
	return map[string]bool{
		"GO_AGENT_LSP_ROOT":                   true,
		"GO_AGENT_LSP_ROOTS":                  true,
		"GO_AGENT_CTL_RPC_ADDR":               true,
		"GO_AGENT_PEER_MODE":                  true,
		"MCP_LSP_GOPLS_DAEMON_IDLE_TIMEOUT":   true,
		"MCP_LSP_IDLE_TIMEOUT":                true,
		"PROJECT_ROOT":                        true,
		"SUPER_DOLPHIN_RUNTIME_MODE":          true,
		"SUPER_DOLPHIN_RUNTIME_RESOURCES_DIR": true,
		"SUPER_DOLPHIN_LSP_BUNDLE_DIR":        true,
		"SUPER_DOLPHIN_LSP_MANIFEST":          true,
	}
}

func lspBinaryEnvKey(item string) string {
	key, _, _ := strings.Cut(item, "=")
	return key
}

func parseLSPBinaryContent(t *testing.T, text string) lineprotocol.Document {
	t.Helper()
	doc, err := lineprotocol.Parse(text)
	if err != nil {
		t.Fatalf("parse line protocol: %v; content=%s", err, text)
	}
	return doc
}

func decodeLSPBinaryGrepContent(t *testing.T, text string) lspBinaryGrepResponse {
	t.Helper()
	doc := parseLSPBinaryContent(t, text)
	if doc.Error != nil || doc.Header.Unit != "match" {
		t.Fatalf("grep content is not a successful match document: error=%#v header=%#v content=%s", doc.Error, doc.Header, text)
	}
	payload := lspBinaryGrepResponse{
		Data:      make(map[string]lspBinaryGrepFileRows),
		Total:     doc.Header.Total,
		Showing:   doc.Header.Showing,
		Truncated: doc.Header.Truncated,
	}
	for _, record := range doc.Records {
		switch record.Kind {
		case "HINT":
			payload.Hint = record.Value
		case "ROW":
			file := record.Fields["file"]
			if strings.TrimSpace(file) == "" {
				t.Fatalf("grep ROW lacks file field: %s", text)
			}
			row := []any{record.Fields["line"], record.Fields["col"], record.Fields["text"]}
			if start, end := record.Fields["func_start"], record.Fields["func_end"]; start != "" || end != "" {
				row = append(row, start, end)
			}
			rows := payload.Data[file]
			rows.Rows = append(rows.Rows, row)
			payload.Data[file] = rows
		}
	}
	return payload
}

func lineProtocolRecordValue(doc lineprotocol.Document, kind string) string {
	values := make([]string, 0, len(doc.Records))
	for _, record := range doc.Records {
		if record.Kind == kind && strings.TrimSpace(record.Value) != "" {
			values = append(values, record.Value)
		}
	}
	return strings.Join(values, " ")
}

func requireRustEmptyResultMessage(t *testing.T, result lspBinaryToolResult, _ string) {
	t.Helper()
	parseLSPBinaryContent(t, result.ContentText())
	if result.IsError {
		requireRustDetachedExplanation(t, result.ContentText())
		return
	}
	requireRustDetachedExplanation(t, result.ContentText())
}

func requireRealRustAnalyzerToolchain(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("rust-analyzer"); err == nil {
		return
	}
	if _, err := exec.LookPath("rustup"); err == nil {
		return
	}
	t.Skip("rust-analyzer or rustup is required for real Rust detached-file e2e")
}

func requireRustDetachedExplanation(t *testing.T, text string) {
	t.Helper()
	lower := strings.ToLower(text)
	if strings.TrimSpace(lower) == "" {
		t.Fatalf("Rust detached-file explanation is empty")
	}
	if !strings.Contains(lower, "rust") && !strings.Contains(lower, "cargo") && !strings.Contains(lower, "workspace") && !strings.Contains(lower, "rust-analyzer") {
		t.Fatalf("Rust detached-file explanation = %q, want Rust/Cargo/workspace/rust-analyzer context", text)
	}
}

func writeLSPBinaryFixture(t *testing.T, path string, contents string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
