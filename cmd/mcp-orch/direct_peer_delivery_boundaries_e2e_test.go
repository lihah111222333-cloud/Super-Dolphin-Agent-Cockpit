//go:build e2e

package main

import (
	"bufio"
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/standalone"
	directpeer "github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/directpeer"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
	mcpcommon "github.com/lihah111222333-cloud/super-dolphin-agent/internal/mcpserver/common"
)

type directPeerBoundaryFixture struct {
	app                     directPeerDeliveryApp
	db                      *sql.DB
	ctx                     context.Context
	workspace, home, thread string
	rollout                 *os.File
}

// TestStandaloneDirectPeerDeliveryBoundaries 串行验证真实接收者的权限撤销、重授和停止边界。
func TestStandaloneDirectPeerDeliveryBoundaries(t *testing.T) {
	if !t.Run("native-marker-evidence", assertDirectPeerBoundaryMarkerEvidence) {
		t.FailNow()
	}

	cli, home, model := requireManagedProviderInputs(t, "CODEX")
	workspace, state := t.TempDir(), t.TempDir()
	cfg := standalone.Config{SchemaVersion: 1, PrincipalRef: "operator/test", ScopeRef: "workspace/test", WorkspaceRoot: workspace, StateDir: state,
		Listen: "127.0.0.1:0", CredentialFile: filepath.Join(state, "unused-token"),
		Providers: []standalone.Provider{{Name: "codex", Executable: cli, Home: home, Models: []string{model}, Sandbox: "workspace-write"}}}
	db := managedProviderDatabase(t, state)
	app := startDirectPeerDeliveryApp(t, cfg, db)
	defer stopStandaloneTestApp(t, app.app)
	ctx, cancel := context.WithTimeout(context.Background(), 240*time.Second)
	defer cancel()
	persistDirectPeerSender(t, db, workspace, model)
	launchDirectPeerReceiver(t, app.registry, workspace, "codex", model)
	warmDirectPeerBoundaryReceiver(t, app, workspace)
	waitDirectPeerAgentIdle(t, ctx, app.agents, "receiver")
	snapshot, err := app.agents.Snapshot(ctx, "receiver")
	if err != nil || snapshot.ThreadID == "" {
		t.Fatalf("receiver identity missing: %v", err)
	}
	f := directPeerBoundaryFixture{app: app, db: db, ctx: ctx, workspace: workspace, home: home, thread: snapshot.ThreadID}
	if !t.Run("unauthorized", func(t *testing.T) { assertDirectPeerPermissionRejected(t, f, "unauthorized") }) {
		t.FailNow()
	}
	grantDirectPeerThroughRegistry(t, directPeerTestApp{registry: app.registry}, workspace)
	revokeDirectPeerThroughRegistry(t, directPeerTestApp{registry: app.registry}, workspace)
	if !t.Run("revoked", func(t *testing.T) { assertDirectPeerPermissionRejected(t, f, "revoked") }) {
		t.FailNow()
	}
	if !t.Run("reauthorized", func(t *testing.T) { assertDirectPeerReauthorized(t, f) }) {
		t.FailNow()
	}
	t.Run("stopped-receiver", func(t *testing.T) { assertDirectPeerStoppedReceiver(t, f) })
}

// warmDirectPeerBoundaryReceiver 只生成一次初始报告与原生rollout，后续拒绝不能产生任何输入。
func warmDirectPeerBoundaryReceiver(t *testing.T, app directPeerDeliveryApp, workspace string) {
	t.Helper()
	callDirectPeerRegistry(t, app.registry, "send_message", mcpcommon.ToolScope{CWD: workspace},
		`{"agent_id":"receiver","message":"Reply READY only. Do not use tools or delegate.","wait_report":true,"timeout_ms":120000}`)
}

type directPeerBoundaryState struct {
	report           contract.AgentReportResult
	messages, events int
}

// readDirectPeerBoundaryState 捕获报告和交付持久记录，授权事件之外不允许拒绝路径新增交付证据。
func readDirectPeerBoundaryState(t *testing.T, f directPeerBoundaryFixture) directPeerBoundaryState {
	t.Helper()
	report, err := f.app.reports.GetReport(f.ctx, "receiver")
	if err != nil {
		t.Fatal(err)
	}
	var state directPeerBoundaryState
	state.report = report
	if err := f.db.QueryRowContext(f.ctx, "SELECT COUNT(*) FROM orchestration_direct_peer_messages").Scan(&state.messages); err != nil {
		t.Fatal(err)
	}
	if err := f.db.QueryRowContext(f.ctx, "SELECT COUNT(*) FROM orchestration_direct_peer_events WHERE message_id <> ''").Scan(&state.events); err != nil {
		t.Fatal(err)
	}
	return state
}

