package toolbridge

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"unsafe"

	"github.com/gorilla/websocket"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
	dto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/mcp"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/mcpcontrol"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/rpc"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/provider/codexapp"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/provider/unified"
	threadstore "github.com/lihah111222333-cloud/super-dolphin-agent/internal/store/thread"
	pkglogger "github.com/lihah111222333-cloud/super-dolphin-agent/pkg/logger"
	"github.com/lihah111222333-cloud/super-dolphin-agent/pkg/skillmetrics"
)

//go:linkname codexNewSession github.com/lihah111222333-cloud/super-dolphin-agent/internal/provider/codexapp.newSession
func codexNewSession(context.Context, *pkglogger.Runtime, string, string, *unified.EventDispatcher, *rpc.ApprovalManager, *codexapp.ServerManager, *skillmetrics.Registry) (unsafe.Pointer, error)

//go:linkname codexSessionOnInboundMessage github.com/lihah111222333-cloud/super-dolphin-agent/internal/provider/codexapp.(*session).onInboundMessage
func codexSessionOnInboundMessage(unsafe.Pointer, context.Context, codexapp.Responder, codexapp.RawMessage)

//go:linkname codexSessionClose github.com/lihah111222333-cloud/super-dolphin-agent/internal/provider/codexapp.(*session).Close
func codexSessionClose(unsafe.Pointer, context.Context) error

// codexSessionClose 由 runtime owner 负责等待 reader、health 和 recovery goroutine 收尾。
// 测试只保留 Close 的 linkname，避免重新引用已经不属于公开测试边界的内部等待函数。

type stubRegistry struct {
	peers     []*mcpcontrol.ToolInstance
	gotKinds  []string
	gotScopes []mcpcontrol.ToolScope
	scoped    bool
}

func (r *stubRegistry) FindActiveByKind(clientKind string) []*mcpcontrol.ToolInstance {
	r.gotKinds = append(r.gotKinds, clientKind)
	return r.peers
}

func (r *stubRegistry) FindActiveForScope(scope mcpcontrol.ToolScope) []*mcpcontrol.ToolInstance {
	r.gotScopes = append(r.gotScopes, scope)
	if !r.scoped {
		return r.FindActiveByKind(scope.Family)
	}
	if exact := r.findExactScopedPeers(scope); len(exact) != 0 {
		return exact
	}
	if relaxed := r.findAgentScopedPeers(scope); len(relaxed) != 0 {
		return relaxed
	}
	return filterStubPeers(r.peers, func(peer *mcpcontrol.ToolInstance) bool {
		return peer.ClientKind == "" || peer.ClientKind == scope.Family
	})
}

func (r *stubRegistry) findExactScopedPeers(scope mcpcontrol.ToolScope) []*mcpcontrol.ToolInstance {
	if scope.AgentID == "" || scope.ThreadID == "" {
		return nil
	}
	return filterStubPeers(r.peers, func(peer *mcpcontrol.ToolInstance) bool {
		return peer.AgentID == scope.AgentID && peer.ThreadID == scope.ThreadID && peer.ClientKind == scope.Family
	})
}

func (r *stubRegistry) findAgentScopedPeers(scope mcpcontrol.ToolScope) []*mcpcontrol.ToolInstance {
	if scope.AgentID == "" {
		return nil
	}
	return filterStubPeers(r.peers, func(peer *mcpcontrol.ToolInstance) bool {
		return peer.AgentID == scope.AgentID && peer.ClientKind == scope.Family
	})
}

func filterStubPeers(peers []*mcpcontrol.ToolInstance, keep func(*mcpcontrol.ToolInstance) bool) []*mcpcontrol.ToolInstance {
	var out []*mcpcontrol.ToolInstance
	for _, peer := range peers {
		if peer != nil && keep(peer) {
			out = append(out, peer)
		}
	}
	return out
}

type stubPeer struct {
	callbackFn func(context.Context, string, any, any) error
}

// stubThreadStore 满足 ports.go 中的 threadConfigOverrideStore 窄端口。
// fixture 仍使用 *threadstore.Thread，保持本文件其他从 thread 行构造 ConfigOverride 的测试一致。
type stubThreadStore struct {
	thread *threadstore.Thread
}

