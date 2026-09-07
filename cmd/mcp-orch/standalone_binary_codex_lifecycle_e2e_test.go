//go:build e2e

package main

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
)

// binaryCodexLifecycleClient 仅通过独立 daemon 的公开 HTTP MCP 操作任务。
type binaryCodexLifecycleClient struct {
	t                                            *testing.T
	ctx                                          context.Context
	client                                       *http.Client
	daemon                                       *serveE2EProcess
	fixture                                      serveE2EFixture
	session, model, proof, initialProof, realCWD string
	requestID                                    int
}

// TestStandaloneBinaryCodexLifecycleThroughMCP 验证 binary 的 cwd、重复 launch、真实 fork 及保持不可变 binding 的 Codex 原生恢复。
func TestStandaloneBinaryCodexLifecycleThroughMCP(t *testing.T) {
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
	parentID := "binary-lifecycle-parent"
	h.launch(parentID, "", "PARENT_FIRST")
	first := h.report(parentID, 0, "PARENT_FIRST")
	parent := h.idleSnapshot(parentID)
	h.assertDuplicateLaunch(parent, first)
	h.assertFork(parent)
	h.assertRecover(parent)
	if err := stopServeProcessE2E(t, daemon, nil); err != nil {
		t.Fatalf("daemon shutdown: %v", err)
	}
	assertServeEndpointClosed(t, client, daemon.ready.Endpoint)
}

// call 每次使用不同 request ID，并沿用拒绝 JSON-RPC/MCP error 的现有 helper。
func (h *binaryCodexLifecycleClient) call(name string, arguments map[string]any, result any) {
	h.t.Helper()
	h.requestID++
	raw := callServeToolStructured(h.t, h.client, h.daemon.ready.Endpoint, h.fixture.token, h.session, h.requestID, name, arguments)
	if err := json.Unmarshal(raw, result); err != nil {
		h.t.Fatalf("decode %s: %v payload=%s", name, err, serveE2EPayloadSummary(raw))
	}
}

// prompt 不透露 proof 或期望绝对路径；必须实际执行 shell 才能取得随机文件内容。
func (h *binaryCodexLifecycleClient) prompt(marker string) string {
	return fmt.Sprintf("Use the shell tool now to execute exactly: pwd -P; cat .orch-cwd-proof. Do not change directory. Report both exact output lines and the marker %s. Do not infer the output, and do not delegate.", marker)
}

// launch 对 root 和 fork 使用同一公开 schema，任何工具失败都立即阻断。
func (h *binaryCodexLifecycleClient) launch(agentID, parentID, marker string) {
	h.t.Helper()
	args := map[string]any{"agent_id": agentID, "name": agentID, "cwd": h.fixture.workspace, "provider": "codex", "model": h.model, "prompt": h.prompt(marker)}
	if parentID != "" {
		args["parent_id"], args["context_mode"] = parentID, "forked"
		args["prompt"] = h.prompt(marker) + " Also report the exact old random proof that the parent read in its PARENT_FIRST turn, using only the inherited conversation. Label it PARENT_INHERITED_PROOF. The on-disk proof has changed. Do not read history, rollout, transcript, report, or state files; do not call any other tools or commands beyond the exact pwd/cat command above. Do not infer or fabricate the parent proof; if the inherited conversation is unavailable, explicitly report that failure."
	}
	var result struct {
		Success bool   `json:"success"`
		AgentID string `json:"agent_id"`
	}
	h.call("launch_agent", args, &result)
	if !result.Success || result.AgentID != agentID {
		h.t.Fatalf("launch identity/success mismatch: %+v", result)
	}
}

// report 通过 after_report_seq 等待本轮完成，不把旧报告或模型复述 prompt 当成 cwd 证据。
func (h *binaryCodexLifecycleClient) report(agentID string, after int64, marker string) contract.AgentReportResult {
	h.t.Helper()
	var report contract.AgentReportResult
	h.call("get_agent_report", map[string]any{"agent_id": agentID, "wait": true, "timeout_ms": 120000, "after_report_seq": after}, &report)
	h.assertReport(report, agentID, after, marker)
	return report
}

// assertReport 同时验证身份、新 fence、成功终态及工作区随机 challenge。
func (h *binaryCodexLifecycleClient) assertReport(report contract.AgentReportResult, agentID string, after int64, marker string) {
	h.t.Helper()
	if report.AgentID != agentID || report.ReportSeq <= after || report.State != "idle" {
		h.t.Fatalf("report identity/fence/state mismatch: %+v", report)
	}
	for _, expected := range []string{marker, h.proof, h.realCWD} {
		if !strings.Contains(report.Report, expected) {
			h.t.Fatalf("report lacks required cwd/challenge/marker evidence %q: %q", expected, report.Report)
		}
	}
}

