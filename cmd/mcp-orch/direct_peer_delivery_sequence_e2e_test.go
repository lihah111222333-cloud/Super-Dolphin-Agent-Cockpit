//go:build e2e

package main

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/standalone"
	directpeerstore "github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/directpeer"
	mcpcommon "github.com/lihah111222333-cloud/super-dolphin-agent/internal/mcpserver/common"
)

// TestStandaloneDirectPeerCodexOrderedDelivery 验证真实Codex连续三轮、原样多语言正文和exclusive分页。
func TestStandaloneDirectPeerCodexOrderedDelivery(t *testing.T) {
	runDirectPeerOrderedDelivery(t, "CODEX", "codex")
}

// TestStandaloneDirectPeerClaudeOrderedDelivery 对真实Claude执行相同的顺序和持久游标契约。
func TestStandaloneDirectPeerClaudeOrderedDelivery(t *testing.T) {
	runDirectPeerOrderedDelivery(t, "CLAUDE", "claude")
}

// runDirectPeerOrderedDelivery 将每一轮报告与其持久消息对应，并验证不会返回前一页或其他接收者消息。
func runDirectPeerOrderedDelivery(t *testing.T, prefix, provider string) {
	cli, home, model := requireManagedProviderInputs(t, prefix)
	workspace, state := t.TempDir(), t.TempDir()
	cfg := standalone.Config{SchemaVersion: 1, PrincipalRef: "operator/test", ScopeRef: "workspace/test", WorkspaceRoot: workspace, StateDir: state,
		Listen: "127.0.0.1:0", CredentialFile: filepath.Join(state, "unused-token"),
		Providers: []standalone.Provider{{Name: provider, Executable: cli, Home: home, Models: []string{model}, Sandbox: providerSandbox(provider), AuthMode: directPeerAuthMode(provider)}}}
	db := managedProviderDatabase(t, state)
	app := startDirectPeerDeliveryApp(t, cfg, db)
	defer stopStandaloneTestApp(t, app.app)
	ctx, cancel := context.WithTimeout(t.Context(), 180*time.Second)
	defer cancel()
	persistDirectPeerSender(t, db, workspace, model)
	launchDirectPeerReceiver(t, app.registry, workspace, provider, model)
	grantDirectPeerThroughRegistry(t, directPeerTestApp{registry: app.registry}, workspace)
	var expected []directpeerstore.Message
	for round := 0; round < 3; round++ {
		t.Run(fmt.Sprintf("round-%d", round+1), func(t *testing.T) {
			expected = append(expected, submitOrderedPeerRound(t, ctx, app, workspace, round))
		})
		if t.Failed() {
			return
		}
	}
	assertOrderedPeerPages(t, app, workspace, expected)
}

// submitOrderedPeerRound 同时核对真实模型新报告与原样持久正文，不把格式变换后的回显当作交付证据。
func submitOrderedPeerRound(t *testing.T, ctx context.Context, app directPeerDeliveryApp, workspace string, round int) directpeerstore.Message {
	t.Helper()
	waitDirectPeerAgentIdle(t, ctx, app.agents, "receiver")
	baseline, err := app.reports.GetReport(ctx, "receiver")
	if err != nil {
		t.Fatal(err)
	}
	marker := "ORDERED_" + rand.Text()
	body := fmt.Sprintf("Please calculate the invoice total for invoice %s: item A costs %d and item B costs %d. Include the invoice reference and the total in your answer.\nCustomer notes: 中文、한국어、emoji 🐬\nquoted=\"value\"; slash=\\; round=%d", marker, 21+round, 37+round, round)
	result := callDirectPeerRegistry(t, app.registry, "send_direct_peer", mcpcommon.ToolScope{CWD: workspace, AgentID: "sender"},
		string(mustJSON(t, map[string]any{"receiver_agent_id": "receiver", "idempotency_key": fmt.Sprintf("ordered-%d", round), "message": body})))
	var message directpeerstore.Message
	if err := json.Unmarshal(mustJSON(t, result), &message); err != nil {
		t.Fatal(err)
	}
	if message.Status != "submitted" || message.SubmissionID == "" || message.MessageID == "" || message.Body != body || message.ReceiverAgentID != "receiver" || message.SenderAgentID != "sender" {
		t.Fatal("ordered delivery did not return the exact submitted message")
	}
	waitDirectPeerAgentIdle(t, ctx, app.agents, "receiver")
	assertDirectPeerDeliveryReport(t, ctx, app, "receiver", baseline.ReportSeq, marker)
	assertOrderedInvoiceTotal(t, ctx, app, marker, 58+2*round)
	return message
}

// assertOrderedInvoiceTotal 用实际业务计算结果佐证消息被正确消费，随机关联号本身不能充当金额。
func assertOrderedInvoiceTotal(t *testing.T, ctx context.Context, app directPeerDeliveryApp, marker string, total int) {
	t.Helper()
	report, err := app.reports.GetReport(ctx, "receiver")
	if err != nil {
		t.Fatal(err)
	}
	text := strings.ReplaceAll(report.Report, marker, "")
	if !regexp.MustCompile(fmt.Sprintf(`\b%d\b`, total)).MatchString(text) {
		t.Fatalf("invoice report lacks calculated total %d: %q", total, report.Report)
	}
}

// assertOrderedPeerPages 验证exclusive游标逐条前进、重复读取稳定、页末为空及接收者隔离。
func assertOrderedPeerPages(t *testing.T, app directPeerDeliveryApp, workspace string, expected []directpeerstore.Message) {
	t.Helper()
	var cursor int64
	for _, want := range expected {
		for repeat := 0; repeat < 2; repeat++ {
			page := readOrderedPeerPage(t, app, workspace, "receiver", cursor)
			assertOrderedPeerPage(t, page, want, cursor)
		}
		cursor = want.Cursor
	}
	if len(readOrderedPeerPage(t, app, workspace, "receiver", cursor)) != 0 {
		t.Fatal("exclusive final cursor returned old messages")
	}
	if len(readOrderedPeerPage(t, app, workspace, "sender", 0)) != 0 {
		t.Fatal("sender read another agent's inbox")
	}
}

// assertOrderedPeerPage 核对唯一页项的顺序及提交身份，不丢失正文比较。
func assertOrderedPeerPage(t *testing.T, page []directpeerstore.Message, want directpeerstore.Message, cursor int64) {
	t.Helper()
	if len(page) != 1 || page[0].Cursor <= cursor || page[0].MessageID != want.MessageID || page[0].Body != want.Body || page[0].SubmissionID != want.SubmissionID || page[0].Status != "submitted" {
		t.Fatal("exclusive page omitted, duplicated, reordered or changed submitted message")
	}
}

func readOrderedPeerPage(t *testing.T, app directPeerDeliveryApp, workspace, receiver string, cursor int64) []directpeerstore.Message {
	t.Helper()
	result := callDirectPeerRegistry(t, app.registry, "read_direct_peer", mcpcommon.ToolScope{CWD: workspace, AgentID: receiver},
		string(mustJSON(t, map[string]any{"after_cursor": cursor, "limit": 1})))
	var page []directpeerstore.Message
	if err := json.Unmarshal(mustJSON(t, result), &page); err != nil {
		t.Fatal(err)
	}
	return page
}