func (s *stubThreadStore) GetConfigOverride(_ context.Context, threadID string) (json.RawMessage, error) {
	if s.thread == nil || s.thread.ThreadID != threadID {
		return nil, fmt.Errorf("thread %s not found", threadID)
	}
	return s.thread.ConfigOverride, nil
}

type stubUIPreferenceReader struct {
	values map[string]any
	cwds   []string
	err    error
}

func (s *stubUIPreferenceReader) GetMergedPreferences(_ context.Context, cwd string) (map[string]any, error) {
	s.cwds = append(s.cwds, cwd)
	if s.err != nil {
		return nil, s.err
	}
	out := make(map[string]any, len(s.values))
	maps.Copy(out, s.values)
	return out, nil
}

func (p *stubPeer) Notify(context.Context, string, any) error { return nil }

func (p *stubPeer) Callback(ctx context.Context, method string, params any, result any) error {
	if p.callbackFn != nil {
		return p.callbackFn(ctx, method, params, result)
	}
	return nil
}

func (p *stubPeer) Close() error { return nil }

type stubResponder struct {
	id      json.RawMessage
	result  any
	callErr error
	calls   int
}

func (r *stubResponder) RespondWithID(id json.RawMessage, result any, callErr error) error {
	r.id = append(json.RawMessage(nil), id...)
	r.result = result
	r.callErr = callErr
	r.calls++
	return nil
}

func newHandlerForTest(peers ...*mcpcontrol.ToolInstance) (*Handler, *stubRegistry) {
	registry := &stubRegistry{peers: peers}
	return &Handler{registry: registry, proxyAuthToken: newProxyAuthToken()}, registry
}

func newToolCallPeer(t *testing.T, wantName string, wantArgs json.RawMessage, replyText string, callErr error) *mcpcontrol.ToolInstance {
	t.Helper()
	return &mcpcontrol.ToolInstance{Peer: &stubPeer{callbackFn: func(_ context.Context, method string, params any, result any) error {
		if method != "tools/call" {
			t.Fatalf("Callback() method = %q, want tools/call", method)
		}
		assertToolCallPayload(t, params, wantName, wantArgs)
		if callErr != nil {
			return callErr
		}
		resp, ok := result.(*peerToolCallResponse)
		if !ok {
			t.Fatalf("Callback() result type = %T, want *peerToolCallResponse", result)
		}
		*resp = peerToolCallResponse{Content: []peerToolCallContent{{Type: "text", Text: replyText}}}
		return nil
	}}}
}

func assertToolCallPayload(t *testing.T, params any, wantName string, wantArgs json.RawMessage) {
	t.Helper()
	payload, ok := params.(map[string]any)
	if !ok {
		t.Fatalf("Callback() params type = %T, want map[string]any", params)
	}
	if got := payload["name"]; got != wantName {
		t.Fatalf("Callback() name = %v, want %q", got, wantName)
	}
	gotArgs, ok := payload["arguments"].(json.RawMessage)
	if !ok {
		t.Fatalf("Callback() arguments type = %T, want json.RawMessage", payload["arguments"])
	}
	if string(gotArgs) != string(wantArgs) {
		t.Fatalf("Callback() arguments = %s, want %s", string(gotArgs), string(wantArgs))
	}
}

func assertSingleTextItem(t *testing.T, got *ToolCallResult, wantText string, wantSuccess bool) {
	t.Helper()
	if got == nil {
		t.Fatal("ToolCallResult = nil")
	}
	if got.Success != wantSuccess {
		t.Fatalf("ToolCallResult.Success = %v, want %v", got.Success, wantSuccess)
	}
	if len(got.ContentItems) != 1 {
		t.Fatalf("len(ContentItems) = %d, want 1", len(got.ContentItems))
	}
	item := got.ContentItems[0]
	if item.Type != "inputText" {
		t.Fatalf("ContentItems[0].Type = %q, want inputText", item.Type)
	}
	if item.Text != wantText {
		t.Fatalf("ContentItems[0].Text = %q, want %q", item.Text, wantText)
	}
}

