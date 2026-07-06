//go:build e2e
// +build e2e

package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/anthropic-ai/super-agent-v3/internal/platform/discovery"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/mcpcontrol"
	"github.com/creachadair/jrpc2"
	"github.com/creachadair/jrpc2/channel"
)

func TestMcpLSPBinaryPythonConstantIdentifierCompletionIsColumnInsensitive_E2E(t *testing.T) {
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

	for _, tc := range []struct {
		pos  string
		want string
	}{
		{pos: target + ":10:5", want: "REG_CN"},
		{pos: target + ":10:6", want: "REG_CN"},
		{pos: target + ":10:7", want: "REG_CN"},
		{pos: target + ":11:6", want: "REG_US"},
		{pos: target + ":13:7", want: "REG_CRYPTO"},
	} {
		completion := client.callTool(t, "completion", map[string]any{
			"pos":         tc.pos,
			"max_results": 10,
		})
		if completion.Result.IsError {
			t.Fatalf("completion at %s returned MCP error result; text=%q structured=%s stderr=%s",
				tc.pos, completion.Result.ContentText(), completion.Result.StructuredContent, client.stderrString())
		}
		labels := completionLabelsFromStructuredContent(t, completion.Result.StructuredContent)
		if !stringSliceContains(labels, tc.want) {
			t.Fatalf("completion at %s is column-sensitive: labels=%v want=%s structured=%s text=%q stderr=%s",
				tc.pos, labels, tc.want, completion.Result.StructuredContent, completion.Result.ContentText(), client.stderrString())
		}
	}
}

func TestMcpLSPBinaryPythonFirstConcurrentCompletionDoesNotTimeout_E2E(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping mcp-lsp binary e2e test in short mode")
	}

	root := t.TempDir()
	target := writePythonConstantCompletionFixture(t, root)
	binary := buildMcpLSPBinaryForTest(t)
	fakePyrightBinDir := writeFakePyrightLangserver(t)

	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Second)
	defer cancel()
	client := startMcpLSPPeerBinaryForTest(t, ctx, binary, root, fakePyrightBinDir, []string{
		"MCP_LSP_FAKE_PYRIGHT_INIT_DELAY=5500ms",
	})
	defer client.close(t)

	client.call(t, "initialize", map[string]any{"protocolVersion": "2024-11-05"})

	type completionOutcome struct {
		index int
		resp  mcpLSPBinaryResponse
		raw   []byte
		err   error
	}
	start := make(chan struct{})
	outcomes := make(chan completionOutcome, 2)
	for i := 0; i < 2; i++ {
		go func(index int) {
			<-start
			resp, raw, err := client.callRaw(ctx, "tools/call", map[string]any{
				"name":            "completion",
				"_cwd":            root,
				"_workspaceRoots": []string{root},
				"arguments": map[string]any{
					"pos":         target + ":10:7",
					"max_results": 10,
				},
			})
			outcomes <- completionOutcome{index: index, resp: resp, raw: raw, err: err}
		}(i)
	}
	close(start)

	for i := 0; i < 2; i++ {
		outcome := <-outcomes
		if outcome.err != nil {
			t.Fatalf("concurrent completion %d HTTP call failed: %v; stderr=%s", outcome.index, outcome.err, client.stderrString())
		}
		if outcome.resp.Error != nil {
			t.Fatalf("concurrent completion %d returned JSON-RPC error %d: %s; raw=%s stderr=%s",
				outcome.index, outcome.resp.Error.Code, outcome.resp.Error.Message, outcome.raw, client.stderrString())
		}
		if outcome.resp.Result.IsError {
			t.Fatalf("first concurrent completion %d timed out or returned MCP error; text=%q structured=%s raw=%s stderr=%s",
				outcome.index, outcome.resp.Result.ContentText(), outcome.resp.Result.StructuredContent, outcome.raw, client.stderrString())
		}
		labels := completionLabelsFromStructuredContent(t, outcome.resp.Result.StructuredContent)
		if !stringSliceContains(labels, "REG_CN") {
			t.Fatalf("concurrent completion %d labels=%v, want REG_CN; raw=%s stderr=%s",
				outcome.index, labels, outcome.raw, client.stderrString())
		}
	}
}

