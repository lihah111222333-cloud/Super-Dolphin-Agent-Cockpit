//go:build e2e

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/internal/lspplatform"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/mcpserver/common/lineprotocol"
)

// lspBinaryClient is test-only stdio plumbing. It knows nothing about removed tools.
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
	IsError bool `json:"isError"`
}

type lspBinaryGrepResponse struct {
	Data      map[string]lspBinaryGrepFileRows
	Total     int
	Showing   int
	Truncated bool
	Hint      string
}

type lspBinaryGrepFileRows struct{ Rows [][]any }

func (r lspBinaryToolResult) ContentText() string {
	if len(r.Content) == 0 {
		return ""
	}
	return r.Content[0].Text
}

func startLSPBinaryClient(t *testing.T, root string) *lspBinaryClient {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	cmd := exec.CommandContext(ctx, buildLSPBinary(t))
	cmd.Dir, cmd.Env = root, lspBinaryEnv(t, root)
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
	c := &lspBinaryClient{cmd: cmd, stdin: stdin, decoder: json.NewDecoder(stdout), cancel: cancel, done: make(chan error, 1), stderr: stderr, root: root}
	go func() { c.done <- cmd.Wait() }()
	t.Cleanup(c.close)
	c.initialize(t)
	return c
}

func (c *lspBinaryClient) initialize(t *testing.T) {
	t.Helper()
	id := c.nextRequestID()
	c.send(t, map[string]any{"jsonrpc": "2.0", "id": id, "method": "initialize", "params": map[string]any{"protocolVersion": "2024-11-05"}})
	resp := c.recv(t)
	if resp.ID != id || resp.Error != nil {
		t.Fatalf("initialize response = %#v stderr=%s", resp, c.stderr.String())
	}
	c.send(t, map[string]any{"jsonrpc": "2.0", "method": "notifications/initialized", "params": map[string]any{}})
}

func (c *lspBinaryClient) callTool(t *testing.T, name string, arguments map[string]any) lspBinaryToolResult {
	t.Helper()
	id := c.nextRequestID()
	c.send(t, map[string]any{"jsonrpc": "2.0", "id": id, "method": "tools/call", "params": map[string]any{"name": name, "arguments": arguments, "_cwd": c.root, "_workspaceRoots": []string{c.root}}})
	resp := c.recv(t)
	if resp.ID != id {
		t.Fatalf("tools/call response id = %d, want %d; stderr=%s", resp.ID, id, c.stderr.String())
	}
	if resp.Error != nil {
		t.Fatalf("tools/call RPC error = %d %s; stderr=%s", resp.Error.Code, resp.Error.Message, c.stderr.String())
	}
	return resp.Result
}

// callToolWithoutTrustedScope is plumbing for scope-boundary tests. Callers
// must use it only with a supported tool or an explicit removed-tool guard.
func (c *lspBinaryClient) callToolWithoutTrustedScope(t *testing.T, name string, arguments map[string]any) lspBinaryToolResult {
	t.Helper()
	id := c.nextRequestID()
	c.send(t, map[string]any{"jsonrpc": "2.0", "id": id, "method": "tools/call", "params": map[string]any{"name": name, "arguments": arguments}})
	resp := c.recv(t)
	if resp.ID != id || resp.Error != nil {
		t.Fatalf("unscoped tools/call response = %#v; stderr=%s", resp, c.stderr.String())
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
func (c *lspBinaryClient) nextRequestID() int { c.nextID++; return c.nextID }
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
		t.Fatalf("read MCP response: %v; stderr=%s", err, c.stderr.String())
	}
	return resp
}

