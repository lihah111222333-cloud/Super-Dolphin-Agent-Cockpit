package toolbridge

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
	"unsafe"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/mcp"
	platformconfig "github.com/anthropic-ai/super-agent-v3/internal/platform/config"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/mcpcontrol"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/rpc"
	"github.com/anthropic-ai/super-agent-v3/internal/provider/codexapp"
	"github.com/anthropic-ai/super-agent-v3/internal/provider/unified"
	threadstore "github.com/anthropic-ai/super-agent-v3/internal/store/thread"
	"github.com/gorilla/websocket"
)

//go:linkname codexNewSession github.com/anthropic-ai/super-agent-v3/internal/provider/codexapp.newSession
func codexNewSession(context.Context, *slog.Logger, string, string, *unified.EventDispatcher, *rpc.ApprovalManager, *codexapp.ServerManager) (unsafe.Pointer, error)

//go:linkname codexSessionOnInboundMessage github.com/anthropic-ai/super-agent-v3/internal/provider/codexapp.(*session).onInboundMessage
func codexSessionOnInboundMessage(unsafe.Pointer, context.Context, codexapp.Responder, codexapp.RawMessage)

//go:linkname codexSessionClose github.com/anthropic-ai/super-agent-v3/internal/provider/codexapp.(*session).Close
func codexSessionClose(unsafe.Pointer, context.Context) error

