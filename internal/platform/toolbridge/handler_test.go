package toolbridge

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
	"unsafe"

	"github.com/anthropic-ai/super-agent-v3/internal/platform/mcpcontrol"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/rpc"
	"github.com/anthropic-ai/super-agent-v3/internal/provider/codexapp"
	"github.com/anthropic-ai/super-agent-v3/internal/provider/unified"
	"github.com/gorilla/websocket"
)

//go:linkname codexNewSession github.com/anthropic-ai/super-agent-v3/internal/provider/codexapp.newSession
func codexNewSession(context.Context, *slog.Logger, string, string, *unified.EventDispatcher, *rpc.ApprovalManager, *codexapp.ServerManager) (unsafe.Pointer, error)

//go:linkname codexSessionOnInboundMessage github.com/anthropic-ai/super-agent-v3/internal/provider/codexapp.(*session).onInboundMessage
func codexSessionOnInboundMessage(unsafe.Pointer, context.Context, codexapp.Responder, codexapp.RawMessage)

//go:linkname codexSessionClose github.com/anthropic-ai/super-agent-v3/internal/provider/codexapp.(*session).Close
func codexSessionClose(unsafe.Pointer, context.Context) error

//go:linkname codexWaitReadLoopStopped github.com/anthropic-ai/super-agent-v3/internal/provider/codexapp.(*session).waitReadLoopStopped
func codexWaitReadLoopStopped(unsafe.Pointer, context.Context) error

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
		if err := codexSessionClose(sessionPtr, context.Background()); err != nil {
			t.Errorf("Close() error = %v", err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := codexWaitReadLoopStopped(sessionPtr, ctx); err != nil {
			t.Errorf("waitReadLoopStopped() error = %v", err)
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

