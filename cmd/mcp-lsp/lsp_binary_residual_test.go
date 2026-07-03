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
)

func TestLSPBinaryPromptDocsUseReadFilePosContract(t *testing.T) {
	skipLSPBinaryResidualE2EInShortMode(t)
	repoRoot := lspBinaryRepoRoot(t)
	client := startLSPBinaryClient(t, repoRoot)

	result := client.callTool(t, "grep", map[string]any{
		"action":      "text_search",
		"query":       `offset=|read_file\([^)]*offset|read_file, offset`,
		"path":        filepath.Join(repoRoot, "internal/platform/shared/builtinprompts/assets"),
		"glob":        "*.md",
		"regex":       true,
		"max_results": 10,
	})
	if result.IsError {
		t.Fatalf("grep returned tool error: %s", result.ContentText())
	}
	var payload lspBinaryGrepResponse
	decodeLSPBinaryStructuredContent(t, result, &payload)
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
		"path":        root,
		"glob":        "*.txt",
		"max_results": 5,
	})
	if result.IsError {
		t.Fatalf("grep returned tool error: %s", result.ContentText())
	}
	var payload lspBinaryGrepResponse
	decodeLSPBinaryStructuredContent(t, result, &payload)
	if !payload.Truncated || payload.Total != 5 || payload.Showing != 5 {
		t.Fatalf("grep truncation payload = total:%d showing:%d truncated:%t, want 5/5/true; content=%s", payload.Total, payload.Showing, payload.Truncated, result.ContentText())
	}
	if strings.TrimSpace(payload.Hint) == "" {
		t.Fatalf("truncated grep response missing hint; structuredContent=%s", string(result.StructuredContent))
	}
	lowerHint := strings.ToLower(payload.Hint)
	if !strings.Contains(lowerHint, "max_results") || (!strings.Contains(lowerHint, "path") && !strings.Contains(lowerHint, "glob")) {
		t.Fatalf("grep truncation hint = %q, want guidance to raise max_results or narrow path/glob", payload.Hint)
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
		"path":        "first second",
		"glob":        "*.txt",
		"max_results": 10,
	})
	if result.IsError {
		t.Fatalf("grep returned tool error for whitespace-separated paths: %s; stderr=%s", result.ContentText(), client.stderr.String())
	}
	var payload lspBinaryGrepResponse
	decodeLSPBinaryStructuredContent(t, result, &payload)
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
			t.Fatalf("grep paths = %#v, missing %s; structuredContent=%s", got, want, string(result.StructuredContent))
		}
	}
	if got["third/skip.txt"] {
		t.Fatalf("grep paths = %#v, searched path outside requested scopes; structuredContent=%s", got, string(result.StructuredContent))
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
	client := startLSPBinaryClient(t, staleRoot)

	needle := "BenchmarkTickAppendStrictParallel"
	writeLSPBinaryFixture(t, currentPath, "existing TN integration notes\n"+needle+"\n")

	result := client.callToolWithoutTrustedScope(t, "grep", map[string]any{
		"action":      "text_search",
		"query":       needle,
		"path":        relPath,
		"max_results": 5,
	})
	if !result.IsError {
		t.Fatalf("grep without trusted scope returned success after external patch; structuredContent=%s stderr=%s",
			string(result.StructuredContent), client.stderr.String())
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
		t.Fatalf("xref at whitespace returned success, want cursor-position error; structuredContent=%s", string(bad.StructuredContent))
	}
	var envelope lspBinaryToolErrorEnvelope
	decodeLSPBinaryStructuredContent(t, bad, &envelope)
	switch envelope.Code {
	case "identifier_not_found", "invalid_position", "position_invalid":
	default:
		t.Fatalf("xref cursor miss code = %q, want identifier_not_found or invalid_position; envelope=%s", envelope.Code, string(bad.StructuredContent))
	}
	if envelope.Code == "file_not_found" {
		t.Fatalf("xref cursor miss was misclassified as file_not_found; envelope=%s", string(bad.StructuredContent))
	}
	lowerHint := strings.ToLower(envelope.Hint)
	if !strings.Contains(lowerHint, "identifier") || !strings.Contains(lowerHint, "column") {
		t.Fatalf("xref cursor miss hint = %q, want guidance to move column onto an identifier", envelope.Hint)
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
	if diagnostics.IsError {
		var envelope lspBinaryToolErrorEnvelope
		decodeLSPBinaryStructuredContent(t, diagnostics, &envelope)
		requireRustDetachedExplanation(t, envelope.Hint+" "+envelope.Error+" "+diagnostics.ContentText())
	} else {
		var payload struct {
			Meta struct {
				Message string `json:"message"`
			} `json:"meta"`
		}
		decodeLSPBinaryStructuredContent(t, diagnostics, &payload)
		requireRustDetachedExplanation(t, payload.Meta.Message)
	}

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
	Data      map[string]json.RawMessage `json:"data"`
	Total     int                        `json:"total"`
	Showing   int                        `json:"showing"`
	Truncated bool                       `json:"truncated"`
	Hint      string                     `json:"hint"`
}

type lspBinaryToolErrorEnvelope struct {
	Success bool   `json:"success"`
	Error   string `json:"error"`
	Code    string `json:"code"`
	Hint    string `json:"hint"`
}

func startLSPBinaryClient(t *testing.T, root string) *lspBinaryClient {
	t.Helper()
	binary := buildLSPBinary(t)
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	cmd := exec.CommandContext(ctx, binary)
	cmd.Dir = root
	cmd.Env = lspBinaryEnv(t, root)
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
	client := &lspBinaryClient{
		cmd:     cmd,
		stdin:   stdin,
		decoder: json.NewDecoder(stdout),
		cancel:  cancel,
		done:    make(chan error, 1),
		stderr:  stderr,
		root:    root,
	}
	go func() {
		client.done <- cmd.Wait()
	}()
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
	)
	return env
}

