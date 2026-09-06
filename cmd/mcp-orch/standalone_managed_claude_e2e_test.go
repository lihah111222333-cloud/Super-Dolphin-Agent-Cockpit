//go:build e2e

package main

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/standalone"
	agentstore "github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/agent"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
	turndto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/turn"
	mcpcommon "github.com/lihah111222333-cloud/super-dolphin-agent/internal/mcpserver/common"
)

// TestStandaloneManagedClaudeTurn 运行真实 Claude stream-json provider，验证持久绑定与根终态。
func TestStandaloneManagedClaudeTurn(t *testing.T) {
	cli, home, model := requireManagedProviderInputs(t, "CLAUDE")
	workspace, state := t.TempDir(), t.TempDir()
	cfg := standalone.Config{SchemaVersion: 1, WorkspaceRoot: workspace, StateDir: state,
		Listen: "127.0.0.1:0", CredentialFile: filepath.Join(state, "unused-token"),
		Providers: []standalone.Provider{{Name: "claude", Executable: cli, Home: home, Models: []string{model}, AuthMode: standalone.ClaudeAuthHostDefault}}}
	db := managedProviderDatabase(t, state)
	execution := standalone.NewExecution(cfg)
	if err := execution.BindSessionStore(agentstore.NewSessionStore(db)); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := execution.Close(); err != nil {
			t.Errorf("managed Claude cleanup: %v", err)
		}
	})
	terminal := make(chan turndto.TurnTerminalV2, 1)
	execution.BindTerminalSink(func(_ context.Context, event turndto.TurnTerminalV2) { terminal <- event })
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	launch := contract.LaunchRequest{AgentID: "managed-claude", Name: "managed-claude", Cwd: workspace,
		Env: []string{"AGENT_PROVIDER=claude", "AGENT_MODEL=" + model}}
	result, err := execution.Launch(ctx, launch)
	if err != nil {
		t.Fatal(err)
	}
	assertManagedSessionPersisted(t, ctx, db, launch.AgentID, result)
	turnID, err := execution.SubmitTurn(ctx, contract.TurnSubmission{AgentID: launch.AgentID,
		Inputs: []turndto.InputItem{{Type: "text", Content: "Reply briefly that the managed Claude turn completed. Do not use tools."}}})
	if err != nil || turnID == "" {
		t.Fatalf("submit managed Claude turn: id=%q err=%v", turnID, err)
	}
	select {
	case event := <-terminal:
		if event.Outcome != "success" || strings.TrimSpace(event.PublicSummary) == "" {
			t.Fatalf("managed Claude terminal=%#v", event)
		}
	case <-ctx.Done():
		t.Fatalf("managed Claude turn timeout: %v", ctx.Err())
	}
}

// TestStandaloneManagedClaudeStop 验证 Claude 不支持归档时，stop_agent 仍执行真实 Stop 而不伪造 archived。
func TestStandaloneManagedClaudeStop(t *testing.T) {
	cli, home, model := requireManagedProviderInputs(t, "CLAUDE")
	workspace, state := t.TempDir(), t.TempDir()
	cfg := standalone.Config{SchemaVersion: 1, PrincipalRef: "operator/test", ScopeRef: "workspace/test", WorkspaceRoot: workspace, StateDir: state,
		Listen: "127.0.0.1:0", CredentialFile: filepath.Join(state, "unused-token"),
		Providers: []standalone.Provider{{Name: "claude", Executable: cli, Home: home, Models: []string{model}, AuthMode: standalone.ClaudeAuthHostDefault}}}
	db := managedProviderDatabase(t, state)
	app := startDirectPeerDeliveryApp(t, cfg, db)
	defer stopStandaloneTestApp(t, app.app)
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	launchDirectPeerAgent(t, app.registry, workspace, "claude-stop", "claude", model)
	waitDirectPeerAgentIdle(t, ctx, app.agents, "claude-stop")
	result := callDirectPeerRegistry(t, app.registry, "stop_agent", mcpcommon.ToolScope{CWD: workspace},
		`{"agent_id":"claude-stop","wait":true,"timeout_ms":10000}`)
	assertManagedClaudeStopResult(t, result)
	if app.execution == nil || app.execution.IsRunning(ctx, "claude-stop") {
		t.Fatal("Claude execution remained running after stop_agent")
	}
}

func assertManagedClaudeStopResult(t *testing.T, result any) {
	t.Helper()
	raw, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	var response struct {
		Success  bool   `json:"success"`
		AgentID  string `json:"agent_id"`
		Archived bool   `json:"archived"`
		Stopped  bool   `json:"stopped"`
		State    string `json:"state"`
	}
	if err := json.Unmarshal(raw, &response); err != nil {
		t.Fatalf("stop response=%s: %v", raw, err)
	}
	if !response.Success || response.AgentID != "claude-stop" || response.Archived || !response.Stopped {
		t.Fatalf("Claude stop response=%s, want successful non-archive stop", raw)
	}
	if response.State != "stopped" && response.State != "failed" {
		t.Fatalf("Claude stop state=%q, want stopped or failed terminal", response.State)
	}
}
