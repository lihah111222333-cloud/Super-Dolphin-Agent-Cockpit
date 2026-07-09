package toolbridge

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	mcpdto "github.com/anthropic-ai/super-agent-v3/internal/dto/mcp"
	providerdto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/mcpcontrol"
)

type stubSessionStatusPort struct {
	calls        int
	lastThreadID string
	lastLimit    int
	lastBefore   string
	result       providerdto.ThreadMessagesResult
	err          error
}

func (s *stubSessionStatusPort) ListSessions(context.Context) ([]contract.SessionThreadSummary, error) {
	return nil, nil
}

func (s *stubSessionStatusPort) ReadMessages(_ context.Context, threadID string, limit int, before string) (providerdto.ThreadMessagesResult, error) {
	s.calls++
	s.lastThreadID = threadID
	s.lastLimit = limit
	s.lastBefore = before
	if s.err != nil {
		return providerdto.ThreadMessagesResult{}, s.err
	}
	return s.result, nil
}

func TestHistoryReadHostToolRegistry_ListSchemaAndCall(t *testing.T) {
	status := &stubSessionStatusPort{result: providerdto.ThreadMessagesResult{
		Messages: []providerdto.Message{{
			ID:        7,
			AgentID:   "agent-1",
			Role:      "assistant",
			Content:   "hello",
			Timestamp: time.Unix(10, 0).UTC(),
		}},
		Total:      1,
		HasMore:    true,
		NextBefore: "before-2",
	}}
	reg := NewHistoryReadHostToolRegistry(status)

	tools := reg.ListHostTools()
	assertHistoryReadSchema(t, tools)
	result, err := reg.CallHostTool(context.Background(), HostToolCall{
		Name:      ToolNameHistoryRead,
		Arguments: mustMarshal(t, map[string]any{"scope": "current_thread", "limit": 2, "cursor": " before-1 "}),
		ThreadID:  " thread-1 ",
		AgentID:   "agent-1",
	})
	if err != nil {
		t.Fatalf("CallHostTool() error = %v", err)
	}
	if status.calls != 1 || status.lastThreadID != "thread-1" || status.lastLimit != 2 || status.lastBefore != "before-1" {
		t.Fatalf("ReadMessages calls=%d thread=%q limit=%d before=%q", status.calls, status.lastThreadID, status.lastLimit, status.lastBefore)
	}
	if got, ok := result.(providerdto.ThreadMessagesResult); !ok || got.Total != 1 || got.NextBefore != "before-2" {
		t.Fatalf("result = %#v, want ThreadMessagesResult total=1 nextBefore=before-2", result)
	}
}

func assertHistoryReadSchema(t *testing.T, tools []mcpdto.MCPTool) {
	t.Helper()
	if len(tools) != 1 || tools[0].Name != ToolNameHistoryRead {
		t.Fatalf("ListHostTools() = %+v, want history_read", tools)
	}
	var schema map[string]any
	if err := json.Unmarshal(tools[0].InputSchema, &schema); err != nil {
		t.Fatalf("schema json error = %v", err)
	}
	properties := schema["properties"].(map[string]any)
	for _, key := range []string{"scope", "limit", "cursor"} {
		if _, ok := properties[key]; !ok {
			t.Fatalf("schema properties missing %q: %#v", key, properties)
		}
	}
	if _, hasThreadID := properties["thread_id"]; hasThreadID {
		t.Fatalf("schema properties = %#v, must not expose thread_id", properties)
	}
	if schema["additionalProperties"] != false {
		t.Fatalf("additionalProperties = %#v, want false", schema["additionalProperties"])
	}
}

func TestHistoryReadHostToolRegistry_InvalidInputIsBounded(t *testing.T) {
	status := &stubSessionStatusPort{}
	reg := NewHistoryReadHostToolRegistry(status)
	cases := []struct {
		name string
		args map[string]any
	}{
		{name: "missing scope", args: map[string]any{"limit": 1}},
		{name: "wrong scope", args: map[string]any{"scope": "any_thread", "limit": 1}},
		{name: "missing limit", args: map[string]any{"scope": "current_thread"}},
		{name: "limit too small", args: map[string]any{"scope": "current_thread", "limit": 0}},
		{name: "limit too large", args: map[string]any{"scope": "current_thread", "limit": 51}},
		{name: "empty cursor", args: map[string]any{"scope": "current_thread", "limit": 1, "cursor": "  "}},
		{name: "long cursor", args: map[string]any{"scope": "current_thread", "limit": 1, "cursor": strings.Repeat("x", 513)}},
		{name: "unknown field", args: map[string]any{"scope": "current_thread", "limit": 1, "thread_id": "evil"}},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			_, err := reg.CallHostTool(context.Background(), HostToolCall{
				Name:      ToolNameHistoryRead,
				Arguments: mustMarshal(t, tt.args),
				ThreadID:  "thread-1",
			})
			if code := contract.AgentMemoryErrorCode(err); code != "invalid_input" {
				t.Fatalf("error code = %q, want invalid_input (err=%v)", code, err)
			}
		})
	}
	if status.calls != 0 {
		t.Fatalf("ReadMessages calls = %d, want 0 for invalid input", status.calls)
	}
}

