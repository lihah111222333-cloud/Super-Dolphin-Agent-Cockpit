package toolbridge

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
	providerdto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/provider"
)

func TestPrepareCodexToolSurfaceUsesHTTPMCPServerTools(t *testing.T) {
	toolsServer, seen := newHTTPMCPToolsTestServer(t)
	defer toolsServer.Close()
	workDir := t.TempDir()
	h := &Handler{}

	tools, err := prepareCodexToolSurfaceForTest(t, h, context.Background(), contract.CodexToolSurfaceScope{
		AgentID:          "agent-http",
		ProviderThreadID: "provider-thread-http",
		CWD:              workDir,
		Manifest: providerdto.MCPManifest{Binaries: []providerdto.MCPBinary{{
			Name:    string(providerdto.FamilyOrch),
			Type:    "http",
			URL:     toolsServer.URL,
			Headers: map[string]string{"Authorization": "Bearer token"},
		}}},
	})
	if err != nil {
		t.Fatalf("PrepareCodexToolSurface() error = %v", err)
	}
	assertDynamicToolNames(t, tools, []string{"remote_search"})

	rawCall, err := json.Marshal(map[string]any{
		"name":      "remote_search",
		"arguments": map[string]any{"query": "hello"},
		"_agentId":  "agent-http",
		"_threadId": "provider-thread-http",
		"_callId":   "call-http",
		"_cwd":      workDir,
	})
	if err != nil {
		t.Fatalf("marshal tool call: %v", err)
	}
	result, err := h.HandleToolCall(context.Background(), contract.ToolCallRawMessage{Params: rawCall})
	if err != nil {
		t.Fatalf("HandleToolCall(remote_search) error = %v", err)
	}
	got, ok := result.(*ToolCallResult)
	if !ok || got == nil || !got.Success {
		t.Fatalf("HandleToolCall result = %#v, want successful ToolCallResult", result)
	}
	assertHTTPMCPToolsCallParams(t, seen.toolsCallParams, workDir)
	assertHTTPMCPMethodOrder(t, seen.methods, []string{"initialize", "notifications/initialized", "tools/list", "tools/call"})
}

func TestPrepareCodexToolSurfaceReadsHTTPMCPEventStreamTools(t *testing.T) {
	toolsServer := newHTTPMCPEventStreamToolsTestServer(t)
	defer toolsServer.Close()
	h := &Handler{}

	tools, err := prepareCodexToolSurfaceForTest(t, h, context.Background(), contract.CodexToolSurfaceScope{
		AgentID:          "agent-http",
		ProviderThreadID: "provider-thread-http",
		CWD:              t.TempDir(),
		Manifest: providerdto.MCPManifest{Binaries: []providerdto.MCPBinary{{
			Name: string(providerdto.FamilyOrch),
			Type: "http",
			URL:  toolsServer.URL,
		}}},
	})
	if err != nil {
		t.Fatalf("PrepareCodexToolSurface() error = %v", err)
	}
	assertDynamicToolNames(t, tools, []string{"remote_search"})
}

