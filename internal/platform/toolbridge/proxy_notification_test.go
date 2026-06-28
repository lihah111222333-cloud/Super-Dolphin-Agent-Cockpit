package toolbridge

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestProxyJSONRPCNotificationToolCallUsesAcceptedNoBody(t *testing.T) {
	h, _ := newHandlerForTest(newToolCallPeer(t, "lsp_hover", json.RawMessage(`{}`), "ok", nil))
	body := string(mustRawJSON(t, map[string]any{
		"jsonrpc": "2.0",
		"method":  "tools/call",
		"params": map[string]any{
			"name":      "lsp_hover",
			"arguments": map[string]any{},
		},
	}))

	resp := callProxyRaw(t, h, "/mcp/lsp/agent-1", body)
	assertProxyNotificationAck(t, resp)
}

func TestProxyJSONRPCNotificationToolCallErrorUsesAcceptedNoBody(t *testing.T) {
	h, _ := newHandlerForTest()
	body := string(mustRawJSON(t, map[string]any{
		"jsonrpc": "2.0",
		"method":  "tools/call",
		"params": map[string]any{
			"name":      "lsp_hover",
			"arguments": map[string]any{},
		},
	}))

	resp := callProxyRaw(t, h, "/mcp/lsp/agent-1", body)
	assertProxyNotificationAck(t, resp)
}

func TestProxyJSONRPCRequestToolCallKeepsResultBody(t *testing.T) {
	h, _ := newHandlerForTest(newToolCallPeer(t, "lsp_hover", json.RawMessage(`{}`), "ok", nil))
	body := string(mustRawJSON(t, map[string]any{
		"jsonrpc": "2.0",
		"id":      "req-success",
		"method":  "tools/call",
		"params": map[string]any{
			"name":      "lsp_hover",
			"arguments": map[string]any{},
		},
	}))

	resp := callProxyRaw(t, h, "/mcp/lsp/agent-1", body)
	if resp.Code != http.StatusOK {
		t.Fatalf("handleProxyRequest() status = %d, want %d", resp.Code, http.StatusOK)
	}
	var got proxyJSONRPCResponse
	if err := json.Unmarshal(resp.Body.Bytes(), &got); err != nil {
		t.Fatalf("json.Unmarshal(response) error = %v, body=%s", err, resp.Body.String())
	}
	if got.ID != "req-success" || got.Error != nil || got.Result == nil {
		t.Fatalf("proxy response = %#v, want result for req-success", got)
	}
}

func TestProxyJSONRPCRequestToolCallKeepsErrorBody(t *testing.T) {
	h, _ := newHandlerForTest()
	body := string(mustRawJSON(t, map[string]any{
		"jsonrpc": "2.0",
		"id":      "req-failure",
		"method":  "tools/call",
		"params": map[string]any{
			"name":      "lsp_hover",
			"arguments": map[string]any{},
		},
	}))

	resp := callProxyRaw(t, h, "/mcp/lsp/agent-1", body)
	if resp.Code != http.StatusOK {
		t.Fatalf("handleProxyRequest() status = %d, want %d", resp.Code, http.StatusOK)
	}
	var got proxyJSONRPCResponse
	if err := json.Unmarshal(resp.Body.Bytes(), &got); err != nil {
		t.Fatalf("json.Unmarshal(response) error = %v, body=%s", err, resp.Body.String())
	}
	if got.ID != "req-failure" || got.Error == nil {
		t.Fatalf("proxy response = %#v, want error for req-failure", got)
	}
}

func callProxyRaw(t *testing.T, h *Handler, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	if h.proxyAuthToken != "" {
		req.Header.Set("Authorization", "Bearer "+h.proxyAuthToken)
	}
	resp := httptest.NewRecorder()
	h.handleProxyRequest(resp, req)
	return resp
}

func assertProxyNotificationAck(t *testing.T, resp *httptest.ResponseRecorder) {
	t.Helper()
	if resp.Code != http.StatusAccepted {
		t.Fatalf("handleProxyRequest() status = %d, want %d; body=%s", resp.Code, http.StatusAccepted, resp.Body.String())
	}
	if body := strings.TrimSpace(resp.Body.String()); body != "" {
		t.Fatalf("handleProxyRequest() body = %q, want empty notification ack", body)
	}
}
