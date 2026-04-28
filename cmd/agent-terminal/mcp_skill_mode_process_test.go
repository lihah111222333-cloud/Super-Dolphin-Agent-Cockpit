package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"
)

type skillProcessJSONRPCRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      int             `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

func TestMcpSkillMode_RealBinaryFramedStdioSmokeAndEOF(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()

	bin := buildAgentTerminalSmokeBinary(t, ctx)

	requests := make(chan skillProcessJSONRPCRequest, 1)
	addr := startProcessSmokeHostRPCServer(t, requests, `{"jsonrpc":"2.0","id":1,"result":{"name":"demo","content":"body","total_bytes":4}}`)

	childCtx, cancelChild := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancelChild()
	cmd := exec.CommandContext(childCtx, bin, "--mcp-skill-mode")
	cmd.Env = append(os.Environ(),
		"GO_AGENT_CTL_RPC_ADDR="+addr,
		"GO_AGENT_SKILL_MCP_CWD=/repo",
		"GO_AGENT_SKILL_MCP_AGENT_ID=agent-1",
		"GO_AGENT_SKILL_MCP_THREAD_ID=thread-1",
	)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatalf("StdinPipe() error = %v", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatalf("StdoutPipe() error = %v", err)
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		t.Fatalf("start --mcp-skill-mode child: %v", err)
	}

	stdoutCh := make(chan []byte, 1)
	stdoutErrCh := make(chan error, 1)
	go func() {
		out, err := io.ReadAll(stdout)
		stdoutCh <- out
		stdoutErrCh <- err
	}()

	for _, payload := range []string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05"}}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}`,
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"skill_expand_body","arguments":{"name":"demo","anchor":"Usage"}}}`,
	} {
		if err := writeMCPFrame(stdin, []byte(payload)); err != nil {
			t.Fatalf("write MCP frame: %v", err)
		}
	}
	if err := stdin.Close(); err != nil {
		t.Fatalf("close child stdin: %v", err)
	}

	waitErr := cmd.Wait()
	stdoutBytes := <-stdoutCh
	if err := <-stdoutErrCh; err != nil {
		t.Fatalf("read child stdout: %v", err)
	}
	if childCtx.Err() == context.DeadlineExceeded {
		t.Fatalf("--mcp-skill-mode child did not exit after stdin EOF; stderr=%s stdout=%s", stderr.String(), string(stdoutBytes))
	}
	if waitErr != nil {
		t.Fatalf("--mcp-skill-mode child exited with error: %v\nstderr=%s\nstdout=%s", waitErr, stderr.String(), string(stdoutBytes))
	}

	responses := decodeMCPFramedResponses(t, stdoutBytes)
	if len(responses) != 3 {
		t.Fatalf("responses len = %d, want 3; raw=%s stderr=%s", len(responses), string(stdoutBytes), stderr.String())
	}
	if responses[0]["id"] != float64(1) || responses[1]["id"] != float64(2) || responses[2]["id"] != float64(3) {
		t.Fatalf("response ids = %#v", responses)
	}
	initResult, _ := responses[0]["result"].(map[string]any)
	serverInfo, _ := initResult["serverInfo"].(map[string]any)
	if serverInfo["name"] != "skill" {
		t.Fatalf("initialize serverInfo = %#v, want skill server", serverInfo)
	}
	listResult, _ := responses[1]["result"].(map[string]any)
	tools, _ := listResult["tools"].([]any)
	if len(tools) != 2 {
		t.Fatalf("tools/list result = %#v, want two static skill tools", listResult)
	}
	callResult, _ := responses[2]["result"].(map[string]any)
	content, _ := callResult["content"].([]any)
	if len(content) != 1 {
		t.Fatalf("tools/call result = %#v, want one text content item", callResult)
	}
	textItem, _ := content[0].(map[string]any)
	text, _ := textItem["text"].(string)
	if !strings.Contains(text, `"content":"body"`) {
		t.Fatalf("tools/call text = %q, want host RPC result body", text)
	}

	select {
	case req := <-requests:
		if req.Method != "skills/expandBody" {
			t.Fatalf("host RPC method = %q, want skills/expandBody", req.Method)
		}
		var params map[string]any
		if err := json.Unmarshal(req.Params, &params); err != nil {
			t.Fatalf("host RPC params invalid JSON: %v", err)
		}
		if params["cwd"] != "/repo" || params["agentId"] != "agent-1" || params["threadId"] != "thread-1" {
			t.Fatalf("host RPC runtime params = %#v", params)
		}
		if params["name"] != "demo" || params["anchor"] != "Usage" {
			t.Fatalf("host RPC model params = %#v", params)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for host RPC request")
	}
}

func TestMcpSkillMode_RealBinaryLatencyBudget(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()

	bin := buildAgentTerminalSmokeBinary(t, ctx)
	requests := make(chan skillProcessJSONRPCRequest, 1)
	addr := startProcessSmokeHostRPCServer(t, requests, `{"jsonrpc":"2.0","id":1,"result":{"name":"demo","content":"body","total_bytes":4}}`)

	childCtx, cancelChild := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancelChild()
	cmd := exec.CommandContext(childCtx, bin, "--mcp-skill-mode")
	cmd.Env = append(os.Environ(),
		"GO_AGENT_CTL_RPC_ADDR="+addr,
		"GO_AGENT_SKILL_MCP_CWD=/repo",
		"GO_AGENT_SKILL_MCP_AGENT_ID=agent-1",
		"GO_AGENT_SKILL_MCP_THREAD_ID=thread-1",
	)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatalf("StdinPipe() error = %v", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatalf("StdoutPipe() error = %v", err)
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	processStart := time.Now()
	if err := cmd.Start(); err != nil {
		t.Fatalf("start --mcp-skill-mode child: %v", err)
	}
	stdoutReader := bufio.NewReader(stdout)

	writeRead := func(label, payload string, budget time.Duration) map[string]any {
		t.Helper()
		start := time.Now()
		if err := writeMCPFrame(stdin, []byte(payload)); err != nil {
			t.Fatalf("write %s MCP frame: %v", label, err)
		}
		resp := readMCPFramedResponse(t, stdoutReader)
		elapsed := time.Since(start)
		t.Logf("skill MCP process latency %s=%s", label, elapsed)
		if elapsed > budget {
			t.Fatalf("skill MCP process %s latency = %s, want <= %s; stderr=%s", label, elapsed, budget, stderr.String())
		}
		return resp
	}

	initResp := writeRead("initialize", `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05"}}`, 7*time.Second)
	if initResp["id"] != float64(1) {
		t.Fatalf("initialize response = %#v", initResp)
	}
	t.Logf("skill MCP process latency start_to_initialize=%s", time.Since(processStart))

	listResp := writeRead("tools_list", `{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}`, time.Second)
	if listResp["id"] != float64(2) {
		t.Fatalf("tools/list response = %#v", listResp)
	}
	listResult, _ := listResp["result"].(map[string]any)
	if tools, _ := listResult["tools"].([]any); len(tools) != 2 {
		t.Fatalf("tools/list result = %#v, want two static skill tools", listResult)
	}

	callResp := writeRead("tools_call_expand_body", `{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"skill_expand_body","arguments":{"name":"demo","anchor":"Usage"}}}`, 3*time.Second)
	if callResp["id"] != float64(3) {
		t.Fatalf("tools/call response = %#v", callResp)
	}
	callResult, _ := callResp["result"].(map[string]any)
	content, _ := callResult["content"].([]any)
	if len(content) != 1 {
		t.Fatalf("tools/call result = %#v, want one text content item", callResult)
	}

	select {
	case req := <-requests:
		if req.Method != "skills/expandBody" {
			t.Fatalf("host RPC method = %q, want skills/expandBody", req.Method)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for host RPC request")
	}

	closeStart := time.Now()
	if err := stdin.Close(); err != nil {
		t.Fatalf("close child stdin: %v", err)
	}
	waitErr := waitForSkillMCPChildExit(t, cmd, 5*time.Second, &stderr)
	closeElapsed := time.Since(closeStart)
	t.Logf("skill MCP process latency stdin_eof_to_exit=%s", closeElapsed)
	if childCtx.Err() == context.DeadlineExceeded {
		t.Fatalf("--mcp-skill-mode child did not exit after stdin EOF; stderr=%s", stderr.String())
	}
	if waitErr != nil {
		t.Fatalf("--mcp-skill-mode child exited with error: %v\nstderr=%s", waitErr, stderr.String())
	}
	if closeElapsed > 2*time.Second {
		t.Fatalf("skill MCP process stdin EOF exit latency = %s, want <= 2s; stderr=%s", closeElapsed, stderr.String())
	}
}

