//go:build e2e

package main

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/terminaloutcome"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
)

// TestStandaloneBinaryCodexMessageRecovery 验证真实提交超时后的只读恢复及公开 interrupt，不重复模型输入。
func TestStandaloneBinaryCodexMessageRecovery(t *testing.T) {
	cli, home, model := requireManagedProviderInputs(t, "CODEX")
	binary := buildServeE2EBinary(t)
	ctx, cancel := context.WithTimeout(context.Background(), 360*time.Second)
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
	const agentID = "binary-message-recovery"
	h.launch(agentID, "", "RECOVERY_INITIAL")
	first := h.report(agentID, 0, "RECOVERY_INITIAL")
	before := h.idleSnapshot(agentID)
	assertBinaryMessageTimeoutRecovery(t, h, before, first, home)
	observed := assertBinaryPublicInterrupt(t, h, before, home, cli)
	observed = append(observed, assertBinaryActiveStop(t, h, before, home, cli)...)
	if err := stopServeProcessE2E(t, daemon, nil); err != nil {
		t.Fatalf("daemon shutdown: %v", err)
	}
	assertNoStandaloneManagedDescendants(t, observed)
	assertServeEndpointClosed(t, client, daemon.ready.Endpoint)
}

// binaryMessageRecoveryMetadata 保留错误中原始查询参数，验证后原样发送，不自行重建恢复请求。
type binaryMessageRecoveryMetadata struct {
	Phase    string `json:"phase"`
	Status   string `json:"submission_status"`
	AgentID  string `json:"agent_id"`
	ThreadID string `json:"thread_id"`
	Previous *int64 `json:"previous_report_seq"`
	Recovery struct {
		Tool      string          `json:"tool"`
		Arguments json.RawMessage `json:"arguments"`
	} `json:"recovery"`
}

// assertBinaryMessageTimeoutRecovery 只接受已提交但等待超时，随后执行返回的只读查询并核对原生输入次数。
func assertBinaryMessageTimeoutRecovery(t *testing.T, h *binaryCodexLifecycleClient, before contract.AgentSnapshot, first contract.AgentReportResult, home string) {
	t.Helper()
	const marker = "RECOVERY_WAITED_ONCE"
	h.rotateProof()
	message := fmt.Sprintf("Use the shell tool now to execute exactly: sleep 8; pwd -P; cat .orch-cwd-proof. Do not change directory. Wait for the command to finish, then report both output lines and marker %s. Do not infer output or delegate.", marker)
	h.requestID++
	reply := callServeE2E(t, h.client, h.daemon.ready.Endpoint, h.fixture.token, h.session, h.requestID, "tools/call", map[string]any{
		"name": "send_message", "arguments": map[string]any{"agent_id": before.AgentID, "message": message, "wait_report": true, "timeout_ms": 2000},
	})
	recovery := requireBinaryMessageRecovery(t, reply, before, first.ReportSeq)
	h.requestID++
	resumed := callServeE2E(t, h.client, h.daemon.ready.Endpoint, h.fixture.token, h.session, h.requestID, "tools/call", map[string]any{
		"name": recovery.Recovery.Tool, "arguments": recovery.Recovery.Arguments,
	})
	var report contract.AgentReportResult
	decodeBinaryMessageResult(t, resumed, &report)
	h.assertReport(report, before.AgentID, first.ReportSeq, marker)
	after := h.idleSnapshot(before.AgentID)
	if after.ThreadID != before.ThreadID {
		t.Fatal("report recovery changed thread binding")
	}
	assertBinaryCodexInputCount(t, home, before.ThreadID, marker, 1)
}

// requireBinaryMessageRecovery 验证最终 MCP 错误阶段、身份和真实游标，拒绝 unknown 或任意其他失败。
func requireBinaryMessageRecovery(t *testing.T, reply serveE2EReply, before contract.AgentSnapshot, previous int64) binaryMessageRecoveryMetadata {
	t.Helper()
	var envelope struct {
		IsError    bool `json:"isError"`
		Structured struct {
			Code      string `json:"code"`
			Error     string `json:"error"`
			Retryable bool   `json:"retryable"`
			Meta      struct {
				Recovery binaryMessageRecoveryMetadata `json:"submission_recovery"`
			} `json:"meta"`
		} `json:"structuredContent"`
	}
	if err := json.Unmarshal(reply.Result, &envelope); err != nil {
		t.Fatal(err)
	}
	if !envelope.IsError || envelope.Structured.Code != "report_wait_incomplete" || envelope.Structured.Retryable {
		t.Fatalf("want nonretryable report_wait_incomplete, got %s", serveE2EPayloadSummary(reply.Result))
	}
	if !strings.Contains(envelope.Structured.Error, "timed out waiting") {
		t.Fatalf("want report wait timeout, got %s", serveE2EPayloadSummary(reply.Result))
	}
	recovery := envelope.Structured.Meta.Recovery
	requireBinaryMessageRecoveryIdentity(t, recovery, before, previous)
	requireBinaryReadOnlyRecoveryQuery(t, recovery, before.AgentID, previous)
	return recovery
}

