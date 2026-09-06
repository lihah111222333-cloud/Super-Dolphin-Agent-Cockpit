package common

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestLocalHTTPProfileBindsConfiguredCallerPrincipal(t *testing.T) {
	principal, err := NewCallerPrincipal("subject/alice", "run/one", "standalone_http_local_v1", "auth/session-1", time.Now().UTC().Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	var observed CallerPrincipal
	var observedOK bool
	provider := captureToolProvider{call: func(ctx context.Context, _ string, _ json.RawMessage) (any, error) {
		observed, observedOK = CallerPrincipalFromContext(ctx)
		return map[string]any{"ok": true}, nil
	}}
	server, err := NewLocalHTTPServer("orch", "test", provider, LocalHTTPProfile{WorkspaceRoot: t.TempDir(), BearerToken: "test-local-operator-credential", CallerPrincipal: principal}, newTestLoggerRuntime())
	if err != nil {
		t.Fatal(err)
	}
	sessionID := initializeLocalHTTPSession(t, server)
	call := httptest.NewRecorder()
	server.handleMCP(call, localProfileRequest(server, http.MethodPost, `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"demo_tool","arguments":{}}}`, sessionID))
	if call.Code != http.StatusOK || !observedOK || observed != principal {
		t.Fatalf("tools/call status=%d principal=%#v observed=%v want=%#v", call.Code, observed, observedOK, principal)
	}
	forged := httptest.NewRecorder()
	server.handleMCP(forged, localProfileRequest(server, http.MethodPost, `{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"demo_tool","_agentId":"subject/forged","arguments":{}}}`, sessionID))
	if forged.Code != http.StatusOK || !strings.Contains(forged.Body.String(), `"error"`) {
		t.Fatalf("forged metadata status=%d body=%s", forged.Code, forged.Body.String())
	}
}

type rotatingCallerPrincipalIssuer struct {
	now   time.Time
	calls int
}

func (i *rotatingCallerPrincipalIssuer) Validate() error {
	if i == nil || i.now.IsZero() {
		return ErrInvalidCallerPrincipal
	}
	return nil
}

func (i *rotatingCallerPrincipalIssuer) Issue() (CallerPrincipal, error) {
	i.calls++
	return NewCallerPrincipal("subject/alice", "run/one", "profile/v1", "auth/server-boot", i.now.Add(time.Duration(i.calls)*time.Minute))
}

func TestLocalHTTPProfileIssuesFreshPrincipalForEachSession(t *testing.T) {
	issuer := &rotatingCallerPrincipalIssuer{now: time.Now().UTC().Add(time.Hour)}
	server, err := NewLocalHTTPServer("orch", "test", testToolProvider{}, LocalHTTPProfile{
		WorkspaceRoot: t.TempDir(), BearerToken: "test-local-operator-credential", PrincipalIssuer: issuer,
	}, newTestLoggerRuntime())
	if err != nil {
		t.Fatal(err)
	}
	firstID := initializeLocalHTTPSession(t, server)
	secondID := initializeLocalHTTPSession(t, server)
	if issuer.calls != 2 {
		t.Fatalf("issuer calls=%d, want one per initialize", issuer.calls)
	}
	first, second := server.sessions[firstID].principal, server.sessions[secondID].principal
	if !second.ExpiresAt.After(first.ExpiresAt) || first.AuthContext != second.AuthContext {
		t.Fatalf("session principals=(%#v,%#v), want fresh expiry and same boot context", first, second)
	}
}

// initializeLocalHTTPSession 执行 local HTTP 握手并返回已初始化 session ID。
func initializeLocalHTTPSession(t *testing.T, server *HTTPServer) string {
	t.Helper()
	initialize := httptest.NewRecorder()
	server.handleMCP(initialize, localProfileRequest(server, http.MethodPost, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25","capabilities":{},"clientInfo":{"name":"test","version":"1"}}}`, ""))
	if initialize.Code != http.StatusOK {
		t.Fatalf("initialize status=%d body=%s", initialize.Code, initialize.Body.String())
	}
	sessionID := initialize.Header().Get(httpMCPHeaderSessionID)
	if sessionID == "" {
		t.Fatal("initialize omitted session id")
	}
	initialized := httptest.NewRecorder()
	server.handleMCP(initialized, localProfileRequest(server, http.MethodPost, `{"jsonrpc":"2.0","method":"notifications/initialized"}`, sessionID))
	if initialized.Code != http.StatusAccepted {
		t.Fatalf("initialized status=%d", initialized.Code)
	}
	return sessionID
}

func TestLocalHTTPProfileRejectsInvalidConfiguredCallerPrincipal(t *testing.T) {
	_, err := NewLocalHTTPServer("orch", "test", testToolProvider{}, LocalHTTPProfile{
		WorkspaceRoot: t.TempDir(), BearerToken: "test-local-operator-credential",
		CallerPrincipal: CallerPrincipal{PrincipalRef: "subject/alice"},
	}, newTestLoggerRuntime())
	if !errors.Is(err, ErrInvalidCallerPrincipal) {
		t.Fatalf("NewLocalHTTPServer() error=%v, want ErrInvalidCallerPrincipal", err)
	}
}

func TestLocalHTTPProfileExpiredSessionPrincipalFailsClosed(t *testing.T) {
	principal, err := NewCallerPrincipal("subject/alice", "run/one", "standalone_http_local_v1", "auth/session-1", time.Now().UTC().Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	server, err := NewLocalHTTPServer("orch", "test", testToolProvider{}, LocalHTTPProfile{
		WorkspaceRoot: t.TempDir(), BearerToken: "test-local-operator-credential", CallerPrincipal: principal,
	}, newTestLoggerRuntime())
	if err != nil {
		t.Fatal(err)
	}
	sessionID, err := server.createSession(principal)
	if err != nil {
		t.Fatal(err)
	}
	session := server.sessions[sessionID]
	session.mu.Lock()
	session.principal.ExpiresAt = time.Now().UTC().Add(-time.Second)
	session.mu.Unlock()
	server.dispatch(context.Background(), session, jsonRPCRequest{JSONRPC: "2.0", Method: "notifications/initialized"})
	session.mu.Lock()
	initialized := session.initialized
	session.mu.Unlock()
	if initialized {
		t.Fatal("expired principal notification unlocked session")
	}
	session.mu.Lock()
	session.initialized = true
	session.mu.Unlock()
	response := server.dispatch(context.Background(), session, jsonRPCRequest{JSONRPC: "2.0", ID: json.RawMessage(`1`), Method: "tools/list"})
	if response == nil || response.Error == nil || response.Error.Code != codeInvalidReq {
		t.Fatalf("expired principal dispatch=%#v, want invalid request", response)
	}
}

func TestLocalHTTPProfileContract(t *testing.T) {
	server, err := NewLocalHTTPServer("orch", "test", testToolProvider{}, LocalHTTPProfile{
		WorkspaceRoot: t.TempDir(), BearerToken: "test-local-operator-credential",
	}, newTestLoggerRuntime())
	if err != nil {
		t.Fatal(err)
	}
	t.Run("invalid_headers_allocate_no_session", func(t *testing.T) { testLocalRejectedHeaders(t, server) })
	t.Run("isolated_sessions_and_delete", func(t *testing.T) { testLocalSessions(t, server) })
	t.Run("caller_scope_is_server_owned", func(t *testing.T) {
		if _, err := server.resolveHTTPToolScope(context.Background(), ToolCallParams{MetaAgentID: "forged"}); err == nil {
			t.Fatal("forged agent scope accepted")
		}
		scope, err := server.resolveHTTPToolScope(context.Background(), ToolCallParams{})
		if err != nil || scope.CWD != server.localProfile.WorkspaceRoot || scope.AgentID != "" {
			t.Fatalf("operator scope = %#v, %v", scope, err)
		}
		ctx, err := (&httpMCPSession{}).bindCallerPrincipal(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if _, ok := CallerPrincipalFromContext(ctx); ok {
			t.Fatal("zero profile principal unexpectedly bound")
		}
	})
}

func TestLocalHTTPProfileRejectsClientScopeMetadataAndIgnoresAttribution(t *testing.T) {
	server, err := NewLocalHTTPServer("orch", "test", testToolProvider{}, LocalHTTPProfile{
		WorkspaceRoot: t.TempDir(), BearerToken: "test-local-operator-credential",
	}, newTestLoggerRuntime())
	if err != nil {
		t.Fatal(err)
	}
	for _, params := range []ToolCallParams{{MetaAgentID: "forged"}, {MetaCallID: "forged"}} {
		if _, err := server.resolveHTTPToolScope(context.Background(), params); err == nil {
			t.Fatalf("forged client scope accepted: %#v", params)
		}
	}
	attributed, err := BindCallerAttribution(context.Background(), validCallerAttribution(t))
	if err != nil {
		t.Fatal(err)
	}
	scope, err := server.resolveHTTPToolScope(attributed, ToolCallParams{})
	if err != nil || scope.AgentID != "" {
		t.Fatalf("operator scope with task attribution = %#v, %v; want empty agent ID", scope, err)
	}
}

func TestTaskHTTPProfileProjectsVerifiedAttributionIntoToolScope(t *testing.T) {
	identity := validTaskSessionIdentity(t, "principal/task-a")
	authenticator := newTaskAuthenticatorFixture(t, map[string]TaskSessionIdentity{taskCredentialA: identity})
	scopes := make(chan ToolScope, 1)
	provider := captureToolProvider{call: func(ctx context.Context, _ string, _ json.RawMessage) (any, error) {
		scope, ok := ToolScopeFromContext(ctx)
		if !ok {
			return nil, errors.New("tool scope missing")
		}
		scopes <- scope
		return map[string]any{"ok": true}, nil
	}}
	server, err := NewLocalHTTPServer("orch", "test", provider, LocalHTTPProfile{
		WorkspaceRoot: t.TempDir(), TaskSessionAuthenticator: authenticator,
	}, newTestLoggerRuntime())
	if err != nil {
		t.Fatal(err)
	}
	initialize := serveTaskRequest(server, http.MethodPost, initializeTaskRequestBody, "", taskCredentialA)
	assertHTTPStatus(t, initialize, http.StatusOK)
	sessionID := initialize.Header().Get(httpMCPHeaderSessionID)
	if sessionID == "" {
		t.Fatal("task initialize omitted session ID")
	}
	initialized := serveTaskRequest(server, http.MethodPost, `{"jsonrpc":"2.0","method":"notifications/initialized"}`, sessionID, taskCredentialA)
	assertHTTPStatus(t, initialized, http.StatusAccepted)
	call := serveTaskRequest(server, http.MethodPost, `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"demo_tool","arguments":{}}}`, sessionID, taskCredentialA)
	assertHTTPStatus(t, call, http.StatusOK)
	select {
	case scope := <-scopes:
		if scope.AgentID != identity.Attribution.TaskID {
			t.Fatalf("task scope agent_id=%q, want verified attribution task_id=%q", scope.AgentID, identity.Attribution.TaskID)
		}
		if scope.CWD != server.localProfile.WorkspaceRoot || scope.Family != "orch" {
			t.Fatalf("task scope=%#v, want server workspace and family", scope)
		}
	case <-time.After(time.Second):
		t.Fatal("task tools/call did not reach provider")
	}
}

// TestLocalHTTPNegotiatesSupportedVersion 防止以预先拒绝替代标准版本协商。
func TestLocalHTTPNegotiatesSupportedVersion(t *testing.T) {
	server, err := NewLocalHTTPServer("orch", "test", testToolProvider{}, LocalHTTPProfile{
		WorkspaceRoot: t.TempDir(), BearerToken: "test-local-operator-credential",
	}, newTestLoggerRuntime())
	if err != nil {
		t.Fatal(err)
	}
	for _, version := range []string{"2025-11-25", "2025-06-18", "2024-11-05", "2099-01-01"} {
		t.Run(version, func(t *testing.T) {
			body := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"` + version + `","capabilities":{},"clientInfo":{"name":"test","version":"1"}}}`
			response := httptest.NewRecorder()
			server.handleMCP(response, localProfileRequest(server, http.MethodPost, body, ""))
			var reply struct {
				Error  *jsonRPCError `json:"error"`
				Result struct {
					ProtocolVersion string `json:"protocolVersion"`
				} `json:"result"`
			}
			if err := json.Unmarshal(response.Body.Bytes(), &reply); err != nil {
				t.Fatal(err)
			}
			if response.Code != http.StatusOK || reply.Error != nil || reply.Result.ProtocolVersion != "2025-11-25" {
				t.Fatalf("negotiation failed: %d %s", response.Code, response.Body.String())
			}
			if response.Header().Get(httpMCPHeaderSessionID) == "" {
				t.Fatal("negotiation omitted session")
			}
		})
	}
}

func localProfileRequest(server *HTTPServer, method, body, session string) *http.Request {
	r := httptest.NewRequest(method, "http://127.0.0.1:3000/mcp", strings.NewReader(body))
	r.Header.Set("Authorization", "Bearer "+server.localProfile.BearerToken)
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Accept", "application/json, text/event-stream")
	if session != "" {
		r.Header.Set(httpMCPHeaderSessionID, session)
		r.Header.Set("MCP-Protocol-Version", "2025-11-25")
	}
	return r
}

func testLocalRejectedHeaders(t *testing.T, server *HTTPServer) {
	t.Helper()
	tests := []struct {
		name   string
		alter  func(*http.Request)
		status int
	}{
		{"missing_auth", func(r *http.Request) { r.Header.Del("Authorization") }, http.StatusUnauthorized},
		{"foreign_origin", func(r *http.Request) { r.Header.Set("Origin", "https://attacker.invalid") }, http.StatusForbidden},
		{"duplicate_auth", func(r *http.Request) { r.Header.Add("Authorization", "Bearer forged") }, http.StatusBadRequest},
		{"q_zero", func(r *http.Request) { r.Header.Set("Accept", "application/json, text/event-stream;q=0") }, http.StatusNotAcceptable},
		{"wrong_media", func(r *http.Request) { r.Header.Set("Content-Type", "text/plain") }, http.StatusUnsupportedMediaType},
		{"unexpected_resume", func(r *http.Request) { r.Header.Set("Last-Event-ID", "other-stream") }, http.StatusBadRequest},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r := localProfileRequest(server, http.MethodPost, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25"}}`, "")
			tc.alter(r)
			response := httptest.NewRecorder()
			server.handleMCP(response, r)
			if response.Code != tc.status || len(server.sessions) != 0 {
				t.Fatalf("status=%d sessions=%d; want %d/0", response.Code, len(server.sessions), tc.status)
			}
		})
	}
}

func testLocalSessions(t *testing.T, server *HTTPServer) {
	t.Helper()
	var sessions []string
	for range 2 {
		response := httptest.NewRecorder()
		server.handleMCP(response, localProfileRequest(server, http.MethodPost, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25","capabilities":{},"clientInfo":{"name":"test","version":"1"}}}`, ""))
		if response.Code != http.StatusOK || response.Header().Get(httpMCPHeaderSessionID) == "" {
			t.Fatalf("initialize = %d %s", response.Code, response.Body.String())
		}
		sessions = append(sessions, response.Header().Get(httpMCPHeaderSessionID))
	}
	if sessions[0] == sessions[1] {
		t.Fatal("two clients share a session ID")
	}
	response := httptest.NewRecorder()
	server.handleMCP(response, localProfileRequest(server, http.MethodDelete, "", sessions[0]))
	if response.Code != http.StatusNoContent {
		t.Fatalf("DELETE status=%d", response.Code)
	}
	response = httptest.NewRecorder()
	server.handleMCP(response, localProfileRequest(server, http.MethodPost, `{"jsonrpc":"2.0","id":2,"method":"ping"}`, sessions[1]))
	if response.Code != http.StatusOK || len(server.sessions) != 1 {
		t.Fatalf("remaining session status=%d sessions=%d", response.Code, len(server.sessions))
	}
}
