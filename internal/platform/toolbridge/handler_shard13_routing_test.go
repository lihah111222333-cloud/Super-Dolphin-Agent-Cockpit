package toolbridge

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/mcp"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/mcpcontrol"
)

func TestToolBridge_ForwardsInjectedCWDToPeer(t *testing.T) {
	root := t.TempDir()
	args := mustRawJSON(t, map[string]any{"action": "read_file", "file_path": "go.mod"})
	peer := &mcpcontrol.ToolInstance{Peer: &stubPeer{callbackFn: func(_ context.Context, method string, params any, result any) error {
		if method != ProxyMethodToolsCall {
			t.Fatalf("Callback() method = %q, want %s", method, ProxyMethodToolsCall)
		}
		payload, ok := params.(map[string]any)
		if !ok {
			t.Fatalf("Callback() params type = %T, want map[string]any", params)
		}
		if got := payload[MetadataKeyCWD]; got != root {
			t.Fatalf("Callback() _cwd = %#v, want %s", got, root)
		}
		resp, ok := result.(*peerToolCallResponse)
		if !ok {
			t.Fatalf("Callback() result type = %T, want *peerToolCallResponse", result)
		}
		*resp = peerToolCallResponse{Content: []peerToolCallContent{{Type: "text", Text: "ok"}}}
		return nil
	}}}
	h, registry := newHandlerForTest(peer)
	registry.scoped = true

	got, err := h.routeToolCall(context.Background(), ToolCallRequest{
		Name:      "file",
		Arguments: args,
		AgentID:   "agent-1",
		CWD:       root,
	})
	if err != nil {
		t.Fatalf("routeToolCall() error = %v", err)
	}
	assertSingleTextItem(t, got, "ok", true)
	if len(registry.gotScopes) != 1 || registry.gotScopes[0].CWD != root {
		t.Fatalf("FindActiveForScope() scopes = %#v, want cwd %q", registry.gotScopes, root)
	}
}

func TestAdaptMCPResponseMarksStructuredFailureAsFailure(t *testing.T) {
	t.Parallel()

	result, err := adaptMCPResponse(peerToolCallResponse{
		Content:           []peerToolCallContent{{Type: "text", Text: "failed"}},
		StructuredContent: json.RawMessage(`{"success":false,"error":"boom"}`),
	})
	if err != nil {
		t.Fatalf("adaptMCPResponse() error = %v", err)
	}
	if result.Success {
		t.Fatalf("Success = true, want false for structuredContent success=false")
	}
}

func TestAdaptMCPResponseAllowsStructuredJSONStringPayload(t *testing.T) {
	t.Parallel()

	raw := json.RawMessage(`"{\"path\":\"go.mod\",\"content\":\"module example\"}"`)
	result, err := adaptMCPResponse(peerToolCallResponse{
		IsError:           false,
		StructuredContent: raw,
	})
	if err != nil {
		t.Fatalf("adaptMCPResponse() error = %v", err)
	}
	if !result.Success {
		t.Fatalf("Success = false, want true for structuredContent JSON string")
	}
	var structured map[string]string
	if err := json.Unmarshal(result.StructuredContent, &structured); err != nil {
		t.Fatalf("StructuredContent = %s, want object: %v", result.StructuredContent, err)
	}
	if structured["value"] != `{"path":"go.mod","content":"module example"}` {
		t.Fatalf("StructuredContent value = %q, want wrapped JSON string", structured["value"])
	}
}

func TestAdaptMCPResponseWrapsStructuredJSONArrayPayload(t *testing.T) {
	t.Parallel()

	result, err := adaptMCPResponse(peerToolCallResponse{
		StructuredContent: json.RawMessage(`[{"name":"targetName"},{"name":"useTarget"}]`),
	})
	if err != nil {
		t.Fatalf("adaptMCPResponse() error = %v", err)
	}
	if !result.Success {
		t.Fatalf("Success = false, want true for structuredContent JSON array")
	}
	var structured struct {
		Items []map[string]any `json:"items"`
		Total int              `json:"total"`
	}
	if err := json.Unmarshal(result.StructuredContent, &structured); err != nil {
		t.Fatalf("StructuredContent = %s, want object: %v", result.StructuredContent, err)
	}
	if structured.Total != 2 || len(structured.Items) != 2 {
		t.Fatalf("StructuredContent = %s, want total/items wrapper", result.StructuredContent)
	}
}

