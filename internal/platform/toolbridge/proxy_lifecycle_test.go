package toolbridge

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	tooldto "github.com/anthropic-ai/super-agent-v3/internal/dto/tool"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/mcpcontrol"
	"github.com/kelindar/event"
)

func TestProxyToolCallPublishesLifecycleEvents(t *testing.T) {
	dispatcher := event.NewDispatcher()
	t.Cleanup(func() { _ = dispatcher.Close() })
	beginCh := make(chan tooldto.ToolCallBegin, 1)
	endCh := make(chan tooldto.ToolCallEnd, 1)
	cancelBegin := event.Subscribe(dispatcher, func(ev tooldto.ToolCallBegin) { beginCh <- ev })
	t.Cleanup(cancelBegin)
	cancelEnd := event.Subscribe(dispatcher, func(ev tooldto.ToolCallEnd) { endCh <- ev })
	t.Cleanup(cancelEnd)

	args := json.RawMessage(`{"action":"read_file","file_path":"smoke.go"}`)
	h, _ := newHandlerForTest(newToolCallPeer(t, "file", args, `{"success":true,"path":"smoke.go"}`, nil))
	h.dispatcher = dispatcher
	h.bindingStore = &toolCallBindingStoreStub{threadID: "thread-1"}

	got := callProxyRequest(t, h, "/mcp/lsp/agent-1", string(mustRawJSON(t, map[string]any{
		"jsonrpc": "2.0",
		"id":      "req-1",
		"method":  "tools/call",
		"params": map[string]any{
			"name":      "file",
			"arguments": json.RawMessage(args),
		},
	})))
	assertProxyLifecycleResponse(t, got)

	begin := waitProxyToolBegin(t, beginCh)
	assertProxyToolBegin(t, begin)
	end := waitProxyToolEnd(t, endCh)
	assertProxyToolEnd(t, end, begin)
}

func TestProxyToolCallLifecycleUsesAgentIDWithoutBindingStore(t *testing.T) {
	dispatcher := event.NewDispatcher()
	t.Cleanup(func() { _ = dispatcher.Close() })
	beginCh := make(chan tooldto.ToolCallBegin, 1)
	cancelBegin := event.Subscribe(dispatcher, func(ev tooldto.ToolCallBegin) { beginCh <- ev })
	t.Cleanup(cancelBegin)

	args := json.RawMessage(`{"action":"read_file","file_path":"smoke.go"}`)
	h, _ := newHandlerForTest(newToolCallPeer(t, "file", args, `{"success":true}`, nil))
	h.dispatcher = dispatcher

	got := callProxyRequest(t, h, "/mcp/lsp/agent-1", proxyLifecycleRequestBody(t, args))
	assertProxyLifecycleResponse(t, got)

	begin := waitProxyToolBegin(t, beginCh)
	if begin.ThreadID != "agent-1" || begin.AgentID != "agent-1" {
		t.Fatalf("begin = %+v, want agent id as fallback thread id", begin)
	}
}

func TestProxyToolCallLifecycleUsesSafePreview(t *testing.T) {
	dispatcher := event.NewDispatcher()
	t.Cleanup(func() { _ = dispatcher.Close() })
	beginCh := make(chan tooldto.ToolCallBegin, 1)
	endCh := make(chan tooldto.ToolCallEnd, 1)
	cancelBegin := event.Subscribe(dispatcher, func(ev tooldto.ToolCallBegin) { beginCh <- ev })
	t.Cleanup(cancelBegin)
	cancelEnd := event.Subscribe(dispatcher, func(ev tooldto.ToolCallEnd) { endCh <- ev })
	t.Cleanup(cancelEnd)

	args := json.RawMessage(`{"action":"read_file","token":"sk-abcdefghijklmnopqrstuvwxyz"}`)
	h, _ := newHandlerForTest(newToolCallPeer(t, "file", args, `{"success":true,"api_key":"sk-zyxwvutsrqponmlkjihgfedcba"}`, nil))
	h.dispatcher = dispatcher
	h.bindingStore = &toolCallBindingStoreStub{threadID: "thread-1"}

	got := callProxyRequest(t, h, "/mcp/lsp/agent-1", proxyLifecycleRequestBody(t, args))
	assertProxyLifecycleResponse(t, got)

	begin := waitProxyToolBegin(t, beginCh)
	if strings.Contains(begin.ArgumentsPreview, "sk-") || !strings.Contains(begin.ArgumentsPreview, "[REDACTED]") {
		t.Fatalf("begin ArgumentsPreview = %q, want redacted safe preview", begin.ArgumentsPreview)
	}
	end := waitProxyToolEnd(t, endCh)
	if strings.Contains(end.Result, "sk-") || !strings.Contains(end.Result, "[REDACTED]") {
		t.Fatalf("end Result = %q, want redacted safe preview", end.Result)
	}
}

func TestProxyToolCallNormalizesPeerStructuredContent(t *testing.T) {
	args := json.RawMessage(`{"action":"document_symbol","file_path":"smoke.go"}`)
	h, _ := newHandlerForTest(&mcpcontrol.ToolInstance{Peer: &stubPeer{callbackFn: func(_ context.Context, method string, params any, result any) error {
		if method != ProxyMethodToolsCall {
			t.Fatalf("Callback() method = %q, want %s", method, ProxyMethodToolsCall)
		}
		assertToolCallPayload(t, params, "structure", args)
		resp, ok := result.(*peerToolCallResponse)
		if !ok {
			t.Fatalf("Callback() result type = %T, want *peerToolCallResponse", result)
		}
		resp.StructuredContent = json.RawMessage(`[{"name":"targetName"},{"name":"useTarget"}]`)
		return nil
	}}})

	got := callProxyRequest(t, h, "/mcp/lsp/agent-1", string(mustRawJSON(t, map[string]any{
		"jsonrpc": "2.0",
		"id":      "req-1",
		"method":  "tools/call",
		"params": map[string]any{
			"name":      "structure",
			"arguments": json.RawMessage(args),
		},
	})))
	assertProxyLifecycleResponse(t, got)
	result := requireProxyResultMap(t, got)
	structured, ok := result["structuredContent"].(map[string]any)
	if !ok {
		t.Fatalf("structuredContent = %T, want object; result=%#v", result["structuredContent"], result)
	}
	if structured["total"] != float64(2) {
		t.Fatalf("structuredContent = %#v, want total=2", structured)
	}
}

