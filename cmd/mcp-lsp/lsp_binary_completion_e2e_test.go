//go:build e2e
// +build e2e

package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestMcpLSPBinaryPythonConstantIdentifierCompletion_E2E(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping mcp-lsp binary e2e test in short mode")
	}

	root := t.TempDir()
	target := writePythonConstantCompletionFixture(t, root)
	binary := buildMcpLSPBinaryForTest(t)
	fakePyrightBinDir := writeFakePyrightLangserver(t)

	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Second)
	defer cancel()
	client := startMcpLSPBinaryForTest(t, ctx, binary, root, fakePyrightBinDir)
	defer client.close(t)

	client.call(t, "initialize", map[string]any{"protocolVersion": "2024-11-05"})
	client.waitForHoverText(t, target+":10:1", "REG_CN", 120*time.Second)

	completion := client.callTool(t, "completion", map[string]any{
		"pos":         target + ":10:7",
		"max_results": 10,
	})
	if completion.Result.IsError {
		t.Fatalf("completion returned MCP error result; text=%q structured=%s stderr=%s",
			completion.Result.ContentText(), completion.Result.StructuredContent, client.stderrString())
	}
	labels := completionLabelsFromStructuredContent(t, completion.Result.StructuredContent)
	for _, label := range labels {
		if label == "REG_CN" {
			return
		}
	}
	t.Fatalf("completion at Python constant identifier %s:10:7 returned labels %v, want REG_CN; structured=%s text=%q stderr=%s",
		target, labels, completion.Result.StructuredContent, completion.Result.ContentText(), client.stderrString())
}

func TestFakePyrightLangserverHelper(t *testing.T) {
	if os.Getenv("MCP_LSP_FAKE_PYRIGHT") != "1" {
		return
	}
	runFakePyrightLangserver()
	os.Exit(0)
}

type mcpLSPBinaryClient struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout *bufio.Reader
	stderr lockedStringBuilder
}

type mcpLSPBinaryResponse struct {
	JSONRPC string                 `json:"jsonrpc"`
	ID      int                    `json:"id"`
	Result  mcpLSPBinaryToolResult `json:"result"`
	Error   *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

type mcpLSPBinaryToolResult struct {
	Content []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content"`
	StructuredContent json.RawMessage `json:"structuredContent"`
	IsError           bool            `json:"isError"`
}

func (r mcpLSPBinaryToolResult) ContentText() string {
	var parts []string
	for _, item := range r.Content {
		if item.Text != "" {
			parts = append(parts, item.Text)
		}
	}
	return strings.Join(parts, "\n")
}

func buildMcpLSPBinaryForTest(t *testing.T) string {
	t.Helper()
	repoRoot := repoRootForMcpLSPBinaryTest(t)
	output := filepath.Join(t.TempDir(), "mcp-lsp")
	cmd := exec.Command("go", "build", "-o", output, "./cmd/mcp-lsp")
	cmd.Dir = repoRoot
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build mcp-lsp binary: %v\n%s", err, out)
	}
	return output
}

func startMcpLSPBinaryForTest(t *testing.T, ctx context.Context, binary, root, fakePyrightBinDir string) *mcpLSPBinaryClient {
	t.Helper()
	repoRoot := repoRootForMcpLSPBinaryTest(t)
	rawRoots, err := json.Marshal([]string{root})
	if err != nil {
		t.Fatalf("marshal roots: %v", err)
	}
	cmd := exec.CommandContext(ctx, binary)
	cmd.Dir = root
	cmd.Env = append(os.Environ(),
		"GO_AGENT_LSP_ROOT="+root,
		"GO_AGENT_LSP_ROOTS="+string(rawRoots),
		"GO_AGENT_PEER_MODE=0",
		"PATH="+fakePyrightBinDir+string(os.PathListSeparator)+os.Getenv("PATH"),
		"SUPER_DOLPHIN_RUNTIME_MODE=dev",
		"SUPER_DOLPHIN_RUNTIME_RESOURCES_DIR="+repoRoot,
	)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatalf("stdin pipe: %v", err)
	}
	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatalf("stdout pipe: %v", err)
	}
	stderrPipe, err := cmd.StderrPipe()
	if err != nil {
		t.Fatalf("stderr pipe: %v", err)
	}
	client := &mcpLSPBinaryClient{
		cmd:    cmd,
		stdin:  stdin,
		stdout: bufio.NewReader(stdoutPipe),
	}
	if err := cmd.Start(); err != nil {
		t.Fatalf("start mcp-lsp binary: %v", err)
	}
	go func() {
		_, _ = io.Copy(&client.stderr, stderrPipe)
	}()
	return client
}