// snapshots 读取完整公开列表，重复 ID 必须被检测而非返回首个匹配。
func (h *binaryCodexLifecycleClient) snapshots() []contract.AgentSnapshot {
	h.t.Helper()
	var envelope struct {
		Data      []contract.AgentSnapshot `json:"data"`
		Total     int                      `json:"total"`
		Showing   int                      `json:"showing"`
		Truncated bool                     `json:"truncated"`
	}
	h.call("list_agents", map[string]any{"include_inactive": true, "envelope": true}, &envelope)
	if len(envelope.Data) == 0 || envelope.Total != len(envelope.Data) || envelope.Showing != len(envelope.Data) || envelope.Truncated {
		h.t.Fatal("list_agents did not return a complete canonical data envelope")
	}
	return envelope.Data
}

// snapshot 对同一个 persisted agent ID 要求恰好一个公开 runtime 条目。
func (h *binaryCodexLifecycleClient) snapshot(agentID string) contract.AgentSnapshot {
	h.t.Helper()
	var found contract.AgentSnapshot
	count := 0
	for _, snapshot := range h.snapshots() {
		if snapshot.AgentID == agentID {
			found = snapshot
			count++
		}
	}
	if count != 1 {
		h.t.Fatalf("agent %s has %d snapshots, want one", agentID, count)
	}
	if found.ThreadID == "" || found.Cwd != h.fixture.workspace {
		h.t.Fatalf("snapshot thread/cwd mismatch: %+v", found)
	}
	return found
}

// idleSnapshot 有界等待终态报告后的 runtime settlement，不容忍 stopped/failed。
func (h *binaryCodexLifecycleClient) idleSnapshot(agentID string) contract.AgentSnapshot {
	h.t.Helper()
	ctx, cancel := context.WithTimeout(h.ctx, 15*time.Second)
	defer cancel()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		snapshot := h.snapshot(agentID)
		if snapshot.State == "idle" && snapshot.ActiveTurnID == "" {
			return snapshot
		}
		if snapshot.State == "stopped" || snapshot.State == "failed" {
			h.t.Fatalf("unexpected terminal snapshot: %+v", snapshot)
		}
		select {
		case <-ctx.Done():
			h.t.Fatalf("idle settlement: %v", ctx.Err())
		case <-ticker.C:
		}
	}
}

// assertDuplicateLaunch 校验 existing 回执、单一 runtime 身份及未推进的报告 fence。
func (h *binaryCodexLifecycleClient) assertDuplicateLaunch(parent contract.AgentSnapshot, first contract.AgentReportResult) {
	h.t.Helper()
	var duplicate struct {
		Success  bool   `json:"success"`
		AgentID  string `json:"agent_id"`
		ThreadID string `json:"thread_id"`
		Status   string `json:"status"`
	}
	h.call("launch_agent", map[string]any{"agent_id": parent.AgentID, "name": parent.Name, "cwd": h.fixture.workspace, "provider": "codex", "model": h.model, "prompt": h.prompt("DUPLICATE_MUST_NOT_RUN")}, &duplicate)
	if !duplicate.Success || duplicate.Status != "existing" || duplicate.AgentID != parent.AgentID || duplicate.ThreadID != parent.ThreadID {
		h.t.Fatalf("duplicate did not return same existing thread: %+v", duplicate)
	}
	after := h.idleSnapshot(parent.AgentID)
	if after.ThreadID != parent.ThreadID || after.PID != parent.PID || !after.CreatedAt.Equal(parent.CreatedAt) {
		h.t.Fatalf("duplicate changed runtime snapshot: before=%+v after=%+v", parent, after)
	}
	var report contract.AgentReportResult
	h.call("get_agent_report", map[string]any{"agent_id": parent.AgentID}, &report)
	if report.ReportSeq != first.ReportSeq || report.Report != first.Report {
		h.t.Fatal("duplicate launch submitted an additional turn")
	}
}

// assertFork 同时验证父对话旧 proof 的继承、磁盘新 proof 与公开 parent/thread 关系。
func (h *binaryCodexLifecycleClient) assertFork(parent contract.AgentSnapshot) {
	h.t.Helper()
	childID := "binary-lifecycle-child"
	parentProof := h.proof
	h.rotateProof()
	h.launch(childID, parent.AgentID, "CHILD_FIRST")
	first := h.report(childID, 0, "CHILD_FIRST")
	if parentProof == h.proof || !strings.Contains(first.Report, "PARENT_INHERITED_PROOF") || !strings.Contains(first.Report, parentProof) {
		h.t.Fatal("fork report did not preserve the parent conversation proof alongside the rotated disk proof")
	}
	child := h.idleSnapshot(childID)
	if child.ParentID != parent.AgentID || child.ThreadID == parent.ThreadID {
		h.t.Fatalf("fork relationship mismatch: parent=%+v child=%+v", parent, child)
	}
	h.rotateProof()
	h.followUp(childID, first.ReportSeq, "CHILD_SECOND")
	after := h.idleSnapshot(childID)
	if after.ParentID != parent.AgentID || after.ThreadID != child.ThreadID {
		h.t.Fatal("child follow-up changed fork binding")
	}
}