func TestMcpLSPBinaryPythonDiagnosticsWaitsForDelayedTargets_E2E(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping mcp-lsp binary e2e test in short mode")
	}

	root := t.TempDir()
	targets := writePythonDiagnosticsFixture(t, root)
	binary := buildMcpLSPBinaryForTest(t)
	fakePyrightBinDir := writeFakePyrightLangserver(t)

	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Second)
	defer cancel()
	client := startMcpLSPBinaryForTestWithEnv(t, ctx, binary, root, fakePyrightBinDir, []string{
		"MCP_LSP_FAKE_PYRIGHT_DIAGNOSTICS=delayed_second",
		"MCP_LSP_FAKE_PYRIGHT_DIAGNOSTIC_SLOW_DELAY=250ms",
	})
	defer client.close(t)

	client.call(t, "initialize", map[string]any{"protocolVersion": "2024-11-05"})

	diagnostics := client.callTool(t, "file", map[string]any{
		"action":     "diagnostics",
		"file_paths": targets,
	})
	if diagnostics.Result.IsError {
		t.Fatalf("diagnostics returned MCP error result; text=%q structured=%s stderr=%s",
			diagnostics.Result.ContentText(), diagnostics.Result.StructuredContent, client.stderrString())
	}
	payload := decodeDiagnosticsStructuredContent(t, diagnostics.Result.StructuredContent)
	if strings.Contains(payload.Meta.Message, "partial diagnostics") {
		t.Fatalf("diagnostics returned partial readiness before delayed Python target published: meta=%q payload=%s text=%q stderr=%s",
			payload.Meta.Message, diagnostics.Result.StructuredContent, diagnostics.Result.ContentText(), client.stderrString())
	}
	if payload.Total != len(targets) {
		t.Fatalf("diagnostics total = %d, want %d; payload=%s text=%q stderr=%s",
			payload.Total, len(targets), diagnostics.Result.StructuredContent, diagnostics.Result.ContentText(), client.stderrString())
	}
	for _, target := range targets {
		if !payload.HasFile(target) {
			t.Fatalf("diagnostics missing %s; payload=%s text=%q stderr=%s",
				target, diagnostics.Result.StructuredContent, diagnostics.Result.ContentText(), client.stderrString())
		}
	}
}

func TestMcpLSPBinaryPythonDiagnosticsRetriesPastStartupRecoveryBudget_E2E(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping mcp-lsp binary e2e test in short mode")
	}

	root := t.TempDir()
	targets := writePythonDiagnosticsFixture(t, root)
	slowTarget := targets[1]
	binary := buildMcpLSPBinaryForTest(t)
	fakePyrightBinDir := writeFakePyrightLangserver(t)

	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Second)
	defer cancel()
	client := startMcpLSPBinaryForTestWithEnv(t, ctx, binary, root, fakePyrightBinDir, []string{
		"MCP_LSP_FAKE_PYRIGHT_DIAGNOSTICS=delayed_second",
		"MCP_LSP_FAKE_PYRIGHT_DIAGNOSTIC_SLOW_DELAY=16500ms",
	})
	defer client.close(t)

	client.call(t, "initialize", map[string]any{"protocolVersion": "2024-11-05"})

	diagnostics := client.callTool(t, "file", map[string]any{
		"action":    "diagnostics",
		"file_path": slowTarget,
	})
	if diagnostics.Result.IsError {
		t.Fatalf("diagnostics timed out before delayed Python target published; text=%q structured=%s stderr=%s",
			diagnostics.Result.ContentText(), diagnostics.Result.StructuredContent, client.stderrString())
	}
	payload := decodeDiagnosticsStructuredContent(t, diagnostics.Result.StructuredContent)
	if strings.Contains(payload.Meta.Message, "partial diagnostics") {
		t.Fatalf("diagnostics returned partial readiness before delayed Python target published: meta=%q payload=%s text=%q stderr=%s",
			payload.Meta.Message, diagnostics.Result.StructuredContent, diagnostics.Result.ContentText(), client.stderrString())
	}
	if payload.Total != 1 {
		t.Fatalf("diagnostics total = %d, want 1; payload=%s text=%q stderr=%s",
			payload.Total, diagnostics.Result.StructuredContent, diagnostics.Result.ContentText(), client.stderrString())
	}
	if !payload.HasFile(slowTarget) {
		t.Fatalf("diagnostics missing %s; payload=%s text=%q stderr=%s",
			slowTarget, diagnostics.Result.StructuredContent, diagnostics.Result.ContentText(), client.stderrString())
	}
}