func TestHandleToolCallPreservesPeerCallbackIsError(t *testing.T) {
	peer := &mcpcontrol.ToolInstance{Peer: &stubPeer{callbackFn: func(_ context.Context, method string, _ any, result any) error {
		if method != ProxyMethodToolsCall {
			t.Fatalf("Callback() method = %q, want %s", method, ProxyMethodToolsCall)
		}
		resp, ok := result.(*peerToolCallResponse)
		if !ok {
			t.Fatalf("Callback() result type = %T, want *peerToolCallResponse", result)
		}
		resp.IsError = true
		resp.Content = []peerToolCallContent{{Type: "text", Text: `{"success":false,"code":"path_outside_workspace"}`}}
		resp.StructuredContent = json.RawMessage(`{"success":false,"code":"path_outside_workspace"}`)
		return nil
	}}}
	h, _ := newHandlerForTest(peer)

	result, err := h.HandleToolCall(context.Background(), contract.ToolCallRawMessage{
		ID:     json.RawMessage(`1`),
		Method: "item/tool/call",
		Params: mustRawJSON(t, map[string]any{
			"name":       "file",
			"arguments":  map[string]any{},
			"clientKind": dto.ClientKindLSP,
		}),
	})
	if err != nil {
		t.Fatalf("HandleToolCall() error = %v", err)
	}
	got, ok := result.(*ToolCallResult)
	if !ok {
		t.Fatalf("HandleToolCall() result type = %T, want *ToolCallResult", result)
	}
	if got == nil || got.Success {
		t.Fatalf("HandleToolCall() = %#v, want Success false", got)
	}
}

type trustedScopePayload struct {
	agentID        string
	threadID       string
	callID         string
	cwd            string
	workspaceRoots []string
	replyText      string
}

func newTrustedLSPPeer(t *testing.T, want trustedScopePayload) *mcpcontrol.ToolInstance {
	t.Helper()
	return &mcpcontrol.ToolInstance{
		AgentID:    want.agentID,
		ThreadID:   want.threadID,
		ClientKind: dto.ClientKindLSP,
		Peer: &stubPeer{callbackFn: func(_ context.Context, method string, params any, result any) error {
			if method != ProxyMethodToolsCall {
				t.Fatalf("Callback() method = %q, want %s", method, ProxyMethodToolsCall)
			}
			assertTrustedScopePayload(t, params, want)
			resp := result.(*peerToolCallResponse)
			*resp = peerToolCallResponse{Content: []peerToolCallContent{{Type: "text", Text: want.replyText}}}
			return nil
		}},
	}
}

func assertTrustedScopePayload(t *testing.T, params any, want trustedScopePayload) {
	t.Helper()
	payload, ok := params.(map[string]any)
	if !ok {
		t.Fatalf("Callback() params type = %T, want map[string]any", params)
	}
	if got := payload[MetadataKeyAgentID]; got != want.agentID {
		t.Fatalf("Callback() _agentId = %#v, want %s", got, want.agentID)
	}
	if got := payload[MetadataKeyThreadID]; got != want.threadID {
		t.Fatalf("Callback() _threadId = %#v, want %s", got, want.threadID)
	}
	if got := payload[MetadataKeyCallID]; got != want.callID {
		t.Fatalf("Callback() _callId = %#v, want %s", got, want.callID)
	}
	if got := payload[MetadataKeyCWD]; got != want.cwd {
		t.Fatalf("Callback() _cwd = %#v, want %s", got, want.cwd)
	}
	if want.workspaceRoots != nil {
		assertTrustedWorkspaceRootsPayload(t, payload[MetadataKeyWorkspaceRoots], want.workspaceRoots)
	}
	if _, ok := payload["agent_id"]; ok {
		t.Fatalf("Callback() leaked public agent_id in top-level payload: %#v", payload)
	}
}

func assertTrustedWorkspaceRootsPayload(t *testing.T, raw any, want []string) {
	t.Helper()
	got, ok := raw.([]string)
	if !ok {
		t.Fatalf("Callback() _workspaceRoots = %#v, want []string", raw)
	}
	if len(got) != len(want) {
		t.Fatalf("Callback() _workspaceRoots = %#v, want %#v", got, want)
	}
	for i, wantRoot := range want {
		if got[i] != wantRoot {
			t.Fatalf("Callback() _workspaceRoots[%d] = %q, want %q; all roots %#v", i, got[i], wantRoot, got)
		}
	}
}