func TestPrepareCodexToolSurfaceNamesHTTPMCPInitializeFailure(t *testing.T) {
	toolsServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"error":{"message":"record not found"}}`, http.StatusNotFound)
	}))
	defer toolsServer.Close()
	h := &Handler{}

	_, err := prepareCodexToolSurfaceForTest(t, h, context.Background(), contract.CodexToolSurfaceScope{
		AgentID:          "agent-http",
		ProviderThreadID: "provider-thread-http",
		CWD:              t.TempDir(),
		Manifest: providerdto.MCPManifest{Binaries: []providerdto.MCPBinary{{
			Name: string(providerdto.FamilyOrch),
			Type: "http",
			URL:  toolsServer.URL,
		}}},
	})
	if err == nil {
		t.Fatal("PrepareCodexToolSurface() error = nil, want named HTTP MCP initialize failure")
	}
	if got := err.Error(); !strings.Contains(got, `MCP server "orch"`) || !strings.Contains(got, "HTTP MCP initialize returned HTTP 404") {
		t.Fatalf("PrepareCodexToolSurface() error = %q, want server name and initialize 404", got)
	}
}

func TestNewHTTPMCPClientRejectsInvalidInitializeProtocolVersion(t *testing.T) {
	tests := []struct {
		name   string
		result map[string]any
		want   string
	}{
		{name: "missing", result: map[string]any{"capabilities": map[string]any{}}, want: "protocolVersion is required"},
		{name: "unsupported", result: map[string]any{"protocolVersion": "2099-01-01"}, want: "is not supported"},
		{name: "control", result: map[string]any{"protocolVersion": "2025-11-25\r\nX-Evil: yes"}, want: "control characters"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var req struct {
					ID json.RawMessage `json:"id"`
				}
				if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
					http.Error(w, err.Error(), http.StatusBadRequest)
					return
				}
				writeHTTPMCPToolsTestResponse(w, req.ID, tt.result)
			}))
			defer server.Close()

			_, err := newHTTPMCPClient(context.Background(), providerdto.MCPBinary{
				Name: string(providerdto.FamilyOrch),
				Type: "http",
				URL:  server.URL,
			})
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("newHTTPMCPClient() error = %v, want substring %q", err, tt.want)
			}
		})
	}
}

func TestNewHTTPMCPClientReleasesSessionAfterMalformedInitializeResponse(t *testing.T) {
	var deletes atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodDelete {
			if got := r.Header.Get(httpMCPHeaderSessionID); got != "session-malformed" {
				http.Error(w, "missing session", http.StatusBadRequest)
				return
			}
			deletes.Add(1)
			w.WriteHeader(http.StatusNoContent)
			return
		}
		w.Header().Set(httpMCPHeaderSessionID, "session-malformed")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{`))
	}))
	defer server.Close()

	_, err := newHTTPMCPClient(context.Background(), providerdto.MCPBinary{
		Name: string(providerdto.FamilyOrch),
		Type: "http",
		URL:  server.URL,
	})
	if err == nil {
		t.Fatal("newHTTPMCPClient() error = nil, want malformed initialize response")
	}
	if got := deletes.Load(); got != 1 {
		t.Fatalf("DELETE calls = %d, want 1", got)
	}
}