func TestMcpSkillMode_ClaudeLikeParentLifecycleEOFCancelAndNoOrphan(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()

	bin := buildAgentTerminalSmokeBinary(t, ctx)

	t.Run("stdin_eof_reaps_child", func(t *testing.T) {
		cmdCtx, cancelCmd := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancelCmd()

		cmd := exec.CommandContext(cmdCtx, bin, "--mcp-skill-mode")
		cmd.Env = append(os.Environ(), "GO_AGENT_SKILL_MCP_CWD=/repo")
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		stdin, err := cmd.StdinPipe()
		if err != nil {
			t.Fatalf("StdinPipe() error = %v", err)
		}
		if err := cmd.Start(); err != nil {
			t.Fatalf("start --mcp-skill-mode child: %v", err)
		}
		pid := cmd.Process.Pid

		if err := stdin.Close(); err != nil {
			t.Fatalf("close child stdin: %v", err)
		}
		waitErr := waitForSkillMCPChildExit(t, cmd, 5*time.Second, &stderr)
		if cmdCtx.Err() == context.DeadlineExceeded {
			t.Fatalf("child did not exit after stdin EOF; stderr=%s", stderr.String())
		}
		if waitErr != nil {
			t.Fatalf("stdin EOF child exited with error: %v\nstderr=%s", waitErr, stderr.String())
		}
		assertSkillMCPChildReaped(t, cmd, pid, &stderr)
	})

	t.Run("context_cancel_reaps_child", func(t *testing.T) {
		cmdCtx, cancelCmd := context.WithCancel(context.Background())
		cmd := exec.CommandContext(cmdCtx, bin, "--mcp-skill-mode")
		cmd.Env = append(os.Environ(), "GO_AGENT_SKILL_MCP_CWD=/repo")
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		stdin, err := cmd.StdinPipe()
		if err != nil {
			t.Fatalf("StdinPipe() error = %v", err)
		}
		defer func() { _ = stdin.Close() }()
		if err := cmd.Start(); err != nil {
			t.Fatalf("start --mcp-skill-mode child: %v", err)
		}
		pid := cmd.Process.Pid

		cancelCmd()
		waitErr := waitForSkillMCPChildExit(t, cmd, 5*time.Second, &stderr)
		if cmdCtx.Err() != context.Canceled {
			t.Fatalf("command context err = %v, want context.Canceled", cmdCtx.Err())
		}
		if waitErr == nil {
			t.Fatalf("context-canceled child exited cleanly; want parent cancellation to terminate the still-open stdio child")
		}
		assertSkillMCPChildReaped(t, cmd, pid, &stderr)
	})
}

