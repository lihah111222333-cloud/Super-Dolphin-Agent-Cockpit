package toolbridge

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestNewHandlerProxyRejectsMissingBearerToken(t *testing.T) {
	t.Parallel()

	h := mustNewToolbridgeDependencyHandler(t)
	req := httptest.NewRequest(http.MethodPost, "/mcp/lsp/agent-1", strings.NewReader(`{"jsonrpc":"2.0","id":"req-1","method":"initialize"}`))
	resp := httptest.NewRecorder()

	h.handleProxyRequest(resp, req)

	if resp.Code != http.StatusUnauthorized {
		t.Fatalf("handleProxyRequest() status = %d, want %d", resp.Code, http.StatusUnauthorized)
	}
}

func TestNewHandlerProxyAcceptsBearerToken(t *testing.T) {
	t.Parallel()

	h := mustNewToolbridgeDependencyHandler(t)
	req := httptest.NewRequest(http.MethodPost, "/mcp/lsp/agent-1", strings.NewReader(`{"jsonrpc":"2.0","id":"req-1","method":"initialize"}`))
	req.Header.Set("Authorization", "Bearer "+h.proxyAuthToken)
	resp := httptest.NewRecorder()

	h.handleProxyRequest(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("handleProxyRequest() status = %d, want %d", resp.Code, http.StatusOK)
	}
	if strings.Contains(resp.Body.String(), `"error"`) {
		t.Fatalf("handleProxyRequest() body = %s, want initialize result", resp.Body.String())
	}
}