func TestHistoryReadHostToolRegistry_MissingTrustedThreadID(t *testing.T) {
	reg := NewHistoryReadHostToolRegistry(&stubSessionStatusPort{})
	_, err := reg.CallHostTool(context.Background(), HostToolCall{
		Name:      ToolNameHistoryRead,
		Arguments: mustMarshal(t, map[string]any{"scope": "current_thread", "limit": 1}),
	})
	if code := contract.AgentMemoryErrorCode(err); code != "missing_thread_id" {
		t.Fatalf("error code = %q, want missing_thread_id (err=%v)", code, err)
	}
}

func TestHistoryReadHostToolRegistry_DoesNotRequireCWD(t *testing.T) {
	status := &stubSessionStatusPort{}
	host := NewHistoryReadHostToolRegistry(status)
	h := &Handler{hostTools: host, registry: &stubKindRegistry{}}

	got, err := h.routeToolCall(context.Background(), ToolCallRequest{
		Name:      ToolNameHistoryRead,
		Arguments: mustMarshal(t, map[string]any{"scope": "current_thread", "limit": 1}),
		ThreadID:  "thread-1",
	})
	if err != nil {
		t.Fatalf("routeToolCall() error = %v", err)
	}
	if got == nil || !got.Success || status.calls != 1 {
		t.Fatalf("result=%+v calls=%d, want successful history_read without cwd", got, status.calls)
	}
}

func TestProxyToolCall_HistoryReadUsesTrustedThreadID(t *testing.T) {
	status := &stubSessionStatusPort{result: providerdto.ThreadMessagesResult{Total: 1}}
	host := NewHistoryReadHostToolRegistry(status)
	h := &Handler{
		registry:       &stubKindRegistry{},
		hostTools:      host,
		bindingStore:   &toolCallBindingStoreStub{threadID: "thread-proxy"},
		proxyAuthToken: newProxyAuthToken(),
	}
	body := string(mustRawJSON(t, map[string]any{
		"jsonrpc": "2.0",
		"id":      "history-1",
		"method":  "tools/call",
		"params": map[string]any{
			"name":      ToolNameHistoryRead,
			"arguments": map[string]any{"scope": "current_thread", "limit": 3, "cursor": "before-3"},
		},
	}))

	got := callProxyRequest(t, h, "/mcp/orch/agent-history", body)

	assertProxyResultNoJSONRPCError(t, got)
	result := requireProxyResultMap(t, got)
	if isError, _ := result["isError"].(bool); isError {
		t.Fatalf("proxy result = %#v, want success", result)
	}
	if status.calls != 1 || status.lastThreadID != "thread-proxy" || status.lastLimit != 3 || status.lastBefore != "before-3" {
		t.Fatalf("ReadMessages calls=%d thread=%q limit=%d before=%q", status.calls, status.lastThreadID, status.lastLimit, status.lastBefore)
	}
}

func TestCodexToolSurfaceHistoryReadUsesHostAndBlocksPeerShadow(t *testing.T) {
	status := &stubSessionStatusPort{result: providerdto.ThreadMessagesResult{Total: 1}}
	peer := &fakeMCPClient{tools: []mcpdto.MCPTool{{
		Name:        ToolNameHistoryRead,
		Description: "peer history",
		InputSchema: mustRawJSON(t, map[string]any{
			"type":                 "object",
			"additionalProperties": true,
		}),
	}}}
	h := &Handler{
		hostTools:          NewHistoryReadHostToolRegistry(status),
		stdioClientFactory: fakeClientFactory(map[string]mcpClient{"lsp": peer}),
	}

	tools := prepareHistoryReadCodexSurface(t, h)

	assertCodexSurfaceHistoryReadIsHostTool(t, tools)
	result, err := h.HandleToolCall(context.Background(), contract.ToolCallRawMessage{
		Params: historyReadSurfaceCallParams(t, map[string]any{
			"scope":  "current_thread",
			"limit":  4,
			"cursor": "before-4",
		}),
	})
	if err != nil {
		t.Fatalf("HandleToolCall() error = %v", err)
	}
	assertHistoryReadSurfaceResult(t, result, status, peer, "provider-thread-1")
	if status.lastLimit != 4 || status.lastBefore != "before-4" {
		t.Fatalf("ReadMessages limit=%d before=%q, want limit=4 before=before-4", status.lastLimit, status.lastBefore)
	}
}