func writeFakePyrightLangserver(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "pyright-langserver")
	script := "#!/bin/sh\nMCP_LSP_FAKE_PYRIGHT=1 exec " + shellQuote(os.Args[0]) + " -test.run=TestFakePyrightLangserverHelper -- \"$@\"\n"
	if err := os.WriteFile(path, []byte(script), 0o700); err != nil {
		t.Fatalf("write fake pyright-langserver: %v", err)
	}
	return dir
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}

func repoRootForMcpLSPBinaryTest(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	return filepath.Clean(filepath.Join(wd, "..", ".."))
}

func (c *mcpLSPBinaryClient) callTool(t *testing.T, name string, args map[string]any) mcpLSPBinaryResponse {
	t.Helper()
	return c.call(t, "tools/call", map[string]any{
		"name":            name,
		"arguments":       args,
		"_cwd":            c.cmd.Dir,
		"_workspaceRoots": []string{c.cmd.Dir},
	})
}

func (c *mcpLSPBinaryClient) waitForHoverText(t *testing.T, pos, want string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	var last mcpLSPBinaryResponse
	for {
		last = c.callTool(t, "inspect", map[string]any{
			"action": "hover",
			"pos":    pos,
		})
		if !last.Result.IsError &&
			(strings.Contains(last.Result.ContentText(), want) ||
				strings.Contains(string(last.Result.StructuredContent), want)) {
			return
		}
		if !completionToolResultHasCode(last.Result.StructuredContent, "lsp_timeout") {
			t.Fatalf("hover readiness check did not resolve %s; text=%q structured=%s stderr=%s",
				want, last.Result.ContentText(), last.Result.StructuredContent, c.stderrString())
		}
		if time.Now().After(deadline) {
			t.Fatalf("hover readiness timed out waiting for %s; last text=%q structured=%s stderr=%s",
				want, last.Result.ContentText(), last.Result.StructuredContent, c.stderrString())
		}
		time.Sleep(500 * time.Millisecond)
	}
}

func completionToolResultHasCode(raw json.RawMessage, code string) bool {
	var payload struct {
		Code string `json:"code"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return false
	}
	return payload.Code == code
}

func (c *mcpLSPBinaryClient) call(t *testing.T, method string, params map[string]any) mcpLSPBinaryResponse {
	t.Helper()
	req := map[string]any{
		"jsonrpc": "2.0",
		"id":      time.Now().UnixNano(),
		"method":  method,
		"params":  params,
	}
	raw, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("marshal %s request: %v", method, err)
	}
	if _, err := c.stdin.Write(append(raw, '\n')); err != nil {
		t.Fatalf("write %s request: %v; stderr=%s", method, err, c.stderrString())
	}
	var resp mcpLSPBinaryResponse
	line, err := c.stdout.ReadBytes('\n')
	if err != nil {
		t.Fatalf("read %s response: %v; stderr=%s", method, err, c.stderrString())
	}
	if err := json.Unmarshal(line, &resp); err != nil {
		t.Fatalf("unmarshal %s response: %v; raw=%s stderr=%s", method, err, line, c.stderrString())
	}
	if resp.Error != nil {
		t.Fatalf("%s returned JSON-RPC error %d: %s; stderr=%s",
			method, resp.Error.Code, resp.Error.Message, c.stderrString())
	}
	return resp
}

func (c *mcpLSPBinaryClient) close(t *testing.T) {
	t.Helper()
	if c == nil || c.cmd == nil {
		return
	}
	raw, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"method":  "exit",
	})
	_, _ = c.stdin.Write(append(raw, '\n'))
	_ = c.stdin.Close()
	done := make(chan error, 1)
	go func() { done <- c.cmd.Wait() }()
	select {
	case err := <-done:
		if err != nil && !errors.Is(err, os.ErrProcessDone) {
			t.Logf("mcp-lsp binary exited with %v; stderr=%s", err, c.stderrString())
		}
	case <-time.After(5 * time.Second):
		_ = c.cmd.Process.Kill()
		t.Logf("killed mcp-lsp binary after shutdown timeout; stderr=%s", c.stderrString())
	}
}

func (c *mcpLSPBinaryClient) stderrString() string {
	if c == nil {
		return ""
	}
	return c.stderr.String()
}

type lockedStringBuilder struct {
	mu      sync.Mutex
	builder strings.Builder
}

func (b *lockedStringBuilder) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.builder.Write(p)
}

func (b *lockedStringBuilder) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.builder.String()
}

func completionLabelsFromStructuredContent(t *testing.T, raw json.RawMessage) []string {
	t.Helper()
	var payload struct {
		Data []struct {
			Label string `json:"label"`
		} `json:"data"`
		Meta struct {
			Message string `json:"message"`
		} `json:"meta"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatalf("unmarshal completion structuredContent: %v; raw=%s", err, raw)
	}
	labels := make([]string, 0, len(payload.Data))
	for _, item := range payload.Data {
		labels = append(labels, item.Label)
	}
	if len(labels) == 0 && payload.Meta.Message != "" {
		labels = append(labels, fmt.Sprintf("<empty: %s>", payload.Meta.Message))
	}
	return labels
}

