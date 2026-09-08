//go:build e2e

package main

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/standalone"
	directpeer "github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/directpeer"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
	mcpcommon "github.com/lihah111222333-cloud/super-dolphin-agent/internal/mcpserver/common"
)

type peerNativeScenario struct {
	app                     directPeerDeliveryApp
	db                      *sql.DB
	ctx                     context.Context
	workspace, home, thread string
}

// TestStandaloneDirectPeerCodexIdempotency 验证真实provider幂等重放与同key异body冲突。
func TestStandaloneDirectPeerCodexIdempotency(t *testing.T) {
	assertPeerNativeIdempotency(t, newPeerNativeScenario(t))
}

// TestStandaloneDirectPeerCodexBusyDelivery 验证busy未知消息不会排队或重投，恢复后只接受独立新任务。
func TestStandaloneDirectPeerCodexBusyDelivery(t *testing.T) {
	assertPeerNativeBusyRecovery(t, newPeerNativeScenario(t))
}

// newPeerNativeScenario 创建真实Codex receiver及持久授权，测试直接经Registry投递。
func newPeerNativeScenario(t *testing.T) peerNativeScenario {
	t.Helper()
	cli, home, model := requireManagedProviderInputs(t, "CODEX")
	workspace, state := t.TempDir(), t.TempDir()
	cfg := standalone.Config{SchemaVersion: 1, PrincipalRef: "operator/test", ScopeRef: "workspace/test", WorkspaceRoot: workspace, StateDir: state, Listen: "127.0.0.1:0", CredentialFile: filepath.Join(state, "unused-token"), Providers: []standalone.Provider{{Name: "codex", Executable: cli, Home: home, Models: []string{model}, Sandbox: "workspace-write"}}}
	db := managedProviderDatabase(t, state)
	app := startDirectPeerDeliveryApp(t, cfg, db)
	t.Cleanup(func() { stopStandaloneTestApp(t, app.app) })
	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	t.Cleanup(cancel)
	persistDirectPeerSender(t, db, workspace, model)
	launchDirectPeerReceiver(t, app.registry, workspace, "codex", model)
	grantDirectPeerThroughRegistry(t, directPeerTestApp{registry: app.registry}, workspace)
	waitDirectPeerAgentIdle(t, ctx, app.agents, "receiver")
	snapshot, err := app.agents.Snapshot(ctx, "receiver")
	if err != nil {
		t.Fatal(err)
	}
	return peerNativeScenario{app: app, db: db, ctx: ctx, workspace: workspace, home: home, thread: snapshot.ThreadID}
}

// sendPeerNativeScenario 保留真实错误返回供拒绝断言，不用成功helper吞掉失败分类。
func sendPeerNativeScenario(t *testing.T, s peerNativeScenario, key, body string) (directpeer.Message, error) {
	t.Helper()
	definition, ok := s.app.registry.Lookup("send_direct_peer")
	if !ok {
		t.Fatal("send_direct_peer missing")
	}
	input := mustJSON(t, map[string]any{"receiver_agent_id": "receiver", "idempotency_key": key, "message": body})
	value, err := definition.Handler(mcpcommon.WithToolScope(s.ctx, mcpcommon.ToolScope{CWD: s.workspace, AgentID: "sender"}), input)
	if err != nil {
		return directpeer.Message{}, err
	}
	var message directpeer.Message
	if err := json.Unmarshal(mustJSON(t, value), &message); err != nil {
		t.Fatal(err)
	}
	return message, nil
}

// assertPeerNativeIdempotency 在真实成功报告后重放同key，并回验唯一持久receipt和原生输入。
func assertPeerNativeIdempotency(t *testing.T, s peerNativeScenario) {
	t.Helper()
	baseline, err := s.app.reports.GetReport(s.ctx, "receiver")
	if err != nil {
		t.Fatal(err)
	}
	marker := "PEER_IDEMPOTENT_" + rand.Text()
	key := "same-key-" + rand.Text()
	body := fmt.Sprintf("Reply with the exact marker %s. Do not use tools or delegate.", marker)
	first, err := sendPeerNativeScenario(t, s, key, body)
	if err != nil {
		t.Fatal(err)
	}
	receipt := assertPeerNativeReceipt(t, s, first)
	waitDirectPeerAgentIdle(t, s.ctx, s.app.agents, "receiver")
	assertDirectPeerDeliveryReport(t, s.ctx, s.app, "receiver", baseline.ReportSeq, marker)
	report, err := s.app.reports.GetReport(s.ctx, "receiver")
	if err != nil {
		t.Fatal(err)
	}
	conflictMarker := assertPeerReplayAndConflict(t, s, key, body, first, receipt, report)
	assertBinaryCodexInputCount(t, s.home, s.thread, marker, 1)
	assertBinaryCodexInputCount(t, s.home, s.thread, conflictMarker, 0)
	assertPeerMessageRows(t, s, key, "submitted", first.SubmissionID, 1)
}

