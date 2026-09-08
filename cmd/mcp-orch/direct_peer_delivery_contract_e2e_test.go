//go:build e2e

package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/standalone"
	directpeer "github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/directpeer"
	mcpcommon "github.com/lihah111222333-cloud/super-dolphin-agent/internal/mcpserver/common"
)

type peerContractFixture struct {
	app       directPeerTestApp
	workspace string
}

// newPeerContractFixture 使用真实Fx registry和SQLite，仅创建持久agent身份，不启动provider或模型。
func newPeerContractFixture(t *testing.T) peerContractFixture {
	t.Helper()
	workspace, state := t.TempDir(), t.TempDir()
	cfg := standalone.Config{SchemaVersion: 1, PrincipalRef: "operator/test", ScopeRef: "workspace/test", WorkspaceRoot: workspace, StateDir: state,
		Listen: "127.0.0.1:0", CredentialFile: filepath.Join(state, "unused-token"),
		Providers: []standalone.Provider{{Name: "codex", Executable: requireExecutable(t), Home: t.TempDir(), Models: []string{"test-model"}, Sandbox: "read-only"}}}
	db := directPeerStandaloneDatabase(t, state)
	persistDirectPeerAgents(t, db, workspace)
	app := startDirectPeerApp(t, cfg, db)
	t.Cleanup(func() { stopStandaloneTestApp(t, app.app) })
	return peerContractFixture{app: app, workspace: workspace}
}

func (f peerContractFixture) call(t *testing.T, tool, agent, workspace string, args map[string]any) (any, error) {
	t.Helper()
	def, ok := f.app.registry.Lookup(tool)
	if !ok {
		t.Fatalf("missing registry tool %s", tool)
	}
	return def.Handler(mcpcommon.WithToolScope(t.Context(), mcpcommon.ToolScope{CWD: workspace, AgentID: agent}), mustJSON(t, args))
}

func (f peerContractFixture) grant(t *testing.T, key string, send, read bool) directpeer.Grant {
	t.Helper()
	result, err := f.call(t, "manage_direct_peer", "", f.workspace, map[string]any{"action": "grant", "sender_agent_id": "sender", "receiver_agent_id": "receiver", "idempotency_key": key, "can_send": send, "can_read": read})
	if err != nil {
		t.Fatal(err)
	}
	var grant directpeer.Grant
	if err := json.Unmarshal(mustJSON(t, result), &grant); err != nil {
		t.Fatal(err)
	}
	return grant
}

func (f peerContractFixture) rows(t *testing.T, tool, agent, workspace string, cursor int64, limit int) []map[string]json.RawMessage {
	t.Helper()
	result, err := f.call(t, tool, agent, workspace, map[string]any{"after_cursor": cursor, "limit": limit})
	if err != nil {
		t.Fatal(err)
	}
	var rows []map[string]json.RawMessage
	if err := json.Unmarshal(mustJSON(t, result), &rows); err != nil {
		t.Fatal(err)
	}
	return rows
}

// TestStandaloneDirectPeerCursorContract 验证registry读端与真实SQL的exclusive分页、稳定顺序及输入边界。
func TestStandaloneDirectPeerCursorContract(t *testing.T) {
	f := newPeerContractFixture(t)
	f.grant(t, "cursor-grant", true, true)
	for i := 0; i < 3; i++ {
		sendDirectPeerThroughStore(t, f.app, f.workspace, "sender", "receiver", fmt.Sprintf("page-%d", i), "persisted intent")
	}
	for _, tool := range []string{"read_direct_peer", "read_direct_peer_events"} {
		t.Run(tool, func(t *testing.T) { assertPeerContractPages(t, f, tool) })
	}
}

func assertPeerContractPages(t *testing.T, f peerContractFixture, tool string) {
	t.Helper()
	all := f.rows(t, tool, "receiver", f.workspace, 0, 200)
	if len(all) < 3 {
		t.Fatalf("insufficient persisted rows: %d", len(all))
	}
	var cursor int64
	var collected []map[string]json.RawMessage
	for range all {
		page := f.rows(t, tool, "receiver", f.workspace, cursor, 1)
		if len(page) != 1 {
			t.Fatalf("limit=1 returned %d rows", len(page))
		}
		var next int64
		if err := json.Unmarshal(page[0]["cursor"], &next); err != nil {
			t.Fatal(err)
		}
		if next <= cursor {
			t.Fatalf("cursor did not advance: %d -> %d", cursor, next)
		}
		cursor = next
		collected = append(collected, page[0])
	}
	if !reflect.DeepEqual(all, collected) {
		t.Fatal("exclusive pages lost, duplicated or reordered rows")
	}
	if len(f.rows(t, tool, "receiver", f.workspace, cursor, 1)) != 0 {
		t.Fatal("exclusive tail repeated final row")
	}
	assertPeerContractReadBounds(t, f, tool)
}

