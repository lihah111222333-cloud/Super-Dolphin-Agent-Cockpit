package common

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHTTPServerRejectsToolsWithoutBearerToken(t *testing.T) {
	server := newTestHTTPServer("mcp-orch", "dev", testToolProvider{}, WithBearerToken("secret"))
	req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}`))
	rec := httptest.NewRecorder()

	server.handleMCP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
	}
	if strings.Contains(rec.Body.String(), `"jsonrpc"`) {
		t.Fatalf("body = %q, want no JSON-RPC response", rec.Body.String())
	}
}

func TestHTTPServerAcceptsToolsWithBearerToken(t *testing.T) {
	server := newTestHTTPServer("mcp-orch", "dev", testToolProvider{}, WithBearerToken("secret"))
	sessionID := initializeHTTPSessionWithBearer(t, server, "secret")
	req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}`))
	req.Header.Set("Authorization", "Bearer secret")
	req.Header.Set(testHTTPMCPHeaderSessionID, sessionID)
	rec := httptest.NewRecorder()

	server.handleMCP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"demo_tool"`) {
		t.Fatalf("body = %s, want tools/list response", rec.Body.String())
	}
}
