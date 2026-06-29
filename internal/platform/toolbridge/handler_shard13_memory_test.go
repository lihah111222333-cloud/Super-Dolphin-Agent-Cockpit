package toolbridge

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/mcp"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/mcpcontrol"
	"github.com/anthropic-ai/super-agent-v3/internal/provider/codexapp"
)

func TestProxyToolCall_MemoryReadUsesHostDirect(t *testing.T) {
	reader := &stubAgentMemoryReader{enabled: true, toolsEnabled: true}
	host := NewMemoryReadHostToolRegistry(reader, MemoryReadHostToolOptions{Enabled: true, ToolsEnabled: true})
	registry := &stubKindRegistry{}
	cwd := t.TempDir()
	h := &Handler{registry: registry, hostTools: host, resolver: &stubCWDResolver{cwd: cwd}, proxyAuthToken: newProxyAuthToken()}
	body := string(mustRawJSON(t, map[string]any{"jsonrpc": "2.0", "id": "read-1", "method": "tools/call", "params": map[string]any{"name": ToolNameMemoryRead, "arguments": map[string]any{"name": "daily", "scope": "user"}}}))

	got := callProxyRequest(t, h, "/mcp/orch/agent-read", body)
	if got.Error != nil {
		t.Fatalf("proxy tools/call error = %+v", got.Error)
	}
	result := got.Result.(map[string]any)
	if result["isError"] == true || reader.calls != 1 {
		t.Fatalf("result=%#v reader.calls=%d, want success host call", result, reader.calls)
	}
	if reader.last.AgentID != "agent-read" || reader.last.CWD != cwd || reader.last.CallID != "read-1" {
		t.Fatalf("reader request = %+v", reader.last)
	}
	if len(registry.gotKinds) != 0 {
		t.Fatalf("FindActiveByKind() kinds = %#v, want none", registry.gotKinds)
	}
}

func TestProxyToolsList_HidesMemoryReadWhenToolsDisabled(t *testing.T) {
	reader := &stubAgentMemoryReader{enabled: true, toolsEnabled: true}
	host := NewMemoryReadHostToolRegistry(reader, MemoryReadHostToolOptions{Enabled: true, ToolsEnabled: false})
	h := &Handler{hostTools: host, registry: &stubKindRegistry{peers: map[string][]*mcpcontrol.ToolInstance{dto.ClientKindOrch: {listToolsPeer([]dto.MCPTool{{Name: "orchestration_launch_agent", Description: "peer orch"}}, nil)}}}, proxyAuthToken: newProxyAuthToken()}

	got := callProxyRequest(t, h, "/mcp/orch/agent-1", `{"jsonrpc":"2.0","id":"req-1","method":"tools/list"}`)
	if got.Error != nil {
		t.Fatalf("proxy tools/list error = %+v", got.Error)
	}
	result := got.Result.(map[string]any)
	tools := result["tools"].([]any)
	if proxyToolsContainName(tools, ToolNameMemoryRead) {
		t.Fatalf("tools = %#v, want memory_read hidden", tools)
	}
	if !proxyToolsContainName(tools, "orchestration_launch_agent") {
		t.Fatalf("tools = %#v, want non-memory peer tool preserved", tools)
	}
}

func TestProxyToolCall_MemoryReadToolsDisabledDoesNotFallbackToPeer(t *testing.T) {
	reader := &stubAgentMemoryReader{enabled: true, toolsEnabled: true}
	host := NewMemoryReadHostToolRegistry(reader, MemoryReadHostToolOptions{Enabled: true, ToolsEnabled: false})
	registry := &stubKindRegistry{peers: map[string][]*mcpcontrol.ToolInstance{
		dto.ClientKindOrch: {
			{Peer: &stubPeer{callbackFn: func(_ context.Context, method string, params any, result any) error {
				t.Fatalf("peer callback called for %s with params %#v", method, params)
				return nil
			}}},
		},
	}}
	h := &Handler{hostTools: host, registry: registry, resolver: &stubCWDResolver{cwd: t.TempDir()}, proxyAuthToken: newProxyAuthToken()}
	body := string(mustRawJSON(t, map[string]any{"jsonrpc": "2.0", "id": "read-tools-off-no-peer", "method": "tools/call", "params": map[string]any{"name": ToolNameMemoryRead, "arguments": map[string]any{"name": "daily"}}}))

	got := callProxyRequest(t, h, "/mcp/orch/agent-read", body)

	assertProxyToolErrorEnvelope(t, got, ToolNameMemoryRead, "tools_disabled")
	if reader.calls != 0 || len(registry.gotKinds) != 0 {
		t.Fatalf("reader.calls=%d registry kinds=%#v, want no reader or peer", reader.calls, registry.gotKinds)
	}
}