func buildAgentTerminalSmokeBinary(t *testing.T, ctx context.Context) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "agent-terminal-smoke")
	if runtime.GOOS == "windows" {
		bin += ".exe"
	}
	build := exec.CommandContext(ctx, "go", "build", "-o", bin, ".")
	build.Env = os.Environ()
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("go build agent-terminal smoke binary: %v\n%s", err, string(out))
	}
	return bin
}

func waitForSkillMCPChildExit(t *testing.T, cmd *exec.Cmd, timeout time.Duration, stderr *bytes.Buffer) error {
	t.Helper()
	waitCh := make(chan error, 1)
	go func() { waitCh <- cmd.Wait() }()
	select {
	case err := <-waitCh:
		return err
	case <-time.After(timeout):
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
		t.Fatalf("timed out waiting for --mcp-skill-mode child pid=%d; stderr=%s", cmd.Process.Pid, stderr.String())
		return nil
	}
}

func assertSkillMCPChildReaped(t *testing.T, cmd *exec.Cmd, pid int, stderr *bytes.Buffer) {
	t.Helper()
	if cmd.ProcessState == nil {
		t.Fatalf("child pid=%d has nil ProcessState after Wait; stderr=%s", pid, stderr.String())
	}
	if skillProcessStillExists(pid) {
		t.Fatalf("child pid=%d still exists after Wait; possible orphan; state=%s stderr=%s", pid, cmd.ProcessState.String(), stderr.String())
	}
}