// requireBinaryMessageRecoveryIdentity 核对提交阶段和已观察到的报告身份。
func requireBinaryMessageRecoveryIdentity(t *testing.T, recovery binaryMessageRecoveryMetadata, before contract.AgentSnapshot, previous int64) {
	t.Helper()
	if recovery.Phase != "wait_report" || recovery.Status != "submitted" || recovery.AgentID != before.AgentID || recovery.ThreadID != before.ThreadID {
		t.Fatalf("wrong submission recovery stage/identity: %+v", recovery)
	}
	if recovery.Previous == nil || *recovery.Previous != previous {
		t.Fatalf("wrong recovery baseline: %+v", recovery)
	}
}

// requireBinaryReadOnlyRecoveryQuery 拒绝恢复查询中的重发工具、额外参数或虚假游标。
func requireBinaryReadOnlyRecoveryQuery(t *testing.T, recovery binaryMessageRecoveryMetadata, agentID string, previous int64) {
	t.Helper()
	if recovery.Recovery.Tool != "get_agent_report" {
		t.Fatalf("unsafe recovery tool %q", recovery.Recovery.Tool)
	}
	var arguments map[string]json.RawMessage
	if err := json.Unmarshal(recovery.Recovery.Arguments, &arguments); err != nil {
		t.Fatal(err)
	}
	if len(arguments) != 3 {
		t.Fatalf("unexpected recovery arguments: %s", recovery.Recovery.Arguments)
	}
	var query struct {
		AgentID string `json:"agent_id"`
		Wait    bool   `json:"wait"`
		After   *int64 `json:"after_report_seq"`
	}
	if err := json.Unmarshal(recovery.Recovery.Arguments, &query); err != nil {
		t.Fatal(err)
	}
	if query.AgentID != agentID || !query.Wait || query.After == nil || *query.After != previous {
		t.Fatalf("wrong read-only recovery query: %s", recovery.Recovery.Arguments)
	}
}

// decodeBinaryMessageResult 要求成功的 structuredContent，避免文本兜底掩盖 MCP 消费面错误。
func decodeBinaryMessageResult(t *testing.T, reply serveE2EReply, destination any) {
	t.Helper()
	var envelope struct {
		IsError    bool            `json:"isError"`
		Structured json.RawMessage `json:"structuredContent"`
	}
	if err := json.Unmarshal(reply.Result, &envelope); err != nil {
		t.Fatal(err)
	}
	if envelope.IsError || len(envelope.Structured) == 0 || string(envelope.Structured) == "null" {
		t.Fatalf("recovery query failed: %s", serveE2EPayloadSummary(reply.Result))
	}
	if err := json.Unmarshal(envelope.Structured, destination); err != nil {
		t.Fatal(err)
	}
}

// assertBinaryPublicInterrupt 等真实 turn_running 后经公开 HTTP 中断，并验证任务可继续使用原线程。
func assertBinaryPublicInterrupt(t *testing.T, h *binaryCodexLifecycleClient, before contract.AgentSnapshot, home, cli string) standaloneManagedProcessSnapshot {
	t.Helper()
	const marker = "PUBLIC_INTERRUPT_ONCE"
	ready := rand.Text()
	var submitted struct {
		Success bool   `json:"success"`
		AgentID string `json:"agent_id"`
	}
	message := binaryPublicInterruptPrompt(ready, marker)
	h.call("send_message", map[string]any{"agent_id": before.AgentID, "message": message}, &submitted)
	if !submitted.Success || submitted.AgentID != before.AgentID {
		t.Fatalf("interrupt input not submitted: %+v", submitted)
	}
	running := waitBinaryMessageRunning(t, h, before)
	waitBinaryInterruptExecution(t, h, running, ready)
	observed := assertStandaloneManagedProcessTree(t, h.daemon, cli)
	binaryInterruptCommandIdentity(t, h.fixture.workspace, observed)
	assertBinaryCodexInputCount(t, home, before.ThreadID, marker, 1)
	var interrupted struct {
		Success      bool   `json:"success"`
		Interrupted  bool   `json:"interrupted"`
		AgentID      string `json:"agent_id"`
		State        string `json:"state"`
		ActiveTurnID string `json:"active_turn_id"`
	}
	h.call("interrupt_agent", map[string]any{"agent_id": before.AgentID, "timeout_ms": 30000}, &interrupted)
	if !interrupted.Success || !interrupted.Interrupted || interrupted.AgentID != before.AgentID || interrupted.State != "idle" || interrupted.ActiveTurnID != "" {
		t.Fatalf("public interrupt did not settle: %+v", interrupted)
	}
	after := h.idleSnapshot(before.AgentID)
	if after.ThreadID != before.ThreadID {
		t.Fatal("public interrupt changed thread binding")
	}
	assertBinaryCodexInputCount(t, home, before.ThreadID, marker, 1)
	assertBinaryInterruptContinuation(t, h, running, home)
	return observed
}

