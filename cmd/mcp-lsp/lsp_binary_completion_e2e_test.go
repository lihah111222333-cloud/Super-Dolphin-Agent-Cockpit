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
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/internal/lspplatform"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/mcpserver/common/lineprotocol"
)

// TestMcpLSPBinaryStdioRemainsAvailablePastLegacyProcessIdleTimeout_E2E 防止进程级空闲回收关闭 agent 持有的 stdio transport。
func TestMcpLSPBinaryStdioRemainsAvailablePastLegacyProcessIdleTimeout_E2E(t *testing.T) {
	root := t.TempDir()
	binary := buildMcpLSPBinaryForTest(t)

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	client := startMcpLSPBinaryForTestWithEnv(t, ctx, binary, root, t.TempDir(), []string{
		"MCP_LSP_PROCESS_IDLE_TIMEOUT=40ms",
	})
	defer client.close(t)

	client.call(t, "initialize", map[string]any{"protocolVersion": "2024-11-05"})
	time.Sleep(200 * time.Millisecond)
	if raw := callMcpLSPBinaryRaw(t, client, "tools/list", map[string]any{}); len(raw) == 0 {
		t.Fatal("tools/list returned an empty result")
	}
}

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
		t.Fatalf("completion returned MCP error result; text=%q stderr=%s",
			completion.Result.ContentText(), client.stderrString())
	}
	labels := completionLabelsFromContent(t, completion)
	if slices.Contains(labels, "REG_CN") {
		return
	}
	t.Fatalf("completion at Python constant identifier %s:10:7 returned labels %v, want REG_CN; text=%q stderr=%s",
		target, labels, completion.Result.ContentText(), client.stderrString())
}

// super-dolphin-ci: helper
func TestFakePyrightLangserverHelper(t *testing.T) {
	if os.Getenv("MCP_LSP_FAKE_PYRIGHT") != "1" {
		return
	}
	runFakePyrightLangserver()
	os.Exit(0)
}

type mcpLSPBinaryClient struct {
	cmd       *exec.Cmd
	stdin     io.WriteCloser
	stdout    *bufio.Reader
	stderr    lockedStringBuilder
	closeHook func() error
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
	return buildMcpLSPBinaryForTestWithTags(t, "")
}

// buildMcpLSPShortIdlePrecheckBinaryForTest 构建显式允许十五分钟以下生命周期的快速预检二进制；该产物不得用于正式生命周期证明。
func buildMcpLSPShortIdlePrecheckBinaryForTest(t *testing.T) string {
	t.Helper()
	t.Log("status=NON_PASS_PRECHECK_ONLY: short-idle binary cannot prove the formal 15-minute lifecycle")
	return buildMcpLSPBinaryForTestWithTags(t, "mcp_lsp_short_idle_precheck")
}

func buildMcpLSPBinaryForTestWithTags(t *testing.T, buildTags string) string {
	t.Helper()
	repoRoot := repoRootForMcpLSPBinaryTest(t)
	output := filepath.Join(t.TempDir(), lspBinaryExecutableNameForTest())
	// E2E 只验证刚编译出的 sidecar。禁用 VCS stamp，避免 WSL 在 /mnt/c
	// 工作区为构建信息执行一次高成本的 `git status`，污染 LSP 冷启动耗时。
	args := []string{"build", "-buildvcs=false"}
	if buildTags != "" {
		args = append(args, "-tags="+buildTags)
	}
	args = append(args, "-o", output, "./cmd/mcp-lsp")
	cmd := exec.Command("go", args...)
	cmd.Dir = repoRoot
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build mcp-lsp binary: %v\n%s", err, out)
	}
	return output
}

