//go:build e2e

package main

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
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

// TestStandaloneDirectPeerCrossProviderConversation 由测试经Registry注入两个方向的消息，验证真实Codex与Claude接收成功。
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
	for round := 0; round < 3; round++ {
		assertDirectPeerDeliveryDirection(t, ctx, app, workspace, "sender", "receiver", fmt.Sprintf("cross-to-claude-%d", round))
		assertDirectPeerDeliveryDirection(t, ctx, app, workspace, "receiver", "sender", fmt.Sprintf("cross-to-codex-%d", round))
	}
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
	assertDirectPeerDeliveryDirection(t, ctx, app, workspace, "sender", "receiver", "delivery-1")
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

// assertDirectPeerDeliveryDirection 由测试显式投递单向消息，以随机标记关联本次成功报告，不声称模型自主发信。
func assertDirectPeerDeliveryDirection(t *testing.T, ctx context.Context, app directPeerDeliveryApp, workspace, sender, receiver, key string) {
	t.Helper()
	waitDirectPeerAgentIdle(t, ctx, app.agents, receiver)
	baseline, err := app.reports.GetReport(ctx, receiver)
	if err != nil || baseline.AgentID != receiver || baseline.ReportSeq < 0 {
		t.Fatalf("invalid receiver report baseline: agent=%s seq=%d err=%v", baseline.AgentID, baseline.ReportSeq, err)
	}
	marker := "DIRECT_PEER_" + rand.Text()
	message := fmt.Sprintf("Reply with the exact marker %s and no other text. Do not use tools or delegate.", marker)
	assertDirectPeerSubmittedReceipt(t, app.registry, workspace, sender, receiver, key, message)
	waitDirectPeerAgentIdle(t, ctx, app.agents, receiver)
	assertDirectPeerDeliveryReport(t, ctx, app, receiver, baseline.ReportSeq, marker)
}

// assertDirectPeerDeliveryReport 要求本次接收者的新报告包含唯一标记，且对应公开outcome为success。
func assertDirectPeerDeliveryReport(t *testing.T, ctx context.Context, app directPeerDeliveryApp, agentID string, baseline int64, marker string) {
	t.Helper()
	result, err := app.reports.GetReport(ctx, agentID)
	if err != nil || result.AgentID != agentID || result.ReportSeq <= baseline || result.State != "idle" || !strings.Contains(result.Report, marker) {
		t.Fatalf("receiver report mismatch: agent=%s seq=%d baseline=%d state=%s marker_present=%t report=%q err=%v", result.AgentID, result.ReportSeq, baseline, result.State, strings.Contains(result.Report, marker), result.Report, err)
	}
	assertDirectPeerDeliveryOutcome(t, ctx, app.agents, agentID, result.Report)
}

// assertDirectPeerDeliveryOutcome 关联同一报告的成功终态，拒绝idle失败或旧报告。
func assertDirectPeerDeliveryOutcome(t *testing.T, ctx context.Context, agents contract.AgentStateReader, agentID, report string) {
	t.Helper()
	snapshot, err := agents.Snapshot(ctx, agentID)
	if err != nil || snapshot.AgentID != agentID || snapshot.State != "idle" || snapshot.ActiveTurnID != "" || snapshot.Outcome == nil || string(snapshot.Outcome.Kind) != "success" || snapshot.LastReport != report {
		t.Fatalf("receiver terminal does not prove this successful report: agent=%s state=%s outcome=%+v err=%v", snapshot.AgentID, snapshot.State, snapshot.Outcome, err)
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
		if err != nil {
			t.Fatalf("read direct peer receiver state: %v", err)
		}
		if snapshot.State == "failed" || snapshot.State == "stopped" {
			t.Fatalf("direct peer receiver terminated before idle: state=%s outcome=%+v", snapshot.State, snapshot.Outcome)
		}
		if snapshot.State == "idle" && snapshot.ActiveTurnID == "" {
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

// assertDirectPeerReceiverReport 为既有恢复场景保留报告存在性检查；消息内容闭包由delivery专用断言承担。
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