func writePythonConstantCompletionFixture(t *testing.T, root string) string {
	t.Helper()
	pyproject := filepath.Join(root, "pyproject.toml")
	if err := os.WriteFile(pyproject, []byte("[project]\nname = \"constant-completion-e2e\"\nversion = \"0.0.0\"\n"), 0o600); err != nil {
		t.Fatalf("write pyproject fixture: %v", err)
	}
	target := filepath.Join(root, "qlib2_lite", "constant.py")
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		t.Fatalf("mkdir fixture dir: %v", err)
	}
	content := `# Copyright (c) Microsoft Corporation.
# Licensed under the MIT License.

# REGION CONST
from typing import TypeVar

REG_ALPHA = "alpha"
REG_BETA = "beta"

REG_CN = "cn"
REG_US = "us"
REG_TW = "tw"
REG_CRYPTO = "crypto"

# Epsilon for avoiding division by zero.
EPS = 1e-12

# Infinity in integer
INF = int(1e18)
REGION = TypeVar("REGION", str, bytes)
`
	if err := os.WriteFile(target, []byte(content), 0o600); err != nil {
		t.Fatalf("write Python fixture: %v", err)
	}
	return target
}

type fakeLSPRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type fakeLSPPositionParams struct {
	Position struct {
		Line      int `json:"line"`
		Character int `json:"character"`
	} `json:"position"`
}

func runFakePyrightLangserver() {
	reader := bufio.NewReader(os.Stdin)
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
		if len(bytes.TrimSpace(req.ID)) == 0 {
			continue
		}
		_ = writeFakeLSPResponse(os.Stdout, req.ID, fakeLSPResult(req))
	}
}

func readFakeLSPFramedMessage(reader *bufio.Reader) (json.RawMessage, error) {
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
		return nil, errors.New("missing Content-Length")
	}
	body := make([]byte, length)
	if _, err := io.ReadFull(reader, body); err != nil {
		return nil, err
	}
	return json.RawMessage(body), nil
}

func writeFakeLSPResponse(writer io.Writer, id json.RawMessage, result any) error {
	raw, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"result":  result,
	})
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintf(writer, "Content-Length: %d\r\n\r\n", len(raw)); err != nil {
		return err
	}
	_, err = writer.Write(raw)
	return err
}

func fakeLSPResult(req fakeLSPRequest) any {
	switch req.Method {
	case "initialize":
		return map[string]any{
			"capabilities": map[string]any{
				"textDocumentSync": 1,
				"hoverProvider":    true,
				"completionProvider": map[string]any{
					"triggerCharacters": []string{"."},
				},
			},
		}
	case "shutdown":
		return nil
	case "textDocument/hover":
		var params fakeLSPPositionParams
		_ = json.Unmarshal(req.Params, &params)
		if params.Position.Line == 9 && params.Position.Character <= 6 {
			return map[string]any{
				"contents": map[string]any{
					"kind":  "markdown",
					"value": "```python\nREG_CN: Literal['cn']\n```",
				},
			}
		}
		return nil
	case "textDocument/completion":
		var params fakeLSPPositionParams
		_ = json.Unmarshal(req.Params, &params)
		if params.Position.Line == 9 && params.Position.Character == 5 {
			return map[string]any{
				"isIncomplete": false,
				"items": []map[string]any{{
					"label":  "REG_CN",
					"kind":   6,
					"detail": "constant",
				}},
			}
		}
		return map[string]any{
			"isIncomplete": false,
			"items":        []any{},
		}
	default:
		return nil
	}
}