func binaryPublicInterruptPrompt(ready, marker string) string {
	return fmt.Sprintf("Use the shell tool to execute exactly: printf '%%s' \"$$\" > .orch-interrupt-pid; printf '%%s' '%s' > .orch-interrupt-ready; sleep 20. Do not answer until the command finishes. marker %s. Do not delegate.", ready, marker)
}

// assertBinaryActiveStop 以真实运行长命令验证结束agent会清理资源；中断turn不承诺终止后台命令。
func assertBinaryActiveStop(t *testing.T, h *binaryCodexLifecycleClient, before contract.AgentSnapshot, home, cli string) standaloneManagedProcessSnapshot {
	t.Helper()
	const marker = "PUBLIC_STOP_RUNNING_ONCE"
	ready := rand.Text()
	message := strings.Replace(binaryPublicInterruptPrompt(ready, marker), "sleep 20", "sleep 60", 1)
	var submitted struct {
		Success bool `json:"success"`
	}
	h.call("send_message", map[string]any{"agent_id": before.AgentID, "message": message}, &submitted)
	if !submitted.Success {
		t.Fatal("active stop input was not submitted")
	}
	running := waitBinaryMessageRunning(t, h, before)
	waitBinaryInterruptExecution(t, h, running, ready)
	observed := assertStandaloneManagedProcessTree(t, h.daemon, cli)
	binaryInterruptCommandIdentity(t, h.fixture.workspace, observed)
	assertBinaryCodexInputCount(t, home, before.ThreadID, marker, 1)
	rollout := openBinaryCodexOwnedRollout(t, home, before.ThreadID)
	defer func() {
		if err := rollout.Close(); err != nil {
			t.Error(err)
		}
	}()
	var stopped struct {
		Success  bool   `json:"success"`
		AgentID  string `json:"agent_id"`
		Stopped  bool   `json:"stopped"`
		Archived bool   `json:"archived"`
	}
	h.call("stop_agent", map[string]any{"agent_id": before.AgentID, "wait": true, "timeout_ms": 30000}, &stopped)
	if !stopped.Success || !stopped.Stopped || !stopped.Archived || stopped.AgentID != before.AgentID {
		t.Fatalf("active stop/archive did not settle: %+v", stopped)
	}
	assertNoStandaloneManagedDescendants(t, observed)
	assertBinaryCodexRolloutInputCount(t, rollout, marker, 1)
	return observed
}

// binaryInterruptCommandIdentity 将工具自行写入的PID绑定到中断前真实后代身份，仅用于退出观测。
func binaryInterruptCommandIdentity(t *testing.T, workspace string, observed standaloneManagedProcessSnapshot) standaloneManagedProcessSnapshot {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(workspace, ".orch-interrupt-pid"))
	if err != nil {
		t.Fatal(err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(raw)))
	if err != nil || pid <= 1 {
		t.Fatalf("invalid command PID evidence: %q", raw)
	}
	for _, identity := range observed {
		if identity.PID == pid {
			return standaloneManagedProcessSnapshot{identity}
		}
	}
	t.Fatal("interrupt command PID not present in owned descendant snapshot")
	return nil
}

// TestStandaloneInterruptPromptEvidence 锁定真实中断提示的原生输入标记格式，不调用模型。
func TestStandaloneInterruptPromptEvidence(t *testing.T) {
	const marker = "PUBLIC_INTERRUPT_ONCE"
	raw := mustJSON(t, map[string]any{
		"type": "response_item",
		"payload": map[string]any{"type": "message", "role": "user", "content": []any{
			map[string]any{"type": "input_text", "text": binaryPublicInterruptPrompt("proof", marker)},
		}},
	})
	if !binaryCodexInputHasMarker(t, raw, marker) {
		t.Fatal("actual interrupt prompt is not recognized by native input evidence parser")
	}
}

