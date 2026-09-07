//go:build e2e

package main

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"
)

// TestStandaloneBinaryDirectPeerOperatorBoundary 验证真实 daemon 的 operator session 不能冒充任务发送者；不调用模型。
func TestStandaloneBinaryDirectPeerOperatorBoundary(t *testing.T) {
	binary := buildServeE2EBinary(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	t.Cleanup(cancel)
	fixture := writeServeE2EFixture(t, binary)
	daemon := startServeE2E(t, ctx, binary, fixture)
	client := &http.Client{Timeout: 5 * time.Second}
	session, _ := initializeServeE2E(t, client, daemon.ready.Endpoint, fixture.token)
	assertServeListAgents(t, client, daemon.ready.Endpoint, fixture.token, session, 2)
	cases := []struct {
		name      string
		arguments map[string]any
		metadata  map[string]any
	}{
		{name: "send_direct_peer", arguments: map[string]any{"receiver_agent_id": "receiver", "idempotency_key": "operator-send", "message": "valid nonempty message"}},
		{name: "read_direct_peer", arguments: map[string]any{"after_cursor": 0, "limit": 10}},
		{name: "send_direct_peer", arguments: map[string]any{"receiver_agent_id": "receiver", "idempotency_key": "forged-meta-send", "message": "valid nonempty message"}, metadata: map[string]any{"agent_id": "sender", "cwd": fixture.workspace, "_agentId": "sender", "_cwd": fixture.workspace}},
		{name: "read_direct_peer", arguments: map[string]any{"after_cursor": 0, "limit": 10}, metadata: map[string]any{"agent_id": "receiver", "cwd": fixture.workspace}},
	}
	id := 10
	for _, tc := range cases {
		params := map[string]any{"name": tc.name, "arguments": tc.arguments}
		if tc.metadata != nil {
			params["_meta"] = tc.metadata
		}
		reply := callServeE2E(t, client, daemon.ready.Endpoint, fixture.token, session, id, "tools/call", params)
		assertBinaryOperatorTaskDenied(t, reply, tc.name)
		assertServeListAgents(t, client, daemon.ready.Endpoint, fixture.token, session, id+1)
		id += 2
	}
	// 标准 _meta 不授予身份；历史顶层 scope 字段则在进入 handler 之前明确拒绝。
	for _, forged := range []map[string]any{{"_agentId": "sender"}, {"_cwd": fixture.workspace}} {
		forged["name"], forged["arguments"] = "list_agents", map[string]any{}
		reply := requestServeE2E(t, client, daemon.ready.Endpoint, fixture.token, session, id, "tools/call", forged)
		assertBinaryOperatorMetadataDenied(t, reply, id)
		assertServeListAgents(t, client, daemon.ready.Endpoint, fixture.token, session, id+1)
		id += 2
	}
	if err := stopServeProcessE2E(t, daemon, nil); err != nil {
		t.Fatalf("daemon shutdown: %v", err)
	}
	assertServeEndpointClosed(t, client, daemon.ready.Endpoint)
}

// assertBinaryOperatorTaskDenied 只接受缺少 task attribution 的工具错误，不能把 schema/receiver/grant 错误当作授权证明。
func assertBinaryOperatorTaskDenied(t *testing.T, reply serveE2EReply, tool string) {
	t.Helper()
	var envelope struct {
		IsError bool `json:"isError"`
	}
	if err := json.Unmarshal(reply.Result, &envelope); err != nil {
		t.Fatal(err)
	}
	if !envelope.IsError {
		t.Fatalf("operator %s unexpectedly succeeded: %s", tool, reply.Result)
	}
	for _, fragment := range []string{"requires task attribution", "standalone task capability unavailable"} {
		if !strings.Contains(string(reply.Result), fragment) {
			t.Fatalf("operator %s failed for wrong reason, missing %q: %s", tool, fragment, reply.Result)
		}
	}
}

// assertBinaryOperatorMetadataDenied 锁定 transport 身份边界错误及原请求 ID，不接受任意失败。
func assertBinaryOperatorMetadataDenied(t *testing.T, reply serveE2EHTTPReply, id int) {
	t.Helper()
	if reply.status != http.StatusOK {
		t.Fatalf("metadata rejection HTTP status=%d", reply.status)
	}
	decoded := decodeServeE2E(t, reply)
	var gotID int
	if err := json.Unmarshal(decoded.ID, &gotID); err != nil {
		t.Fatal(err)
	}
	if gotID != id || decoded.JSONRPC != "2.0" {
		t.Fatalf("metadata rejection response identity invalid: %+v", decoded)
	}
	if decoded.Error == nil {
		t.Fatal("client-supplied scope was accepted")
	}
	if decoded.Error.Code != -32602 || decoded.Error.Message != "client metadata cannot supply standalone caller scope" {
		t.Fatalf("metadata failed for wrong reason: %+v", decoded.Error)
	}
}