func TestMcpLSPBinaryPythonDiagnosticsSummarizesMultilineMessages_E2E(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping mcp-lsp binary e2e test in short mode")
	}

	root := t.TempDir()
	targets := writePythonDiagnosticsFixture(t, root)
	binary := buildMcpLSPBinaryForTest(t)
	fakePyrightBinDir := writeFakePyrightLangserver(t)

	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Second)
	defer cancel()
	client := startMcpLSPBinaryForTestWithEnv(t, ctx, binary, root, fakePyrightBinDir, []string{
		"MCP_LSP_FAKE_PYRIGHT_DIAGNOSTICS=multiline",
	})
	defer client.close(t)

	client.call(t, "initialize", map[string]any{"protocolVersion": "2024-11-05"})

	diagnostics := client.callTool(t, "file", map[string]any{
		"action":    "diagnostics",
		"file_path": targets[0],
	})
	if diagnostics.Result.IsError {
		t.Fatalf("diagnostics returned MCP error result; text=%q structured=%s stderr=%s",
			diagnostics.Result.ContentText(), diagnostics.Result.StructuredContent, client.stderrString())
	}
	payload := decodeDiagnosticsStructuredContent(t, diagnostics.Result.StructuredContent)
	msg := payload.FirstMessageForFile(t, targets[0])
	want := `Argument of type "dict[str, object]" cannot be assigned to parameter "feature_name" of type "FeatureName" in function "train"`
	if msg != want {
		t.Fatalf("diagnostics msg = %q, want first-line summary %q; payload=%s text=%q stderr=%s",
			msg, want, diagnostics.Result.StructuredContent, diagnostics.Result.ContentText(), client.stderrString())
	}
	if strings.Contains(msg, "\n") {
		t.Fatalf("diagnostics msg still contains Pyright detail lines: %q", msg)
	}
}

type mcpLSPPeerBinaryClient struct {
	cmd        *exec.Cmd
	stdin      io.WriteCloser
	stderr     lockedStringBuilder
	cancel     context.CancelFunc
	done       chan error
	addr       string
	token      string
	httpClient *http.Client
}

func startMcpLSPBinaryForTestWithEnv(t *testing.T, ctx context.Context, binary, root, fakePyrightBinDir string, extraEnv []string) *mcpLSPBinaryClient {
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
	cmd.Env = append(cmd.Env, extraEnv...)
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

func startMcpLSPPeerBinaryForTest(t *testing.T, parent context.Context, binary, root, fakePyrightBinDir string, extraEnv []string) *mcpLSPPeerBinaryClient {
	t.Helper()
	repoRoot := repoRootForMcpLSPBinaryTest(t)
	rawRoots, err := json.Marshal([]string{root})
	if err != nil {
		t.Fatalf("marshal roots: %v", err)
	}
	token := "test-lsp-peer-token"
	controlAddr := startMcpLSPPeerControlPlane(t)
	ctx, cancel := context.WithCancel(parent)
	cmd := exec.CommandContext(ctx, binary)
	cmd.Dir = root
	cmd.Env = append(mcpLSPPeerBaseEnv(),
		"GO_AGENT_LSP_ROOT="+root,
		"GO_AGENT_LSP_ROOTS="+string(rawRoots),
		"GO_AGENT_PEER_MODE=1",
		"GO_AGENT_CTL_RPC_ADDR="+controlAddr,
		"GO_AGENT_CTL_SESSION_TOKEN="+token,
		"PATH="+fakePyrightBinDir+string(os.PathListSeparator)+os.Getenv("PATH"),
		"SUPER_DOLPHIN_RUNTIME_MODE=dev",
		"SUPER_DOLPHIN_RUNTIME_RESOURCES_DIR="+repoRoot,
	)
	cmd.Env = append(cmd.Env, extraEnv...)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		cancel()
		t.Fatalf("stdin pipe: %v", err)
	}
	cmd.Stdout = io.Discard
	stderrPipe, err := cmd.StderrPipe()
	if err != nil {
		cancel()
		t.Fatalf("stderr pipe: %v", err)
	}
	client := &mcpLSPPeerBinaryClient{
		cmd:        cmd,
		stdin:      stdin,
		cancel:     cancel,
		done:       make(chan error, 1),
		token:      token,
		httpClient: &http.Client{Timeout: 20 * time.Second},
	}
	if err := cmd.Start(); err != nil {
		cancel()
		t.Fatalf("start mcp-lsp peer binary: %v", err)
	}
	go func() {
		_, _ = io.Copy(&client.stderr, stderrPipe)
	}()
	go func() {
		client.done <- cmd.Wait()
	}()
	client.addr = waitForMcpLSPPeerAddr(t, token, &client.stderr)
	return client
}