func TestHTTPMCPClientCallToolConvertsJSONRPCErrorToToolResult(t *testing.T) {
	const marker = "token=sk-test-secret dsn=postgres://alice:secret@db/private path=/Users/private/secret.go"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			ID     json.RawMessage `json:"id,omitempty"`
			Method string          `json:"method"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		writeHTTPMCPToolsTestRPCError(w, req.ID, -32602, marker)
	}))
	defer server.Close()
	client := &httpMCPClient{client: server.Client(), endpoint: server.URL}

	got, err := client.CallTool(context.Background(), "launch_agent", json.RawMessage(`{"name":"worker"}`), ToolCallRequest{})
	if err != nil {
		t.Fatalf("CallTool() error = %v, want tool failure result", err)
	}
	if got == nil || got.Success {
		t.Fatalf("CallTool() success = %#v, want false", got)
	}
	if len(got.ContentItems) != 1 || strings.Contains(got.ContentItems[0].Text, marker) || !strings.HasPrefix(got.ContentItems[0].Text, "Tool execution failed. Diagnostic ID: ") {
		t.Fatalf("CallTool() content = %#v, want public diagnostic error without marker", got.ContentItems)
	}
}

func TestHTTPMCPClientCloseDeletesSessionOnce(t *testing.T) {
	var deletes atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if got := r.Header.Get(httpMCPHeaderSessionID); got != "session-close" {
			http.Error(w, "missing session", http.StatusBadRequest)
			return
		}
		if got := r.Header.Get(httpMCPHeaderProtocolVersion); got != ProxyProtocolVersion {
			http.Error(w, "missing protocol version", http.StatusBadRequest)
			return
		}
		if got := r.Header.Get("Authorization"); got != "Bearer token" {
			http.Error(w, "missing authorization", http.StatusUnauthorized)
			return
		}
		deletes.Add(1)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()
	client := &httpMCPClient{
		client:          server.Client(),
		endpoint:        server.URL,
		headers:         map[string]string{"Authorization": "Bearer token"},
		sessionID:       "session-close",
		protocolVersion: ProxyProtocolVersion,
	}

	if err := client.Close(); err != nil {
		t.Fatalf("first Close() error = %v", err)
	}
	if err := client.Close(); err != nil {
		t.Fatalf("second Close() error = %v", err)
	}
	if got := deletes.Load(); got != 1 {
		t.Fatalf("DELETE calls = %d, want 1", got)
	}
}

func TestHTTPMCPClientCloseAcceptsTypedNilAfterInitializeFailure(t *testing.T) {
	var client *httpMCPClient
	if err := client.Close(); err != nil {
		t.Fatalf("typed-nil Close() error = %v", err)
	}
}

func TestBuildHTTPMCPClientRejectsPrivateURL(t *testing.T) {
	_, err := buildHTTPMCPClient(providerdto.MCPBinary{
		Name:            "loopback",
		TrustedServerID: "loopback",
		Type:            "http",
		URL:             "http://127.0.0.1:9090/mcp",
	})
	if err == nil {
		t.Fatal("buildHTTPMCPClient() error = nil, want private network rejection")
	}
	if !strings.Contains(err.Error(), "private network") {
		t.Fatalf("buildHTTPMCPClient() error = %v, want private network rejection", err)
	}
}

func TestBuildHTTPMCPClientCreatesIndependentManagedClients(t *testing.T) {
	t.Parallel()

	binary := providerdto.MCPBinary{Name: string(providerdto.FamilyOrch), Type: "http", URL: "http://127.0.0.1:9900/mcp"}
	first, err := buildHTTPMCPClient(binary)
	if err != nil {
		t.Fatalf("first buildHTTPMCPClient() error = %v", err)
	}
	second, err := buildHTTPMCPClient(binary)
	if err != nil {
		t.Fatalf("second buildHTTPMCPClient() error = %v", err)
	}
	firstHTTP, ok := first.client.(*http.Client)
	if !ok {
		t.Fatalf("first client = %T, want *http.Client", first.client)
	}
	secondHTTP, ok := second.client.(*http.Client)
	if !ok {
		t.Fatalf("second client = %T, want *http.Client", second.client)
	}
	if firstHTTP == secondHTTP {
		t.Fatal("buildHTTPMCPClient() shared the default HTTP client across builds")
	}
}

func TestHTTPMCPClientListToolsRejectsMalformedResult(t *testing.T) {
	cases := []struct {
		name    string
		result  map[string]any
		wantErr string
	}{
		{name: "missing tools", result: map[string]any{}, wantErr: "tools array is required"},
		{name: "tools not array", result: map[string]any{"tools": nil}, wantErr: "tools array is required"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var req struct {
					ID     json.RawMessage `json:"id,omitempty"`
					Method string          `json:"method"`
				}
				if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
					http.Error(w, err.Error(), http.StatusBadRequest)
					return
				}
				if req.Method != "tools/list" {
					http.Error(w, "unexpected method", http.StatusBadRequest)
					return
				}
				writeHTTPMCPToolsTestResponse(w, req.ID, tc.result)
			}))
			defer server.Close()
			client := &httpMCPClient{client: server.Client(), endpoint: server.URL}

			_, err := client.ListTools(context.Background())
			if err == nil {
				t.Fatalf("ListTools() error = nil, want %s", tc.wantErr)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("ListTools() error = %v, want %s", err, tc.wantErr)
			}
		})
	}
}

type httpMCPToolsTestSeen struct {
	methods         []string
	toolsCallParams map[string]any
}

func newHTTPMCPToolsTestServer(t *testing.T) (*httptest.Server, *httpMCPToolsTestSeen) {
	t.Helper()
	seen := &httpMCPToolsTestSeen{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if got := r.Header.Get("Authorization"); got != "Bearer token" {
			http.Error(w, "missing authorization", http.StatusUnauthorized)
			return
		}
		if !requestAcceptsHTTPMCP(r.Header.Get("Accept")) {
			http.Error(w, "missing streamable HTTP accept", http.StatusNotAcceptable)
			return
		}
		var req struct {
			ID     json.RawMessage `json:"id,omitempty"`
			Method string          `json:"method"`
			Params json.RawMessage `json:"params,omitempty"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if !requireHTTPMCPTestSessionHeaders(w, r, req.Method) {
			return
		}
		seen.methods = append(seen.methods, req.Method)
		handleHTTPMCPToolsTestRequest(t, w, req.ID, req.Method, req.Params, seen)
	}))
	return server, seen
}

