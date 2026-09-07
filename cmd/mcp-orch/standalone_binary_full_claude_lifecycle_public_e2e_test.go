//go:build e2e

package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"reflect"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/terminaloutcome"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
)

// standaloneClaudePublicLifecycle 从同一 operator HTTP session 验证 stop/recover，不访问 Execution 内存。
type standaloneClaudePublicLifecycle struct {
	t                *testing.T
	ctx              context.Context
	db               *sql.DB
	fixture          serveE2EFixture
	daemon           *serveE2EProcess
	client           *http.Client
	session, agentID string
	requestID        int
}

// call 复用严格 HTTP/MCP error 校验，并显式解码当前公开 response shape。
func (h *standaloneClaudePublicLifecycle) call(name string, args map[string]any, out any) {
	h.t.Helper()
	h.requestID++
	raw := callServeToolStructured(h.t, h.client, h.daemon.ready.Endpoint, h.fixture.token, h.session, h.requestID, name, args)
	if err := json.Unmarshal(raw, out); err != nil {
		h.t.Fatalf("decode %s: %v", name, err)
	}
}

// stopAndCheckRecovery 验证 idle stop 独立终态，并按既有契约拒绝不支持的 Claude 恢复。
func (h *standaloneClaudePublicLifecycle) stopAndCheckRecovery(second contract.TerminalOutcomeCommit) {
	h.t.Helper()
	waitManagedClaudeRuntimeIdleThroughMCP(h.t, h.ctx, h.client, h.daemon.ready.Endpoint, h.fixture.token, h.session, h.agentID)
	oldRow := h.historyRow(second.Identity.TerminalIdentity)
	completed := h.completedTurnCount()
	h.stopPublicAgent()
	stopped := h.currentTerminal()
	h.assertStoppedTerminal(stopped, second)
	if !reflect.DeepEqual(oldRow, h.historyRow(second.Identity.TerminalIdentity)) || h.completedTurnCount() != completed {
		h.t.Fatal("idle stop changed a completed turn or fabricated a model completion")
	}
	h.assertReportMatches(stopped, "stopped")
	h.assertRecoveryUnsupported(stopped)
	if !reflect.DeepEqual(oldRow, h.historyRow(second.Identity.TerminalIdentity)) || h.completedTurnCount() != completed {
		h.t.Fatal("unsupported recovery changed completed history")
	}
}

// stopPublicAgent 要求 Claude unsupported archive 后执行真实 stop，并在公开列表中保留 stopped 快照。
func (h *standaloneClaudePublicLifecycle) stopPublicAgent() {
	h.t.Helper()
	var result struct {
		Success  bool   `json:"success"`
		AgentID  string `json:"agent_id"`
		Stopped  bool   `json:"stopped"`
		Archived bool   `json:"archived"`
		State    string `json:"state"`
	}
	h.call("stop_agent", map[string]any{"agent_id": h.agentID, "wait": true, "timeout_ms": 30000}, &result)
	if !result.Success || !result.Stopped || result.Archived || result.AgentID != h.agentID || result.State != "stopped" {
		h.t.Fatalf("Claude public stop mismatch: %+v", result)
	}
	if got := h.snapshot(); got.State != "stopped" {
		h.t.Fatalf("public stopped snapshot mismatch: %+v", got)
	}
}

// snapshot 显式请求 envelope，要求目标 agent 恰好出现一次且 cwd/thread 完整。
func (h *standaloneClaudePublicLifecycle) snapshot() contract.AgentSnapshot {
	h.t.Helper()
	var result struct {
		Agents []contract.AgentSnapshot `json:"agents"`
	}
	h.call("list_agents", map[string]any{"include_inactive": true, "envelope": true}, &result)
	var found contract.AgentSnapshot
	count := 0
	for _, item := range result.Agents {
		if item.AgentID == h.agentID {
			found = item
			count++
		}
	}
	if count != 1 || found.Cwd != h.fixture.workspace || found.ThreadID == "" {
		h.t.Fatalf("public snapshot identity/cwd mismatch: count=%d snapshot=%+v", count, found)
	}
	return found
}

// currentTerminal 通过生产 owner 校验 current-head/history/outbox 身份闭包。
func (h *standaloneClaudePublicLifecycle) currentTerminal() contract.TerminalOutcomeCommit {
	h.t.Helper()
	got, err := terminaloutcome.New(h.db).GetPublicTerminalOutcome(h.ctx, h.agentID)
	if err != nil {
		h.t.Fatal(err)
	}
	if err := got.Validate(); err != nil {
		h.t.Fatal(err)
	}
	return got
}

