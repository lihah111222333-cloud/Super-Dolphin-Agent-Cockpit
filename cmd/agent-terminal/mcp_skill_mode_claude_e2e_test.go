package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"
)

const runClaudeCLIE2EEnv = "RUN_CLAUDE_CLI_E2E"

func TestMcpSkillMode_ClaudeCLIManagedSameBinarySkillE2E(t *testing.T) {
	if strings.TrimSpace(os.Getenv(runClaudeCLIE2EEnv)) != "1" {
		t.Skipf("set %s=1 to run the live Claude CLI --mcp-config E2E", runClaudeCLIE2EEnv)
	}

	claudeBin := strings.TrimSpace(os.Getenv("CLAUDE_CLI_BIN"))
	if claudeBin == "" {
		var err error
		claudeBin, err = exec.LookPath("claude")
		if err != nil {
			t.Skip("claude CLI not found in PATH and CLAUDE_CLI_BIN is unset")
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	agentBin := buildAgentTerminalSmokeBinary(t, ctx)
	requests := make(chan skillProcessJSONRPCRequest, 4)
	hostRPCAddr := startProcessSmokeHostRPCServer(t, requests, `{"jsonrpc":"2.0","id":1,"result":{"name":"demo","content":"body-from-claude-cli-e2e","total_bytes":25}}`)

	workDir := t.TempDir()
	mcpConfigPath := writeClaudeCLISkillMCPConfig(t, agentBin, hostRPCAddr, workDir)
	debugLogPath := filepath.Join(t.TempDir(), "claude-debug.log")

	monitorCtx, stopMonitor := context.WithCancel(context.Background())
	defer stopMonitor()
	observedChildren := monitorSkillModeChildren(monitorCtx, t, agentBin, 25*time.Millisecond)

	prompt := strings.Join([]string{
		"This is an integration test.",
		"Call the MCP tool mcp__skill__skill_expand_body exactly once with arguments {\"name\":\"demo\",\"anchor\":\"Usage\"}.",
		"After the tool returns, print exactly DONE and no extra text.",
	}, " ")
	args := []string{
		"--bare",
		"-p", prompt,
		"--output-format", "stream-json",
		"--verbose",
		"--no-session-persistence",
		"--strict-mcp-config",
		"--mcp-config", mcpConfigPath,
		"--allowedTools", "mcp__skill__skill_expand_body",
		"--disallowedTools", "Read,Write,Edit,MultiEdit,Bash,Grep,Glob,LS",
		"--permission-mode", "bypassPermissions",
		"--debug-file", debugLogPath,
		"--max-budget-usd", "0.05",
		"--system-prompt", "You are a deterministic integration test runner. When asked to call an MCP tool, call it exactly as requested.",
	}
	if model := strings.TrimSpace(os.Getenv("CLAUDE_CLI_E2E_MODEL")); model != "" {
		args = append(args, "--model", model)
	}

	cmd := exec.CommandContext(ctx, claudeBin, args...)
	cmd.Dir = workDir
	cmd.Env = os.Environ()
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	start := time.Now()
	runErr := cmd.Run()
	stopMonitor()
	observedPIDs := <-observedChildren
	if ctx.Err() == context.DeadlineExceeded {
		t.Fatalf("claude CLI E2E timed out after %s\nstdout=%s\nstderr=%s\ndebug=%s", time.Since(start), stdout.String(), stderr.String(), readOptionalFile(t, debugLogPath))
	}
	if runErr != nil {
		combinedOutput := stdout.String() + stderr.String() + readOptionalFile(t, debugLogPath)
		if isClaudeCLIAuthFailure(combinedOutput) {
			if len(observedPIDs) > 0 {
				assertNoSkillModeChildren(t, agentBin, observedPIDs, 5*time.Second, stdout.String(), stderr.String(), readOptionalFile(t, debugLogPath))
			}
			t.Skip("claude CLI is installed but not authenticated; run `claude auth`/`claude setup-token` or provide ANTHROPIC_API_KEY before enabling the live E2E")
		}
		t.Fatalf("claude CLI E2E failed after %s: %v\ncmd=%s %s\nstdout=%s\nstderr=%s\ndebug=%s",
			time.Since(start), runErr, claudeBin, strings.Join(args, " "), stdout.String(), stderr.String(), readOptionalFile(t, debugLogPath))
	}
	if len(observedPIDs) == 0 {
		t.Fatalf("claude CLI did not appear to spawn %s --mcp-skill-mode\nstdout=%s\nstderr=%s\ndebug=%s", agentBin, stdout.String(), stderr.String(), readOptionalFile(t, debugLogPath))
	}

	select {
	case req := <-requests:
		assertClaudeCLISkillExpandRequest(t, req)
	case <-time.After(10 * time.Second):
		t.Fatalf("timed out waiting for skill host RPC request\nstdout=%s\nstderr=%s\ndebug=%s", stdout.String(), stderr.String(), readOptionalFile(t, debugLogPath))
	}

	assertNoSkillModeChildren(t, agentBin, observedPIDs, 5*time.Second, stdout.String(), stderr.String(), readOptionalFile(t, debugLogPath))
}

func writeClaudeCLISkillMCPConfig(t *testing.T, agentBin, hostRPCAddr, cwd string) string {
	t.Helper()
	doc := map[string]any{
		"mcpServers": map[string]any{
			"skill": map[string]any{
				"command": agentBin,
				"args":    []string{"--mcp-skill-mode"},
				"env": map[string]string{
					"GO_AGENT_CTL_RPC_ADDR":         hostRPCAddr,
					"GO_AGENT_SKILL_MCP_CWD":        cwd,
					"GO_AGENT_SKILL_MCP_AGENT_ID":   "agent-claude-cli-e2e",
					"GO_AGENT_SKILL_MCP_THREAD_ID":  "thread-claude-cli-e2e",
					"GO_AGENT_SKILL_MCP_E2E_MARKER": "1",
				},
				"cwd": cwd,
			},
		},
	}
	raw, err := json.Marshal(doc)
	if err != nil {
		t.Fatalf("marshal Claude CLI MCP config: %v", err)
	}
	path := filepath.Join(t.TempDir(), "claude-mcp-config.json")
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatalf("write Claude CLI MCP config: %v", err)
	}
	return path
}

func assertClaudeCLISkillExpandRequest(t *testing.T, req skillProcessJSONRPCRequest) {
	t.Helper()
	if req.JSONRPC != "2.0" {
		t.Fatalf("host RPC jsonrpc = %q, want 2.0", req.JSONRPC)
	}
	if req.Method != "skills/expandBody" {
		t.Fatalf("host RPC method = %q, want skills/expandBody", req.Method)
	}
	var params map[string]any
	if err := json.Unmarshal(req.Params, &params); err != nil {
		t.Fatalf("host RPC params invalid JSON: %v", err)
	}
	want := map[string]any{
		"name":     "demo",
		"anchor":   "Usage",
		"agentId":  "agent-claude-cli-e2e",
		"threadId": "thread-claude-cli-e2e",
	}
	for key, value := range want {
		if params[key] != value {
			t.Fatalf("host RPC params[%s] = %#v, want %#v; params=%#v", key, params[key], value, params)
		}
	}
	if strings.TrimSpace(fmt.Sprint(params["cwd"])) == "" {
		t.Fatalf("host RPC cwd missing; params=%#v", params)
	}
}

func isClaudeCLIAuthFailure(output string) bool {
	output = strings.ToLower(output)
	return strings.Contains(output, "authentication_failed") ||
		strings.Contains(output, "not logged in") ||
		strings.Contains(output, "no api key available")
}

func monitorSkillModeChildren(ctx context.Context, t *testing.T, agentBin string, interval time.Duration) <-chan map[int]struct{} {
	t.Helper()
	done := make(chan map[int]struct{}, 1)
	go func() {
		defer close(done)
		seen := make(map[int]struct{})
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			if pids, err := skillModeChildPIDs(agentBin); err == nil {
				for _, pid := range pids {
					seen[pid] = struct{}{}
				}
			}
			select {
			case <-ctx.Done():
				done <- seen
				return
			case <-ticker.C:
			}
		}
	}()
	return done
}

