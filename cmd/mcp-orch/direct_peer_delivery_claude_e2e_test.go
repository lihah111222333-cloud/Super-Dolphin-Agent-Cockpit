//go:build e2e

package main

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/standalone"
	directpeer "github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/directpeer"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
)

// TestStandaloneDirectPeerClaudeIdempotency 验证真实Claude原生UUID确认、相同消息重放与冲突不新增输入。
func TestStandaloneDirectPeerClaudeIdempotency(t *testing.T) {
	s := newClaudePeerScenario(t)
	baseline, err := s.app.reports.GetReport(s.ctx, "receiver")
	if err != nil {
		t.Fatal(err)
	}
	marker := "CLAUDE_PEER_ONCE_" + rand.Text()
	body := fmt.Sprintf("Reply with the exact marker %s. Do not use tools or delegate.", marker)
	first, err := sendPeerNativeScenario(t, s, "once", body)
	if err != nil {
		t.Fatal(err)
	}
	receipt := assertClaudePeerReceipt(t, s, first)
	waitDirectPeerAgentIdle(t, s.ctx, s.app.agents, "receiver")
	assertDirectPeerDeliveryReport(t, s.ctx, s.app, "receiver", baseline.ReportSeq, marker)
	report, err := s.app.reports.GetReport(s.ctx, "receiver")
	if err != nil {
		t.Fatal(err)
	}
	conflict := assertClaudePeerReplay(t, s, first, receipt, body)
	after, err := s.app.reports.GetReport(s.ctx, "receiver")
	if err != nil {
		t.Fatal(err)
	}
	if after.ReportSeq != report.ReportSeq || after.Report != report.Report {
		t.Fatal("Claude replay changed report")
	}
	assertClaudePeerInputCount(t, s, marker, receipt.ReceiptID, 1)
	assertClaudePeerInputCount(t, s, conflict, "", 0)
	assertPeerMessageRows(t, s, "once", "submitted", first.SubmissionID, 1)
}

// TestStandaloneDirectPeerClaudeBusyDelivery 以原生确认和活动turn证明真实模型忙碌，不依赖未授权工具。
func TestStandaloneDirectPeerClaudeBusyDelivery(t *testing.T) {
	s := newClaudePeerScenario(t)
	baseline, err := s.app.reports.GetReport(s.ctx, "receiver")
	if err != nil {
		t.Fatal(err)
	}
	activeMarker := "CLAUDE_BUSY_ACTIVE_" + rand.Text()
	activeBody := fmt.Sprintf("Reply with the exact marker %s. Do not use tools or delegate.", activeMarker)
	active, err := sendPeerNativeScenario(t, s, "active", activeBody)
	if err != nil {
		t.Fatal(err)
	}
	activeReceipt := assertClaudePeerReceipt(t, s, active)
	assertClaudePeerActiveReceipt(t, s, activeReceipt)
	unknownMarker := "CLAUDE_BUSY_UNKNOWN_" + rand.Text()
	unknownBody := fmt.Sprintf("Reply with the exact marker %s. Do not use tools or delegate.", unknownMarker)
	assertPeerDeliveryUnknown(t, s, "unknown", unknownBody)
	assertPeerMessageRows(t, s, "unknown", "accepted", "", 0)
	waitDirectPeerAgentIdle(t, s.ctx, s.app.agents, "receiver")
	assertDirectPeerDeliveryReport(t, s.ctx, s.app, "receiver", baseline.ReportSeq, activeMarker)
	assertPeerDeliveryUnknown(t, s, "unknown", unknownBody)
	assertClaudePeerInputCount(t, s, unknownMarker, "", 0)
	assertClaudePeerIndependentTask(t, s, unknownMarker)
	assertClaudePeerInputCount(t, s, activeMarker, activeReceipt.ReceiptID, 1)
	assertPeerMessageRows(t, s, "unknown", "accepted", "", 0)
}