func TestToolBridge_ForwardsTrustedWorkspaceRootsToPeer(t *testing.T) {
	root := t.TempDir()
	extra := filepath.Join(root, "packages", "api")
	args := mustRawJSON(t, map[string]any{
		"action":          "read_file",
		"file_path":       "go.mod",
		"_workspaceRoots": []string{"/forged/arguments"},
	})
	peer := newTrustedLSPPeer(t, trustedScopePayload{
		agentID:        "agent-roots",
		threadID:       "thread-roots",
		callID:         "call-roots",
		cwd:            root,
		workspaceRoots: []string{root, extra},
		replyText:      "roots forwarded",
	})
	h, registry := newHandlerForTest(peer)
	registry.scoped = true

	got, err := h.routeToolCall(context.Background(), ToolCallRequest{
		Name:           "file",
		Arguments:      args,
		AgentID:        "agent-roots",
		ThreadID:       "thread-roots",
		CallID:         "call-roots",
		CWD:            root,
		WorkspaceRoots: []string{root, extra},
	})
	if err != nil {
		t.Fatalf("routeToolCall() error = %v", err)
	}
	assertSingleTextItem(t, got, "roots forwarded", true)
}

func TestToolBridge_DoesNotTrustRelativePrimaryRoot(t *testing.T) {
	args := mustRawJSON(t, map[string]any{"action": "read_file", "file_path": "go.mod"})
	peer := newTrustedLSPPeer(t, trustedScopePayload{
		agentID: "agent-rel", threadID: "thread-rel", callID: "call-rel", cwd: "", workspaceRoots: nil, replyText: "relative dropped",
	})
	h, registry := newHandlerForTest(peer)
	registry.scoped = true

	got, err := h.routeToolCall(context.Background(), ToolCallRequest{
		Name:           "file",
		Arguments:      args,
		AgentID:        "agent-rel",
		ThreadID:       "thread-rel",
		CallID:         "call-rel",
		CWD:            ".",
		WorkspaceRoots: []string{"packages/api"},
	})
	if err != nil {
		t.Fatalf("routeToolCall() error = %v", err)
	}
	assertSingleTextItem(t, got, "relative dropped", true)
}

func TestToolBridge_ForwardsAbsoluteAdditionalRootWithoutTrustedPrimary(t *testing.T) {
	extra := filepath.Join(t.TempDir(), "packages", "api")
	args := mustRawJSON(t, map[string]any{"action": "read_file", "file_path": "go.mod"})
	peer := newTrustedLSPPeer(t, trustedScopePayload{
		agentID:        "agent-no-primary",
		threadID:       "thread-no-primary",
		callID:         "call-no-primary",
		cwd:            "",
		workspaceRoots: []string{extra},
		replyText:      "additional forwarded",
	})
	h, registry := newHandlerForTest(peer)
	registry.scoped = true

	got, err := h.routeToolCall(context.Background(), ToolCallRequest{
		Name:           "file",
		Arguments:      args,
		AgentID:        "agent-no-primary",
		ThreadID:       "thread-no-primary",
		CallID:         "call-no-primary",
		CWD:            ".",
		WorkspaceRoots: []string{extra},
	})
	if err != nil {
		t.Fatalf("routeToolCall() error = %v", err)
	}
	assertSingleTextItem(t, got, "additional forwarded", true)
}

func TestToolBridge_ResolvesRelativeAdditionalRootsAgainstPrimaryRoot(t *testing.T) {
	root := t.TempDir()
	extra := filepath.Join(root, "packages", "api")
	args := mustRawJSON(t, map[string]any{"action": "read_file", "file_path": "go.mod"})
	peer := newTrustedLSPPeer(t, trustedScopePayload{
		agentID:        "agent-rel-extra",
		threadID:       "thread-rel-extra",
		callID:         "call-rel-extra",
		cwd:            root,
		workspaceRoots: []string{root, extra},
		replyText:      "relative resolved",
	})
	h, registry := newHandlerForTest(peer)
	registry.scoped = true

	got, err := h.routeToolCall(context.Background(), ToolCallRequest{
		Name:           "file",
		Arguments:      args,
		AgentID:        "agent-rel-extra",
		ThreadID:       "thread-rel-extra",
		CallID:         "call-rel-extra",
		CWD:            root,
		WorkspaceRoots: []string{"packages/api"},
	})
	if err != nil {
		t.Fatalf("routeToolCall() error = %v", err)
	}
	assertSingleTextItem(t, got, "relative resolved", true)
}