func assertNoSkillModeChildren(t *testing.T, agentBin string, candidates map[int]struct{}, timeout time.Duration, stdout, stderr, debug string) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		var stillAlive []int
		current, err := skillModeChildPIDs(agentBin)
		if err != nil {
			if errors.Is(err, exec.ErrNotFound) {
				t.Skip("ps not available for Claude CLI child PID observation")
			}
			t.Fatalf("ps for Claude CLI child PID observation: %v", err)
		}
		for _, pid := range current {
			if _, ok := candidates[pid]; ok || len(candidates) == 0 {
				stillAlive = append(stillAlive, pid)
			}
		}
		if len(stillAlive) == 0 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("skill MCP child processes still alive after Claude CLI exit: %v\nstdout=%s\nstderr=%s\ndebug=%s", stillAlive, stdout, stderr, debug)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func skillModeChildPIDs(agentBin string) ([]int, error) {
	if runtime.GOOS == "windows" {
		return nil, nil
	}
	out, err := exec.Command("ps", "-axo", "pid=,command=").Output()
	if err != nil {
		return nil, err
	}
	var pids []int
	for line := range strings.SplitSeq(string(out), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || !strings.Contains(line, agentBin) || !strings.Contains(line, "--mcp-skill-mode") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		pid, err := strconv.Atoi(fields[0])
		if err != nil || pid <= 0 {
			continue
		}
		pids = append(pids, pid)
	}
	return pids, nil
}

func readOptionalFile(t *testing.T, path string) string {
	t.Helper()
	if strings.TrimSpace(path) == "" {
		return ""
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return string(raw)
}