func newHTTPMCPEventStreamToolsTestServer(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !requestAcceptsHTTPMCP(r.Header.Get("Accept")) {
			http.Error(w, "missing streamable HTTP accept", http.StatusNotAcceptable)
			return
		}
		var req struct {
			ID     json.RawMessage `json:"id,omitempty"`
			Method string          `json:"method"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if !requireHTTPMCPTestSessionHeaders(w, r, req.Method) {
			return
		}
		switch req.Method {
		case "initialize":
			writeHTTPMCPToolsTestResponse(w, req.ID, map[string]any{"protocolVersion": ProxyProtocolVersion, "capabilities": map[string]any{"tools": map[string]any{}}})
		case "notifications/initialized":
			w.WriteHeader(http.StatusAccepted)
		case "tools/list":
			writeHTTPMCPToolsTestEvent(w, req.ID, map[string]any{"tools": []map[string]any{{
				"name":        "remote_search",
				"description": "search remote documents",
				"inputSchema": map[string]any{"type": "object"},
			}}})
		default:
			http.Error(w, "unknown method", http.StatusBadRequest)
		}
	}))
}

func handleHTTPMCPToolsTestRequest(t *testing.T, w http.ResponseWriter, id json.RawMessage, method string, params json.RawMessage, seen *httpMCPToolsTestSeen) {
	t.Helper()
	switch method {
	case "initialize":
		writeHTTPMCPToolsTestResponse(w, id, map[string]any{"protocolVersion": ProxyProtocolVersion, "capabilities": map[string]any{"tools": map[string]any{}}})
	case "notifications/initialized":
		w.WriteHeader(http.StatusAccepted)
	case "tools/list":
		writeHTTPMCPToolsTestResponse(w, id, map[string]any{"tools": []map[string]any{{
			"name":        "remote_search",
			"description": "search remote documents",
			"inputSchema": map[string]any{"type": "object"},
		}}})
	case "tools/call":
		if err := json.Unmarshal(params, &seen.toolsCallParams); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		writeHTTPMCPToolsTestResponse(w, id, map[string]any{"content": []map[string]any{{"type": "text", "text": "ok"}}})
	default:
		http.Error(w, "unknown method", http.StatusBadRequest)
	}
}

func writeHTTPMCPToolsTestResponse(w http.ResponseWriter, id json.RawMessage, result map[string]any) {
	w.Header().Set("Content-Type", "application/json")
	if _, ok := result["protocolVersion"]; ok {
		w.Header().Set(httpMCPHeaderSessionID, "session-http")
	}
	_ = json.NewEncoder(w).Encode(map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"result":  result,
	})
}

func writeHTTPMCPToolsTestRPCError(w http.ResponseWriter, id json.RawMessage, code int, message string) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"error": map[string]any{
			"code":    code,
			"message": message,
		},
	})
}

func writeHTTPMCPToolsTestEvent(w http.ResponseWriter, id json.RawMessage, result map[string]any) {
	w.Header().Set("Content-Type", "text/event-stream")
	raw, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"result":  result,
	})
	_, _ = w.Write([]byte("event: message\ndata: " + string(raw) + "\n\n"))
}

func requestAcceptsHTTPMCP(accept string) bool {
	return strings.Contains(accept, "application/json") && strings.Contains(accept, "text/event-stream")
}

func requireHTTPMCPTestSessionHeaders(w http.ResponseWriter, r *http.Request, method string) bool {
	if method == "initialize" {
		return true
	}
	if got := r.Header.Get(httpMCPHeaderSessionID); got != "session-http" {
		http.Error(w, "missing session", http.StatusBadRequest)
		return false
	}
	if got := r.Header.Get(httpMCPHeaderProtocolVersion); got != ProxyProtocolVersion {
		http.Error(w, "missing protocol version", http.StatusBadRequest)
		return false
	}
	return true
}

func assertHTTPMCPToolsCallParams(t *testing.T, got map[string]any, workDir string) {
	t.Helper()
	for key, want := range map[string]string{
		"name":      "remote_search",
		"_agentId":  "agent-http",
		"_threadId": "provider-thread-http",
		"_callId":   "call-http",
		"_cwd":      workDir,
	} {
		if value, _ := got[key].(string); value != want {
			t.Fatalf("tools/call param %s = %#v, want %q; params=%#v", key, got[key], want, got)
		}
	}
}

func assertHTTPMCPMethodOrder(t *testing.T, got, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("HTTP MCP methods = %#v, want %#v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("HTTP MCP methods = %#v, want %#v", got, want)
		}
	}
}
