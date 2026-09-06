//go:build e2e

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/standalone/codexwire"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
)

// TestStandaloneCodexClientLaunchesManagedCodexThroughMCP verifies the complete
// standalone binary -> HTTP MCP -> official Codex client -> managed Codex
// launch/turn/follow-up path. The client uses a temporary CODEX_HOME, while
// the managed provider receives only the explicitly configured provider home.
func TestStandaloneCodexClientLaunchesManagedCodexThroughMCP(t *testing.T) {
	cli, providerHome, model := requireManagedProviderInputs(t, "CODEX")
	binary := buildServeE2EBinary(t)
	ctx, cancel := context.WithTimeout(context.Background(), 240*time.Second)
	t.Cleanup(cancel)
	fixture := writeManagedCodexClientE2EFixture(t, binary, cli, providerHome, model)
	daemon := startServeE2E(t, ctx, binary, fixture)

	clientHome := t.TempDir()
	clientConfig := fmt.Sprintf("[mcp_servers.orch]\nurl = %q\nbearer_token_env_var = \"ORCH_E2E_TOKEN\"\nstartup_timeout_sec = 15\n", daemon.ready.Endpoint)
	writeServeE2EFile(t, filepath.Join(clientHome, "config.toml"), []byte(clientConfig), 0o600)
	clientEnv := append(serveE2EEnv(t.TempDir()), "CODEX_HOME="+clientHome, "ORCH_E2E_TOKEN="+fixture.token)
	client, err := codexwire.Start(ctx, cli, fixture.workspace, clientEnv, nil)
	if err != nil {
		t.Fatalf("start official Codex MCP client: %v", err)
	}
	t.Cleanup(func() {
		if err := client.Close(); err != nil {
			t.Errorf("Codex client cleanup: %v", err)
		}
	})

	threadID := startStandaloneCodexClientThread(t, ctx, client, fixture.workspace)
	launch := callStandaloneCodexClientTool(t, ctx, client, threadID, "launch_agent", map[string]any{
		"agent_id": "daemon-managed-codex",
		"name":     "daemon-managed-codex",
		"cwd":      fixture.workspace,
		"provider": "codex",
		"model":    model,
		"prompt":   "Reply briefly with a confirmation that the first managed turn completed. Do not use tools.",
	})
	assertStandaloneCodexLaunchResult(t, launch)

	first := waitStandaloneCodexClientReport(t, ctx, client, threadID, "daemon-managed-codex", 0, "")
	waitStandaloneCodexRuntimeIdleThroughMCP(t, ctx, client, threadID, "daemon-managed-codex")
	observed := assertStandaloneManagedProcessTree(t, daemon, cli)
	followUp := callStandaloneCodexClientTool(t, ctx, client, threadID, "send_message", map[string]any{
		"agent_id":    "daemon-managed-codex",
		"message":     "Reply briefly with a confirmation that the second managed turn completed. Do not use tools.",
		"wait_report": true,
		"timeout_ms":  60000,
	})
	assertStandaloneCodexFollowUpResult(t, followUp, first)
	if err := stopServeProcessE2E(t, daemon, nil); err != nil {
		t.Fatalf("standalone daemon shutdown after Codex dialogue: %v", err)
	}
	assertServeEndpointClosed(t, &http.Client{Timeout: 5 * time.Second}, daemon.ready.Endpoint)
	if daemon.stdout.String() != "" {
		t.Fatalf("daemon wrote stdout: %s", serveE2ETextSummary(daemon.stdout.String()))
	}
	assertNoStandaloneManagedDescendants(t, observed)
}

func writeManagedCodexClientE2EFixture(t *testing.T, binary, executable, home, model string) serveE2EFixture {
	t.Helper()
	fixture := writeServeE2EFixture(t, binary)
	var config map[string]any
	if err := json.Unmarshal(readServeE2EFile(t, fixture.config), &config); err != nil {
		t.Fatalf("decode standalone fixture config: %v", err)
	}
	providers, ok := config["providers"].([]any)
	if !ok || len(providers) != 1 {
		t.Fatalf("standalone fixture providers invalid: type=%T count=%d, want one provider", config["providers"], len(providers))
	}
	provider, ok := providers[0].(map[string]any)
	if !ok {
		t.Fatalf("standalone fixture provider has type %T, want object", providers[0])
	}
	provider["name"], provider["executable"], provider["home"] = "codex", executable, home
	provider["models"], provider["sandbox"] = []string{model}, "workspace-write"
	raw, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		t.Fatalf("encode standalone fixture config: %v", err)
	}
	writeServeE2EFile(t, fixture.config, raw, 0o600)
	return fixture
}

func startStandaloneCodexClientThread(t *testing.T, ctx context.Context, client *codexwire.Process, cwd string) string {
	t.Helper()
	var started struct {
		Thread struct {
			ID string `json:"id"`
		} `json:"thread"`
	}
	if err := client.Client.CallResult(ctx, "thread/start", map[string]any{
		"cwd": cwd, "approvalPolicy": "never", "sandbox": "read-only",
	}, &started); err != nil {
		t.Fatalf("official Codex thread/start: %v", err)
	}
	if strings.TrimSpace(started.Thread.ID) == "" {
		t.Fatal("official Codex thread/start returned empty thread id")
	}
	return started.Thread.ID
}