func TestProxyToolCall_MemoryReadFeatureDisabledDoesNotFallbackToPeer(t *testing.T) {
	reader := &stubAgentMemoryReader{enabled: true, toolsEnabled: true}
	host := NewMemoryReadHostToolRegistry(reader, MemoryReadHostToolOptions{Enabled: false, ToolsEnabled: true})
	registry := &stubKindRegistry{peers: map[string][]*mcpcontrol.ToolInstance{
		dto.ClientKindOrch: {
			{Peer: &stubPeer{callbackFn: func(_ context.Context, method string, params any, result any) error {
				t.Fatalf("peer callback called for %s with params %#v", method, params)
				return nil
			}}},
		},
	}}
	h := &Handler{hostTools: host, registry: registry, resolver: &stubCWDResolver{cwd: t.TempDir()}, proxyAuthToken: newProxyAuthToken()}
	body := string(mustRawJSON(t, map[string]any{"jsonrpc": "2.0", "id": "read-feature-off-no-peer", "method": "tools/call", "params": map[string]any{"name": ToolNameMemoryRead, "arguments": map[string]any{"name": "daily"}}}))

	got := callProxyRequest(t, h, "/mcp/orch/agent-read", body)

	assertProxyToolErrorEnvelope(t, got, ToolNameMemoryRead, "feature_disabled")
	if reader.calls != 0 || len(registry.gotKinds) != 0 {
		t.Fatalf("reader.calls=%d registry kinds=%#v, want no reader or peer", reader.calls, registry.gotKinds)
	}
}

func TestProxyToolCall_MemoryReadReaderErrorDoesNotFallbackToPeer(t *testing.T) {
	reader := &stubAgentMemoryReader{enabled: true, toolsEnabled: true, err: contract.NewAgentMemoryError("not_visible", errors.New("memory not visible"))}
	host := NewMemoryReadHostToolRegistry(reader, MemoryReadHostToolOptions{Enabled: true, ToolsEnabled: true})
	registry := &stubKindRegistry{peers: map[string][]*mcpcontrol.ToolInstance{
		dto.ClientKindOrch: {
			{Peer: &stubPeer{callbackFn: func(_ context.Context, method string, params any, result any) error {
				t.Fatalf("peer callback called for %s with params %#v", method, params)
				return nil
			}}},
		},
	}}
	h := &Handler{hostTools: host, registry: registry, resolver: &stubCWDResolver{cwd: t.TempDir()}, proxyAuthToken: newProxyAuthToken()}
	body := string(mustRawJSON(t, map[string]any{"jsonrpc": "2.0", "id": "read-not-visible-no-peer", "method": "tools/call", "params": map[string]any{"name": ToolNameMemoryRead, "arguments": map[string]any{"name": "private"}}}))

	got := callProxyRequest(t, h, "/mcp/orch/agent-read", body)

	assertProxyToolErrorEnvelope(t, got, ToolNameMemoryRead, "not_visible")
	if reader.calls != 1 || len(registry.gotKinds) != 0 {
		t.Fatalf("reader.calls=%d registry kinds=%#v, want one reader call and no peer", reader.calls, registry.gotKinds)
	}
}

func TestProxyToolCall_MemoryReadToolsDisabledReturnsToolErrorEnvelope(t *testing.T) {
	reader := &stubAgentMemoryReader{enabled: true, toolsEnabled: true}
	host := NewMemoryReadHostToolRegistry(reader, MemoryReadHostToolOptions{Enabled: true, ToolsEnabled: false})
	h := &Handler{hostTools: host, registry: &stubKindRegistry{}, resolver: &stubCWDResolver{cwd: t.TempDir()}, proxyAuthToken: newProxyAuthToken()}
	body := string(mustRawJSON(t, map[string]any{"jsonrpc": "2.0", "id": "read-tools-off", "method": "tools/call", "params": map[string]any{"name": ToolNameMemoryRead, "arguments": map[string]any{"name": "daily"}}}))

	got := callProxyRequest(t, h, "/mcp/orch/agent-read", body)
	assertProxyToolErrorEnvelope(t, got, ToolNameMemoryRead, "tools_disabled")
}