func mustRawJSON(t *testing.T, v any) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	return raw
}

func callProxyRequest(t *testing.T, h *Handler, path, body string) proxyJSONRPCResponse {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	if h.proxyAuthToken != "" {
		req.Header.Set("Authorization", "Bearer "+h.proxyAuthToken)
	}
	resp := httptest.NewRecorder()
	h.handleProxyRequest(resp, req)
	if resp.Code != http.StatusOK {
		t.Fatalf("handleProxyRequest() status = %d, want %d", resp.Code, http.StatusOK)
	}
	var got proxyJSONRPCResponse
	if err := json.Unmarshal(resp.Body.Bytes(), &got); err != nil {
		t.Fatalf("json.Unmarshal(response) error = %v, body=%s", err, resp.Body.String())
	}
	return got
}

func startCodexBridgeTestServer(t *testing.T) string {
	t.Helper()
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		for {
			_, raw, err := conn.ReadMessage()
			if err != nil {
				return
			}
			var msg struct {
				ID json.RawMessage `json:"id"`
			}
			if err := json.Unmarshal(raw, &msg); err != nil || len(msg.ID) == 0 {
				continue
			}
			resp, err := json.Marshal(map[string]any{
				"jsonrpc": "2.0",
				"id":      json.RawMessage(append([]byte(nil), msg.ID...)),
				"result":  map[string]any{"ok": true},
			})
			if err != nil {
				t.Fatalf("json.Marshal(response) error = %v", err)
			}
			if err := conn.WriteMessage(websocket.TextMessage, resp); err != nil {
				return
			}
		}
	}))
	t.Cleanup(server.Close)
	return "ws" + strings.TrimPrefix(server.URL, "http")
}

func newInboundSession(t *testing.T) unsafe.Pointer {
	t.Helper()
	logRuntime := pkglogger.NewRuntime(pkglogger.RuntimeConfig{})
	sessionPtr, err := codexNewSession(context.Background(), logRuntime, startCodexBridgeTestServer(t), "agent-1", nil, rpc.NewApprovalManager(nil, nil), nil, skillmetrics.NewRegistry())
	if err != nil {
		t.Fatalf("newSession() error = %v", err)
	}
	t.Cleanup(func() {
		// Close 返回前已经收尾 reader、health 和 recovery goroutine，不再需要额外等待内部 reader。
		if err := codexSessionClose(sessionPtr, context.Background()); err != nil {
			t.Errorf("Close() error = %v", err)
		}
	})
	return sessionPtr
}

func TestToolBridge_FreshSession_ToolCallForward(t *testing.T) {
	args := mustRawJSON(t, map[string]any{"line": 1})
	h, registry := newHandlerForTest(newToolCallPeer(t, "inspect", args, "fresh ok", nil))

	got, err := h.routeToolCall(context.Background(), ToolCallRequest{Name: "inspect", Arguments: args})
	if err != nil {
		t.Fatalf("routeToolCall() error = %v", err)
	}
	if len(registry.gotKinds) != 1 || registry.gotKinds[0] != classifyTool("inspect") {
		t.Fatalf("FindActiveByKind() kinds = %#v, want [%q]", registry.gotKinds, classifyTool("inspect"))
	}
	assertSingleTextItem(t, got, "fresh ok", true)
}

func TestToolBridge_Resume_ToolCallStillWorks(t *testing.T) {
	args := mustRawJSON(t, map[string]any{"symbol": "x"})
	h, registry := newHandlerForTest(newToolCallPeer(t, "inspect", args, "resume ok", nil))
	msg := contract.ToolCallRawMessage{Params: mustRawJSON(t, map[string]any{
		"name":      "inspect",
		"arguments": json.RawMessage(args),
		"agentId":   "agent-1",
		"threadId":  "thread-1",
		"callId":    "call-1",
	})}

	result, err := h.HandleToolCall(context.Background(), msg)
	if err != nil {
		t.Fatalf("HandleToolCall() error = %v", err)
	}
	got, ok := result.(*ToolCallResult)
	if !ok {
		t.Fatalf("HandleToolCall() result type = %T, want *ToolCallResult", result)
	}
	if len(registry.gotKinds) != 1 || registry.gotKinds[0] != classifyTool("inspect") {
		t.Fatalf("FindActiveByKind() kinds = %#v, want [%q]", registry.gotKinds, classifyTool("inspect"))
	}
	assertSingleTextItem(t, got, "resume ok", true)
}