func callStandaloneCodexClientTool(t *testing.T, ctx context.Context, client *codexwire.Process, threadID, name string, arguments map[string]any) json.RawMessage {
	t.Helper()
	var result struct {
		Content           []struct{ Type, Text string } `json:"content"`
		StructuredContent json.RawMessage               `json:"structuredContent"`
		IsError           bool                          `json:"isError"`
	}
	if err := client.Client.CallResult(ctx, "mcpServer/tool/call", map[string]any{
		"threadId": threadID, "server": "orch", "tool": name, "arguments": arguments,
	}, &result); err != nil {
		t.Fatalf("official Codex MCP tool %s: %v", name, err)
	}
	if result.IsError {
		t.Fatalf("official Codex MCP tool %s returned isError", name)
	}
	if len(result.StructuredContent) != 0 && string(result.StructuredContent) != "null" {
		return result.StructuredContent
	}
	if len(result.Content) == 0 || result.Content[0].Type != "text" || strings.TrimSpace(result.Content[0].Text) == "" {
		t.Fatalf("official Codex MCP tool %s returned no text content", name)
	}
	return json.RawMessage(result.Content[0].Text)
}

func assertStandaloneCodexLaunchResult(t *testing.T, raw json.RawMessage) {
	t.Helper()
	var result struct {
		Success bool   `json:"success"`
		AgentID string `json:"agent_id"`
		Status  string `json:"status"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatalf("decode launch_agent result: %v; payload=%s", err, serveE2EPayloadSummary(raw))
	}
	if !result.Success || result.AgentID != "daemon-managed-codex" || result.Status != "launching" {
		t.Fatalf("launch_agent result invalid: success=%t agent_id=%q status=%q", result.Success, result.AgentID, result.Status)
	}
}

func waitStandaloneCodexClientReport(t *testing.T, ctx context.Context, client *codexwire.Process, threadID, agentID string, afterSeq int64, marker string) contract.AgentReportResult {
	t.Helper()
	deadline := time.NewTimer(120 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	lastSummary := "<no report received>"
	for {
		raw := callStandaloneCodexClientTool(t, ctx, client, threadID, "get_agent_report", map[string]any{"agent_id": agentID})
		lastSummary = serveE2EPayloadSummary(raw)
		var report contract.AgentReportResult
		if err := json.Unmarshal(raw, &report); err != nil {
			t.Fatalf("decode get_agent_report result: %v; payload=%s", err, lastSummary)
		}
		if report.AgentID == agentID && (report.State == "failed" || report.State == "stopped") {
			t.Fatalf("standalone Codex client report reached terminal state=%q report_seq=%d report_present=%t", report.State, report.ReportSeq, strings.TrimSpace(report.Report) != "")
		}
		if standaloneCodexReportReady(report, agentID, afterSeq, marker) {
			return report
		}
		select {
		case <-ctx.Done():
			t.Fatalf("standalone Codex client report context expired: %v; last=%s", ctx.Err(), lastSummary)
		case <-deadline.C:
			t.Fatalf("standalone Codex client report timeout: last=%s", lastSummary)
		case <-ticker.C:
		}
	}
}

func standaloneCodexReportReady(report contract.AgentReportResult, agentID string, afterSeq int64, marker string) bool {
	if report.AgentID != agentID || report.ReportSeq <= afterSeq || report.State != "idle" {
		return false
	}
	if strings.TrimSpace(report.Report) == "" {
		return false
	}
	return marker == "" || strings.Contains(report.Report, marker)
}

func waitStandaloneCodexRuntimeIdleThroughMCP(t *testing.T, ctx context.Context, client *codexwire.Process, threadID, agentID string) {
	t.Helper()
	deadline := time.NewTimer(20 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		raw := callStandaloneCodexClientTool(t, ctx, client, threadID, "list_agents", map[string]any{"include_inactive": true})
		agents, err := decodeServeAgentSnapshots(raw)
		if err != nil {
			t.Fatalf("decode Codex list_agents result while waiting for idle: %v; payload=%s", err, serveE2EPayloadSummary(raw))
		}
		if standaloneCodexAgentIdle(t, agents, agentID) {
			return
		}
		select {
		case <-ctx.Done():
			t.Fatalf("standalone Codex managed runtime did not become idle: %v", ctx.Err())
		case <-deadline.C:
			t.Fatal("standalone Codex managed runtime did not become idle")
		case <-ticker.C:
		}
	}
}

func standaloneCodexAgentIdle(t *testing.T, agents []contract.AgentSnapshot, agentID string) bool {
	t.Helper()
	for _, agent := range agents {
		if agent.AgentID != agentID {
			continue
		}
		if agent.State == "failed" || agent.State == "stopped" {
			t.Fatalf("standalone Codex managed runtime reached terminal state=%q before idle", agent.State)
		}
		if agent.State == "idle" && strings.TrimSpace(agent.ActiveTurnID) == "" {
			return true
		}
	}
	return false
}

func assertStandaloneCodexFollowUpResult(t *testing.T, raw json.RawMessage, first contract.AgentReportResult) {
	t.Helper()
	var result struct {
		Success           bool                       `json:"success"`
		AgentID           string                     `json:"agent_id"`
		PreviousReportSeq int64                      `json:"previous_report_seq"`
		Report            contract.AgentReportResult `json:"report"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatalf("decode send_message result: %v; payload=%s", err, serveE2EPayloadSummary(raw))
	}
	if !result.Success || result.AgentID != first.AgentID || result.PreviousReportSeq != first.ReportSeq || result.Report.ReportSeq <= first.ReportSeq || result.Report.State != "idle" || strings.TrimSpace(result.Report.Report) == "" {
		t.Fatalf("send_message result invalid: success=%t agent_id=%q previous_report_seq=%d report_seq=%d report_state=%q report_present=%t; want follow-up report after seq %d", result.Success, result.AgentID, result.PreviousReportSeq, result.Report.ReportSeq, result.Report.State, strings.TrimSpace(result.Report.Report) != "", first.ReportSeq)
	}
}
