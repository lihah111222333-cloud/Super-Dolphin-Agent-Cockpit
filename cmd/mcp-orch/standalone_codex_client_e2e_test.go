//go:build e2e

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/standalone/codexwire"
)

// TestStandaloneOfficialCodexClient 调用真实 Codex MCP client，不发送模型 turn，也不读取个人配置。
func TestStandaloneOfficialCodexClient(t *testing.T) {
	cli := os.Getenv("ORCH_E2E_CODEX_BIN")
	if !filepath.IsAbs(cli) {
		t.Fatal("ORCH_E2E_CODEX_BIN must name the installed official CLI absolute path")
	}
	binary := buildServeE2EBinary(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	t.Cleanup(cancel)
	fixture := writeServeE2EFixture(t, binary)
	daemon := startServeE2E(t, ctx, binary, fixture)
	home := t.TempDir()
	config := fmt.Sprintf("[mcp_servers.orch]\nurl = %q\nbearer_token_env_var = \"ORCH_E2E_TOKEN\"\nstartup_timeout_sec = 15\n", daemon.ready.Endpoint)
	writeServeE2EFile(t, filepath.Join(home, "config.toml"), []byte(config), 0o600)
	env := append(serveE2EEnv(t.TempDir()), "CODEX_HOME="+home, "ORCH_E2E_TOKEN="+fixture.token)
	client, err := codexwire.Start(ctx, cli, fixture.workspace, env, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := client.Close(); err != nil {
			t.Errorf("Codex client cleanup: %v", err)
		}
	})
	var started struct {
		Thread struct {
			ID string `json:"id"`
		} `json:"thread"`
	}
	if err := client.Client.CallResult(ctx, "thread/start", map[string]any{
		"cwd": fixture.workspace, "approvalPolicy": "never", "sandbox": "read-only",
	}, &started); err != nil {
		t.Fatal(err)
	}
	if started.Thread.ID == "" {
		t.Fatal("Codex did not create a client thread")
	}
	var result struct {
		Content []json.RawMessage `json:"content"`
		IsError bool              `json:"isError"`
	}
	if err := client.Client.CallResult(ctx, "mcpServer/tool/call", map[string]any{
		"threadId": started.Thread.ID, "server": "orch", "tool": "list_agents", "arguments": map[string]any{},
	}, &result); err != nil {
		t.Fatalf("official Codex -> orch tool call: %v", err)
	}
	if result.IsError || len(result.Content) == 0 {
		t.Fatalf("official Codex MCP result did not succeed: %#v", result)
	}
}