// P22 P1c (commit 4dfed68): codexapp deleted (*session).waitReadLoopStopped —
// the runtime owner joined via codexSessionClose already drains the reader,
// so this linkname is no longer needed. Kept removed to avoid relocation
// failures against non-existent symbols.

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
	if scope.AgentID != "" && scope.ThreadID != "" {
		if exact := filterStubPeers(r.peers, func(peer *mcpcontrol.ToolInstance) bool {
			return peer.AgentID == scope.AgentID && peer.ThreadID == scope.ThreadID && peer.ClientKind == scope.Family
		}); len(exact) != 0 {
			return exact
		}
	}
	if scope.AgentID != "" {
		if relaxed := filterStubPeers(r.peers, func(peer *mcpcontrol.ToolInstance) bool {
			return peer.AgentID == scope.AgentID && peer.ClientKind == scope.Family
		}); len(relaxed) != 0 {
			return relaxed
		}
	}
	return filterStubPeers(r.peers, func(peer *mcpcontrol.ToolInstance) bool {
		return peer.ClientKind == "" || peer.ClientKind == scope.Family
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

// stubThreadStore satisfies the narrow threadConfigOverrideStore port
// from ports.go. The fixture data type is still *threadstore.Thread for
// consistency with other tests in this file that build
// ConfigOverride from a thread row.
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
	for key, value := range s.values {
		out[key] = value
	}
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
	return &Handler{registry: registry}, registry
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
	sessionPtr, err := codexNewSession(context.Background(), nil, startCodexBridgeTestServer(t), "agent-1", nil, nil, nil)
	if err != nil {
		t.Fatalf("newSession() error = %v", err)
	}
	t.Cleanup(func() {
		// P22 P1c: Close() now drains the runtime (reader + health + recovery)
		// before returning, so the historical waitReadLoopStopped follow-up is
		// unnecessary.
		if err := codexSessionClose(sessionPtr, context.Background()); err != nil {
			t.Errorf("Close() error = %v", err)
		}
	})
	return sessionPtr
}

func TestToolBridge_FreshSession_ToolCallForward(t *testing.T) {
	args := mustRawJSON(t, map[string]any{"line": 1})
	h, registry := newHandlerForTest(newToolCallPeer(t, "lsp_hover", args, "fresh ok", nil))

	got, err := h.routeToolCall(context.Background(), ToolCallRequest{Name: "lsp_hover", Arguments: args})
	if err != nil {
		t.Fatalf("routeToolCall() error = %v", err)
	}
	if len(registry.gotKinds) != 1 || registry.gotKinds[0] != classifyTool("lsp_hover") {
		t.Fatalf("FindActiveByKind() kinds = %#v, want [%q]", registry.gotKinds, classifyTool("lsp_hover"))
	}
	assertSingleTextItem(t, got, "fresh ok", true)
}

func TestToolBridge_Resume_ToolCallStillWorks(t *testing.T) {
	args := mustRawJSON(t, map[string]any{"symbol": "x"})
	h, registry := newHandlerForTest(newToolCallPeer(t, "lsp_definition", args, "resume ok", nil))
	msg := contract.ToolCallRawMessage{Params: mustRawJSON(t, map[string]any{
		"name":      "lsp_definition",
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
	if len(registry.gotKinds) != 1 || registry.gotKinds[0] != classifyTool("lsp_definition") {
		t.Fatalf("FindActiveByKind() kinds = %#v, want [%q]", registry.gotKinds, classifyTool("lsp_definition"))
	}
	assertSingleTextItem(t, got, "resume ok", true)
}

func TestToolBridge_OrchestrationLaunchInheritsParentContextFromProviderThread(t *testing.T) {
	args := mustRawJSON(t, map[string]any{
		"name":     "idle-agent",
		"provider": "codex",
	})
	wantArgs := mustRawJSON(t, map[string]any{
		"cwd":       "/repo/project",
		"name":      "idle-agent",
		"parent_id": "agent-parent",
		"provider":  "codex",
	})
	h, registry := newHandlerForTest(newToolCallPeer(t, "orchestration_launch_agent", wantArgs, "launching", nil))
	h.bindingStore = &toolCallBindingStoreStub{bindingsByProvider: map[string]toolCallBinding{
		"codex:provider-thread-parent": {
			AgentID:            "agent-parent",
			Provider:           "codex",
			ProviderThreadID:   "provider-thread-parent",
			CWD:                "/repo/project",
			CodexHome:          "/Users/test/.codex",
			CodexInstanceKey:   "default",
			CodexModelProvider: "openai",
		},
	}}

	got, err := h.routeToolCall(context.Background(), ToolCallRequest{
		Name:      "orchestration_launch_agent",
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
		"cwd":       "/repo/project",
		"effort":    "xhigh",
		"model":     "gpt-5.5",
		"name":      "idle-agent",
		"parent_id": "agent-parent",
		"provider":  "codex",
	})
	h, _ := newHandlerForTest(newToolCallPeer(t, "orchestration_launch_agent", wantArgs, "launching", nil))
	h.bindingStore = &toolCallBindingStoreStub{bindingsByAgent: map[string]toolCallBinding{
		"agent-parent": {
			AgentID: "agent-parent",
			CWD:     "/repo/project",
		},
	}}
	h.threadStore = &toolCallThreadStoreStub{row: &threadstore.Thread{
		ThreadID:       "agent-parent",
		ConfigOverride: json.RawMessage(`{"model":"gpt-5.5","effort":"xhigh"}`),
	}}

	got, err := h.routeToolCall(context.Background(), ToolCallRequest{
		Name:      "orchestration_launch_agent",
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
		"cwd":       "/repo/project",
		"effort":    "high",
		"model":     "sonnet",
		"name":      "idle-agent",
		"parent_id": "agent-parent",
		"provider":  "claude",
	})
	h, _ := newHandlerForTest(newToolCallPeer(t, "orchestration_launch_agent", wantArgs, "launching", nil))
	h.bindingStore = &toolCallBindingStoreStub{bindingsByAgent: map[string]toolCallBinding{
		"agent-parent": {
			AgentID:  "agent-parent",
			Provider: "codex",
			CWD:      "/repo/project",
		},
	}}
	h.threadStore = &toolCallThreadStoreStub{row: &threadstore.Thread{
		ThreadID:       "agent-parent",
		ConfigOverride: json.RawMessage(`{"model":"gpt-5.5","effort":"xhigh"}`),
	}}
	h.preferences = &stubUIPreferenceReader{}

	got, err := h.routeToolCall(context.Background(), ToolCallRequest{
		Name:      "orchestration_launch_agent",
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
		"cwd":       "/repo/project",
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
	h, _ := newHandlerForTest(newToolCallPeer(t, "orchestration_launch_agent", wantArgs, "launching", nil))
	h.bindingStore = &toolCallBindingStoreStub{bindingsByAgent: map[string]toolCallBinding{
		"agent-parent": {
			AgentID: "agent-parent",
			CWD:     "/repo/project",
		},
	}}
	h.threadStore = &toolCallThreadStoreStub{}
	h.preferences = prefs

	got, err := h.routeToolCall(context.Background(), ToolCallRequest{
		Name:      "orchestration_launch_agent",
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
		"cwd":       "/repo/project",
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
	h, _ := newHandlerForTest(newToolCallPeer(t, "orchestration_launch_agent", wantArgs, "launching", nil))
	h.bindingStore = &toolCallBindingStoreStub{bindingsByAgent: map[string]toolCallBinding{
		"agent-parent": {
			AgentID:  "agent-parent",
			Provider: "codex",
			CWD:      "/repo/project",
		},
	}}
	h.threadStore = &toolCallThreadStoreStub{}
	h.preferences = prefs

	got, err := h.routeToolCall(context.Background(), ToolCallRequest{
		Name:      "orchestration_launch_agent",
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
		"cwd":       "/repo/project",
		"effort":    "xhigh",
		"model":     "gpt-5.5",
		"name":      "idle-agent",
		"parent_id": "agent-parent",
		"provider":  "codex",
	})
	h, _ := newHandlerForTest(newToolCallPeer(t, "orchestration_launch_agent", wantArgs, "launching", nil))
	h.bindingStore = &toolCallBindingStoreStub{bindingsByAgent: map[string]toolCallBinding{
		"agent-parent": {
			AgentID: "agent-parent",
			CWD:     "/repo/project",
		},
	}}
	h.threadStore = &toolCallThreadStoreStub{}
	h.preferences = &stubUIPreferenceReader{}

	got, err := h.routeToolCall(context.Background(), ToolCallRequest{
		Name:      "orchestration_launch_agent",
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
	h, _ := newHandlerForTest(newToolCallPeer(t, "orchestration_launch_agent", args, "launching", nil))
	h.bindingStore = &toolCallBindingStoreStub{bindingsByAgent: map[string]toolCallBinding{
		"agent-parent": {
			AgentID: "agent-parent",
			CWD:     "/repo/project",
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
		Name:      "orchestration_launch_agent",
		Arguments: args,
		AgentID:   "agent-parent",
	})
	if err != nil {
		t.Fatalf("routeToolCall() error = %v", err)
	}
	assertSingleTextItem(t, got, "launching", true)
}

func TestToolBridge_OrchestrationLaunchUsesInjectedCWDOverStaleBinding(t *testing.T) {
	args := mustRawJSON(t, map[string]any{
		"name":     "idle-agent",
		"provider": "codex",
	})
	wantArgs := mustRawJSON(t, map[string]any{
		"cwd":       "/repo/current",
		"name":      "idle-agent",
		"parent_id": "agent-parent",
		"provider":  "codex",
	})
	h, _ := newHandlerForTest(newToolCallPeer(t, "orchestration_launch_agent", wantArgs, "launching", nil))
	h.bindingStore = &toolCallBindingStoreStub{bindingsByAgent: map[string]toolCallBinding{
		"agent-parent": {
			AgentID: "agent-parent",
			CWD:     "/repo/stale",
		},
	}}

	got, err := h.routeToolCall(context.Background(), ToolCallRequest{
		Name:      "orchestration_launch_agent",
		Arguments: args,
		AgentID:   "agent-parent",
		CWD:       "/repo/current",
	})
	if err != nil {
		t.Fatalf("routeToolCall() error = %v", err)
	}
	assertSingleTextItem(t, got, "launching", true)
}

func TestToolBridge_ForwardsInjectedCWDToPeer(t *testing.T) {
	args := mustRawJSON(t, map[string]any{"action": "read_file", "file_path": "go.mod"})
	peer := &mcpcontrol.ToolInstance{Peer: &stubPeer{callbackFn: func(_ context.Context, method string, params any, result any) error {
		if method != ProxyMethodToolsCall {
			t.Fatalf("Callback() method = %q, want %s", method, ProxyMethodToolsCall)
		}
		payload, ok := params.(map[string]any)
		if !ok {
			t.Fatalf("Callback() params type = %T, want map[string]any", params)
		}
		if got := payload[MetadataKeyCWD]; got != "/repo/wjboot-v2" {
			t.Fatalf("Callback() _cwd = %#v, want /repo/wjboot-v2", got)
		}
		resp, ok := result.(*peerToolCallResponse)
		if !ok {
			t.Fatalf("Callback() result type = %T, want *peerToolCallResponse", result)
		}
		*resp = peerToolCallResponse{Content: []peerToolCallContent{{Type: "text", Text: "ok"}}}
		return nil
	}}}
	h, registry := newHandlerForTest(peer)
	h.bindingStore = &toolCallBindingStoreStub{bindingsByAgent: map[string]toolCallBinding{
		"agent-1": {AgentID: "agent-1", CWD: "/stale/startup/root"},
	}}

	result, err := h.HandleToolCall(context.Background(), contract.ToolCallRawMessage{
		ID:     json.RawMessage(`1`),
		Method: "item/tool/call",
		Params: mustRawJSON(t, map[string]any{
			"name":      "lsp_file",
			"arguments": args,
			"agentId":   "agent-1",
			"_cwd":      "/repo/wjboot-v2",
		}),
	})
	if err != nil {
		t.Fatalf("HandleToolCall() error = %v", err)
	}
	got, ok := result.(*ToolCallResult)
	if !ok {
		t.Fatalf("HandleToolCall() result type = %T, want *ToolCallResult", result)
	}
	assertSingleTextItem(t, got, "ok", true)
	if len(registry.gotKinds) != 1 || registry.gotKinds[0] != dto.ClientKindLSP {
		t.Fatalf("FindActiveByKind() kinds = %#v, want [%q]", registry.gotKinds, dto.ClientKindLSP)
	}
}

func TestToolBridge_ScopedLSPPeerRoutingUsesTrustedMetadata(t *testing.T) {
	args := mustRawJSON(t, map[string]any{
		"action":    "read_file",
		"file_path": "go.mod",
		"agent_id":  "forged-agent",
		"cwd":       "/forged/root",
	})
	wrongPeer := &mcpcontrol.ToolInstance{
		AgentID:    "agent-a",
		ThreadID:   "thread-a",
		ClientKind: dto.ClientKindLSP,
		Peer: &stubPeer{callbackFn: func(context.Context, string, any, any) error {
			t.Fatal("wrong scoped peer was called")
			return nil
		}},
	}
	rightPeer := &mcpcontrol.ToolInstance{
		AgentID:    "agent-b",
		ThreadID:   "thread-b",
		ClientKind: dto.ClientKindLSP,
		Peer: &stubPeer{callbackFn: func(_ context.Context, method string, params any, result any) error {
			if method != ProxyMethodToolsCall {
				t.Fatalf("Callback() method = %q, want %s", method, ProxyMethodToolsCall)
			}
			payload, ok := params.(map[string]any)
			if !ok {
				t.Fatalf("Callback() params type = %T, want map[string]any", params)
			}
			if got := payload[MetadataKeyAgentID]; got != "agent-b" {
				t.Fatalf("Callback() _agentId = %#v, want agent-b", got)
			}
			if got := payload[MetadataKeyThreadID]; got != "thread-b" {
				t.Fatalf("Callback() _threadId = %#v, want thread-b", got)
			}
			if got := payload[MetadataKeyCallID]; got != "call-1" {
				t.Fatalf("Callback() _callId = %#v, want call-1", got)
			}
			if got := payload[MetadataKeyCWD]; got != "/trusted/root" {
				t.Fatalf("Callback() _cwd = %#v, want /trusted/root", got)
			}
			if _, ok := payload["agent_id"]; ok {
				t.Fatalf("Callback() leaked public agent_id in top-level payload: %#v", payload)
			}
			resp := result.(*peerToolCallResponse)
			*resp = peerToolCallResponse{Content: []peerToolCallContent{{Type: "text", Text: "ok"}}}
			return nil
		}},
	}
	h, registry := newHandlerForTest(wrongPeer, rightPeer)
	registry.scoped = true

	got, err := h.routeToolCall(context.Background(), ToolCallRequest{
		Name:      "lsp_file",
		Arguments: args,
		AgentID:   "agent-b",
		ThreadID:  "thread-b",
		CallID:    "call-1",
		CWD:       "/trusted/root",
	})
	if err != nil {
		t.Fatalf("routeToolCall() error = %v", err)
	}
	assertSingleTextItem(t, got, "ok", true)
	if len(registry.gotScopes) != 1 {
		t.Fatalf("FindActiveForScope() calls = %d, want 1", len(registry.gotScopes))
	}
	if scope := registry.gotScopes[0]; scope.AgentID != "agent-b" || scope.ThreadID != "thread-b" || scope.Family != dto.ClientKindLSP {
		t.Fatalf("FindActiveForScope() scope = %#v, want trusted agent/thread lsp", scope)
	}
}

func TestToolBridge_NoPeer_FailFast(t *testing.T) {
	h, _ := newHandlerForTest()

	got, err := h.routeToolCall(context.Background(), ToolCallRequest{Name: "lsp_hover", Arguments: json.RawMessage(`{}`)})
	if !errors.Is(err, ErrNoPeerAvailable) {
		t.Fatalf("routeToolCall() error = %v, want %v", err, ErrNoPeerAvailable)
	}
	if got != nil {
		t.Fatalf("routeToolCall() result = %#v, want nil", got)
	}
}

func TestToolBridge_MultiplePeers_Ambiguous(t *testing.T) {
	h, _ := newHandlerForTest(newToolCallPeer(t, "lsp_hover", json.RawMessage(`{}`), "ignored", nil), newToolCallPeer(t, "lsp_hover", json.RawMessage(`{}`), "ignored", nil))

	got, err := h.routeToolCall(context.Background(), ToolCallRequest{Name: "lsp_hover", Arguments: json.RawMessage(`{}`)})
	if !errors.Is(err, ErrAmbiguousPeer) {
		t.Fatalf("routeToolCall() error = %v, want %v", err, ErrAmbiguousPeer)
	}
	if got != nil {
		t.Fatalf("routeToolCall() result = %#v, want nil", got)
	}
}

func TestToolBridge_Timeout_120s(t *testing.T) {
	if toolCallTimeout != 120*time.Second {
		t.Fatalf("toolCallTimeout = %s, want %s", toolCallTimeout, 120*time.Second)
	}
}

func TestToolBridge_PeerError_AdaptToResult(t *testing.T) {
	peerErr := errors.New("peer callback failed")
	h, _ := newHandlerForTest(newToolCallPeer(t, "lsp_hover", json.RawMessage(`{}`), "", peerErr))

	got, err := h.routeToolCall(context.Background(), ToolCallRequest{Name: "lsp_hover", Arguments: json.RawMessage(`{}`)})
	if err != nil {
		t.Fatalf("routeToolCall() error = %v, want nil", err)
	}
	assertSingleTextItem(t, got, peerErr.Error(), false)
}

func TestToolBridge_RouteToolCall_RejectsMismatchedClientKind(t *testing.T) {
	h, registry := newHandlerForTest(newToolCallPeer(t, "lsp_hover", json.RawMessage(`{}`), "ignored", nil))

	got, err := h.routeToolCall(context.Background(), ToolCallRequest{
		Name:       "lsp_hover",
		Arguments:  json.RawMessage(`{}`),
		ClientKind: "orch",
	})
	if err == nil || !strings.Contains(err.Error(), `belongs to "lsp", not "orch"`) {
		t.Fatalf("routeToolCall() error = %v, want family mismatch", err)
	}
	if got != nil {
		t.Fatalf("routeToolCall() result = %#v, want nil", got)
	}
	if len(registry.gotKinds) != 0 {
		t.Fatalf("FindActiveByKind() kinds = %#v, want none", registry.gotKinds)
	}
}

func TestProxyRequest_RejectsOversizedBody(t *testing.T) {
	h, _ := newHandlerForTest()
	body := fmt.Sprintf(`{"jsonrpc":"2.0","id":"req-1","method":"tools/call","params":{"name":"lsp_hover","arguments":{"blob":"%s"}}}`,
		strings.Repeat("x", proxyMaxBodyBytes))

	got := callProxyRequest(t, h, "/mcp/lsp/agent-1", body)
	if got.Error == nil {
		t.Fatal("proxy response error = nil, want invalid params")
	}
	if got.Error.Code != jsonRPCCodeInvalidParam {
		t.Fatalf("proxy error code = %d, want %d", got.Error.Code, jsonRPCCodeInvalidParam)
	}
	if !strings.Contains(got.Error.Message, "request body too large") {
		t.Fatalf("proxy error message = %q, want request body too large", got.Error.Message)
	}
}

func TestProxyToolCall_SetsTimeoutAndNormalizesNullArguments(t *testing.T) {
	var deadline time.Time
	h, _ := newHandlerForTest(&mcpcontrol.ToolInstance{Peer: &stubPeer{callbackFn: func(ctx context.Context, method string, params any, result any) error {
		var ok bool
		deadline, ok = ctx.Deadline()
		if !ok {
			t.Fatal("Callback() context missing deadline")
		}
		assertToolCallPayload(t, params, "lsp_hover", json.RawMessage(`{}`))
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
			"name":      "lsp_hover",
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

func TestToolBridge_RejectsSpawnAgentWhenPersistentSubagentDefaultEnabled(t *testing.T) {
	args := mustRawJSON(t, map[string]any{"message": "create child agent"})
	h, registry := newHandlerForTest(&mcpcontrol.ToolInstance{Peer: &stubPeer{callbackFn: func(context.Context, string, any, any) error {
		t.Fatal("Callback() should not be invoked when spawn_agent is blocked")
		return nil
	}}})
	raw := mustRawJSON(t, map[string]any{
		"runtime": map[string]any{
			"enabledTools": []string{"spawn_agent", "orchestration_launch_agent"},
			"sessionFlags": map[string]any{"persistent_subagent_default": true},
		},
	})
	h.threadStore = &stubThreadStore{thread: &threadstore.Thread{ThreadID: "thread-1", ConfigOverride: raw}}
	h.cfg = &platformconfig.Config{Agent: platformconfig.AgentConfig{PersistentSubagentDefault: true}}

	got, err := h.routeToolCall(context.Background(), ToolCallRequest{
		Name:      "spawn_agent",
		Arguments: args,
		ThreadID:  "thread-1",
	})
	if err != nil {
		t.Fatalf("routeToolCall() error = %v", err)
	}
	assertSingleTextItem(t, got, "当前会话启用了 persistent_subagent_default：禁止使用 `spawn_agent` 创建临时子 agent。请改用 `orchestration_launch_agent` 创建持续化 UI 子 agent。", false)
	if len(registry.gotKinds) != 0 {
		t.Fatalf("FindActiveByKind() kinds = %#v, want none", registry.gotKinds)
	}
}

func TestPersistentSubagentAllowsExplicitRuntimeFlagFalse(t *testing.T) {
	args := mustRawJSON(t, map[string]any{"message": "create child agent"})
	h, registry := newHandlerForTest(newToolCallPeer(t, "spawn_agent", args, "spawned", nil))
	raw := mustRawJSON(t, map[string]any{
		"runtime": map[string]any{
			"enabledTools": []string{"spawn_agent", "orchestration_launch_agent"},
			"sessionFlags": map[string]any{"persistent_subagent_default": false},
		},
	})
	h.threadStore = &stubThreadStore{thread: &threadstore.Thread{ThreadID: "thread-flag-false", ConfigOverride: raw}}
	h.cfg = &platformconfig.Config{Agent: platformconfig.AgentConfig{PersistentSubagentDefault: true}}

	got, err := h.routeToolCall(context.Background(), ToolCallRequest{
		Name:      "spawn_agent",
		Arguments: args,
		ThreadID:  "thread-flag-false",
	})
	if err != nil {
		t.Fatalf("routeToolCall() error = %v", err)
	}
	assertSingleTextItem(t, got, "spawned", true)
	if len(registry.gotKinds) != 1 || registry.gotKinds[0] != dto.ClientKindOrch {
		t.Fatalf("FindActiveByKind() kinds = %#v, want [%q]", registry.gotKinds, dto.ClientKindOrch)
	}
}

func TestPersistentSubagentRequiresExplicitRuntimeFlag(t *testing.T) {
	h, registry := newHandlerForTest(&mcpcontrol.ToolInstance{Peer: &stubPeer{callbackFn: func(context.Context, string, any, any) error {
		t.Fatal("Callback() should not be invoked when persistent-subagent flag is absent")
		return nil
	}}})
	raw := mustRawJSON(t, map[string]any{
		"runtime": map[string]any{
			"enabledTools": []string{"spawn_agent", "orchestration_launch_agent"},
		},
	})
	h.threadStore = &stubThreadStore{thread: &threadstore.Thread{ThreadID: "thread-missing-flag", ConfigOverride: raw}}
	h.cfg = &platformconfig.Config{Agent: platformconfig.AgentConfig{PersistentSubagentDefault: true}}

	got, err := h.routeToolCall(context.Background(), ToolCallRequest{
		Name:      "spawn_agent",
		Arguments: mustRawJSON(t, map[string]any{"message": "create child agent"}),
		ThreadID:  "thread-missing-flag",
	})
	if !errors.Is(err, contract.ErrPersistentSubagentFlagRequired) {
		t.Fatalf("routeToolCall() error = %v, want %v", err, contract.ErrPersistentSubagentFlagRequired)
	}
	if got != nil {
		t.Fatalf("routeToolCall() result = %#v, want nil", got)
	}
	if len(registry.gotKinds) != 0 {
		t.Fatalf("FindActiveByKind() kinds = %#v, want none", registry.gotKinds)
	}
}

func TestPersistentSubagentAllowsLegacyOptInFallback(t *testing.T) {
	t.Setenv(allowDefaultPersistentSubagentEnv, "1")
	var logs bytes.Buffer
	h, registry := newHandlerForTest(&mcpcontrol.ToolInstance{Peer: &stubPeer{callbackFn: func(context.Context, string, any, any) error {
		t.Fatal("Callback() should not be invoked when legacy fallback blocks spawn_agent")
		return nil
	}}})
	h.logger = slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	raw := mustRawJSON(t, map[string]any{
		"runtime": map[string]any{
			"enabledTools": []string{"spawn_agent", "orchestration_launch_agent"},
		},
	})
	h.threadStore = &stubThreadStore{thread: &threadstore.Thread{ThreadID: "thread-legacy-fallback", ConfigOverride: raw}}
	h.cfg = &platformconfig.Config{Agent: platformconfig.AgentConfig{PersistentSubagentDefault: true}}
	before := persistentSubagentDefaultFallbackCount()

	got, err := h.routeToolCall(context.Background(), ToolCallRequest{
		Name:      "spawn_agent",
		Arguments: mustRawJSON(t, map[string]any{"message": "create child agent"}),
		ThreadID:  "thread-legacy-fallback",
	})
	if err != nil {
		t.Fatalf("routeToolCall() error = %v", err)
	}
	assertSingleTextItem(t, got, "当前会话启用了 persistent_subagent_default：禁止使用 `spawn_agent` 创建临时子 agent。请改用 `orchestration_launch_agent` 创建持续化 UI 子 agent。", false)
	if after := persistentSubagentDefaultFallbackCount(); after != before+1 {
		t.Fatalf("persistentSubagentDefaultFallbackCount() = %d, want %d", after, before+1)
	}
	if !strings.Contains(logs.String(), "compatibility-only: persistent subagent default fallback") {
		t.Fatalf("logs = %q, want compatibility warning", logs.String())
	}
	if len(registry.gotKinds) != 0 {
		t.Fatalf("FindActiveByKind() kinds = %#v, want none", registry.gotKinds)
	}
}

func TestProxyMapsPersistentSubagentFlagRequired(t *testing.T) {
	h, registry := newHandlerForTest(&mcpcontrol.ToolInstance{Peer: &stubPeer{callbackFn: func(context.Context, string, any, any) error {
		t.Fatal("Callback() should not be invoked when persistent-subagent flag is absent")
		return nil
	}}})
	raw := mustRawJSON(t, map[string]any{
		"runtime": map[string]any{
			"enabledTools": []string{"spawn_agent", "orchestration_launch_agent"},
		},
	})
	h.bindingStore = &toolCallBindingStoreStub{threadID: "thread-proxy-missing-flag"}
	h.threadStore = &stubThreadStore{thread: &threadstore.Thread{ThreadID: "thread-proxy-missing-flag", ConfigOverride: raw}}
	body := string(mustRawJSON(t, map[string]any{
		"jsonrpc": "2.0",
		"id":      "req-flag",
		"method":  "tools/call",
		"params": map[string]any{
			"name":      "spawn_agent",
			"arguments": map[string]any{},
		},
	}))

	got := callProxyRequest(t, h, "/mcp/orch/agent-flag", body)
	if got.Error == nil {
		t.Fatal("proxy response error = nil, want invalid params")
	}
	if got.Error.Code != jsonRPCCodeInvalidParam {
		t.Fatalf("proxy error code = %d, want %d", got.Error.Code, jsonRPCCodeInvalidParam)
	}
	if !strings.Contains(got.Error.Message, contract.ErrPersistentSubagentFlagRequired.Error()) {
		t.Fatalf("proxy error message = %q, want substring %q", got.Error.Message, contract.ErrPersistentSubagentFlagRequired.Error())
	}
	if len(registry.gotKinds) != 0 {
		t.Fatalf("FindActiveByKind() kinds = %#v, want none", registry.gotKinds)
	}
}

func TestToolBridge_RejectsSpawnAgentWithoutThreadRuntime(t *testing.T) {
	h, _ := newHandlerForTest()

	got, err := h.routeToolCall(context.Background(), ToolCallRequest{
		Name:      "spawn_agent",
		Arguments: mustRawJSON(t, map[string]any{"message": "create child agent"}),
	})
	if !errors.Is(err, contract.ErrThreadRuntimeRequired) {
		t.Fatalf("routeToolCall() error = %v, want %v", err, contract.ErrThreadRuntimeRequired)
	}
	if got != nil {
		t.Fatalf("routeToolCall() result = %#v, want nil", got)
	}
}

func TestToolBridge_RejectsSpawnAgentWithoutStoredRuntime(t *testing.T) {
	h, _ := newHandlerForTest()
	h.threadStore = &stubThreadStore{}

	got, err := h.routeToolCall(context.Background(), ToolCallRequest{
		Name:      "spawn_agent",
		Arguments: mustRawJSON(t, map[string]any{"message": "create child agent"}),
		ThreadID:  "thread-missing-runtime",
	})
	if !errors.Is(err, contract.ErrPersistentSubagentRuntimeRequired) {
		t.Fatalf("routeToolCall() error = %v, want %v", err, contract.ErrPersistentSubagentRuntimeRequired)
	}
	if got != nil {
		t.Fatalf("routeToolCall() result = %#v, want nil", got)
	}
}

func TestProxyToolCall_RejectsMissingRuntimeAsInvalidParams(t *testing.T) {
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

	got := callProxyRequest(t, h, "/mcp/orch/agent-1", body)
	if got.Error == nil {
		t.Fatal("proxy response error = nil, want invalid params")
	}
	if got.Error.Code != jsonRPCCodeInvalidParam {
		t.Fatalf("proxy error code = %d, want %d", got.Error.Code, jsonRPCCodeInvalidParam)
	}
	if !strings.Contains(got.Error.Message, contract.ErrThreadRuntimeRequired.Error()) {
		t.Fatalf("proxy error message = %q, want substring %q", got.Error.Message, contract.ErrThreadRuntimeRequired.Error())
	}
	if len(registry.gotKinds) != 0 {
		t.Fatalf("FindActiveByKind() kinds = %#v, want none", registry.gotKinds)
	}
}

func TestProxyToolsList_OrchIncludesHostAndSurvivesPeerDown(t *testing.T) {
	host := &stubHostToolRegistry{tools: []dto.MCPTool{{Name: "memory_write", Description: "host memory"}}}
	h := &Handler{
		registry: &stubKindRegistry{peers: map[string][]*mcpcontrol.ToolInstance{
			dto.ClientKindOrch: {listToolsPeer(nil, errors.New("orch down"))},
		}},
		hostTools: host,
	}

	got := callProxyRequest(t, h, "/mcp/orch/agent-1", `{"jsonrpc":"2.0","id":"req-1","method":"tools/list"}`)
	if got.Error != nil {
		t.Fatalf("proxy tools/list error = %+v, want result", got.Error)
	}
	result, ok := got.Result.(map[string]any)
	if !ok {
		t.Fatalf("result type = %T, want object", got.Result)
	}
	tools, ok := result["tools"].([]any)
	if !ok || len(tools) != 1 {
		t.Fatalf("tools = %#v, want one host tool", result["tools"])
	}
	tool, ok := tools[0].(map[string]any)
	if !ok || tool["name"] != "memory_write" {
		t.Fatalf("tool = %#v, want memory_write", tools[0])
	}
}

func TestProxyToolsList_LSPDoesNotIncludeHostMemory(t *testing.T) {
	host := &stubHostToolRegistry{tools: []dto.MCPTool{{Name: "memory_write", Description: "host memory"}}}
	h := &Handler{
		registry: &stubKindRegistry{peers: map[string][]*mcpcontrol.ToolInstance{
			dto.ClientKindLSP: {listToolsPeer([]dto.MCPTool{{Name: "lsp_hover", Description: "lsp"}}, nil)},
		}},
		hostTools: host,
	}

	got := callProxyRequest(t, h, "/mcp/lsp/agent-1", `{"jsonrpc":"2.0","id":"req-1","method":"tools/list"}`)
	if got.Error != nil {
		t.Fatalf("proxy tools/list error = %+v", got.Error)
	}
	result, ok := got.Result.(map[string]any)
	if !ok {
		t.Fatalf("result type = %T, want object", got.Result)
	}
	tools, ok := result["tools"].([]any)
	if !ok || len(tools) != 1 {
		t.Fatalf("tools = %#v, want one lsp tool", result["tools"])
	}
	tool := tools[0].(map[string]any)
	if tool["name"] != "lsp_hover" {
		t.Fatalf("tool = %#v, want lsp_hover", tool)
	}
}

func TestProxyToolsList_LSPFiltersPeerMemoryRead(t *testing.T) {
	h := &Handler{registry: &stubKindRegistry{peers: map[string][]*mcpcontrol.ToolInstance{
		dto.ClientKindLSP: {listToolsPeer([]dto.MCPTool{{Name: ToolNameMemoryRead, Description: "peer memory"}, {Name: "lsp_hover", Description: "lsp"}}, nil)},
	}}}

	got := callProxyRequest(t, h, "/mcp/lsp/agent-1", `{"jsonrpc":"2.0","id":"req-1","method":"tools/list"}`)
	if got.Error != nil {
		t.Fatalf("proxy tools/list error = %+v", got.Error)
	}
	result := got.Result.(map[string]any)
	tools := result["tools"].([]any)
	if proxyToolsContainName(tools, ToolNameMemoryRead) {
		t.Fatalf("tools = %#v, must filter peer memory_read for non-orch proxy list", tools)
	}
	if !proxyToolsContainName(tools, "lsp_hover") {
		t.Fatalf("tools = %#v, want non-memory peer tool preserved", tools)
	}
}

func TestProxyToolsList_OrchIncludesHostMemoryRead(t *testing.T) {
	host := NewMemoryReadHostToolRegistry(&stubAgentMemoryReader{enabled: true, toolsEnabled: true}, MemoryReadHostToolOptions{Enabled: true, ToolsEnabled: true})
	h := &Handler{registry: &stubKindRegistry{peers: map[string][]*mcpcontrol.ToolInstance{dto.ClientKindOrch: {listToolsPeer([]dto.MCPTool{{Name: "orchestration_launch_agent", Description: "peer orch"}}, nil)}}}, hostTools: host}

	got := callProxyRequest(t, h, "/mcp/orch/agent-1", `{"jsonrpc":"2.0","id":"req-1","method":"tools/list"}`)
	if got.Error != nil {
		t.Fatalf("proxy tools/list error = %+v", got.Error)
	}
	result := got.Result.(map[string]any)
	tools := result["tools"].([]any)
	if !proxyToolsContainName(tools, ToolNameMemoryRead) {
		t.Fatalf("tools = %#v, want memory_read", tools)
	}
	if !proxyToolsContainName(tools, "orchestration_launch_agent") {
		t.Fatalf("tools = %#v, want non-memory peer tool preserved", tools)
	}
}

func TestProxyToolsList_OrchFiltersPeerMemoryReadWhenReaderUnavailable(t *testing.T) {
	h := &Handler{registry: &stubKindRegistry{peers: map[string][]*mcpcontrol.ToolInstance{
		dto.ClientKindOrch: {listToolsPeer([]dto.MCPTool{{Name: ToolNameMemoryRead, Description: "peer memory"}, {Name: "orchestration_launch_agent", Description: "peer orch"}}, nil)},
	}}}

	got := callProxyRequest(t, h, "/mcp/orch/agent-1", `{"jsonrpc":"2.0","id":"req-1","method":"tools/list"}`)
	if got.Error != nil {
		t.Fatalf("proxy tools/list error = %+v", got.Error)
	}
	result := got.Result.(map[string]any)
	tools := result["tools"].([]any)
	if proxyToolsContainName(tools, ToolNameMemoryRead) {
		t.Fatalf("tools = %#v, must filter peer memory_read when host reader is unavailable", tools)
	}
	if !proxyToolsContainName(tools, "orchestration_launch_agent") {
		t.Fatalf("tools = %#v, want non-memory peer tool preserved", tools)
	}
}

func TestProxyToolsList_OrchFiltersPeerMemoryReadWhenMemoryReadToolsDisabled(t *testing.T) {
	reader := &stubAgentMemoryReader{enabled: true, toolsEnabled: true}
	host := NewMemoryReadHostToolRegistry(reader, MemoryReadHostToolOptions{Enabled: true, ToolsEnabled: false})
	h := &Handler{hostTools: host, registry: &stubKindRegistry{peers: map[string][]*mcpcontrol.ToolInstance{
		dto.ClientKindOrch: {listToolsPeer([]dto.MCPTool{{Name: ToolNameMemoryRead, Description: "peer memory"}, {Name: "orchestration_launch_agent", Description: "peer orch"}}, nil)},
	}}}

	got := callProxyRequest(t, h, "/mcp/orch/agent-1", `{"jsonrpc":"2.0","id":"req-1","method":"tools/list"}`)
	if got.Error != nil {
		t.Fatalf("proxy tools/list error = %+v", got.Error)
	}
	result := got.Result.(map[string]any)
	tools := result["tools"].([]any)
	if proxyToolsContainName(tools, ToolNameMemoryRead) {
		t.Fatalf("tools = %#v, must filter peer memory_read when memory_read tools disabled", tools)
	}
	if !proxyToolsContainName(tools, "orchestration_launch_agent") {
		t.Fatalf("tools = %#v, want non-memory peer tool preserved", tools)
	}
}

func proxyToolsContainName(tools []any, name string) bool {
	for _, raw := range tools {
		tool, ok := raw.(map[string]any)
		if ok && tool["name"] == name {
			return true
		}
	}
	return false
}

func TestProxyToolCall_MemoryReadUsesHostDirect(t *testing.T) {
	reader := &stubAgentMemoryReader{enabled: true, toolsEnabled: true}
	host := NewMemoryReadHostToolRegistry(reader, MemoryReadHostToolOptions{Enabled: true, ToolsEnabled: true})
	registry := &stubKindRegistry{}
	h := &Handler{registry: registry, hostTools: host, resolver: &stubCWDResolver{cwd: "/repo"}}
	body := string(mustRawJSON(t, map[string]any{"jsonrpc": "2.0", "id": "read-1", "method": "tools/call", "params": map[string]any{"name": ToolNameMemoryRead, "arguments": map[string]any{"name": "daily", "scope": "user"}}}))

	got := callProxyRequest(t, h, "/mcp/orch/agent-read", body)
	if got.Error != nil {
		t.Fatalf("proxy tools/call error = %+v", got.Error)
	}
	result := got.Result.(map[string]any)
	if result["isError"] == true || reader.calls != 1 {
		t.Fatalf("result=%#v reader.calls=%d, want success host call", result, reader.calls)
	}
	if reader.last.AgentID != "agent-read" || reader.last.CWD != "/repo" || reader.last.CallID != "read-1" {
		t.Fatalf("reader request = %+v", reader.last)
	}
	if len(registry.gotKinds) != 0 {
		t.Fatalf("FindActiveByKind() kinds = %#v, want none", registry.gotKinds)
	}
}

func TestProxyToolsList_HidesMemoryReadWhenToolsDisabled(t *testing.T) {
	reader := &stubAgentMemoryReader{enabled: true, toolsEnabled: true}
	host := NewMemoryReadHostToolRegistry(reader, MemoryReadHostToolOptions{Enabled: true, ToolsEnabled: false})
	h := &Handler{hostTools: host, registry: &stubKindRegistry{peers: map[string][]*mcpcontrol.ToolInstance{dto.ClientKindOrch: {listToolsPeer([]dto.MCPTool{{Name: "orchestration_launch_agent", Description: "peer orch"}}, nil)}}}}

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
	h := &Handler{hostTools: host, registry: registry}
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
	h := &Handler{hostTools: host, registry: registry}
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
	h := &Handler{hostTools: host, registry: registry}
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
	h := &Handler{hostTools: host, registry: &stubKindRegistry{}}
	body := string(mustRawJSON(t, map[string]any{"jsonrpc": "2.0", "id": "read-tools-off", "method": "tools/call", "params": map[string]any{"name": ToolNameMemoryRead, "arguments": map[string]any{"name": "daily"}}}))

	got := callProxyRequest(t, h, "/mcp/orch/agent-read", body)
	assertProxyToolErrorEnvelope(t, got, ToolNameMemoryRead, "tools_disabled")
}

func TestProxyToolCall_MemoryReadFeatureDisabledReturnsToolErrorEnvelope(t *testing.T) {
	reader := &stubAgentMemoryReader{enabled: true, toolsEnabled: true}
	host := NewMemoryReadHostToolRegistry(reader, MemoryReadHostToolOptions{Enabled: false, ToolsEnabled: true})
	h := &Handler{hostTools: host, registry: &stubKindRegistry{}}
	body := string(mustRawJSON(t, map[string]any{"jsonrpc": "2.0", "id": "read-feature-off", "method": "tools/call", "params": map[string]any{"name": ToolNameMemoryRead, "arguments": map[string]any{"name": "daily"}}}))

	got := callProxyRequest(t, h, "/mcp/orch/agent-read", body)
	assertProxyToolErrorEnvelope(t, got, ToolNameMemoryRead, "feature_disabled")
}

func TestProxyToolCall_StaleMemoryReadCallReturnsStableToolError(t *testing.T) {
	h := &Handler{}
	body := string(mustRawJSON(t, map[string]any{"jsonrpc": "2.0", "id": "read-stale", "method": "tools/call", "params": map[string]any{"name": ToolNameMemoryRead, "arguments": map[string]any{"name": "daily"}}}))

	got := callProxyRequest(t, h, "/mcp/orch/agent-read", body)
	assertProxyToolErrorEnvelope(t, got, ToolNameMemoryRead, "reader_unavailable")
}

func TestProxyToolCall_MemoryReadReaderErrorUsesToolErrorNotJSONRPCError(t *testing.T) {
	reader := &stubAgentMemoryReader{enabled: true, toolsEnabled: true, err: contract.NewAgentMemoryError("not_found", errors.New("missing"))}
	host := NewMemoryReadHostToolRegistry(reader, MemoryReadHostToolOptions{Enabled: true, ToolsEnabled: true})
	h := &Handler{hostTools: host, registry: &stubKindRegistry{}}
	body := string(mustRawJSON(t, map[string]any{"jsonrpc": "2.0", "id": "read-missing", "method": "tools/call", "params": map[string]any{"name": ToolNameMemoryRead, "arguments": map[string]any{"name": "missing"}}}))

	got := callProxyRequest(t, h, "/mcp/orch/agent-read", body)
	assertProxyToolErrorEnvelope(t, got, ToolNameMemoryRead, "not_found")
}

func TestProxyToolCall_MemoryReadUnsupportedScopeReturnsToolErrorEnvelope(t *testing.T) {
	reader := &stubAgentMemoryReader{enabled: true, toolsEnabled: true, err: contract.NewAgentMemoryError("unsupported_scope", errors.New("unsupported"))}
	host := NewMemoryReadHostToolRegistry(reader, MemoryReadHostToolOptions{Enabled: true, ToolsEnabled: true})
	h := &Handler{hostTools: host, registry: &stubKindRegistry{}}
	body := string(mustRawJSON(t, map[string]any{"jsonrpc": "2.0", "id": "read-unsupported", "method": "tools/call", "params": map[string]any{"name": ToolNameMemoryRead, "arguments": map[string]any{"name": "x", "scope": "project"}}}))

	got := callProxyRequest(t, h, "/mcp/orch/agent-read", body)
	assertProxyToolErrorEnvelope(t, got, ToolNameMemoryRead, "unsupported_scope")
}

func TestProxyToolCall_MemoryReadReaderUnavailableReturnsToolErrorEnvelope(t *testing.T) {
	host := &MemoryReadHostToolRegistry{opts: MemoryReadHostToolOptions{Enabled: true, ToolsEnabled: true}}
	registry := &stubKindRegistry{}
	h := &Handler{registry: registry, hostTools: host}
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
	h := &Handler{registry: registry, hostTools: host}
	body := string(mustRawJSON(t, map[string]any{"jsonrpc": "2.0", "id": "read-bad", "method": "tools/call", "params": map[string]any{"name": ToolNameMemoryRead, "arguments": "not-object"}}))

	got := callProxyRequest(t, h, "/mcp/orch/agent-read", body)
	assertProxyToolErrorEnvelope(t, got, ToolNameMemoryRead, "invalid_input")
	if reader.calls != 0 || len(registry.gotKinds) != 0 {
		t.Fatalf("reader.calls=%d registry kinds=%#v, want no reader or peer", reader.calls, registry.gotKinds)
	}
}

func assertProxyToolErrorEnvelope(t *testing.T, got proxyJSONRPCResponse, toolName, code string) {
	t.Helper()
	if got.Error != nil {
		t.Fatalf("proxy response error = %+v, want JSON-RPC result", got.Error)
	}
	result, ok := got.Result.(map[string]any)
	if !ok {
		t.Fatalf("result type = %T, want object", got.Result)
	}
	if isError, _ := result["isError"].(bool); !isError {
		t.Fatalf("isError = %v, want true (result=%#v)", result["isError"], result)
	}
	content, ok := result["content"].([]any)
	if !ok || len(content) == 0 {
		t.Fatalf("content = %#v, want text envelope", result["content"])
	}
	text, _ := content[0].(map[string]any)["text"].(string)
	var envelope map[string]any
	if err := json.Unmarshal([]byte(text), &envelope); err != nil {
		t.Fatalf("content text json error = %v text=%q", err, text)
	}
	if envelope["kind"] != "host_tool_error" || envelope["tool"] != toolName || envelope["code"] != code {
		t.Fatalf("envelope = %#v, want tool=%q code=%q", envelope, toolName, code)
	}
	if strings.TrimSpace(fmt.Sprint(envelope["error"])) == "" {
		t.Fatalf("envelope missing non-empty error: %#v", envelope)
	}
}

func TestProxyToolCall_MemoryWriteUsesHostDirect(t *testing.T) {
	writer := &stubAgentMemoryWriter{}
	host := NewMemoryWriteHostToolRegistry(writer, MemoryWriteHostToolOptions{Enabled: true, ToolsEnabled: true})
	resolver := &stubCWDResolver{cwd: "/repo"}
	registry := &stubKindRegistry{}
	h := &Handler{registry: registry, hostTools: host, resolver: resolver}

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
	if writer.calls != 1 {
		t.Fatalf("writer calls = %d, want 1", writer.calls)
	}
	if writer.last.Name != "agent-memory-visible" || writer.last.AgentID != "agent-claude" || writer.last.CWD != "/repo" || writer.last.CallID != "req-memory" || writer.last.Source != "agent_tool" {
		t.Fatalf("writer request = %+v", writer.last)
	}
	if len(registry.gotKinds) != 0 {
		t.Fatalf("FindActiveByKind() kinds = %#v, want none", registry.gotKinds)
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
