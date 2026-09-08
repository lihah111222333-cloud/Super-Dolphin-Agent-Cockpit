//go:build e2e

package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/standalone"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/pidregistry"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/securefs"
)

// TestStandaloneBinaryLaunchesManagedClaudeThroughMCP 验证真实 standalone binary
// 通过 MCP launch_agent 启动 Claude，并从同一 daemon 读取后续 turn 报告。
func TestStandaloneBinaryLaunchesManagedClaudeThroughMCP(t *testing.T) {
	cli, home, model := requireManagedProviderInputs(t, "CLAUDE")
	binary := buildServeE2EBinary(t)
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Second)
	t.Cleanup(cancel)
	fixture := writeManagedServeE2EFixture(t, "claude", cli, home, model)
	daemon := startServeE2E(t, ctx, binary, fixture)
	// send_message(wait_report=true) holds one HTTP request until the second
	// provider turn reaches a terminal report; keep transport timeout above the
	// explicit tool timeout so a slow but valid Claude turn is not truncated.
	client := &http.Client{Timeout: 90 * time.Second}
	session, _ := initializeServeE2E(t, client, daemon.ready.Endpoint, fixture.token)
	launchManagedClaudeThroughMCP(t, client, daemon.ready.Endpoint, fixture, session, model)
	firstSeq := waitManagedClaudeReportThroughMCP(t, ctx, client, daemon.ready.Endpoint, fixture.token, session, "daemon-managed-claude", 0, "DAEMON_CLAUDE_TURN_OK")
	waitManagedClaudeRuntimeIdleThroughMCP(t, ctx, client, daemon.ready.Endpoint, fixture.token, session, "daemon-managed-claude")
	observed := assertStandaloneManagedProcessTree(t, daemon, cli)
	second := sendManagedClaudeFollowUpThroughMCP(t, client, daemon.ready.Endpoint, fixture.token, session)
	if second.ReportSeq <= firstSeq || second.State != "idle" || !strings.Contains(second.Report, "DAEMON_CLAUDE_SECOND_OK") {
		t.Fatalf("Claude follow-up report invalid: agent_id=%q state=%q report_seq=%d report_present=%t, want idle report_seq>%d with second marker", second.AgentID, second.State, second.ReportSeq, strings.TrimSpace(second.Report) != "", firstSeq)
	}
	if err := stopServeProcessE2E(t, daemon, nil); err != nil {
		t.Fatalf("standalone daemon shutdown after Claude dialogue: %v", err)
	}
	assertServeEndpointClosed(t, client, daemon.ready.Endpoint)
	if daemon.stdout.String() != "" {
		t.Fatalf("daemon wrote stdout: %s", serveE2ETextSummary(daemon.stdout.String()))
	}
	assertNoStandaloneManagedDescendants(t, observed)
}

