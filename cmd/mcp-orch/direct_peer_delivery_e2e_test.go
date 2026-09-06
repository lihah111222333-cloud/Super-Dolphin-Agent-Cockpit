//go:build e2e

package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"go.uber.org/fx"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/standalone"
	agentstore "github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/agent"
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/tools"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
	mcpcommon "github.com/lihah111222333-cloud/super-dolphin-agent/internal/mcpserver/common"
)

// TestStandaloneDirectPeerSubmitsReceiverTurn 验证持久消息进入活体 receiver provider 的第二个 turn。
func TestStandaloneDirectPeerSubmitsReceiverTurn(t *testing.T) {
	runDirectPeerDelivery(t, "CODEX", "codex")
}

// TestStandaloneDirectPeerSubmitsClaudeReceiverTurn 验证 Claude provider 也经过独立 service terminal sink。
func TestStandaloneDirectPeerSubmitsClaudeReceiverTurn(t *testing.T) {
	runDirectPeerDelivery(t, "CLAUDE", "claude")
}

// TestStandaloneDirectPeerCrossProviderConversation 验证 Codex 与 Claude root task 通过 direct-peer 双向往返。
func TestStandaloneDirectPeerCrossProviderConversation(t *testing.T) {
	codexCLI, codexHome, codexModel := requireManagedProviderInputs(t, "CODEX")
	claudeCLI, claudeHome, claudeModel := requireManagedProviderInputs(t, "CLAUDE")
	workspace, state := t.TempDir(), t.TempDir()
	cfg := standalone.Config{SchemaVersion: 1, PrincipalRef: "operator/test", ScopeRef: "workspace/test", WorkspaceRoot: workspace, StateDir: state,
		Listen: "127.0.0.1:0", CredentialFile: filepath.Join(state, "unused-token"), Providers: []standalone.Provider{
			{Name: "codex", Executable: codexCLI, Home: codexHome, Models: []string{codexModel}, Sandbox: "workspace-write"},
			{Name: "claude", Executable: claudeCLI, Home: claudeHome, Models: []string{claudeModel}, AuthMode: standalone.ClaudeAuthHostDefault},
		}}
	db := managedProviderDatabase(t, state)
	app := startDirectPeerDeliveryApp(t, cfg, db)
	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()
	launchDirectPeerAgent(t, app.registry, workspace, "sender", "codex", codexModel)
	launchDirectPeerAgent(t, app.registry, workspace, "receiver", "claude", claudeModel)
	grantDirectPeerDirectionThroughRegistry(t, directPeerTestApp{registry: app.registry}, workspace, "sender", "receiver", "cross-grant-1")
	grantDirectPeerDirectionThroughRegistry(t, directPeerTestApp{registry: app.registry}, workspace, "receiver", "sender", "cross-grant-2")
	assertDirectPeerSubmittedReceipt(t, app.registry, workspace, "sender", "receiver", "cross-message-1", "Reply briefly that you received the Codex message.")
	waitDirectPeerAgentIdle(t, ctx, app.agents, "receiver")
	assertDirectPeerReceiverReport(t, app.reports, ctx, "receiver")
	assertDirectPeerSubmittedReceipt(t, app.registry, workspace, "receiver", "sender", "cross-message-2", "Reply briefly that you received the Claude message.")
	waitDirectPeerAgentIdle(t, ctx, app.agents, "sender")
	assertDirectPeerReceiverReport(t, app.reports, ctx, "sender")
	stopStandaloneTestApp(t, app.app)
}

func runDirectPeerDelivery(t *testing.T, prefix, providerName string) {
	t.Helper()
	cli, home, model := requireManagedProviderInputs(t, prefix)
	workspace, state := t.TempDir(), t.TempDir()
	cfg := standalone.Config{SchemaVersion: 1, PrincipalRef: "operator/test", ScopeRef: "workspace/test", WorkspaceRoot: workspace, StateDir: state,
		Listen: "127.0.0.1:0", CredentialFile: filepath.Join(state, "unused-token"),
		Providers: []standalone.Provider{{Name: providerName, Executable: cli, Home: home, Models: []string{model}, Sandbox: providerSandbox(providerName), AuthMode: directPeerAuthMode(providerName)}}}
	db := managedProviderDatabase(t, state)
	app := startDirectPeerDeliveryApp(t, cfg, db)
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	persistDirectPeerSender(t, db, workspace, model)
	launchDirectPeerReceiver(t, app.registry, workspace, providerName, model)
	grantDirectPeerThroughRegistry(t, directPeerTestApp{registry: app.registry}, workspace)
	result := callDirectPeerRegistry(t, app.registry, "send_direct_peer", mcpcommon.ToolScope{CWD: workspace, AgentID: "sender"},
		`{"receiver_agent_id":"receiver","idempotency_key":"delivery-1","message":"Reply briefly that the direct peer message was received. Do not use tools."}`)
	raw, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	var receipt struct {
		Status       string `json:"status"`
		SubmissionID string `json:"submission_id"`
	}
	if err := json.Unmarshal(raw, &receipt); err != nil || receipt.Status != "submitted" || receipt.SubmissionID == "" {
		t.Fatalf("delivery receipt=%s err=%v", raw, err)
	}
	waitDirectPeerReceiverIdle(t, ctx, app.agents)
	assertDirectPeerReceiverReport(t, app.reports, ctx, "receiver")
	stopStandaloneTestApp(t, app.app)
}