func lspBinaryEnvSkipKeys() map[string]bool {
	return map[string]bool{
		"GO_AGENT_LSP_ROOT":                   true,
		"GO_AGENT_LSP_ROOTS":                  true,
		"GO_AGENT_CTL_RPC_ADDR":               true,
		"GO_AGENT_PEER_MODE":                  true,
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

func decodeLSPBinaryStructuredContent(t *testing.T, result lspBinaryToolResult, out any) {
	t.Helper()
	if len(bytes.TrimSpace(result.StructuredContent)) == 0 {
		t.Fatalf("structuredContent is empty; content=%s", result.ContentText())
	}
	if err := json.Unmarshal(result.StructuredContent, out); err != nil {
		t.Fatalf("decode structuredContent as %T: %v; structuredContent=%s content=%s", out, err, string(result.StructuredContent), result.ContentText())
	}
}

func requireRustEmptyResultMessage(t *testing.T, result lspBinaryToolResult, capability string) {
	t.Helper()
	if result.IsError {
		var envelope lspBinaryToolErrorEnvelope
		decodeLSPBinaryStructuredContent(t, result, &envelope)
		requireRustDetachedExplanation(t, envelope.Hint+" "+envelope.Error+" "+result.ContentText())
		return
	}
	var payload struct {
		Success bool `json:"success"`
		Meta    struct {
			Message string `json:"message"`
		} `json:"meta"`
	}
	if err := json.Unmarshal(result.StructuredContent, &payload); err != nil {
		t.Fatalf("%s empty result must be structured with meta.message; decode error: %v; structuredContent=%s content=%s", capability, err, string(result.StructuredContent), result.ContentText())
	}
	if strings.TrimSpace(payload.Meta.Message) == "" {
		t.Fatalf("%s empty result missing meta.message; structuredContent=%s content=%s", capability, string(result.StructuredContent), result.ContentText())
	}
	requireRustDetachedExplanation(t, payload.Meta.Message)
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