// invokeDirectPeerBoundary 直接调用真实registry的错误分支，保留error而不使用成功专用helper。
func invokeDirectPeerBoundary(t *testing.T, f directPeerBoundaryFixture, key, marker string) (any, error) {
	t.Helper()
	def, ok := f.app.registry.Lookup("send_direct_peer")
	if !ok {
		t.Fatal("send_direct_peer missing")
	}
	raw := mustJSON(t, map[string]any{"receiver_agent_id": "receiver", "idempotency_key": key, "message": directPeerBoundaryMessage(marker)})
	return def.Handler(mcpcommon.WithToolScope(f.ctx, mcpcommon.ToolScope{CWD: f.workspace, AgentID: "sender"}), raw)
}

// assertDirectPeerPermissionRejected 必须是权限拒绝，不能把其他错误视为成功的边界防御。
func assertDirectPeerPermissionRejected(t *testing.T, f directPeerBoundaryFixture, key string) {
	t.Helper()
	before := readDirectPeerBoundaryState(t, f)
	marker := "DENIED_" + rand.Text()
	_, err := invokeDirectPeerBoundary(t, f, key, marker)
	if !errors.Is(err, directpeer.ErrUnauthorized) {
		t.Fatalf("want unauthorized, got %v", err)
	}
	assertDirectPeerBoundaryUnchanged(t, f, before, marker)
}

// assertDirectPeerBoundaryUnchanged 同时验证无消息/回执事件、无报告推进和无真实provider输入。
func assertDirectPeerBoundaryUnchanged(t *testing.T, f directPeerBoundaryFixture, before directPeerBoundaryState, marker string) {
	t.Helper()
	after := readDirectPeerBoundaryState(t, f)
	if before.messages != after.messages || before.events != after.events {
		t.Fatalf("rejected delivery mutated durable records: before=%+v after=%+v", before, after)
	}
	if before.report.ReportSeq != after.report.ReportSeq || before.report.Report != after.report.Report {
		t.Fatal("rejected delivery advanced receiver report")
	}
	assertDirectPeerRejectedNativeInput(t, f, marker)
}

// assertDirectPeerReauthorized 要求撤销后同方向新授权真正恢复，再以全新消息key取得原生确认和新报告。
func assertDirectPeerReauthorized(t *testing.T, f directPeerBoundaryFixture) {
	t.Helper()
	grantDirectPeerDirectionThroughRegistry(t, directPeerTestApp{registry: f.app.registry}, f.workspace, "sender", "receiver", "grant-restored")
	waitDirectPeerAgentIdle(t, f.ctx, f.app.agents, "receiver")
	baseline := readDirectPeerBoundaryState(t, f)
	marker := "RESTORED_" + rand.Text()
	result, err := invokeDirectPeerBoundary(t, f, "restored-new-key", marker)
	if err != nil {
		t.Fatalf("restored grant did not allow delivery: %v", err)
	}
	raw := mustJSON(t, result)
	var message directpeer.Message
	if err := json.Unmarshal(raw, &message); err != nil {
		t.Fatal(err)
	}
	if message.Status != "submitted" || message.SubmissionID == "" {
		t.Fatalf("restored delivery lacks acknowledgement: %+v", message)
	}
	assertDirectPeerRestoredReceipt(t, f, message)
	waitDirectPeerAgentIdle(t, f.ctx, f.app.agents, "receiver")
	assertDirectPeerDeliveryReport(t, f.ctx, f.app, "receiver", baseline.report.ReportSeq, marker)
	assertBinaryCodexInputCount(t, f.home, f.thread, marker, 1)
}

// assertDirectPeerStoppedReceiver 保留未知intent且禁止伪报submitted，重试原key也不得产生provider输入。
func assertDirectPeerStoppedReceiver(t *testing.T, f directPeerBoundaryFixture) {
	t.Helper()
	f.rollout = openBinaryCodexOwnedRollout(t, f.home, f.thread)
	defer func() {
		if err := f.rollout.Close(); err != nil {
			t.Error(err)
		}
	}()

	callDirectPeerRegistry(t, f.app.registry, "stop_agent", mcpcommon.ToolScope{CWD: f.workspace}, `{"agent_id":"receiver","wait":true,"timeout_ms":30000}`)
	before := readDirectPeerBoundaryState(t, f)
	marker := "STOPPED_" + rand.Text()
	_, err := invokeDirectPeerBoundary(t, f, "stopped-key", marker)
	requireDirectPeerBoundaryUnknown(t, err)
	assertDirectPeerStoppedIntent(t, f)
	after := readDirectPeerBoundaryState(t, f)
	if after.messages != before.messages+1 || after.events != before.events+1 {
		t.Fatal("stopped receiver must persist only accepted intent")
	}
	if after.report.ReportSeq != before.report.ReportSeq || after.report.Report != before.report.Report {
		t.Fatal("stopped delivery changed receiver report")
	}
	_, err = invokeDirectPeerBoundary(t, f, "stopped-key", marker)
	requireDirectPeerBoundaryUnknown(t, err)
	assertDirectPeerBoundaryUnchanged(t, f, after, marker)
}