func directPeerAuthMode(provider string) string {
	if provider == "claude" {
		return standalone.ClaudeAuthHostDefault
	}
	return ""
}

type directPeerDeliveryApp struct {
	app       *fx.App
	registry  tools.Registry
	agents    contract.AgentStateReader
	reports   contract.AgentReportPort
	execution *standalone.Execution
}

func startDirectPeerDeliveryApp(t *testing.T, cfg standalone.Config, db *sql.DB) directPeerDeliveryApp {
	t.Helper()
	var registry tools.Registry
	var agents contract.AgentStateReader
	var reports contract.AgentReportPort
	var execution *standalone.Execution
	ready := t.TempDir()
	if err := os.Chmod(ready, 0o700); err != nil {
		t.Fatal(err)
	}
	app := newStandaloneTestApp(t, cfg, "test-only-operator-token-value", filepath.Join(ready, "ready.json"), db, fx.Populate(&registry, &agents, &reports, &execution))
	if err := app.Err(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := app.Start(ctx); err != nil {
		t.Fatal(err)
	}
	return directPeerDeliveryApp{app: app, registry: registry, agents: agents, reports: reports, execution: execution}
}

// assertDirectPeerReceiverReport 通过 service report 端口确认 provider terminal 已回到 orchestration owner。
func assertDirectPeerReceiverReport(t *testing.T, reports contract.AgentReportPort, ctx context.Context, agentID string) {
	t.Helper()
	if reports == nil {
		t.Fatal("standalone graph did not expose agent report port")
	}
	result, err := reports.GetReport(ctx, agentID)
	if err != nil || result.ReportSeq < 1 || result.Report == "" {
		t.Fatalf("receiver service report=%#v err=%v", result, err)
	}
}

func waitDirectPeerReceiverIdle(t *testing.T, ctx context.Context, agents contract.AgentStateReader) {
	waitDirectPeerAgentIdle(t, ctx, agents, "receiver")
}

func waitDirectPeerAgentIdle(t *testing.T, ctx context.Context, agents contract.AgentStateReader, agentID string) {
	t.Helper()
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	for {
		snapshot, err := agents.Snapshot(ctx, agentID)
		if err == nil && snapshot.State == "idle" && snapshot.ActiveTurnID == "" {
			return
		}
		select {
		case <-ctx.Done():
			t.Fatalf("direct peer receiver did not settle: %v", ctx.Err())
		case <-ticker.C:
		}
	}
}

func launchDirectPeerReceiver(t *testing.T, registry tools.Registry, workspace, provider, model string) {
	launchDirectPeerAgent(t, registry, workspace, "receiver", provider, model)
}

func launchDirectPeerAgent(t *testing.T, registry tools.Registry, workspace, agentID, provider, model string) {
	t.Helper()
	raw, err := json.Marshal(map[string]any{"agent_id": agentID, "name": agentID, "cwd": workspace,
		"provider": provider, "model": model})
	if err != nil {
		t.Fatal(err)
	}
	callDirectPeerRegistry(t, registry, "launch_agent", mcpcommon.ToolScope{CWD: workspace}, string(raw))
}

func assertDirectPeerSubmittedReceipt(t *testing.T, registry tools.Registry, workspace, sender, receiver, key, body string) {
	t.Helper()
	result := callDirectPeerRegistry(t, registry, "send_direct_peer", mcpcommon.ToolScope{CWD: workspace, AgentID: sender},
		string(mustJSON(t, map[string]any{"receiver_agent_id": receiver, "idempotency_key": key, "message": body})))
	raw, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	var receipt struct {
		Status       string `json:"status"`
		SubmissionID string `json:"submission_id"`
	}
	if err := json.Unmarshal(raw, &receipt); err != nil || receipt.Status != "submitted" || receipt.SubmissionID == "" {
		t.Fatalf("delivery receipt=%s err=%v", raw, err)
	}
}

func mustJSON(t *testing.T, value any) []byte {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func persistDirectPeerSender(t *testing.T, db *sql.DB, workspace, model string) {
	t.Helper()
	if err := agentstore.NewSessionStore(db).CreateSession(t.Context(), standalone.SessionRecord{AgentID: "sender", ThreadID: "sender-thread",
		Provider: "codex", Name: "sender", CWD: workspace, Model: model, CreatedAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
}

func providerSandbox(provider string) string {
	if provider == "codex" {
		return "workspace-write"
	}
	return ""
}