func TestToolBridge_OrchestrationLaunchInjectsOnlyParentIDFromProviderLookup(t *testing.T) {
	args := mustRawJSON(t, map[string]any{
		"context_mode": "forked",
		"name":         "idle-agent",
		"provider":     "codex",
	})
	wantArgs := mustRawJSON(t, map[string]any{
		"codex_home":           "/Users/test/.codex",
		"codex_instance_key":   "default",
		"codex_model_provider": "openai",
		"context_mode":         "forked",
		"name":                 "idle-agent",
		"parent_id":            "agent-parent",
		"provider":             "codex",
	})
	h, registry := newHandlerForTest(newToolCallPeer(t, "launch_agent", wantArgs, "launching", nil))
	h.bindingStore = &toolCallBindingStoreStub{bindingsByProvider: map[string]toolCallBinding{
		"codex:provider-thread-parent": {
			AgentID:            "agent-parent",
			Provider:           "codex",
			ProviderThreadID:   "provider-thread-parent",
			CodexThreadID:      "thread-parent-public",
			CWD:                "/repo/project",
			CodexHome:          "/Users/test/.codex",
			CodexInstanceKey:   "default",
			CodexModelProvider: "openai",
		},
	}}

	got, err := h.routeToolCall(context.Background(), ToolCallRequest{
		Name:      "launch_agent",
		Arguments: args,
		ThreadID:  "provider-thread-parent",
	})
	if err != nil {
		t.Fatalf("routeToolCall() error = %v", err)
	}
	assertSingleTextItem(t, got, "launching", true)
	if len(registry.gotKinds) != 1 || registry.gotKinds[0] != dto.ClientKindOrch {
		t.Fatalf("FindActiveByKind() kinds = %#v, want [%q]", registry.gotKinds, dto.ClientKindOrch)
	}
}

func TestToolBridge_OrchestrationLaunchInheritsParentModelEffort(t *testing.T) {
	args := mustRawJSON(t, map[string]any{
		"name":     "idle-agent",
		"provider": "codex",
	})
	wantArgs := mustRawJSON(t, map[string]any{
		"effort":    "xhigh",
		"model":     "gpt-5.5",
		"name":      "idle-agent",
		"parent_id": "agent-parent",
		"provider":  "codex",
	})
	h, _ := newHandlerForTest(newToolCallPeer(t, "launch_agent", wantArgs, "launching", nil))
	h.bindingStore = &toolCallBindingStoreStub{bindingsByAgent: map[string]toolCallBinding{
		"agent-parent": {
			AgentID:          "agent-parent",
			ProviderThreadID: "provider-thread-parent",
			CWD:              "/repo/project",
		},
	}}
	h.threadStore = &toolCallThreadStoreStub{row: &threadstore.Thread{
		ThreadID:       "agent-parent",
		ConfigOverride: json.RawMessage(`{"model":"gpt-5.5","effort":"xhigh"}`),
	}}

	got, err := h.routeToolCall(context.Background(), ToolCallRequest{
		Name:      "launch_agent",
		Arguments: args,
		AgentID:   "agent-parent",
	})
	if err != nil {
		t.Fatalf("routeToolCall() error = %v", err)
	}
	assertSingleTextItem(t, got, "launching", true)
}

