//go:build e2e

package main

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/terminaloutcome"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/pidregistry"
)

// TestStandaloneBinaryCodexAutoRecoversProviderCrash 验证独立 daemon 内 provider 异常退出自动恢复，不涵盖 daemon 崩溃。
func TestStandaloneBinaryCodexAutoRecoversProviderCrash(t *testing.T) {
	testBinaryCodexInputEvidence(t)
	cli, home, model := requireManagedProviderInputs(t, "CODEX")
	binary := buildServeE2EBinary(t)
	ctx, cancel := context.WithTimeout(context.Background(), 480*time.Second)
	t.Cleanup(cancel)
	fixture := writeManagedCodexClientE2EFixture(t, binary, cli, home, model)
	proof := rand.Text()
	writeServeE2EFile(t, filepath.Join(fixture.workspace, ".orch-cwd-proof"), []byte(proof+"\n"), 0o600)
	realCWD, err := filepath.EvalSymlinks(fixture.workspace)
	if err != nil {
		t.Fatal(err)
	}
	daemon := startServeE2E(t, ctx, binary, fixture)
	client := &http.Client{Timeout: 150 * time.Second}
	session, _ := initializeServeE2E(t, client, daemon.ready.Endpoint, fixture.token)
	h := &binaryCodexLifecycleClient{t: t, ctx: ctx, client: client, daemon: daemon, fixture: fixture, session: session, model: model, proof: proof, initialProof: proof, realCWD: realCWD, requestID: 10}
	const agentID = "binary-crash-parent"
	const firstMarker = "PARENT_FIRST"
	t.Log("evidence_layer=binary+http-managed-real-model+os-process-identity+provider-history-observation+db-observation")
	h.launch(agentID, "", firstMarker)
	first := h.report(agentID, 0, firstMarker)
	before := h.idleSnapshot(agentID)
	db, _, _ := openStandaloneClaudeEvidenceStore(t, ctx, fixture)
	evidence := standaloneClaudePublicLifecycle{t: t, ctx: ctx, db: db, agentID: agentID}
	firstOutcome, err := terminaloutcome.New(db).GetPublicTerminalOutcome(ctx, agentID)
	if err != nil {
		t.Fatal(err)
	}
	if firstOutcome.PublicOutcome.Kind != "success" || firstOutcome.Identity.PublicThreadID != before.ThreadID {
		t.Fatal("first model terminal evidence mismatch")
	}
	oldRow := evidence.historyRow(firstOutcome.Identity.TerminalIdentity)
	completed := evidence.completedTurnCount()
	original := requireBinaryCodexProcess(t, daemon, cli)
	assertBinaryCodexInputCount(t, home, before.ThreadID, firstMarker, 1)
	killBinaryCodexProcess(t, daemon, cli, original)
	recovered := waitBinaryCodexAutoRecovery(t, h, before, original, cli)
	t.Logf("owned provider crash recovered: old_pid=%d new_pid=%d same_thread=%s", original.PID, recovered.PID, before.ThreadID)
	// 恢复是纯控制面操作，不得借显式 recover_agent 或 send_message 触发。
	// process_failed 可以合法发布新报告；窗口从恢复后新 fence 开始，而非冻结首轮 fence。
	var baseline contract.AgentReportResult
	h.call("get_agent_report", map[string]any{"agent_id": agentID}, &baseline)
	if baseline.ReportSeq <= first.ReportSeq {
		t.Fatal("process failure did not publish its own report fence")
	}
	assertBinaryCodexProcessFailure(t, &evidence)
	assertBinaryCodexHistoryPreserved(t, &evidence, firstOutcome.Identity.TerminalIdentity, oldRow, completed)
	assertBinaryCodexRecoveryQuiescent(t, h, before, baseline, home)
	h.rotateProof()
	h.followUp(agentID, baseline.ReportSeq, "PARENT_RECOVERED")
	after := h.idleSnapshot(agentID)
	h.assertRecoveredRuntime(after, before)
	assertBinaryCodexHistoryPreserved(t, &evidence, firstOutcome.Identity.TerminalIdentity, oldRow, completed+1)
	assertBinaryCodexInputCount(t, home, before.ThreadID, firstMarker, 1)
	assertBinaryCodexInputCount(t, home, before.ThreadID, "PARENT_RECOVERED", 1)
	if err := stopServeProcessE2E(t, daemon, nil); err != nil {
		t.Fatalf("daemon shutdown: %v", err)
	}
	assertServeEndpointClosed(t, client, daemon.ready.Endpoint)
	assertNoStandaloneManagedDescendants(t, standaloneManagedProcessSnapshot{original, recovered})
}