func startMcpLSPBinaryForTest(t *testing.T, ctx context.Context, binary, root, fakePyrightBinDir string) *mcpLSPBinaryClient {
	return startMcpLSPBinaryForTestWithEnv(t, ctx, binary, root, fakePyrightBinDir, nil)
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
	root, err := lspplatform.CanonicalDirectoryPath(filepath.Clean(filepath.Join(wd, "..", "..")))
	if err != nil {
		t.Fatalf("canonicalize repository root: %v", err)
	}
	return root
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

func callMCPToolForScopedE2E(client *mcpLSPBinaryClient, name string, args map[string]any, cwd string, workspaceRoots []string) (mcpLSPBinaryResponse, error) {
	params := map[string]any{"name": name, "arguments": args, "_cwd": cwd, "_workspaceRoots": workspaceRoots}
	req := map[string]any{"jsonrpc": "2.0", "id": time.Now().UnixNano(), "method": "tools/call", "params": params}
	raw, err := json.Marshal(req)
	if err != nil {
		return mcpLSPBinaryResponse{}, fmt.Errorf("marshal tools/call request: %w", err)
	}
	if _, err := client.stdin.Write(append(raw, '\n')); err != nil {
		return mcpLSPBinaryResponse{}, fmt.Errorf("write tools/call request: %w", err)
	}
	line, err := client.stdout.ReadBytes('\n')
	if err != nil {
		return mcpLSPBinaryResponse{}, fmt.Errorf("read tools/call response: %w", err)
	}
	var response mcpLSPBinaryResponse
	if err := json.Unmarshal(line, &response); err != nil {
		return mcpLSPBinaryResponse{}, fmt.Errorf("unmarshal tools/call response: %w", err)
	}
	if response.Error != nil {
		return mcpLSPBinaryResponse{}, fmt.Errorf("tools/call JSON-RPC error %d: %s", response.Error.Code, response.Error.Message)
	}
	return response, nil
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
		if !last.Result.IsError && strings.Contains(last.Result.ContentText(), want) {
			return
		}
		if !completionToolResultHasCode(last.Result.ContentText(), "lsp_timeout") {
			t.Fatalf("hover readiness check did not resolve %s; text=%q stderr=%s",
				want, last.Result.ContentText(), c.stderrString())
		}
		if time.Now().After(deadline) {
			t.Fatalf("hover readiness timed out waiting for %s; last text=%q stderr=%s",
				want, last.Result.ContentText(), c.stderrString())
		}
		time.Sleep(500 * time.Millisecond)
	}
}

func completionToolResultHasCode(text, code string) bool {
	doc, err := lineprotocol.Parse(text)
	if err != nil || doc.Error == nil {
		return false
	}
	return doc.Error.Code == code
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

func callMcpLSPBinaryRaw(t *testing.T, client *mcpLSPBinaryClient, method string, params map[string]any) json.RawMessage {
	t.Helper()
	req := map[string]any{"jsonrpc": "2.0", "id": time.Now().UnixNano(), "method": method, "params": params}
	raw, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("marshal %s request: %v", method, err)
	}
	if _, err := client.stdin.Write(append(raw, '\n')); err != nil {
		t.Fatalf("write %s request: %v; stderr=%s", method, err, client.stderrString())
	}
	line, err := client.stdout.ReadBytes('\n')
	if err != nil {
		t.Fatalf("read %s response: %v; stderr=%s", method, err, client.stderrString())
	}
	var response struct {
		Result json.RawMessage `json:"result"`
		Error  json.RawMessage `json:"error"`
	}
	if err := json.Unmarshal(line, &response); err != nil {
		t.Fatalf("decode %s response: %v; raw=%s", method, err, line)
	}
	if len(response.Error) > 0 && string(response.Error) != "null" {
		t.Fatalf("%s returned JSON-RPC error: %s", method, response.Error)
	}
	return response.Result
}

func (c *mcpLSPBinaryClient) close(t *testing.T) {
	t.Helper()
	if c == nil || c.cmd == nil {
		return
	}
	cmd := c.cmd
	c.cmd = nil
	closeHook := c.closeHook
	c.closeHook = nil
	defer func() {
		if closeHook != nil {
			if err := closeHook(); err != nil {
				t.Errorf("close mcp-lsp test process owner: %v", err)
			}
		}
	}()
	raw, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"method":  "exit",
	})
	_, _ = c.stdin.Write(append(raw, '\n'))
	_ = c.stdin.Close()
	done := make(chan error, 1)
	var waiters sync.WaitGroup
	waiters.Go(func() { done <- cmd.Wait() })
	defer waiters.Wait()
	select {
	case err := <-done:
		if err != nil && !errors.Is(err, os.ErrProcessDone) {
			t.Logf("mcp-lsp binary exited with %v; stderr=%s", err, c.stderrString())
		}
	// 全语言 E2E 会在一个 sidecar 中持有数十个独立语言服务器。生命周期策略要求
	// 每个 client 串行完成 protocol shutdown/exit 与 process-tree owner 释放；5 秒只够
	// 少量 client，会把仍在正常收敛的进程误杀。30 秒仍是有界门禁，并足以暴露泄漏。
	case <-time.After(30 * time.Second):
		_ = cmd.Process.Kill()
		t.Errorf("mcp-lsp binary required kill after shutdown timeout; stderr=%s", c.stderrString())
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

type fakeLSPDidOpenParams struct {
	TextDocument struct {
		URI        string `json:"uri"`
		LanguageID string `json:"languageId"`
	} `json:"textDocument"`
}

type fakeLSPPositionParams struct {
	Position struct {
		Line      int `json:"line"`
		Character int `json:"character"`
	} `json:"position"`
}

type fakeLSPWriter struct {
	mu         sync.Mutex
	w          io.Writer
	goroutines *sync.WaitGroup
}

func runFakePyrightLangserver() {
	reader := bufio.NewReader(os.Stdin)
	var goroutines sync.WaitGroup
	defer goroutines.Wait()
	writer := &fakeLSPWriter{w: os.Stdout, goroutines: &goroutines}
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
		if writer.handleNotification(req) {
			continue
		}
		if len(bytes.TrimSpace(req.ID)) == 0 {
			continue
		}
		_ = writer.writeResponse(req.ID, fakeLSPResult(req))
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

func (w *fakeLSPWriter) writeResponse(id json.RawMessage, result any) error {
	return w.write(map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"result":  result,
	})
}

func (w *fakeLSPWriter) writeNotification(method string, params any) error {
	return w.write(map[string]any{
		"jsonrpc": "2.0",
		"method":  method,
		"params":  params,
	})
}

func (w *fakeLSPWriter) write(payload map[string]any) error {
	raw, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if _, err := fmt.Fprintf(w.w, "Content-Length: %d\r\n\r\n", len(raw)); err != nil {
		return err
	}
	_, err = w.w.Write(raw)
	return err
}

func (w *fakeLSPWriter) goAsync(fn func()) {
	w.goroutines.Go(fn)
}

func (w *fakeLSPWriter) handleNotification(req fakeLSPRequest) bool {
	if len(bytes.TrimSpace(req.ID)) != 0 {
		return false
	}
	if req.Method != "textDocument/didOpen" {
		return false
	}
	var params fakeLSPDidOpenParams
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return true
	}
	uri := strings.TrimSpace(params.TextDocument.URI)
	if uri == "" {
		return true
	}
	delay := fakePyrightDiagnosticDelay(uri)
	w.goAsync(func() {
		if delay > 0 {
			time.Sleep(delay)
		}
		_ = w.writeNotification("textDocument/publishDiagnostics", fakePyrightDiagnostics(uri))
	})
	return true
}

