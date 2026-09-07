//go:build e2e

package main

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestStandaloneOfficialClaudeConnection 使用真实 CLI 健康检查，不能冒充工具调用或模型执行。
func TestStandaloneOfficialClaudeConnection(t *testing.T) {
	cli := os.Getenv("ORCH_E2E_CLAUDE_BIN")
	if !filepath.IsAbs(cli) {
		t.Fatal("ORCH_E2E_CLAUDE_BIN must name the installed official CLI absolute path")
	}
	binary := buildServeE2EBinary(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	t.Cleanup(cancel)
	fixture := writeServeE2EFixture(t, binary)
	daemon := startServeE2E(t, ctx, binary, fixture)
	configHome := t.TempDir()
	env := append(serveE2EEnv(t.TempDir()), "CLAUDE_CONFIG_DIR="+configHome,
		"DISABLE_TELEMETRY=1", "DISABLE_ERROR_REPORTING=1", "NO_COLOR=1")
	// 仅测试生成的临时 token，不使用用户凭据；CLI 自己在隔离目录写配置。
	runClaudeConnectionCommand(t, ctx, cli, fixture.workspace, env, "mcp", "add", "--transport", "http",
		"--scope", "user", "orch", daemon.ready.Endpoint, "--header", "Authorization: Bearer "+fixture.token)
	status := runClaudeConnectionCommand(t, ctx, cli, fixture.workspace, env, "mcp", "get", "orch")
	if !strings.Contains(status, "Connected") || !strings.Contains(status, daemon.ready.Endpoint) {
		t.Fatalf("Claude did not confirm live orch connection: %s", status)
	}
	if err := stopServeProcessE2E(t, daemon, nil); err != nil {
		t.Fatal(err)
	}
	afterStop := runClaudeConnectionCommand(t, ctx, cli, fixture.workspace, env, "mcp", "get", "orch")
	if strings.Contains(afterStop, "Connected") || !strings.Contains(afterStop, "Failed to connect") {
		t.Fatalf("Claude health check did not detect stopped daemon: %s", afterStop)
	}
}

// TestStandaloneOfficialClaudeToolCall 使用真实 Claude print 会话调用 standalone MCP 工具。
func TestStandaloneOfficialClaudeToolCall(t *testing.T) {
	cli := os.Getenv("ORCH_E2E_CLAUDE_BIN")
	if !filepath.IsAbs(cli) {
		t.Fatal("ORCH_E2E_CLAUDE_BIN must name the installed official CLI absolute path")
	}
	model := strings.TrimSpace(os.Getenv("ORCH_E2E_CLAUDE_MODEL"))
	if model == "" {
		t.Fatal("ORCH_E2E_CLAUDE_MODEL must explicitly select a cheap Claude model")
	}
	binary := buildServeE2EBinary(t)
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	t.Cleanup(cancel)
	fixture := writeServeE2EFixture(t, binary)
	daemon := startServeE2E(t, ctx, binary, fixture)
	mcpConfig := writeClaudeToolCallConfig(t, daemon.ready.Endpoint, fixture.token)
	env := claudeToolCallEnv(t)
	output := runClaudeToolCallCommand(t, ctx, cli, fixture.workspace, env,
		"-p", "Use the orch MCP server's list_agents tool exactly once. After the tool returns, reply with the exact marker ORCH_TOOL_CALL_OK and no other text.",
		"--model", model, "--output-format", "stream-json", "--verbose", "--permission-mode", "dontAsk",
		"--tools", "", "--allowed-tools", "mcp__orch__list_agents", "--strict-mcp-config", "--mcp-config", mcpConfig,
		"--no-session-persistence")
	if err := validateClaudeToolCallStream(output); err != nil {
		t.Fatalf("Claude did not complete the orch tool call: %v", err)
	}
}

func writeClaudeToolCallConfig(t *testing.T, endpoint, token string) string {
	t.Helper()
	config := map[string]any{"mcpServers": map[string]any{"orch": map[string]any{
		"type": "http", "url": endpoint, "headers": map[string]string{"Authorization": "Bearer " + token},
	}}}
	raw, err := json.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "claude-mcp.json")
	writeServeE2EFile(t, path, raw, 0o600)
	return path
}

func claudeToolCallEnv(t *testing.T) []string {
	t.Helper()
	home := strings.TrimSpace(os.Getenv("HOME"))
	if !filepath.IsAbs(home) {
		t.Fatal("HOME must be absolute for authenticated Claude E2E")
	}
	env := make([]string, 0, len(os.Environ())+1)
	for _, value := range os.Environ() {
		key, _, _ := strings.Cut(value, "=")
		if strings.HasPrefix(key, "ANTHROPIC_") || key == "CLAUDE_CONFIG_DIR" || key == "CLAUDE_CODE_OAUTH_TOKEN" || strings.HasPrefix(key, "SUPER_DOLPHIN_") {
			continue
		}
		env = append(env, value)
	}
	env = append(env, "HOME="+home)
	// host_default auth deliberately leaves CLAUDE_CONFIG_DIR unset so Claude
	// can resolve the host login keychain/config selected by the service mode.
	return append(env, "DISABLE_TELEMETRY=1", "DISABLE_ERROR_REPORTING=1", "NO_COLOR=1")
}

// runClaudeConnectionCommand 只执行指定 CLI，输出中的测试 token 也脱敏，不继承个人配置环境。
func runClaudeConnectionCommand(t *testing.T, parent context.Context, cli, cwd string, env []string, args ...string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(parent, 20*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, cli, args...)
	command.Dir, command.Env = cwd, env
	raw, err := command.CombinedOutput()
	output := strings.ReplaceAll(string(raw), serveE2EToken, "<test-token>")
	if err != nil {
		t.Fatalf("Claude connection command failed: %v; output=%s", err, output)
	}
	return output
}

// runClaudeToolCallCommand 执行一次真实模型会话并保留 stream-json 事件用于工具调用断言。
func runClaudeToolCallCommand(t *testing.T, parent context.Context, cli, cwd string, env []string, args ...string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(parent, 100*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, cli, args...)
	command.Dir, command.Env = cwd, env
	raw, err := command.CombinedOutput()
	output := strings.ReplaceAll(string(raw), serveE2EToken, "<test-token>")
	if err != nil {
		t.Fatalf("Claude tool call failed: %v (raw authenticated CLI output withheld)", err)
	}
	return output
}