// binaryCodexProcess 只接受本测试 daemon 的直接子进程和解析后的精确 executable 身份；不读取进程环境。
func binaryCodexProcess(t *testing.T, daemon *serveE2EProcess, executable string) (pidregistry.StableProcessIdentity, bool) {
	t.Helper()
	resolved, err := filepath.EvalSymlinks(executable)
	if err != nil {
		t.Fatal(err)
	}
	entries, err := readStandaloneProcessTable()
	if err != nil {
		t.Fatal(err)
	}
	root := standaloneDaemonPID(t, daemon)
	assertStandaloneDaemonRoot(t, entries, root)
	var found pidregistry.StableProcessIdentity
	count := 0
	for _, entry := range entries {
		if entry.ppid != root {
			continue
		}
		identity, err := pidregistry.CaptureStableProcessIdentity(entry.pid)
		if errors.Is(err, pidregistry.ErrStableProcessNotFound) {
			continue
		}
		if err != nil {
			t.Fatalf("read direct child identity: %v", err)
		}
		if identity.ExecutableIdentity != resolved {
			continue
		}
		if !validStandaloneProcessIdentity(identity) {
			t.Fatal("invalid provider process identity")
		}
		found = identity
		count++
	}
	if count > 1 {
		t.Fatalf("fixture has %d direct Codex processes; refusing ambiguous crash target", count)
	}
	return found, count == 1
}

// requireBinaryCodexProcess 在注入前要求唯一可核验的进程，缺失身份立即阻断。
func requireBinaryCodexProcess(t *testing.T, daemon *serveE2EProcess, executable string) pidregistry.StableProcessIdentity {
	t.Helper()
	identity, found := binaryCodexProcess(t, daemon, executable)
	if !found {
		t.Fatal("no exact owned Codex process identity; refusing crash injection")
	}
	return identity
}

// waitBinaryCodexAutoRecovery 有界等待新进程与同 thread 的 idle，不调用恢复工具或提交模型输入。
func waitBinaryCodexAutoRecovery(t *testing.T, h *binaryCodexLifecycleClient, before contract.AgentSnapshot, original pidregistry.StableProcessIdentity, executable string) pidregistry.StableProcessIdentity {
	t.Helper()
	ctx, cancel := context.WithTimeout(h.ctx, 45*time.Second)
	defer cancel()
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()
	for {
		identity, found := binaryCodexProcess(t, h.daemon, executable)
		snapshot := h.snapshot(before.AgentID)
		if snapshot.ThreadID != before.ThreadID {
			t.Fatal("automatic recovery changed immutable thread binding")
		}
		if found && identity != original && snapshot.State == "idle" && snapshot.ActiveTurnID == "" {
			assertBinaryCodexOriginalExited(t, original)
			return identity
		}
		select {
		case <-ctx.Done():
			t.Fatalf("provider did not automatically recover within deadline: state=%s new_process=%t: %v", snapshot.State, found && identity != original, ctx.Err())
		case <-ticker.C:
		}
	}
}

// assertBinaryCodexInputCount 仅打开精确测试 thread 的原生 rollout，按 user_message 事件计数；报告 fence 不能代替该证据。
func assertBinaryCodexInputCount(t *testing.T, home, threadID, marker string, want int) {
	t.Helper()
	file := openBinaryCodexOwnedRollout(t, home, threadID)
	defer func() {
		if err := file.Close(); err != nil {
			t.Error(err)
		}
	}()
	assertBinaryCodexRolloutInputCount(t, file, marker, want)
}

// assertBinaryCodexRolloutInputCount 从已绑定的精确历史句柄验证输入次数，归档移动路径不会改变所有权。
func assertBinaryCodexRolloutInputCount(t *testing.T, file *os.File, marker string, want int) {
	t.Helper()
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64*1024), 16*1024*1024)
	count := 0
	for scanner.Scan() {
		if binaryCodexInputHasMarker(t, scanner.Bytes(), marker) {
			count++
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	if count != want {
		t.Fatalf("owned native thread marker %s input count=%d want=%d", marker, count, want)
	}
}

// killBinaryCodexProcess 在信号注入前再次验证测试所有权与启动身份。
func killBinaryCodexProcess(t *testing.T, daemon *serveE2EProcess, cli string, original pidregistry.StableProcessIdentity) {
	t.Helper()
	// 仅此 fixture 启动了一个 agent；杀死前再次核对直接父进程和稳定身份。
	confirmed := requireBinaryCodexProcess(t, daemon, cli)
	if confirmed != original {
		t.Fatal("provider identity changed before crash injection")
	}
	process, err := os.FindProcess(original.PID)
	if err != nil {
		t.Fatal(err)
	}
	if err := process.Kill(); err != nil {
		t.Fatalf("kill owned provider: %v", err)
	}
}

// assertBinaryCodexRecoveryQuiescent 观察恢复后静默窗口，拒绝旧输入重放或无输入报告推进。
func assertBinaryCodexRecoveryQuiescent(t *testing.T, h *binaryCodexLifecycleClient, before contract.AgentSnapshot, baseline contract.AgentReportResult, home string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		current := h.snapshot(before.AgentID)
		if current.State != "idle" || current.ActiveTurnID != "" || current.ThreadID != before.ThreadID {
			t.Fatalf("auto recovery replayed work or changed identity: %+v", current)
		}
		var report contract.AgentReportResult
		h.call("get_agent_report", map[string]any{"agent_id": before.AgentID}, &report)
		if report.ReportSeq != baseline.ReportSeq || report.Report != baseline.Report {
			t.Fatal("auto recovery changed completed report without new input")
		}
		assertBinaryCodexInputCount(t, home, before.ThreadID, "PARENT_FIRST", 1)
		time.Sleep(250 * time.Millisecond)
	}
}

