//go:build e2e

package main

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/standalone"
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/tools"
	mcpcommon "github.com/lihah111222333-cloud/super-dolphin-agent/internal/mcpserver/common"
)

// TestStandaloneManagedDaemonRehydratesCodexAfterExecutionLoss 验证新 daemon 从持久 binding 接管旧 thread。
func TestStandaloneManagedDaemonRehydratesCodexAfterExecutionLoss(t *testing.T) {
	cli, home, model := requireManagedProviderInputs(t, "CODEX")
	workspace, state := t.TempDir(), t.TempDir()
	cfg := standalone.Config{SchemaVersion: 1, PrincipalRef: "operator/test", ScopeRef: "workspace/test", WorkspaceRoot: workspace, StateDir: state,
		Listen: "127.0.0.1:0", CredentialFile: filepath.Join(state, "unused-token"),
		Providers: []standalone.Provider{{Name: "codex", Executable: cli, Home: home, Models: []string{model}, Sandbox: "workspace-write"}}}
	db := managedProviderDatabase(t, state)
	first := startDirectPeerDeliveryApp(t, cfg, db)
	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()
	launchDirectPeerAgent(t, first.registry, workspace, "recoverable", "codex", model)
	waitDirectPeerAgentIdle(t, ctx, first.agents, "recoverable")
	sendRecoveryMessage(t, first.registry, workspace, "first daemon turn")
	waitDirectPeerAgentIdle(t, ctx, first.agents, "recoverable")
	assertDirectPeerReceiverReport(t, first.reports, ctx, "recoverable")
	if first.execution == nil {
		t.Fatal("standalone graph did not expose execution")
	}
	if err := first.execution.Close(); err != nil {
		t.Fatal(err)
	}
	stopStandaloneTestApp(t, first.app)
	second := startDirectPeerDeliveryApp(t, cfg, db)
	defer stopStandaloneTestApp(t, second.app)
	sendRecoveryMessage(t, second.registry, workspace, "resumed daemon turn")
	waitDirectPeerAgentIdle(t, ctx, second.agents, "recoverable")
	assertDirectPeerReceiverReport(t, second.reports, ctx, "recoverable")
}

func sendRecoveryMessage(t *testing.T, registry tools.Registry, workspace, message string) {
	t.Helper()
	result := callDirectPeerRegistry(t, registry, "send_message", mcpcommon.ToolScope{CWD: workspace}, string(mustJSON(t, map[string]any{
		"agent_id": "recoverable", "message": message,
	})))
	if raw, err := json.Marshal(result); err != nil || len(raw) == 0 {
		t.Fatalf("send recovery message result=%#v err=%v", result, err)
	}
}