func skillProcessStillExists(pid int) bool {
	if pid <= 0 || runtime.GOOS == "windows" {
		return false
	}
	return exec.Command("kill", "-0", strconv.Itoa(pid)).Run() == nil
}

func writeMCPFrame(w io.Writer, payload []byte) error {
	if _, err := fmt.Fprintf(w, "Content-Length: %d\r\n\r\n", len(payload)); err != nil {
		return err
	}
	_, err := w.Write(payload)
	return err
}

func readMCPFramedResponse(t *testing.T, reader *bufio.Reader) map[string]any {
	t.Helper()
	length := -1
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			t.Fatalf("read MCP response header: %v", err)
		}
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			break
		}
		name, value, ok := strings.Cut(line, ":")
		if !ok {
			t.Fatalf("malformed MCP response header %q", line)
		}
		if !strings.EqualFold(strings.TrimSpace(name), "Content-Length") {
			continue
		}
		parsed, err := strconv.Atoi(strings.TrimSpace(value))
		if err != nil || parsed < 0 {
			t.Fatalf("invalid MCP response content length %q", value)
		}
		length = parsed
	}
	if length < 0 {
		t.Fatal("missing MCP response Content-Length")
	}
	body := make([]byte, length)
	if _, err := io.ReadFull(reader, body); err != nil {
		t.Fatalf("read MCP response body: %v", err)
	}
	var resp map[string]any
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatalf("decode MCP response body %q: %v", string(body), err)
	}
	return resp
}

func decodeMCPFramedResponses(t *testing.T, raw []byte) []map[string]any {
	t.Helper()
	reader := bufio.NewReader(bytes.NewReader(raw))
	var out []map[string]any
	for {
		length := -1
		for {
			line, err := reader.ReadString('\n')
			if err == io.EOF && strings.TrimSpace(line) == "" {
				return out
			}
			if err != nil {
				t.Fatalf("read MCP response header: %v; raw=%s", err, string(raw))
			}
			line = strings.TrimRight(line, "\r\n")
			if line == "" {
				break
			}
			name, value, ok := strings.Cut(line, ":")
			if !ok {
				t.Fatalf("malformed MCP response header %q; raw=%s", line, string(raw))
			}
			if !strings.EqualFold(strings.TrimSpace(name), "Content-Length") {
				continue
			}
			parsed, err := strconv.Atoi(strings.TrimSpace(value))
			if err != nil || parsed < 0 {
				t.Fatalf("invalid MCP response content length %q; raw=%s", value, string(raw))
			}
			length = parsed
		}
		if length < 0 {
			t.Fatalf("missing MCP response Content-Length; raw=%s", string(raw))
		}
		body := make([]byte, length)
		if _, err := io.ReadFull(reader, body); err != nil {
			t.Fatalf("read MCP response body: %v; raw=%s", err, string(raw))
		}
		var resp map[string]any
		if err := json.Unmarshal(body, &resp); err != nil {
			t.Fatalf("decode MCP response body %q: %v", string(body), err)
		}
		out = append(out, resp)
	}
}

func startProcessSmokeHostRPCServer(t *testing.T, requests chan<- skillProcessJSONRPCRequest, response string) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen() error = %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		var req skillProcessJSONRPCRequest
		if err := json.NewDecoder(conn).Decode(&req); err == nil {
			requests <- req
		}
		_, _ = conn.Write([]byte(response + "\n"))
	}()
	return ln.Addr().String()
}