func fakeLSPResult(req fakeLSPRequest) any {
	switch req.Method {
	case "initialize":
		if delay := fakePyrightDurationFromEnv("MCP_LSP_FAKE_PYRIGHT_INIT_DELAY"); delay > 0 {
			time.Sleep(delay)
		}
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
		if label := fakePyrightCompletionLabel(params.Position); label != "" {
			return map[string]any{
				"isIncomplete": false,
				"items": []map[string]any{{
					"label":  label,
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

func fakePyrightCompletionLabel(position struct {
	Line      int `json:"line"`
	Character int `json:"character"`
}) string {
	switch {
	case position.Line == 9 && position.Character == 4:
		return "REG_CN"
	case position.Line == 10 && position.Character == 4:
		return "REG_US"
	case position.Line == 12 && position.Character == 4:
		return "REG_CRYPTO"
	default:
		return ""
	}
}

func fakePyrightDiagnosticDelay(uri string) time.Duration {
	if os.Getenv("MCP_LSP_FAKE_PYRIGHT_DIAGNOSTICS") != "delayed_second" {
		return 0
	}
	if strings.HasSuffix(uri, "/slow.py") {
		return fakePyrightDurationFromEnv("MCP_LSP_FAKE_PYRIGHT_DIAGNOSTIC_SLOW_DELAY")
	}
	return 0
}

func fakePyrightDurationFromEnv(name string) time.Duration {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return 0
	}
	if duration, err := time.ParseDuration(raw); err == nil {
		return duration
	}
	milliseconds, err := strconv.Atoi(raw)
	if err != nil {
		return 0
	}
	return time.Duration(milliseconds) * time.Millisecond
}

func fakePyrightDiagnostics(uri string) map[string]any {
	diagnostics := []map[string]any(nil)
	message := "fake diagnostic for " + filepath.Base(uri)
	code := "fake-type"
	if os.Getenv("MCP_LSP_FAKE_PYRIGHT_DIAGNOSTICS") == "multiline" {
		message = strings.Join([]string{
			`Argument of type "dict[str, object]" cannot be assigned to parameter "feature_name" of type "FeatureName" in function "train"`,
			`  "Literal['yes']" is not assignable to "bool"`,
			`  "Literal[42]" is not assignable to "str"`,
			`  Type "list[str]" is not assignable to "list[float]"`,
		}, "\n")
		code = "reportArgumentType"
	}
	if os.Getenv("MCP_LSP_FAKE_PYRIGHT_DIAGNOSTICS") != "" {
		diagnostics = []map[string]any{{
			"range": map[string]any{
				"start": map[string]any{"line": 0, "character": 0},
				"end":   map[string]any{"line": 0, "character": 5},
			},
			"severity": 1,
			"source":   "fake-pyright",
			"message":  message,
			"code":     code,
		}}
	}
	return map[string]any{
		"uri":         uri,
		"diagnostics": diagnostics,
	}
}