func TestProxyToolCall_MemoryReadFeatureDisabledReturnsToolErrorEnvelope(t *testing.T) {
	reader := &stubAgentMemoryReader{enabled: true, toolsEnabled: true}
	host := NewMemoryReadHostToolRegistry(reader, MemoryReadHostToolOptions{Enabled: false, ToolsEnabled: true})
	h := &Handler{hostTools: host, registry: &stubKindRegistry{}, resolver: &stubCWDResolver{cwd: t.TempDir()}, proxyAuthToken: newProxyAuthToken()}
	body := string(mustRawJSON(t, map[string]any{"jsonrpc": "2.0", "id": "read-feature-off", "method": "tools/call", "params": map[string]any{"name": ToolNameMemoryRead, "arguments": map[string]any{"name": "daily"}}}))

	got := callProxyRequest(t, h, "/mcp/orch/agent-read", body)
	assertProxyToolErrorEnvelope(t, got, ToolNameMemoryRead, "feature_disabled")
}

func TestProxyToolCall_StaleMemoryReadCallReturnsStableToolError(t *testing.T) {
	h := &Handler{proxyAuthToken: newProxyAuthToken()}
	body := string(mustRawJSON(t, map[string]any{"jsonrpc": "2.0", "id": "read-stale", "method": "tools/call", "params": map[string]any{"name": ToolNameMemoryRead, "arguments": map[string]any{"name": "daily"}}}))

	got := callProxyRequest(t, h, "/mcp/orch/agent-read", body)
	assertProxyToolResultEnvelope(t, got, ToolNameMemoryRead, "reader_unavailable")
}

func TestProxyToolCall_MemoryReadReaderErrorUsesToolErrorNotJSONRPCError(t *testing.T) {
	reader := &stubAgentMemoryReader{enabled: true, toolsEnabled: true, err: contract.NewAgentMemoryError("not_found", errors.New("missing"))}
	host := NewMemoryReadHostToolRegistry(reader, MemoryReadHostToolOptions{Enabled: true, ToolsEnabled: true})
	h := &Handler{hostTools: host, registry: &stubKindRegistry{}, resolver: &stubCWDResolver{cwd: t.TempDir()}, proxyAuthToken: newProxyAuthToken()}
	body := string(mustRawJSON(t, map[string]any{"jsonrpc": "2.0", "id": "read-missing", "method": "tools/call", "params": map[string]any{"name": ToolNameMemoryRead, "arguments": map[string]any{"name": "missing"}}}))

	got := callProxyRequest(t, h, "/mcp/orch/agent-read", body)
	assertProxyToolErrorEnvelope(t, got, ToolNameMemoryRead, "not_found")
}

func TestProxyToolCall_MemoryReadUnsupportedScopeReturnsToolErrorEnvelope(t *testing.T) {
	reader := &stubAgentMemoryReader{enabled: true, toolsEnabled: true, err: contract.NewAgentMemoryError("unsupported_scope", errors.New("unsupported"))}
	host := NewMemoryReadHostToolRegistry(reader, MemoryReadHostToolOptions{Enabled: true, ToolsEnabled: true})
	h := &Handler{hostTools: host, registry: &stubKindRegistry{}, resolver: &stubCWDResolver{cwd: t.TempDir()}, proxyAuthToken: newProxyAuthToken()}
	body := string(mustRawJSON(t, map[string]any{"jsonrpc": "2.0", "id": "read-unsupported", "method": "tools/call", "params": map[string]any{"name": ToolNameMemoryRead, "arguments": map[string]any{"name": "x", "scope": "project"}}}))

	got := callProxyRequest(t, h, "/mcp/orch/agent-read", body)
	assertProxyToolErrorEnvelope(t, got, ToolNameMemoryRead, "unsupported_scope")
}

func TestProxyToolCall_MemoryReadReaderUnavailableReturnsToolErrorEnvelope(t *testing.T) {
	host := &MemoryReadHostToolRegistry{opts: MemoryReadHostToolOptions{Enabled: true, ToolsEnabled: true}}
	registry := &stubKindRegistry{}
	h := &Handler{registry: registry, hostTools: host, resolver: &stubCWDResolver{cwd: t.TempDir()}, proxyAuthToken: newProxyAuthToken()}
	body := string(mustRawJSON(t, map[string]any{"jsonrpc": "2.0", "id": "read-err", "method": "tools/call", "params": map[string]any{"name": ToolNameMemoryRead, "arguments": map[string]any{"name": "daily"}}}))

	got := callProxyRequest(t, h, "/mcp/orch/agent-read", body)
	assertProxyToolErrorEnvelope(t, got, ToolNameMemoryRead, "reader_unavailable")
	if len(registry.gotKinds) != 0 {
		t.Fatalf("FindActiveByKind() kinds = %#v, want none", registry.gotKinds)
	}
}