// assertDirectPeerStoppedIntent 要求停止后的持久消息只有accepted意图，没有伪造确认。
func assertDirectPeerStoppedIntent(t *testing.T, f directPeerBoundaryFixture) {
	t.Helper()
	var status, submissionID string
	if err := f.db.QueryRowContext(f.ctx, "SELECT status, submission_id FROM orchestration_direct_peer_messages WHERE idempotency_key='stopped-key'").Scan(&status, &submissionID); err != nil {
		t.Fatal(err)
	}
	if status != "accepted" || submissionID != "" {
		t.Fatalf("stopped receiver falsely acknowledged: status=%s submission=%s", status, submissionID)
	}
}

// assertDirectPeerRejectedNativeInput 在归档前持有精确rollout句柄，归档移动路径不能掩盖新增输入。
func assertDirectPeerRejectedNativeInput(t *testing.T, f directPeerBoundaryFixture, marker string) {
	t.Helper()
	if f.rollout == nil {
		assertBinaryCodexInputCount(t, f.home, f.thread, marker, 0)
		return
	}
	if _, err := f.rollout.Seek(0, io.SeekStart); err != nil {
		t.Fatal(err)
	}
	scanner := bufio.NewScanner(f.rollout)
	scanner.Buffer(make([]byte, 64*1024), 16*1024*1024)
	for scanner.Scan() {
		if binaryCodexInputHasMarker(t, scanner.Bytes(), marker) {
			t.Fatal("stopped receiver received forbidden native input")
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
}

// requireDirectPeerBoundaryUnknown 不接受可重试错误或其他原因冒充生命周期未知结果。
func requireDirectPeerBoundaryUnknown(t *testing.T, err error) {
	t.Helper()
	var coded *mcpcommon.CodedToolError
	if !errors.As(err, &coded) || coded.Code != "delivery_outcome_unknown" || coded.Retryable {
		t.Fatalf("want nonretryable delivery_outcome_unknown: %v", err)
	}
}

// assertDirectPeerRestoredReceipt 通过生产owner验证回执闭包及接收者原生身份。
func assertDirectPeerRestoredReceipt(t *testing.T, f directPeerBoundaryFixture, message directpeer.Message) {
	t.Helper()
	store, err := directpeer.NewStore(f.db)
	if err != nil {
		t.Fatal(err)
	}
	receipt, err := store.ReadNativeReceipt(f.ctx, message)
	if err != nil {
		t.Fatal(err)
	}
	if err := receipt.Validate(); err != nil {
		t.Fatal(err)
	}
	if receipt.AgentID != "receiver" || receipt.ThreadID != f.thread || receipt.TurnID != message.SubmissionID {
		t.Fatalf("wrong native receipt identity: %+v", receipt)
	}
}

// directPeerBoundaryMessage 保留原生输入证据解析器要求的明确marker边界。
func directPeerBoundaryMessage(marker string) string {
	return fmt.Sprintf("Reply with the exact marker %s. Do not use tools or delegate.", marker)
}

// assertDirectPeerBoundaryMarkerEvidence 用真实消息构造器生成原生帧，防止零次输入断言因格式不匹配假绿。
func assertDirectPeerBoundaryMarkerEvidence(t *testing.T) {
	const marker = "BOUNDARY_MARKER_EVIDENCE"
	frame := mustJSON(t, map[string]any{
		"type": "response_item",
		"payload": map[string]any{
			"type": "message", "role": "user",
			"content": []map[string]any{{"type": "input_text", "text": directPeerBoundaryMessage(marker)}},
		},
	})
	if !binaryCodexInputHasMarker(t, frame, marker) {
		t.Fatal("boundary message does not match the native user-input marker contract")
	}
	if binaryCodexInputHasMarker(t, frame, "OTHER_BOUNDARY_MARKER") {
		t.Fatal("native marker evidence matched an unrelated marker")
	}
}