// followUp 要求提交前 fence 与已知报告一致，且返回新的成功报告。
func (h *binaryCodexLifecycleClient) followUp(agentID string, previous int64, marker string) contract.AgentReportResult {
	h.t.Helper()
	var result struct {
		Success  bool                       `json:"success"`
		AgentID  string                     `json:"agent_id"`
		Previous int64                      `json:"previous_report_seq"`
		Report   contract.AgentReportResult `json:"report"`
	}
	message := h.prompt(marker)
	if marker == "PARENT_RECOVERED" {
		message += " Also report the exact original random proof read in PARENT_FIRST from the resumed conversation. Do not read history, rollout, transcript, report or state files, and do not infer or fabricate it."
	}
	h.call("send_message", map[string]any{"agent_id": agentID, "message": message, "wait_report": true, "timeout_ms": 120000}, &result)
	if !result.Success || result.AgentID != agentID || result.Previous != previous {
		h.t.Fatalf("follow-up prior fence mismatch: %+v", result)
	}
	h.assertReport(result.Report, agentID, previous, marker)
	if marker == "PARENT_RECOVERED" && !strings.Contains(result.Report.Report, h.initialProof) {
		h.t.Fatal("native recovery did not preserve the original conversation proof")
	}
	return result.Report
}

// assertRecover 验证 Codex 以原生 unarchive/resume 恢复同一不可变 thread binding。
func (h *binaryCodexLifecycleClient) assertRecover(parent contract.AgentSnapshot) {
	h.t.Helper()
	var stopped struct {
		Success  bool   `json:"success"`
		AgentID  string `json:"agent_id"`
		Stopped  bool   `json:"stopped"`
		Archived bool   `json:"archived"`
	}
	h.call("stop_agent", map[string]any{"agent_id": parent.AgentID, "wait": true, "timeout_ms": 30000}, &stopped)
	if !stopped.Success || !stopped.Stopped || !stopped.Archived || stopped.AgentID != parent.AgentID {
		h.t.Fatalf("Codex stop/archive failed: %+v", stopped)
	}
	before := h.snapshot(parent.AgentID)
	if before.State != "stopped" && before.State != "failed" {
		h.t.Fatalf("recover requires stopped/failed: %+v", before)
	}
	var recovered contract.AgentSnapshot
	h.call("recover_agent", map[string]any{"agent_id": parent.AgentID}, &recovered)
	h.assertRecoveredRuntime(recovered, parent)
	h.t.Logf("recover_agent identity: agent=%s before_thread=%s after_thread=%s; native same-thread resume", parent.AgentID, parent.ThreadID, recovered.ThreadID)
	h.idleSnapshot(parent.AgentID)
	var report contract.AgentReportResult
	h.call("get_agent_report", map[string]any{"agent_id": parent.AgentID}, &report)
	h.rotateProof()
	h.followUp(parent.AgentID, report.ReportSeq, "PARENT_RECOVERED")
	after := h.idleSnapshot(parent.AgentID)
	h.assertRecoveredRuntime(after, parent)
	if after.ThreadID != recovered.ThreadID {
		h.t.Fatal("recovered turn changed the new runtime thread binding")
	}
}

// assertRecoveredRuntime 锁定恢复后的 agent/cwd 和原 thread，不通过更换不可变 binding 伪造恢复。
func (h *binaryCodexLifecycleClient) assertRecoveredRuntime(recovered, parent contract.AgentSnapshot) {
	h.t.Helper()
	if recovered.AgentID != parent.AgentID || recovered.ThreadID == "" || recovered.ThreadID != parent.ThreadID || recovered.Cwd != parent.Cwd {
		h.t.Fatalf("recover must restore usable agent runtime: before=%+v after=%+v", parent, recovered)
	}
	if recovered.State != "idle" {
		h.t.Fatalf("recover did not produce idle runtime: %+v", recovered)
	}
}

// rotateProof 为每个模型 turn 生成新挑战，防 fork 历史或上一轮输出被重用为 shell 执行证据。
func (h *binaryCodexLifecycleClient) rotateProof() {
	h.t.Helper()
	h.proof = rand.Text()
	writeServeE2EFile(h.t, filepath.Join(h.fixture.workspace, ".orch-cwd-proof"), []byte(h.proof+"\n"), 0o600)
}