// assertPeerNativeReceipt 通过生产store解码完整原生确认，再用SQLite证明唯一submitted事件。
func assertPeerNativeReceipt(t *testing.T, s peerNativeScenario, message directpeer.Message) contract.ProviderSubmissionReceipt {
	t.Helper()
	if message.Status != "submitted" || message.MessageID == "" || message.SubmissionID == "" {
		t.Fatalf("missing native submitted receipt: %+v", message)
	}
	store, err := directpeer.NewStore(s.db)
	if err != nil {
		t.Fatal(err)
	}
	receipt, err := store.ReadNativeReceipt(s.ctx, message)
	if err != nil {
		t.Fatal(err)
	}
	if receipt.AgentID != "receiver" || receipt.ThreadID != s.thread || receipt.TurnID != message.SubmissionID || receipt.Provider != "codex" {
		t.Fatalf("receipt identity mismatch: %+v", receipt)
	}
	assertPeerSubmittedEventOnce(t, s, message.MessageID)
	return receipt
}

// assertPeerNativeBusyRecovery 验证busy不排队，accepted未知记录重放不重投，恢复后只投递独立新任务，原unknown消息始终不重发。
func assertPeerNativeBusyRecovery(t *testing.T, s peerNativeScenario) {
	t.Helper()
	ready := "PEER_BUSY_READY_" + rand.Text()
	activeMarker := "PEER_ACTIVE_" + rand.Text()
	activeKey := "active-" + rand.Text()
	activeBody := fmt.Sprintf("Use the shell tool exactly: printf '%%s' '%s' > .peer-busy-ready; sleep 8. Wait for completion then reply with the exact marker %s. Do not delegate.", ready, activeMarker)
	baseline, err := s.app.reports.GetReport(s.ctx, "receiver")
	if err != nil {
		t.Fatal(err)
	}
	active, err := sendPeerNativeScenario(t, s, activeKey, activeBody)
	if err != nil {
		t.Fatal(err)
	}
	assertPeerNativeReceipt(t, s, active)
	waitPeerBusyCommandReady(t, s, ready)
	marker := "PEER_BUSY_MESSAGE_" + rand.Text()
	key := "busy-" + rand.Text()
	body := fmt.Sprintf("Reply with the exact marker %s. Do not use tools or delegate.", marker)
	assertPeerDeliveryUnknown(t, s, key, body)
	assertPeerMessageRows(t, s, key, "accepted", "", 0)
	waitDirectPeerAgentIdle(t, s.ctx, s.app.agents, "receiver")
	assertDirectPeerDeliveryReport(t, s.ctx, s.app, "receiver", baseline.ReportSeq, activeMarker)
	assertPeerDeliveryUnknown(t, s, key, body)
	assertBinaryCodexInputCount(t, s.home, s.thread, marker, 0)
	assertPeerMessageRows(t, s, key, "accepted", "", 0)
	assertPeerIndependentTaskAfterBusy(t, s, key, marker, activeMarker)
}

// assertPeerDeliveryUnknown 要求保守错误码与禁止自动重投语义，不把busy假装成排队成功。
func assertPeerDeliveryUnknown(t *testing.T, s peerNativeScenario, key, body string) {
	t.Helper()
	_, err := sendPeerNativeScenario(t, s, key, body)
	var coded *mcpcommon.CodedToolError
	if !errors.As(err, &coded) || coded.Code != "delivery_outcome_unknown" || coded.Retryable {
		t.Fatalf("busy/replay error=%v want nonretryable delivery_outcome_unknown", err)
	}
}

// waitPeerBusyCommandReady 用真实shell写入随机证据并核对活跃turn，拒绝靠固定延时猜busy。
func waitPeerBusyCommandReady(t *testing.T, s peerNativeScenario, marker string) {
	t.Helper()
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	for {
		raw, err := os.ReadFile(filepath.Join(s.workspace, ".peer-busy-ready"))
		if err == nil && string(raw) == marker {
			assertPeerReceiverBusy(t, s)
			return
		}
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			t.Fatal(err)
		}
		select {
		case <-s.ctx.Done():
			t.Fatal("receiver shell readiness timed out")
		case <-ticker.C:
		}
	}
}