func startMcpLSPPeerControlPlane(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen fake control plane: %v", err)
	}
	methods := mcpcontrol.NewHandlers(mcpcontrol.HandlerDeps{
		Registry: mcpcontrol.NewRegistry(),
		Logger:   slog.New(slog.NewTextHandler(io.Discard, nil)),
	}).Handlers
	done := make(chan error, 1)
	go func() {
		conn, acceptErr := ln.Accept()
		if acceptErr != nil {
			done <- acceptErr
			return
		}
		_ = ln.Close()
		stat := jrpc2.NewServer(methods, &jrpc2.ServerOptions{}).Start(channel.Line(conn, conn)).WaitStatus()
		done <- stat.Err
	}()
	t.Cleanup(func() {
		_ = ln.Close()
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Log("fake control plane server did not stop")
		}
	})
	return ln.Addr().String()
}

func mcpLSPPeerBaseEnv() []string {
	skip := map[string]bool{
		"GO_AGENT_CTL_RPC_ADDR":      true,
		"RPC_ADDR":                   true,
		"GO_AGENT_MCP_SESSION_TOKEN": true,
	}
	env := make([]string, 0, len(os.Environ()))
	for _, item := range os.Environ() {
		key, _, _ := strings.Cut(item, "=")
		if skip[key] {
			continue
		}
		env = append(env, item)
	}
	return env
}

func (c *mcpLSPPeerBinaryClient) call(t *testing.T, method string, params map[string]any) mcpLSPBinaryResponse {
	t.Helper()
	resp, raw, err := c.callRaw(context.Background(), method, params)
	if err != nil {
		t.Fatalf("%s HTTP call failed: %v; stderr=%s", method, err, c.stderrString())
	}
	if resp.Error != nil {
		t.Fatalf("%s returned JSON-RPC error %d: %s; raw=%s stderr=%s",
			method, resp.Error.Code, resp.Error.Message, raw, c.stderrString())
	}
	return resp
}

