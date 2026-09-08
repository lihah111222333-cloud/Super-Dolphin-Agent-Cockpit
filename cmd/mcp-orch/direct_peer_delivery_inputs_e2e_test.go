//go:build e2e

package main

import (
	"encoding/json"
	"strings"
	"testing"

	directpeer "github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/directpeer"
	mcpcommon "github.com/lihah111222333-cloud/super-dolphin-agent/internal/mcpserver/common"
)

// TestStandaloneDirectPeerInputContract 使用真实Registry/SQLite验证输入边界；最大正文仅写store，不启动provider。
func TestStandaloneDirectPeerInputContract(t *testing.T) {
	f := newPeerContractFixture(t)
	t.Run("scope", func(t *testing.T) { assertPeerInputScopes(t, f) })
	t.Run("manage", func(t *testing.T) { assertPeerManageInputs(t, f) })
	f.grant(t, "valid-input-grant", true, true)
	t.Run("message-bytes", func(t *testing.T) { assertPeerMessageBytes(t, f) })
	t.Run("read-limit", func(t *testing.T) {
		for _, tool := range []string{"read_direct_peer", "read_direct_peer_events"} {
			f.rows(t, tool, "receiver", f.workspace, 0, 200)
			if _, err := f.call(t, tool, "receiver", f.workspace, map[string]any{"limit": 201}); err == nil {
				t.Fatalf("%s accepted limit201", tool)
			}
		}
	})
}

func assertPeerInputScopes(t *testing.T, f peerContractFixture) {
	t.Helper()
	for _, tool := range []string{"manage_direct_peer", "send_direct_peer", "read_direct_peer", "read_direct_peer_events"} {
		def, ok := f.app.registry.Lookup(tool)
		if !ok {
			t.Fatal("missing tool " + tool)
		}
		if _, err := def.Handler(t.Context(), json.RawMessage("{}")); err == nil {
			t.Fatalf("%s accepted absent trusted scope", tool)
		}
		if _, err := def.Handler(mcpcommon.WithToolScope(t.Context(), mcpcommon.ToolScope{AgentID: "sender"}), json.RawMessage("{}")); err == nil {
			t.Fatalf("%s accepted empty cwd", tool)
		}
	}
	for _, tool := range []string{"send_direct_peer", "read_direct_peer", "read_direct_peer_events"} {
		if _, err := f.call(t, tool, "", f.workspace, map[string]any{}); err == nil {
			t.Fatalf("%s accepted absent agent", tool)
		}
	}
}

func assertPeerManageInputs(t *testing.T, f peerContractFixture) {
	t.Helper()
	for _, tc := range []struct {
		field string
		value any
	}{
		{"action", "unknown"}, {"sender_agent_id", "missing"}, {"receiver_agent_id", "missing"}, {"receiver_agent_id", "sender"}, {"idempotency_key", ""},
	} {
		args := map[string]any{"action": "grant", "sender_agent_id": "sender", "receiver_agent_id": "receiver", "idempotency_key": "invalid", "can_send": true, "can_read": true}
		args[tc.field] = tc.value
		if _, err := f.call(t, "manage_direct_peer", "", f.workspace, args); err == nil {
			t.Fatalf("accepted invalid manage %s=%v", tc.field, tc.value)
		}
	}
	args := map[string]any{"action": "grant", "sender_agent_id": "sender", "receiver_agent_id": "receiver", "idempotency_key": "no-rights", "can_send": false, "can_read": false}
	if _, err := f.call(t, "manage_direct_peer", "", f.workspace, args); err == nil {
		t.Fatal("accepted grant with no rights")
	}
	args["can_send"] = true
	if _, err := f.call(t, "manage_direct_peer", "", ".", args); err == nil {
		t.Fatal("accepted relative workspace")
	}
}

func assertPeerMessageBytes(t *testing.T, f peerContractFixture) {
	t.Helper()
	assertPeerEmptyMessageInputs(t, f)
	body := strings.Repeat("界", (1<<20)/3) + "x"
	if len([]byte(body)) != 1<<20 {
		t.Fatal("UTF8 byte fixture size incorrect")
	}
	request := directpeer.SendRequest{WorkspaceRoot: f.workspace, SenderAgentID: "sender", ReceiverAgentID: "receiver", IdempotencyKey: "byte-limit", Body: body}
	message, err := f.app.store.Send(t.Context(), request)
	if err != nil || message.Status != "accepted" {
		t.Fatalf("exact byte limit rejected: status=%s err=%v", message.Status, err)
	}
	before := f.rows(t, "read_direct_peer_events", "receiver", f.workspace, 0, 200)
	request.IdempotencyKey = "too-large"
	request.Body += "x"
	if _, err := f.app.store.Send(t.Context(), request); err == nil {
		t.Fatal("accepted byte limit+1")
	}
	_, err = f.call(t, "send_direct_peer", "sender", f.workspace, map[string]any{"receiver_agent_id": "receiver", "idempotency_key": "too-large-registry", "message": request.Body})
	if err == nil || !strings.Contains(err.Error(), "exceeds 1 MiB") {
		t.Fatalf("registry oversized body error=%v", err)
	}
	after := f.rows(t, "read_direct_peer_events", "receiver", f.workspace, 0, 200)
	if string(mustJSON(t, before)) != string(mustJSON(t, after)) {
		t.Fatal("oversized message appended events")
	}
	assertPeerExactBodyPersisted(t, f, body)
}

// assertPeerEmptyMessageInputs 先验证空输入拒绝且没有持久消息，再进行大正文边界。
func assertPeerEmptyMessageInputs(t *testing.T, f peerContractFixture) {
	t.Helper()
	for _, args := range []map[string]any{
		{"receiver_agent_id": "receiver", "idempotency_key": "", "message": "valid"},
		{"receiver_agent_id": "receiver", "idempotency_key": "blank", "message": " \t\n"},
	} {
		if _, err := f.call(t, "send_direct_peer", "sender", f.workspace, args); err == nil {
			t.Fatal("invalid message accepted")
		}
	}
	if len(f.rows(t, "read_direct_peer", "receiver", f.workspace, 0, 200)) != 0 {
		t.Fatal("invalid message created durable intent")
	}
}

// assertPeerExactBodyPersisted 验证UTF8原文逐字持久化，超限拒绝没有额外intent。
func assertPeerExactBodyPersisted(t *testing.T, f peerContractFixture, body string) {
	t.Helper()
	rows := f.rows(t, "read_direct_peer", "receiver", f.workspace, 0, 200)
	if len(rows) != 1 {
		t.Fatalf("oversized message created intent: rows=%d", len(rows))
	}
	var stored string
	if err := json.Unmarshal(rows[0]["body"], &stored); err != nil {
		t.Fatal(err)
	}
	if stored != body {
		t.Fatal("exact-limit UTF8 body changed in persistence")
	}
}