// assertPeerMessageRows 验证同key唯一持久消息和原生确认事件数，accepted不隐式升级为submitted。
func assertPeerMessageRows(t *testing.T, s peerNativeScenario, key, status, submission string, eventCount int) {
	t.Helper()
	var count int
	if err := s.db.QueryRowContext(s.ctx, "SELECT COUNT(*) FROM orchestration_direct_peer_messages WHERE workspace_root=? AND sender_agent_id='sender' AND receiver_agent_id='receiver' AND idempotency_key=?", s.workspace, key).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("same-key message count=%d", count)
	}
	var id, gotStatus, gotSubmission string
	if err := s.db.QueryRowContext(s.ctx, "SELECT message_id,status,submission_id FROM orchestration_direct_peer_messages WHERE workspace_root=? AND sender_agent_id='sender' AND receiver_agent_id='receiver' AND idempotency_key=?", s.workspace, key).Scan(&id, &gotStatus, &gotSubmission); err != nil {
		t.Fatal(err)
	}
	if gotStatus != status || gotSubmission != submission {
		t.Fatalf("durable message=%s/%s want=%s/%s", gotStatus, gotSubmission, status, submission)
	}
	if err := s.db.QueryRowContext(s.ctx, "SELECT COUNT(*) FROM orchestration_direct_peer_events WHERE message_id=? AND event_type='message.submitted'", id).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != eventCount {
		t.Fatalf("submitted events=%d want=%d", count, eventCount)
	}
}

// assertPeerReplayAndConflict 校验重放原身份及冲突拒绝，不引入新的native提交。
func assertPeerReplayAndConflict(t *testing.T, s peerNativeScenario, key, body string, first directpeer.Message, receipt contract.ProviderSubmissionReceipt, report contract.AgentReportResult) string {
	t.Helper()
	replay, err := sendPeerNativeScenario(t, s, key, body)
	if err != nil {
		t.Fatal(err)
	}
	if replay.MessageID != first.MessageID || replay.SubmissionID != first.SubmissionID {
		t.Fatalf("replay minted new delivery: first=%s/%s replay=%s/%s", first.MessageID, first.SubmissionID, replay.MessageID, replay.SubmissionID)
	}
	if repeated := assertPeerNativeReceipt(t, s, replay); !reflect.DeepEqual(receipt, repeated) {
		t.Fatal("replay changed durable native receipt")
	}
	conflictMarker := "PEER_CONFLICT_" + rand.Text()
	if _, err := sendPeerNativeScenario(t, s, key, body+fmt.Sprintf(" Conflicting marker %s.", conflictMarker)); !errors.Is(err, directpeer.ErrIdempotencyConflict) {
		t.Fatalf("same-key changed body error=%v", err)
	}
	after, err := s.app.reports.GetReport(s.ctx, "receiver")
	if err != nil {
		t.Fatal(err)
	}
	if after.ReportSeq != report.ReportSeq || after.Report != report.Report {
		t.Fatal("replay/conflict changed completed report")
	}
	return conflictMarker
}

// assertPeerIndependentTaskAfterBusy 仅发送独立新任务，未知原消息保持accepted且原生输入为零。
func assertPeerIndependentTaskAfterBusy(t *testing.T, s peerNativeScenario, key, marker, activeMarker string) {
	t.Helper()
	before, err := s.app.reports.GetReport(s.ctx, "receiver")
	if err != nil {
		t.Fatal(err)
	}
	newMarker := "PEER_INDEPENDENT_TASK_" + rand.Text()
	newBody := fmt.Sprintf("This is an independent new task: reply with the exact marker %s. Do not use tools or delegate.", newMarker)
	recovered, err := sendPeerNativeScenario(t, s, "independent-task-"+rand.Text(), newBody)
	if err != nil {
		t.Fatal(err)
	}
	assertPeerNativeReceipt(t, s, recovered)
	waitDirectPeerAgentIdle(t, s.ctx, s.app.agents, "receiver")
	assertDirectPeerDeliveryReport(t, s.ctx, s.app, "receiver", before.ReportSeq, newMarker)
	assertBinaryCodexInputCount(t, s.home, s.thread, newMarker, 1)
	assertBinaryCodexInputCount(t, s.home, s.thread, marker, 0)
	assertBinaryCodexInputCount(t, s.home, s.thread, activeMarker, 1)
	assertPeerMessageRows(t, s, key, "accepted", "", 0)
}

// assertPeerReceiverBusy 要求已写入ready的真实命令仍由活跃turn持有。
func assertPeerReceiverBusy(t *testing.T, s peerNativeScenario) {
	t.Helper()
	snapshot, err := s.app.agents.Snapshot(s.ctx, "receiver")
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.State != "turn_running" || snapshot.ActiveTurnID == "" {
		t.Fatalf("ready command is not active: state=%s turn=%s", snapshot.State, snapshot.ActiveTurnID)
	}
}

// assertPeerSubmittedEventOnce 验证原生确认只产生一个持久submitted事件。
func assertPeerSubmittedEventOnce(t *testing.T, s peerNativeScenario, messageID string) {
	t.Helper()
	var count int
	if err := s.db.QueryRowContext(s.ctx, "SELECT COUNT(*) FROM orchestration_direct_peer_events WHERE message_id=? AND event_type='message.submitted'", messageID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("native submitted event count=%d", count)
	}
}