func (c *mcpLSPPeerBinaryClient) callRaw(ctx context.Context, method string, params map[string]any) (mcpLSPBinaryResponse, []byte, error) {
	req := map[string]any{
		"jsonrpc": "2.0",
		"id":      time.Now().UnixNano(),
		"method":  method,
		"params":  params,
	}
	rawReq, err := json.Marshal(req)
	if err != nil {
		return mcpLSPBinaryResponse{}, nil, fmt.Errorf("marshal %s request: %w", method, err)
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://"+c.addr+"/mcp", bytes.NewReader(rawReq))
	if err != nil {
		return mcpLSPBinaryResponse{}, nil, fmt.Errorf("build %s request: %w", method, err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+c.token)
	httpResp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return mcpLSPBinaryResponse{}, nil, fmt.Errorf("post %s request: %w", method, err)
	}
	defer httpResp.Body.Close()
	rawResp, err := io.ReadAll(httpResp.Body)
	if err != nil {
		return mcpLSPBinaryResponse{}, nil, fmt.Errorf("read %s response: %w", method, err)
	}
	if httpResp.StatusCode < 200 || httpResp.StatusCode >= 300 {
		return mcpLSPBinaryResponse{}, rawResp, fmt.Errorf("%s HTTP status %d", method, httpResp.StatusCode)
	}
	var resp mcpLSPBinaryResponse
	if err := json.Unmarshal(rawResp, &resp); err != nil {
		return mcpLSPBinaryResponse{}, rawResp, fmt.Errorf("unmarshal %s response: %w", method, err)
	}
	return resp, rawResp, nil
}

func (c *mcpLSPPeerBinaryClient) close(t *testing.T) {
	t.Helper()
	if c == nil {
		return
	}
	_ = discovery.CleanupDiscoveryFile("mcp-lsp", os.Getpid())
	if c.stdin != nil {
		_ = c.stdin.Close()
	}
	if c.cancel != nil {
		c.cancel()
	}
	select {
	case <-c.done:
	case <-time.After(5 * time.Second):
		if c.cmd != nil && c.cmd.Process != nil {
			_ = c.cmd.Process.Kill()
		}
		t.Fatalf("mcp-lsp peer binary did not exit; stderr=%s", c.stderrString())
	}
}

func (c *mcpLSPPeerBinaryClient) stderrString() string {
	if c == nil {
		return ""
	}
	return c.stderr.String()
}

func stringSliceContains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

type diagnosticsPayload struct {
	Success bool `json:"success"`
	Data    []struct {
		File string  `json:"file"`
		Rows [][]any `json:"rows"`
	} `json:"data"`
	Total int `json:"total"`
	Meta  struct {
		Message string `json:"message"`
	} `json:"meta"`
}

func (p diagnosticsPayload) HasFile(path string) bool {
	for _, table := range p.Data {
		if table.File == path {
			return true
		}
	}
	return false
}

func (p diagnosticsPayload) FirstMessageForFile(t *testing.T, path string) string {
	t.Helper()
	for _, table := range p.Data {
		if table.File != path {
			continue
		}
		if len(table.Rows) == 0 {
			t.Fatalf("diagnostics table for %s has no rows", path)
		}
		if len(table.Rows[0]) < 4 {
			t.Fatalf("diagnostics row for %s has %d columns, want msg column: %#v", path, len(table.Rows[0]), table.Rows[0])
		}
		msg, ok := table.Rows[0][3].(string)
		if !ok {
			t.Fatalf("diagnostics msg column type = %T, want string; row=%#v", table.Rows[0][3], table.Rows[0])
		}
		return msg
	}
	t.Fatalf("diagnostics missing %s: %#v", path, p.Data)
	return ""
}

func decodeDiagnosticsStructuredContent(t *testing.T, raw json.RawMessage) diagnosticsPayload {
	t.Helper()
	var payload diagnosticsPayload
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatalf("unmarshal diagnostics structuredContent: %v; raw=%s", err, raw)
	}
	return payload
}

func writePythonDiagnosticsFixture(t *testing.T, root string) []string {
	t.Helper()
	pyproject := filepath.Join(root, "pyproject.toml")
	if err := os.WriteFile(pyproject, []byte("[project]\nname = \"diagnostics-e2e\"\nversion = \"0.0.0\"\n"), 0o600); err != nil {
		t.Fatalf("write pyproject fixture: %v", err)
	}
	dir := filepath.Join(root, "pkg")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir diagnostics fixture dir: %v", err)
	}
	targets := []string{
		filepath.Join(dir, "ready.py"),
		filepath.Join(dir, "slow.py"),
	}
	for _, target := range targets {
		if err := os.WriteFile(target, []byte("value: int = \"not an int\"\n"), 0o600); err != nil {
			t.Fatalf("write Python diagnostics fixture %s: %v", target, err)
		}
	}
	return targets
}

func waitForMcpLSPPeerAddr(t *testing.T, token string, stderr fmt.Stringer) string {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	var lastErr error
	for time.Now().Before(deadline) {
		addr, err := discovery.ReadDiscoveryAddr("mcp-lsp", os.Getpid())
		if err == nil {
			if probeErr := discovery.ProbePeerHTTPAddrWithToken(addr, token); probeErr == nil {
				return addr
			} else {
				lastErr = probeErr
			}
		} else {
			lastErr = err
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("mcp-lsp peer discovery did not become ready: %v; stderr=%s", lastErr, stderr.String())
	return ""
}