func waitManagedClaudeRuntimeIdleThroughMCP(t *testing.T, ctx context.Context, client *http.Client, endpoint, token, session, agentID string) {
	t.Helper()
	deadline := time.NewTimer(20 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	requestID := 5
	for {
		raw := callServeToolStructured(t, client, endpoint, token, session, requestID, "list_agents", map[string]any{"include_inactive": true})
		requestID++
		agents, err := decodeServeAgentSnapshots(raw)
		if err != nil {
			t.Fatalf("decode list_agents result while waiting for idle: %v; payload=%s", err, serveE2EPayloadSummary(raw))
		}
		if standaloneManagedClaudeRuntimeIdle(t, agents, agentID) {
			return
		}
		select {
		case <-ctx.Done():
			t.Fatalf("standalone daemon Claude runtime did not become idle: %v", ctx.Err())
		case <-deadline.C:
			t.Fatalf("standalone daemon Claude runtime did not become idle")
		case <-ticker.C:
		}
	}
}

func standaloneManagedClaudeRuntimeIdle(t *testing.T, agents []contract.AgentSnapshot, agentID string) bool {
	t.Helper()
	for _, agent := range agents {
		if agent.AgentID != agentID {
			continue
		}
		if agent.State == "failed" || agent.State == "stopped" {
			t.Fatalf("standalone daemon Claude runtime reached terminal state=%q before idle", agent.State)
		}
		if agent.State == "idle" && strings.TrimSpace(agent.ActiveTurnID) == "" {
			return true
		}
	}
	return false
}

func decodeServeAgentSnapshots(raw json.RawMessage) ([]contract.AgentSnapshot, error) {
	var agents []contract.AgentSnapshot
	if err := json.Unmarshal(raw, &agents); err == nil {
		return agents, nil
	}
	var envelope struct {
		Items  []contract.AgentSnapshot `json:"items"`
		Agents []contract.AgentSnapshot `json:"agents"`
		Data   []contract.AgentSnapshot `json:"data"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return nil, err
	}
	switch {
	case envelope.Items != nil:
		return envelope.Items, nil
	case envelope.Agents != nil:
		return envelope.Agents, nil
	case envelope.Data != nil:
		return envelope.Data, nil
	default:
		return nil, errors.New("list_agents result has no agent collection")
	}
}

type standaloneProcessEntry struct {
	pid, ppid int
	command   string
}

// assertStandaloneManagedProcessTree checks command-line ownership without
// dumping environments or arguments that could contain credentials.
func assertStandaloneManagedProcessTree(t *testing.T, daemon *serveE2EProcess, providerExecutable string) standaloneManagedProcessSnapshot {
	t.Helper()
	assertStandaloneUnixProcessProof(t)
	rootPID := standaloneDaemonPID(t, daemon)
	entries, err := readStandaloneProcessTable()
	if err != nil {
		t.Fatalf("read standalone process tree: %v", err)
	}
	assertStandaloneDaemonRoot(t, entries, rootPID)
	descendants := standaloneDescendants(entries, rootPID)
	if !standaloneTreeContainsProvider(descendants, providerExecutable) {
		t.Fatalf("standalone daemon process tree did not contain configured provider executable")
	}
	assertStandaloneNoForbiddenProcess(t, descendants)
	observed, err := captureStandaloneManagedProcesses(descendants, providerExecutable, pidregistry.CaptureStableProcessIdentity)
	if err != nil {
		t.Fatalf("capture managed process evidence: %v", err)
	}
	return observed
}

func assertStandaloneUnixProcessProof(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("process-tree proof requires the Unix ps contract; Windows evidence remains pending")
	}
}

func standaloneDaemonPID(t *testing.T, daemon *serveE2EProcess) int {
	t.Helper()
	if daemon == nil || daemon.cmd == nil || daemon.cmd.Process == nil {
		t.Fatal("standalone daemon process handle is required for process-tree proof")
	}
	return daemon.cmd.Process.Pid
}

func assertStandaloneDaemonRoot(t *testing.T, entries map[int]standaloneProcessEntry, rootPID int) {
	t.Helper()
	root, ok := entries[rootPID]
	if !ok {
		t.Fatal("standalone daemon process is missing from ps output")
	}
	if !strings.Contains(strings.ToLower(root.command), "mcp-orch") {
		t.Fatalf("daemon process tree root is not the standalone mcp-orch binary")
	}
}

func assertStandaloneNoForbiddenProcess(t *testing.T, entries []standaloneProcessEntry) {
	t.Helper()
	for _, entry := range entries {
		if standaloneForbiddenProcessCommand(entry.command) {
			t.Fatal("standalone daemon process tree contains forbidden runtime command")
		}
	}
}

func readStandaloneProcessTable() (map[int]standaloneProcessEntry, error) {
	raw, err := exec.Command("ps", "-axo", "pid=,ppid=,command=").Output()
	if err != nil {
		return nil, err
	}
	entries := make(map[int]standaloneProcessEntry)
	for _, line := range strings.Split(string(raw), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 3 {
			if strings.TrimSpace(line) == "" {
				continue
			}
			return nil, errors.New("ps returned a malformed process row")
		}
		pid, err := strconv.Atoi(fields[0])
		if err != nil {
			return nil, errors.New("ps returned an invalid process id")
		}
		ppid, err := strconv.Atoi(fields[1])
		if err != nil {
			return nil, errors.New("ps returned an invalid parent process id")
		}
		entries[pid] = standaloneProcessEntry{pid: pid, ppid: ppid, command: strings.Join(fields[2:], " ")}
	}
	return entries, nil
}

func standaloneDescendants(entries map[int]standaloneProcessEntry, rootPID int) []standaloneProcessEntry {
	children := make(map[int][]int)
	for pid, entry := range entries {
		children[entry.ppid] = append(children[entry.ppid], pid)
	}
	queue := append([]int(nil), children[rootPID]...)
	result := make([]standaloneProcessEntry, 0, len(queue))
	seen := make(map[int]struct{}, len(queue))
	for len(queue) > 0 {
		pid := queue[0]
		queue = queue[1:]
		if _, ok := seen[pid]; ok {
			continue
		}
		seen[pid] = struct{}{}
		entry, ok := entries[pid]
		if !ok {
			continue
		}
		result = append(result, entry)
		queue = append(queue, children[pid]...)
	}
	return result
}

func standaloneTreeContainsProvider(entries []standaloneProcessEntry, executable string) bool {
	resolved, err := filepath.EvalSymlinks(executable)
	if err != nil {
		resolved = executable
	}
	base := filepath.Base(resolved)
	for _, entry := range entries {
		if strings.Contains(entry.command, executable) || strings.Contains(entry.command, resolved) || strings.Contains(entry.command, base) {
			return true
		}
	}
	return false
}

func standaloneForbiddenProcessCommand(command string) bool {
	lower := strings.ToLower(command)
	for _, token := range []string{"agent-terminal", "super-dolphin-agent", "go_agent_", "super_dolphin_", "project_root", "go-agent-ctl"} {
		if strings.Contains(lower, token) {
			return true
		}
	}
	return false
}

func assertNoStandaloneManagedDescendants(t *testing.T, observed standaloneManagedProcessSnapshot) {
	t.Helper()
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		alive, err := standaloneManagedProcessesAlive(observed, pidregistry.CaptureStableProcessIdentity)
		if err != nil {
			t.Fatalf("read managed process identity after shutdown: %v", err)
		}
		if len(alive) == 0 {
			return
		}
		select {
		case <-deadline.C:
			for _, pid := range alive {
				state, stateErr := exec.Command("ps", "-p", strconv.Itoa(pid), "-o", "pid=,ppid=,pgid=,stat=,etime=,comm=").CombinedOutput()
				t.Logf("remaining process state: %s; read_error=%v", state, stateErr)
			}
			t.Fatalf("observed managed processes remain alive after expected termination: pids=%v", alive)
		case <-ticker.C:
		}
	}
}

func launchManagedClaudeThroughMCP(t *testing.T, client *http.Client, endpoint string, fixture serveE2EFixture, session, model string) {
	t.Helper()
	launch := callServeToolStructured(t, client, endpoint, fixture.token, session, 2,
		"launch_agent", map[string]any{
			"agent_id": "daemon-managed-claude",
			"name":     "daemon-managed-claude",
			"cwd":      fixture.workspace,
			"provider": "claude",
			"model":    model,
			"prompt":   "Reply briefly with the exact marker DAEMON_CLAUDE_TURN_OK and no tools.",
		})
	var launchResult struct {
		Success bool   `json:"success"`
		AgentID string `json:"agent_id"`
	}
	if err := json.Unmarshal(launch, &launchResult); err != nil {
		t.Fatalf("decode launch_agent result: %v; payload=%s", err, serveE2EPayloadSummary(launch))
	}
	if !launchResult.Success || launchResult.AgentID != "daemon-managed-claude" {
		t.Fatalf("launch_agent result invalid: success=%t agent_id=%q", launchResult.Success, launchResult.AgentID)
	}
}

func sendManagedClaudeFollowUpThroughMCP(t *testing.T, client *http.Client, endpoint, token, session string) contract.AgentReportResult {
	t.Helper()
	raw := callServeToolStructured(t, client, endpoint, token, session, 4, "send_message", map[string]any{
		"agent_id": "daemon-managed-claude", "message": "Reply briefly with the exact marker DAEMON_CLAUDE_SECOND_OK and no tools.",
		"wait_report": true, "timeout_ms": 60000,
	})
	var result struct {
		Success           bool                       `json:"success"`
		AgentID           string                     `json:"agent_id"`
		PreviousReportSeq int64                      `json:"previous_report_seq"`
		Report            contract.AgentReportResult `json:"report"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatalf("decode send_message result: %v; payload=%s", err, serveE2EPayloadSummary(raw))
	}
	if !result.Success || result.AgentID != "daemon-managed-claude" || result.PreviousReportSeq <= 0 || result.Report.AgentID != result.AgentID {
		t.Fatalf("send_message result invalid: success=%t agent_id=%q previous_report_seq=%d report_agent_id=%q report_seq=%d report_state=%q report_present=%t", result.Success, result.AgentID, result.PreviousReportSeq, result.Report.AgentID, result.Report.ReportSeq, result.Report.State, strings.TrimSpace(result.Report.Report) != "")
	}
	return result.Report
}

func waitManagedClaudeReportThroughMCP(t *testing.T, ctx context.Context, client *http.Client, endpoint, token, session, agentID string, afterSeq int64, marker string) int64 {
	t.Helper()
	deadline := time.NewTimer(90 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	requestID := 3
	lastReportSummary := "<no report received>"
	for {
		reportRaw := callServeToolStructured(t, client, endpoint, token, session, requestID,
			"get_agent_report", map[string]any{"agent_id": agentID})
		requestID++
		lastReportSummary = serveE2EPayloadSummary(reportRaw)
		var report struct {
			AgentID   string `json:"agent_id"`
			Report    string `json:"report"`
			ReportSeq int64  `json:"report_seq"`
			State     string `json:"state"`
		}
		if err := json.Unmarshal(reportRaw, &report); err != nil {
			t.Fatalf("decode get_agent_report result: %v; payload=%s", err, lastReportSummary)
		}
		if reportSeq, ok := standaloneManagedClaudeReportReady(t, report, agentID, afterSeq, marker); ok {
			return reportSeq
		}
		select {
		case <-ctx.Done():
			t.Fatalf("standalone daemon Claude turn context expired: %v", ctx.Err())
		case <-deadline.C:
			t.Fatalf("standalone daemon Claude report timeout: last=%s", lastReportSummary)
		case <-ticker.C:
		}
	}
}

func standaloneManagedClaudeReportReady(t *testing.T, report struct {
	AgentID   string `json:"agent_id"`
	Report    string `json:"report"`
	ReportSeq int64  `json:"report_seq"`
	State     string `json:"state"`
}, agentID string, afterSeq int64, marker string) (int64, bool) {
	t.Helper()
	if report.AgentID == agentID && (report.State == "failed" || report.State == "stopped") {
		t.Fatalf("standalone daemon Claude report reached terminal state=%q report_seq=%d report_present=%t", report.State, report.ReportSeq, strings.TrimSpace(report.Report) != "")
	}
	if report.AgentID == agentID && report.ReportSeq > afterSeq && report.State == "idle" && strings.Contains(report.Report, marker) {
		return report.ReportSeq, true
	}
	return 0, false
}

func callServeToolStructured(t *testing.T, client *http.Client, endpoint, token, session string, id int, name string, arguments map[string]any) json.RawMessage {
	t.Helper()
	reply := callServeE2E(t, client, endpoint, token, session, id, "tools/call", map[string]any{"name": name, "arguments": arguments})
	var envelope struct {
		Content           []struct{ Type, Text string } `json:"content"`
		StructuredContent json.RawMessage               `json:"structuredContent"`
		IsError           bool                          `json:"isError"`
	}
	if err := json.Unmarshal(reply.Result, &envelope); err != nil {
		t.Fatalf("decode %s tool envelope: %v; payload=%s", name, err, serveE2EPayloadSummary(reply.Result))
	}
	if envelope.IsError {
		t.Fatalf("%s returned MCP isError: payload=%s", name, serveE2EPayloadSummary(reply.Result))
	}
	if len(envelope.StructuredContent) != 0 && string(envelope.StructuredContent) != "null" {
		return envelope.StructuredContent
	}
	if len(envelope.Content) == 0 || envelope.Content[0].Type != "text" {
		t.Fatalf("%s returned no structured content: payload=%s", name, serveE2EPayloadSummary(reply.Result))
	}
	return json.RawMessage(envelope.Content[0].Text)
}

func writeManagedServeE2EFixture(t *testing.T, name, executable, home, model string) serveE2EFixture {
	t.Helper()
	workspace, state, credDir, configDir := t.TempDir(), t.TempDir(), t.TempDir(), t.TempDir()
	for _, dir := range []string{workspace, state, credDir, configDir} {
		if err := securefs.RestrictPrivateOwnerOnly(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	credential := filepath.Join(credDir, "token")
	writeServeE2EFile(t, credential, []byte(serveE2EToken+"\n"), 0o600)
	provider := map[string]any{"name": name, "executable": executable, "home": home, "models": []string{model}}
	if name == "codex" {
		provider["sandbox"] = "workspace-write"
	} else if name == "claude" {
		provider["auth_mode"] = standalone.ClaudeAuthHostDefault
	}
	config := map[string]any{
		"schema_version":  1,
		"workspace_root":  workspace,
		"state_dir":       state,
		"listen":          "127.0.0.1:0",
		"credential_file": credential,
		"principal_ref":   "operator/e2e",
		"scope_ref":       "workspace/e2e",
		"providers":       []any{provider},
	}
	raw, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(configDir, "serve.json")
	writeServeE2EFile(t, configPath, raw, 0o600)
	return serveE2EFixture{config: configPath, workspace: workspace, state: state, token: serveE2EToken}
}