func TestProxyToolCall_MemoryReadMalformedInputReturnsToolErrorEnvelope(t *testing.T) {
	reader := &stubAgentMemoryReader{enabled: true, toolsEnabled: true}
	host := NewMemoryReadHostToolRegistry(reader, MemoryReadHostToolOptions{Enabled: true, ToolsEnabled: true})
	registry := &stubKindRegistry{}
	h := &Handler{registry: registry, hostTools: host, resolver: &stubCWDResolver{cwd: t.TempDir()}, proxyAuthToken: newProxyAuthToken()}
	body := string(mustRawJSON(t, map[string]any{"jsonrpc": "2.0", "id": "read-bad", "method": "tools/call", "params": map[string]any{"name": ToolNameMemoryRead, "arguments": "not-object"}}))

	got := callProxyRequest(t, h, "/mcp/orch/agent-read", body)
	assertProxyToolErrorEnvelope(t, got, ToolNameMemoryRead, "invalid_input")
	if reader.calls != 0 || len(registry.gotKinds) != 0 {
		t.Fatalf("reader.calls=%d registry kinds=%#v, want no reader or peer", reader.calls, registry.gotKinds)
	}
}

func assertProxyToolErrorEnvelope(t *testing.T, got proxyJSONRPCResponse, toolName, code string) {
	t.Helper()
	assertProxyToolResultEnvelope(t, got, toolName, code)
}

func assertProxyToolResultEnvelope(t *testing.T, got proxyJSONRPCResponse, toolName, code string) {
	t.Helper()
	assertProxyResultNoJSONRPCError(t, got)
	result := requireProxyResultMap(t, got)
	assertToolErrorResult(t, result)
	envelope := requireToolErrorEnvelope(t, result)
	assertToolErrorEnvelopeFields(t, envelope, toolName, code)
}

func assertProxyResultNoJSONRPCError(t *testing.T, got proxyJSONRPCResponse) {
	t.Helper()
	if got.Error != nil {
		t.Fatalf("proxy response error = %+v, want JSON-RPC result", got.Error)
	}
}

func requireProxyResultMap(t *testing.T, got proxyJSONRPCResponse) map[string]any {
	t.Helper()
	result, ok := got.Result.(map[string]any)
	if !ok {
		t.Fatalf("result type = %T, want object", got.Result)
	}
	return result
}

func assertToolErrorResult(t *testing.T, result map[string]any) {
	t.Helper()
	if isError, _ := result["isError"].(bool); !isError {
		t.Fatalf("isError = %v, want true (result=%#v)", result["isError"], result)
	}
}

func requireToolErrorEnvelope(t *testing.T, result map[string]any) map[string]any {
	t.Helper()
	content, ok := result["content"].([]any)
	if !ok || len(content) == 0 {
		t.Fatalf("content = %#v, want text envelope", result["content"])
	}
	text, _ := content[0].(map[string]any)["text"].(string)
	var envelope map[string]any
	if err := json.Unmarshal([]byte(text), &envelope); err != nil {
		t.Fatalf("content text json error = %v text=%q", err, text)
	}
	return envelope
}

func assertToolErrorEnvelopeFields(t *testing.T, envelope map[string]any, toolName, code string) {
	t.Helper()
	if envelope["kind"] != "host_tool_error" || envelope["tool"] != toolName || envelope["code"] != code {
		t.Fatalf("envelope = %#v, want tool=%q code=%q", envelope, toolName, code)
	}
	if envelope["success"] != false || envelope["hint"] == "" {
		t.Fatalf("envelope = %#v, want success=false and non-empty hint", envelope)
	}
	meta, ok := envelope["meta"].(map[string]any)
	if !ok || meta["tool"] != toolName || meta["kind"] != "host_tool_error" {
		t.Fatalf("envelope meta = %#v, want tool=%q kind=host_tool_error", envelope["meta"], toolName)
	}
	if strings.TrimSpace(fmt.Sprint(envelope["error"])) == "" {
		t.Fatalf("envelope missing non-empty error: %#v", envelope)
	}
}