func TestToolBridge_OrchestrationLaunchExplicitProviderDoesNotInheritMismatchedParentModelEffort(t *testing.T) {
	args := mustRawJSON(t, map[string]any{
		"name":     "idle-agent",
		"provider": "claude",
	})
	wantArgs := mustRawJSON(t, map[string]any{
		"effort":    "high",
		"model":     "sonnet",
		"name":      "idle-agent",
		"parent_id": "agent-parent",
		"provider":  "claude",
	})
	h, _ := newHandlerForTest(newToolCallPeer(t, "launch_agent", wantArgs, "launching", nil))
	h.bindingStore = &toolCallBindingStoreStub{bindingsByAgent: map[string]toolCallBinding{
		"agent-parent": {
			AgentID:          "agent-parent",
			Provider:         "codex",
			ProviderThreadID: "provider-thread-parent",
			CWD:              "/repo/project",
		},
	}}
	h.threadStore = &toolCallThreadStoreStub{row: &threadstore.Thread{
		ThreadID:       "agent-parent",
		ConfigOverride: json.RawMessage(`{"model":"gpt-5.5","effort":"xhigh"}`),
	}}
	h.preferences = &stubUIPreferenceReader{}

	got, err := h.routeToolCall(context.Background(), ToolCallRequest{
		Name:      "launch_agent",
		Arguments: args,
		AgentID:   "agent-parent",
	})
	if err != nil {
		t.Fatalf("routeToolCall() error = %v", err)
	}
	assertSingleTextItem(t, got, "launching", true)
}

func TestToolBridge_OrchestrationLaunchFallsBackToUIPreferences(t *testing.T) {
	args := mustRawJSON(t, map[string]any{
		"name":     "idle-agent",
		"provider": "codex",
	})
	wantArgs := mustRawJSON(t, map[string]any{
		"effort":    "high",
		"model":     "gpt-5.4",
		"name":      "idle-agent",
		"parent_id": "agent-parent",
		"provider":  "codex",
	})
	prefs := &stubUIPreferenceReader{values: map[string]any{
		"settings.provider.codex.model":  "gpt-5.4",
		"settings.provider.codex.effort": "high",
	}}
	h, _ := newHandlerForTest(newToolCallPeer(t, "launch_agent", wantArgs, "launching", nil))
	h.bindingStore = &toolCallBindingStoreStub{bindingsByAgent: map[string]toolCallBinding{
		"agent-parent": {
			AgentID:          "agent-parent",
			ProviderThreadID: "provider-thread-parent",
			CWD:              "/repo/project",
		},
	}}
	h.threadStore = &toolCallThreadStoreStub{}
	h.preferences = prefs

	got, err := h.routeToolCall(context.Background(), ToolCallRequest{
		Name:      "launch_agent",
		Arguments: args,
		AgentID:   "agent-parent",
	})
	if err != nil {
		t.Fatalf("routeToolCall() error = %v", err)
	}
	assertSingleTextItem(t, got, "launching", true)
	if len(prefs.cwds) != 1 || prefs.cwds[0] != "/repo/project" {
		t.Fatalf("GetMergedPreferences() cwds = %#v, want [/repo/project]", prefs.cwds)
	}
}

func TestToolBridge_OrchestrationLaunchFillsMissingProviderFromUIPreferences(t *testing.T) {
	args := mustRawJSON(t, map[string]any{
		"name": "idle-agent",
	})
	wantArgs := mustRawJSON(t, map[string]any{
		"effort":    "high",
		"model":     "sonnet",
		"name":      "idle-agent",
		"parent_id": "agent-parent",
		"provider":  "claude",
	})
	prefs := &stubUIPreferenceReader{values: map[string]any{
		"settings.provider.active":        "claude",
		"settings.provider.claude.model":  "sonnet",
		"settings.provider.claude.effort": "high",
	}}
	h, _ := newHandlerForTest(newToolCallPeer(t, "launch_agent", wantArgs, "launching", nil))
	h.bindingStore = &toolCallBindingStoreStub{bindingsByAgent: map[string]toolCallBinding{
		"agent-parent": {
			AgentID:          "agent-parent",
			Provider:         "codex",
			ProviderThreadID: "provider-thread-parent",
			CWD:              "/repo/project",
		},
	}}
	h.threadStore = &toolCallThreadStoreStub{}
	h.preferences = prefs

	got, err := h.routeToolCall(context.Background(), ToolCallRequest{
		Name:      "launch_agent",
		Arguments: args,
		AgentID:   "agent-parent",
	})
	if err != nil {
		t.Fatalf("routeToolCall() error = %v", err)
	}
	assertSingleTextItem(t, got, "launching", true)
}