func assertPeerContractReadBounds(t *testing.T, f peerContractFixture, tool string) {
	t.Helper()
	for _, args := range []map[string]any{{"after_cursor": -1, "limit": 1}, {"after_cursor": 0, "limit": -1}, {"after_cursor": 0, "limit": 201}} {
		if _, err := f.call(t, tool, "receiver", f.workspace, args); err == nil {
			t.Fatalf("invalid bounds accepted: %v", args)
		}
	}
	// 当前公开工具将省略或0 limit定义为50，守卫不能假设0非法。
	defaults := f.rows(t, tool, "receiver", f.workspace, 0, 0)
	explicit := f.rows(t, tool, "receiver", f.workspace, 0, 50)
	if !reflect.DeepEqual(defaults, explicit) {
		t.Fatal("documented default page differs from limit=50")
	}
}

// TestStandaloneDirectPeerScopeAndPermissions 验证方向、读写能力和可信scope边界，不启动provider。
func TestStandaloneDirectPeerScopeAndPermissions(t *testing.T) {
	t.Run("send-only", func(t *testing.T) {
		f := newPeerContractFixture(t)
		f.grant(t, "send-only", true, false)
		sendDirectPeerThroughStore(t, f.app, f.workspace, "sender", "receiver", "intent", "durable")
		assertPeerContractEmptyReads(t, f, "receiver", f.workspace)
		assertPeerContractSendDenied(t, f, "receiver", f.workspace, "sender")
	})
	t.Run("read-only", func(t *testing.T) {
		f := newPeerContractFixture(t)
		f.grant(t, "read-only", false, true)
		assertPeerContractSendDenied(t, f, "sender", f.workspace, "receiver")
		if len(f.rows(t, "read_direct_peer_events", "receiver", f.workspace, 0, 50)) != 1 {
			t.Fatal("read-only receiver cannot observe grant")
		}
	})
	t.Run("scope", func(t *testing.T) { assertPeerContractScopes(t, newPeerContractFixture(t)) })
}

func assertPeerContractEmptyReads(t *testing.T, f peerContractFixture, agent, workspace string) {
	t.Helper()
	for _, tool := range []string{"read_direct_peer", "read_direct_peer_events"} {
		if len(f.rows(t, tool, agent, workspace, 0, 50)) != 0 {
			t.Fatalf("%s leaked unauthorized records", tool)
		}
	}
}

func assertPeerContractSendDenied(t *testing.T, f peerContractFixture, agent, workspace, receiver string) {
	t.Helper()
	_, err := f.call(t, "send_direct_peer", agent, workspace, map[string]any{"receiver_agent_id": receiver, "idempotency_key": "denied", "message": "must not submit"})
	if !errors.Is(err, directpeer.ErrUnauthorized) {
		t.Fatalf("want permission denial, got %v", err)
	}
}

func assertPeerContractScopes(t *testing.T, f peerContractFixture) {
	t.Helper()
	f.grant(t, "scope-grant", true, true)
	sendDirectPeerThroughStore(t, f.app, f.workspace, "sender", "receiver", "scope-intent", "private workspace message")
	other := t.TempDir()
	assertPeerContractEmptyReads(t, f, "receiver", other)
	assertPeerContractEmptyReads(t, f, "sender", f.workspace)
	assertPeerContractSendDenied(t, f, "sender", other, "receiver")
	_, err := f.call(t, "send_direct_peer", "", f.workspace, map[string]any{"receiver_agent_id": "receiver", "idempotency_key": "operator", "message": "must not submit"})
	if err == nil || !strings.Contains(err.Error(), "trusted workspace and agent scope") {
		t.Fatalf("operator must not impersonate sender: %v", err)
	}
	args := map[string]any{"action": "grant", "sender_agent_id": "sender", "receiver_agent_id": "receiver", "idempotency_key": "bad-grant", "can_send": true, "can_read": true}
	_, err = f.call(t, "manage_direct_peer", "sender", f.workspace, args)
	if err == nil || !strings.Contains(err.Error(), "operator scope") {
		t.Fatalf("agent granted itself permission: %v", err)
	}
	_, err = f.call(t, "manage_direct_peer", "", other, args)
	if err == nil || !strings.Contains(err.Error(), "operator workspace") {
		t.Fatalf("cross-workspace grant accepted: %v", err)
	}
}