// newClaudePeerScenario 启动host_default认证的真实Claude，所有历史读取限定本次thread UUID。
func newClaudePeerScenario(t *testing.T) peerNativeScenario {
	t.Helper()
	cli, home, model := requireManagedProviderInputs(t, "CLAUDE")
	workspace, state := t.TempDir(), t.TempDir()
	cfg := standalone.Config{SchemaVersion: 1, PrincipalRef: "operator/test", ScopeRef: "workspace/test", WorkspaceRoot: workspace, StateDir: state, Listen: "127.0.0.1:0", CredentialFile: filepath.Join(state, "unused-token"), Providers: []standalone.Provider{{Name: "claude", Executable: cli, Home: home, Models: []string{model}, AuthMode: standalone.ClaudeAuthHostDefault}}}
	db := managedProviderDatabase(t, state)
	app := startDirectPeerDeliveryApp(t, cfg, db)
	t.Cleanup(func() { stopStandaloneTestApp(t, app.app) })
	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	t.Cleanup(cancel)
	persistDirectPeerSender(t, db, workspace, model)
	launchDirectPeerReceiver(t, app.registry, workspace, "claude", model)
	grantDirectPeerThroughRegistry(t, directPeerTestApp{registry: app.registry}, workspace)
	waitDirectPeerAgentIdle(t, ctx, app.agents, "receiver")
	snapshot, err := app.agents.Snapshot(ctx, "receiver")
	if err != nil {
		t.Fatal(err)
	}
	return peerNativeScenario{app: app, db: db, ctx: ctx, workspace: workspace, home: home, thread: snapshot.ThreadID}
}

// assertClaudePeerActiveReceipt 将busy前置绑定到原生确认的同一线程和turn，拒绝用时间猜测忙碌。
func assertClaudePeerActiveReceipt(t *testing.T, s peerNativeScenario, receipt contract.ProviderSubmissionReceipt) {
	t.Helper()
	snapshot, err := s.app.agents.Snapshot(s.ctx, "receiver")
	if err != nil || snapshot.State != "turn_running" || snapshot.ThreadID != receipt.ThreadID || snapshot.ActiveTurnID != receipt.TurnID {
		t.Fatalf("Claude busy precondition not bound to native receipt: state=%s turn=%s err=%v", snapshot.State, snapshot.ActiveTurnID, err)
	}
}

// assertClaudePeerReceipt 要求持久原生确认属于当前Claude会话和UUID，不把messageID当确认。
func assertClaudePeerReceipt(t *testing.T, s peerNativeScenario, message directpeer.Message) contract.ProviderSubmissionReceipt {
	t.Helper()
	if message.Status != "submitted" || message.MessageID == "" || message.SubmissionID == "" {
		t.Fatal("Claude submitted identity missing")
	}
	store, err := directpeer.NewStore(s.db)
	if err != nil {
		t.Fatal(err)
	}
	receipt, err := store.ReadNativeReceipt(s.ctx, message)
	if err != nil {
		t.Fatal(err)
	}
	if receipt.Provider != "claude" || receipt.AgentID != "receiver" || receipt.ThreadID != s.thread || receipt.TurnID != message.SubmissionID {
		t.Fatalf("Claude receipt mismatch: %+v", receipt)
	}
	assertPeerSubmittedEventOnce(t, s, message.MessageID)
	return receipt
}

// assertClaudePeerReplay 验证相同key重放原确认，不同body明确冲突。
func assertClaudePeerReplay(t *testing.T, s peerNativeScenario, first directpeer.Message, receipt contract.ProviderSubmissionReceipt, body string) string {
	t.Helper()
	repeated, err := sendPeerNativeScenario(t, s, "once", body)
	if err != nil {
		t.Fatal(err)
	}
	if first.MessageID != repeated.MessageID || first.SubmissionID != repeated.SubmissionID {
		t.Fatal("Claude replay changed identity")
	}
	if replayReceipt := assertClaudePeerReceipt(t, s, repeated); !reflect.DeepEqual(receipt, replayReceipt) {
		t.Fatal("Claude replay changed native receipt")
	}
	conflict := "CLAUDE_PEER_CONFLICT_" + rand.Text()
	if _, err := sendPeerNativeScenario(t, s, "once", body+fmt.Sprintf(" Conflicting marker %s.", conflict)); !errors.Is(err, directpeer.ErrIdempotencyConflict) {
		t.Fatalf("Claude changed body error=%v", err)
	}
	return conflict
}

// assertClaudePeerIndependentTask 恢复后只投递独立新任务，原unknown消息输入计数保持零。
func assertClaudePeerIndependentTask(t *testing.T, s peerNativeScenario, unknownMarker string) {
	t.Helper()
	baseline, err := s.app.reports.GetReport(s.ctx, "receiver")
	if err != nil {
		t.Fatal(err)
	}
	marker := "CLAUDE_INDEPENDENT_" + rand.Text()
	body := fmt.Sprintf("Independent new task: reply with the exact marker %s. Do not use tools or delegate.", marker)
	message, err := sendPeerNativeScenario(t, s, "independent", body)
	if err != nil {
		t.Fatal(err)
	}
	receipt := assertClaudePeerReceipt(t, s, message)
	waitDirectPeerAgentIdle(t, s.ctx, s.app.agents, "receiver")
	assertDirectPeerDeliveryReport(t, s.ctx, s.app, "receiver", baseline.ReportSeq, marker)
	assertClaudePeerInputCount(t, s, marker, receipt.ReceiptID, 1)
	assertClaudePeerInputCount(t, s, unknownMarker, "", 0)
}