func TestCodexToolSurfaceHistoryReadRejectsThreadIDArgumentBeforeHostOrPeer(t *testing.T) {
	status := &stubSessionStatusPort{}
	peer := &fakeMCPClient{tools: []mcpdto.MCPTool{{
		Name:        ToolNameHistoryRead,
		Description: "peer history",
		InputSchema: mustRawJSON(t, map[string]any{
			"type":                 "object",
			"additionalProperties": true,
		}),
	}}}
	h := &Handler{
		hostTools:          NewHistoryReadHostToolRegistry(status),
		stdioClientFactory: fakeClientFactory(map[string]mcpClient{"lsp": peer}),
	}
	prepareHistoryReadCodexSurface(t, h)

	_, err := h.HandleToolCall(context.Background(), contract.ToolCallRawMessage{
		Params: historyReadSurfaceCallParams(t, map[string]any{
			"scope":     "current_thread",
			"limit":     1,
			"thread_id": "attacker-thread",
		}),
	})
	if err == nil || !strings.Contains(err.Error(), "thread_id") {
		t.Fatalf("HandleToolCall() error = %v, want schema rejection mentioning thread_id", err)
	}
	if status.calls != 0 {
		t.Fatalf("ReadMessages calls = %d, want 0 for invalid surface arguments", status.calls)
	}
	if len(peer.calls) != 0 {
		t.Fatalf("peer calls = %#v, want no peer fallback for invalid history_read", peer.calls)
	}
}

func prepareHistoryReadCodexSurface(t *testing.T, h *Handler) []contract.DynamicToolSchema {
	t.Helper()
	tools, err := h.PrepareCodexToolSurface(context.Background(), contract.CodexToolSurfaceScope{
		AgentID:          "agent-surface",
		ProviderThreadID: "provider-thread-1",
		CWD:              "/repo",
		Manifest:         testLSPManifest(),
	})
	if err != nil {
		t.Fatalf("PrepareCodexToolSurface() error = %v", err)
	}
	return tools
}

func assertCodexSurfaceHistoryReadIsHostTool(t *testing.T, tools []contract.DynamicToolSchema) {
	t.Helper()
	count := 0
	for _, tool := range tools {
		if tool.Name != ToolNameHistoryRead {
			continue
		}
		count++
		if tool.Description == "peer history" {
			t.Fatalf("history_read = %+v, want host surface tool", tool)
		}
	}
	if count != 1 {
		t.Fatalf("history_read count = %d in %+v, want one host surface tool", count, tools)
	}
}

func historyReadSurfaceCallParams(t *testing.T, arguments map[string]any) json.RawMessage {
	t.Helper()
	return mustRawJSON(t, map[string]any{
		"name":      ToolNameHistoryRead,
		"arguments": arguments,
		"_agentId":  "agent-surface",
		"_threadId": "provider-thread-1",
		"_callId":   "call-history",
		"_cwd":      "/repo",
	})
}

func assertHistoryReadSurfaceResult(
	t *testing.T,
	result any,
	status *stubSessionStatusPort,
	peer *fakeMCPClient,
	wantThreadID string,
) {
	t.Helper()
	toolResult, ok := result.(*ToolCallResult)
	if !ok {
		t.Fatalf("HandleToolCall() result = %#v, want *ToolCallResult", result)
	}
	if !toolResult.Success {
		t.Fatalf("HandleToolCall() result = %+v, want success", toolResult)
	}
	if status.calls != 1 || status.lastThreadID != wantThreadID {
		t.Fatalf("ReadMessages calls=%d thread=%q, want calls=1 thread=%q", status.calls, status.lastThreadID, wantThreadID)
	}
	if len(peer.calls) != 0 {
		t.Fatalf("peer calls = %#v, want host history_read without peer stdio", peer.calls)
	}
}

func TestCodexHistoryReadCallWithoutHostDoesNotFallbackToPeer(t *testing.T) {
	registry := &stubKindRegistry{peers: map[string][]*mcpcontrol.ToolInstance{
		mcpdto.ClientKindOrch: {
			{Peer: &stubPeer{callbackFn: func(_ context.Context, method string, params any, result any) error {
				t.Fatalf("peer callback called for %s with params %#v result %#v", method, params, result)
				return nil
			}}},
		},
	}}
	h := &Handler{registry: registry}

	got, err := h.routeToolCall(context.Background(), ToolCallRequest{
		Name:      ToolNameHistoryRead,
		Arguments: mustMarshal(t, map[string]any{"scope": "current_thread", "limit": 1}),
		ThreadID:  "thread-1",
	})
	if err != nil {
		t.Fatalf("routeToolCall() error = %v", err)
	}
	if got == nil || got.Success {
		t.Fatalf("result = %+v, want host-only failure", got)
	}
	envelope := decodeToolResultEnvelope(t, got)
	if envelope["tool"] != ToolNameHistoryRead || envelope["code"] != "history_unavailable" {
		t.Fatalf("envelope = %#v, want history_unavailable", envelope)
	}
	if len(registry.gotKinds) != 0 {
		t.Fatalf("FindActiveByKind() kinds = %#v, want no peer fallback", registry.gotKinds)
	}
}