// assertStoppedTerminal 允许 head fence 推进，但绝不把 idle stop 写成新的模型完成。
func (h *standaloneClaudePublicLifecycle) assertStoppedTerminal(stopped, second contract.TerminalOutcomeCommit) {
	h.t.Helper()
	if stopped.ProjectionKind != "process_stopped" || stopped.PublicOutcome.Kind != "stopped" || stopped.Identity.HeadVersion <= second.Identity.HeadVersion {
		h.t.Fatal("idle stop must advance a process_stopped lifecycle head")
	}
	if stopped.Identity.ProviderTurnID == second.Identity.ProviderTurnID || stopped.Identity.TerminalIdentity == second.Identity.TerminalIdentity || stopped.Identity.PublicThreadID != second.Identity.PublicThreadID {
		h.t.Fatal("idle stop reused model terminal identity or changed thread")
	}
}

// assertReportMatches 将公开状态/报告 fence 与真实持久终态对应，拒绝 SSOT overlay 漂移。
func (h *standaloneClaudePublicLifecycle) assertReportMatches(outcome contract.TerminalOutcomeCommit, state string) {
	h.t.Helper()
	var report contract.AgentReportResult
	h.call("get_agent_report", map[string]any{"agent_id": h.agentID}, &report)
	if report.AgentID != h.agentID || report.ReportSeq != int64(outcome.Identity.HeadVersion) || report.State != state || report.Report != outcome.PublicReport {
		h.t.Fatalf("public report and durable terminal differ: %+v", report)
	}
}

// assertRecoveryUnsupported 要求准确 capability 错误，不能把新会话冒充 Claude 持久恢复。
func (h *standaloneClaudePublicLifecycle) assertRecoveryUnsupported(stopped contract.TerminalOutcomeCommit) {
	h.t.Helper()
	before := h.snapshot()
	h.requestID++
	reply := callServeE2E(h.t, h.client, h.daemon.ready.Endpoint, h.fixture.token, h.session, h.requestID, "tools/call", map[string]any{"name": "recover_agent", "arguments": map[string]any{"agent_id": h.agentID}})
	var result struct {
		IsError           bool `json:"isError"`
		StructuredContent struct {
			Code  string `json:"code"`
			Error string `json:"error"`
		} `json:"structuredContent"`
	}
	if err := json.Unmarshal(reply.Result, &result); err != nil {
		h.t.Fatal(err)
	}
	if !result.IsError || result.StructuredContent.Code != "capability_unsupported" || result.StructuredContent.Error != `capability "thread_resume" is not supported by claude driver` {
		h.t.Fatalf("Claude recovery must return its exact unsupported capability: %+v", result)
	}
	after := h.snapshot()
	if after.AgentID != before.AgentID || after.ThreadID != before.ThreadID || after.Cwd != before.Cwd || after.State != "stopped" {
		h.t.Fatal("unsupported Claude recovery changed agent binding/state")
	}
	if !reflect.DeepEqual(h.currentTerminal(), stopped) {
		h.t.Fatal("unsupported Claude recovery changed canonical terminal/head")
	}
	h.assertReportMatches(stopped, "stopped")
}

// completedTurnCount 只计模型完成历史，process_stopped 允许独立追加。
func (h *standaloneClaudePublicLifecycle) completedTurnCount() int64 {
	h.t.Helper()
	var count int64
	if err := h.db.QueryRowContext(h.ctx, `SELECT count(*) FROM public_terminal_outcome_history WHERE agent_id = ? AND projection_kind = 'turn_completed'`, h.agentID).Scan(&count); err != nil {
		h.t.Fatal(err)
	}
	return count
}

// historyRow 动态读取指定旧终态全部持久字段，避免字段新增后遗漏不可变性校验。
func (h *standaloneClaudePublicLifecycle) historyRow(identity string) []any {
	h.t.Helper()
	rows, err := h.db.QueryContext(h.ctx, `SELECT * FROM public_terminal_outcome_history WHERE terminal_identity = ? AND agent_id = ?`, identity, h.agentID)
	if err != nil {
		h.t.Fatal(err)
	}
	defer func() {
		if err := rows.Close(); err != nil {
			h.t.Error(err)
		}
	}()
	columns, err := rows.Columns()
	if err != nil {
		h.t.Fatal(err)
	}
	values, destinations := make([]any, len(columns)), make([]any, len(columns))
	for i := range values {
		destinations[i] = &values[i]
	}
	if !rows.Next() {
		h.t.Fatalf("expected old terminal history row: %v", rows.Err())
	}
	if err := rows.Scan(destinations...); err != nil {
		h.t.Fatal(err)
	}
	if rows.Next() {
		h.t.Fatal("duplicate terminal identity")
	}
	if err := rows.Err(); err != nil {
		h.t.Fatal(err)
	}
	return values
}