// assertClaudePeerInputCount 仅打开精确session UUID文件，排除assistant、tool_result和其他会话内容。
func assertClaudePeerInputCount(t *testing.T, s peerNativeScenario, marker, receiptID string, want int) {
	t.Helper()
	path := claudePeerSessionPath(t, s.thread)
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := file.Close(); err != nil {
			t.Error(err)
		}
	}()
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64*1024), 16*1024*1024)
	count := 0
	for scanner.Scan() {
		if claudePeerUserInputMatches(t, scanner.Bytes(), s.thread, marker, receiptID) {
			count++
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	if count != want {
		t.Fatalf("owned Claude input marker count=%d want=%d thread=%s", count, want, s.thread)
	}
}

// claudePeerUserInputMatches 只统计原生user文本项，工具回显即使包含marker也不能算输入。
func claudePeerUserInputMatches(t *testing.T, raw []byte, thread, marker, receiptID string) bool {
	t.Helper()
	var event struct {
		Type, SessionID, UUID string
		Message               struct {
			Role    string
			Content json.RawMessage
		}
	}
	if err := json.Unmarshal(raw, &event); err != nil {
		t.Fatal(err)
	}
	if event.Type != "user" || event.Message.Role != "user" {
		return false
	}
	if event.SessionID != thread {
		t.Fatal("owned Claude user input has foreign session identity")
	}
	if !claudePeerContentContains(t, event.Message.Content, "marker "+marker+".") {
		return false
	}
	if event.UUID == "" {
		t.Fatal("Claude native user input UUID missing")
	}
	if receiptID != "" && event.UUID != receiptID {
		t.Fatal("Claude user UUID differs from persisted native receipt")
	}
	return true
}

// claudePeerContentContains 支持原生user字符串与text block，明确不读取tool_result正文。
func claudePeerContentContains(t *testing.T, raw json.RawMessage, marker string) bool {
	t.Helper()
	if len(raw) == 0 {
		t.Fatal("Claude user input content missing")
	}
	if raw[0] == '"' {
		var text string
		if err := json.Unmarshal(raw, &text); err != nil {
			t.Fatal(err)
		}
		return strings.Contains(text, marker)
	}
	var blocks []struct{ Type, Text string }
	if err := json.Unmarshal(raw, &blocks); err != nil {
		t.Fatal(err)
	}
	for _, block := range blocks {
		if block.Type == "text" && strings.Contains(block.Text, marker) {
			return true
		}
	}
	return false
}

// claudePeerSessionPath 只定位本次会话UUID，不能枚举或读取其他会话正文。
func claudePeerSessionPath(t *testing.T, thread string) string {
	t.Helper()
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	if len(thread) != 36 || strings.ContainsAny(thread, "/\\*?[]") {
		t.Fatal("Claude thread is not a safe UUID filename")
	}
	paths, err := filepath.Glob(filepath.Join(home, ".claude", "projects", "*", thread+".jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if len(paths) != 1 {
		t.Fatalf("expected one owned Claude session file for %s, got %d", thread, len(paths))
	}
	return paths[0]
}

// TestClaudePeerNativeInputParser 排除assistant和工具回显，只认可本会话user文本及精确marker。
func TestClaudePeerNativeInputParser(t *testing.T) {
	cases := []struct {
		name, raw string
		want      bool
	}{
		{"native-text-block", `{"type":"user","sessionId":"thread","uuid":"receipt","message":{"role":"user","content":[{"type":"text","text":"Reply with the exact marker TOKEN."}]}}`, true},
		{"native-string", `{"type":"user","sessionId":"thread","uuid":"receipt","message":{"role":"user","content":"Reply with the exact marker TOKEN."}}`, true},
		{"assistant-echo", `{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"marker TOKEN."}]}}`, false},
		{"tool-result-echo", `{"type":"user","sessionId":"thread","uuid":"tool","message":{"role":"user","content":[{"type":"tool_result","content":"marker TOKEN."}]}}`, false},
		{"prefix-collision", `{"type":"user","sessionId":"thread","uuid":"receipt","message":{"role":"user","content":"marker TOKEN_OTHER."}}`, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := claudePeerUserInputMatches(t, []byte(tc.raw), "thread", "TOKEN", "receipt"); got != tc.want {
				t.Fatalf("match=%v want=%v", got, tc.want)
			}
		})
	}
}
