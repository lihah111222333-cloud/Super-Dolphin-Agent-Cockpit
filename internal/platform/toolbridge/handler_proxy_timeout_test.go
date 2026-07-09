package toolbridge

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/anthropic-ai/super-agent-v3/internal/platform/mcpcontrol"
)

func TestProxyToolCall_SetsTimeoutAndNormalizesNullArguments(t *testing.T) {
	var deadline time.Time
	h, _ := newHandlerForTest(&mcpcontrol.ToolInstance{Peer: &stubPeer{callbackFn: func(ctx context.Context, method string, params any, result any) error {
		var ok bool
		deadline, ok = ctx.Deadline()
		if !ok {
			t.Fatal("Callback() context missing deadline")
		}
		assertToolCallPayload(t, params, "inspect", json.RawMessage(`{}`))
		resp, ok := result.(*peerToolCallResponse)
		if !ok {
			t.Fatalf("Callback() result type = %T, want *peerToolCallResponse", result)
		}
		*resp = peerToolCallResponse{Content: []peerToolCallContent{{Type: "text", Text: "ok"}}}
		return nil
	}}})
	body := string(mustRawJSON(t, map[string]any{
		"jsonrpc": "2.0",
		"id":      "req-1",
		"method":  "tools/call",
		"params": map[string]any{
			"name":      "inspect",
			"arguments": nil,
		},
	}))

	got := callProxyRequest(t, h, "/mcp/lsp/agent-1", body)
	if got.Error != nil {
		t.Fatalf("proxy response error = %#v, want nil", got.Error)
	}
	if deadline.IsZero() {
		t.Fatal("Callback() deadline was not recorded")
	}
	if remaining := time.Until(deadline); remaining <= 0 || remaining > proxyToolCallTimeout+time.Second {
		t.Fatalf("Callback() deadline remaining = %s, want within (0,%s]", remaining, proxyToolCallTimeout+time.Second)
	}
}

func TestProxyToolCall_RejectsFamilyMismatch(t *testing.T) {
	h, registry := newHandlerForTest(newToolCallPeer(t, "spawn_agent", json.RawMessage(`{}`), "ignored", nil))
	body := string(mustRawJSON(t, map[string]any{
		"jsonrpc": "2.0",
		"id":      "req-1",
		"method":  "tools/call",
		"params": map[string]any{
			"name":      "spawn_agent",
			"arguments": map[string]any{},
		},
	}))

	got := callProxyRequest(t, h, "/mcp/lsp/agent-1", body)
	if got.Error == nil {
		t.Fatal("proxy response error = nil, want invalid params")
	}
	if got.Error.Code != jsonRPCCodeInvalidParam {
		t.Fatalf("proxy error code = %d, want %d", got.Error.Code, jsonRPCCodeInvalidParam)
	}
	if len(registry.gotKinds) != 0 {
		t.Fatalf("FindActiveByKind() kinds = %#v, want none", registry.gotKinds)
	}
}