// TestStandaloneDirectPeerUnknownRevisionContract 使用真实持久accepted意图模拟确认缺口，重试不调用provider并保留旧代际隔离。
func TestStandaloneDirectPeerUnknownRevisionContract(t *testing.T) {
	f := newPeerContractFixture(t)
	old := f.grant(t, "original-grant", true, true)
	messageID := sendDirectPeerThroughStore(t, f.app, f.workspace, "sender", "receiver", "unknown-key", "same durable body")
	assertPeerContractUnknownReplay(t, f, "unknown-key", "same durable body")
	_, err := f.call(t, "send_direct_peer", "sender", f.workspace, map[string]any{"receiver_agent_id": "receiver", "idempotency_key": "unknown-key", "message": "different body"})
	if !errors.Is(err, directpeer.ErrIdempotencyConflict) {
		t.Fatalf("changed payload must conflict: %v", err)
	}
	revokeDirectPeerThroughRegistry(t, f.app, f.workspace)
	assertPeerContractEmptyReads(t, f, "receiver", f.workspace)
	restored := f.grant(t, "restored-grant", true, true)
	if restored.Revision <= old.Revision {
		t.Fatal("restored grant did not advance revision")
	}
	if len(f.rows(t, "read_direct_peer", "receiver", f.workspace, 0, 50)) != 0 {
		t.Fatal("old grant revision message leaked after regrant")
	}
	_, err = f.call(t, "send_direct_peer", "sender", f.workspace, map[string]any{"receiver_agent_id": "receiver", "idempotency_key": "unknown-key", "message": "same durable body"})
	if !errors.Is(err, directpeer.ErrUnauthorized) {
		t.Fatalf("old revision retry must remain revoked: %v", err)
	}

	fresh := sendDirectPeerThroughStore(t, f.app, f.workspace, "sender", "receiver", "fresh-key", "new revision body")
	rows := f.rows(t, "read_direct_peer", "receiver", f.workspace, 0, 50)
	if len(rows) != 1 || string(rows[0]["message_id"]) != string(mustJSON(t, fresh)) {
		t.Fatal("new revision read did not isolate fresh intent")
	}
	if fresh == messageID {
		t.Fatal("new key reused old message identity")
	}
	assertPeerContractRevisionEvents(t, f, old.Revision, restored.Revision)
}

func assertPeerContractUnknownReplay(t *testing.T, f peerContractFixture, key, body string) {
	t.Helper()
	before := f.rows(t, "read_direct_peer_events", "receiver", f.workspace, 0, 200)
	_, err := f.call(t, "send_direct_peer", "sender", f.workspace, map[string]any{"receiver_agent_id": "receiver", "idempotency_key": key, "message": body})
	var coded *mcpcommon.CodedToolError
	if !errors.As(err, &coded) || coded.Code != "delivery_outcome_unknown" || coded.Retryable {
		t.Fatalf("accepted intent must remain nonretryable unknown: %v", err)
	}
	after := f.rows(t, "read_direct_peer_events", "receiver", f.workspace, 0, 200)
	if !reflect.DeepEqual(before, after) {
		t.Fatal("unknown replay appended delivery evidence")
	}
}

// assertPeerContractRevisionEvents 事件审计保留旧revision但明确标识代际，消息读端则只显示当前revision。
func assertPeerContractRevisionEvents(t *testing.T, f peerContractFixture, old, current int64) {
	t.Helper()
	rows := f.rows(t, "read_direct_peer_events", "receiver", f.workspace, 0, 200)
	seen := map[int64]bool{}
	for _, row := range rows {
		var revision int64
		if err := json.Unmarshal(row["grant_revision"], &revision); err != nil {
			t.Fatal(err)
		}
		seen[revision] = true
	}
	if !seen[old] || !seen[current] {
		t.Fatalf("audit history lost explicit grant revisions: %v", seen)
	}
}