func buildLSPBinary(t *testing.T) string {
	t.Helper()
	binary := filepath.Join(t.TempDir(), lspBinaryExecutableNameForTest())
	cmd := exec.Command("go", "build", "-o", binary, ".")
	cmd.Dir = lspBinaryPackageDir(t)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build mcp-lsp binary: %v\n%s", err, output)
	}
	return binary
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
	root, err = lspplatform.CanonicalDirectoryPath(root)
	if err != nil {
		t.Fatalf("resolve repo root %s: %v", root, err)
	}
	return root
}
func lspBinaryEnv(t *testing.T, root string) []string {
	t.Helper()
	repoRoot := lspBinaryRepoRoot(t)
	env := make([]string, 0, len(os.Environ())+6)
	skip := lspBinaryEnvSkipKeys()
	for _, item := range os.Environ() {
		if !skip[lspBinaryEnvKey(item)] {
			env = append(env, item)
		}
	}
	roots, _ := json.Marshal([]string{root})
	return append(env, "GO_AGENT_LSP_ROOT="+root, "GO_AGENT_LSP_ROOTS="+string(roots), "PROJECT_ROOT="+repoRoot, "SUPER_DOLPHIN_RUNTIME_MODE=dev", "SUPER_DOLPHIN_RUNTIME_RESOURCES_DIR="+repoRoot, "SUPER_DOLPHIN_DEPENDENCY_PROFILE=production")
}
func lspBinaryEnvSkipKeys() map[string]bool {
	return map[string]bool{"GO_AGENT_LSP_ROOT": true, "GO_AGENT_LSP_ROOTS": true, "GO_AGENT_CTL_RPC_ADDR": true, "GO_AGENT_PEER_MODE": true, "MCP_LSP_GOPLS_DAEMON_IDLE_TIMEOUT": true, "MCP_LSP_IDLE_TIMEOUT": true, "PROJECT_ROOT": true, "SUPER_DOLPHIN_RUNTIME_MODE": true, "SUPER_DOLPHIN_RUNTIME_RESOURCES_DIR": true, "SUPER_DOLPHIN_LSP_BUNDLE_DIR": true, "SUPER_DOLPHIN_LSP_MANIFEST": true}
}
func lspBinaryEnvKey(item string) string { key, _, _ := strings.Cut(item, "="); return key }

func skipLSPBinaryResidualE2EInShortMode(t *testing.T) {
	t.Helper()
	if testing.Short() {
		t.Skip("skipping mcp-lsp binary e2e test in short mode")
	}
}

func parseLSPBinaryContent(t *testing.T, text string) lineprotocol.Document {
	t.Helper()
	doc, err := lineprotocol.Parse(text)
	if err != nil {
		t.Fatalf("parse line protocol: %v; content=%s", err, text)
	}
	return doc
}

// decodeLSPBinaryGrepContent is retained only while grep E2E files are being
// migrated to native os.ReadFile/bytes/strings assertions. It is test parsing
// support, not a public MCP contract assertion.
func decodeLSPBinaryGrepContent(t *testing.T, text string) lspBinaryGrepResponse {
	t.Helper()
	doc := parseLSPBinaryContent(t, text)
	if doc.Error != nil || doc.Header.Unit != "match" {
		t.Fatalf("grep content is not a successful match document: error=%#v header=%#v content=%s", doc.Error, doc.Header, text)
	}
	payload := lspBinaryGrepResponse{Data: make(map[string]lspBinaryGrepFileRows), Total: doc.Header.Total, Showing: doc.Header.Showing, Truncated: doc.Header.Truncated}
	for _, record := range doc.Records {
		switch record.Kind {
		case "HINT":
			payload.Hint = record.Value
		case "ROW":
			file := record.Fields["file"]
			if strings.TrimSpace(file) == "" {
				t.Fatalf("grep ROW lacks file field: %s", text)
			}
			rows := payload.Data[file]
			rows.Rows = append(rows.Rows, []any{record.Fields["line"], record.Fields["col"], record.Fields["text"]})
			payload.Data[file] = rows
		}
	}
	return payload
}
func writeLSPBinaryFixture(t *testing.T, path, contents string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
