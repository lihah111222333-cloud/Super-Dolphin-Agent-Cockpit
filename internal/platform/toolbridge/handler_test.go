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
	peers    []*mcpcontrol.ToolInstance
	gotKinds []string
}

func (r *stubRegistry) FindActiveByKind(clientKind string) []*mcpcontrol.ToolInstance {
	r.gotKinds = append(r.gotKinds, clientKind)
	return r.peers
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
	msg := codexapp.RawMessage{Params: mustRawJSON(t, map[string]any{
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