func TestProxyToolCallFailsWhenBindingThreadIDMissing(t *testing.T) {
	dispatcher := event.NewDispatcher()
	t.Cleanup(func() { _ = dispatcher.Close() })
	beginCh := make(chan tooldto.ToolCallBegin, 1)
	cancelBegin := event.Subscribe(dispatcher, func(ev tooldto.ToolCallBegin) { beginCh <- ev })
	t.Cleanup(cancelBegin)

	args := json.RawMessage(`{"action":"read_file","file_path":"smoke.go"}`)
	h, _ := newHandlerForTest(newToolCallPeer(t, "file", args, `{"success":true}`, nil))
	h.dispatcher = dispatcher
	h.bindingStore = &toolCallBindingStoreStub{threadID: " "}

	got := callProxyRequest(t, h, "/mcp/lsp/agent-1", proxyLifecycleRequestBody(t, args))
	if got.Error == nil || !strings.Contains(got.Error.Message, "empty thread id") {
		t.Fatalf("proxy response error = %+v, want empty thread id error", got.Error)
	}
	assertNoProxyToolBegin(t, beginCh)
}

func TestProxyToolCallFailsWhenBindingLookupErrors(t *testing.T) {
	dispatcher := event.NewDispatcher()
	t.Cleanup(func() { _ = dispatcher.Close() })
	beginCh := make(chan tooldto.ToolCallBegin, 1)
	cancelBegin := event.Subscribe(dispatcher, func(ev tooldto.ToolCallBegin) { beginCh <- ev })
	t.Cleanup(cancelBegin)

	args := json.RawMessage(`{"action":"read_file","file_path":"smoke.go"}`)
	h, _ := newHandlerForTest(newToolCallPeer(t, "file", args, `{"success":true}`, nil))
	h.dispatcher = dispatcher
	h.bindingStore = &toolCallBindingStoreStub{err: errors.New("lookup failed")}

	got := callProxyRequest(t, h, "/mcp/lsp/agent-1", proxyLifecycleRequestBody(t, args))
	if got.Error == nil || !strings.Contains(got.Error.Message, "lookup failed") {
		t.Fatalf("proxy response error = %+v, want lookup error", got.Error)
	}
	assertNoProxyToolBegin(t, beginCh)
}

func proxyLifecycleRequestBody(t *testing.T, args json.RawMessage) string {
	t.Helper()
	return string(mustRawJSON(t, map[string]any{
		"jsonrpc": "2.0",
		"id":      "req-1",
		"method":  "tools/call",
		"params": map[string]any{
			"name":      "file",
			"arguments": json.RawMessage(args),
		},
	}))
}

func assertProxyLifecycleResponse(t *testing.T, got proxyJSONRPCResponse) {
	t.Helper()
	if got.Error != nil {
		t.Fatalf("proxy response error = %+v", got.Error)
	}
}

func assertProxyToolBegin(t *testing.T, begin tooldto.ToolCallBegin) {
	t.Helper()
	if begin.ThreadID != "thread-1" || begin.AgentID != "agent-1" || begin.CallID != "req-1" || begin.ToolName != "file" {
		t.Fatalf("begin = %+v, want thread-1/agent-1/req-1/file", begin)
	}
	if !strings.Contains(begin.ArgumentsPreview, "read_file") {
		t.Fatalf("begin ArgumentsPreview = %q, want read_file", begin.ArgumentsPreview)
	}
}

func assertProxyToolEnd(t *testing.T, end tooldto.ToolCallEnd, begin tooldto.ToolCallBegin) {
	t.Helper()
	if end.ThreadID != begin.ThreadID || end.AgentID != begin.AgentID || end.CallID != begin.CallID || end.ToolName != begin.ToolName {
		t.Fatalf("end = %+v, want same scope as begin %+v", end, begin)
	}
	if !end.Success || !strings.Contains(end.Result, "smoke.go") {
		t.Fatalf("end = %+v, want successful result preview containing smoke.go", end)
	}
}

func waitProxyToolBegin(t *testing.T, ch <-chan tooldto.ToolCallBegin) tooldto.ToolCallBegin {
	t.Helper()
	select {
	case ev := <-ch:
		return ev
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for proxy ToolCallBegin")
		return tooldto.ToolCallBegin{}
	}
}

func waitProxyToolEnd(t *testing.T, ch <-chan tooldto.ToolCallEnd) tooldto.ToolCallEnd {
	t.Helper()
	select {
	case ev := <-ch:
		return ev
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for proxy ToolCallEnd")
		return tooldto.ToolCallEnd{}
	}
}

func assertNoProxyToolBegin(t *testing.T, ch <-chan tooldto.ToolCallBegin) {
	t.Helper()
	select {
	case ev := <-ch:
		t.Fatalf("unexpected proxy ToolCallBegin = %+v", ev)
	case <-time.After(50 * time.Millisecond):
	}
}