// waitBinaryInterruptExecution 先观察provider实际执行shell的随机信号，避免只凭turn_started抢先中断输入接收。
func waitBinaryInterruptExecution(t *testing.T, h *binaryCodexLifecycleClient, running contract.AgentSnapshot, proof string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(h.ctx, 30*time.Second)
	defer cancel()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		content, err := os.ReadFile(filepath.Join(h.fixture.workspace, ".orch-interrupt-ready"))
		if err == nil && string(content) == proof {
			requireBinaryInterruptRunning(t, h.snapshot(running.AgentID), running)
			return
		}
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("read provider execution proof: %v", err)
		}
		select {
		case <-ctx.Done():
			t.Fatalf("provider did not execute interrupt precondition: %v", ctx.Err())
		case <-ticker.C:
		}
	}
}

// requireBinaryInterruptRunning 将实际执行信号绑定到尚未完成的同一个provider turn。
func requireBinaryInterruptRunning(t *testing.T, current, running contract.AgentSnapshot) {
	t.Helper()
	if current.ThreadID != running.ThreadID || current.ActiveTurnID != running.ActiveTurnID || current.State != "turn_running" {
		t.Fatalf("turn completed or changed before interrupt: %+v", current)
	}
}

// waitBinaryMessageRunning 有界等待活动 turn 身份，避免在模型尚未开始时发出中断。
func waitBinaryMessageRunning(t *testing.T, h *binaryCodexLifecycleClient, before contract.AgentSnapshot) contract.AgentSnapshot {
	t.Helper()
	ctx, cancel := context.WithTimeout(h.ctx, 15*time.Second)
	defer cancel()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		current := h.snapshot(before.AgentID)
		if current.ThreadID != before.ThreadID {
			t.Fatal("new turn changed thread binding")
		}
		if current.State == "turn_running" && current.ActiveTurnID != "" {
			return current
		}
		if current.State == "stopped" || current.State == "failed" {
			t.Fatalf("new turn unexpectedly terminal: %+v", current)
		}
		select {
		case <-ctx.Done():
			t.Fatalf("new turn never became running: %v", ctx.Err())
		case <-ticker.C:
		}
	}
}

// assertBinaryInterruptContinuation 先证明被中断 turn 的持久终态，再以新 proof 验证原线程继续工作。
func assertBinaryInterruptContinuation(t *testing.T, h *binaryCodexLifecycleClient, running contract.AgentSnapshot, home string) {
	t.Helper()
	interruptedSeq := requireBinaryInterruptedTerminal(t, h, running)
	var current contract.AgentReportResult
	h.call("get_agent_report", map[string]any{"agent_id": running.AgentID}, &current)
	if current.ReportSeq <= 0 || uint64(current.ReportSeq) != interruptedSeq {
		t.Fatalf("interrupted report fence differs from persisted terminal: seq=%d terminal=%d", current.ReportSeq, interruptedSeq)
	}
	h.rotateProof()
	const marker = "PUBLIC_INTERRUPT_CONTINUED"
	h.followUp(running.AgentID, current.ReportSeq, marker)
	after := h.idleSnapshot(running.AgentID)
	if after.ThreadID != running.ThreadID {
		t.Fatal("post-interrupt follow-up changed original thread")
	}
	assertBinaryCodexInputCount(t, home, running.ThreadID, marker, 1)
}

// requireBinaryInterruptedTerminal 经生产 owner 验证确切活动 turn 已中断，拒绝自然成功或其他 turn 的结果。
func requireBinaryInterruptedTerminal(t *testing.T, h *binaryCodexLifecycleClient, running contract.AgentSnapshot) uint64 {
	t.Helper()
	db, _, _ := openStandaloneClaudeEvidenceStore(t, h.ctx, h.fixture)
	outcome, err := terminaloutcome.New(db).GetPublicTerminalOutcome(h.ctx, running.AgentID)
	if err != nil {
		t.Fatal(err)
	}
	if err := outcome.Validate(); err != nil {
		t.Fatal(err)
	}
	if outcome.Identity.AgentID != running.AgentID || outcome.Identity.PublicThreadID != running.ThreadID || outcome.Identity.ProviderTurnID != running.ActiveTurnID {
		t.Fatalf("terminal does not identify interrupted active turn: identity=%+v active=%s", outcome.Identity, running.ActiveTurnID)
	}
	if outcome.ProjectionKind != "turn_completed" || outcome.PublicOutcome.Kind != "stopped" || outcome.PublicOutcome.Code != "interrupted" {
		t.Fatalf("active turn did not end interrupted: kind=%s code=%s", outcome.PublicOutcome.Kind, outcome.PublicOutcome.Code)
	}
	return outcome.Identity.HeadVersion
}