func TestToolBridge_OrchestrationLaunchUsesProviderDefaultsWhenUIPreferencesUnset(t *testing.T) {
	args := mustRawJSON(t, map[string]any{
		"name":     "idle-agent",
		"provider": "codex",
	})
	wantArgs := mustRawJSON(t, map[string]any{
		"effort":    "xhigh",
		"model":     "gpt-5.5",
		"name":      "idle-agent",
		"parent_id": "agent-parent",
		"provider":  "codex",
	})
	h, _ := newHandlerForTest(newToolCallPeer(t, "launch_agent", wantArgs, "launching", nil))
	h.bindingStore = &toolCallBindingStoreStub{bindingsByAgent: map[string]toolCallBinding{
		"agent-parent": {
			AgentID:          "agent-parent",
			ProviderThreadID: "provider-thread-parent",
			CWD:              "/repo/project",
		},
	}}
	h.threadStore = &toolCallThreadStoreStub{}
	h.preferences = &stubUIPreferenceReader{}

	got, err := h.routeToolCall(context.Background(), ToolCallRequest{
		Name:      "launch_agent",
		Arguments: args,
		AgentID:   "agent-parent",
	})
	if err != nil {
		t.Fatalf("routeToolCall() error = %v", err)
	}
	assertSingleTextItem(t, got, "launching", true)
}

func TestToolBridge_OrchestrationLaunchPreservesExplicitParentContext(t *testing.T) {
	args := mustRawJSON(t, map[string]any{
		"cwd":       "/explicit",
		"effort":    "medium",
		"model":     "gpt-5.2",
		"name":      "idle-agent",
		"parent_id": "agent-explicit",
		"provider":  "codex",
	})
	h, _ := newHandlerForTest(newToolCallPeer(t, "launch_agent", args, "launching", nil))
	h.bindingStore = &toolCallBindingStoreStub{bindingsByAgent: map[string]toolCallBinding{
		"agent-parent": {
			AgentID:          "agent-parent",
			ProviderThreadID: "provider-thread-parent",
			CWD:              "/repo/project",
		},
	}}
	h.threadStore = &toolCallThreadStoreStub{row: &threadstore.Thread{
		ThreadID:       "agent-parent",
		ConfigOverride: json.RawMessage(`{"model":"gpt-5.5","effort":"xhigh"}`),
	}}
	h.preferences = &stubUIPreferenceReader{values: map[string]any{
		"settings.provider.codex.model":  "gpt-5.4",
		"settings.provider.codex.effort": "high",
	}}

	got, err := h.routeToolCall(context.Background(), ToolCallRequest{
		Name:      "launch_agent",
		Arguments: args,
		AgentID:   "agent-parent",
	})
	if err != nil {
		t.Fatalf("routeToolCall() error = %v", err)
	}
	assertSingleTextItem(t, got, "launching", true)
}

func TestToolBridge_OrchestrationLaunchDoesNotInjectCurrentCWDOverParent(t *testing.T) {
	args := mustRawJSON(t, map[string]any{
		"name":     "idle-agent",
		"provider": "codex",
	})
	wantArgs := mustRawJSON(t, map[string]any{
		"name":      "idle-agent",
		"parent_id": "agent-parent",
		"provider":  "codex",
	})
	h, _ := newHandlerForTest(newToolCallPeer(t, "launch_agent", wantArgs, "launching", nil))
	h.bindingStore = &toolCallBindingStoreStub{bindingsByAgent: map[string]toolCallBinding{
		"agent-parent": {
			AgentID:          "agent-parent",
			ProviderThreadID: "provider-thread-parent",
			CWD:              "/repo/stale",
		},
	}}

	got, err := h.routeToolCall(context.Background(), ToolCallRequest{
		Name:      "launch_agent",
		Arguments: args,
		AgentID:   "agent-parent",
		CWD:       "/repo/current",
	})
	if err != nil {
		t.Fatalf("routeToolCall() error = %v", err)
	}
	assertSingleTextItem(t, got, "launching", true)
}