func TestProxyToolCall_MemoryWriteUsesHostDirect(t *testing.T) {
	writer := &stubAgentMemoryWriter{}
	host := NewMemoryWriteHostToolRegistry(writer, MemoryWriteHostToolOptions{Enabled: true, ToolsEnabled: true})
	cwd := t.TempDir()
	resolver := &stubCWDResolver{cwd: cwd}
	registry := &stubKindRegistry{}
	h := &Handler{registry: registry, hostTools: host, resolver: resolver, proxyAuthToken: newProxyAuthToken()}

	body := string(mustRawJSON(t, map[string]any{
		"jsonrpc": "2.0",
		"id":      "req-memory",
		"method":  "tools/call",
		"params": map[string]any{
			"name": ToolNameMemoryWrite,
			"arguments": map[string]any{
				"name":        "agent-memory-visible",
				"description": "Agent memory visible in center",
				"content":     "Agents can write durable memory.\nWhy: user asked to verify agent write path.\nHow to apply: surface it in memory center.",
				"type":        "feedback",
			},
		},
	}))

	got := callProxyRequest(t, h, "/mcp/orch/agent-claude", body)
	assertMemoryWriteProxySuccess(t, got)
	assertMemoryWriteHostCall(t, writer, cwd)
	if len(registry.gotKinds) != 0 {
		t.Fatalf("FindActiveByKind() kinds = %#v, want none", registry.gotKinds)
	}
}

func assertMemoryWriteProxySuccess(t *testing.T, got proxyJSONRPCResponse) {
	t.Helper()
	if got.Error != nil {
		t.Fatalf("proxy tools/call error = %+v", got.Error)
	}
	result, ok := got.Result.(map[string]any)
	if !ok {
		t.Fatalf("result type = %T, want object", got.Result)
	}
	if isError, _ := result["isError"].(bool); isError {
		t.Fatalf("result isError = true: %#v", result)
	}
}

func assertMemoryWriteHostCall(t *testing.T, writer *stubAgentMemoryWriter, wantCWD string) {
	t.Helper()
	if writer.calls != 1 {
		t.Fatalf("writer calls = %d, want 1", writer.calls)
	}
	assertMemoryWriteRequest(t, writer.last, wantCWD)
}

func assertMemoryWriteRequest(t *testing.T, got contract.AgentMemoryWriteRequest, wantCWD string) {
	t.Helper()
	if got.Name != "agent-memory-visible" || got.AgentID != "agent-claude" || got.CWD != wantCWD || got.CallID != "req-memory" || got.Source != "agent_tool" {
		t.Fatalf("writer request = %+v", got)
	}
}

func TestProxyToolCall_RejectsInvalidParams(t *testing.T) {
	h, registry := newHandlerForTest()
	tests := []struct {
		name    string
		body    string
		wantMsg string
	}{
		{
			name:    "null params",
			body:    `{"jsonrpc":"2.0","id":"req-1","method":"tools/call","params":null}`,
			wantMsg: "tool call params must be a non-null object",
		},
		{
			name:    "missing params",
			body:    `{"jsonrpc":"2.0","id":"req-1","method":"tools/call"}`,
			wantMsg: "tool call params must be a non-null object",
		},
		{
			name: "blank tool name",
			body: string(mustRawJSON(t, map[string]any{
				"jsonrpc": "2.0",
				"id":      "req-1",
				"method":  "tools/call",
				"params": map[string]any{
					"name":      "   ",
					"arguments": map[string]any{},
				},
			})),
			wantMsg: "tool name is required",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := callProxyRequest(t, h, "/mcp/lsp/agent-1", tt.body)
			if got.Error == nil {
				t.Fatal("proxy response error = nil, want invalid params")
			}
			if got.Error.Code != jsonRPCCodeInvalidParam {
				t.Fatalf("proxy error code = %d, want %d", got.Error.Code, jsonRPCCodeInvalidParam)
			}
			if !strings.Contains(got.Error.Message, tt.wantMsg) {
				t.Fatalf("proxy error message = %q, want substring %q", got.Error.Message, tt.wantMsg)
			}
		})
	}
	if len(registry.gotKinds) != 0 {
		t.Fatalf("FindActiveByKind() kinds = %#v, want none", registry.gotKinds)
	}
}

func TestOnInboundMessage_NonToolRequest_WithID_NotIntercepted(t *testing.T) {
	sessionPtr := newInboundSession(t)
	resp := &stubResponder{}
	msg := codexapp.RawMessage{ID: json.RawMessage(`"req-1"`), Method: "unknown/method", Params: json.RawMessage(`{"ok":true}`)}

	codexSessionOnInboundMessage(sessionPtr, context.Background(), resp, msg)
	if resp.calls != 1 {
		t.Fatalf("RespondWithID() calls = %d, want 1", resp.calls)
	}
	if string(resp.id) != `"req-1"` {
		t.Fatalf("RespondWithID() id = %s, want \"req-1\"", string(resp.id))
	}
	if resp.result != nil {
		t.Fatalf("RespondWithID() result = %#v, want nil", resp.result)
	}
	if resp.callErr == nil || !strings.Contains(resp.callErr.Error(), "method not supported: unknown/method") {
		t.Fatalf("RespondWithID() error = %v, want method not supported", resp.callErr)
	}
}