// binaryCodexInputHasMarker 仅识别原生用户输入事件，不把 assistant 引用计作新输入。
func binaryCodexInputHasMarker(t *testing.T, raw []byte, marker string) bool {
	t.Helper()
	var event struct {
		Type    string `json:"type"`
		Payload struct {
			Type    string          `json:"type"`
			Role    string          `json:"role"`
			Content json.RawMessage `json:"content"`
		} `json:"payload"`
	}
	if err := json.Unmarshal(raw, &event); err != nil {
		t.Fatalf("decode owned rollout event: %v", err)
	}
	if event.Type != "response_item" || event.Payload.Type != "message" || event.Payload.Role != "user" {
		return false
	}
	var content []struct{ Type, Text string }
	if err := json.Unmarshal(event.Payload.Content, &content); err != nil {
		t.Fatalf("decode native user message content: %v", err)
	}
	for _, part := range content {
		if part.Type == "input_text" && strings.Contains(part.Text, fmt.Sprintf("marker %s.", marker)) {
			return true
		}
	}
	return false
}

// assertBinaryCodexProcessFailure 验证只有一个独立 process_failed 持久终态。
func assertBinaryCodexProcessFailure(t *testing.T, evidence *standaloneClaudePublicLifecycle) {
	t.Helper()
	var failed int
	if err := evidence.db.QueryRowContext(evidence.ctx, `SELECT count(*) FROM public_terminal_outcome_history WHERE agent_id = ? AND projection_kind = 'process_failed'`, evidence.agentID).Scan(&failed); err != nil {
		t.Fatal(err)
	}
	if failed != 1 {
		t.Fatalf("process_failed terminal count=%d want=1", failed)
	}
}

// assertBinaryCodexHistoryPreserved 比较旧终态整行并校验完成模型轮数，包含未来新增的列。
func assertBinaryCodexHistoryPreserved(t *testing.T, evidence *standaloneClaudePublicLifecycle, identity string, original []any, completed int64) {
	t.Helper()
	if !reflect.DeepEqual(original, evidence.historyRow(identity)) || evidence.completedTurnCount() != completed {
		t.Fatal("completed model history changed or completion count mismatch")
	}
}

// openBinaryCodexOwnedRollout 只打开精确测试 thread 唯一匹配的原生历史，不读取其他任务内容。
func openBinaryCodexOwnedRollout(t *testing.T, home, threadID string) *os.File {
	t.Helper()
	if threadID == "" || filepath.Base(threadID) != threadID || strings.ContainsAny(threadID, "*?[]") {
		t.Fatal("unsafe native thread identity")
	}
	paths, err := filepath.Glob(filepath.Join(home, "sessions", "*", "*", "*", "rollout-*-"+threadID+".jsonl"))
	if err != nil || len(paths) != 1 {
		t.Fatalf("require exact owned thread rollout: matches=%d err=%v", len(paths), err)
	}
	file, err := os.Open(paths[0])
	if err != nil {
		t.Fatal(err)
	}
	return file
}

// assertBinaryCodexOriginalExited 基于原启动身份确认目标已退出，不以 PID 消失猜测恢复成功。
func assertBinaryCodexOriginalExited(t *testing.T, original pidregistry.StableProcessIdentity) {
	t.Helper()
	alive, err := standaloneManagedProcessesAlive(standaloneManagedProcessSnapshot{original}, pidregistry.CaptureStableProcessIdentity)
	if err != nil {
		t.Fatal(err)
	}
	if len(alive) != 0 {
		t.Fatal("original provider process remains alive after crash")
	}
}

// testBinaryCodexInputEvidence 按已观察的0.153.3原生记录区分用户输入与assistant或工具回显。
func testBinaryCodexInputEvidence(t *testing.T) {
	for _, tc := range []struct {
		name, raw string
		want      bool
	}{
		{"user", `{"type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"the marker PARENT_FIRST."}]}}`, true},
		{"assistant", `{"type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"the marker PARENT_FIRST."}]}}`, false},
		{"tool", `{"type":"response_item","payload":{"type":"custom_tool_call_output","output":"the marker PARENT_FIRST."}}`, false},
		{"other-input", `{"type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"another task"}]}}`, false},
		{"not-input-text", `{"type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"output_text","text":"the marker PARENT_FIRST."}]}}`, false},
		{"legacy-event-not-counted", `{"type":"event_msg","payload":{"type":"user_message","message":"the marker PARENT_FIRST."}}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := binaryCodexInputHasMarker(t, []byte(tc.raw), "PARENT_FIRST"); got != tc.want {
				t.Fatalf("native input evidence=%t want=%t", got, tc.want)
			}
		})
	}
}