func TestToolBridge_ScopedLSPPeerRoutingUsesTrustedMetadata(t *testing.T) {
	root := t.TempDir()
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
	rightPeer := newTrustedLSPPeer(t, trustedScopePayload{
		agentID: "agent-b", threadID: "thread-b", callID: "call-1", cwd: root, replyText: "ok",
	})
	h, registry := newHandlerForTest(wrongPeer, rightPeer)
	registry.scoped = true

	got, err := h.routeToolCall(context.Background(), ToolCallRequest{
		Name:      "file",
		Arguments: args,
		AgentID:   "agent-b",
		ThreadID:  "thread-b",
		CallID:    "call-1",
		CWD:       root,
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

func TestLSPReleaseScopeAdminCallCarriesTrustedScope(t *testing.T) {
	root := t.TempDir()
	args := mustRawJSON(t, map[string]any{
		"action":    "read_file",
		"file_path": "go.mod",
		"agent_id":  "forged-agent",
		"thread_id": "forged-thread",
		"cwd":       "/forged/root",
	})
	peer := newTrustedLSPPeer(t, trustedScopePayload{
		agentID: "trusted-agent", threadID: "trusted-thread", callID: "trusted-call", cwd: root, replyText: "trusted scope",
	})
	h, registry := newHandlerForTest(peer)
	registry.scoped = true

	got, err := h.routeToolCall(context.Background(), ToolCallRequest{
		Name:      "file",
		Arguments: args,
		AgentID:   "trusted-agent",
		ThreadID:  "trusted-thread",
		CallID:    "trusted-call",
		CWD:       root,
	})
	if err != nil {
		t.Fatalf("routeToolCall() error = %v", err)
	}
	assertSingleTextItem(t, got, "trusted scope", true)
	if len(registry.gotScopes) != 1 {
		t.Fatalf("FindActiveForScope() calls = %d, want 1", len(registry.gotScopes))
	}
	if scope := registry.gotScopes[0]; scope.AgentID != "trusted-agent" || scope.ThreadID != "trusted-thread" || scope.CallID != "trusted-call" || scope.CWD != root || scope.Family != dto.ClientKindLSP {
		t.Fatalf("FindActiveForScope() scope = %#v, want trusted LSP scope", scope)
	}
}

func TestToolbridgeHTTPPeerProxyInjectsTrustedScopeMetadata(t *testing.T) {
	root := t.TempDir()
	args := mustRawJSON(t, map[string]any{
		"action":    "read_file",
		"file_path": "go.mod",
		"agent_id":  "forged-agent",
		"cwd":       "/forged/root",
	})
	peer := newTrustedLSPPeer(t, trustedScopePayload{
		agentID: "agent-http", threadID: "thread-http", callID: "call-http", cwd: root, replyText: "metadata injected",
	})
	h, registry := newHandlerForTest(peer)
	registry.scoped = true

	got, err := h.routeToolCall(context.Background(), ToolCallRequest{
		Name:      "file",
		Arguments: args,
		AgentID:   "agent-http",
		ThreadID:  "thread-http",
		CallID:    "call-http",
		CWD:       root,
	})
	if err != nil {
		t.Fatalf("routeToolCall() error = %v", err)
	}
	assertSingleTextItem(t, got, "metadata injected", true)
}

func TestToolBridge_NoPeer_FailFast(t *testing.T) {
	h, _ := newHandlerForTest()

	got, err := h.routeToolCall(context.Background(), ToolCallRequest{Name: "inspect", Arguments: json.RawMessage(`{}`)})
	if !errors.Is(err, ErrNoPeerAvailable) {
		t.Fatalf("routeToolCall() error = %v, want %v", err, ErrNoPeerAvailable)
	}
	if got != nil {
		t.Fatalf("routeToolCall() result = %#v, want nil", got)
	}
}

func TestToolBridge_MultiplePeers_Ambiguous(t *testing.T) {
	h, _ := newHandlerForTest(newToolCallPeer(t, "inspect", json.RawMessage(`{}`), "ignored", nil), newToolCallPeer(t, "inspect", json.RawMessage(`{}`), "ignored", nil))

	got, err := h.routeToolCall(context.Background(), ToolCallRequest{Name: "inspect", Arguments: json.RawMessage(`{}`)})
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
	h, _ := newHandlerForTest(newToolCallPeer(t, "inspect", json.RawMessage(`{}`), "", peerErr))

	got, err := h.routeToolCall(context.Background(), ToolCallRequest{Name: "inspect", Arguments: json.RawMessage(`{}`)})
	if err != nil {
		t.Fatalf("routeToolCall() error = %v, want structured tool failure result", err)
	}
	if got == nil {
		t.Fatalf("routeToolCall() result = nil, want structured failure result with fail-fast error")
	}
	assertSingleTextItem(t, got, peerErr.Error(), false)
}

func TestProxyToolCall_PeerErrorUsesToolResultNotJSONRPCError(t *testing.T) {
	peerErr := errors.New("peer callback failed")
	h, _ := newHandlerForTest(newToolCallPeer(t, "inspect", json.RawMessage(`{}`), "", peerErr))
	body := string(mustRawJSON(t, map[string]any{
		"jsonrpc": "2.0",
		"id":      "req-peer-error",
		"method":  "tools/call",
		"params": map[string]any{
			"name":      "inspect",
			"arguments": map[string]any{},
		},
	}))

	got := callProxyRequest(t, h, "/mcp/lsp/agent-1", body)
	if got.Error != nil {
		t.Fatalf("proxy response error = %+v, want tool result", got.Error)
	}
	result := requireProxyResultMap(t, got)
	assertToolErrorResult(t, result)
	content, ok := result["content"].([]any)
	if !ok || len(content) != 1 {
		t.Fatalf("content = %#v, want one error item", result["content"])
	}
	item, ok := content[0].(map[string]any)
	if !ok || item["text"] != peerErr.Error() {
		t.Fatalf("content[0] = %#v, want peer error text", content[0])
	}
}

func TestProxyToolCall_PeerExplicitFailureSetsIsError(t *testing.T) {
	h, _ := newHandlerForTest(&mcpcontrol.ToolInstance{Peer: &stubPeer{callbackFn: func(_ context.Context, method string, params any, result any) error {
		if method != ProxyMethodToolsCall {
			t.Fatalf("Callback() method = %q, want %s", method, ProxyMethodToolsCall)
		}
		assertToolCallPayload(t, params, "patch_edit", json.RawMessage(`{}`))
		resp, ok := result.(*peerToolCallResponse)
		if !ok {
			t.Fatalf("Callback() result type = %T, want *peerToolCallResponse", result)
		}
		resp.Content = []peerToolCallContent{{Type: "text", Text: `{"success":false,"error":"sandbox failed"}`}}
		resp.StructuredContent = json.RawMessage(`{"success":false,"error":"sandbox failed"}`)
		resp.IsError = true
		return nil
	}}})
	body := string(mustRawJSON(t, map[string]any{
		"jsonrpc": "2.0",
		"id":      "req-peer-failure",
		"method":  "tools/call",
		"params": map[string]any{
			"name":      "patch_edit",
			"arguments": map[string]any{},
		},
	}))

	got := callProxyRequest(t, h, "/mcp/lsp/agent-1", body)
	if got.Error != nil {
		t.Fatalf("proxy response error = %+v, want tool result", got.Error)
	}
	result := requireProxyResultMap(t, got)
	assertToolErrorResult(t, result)
	if _, ok := result["structuredContent"]; !ok {
		t.Fatalf("result = %#v, want structuredContent preserved", result)
	}
}

func TestToolBridge_RouteToolCall_RejectsMismatchedClientKind(t *testing.T) {
	h, registry := newHandlerForTest(newToolCallPeer(t, "inspect", json.RawMessage(`{}`), "ignored", nil))

	got, err := h.routeToolCall(context.Background(), ToolCallRequest{
		Name:       "inspect",
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
	body := fmt.Sprintf(`{"jsonrpc":"2.0","id":"req-1","method":"tools/call","params":{"name":"inspect","arguments":{"blob":"%s"}}}`,
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