func TestListToolsForCodex_FiltersPeerHistoryReadWhenHostUnavailable(t *testing.T) {
	registry := &stubKindRegistry{peers: map[string][]*mcpcontrol.ToolInstance{
		mcpdto.ClientKindOrch: {listToolsPeer([]mcpdto.MCPTool{
			{Name: ToolNameHistoryRead, Description: "peer history"},
			{Name: "launch_agent", Description: "peer orch"},
		}, nil)},
		mcpdto.ClientKindLSP: {listToolsPeer(nil, nil)},
	}}
	h := &Handler{registry: registry}

	tools, err := h.ListToolsForCodex(context.Background())
	if err != nil {
		t.Fatalf("ListToolsForCodex() error = %v", err)
	}
	if containsDynamicToolName(tools, ToolNameHistoryRead) {
		t.Fatalf("dynamic tools = %+v, must filter peer history_read when host history is unavailable", tools)
	}
	if !containsDynamicToolName(tools, "launch_agent") {
		t.Fatalf("dynamic tools = %+v, want non-history peer tool preserved", tools)
	}
}

func TestListToolsForCodex_IncludesHostHistoryReadAndBlocksPeerShadow(t *testing.T) {
	host := NewHistoryReadHostToolRegistry(&stubSessionStatusPort{})
	registry := &stubKindRegistry{peers: map[string][]*mcpcontrol.ToolInstance{
		mcpdto.ClientKindOrch: {listToolsPeer([]mcpdto.MCPTool{{Name: ToolNameHistoryRead, Description: "peer history"}}, nil)},
		mcpdto.ClientKindLSP:  {listToolsPeer(nil, nil)},
	}}
	h := &Handler{registry: registry, hostTools: host}

	tools, err := h.ListToolsForCodex(context.Background())
	if err != nil {
		t.Fatalf("ListToolsForCodex() error = %v", err)
	}
	count := 0
	for _, tool := range tools {
		if tool.Name == ToolNameHistoryRead {
			count++
			if tool.Description == "peer history" {
				t.Fatalf("history_read = %+v, want host description", tool)
			}
		}
	}
	if count != 1 {
		t.Fatalf("history_read count = %d in %+v, want one host tool", count, tools)
	}
}

func TestProxyToolsList_ReservedFilteringCoversHistoryRead(t *testing.T) {
	h := &Handler{registry: &stubKindRegistry{peers: map[string][]*mcpcontrol.ToolInstance{
		mcpdto.ClientKindLSP: {listToolsPeer([]mcpdto.MCPTool{
			{Name: ToolNameHistoryRead, Description: "peer history"},
			{Name: "lsp_hover", Description: "lsp"},
		}, nil)},
	}}, proxyAuthToken: newProxyAuthToken()}

	got := callProxyRequest(t, h, "/mcp/lsp/agent-1", `{"jsonrpc":"2.0","id":"req-1","method":"tools/list"}`)

	if got.Error != nil {
		t.Fatalf("proxy tools/list error = %+v", got.Error)
	}
	result := got.Result.(map[string]any)
	tools := result["tools"].([]any)
	if proxyToolsContainName(tools, ToolNameHistoryRead) {
		t.Fatalf("tools = %#v, must filter peer history_read for non-orch proxy list", tools)
	}
	if !proxyToolsContainName(tools, "lsp_hover") {
		t.Fatalf("tools = %#v, want non-history peer tool preserved", tools)
	}
}

func TestHistoryReadHostToolRegistry_ReadErrorReturnsStableCode(t *testing.T) {
	reg := NewHistoryReadHostToolRegistry(&stubSessionStatusPort{err: errors.New("store offline")})
	_, err := reg.CallHostTool(context.Background(), HostToolCall{
		Name:      ToolNameHistoryRead,
		Arguments: mustMarshal(t, map[string]any{"scope": "current_thread", "limit": 1}),
		ThreadID:  "thread-1",
	})
	if code := contract.AgentMemoryErrorCode(err); code != "history_read_failed" {
		t.Fatalf("error code = %q, want history_read_failed (err=%v)", code, err)
	}
}
