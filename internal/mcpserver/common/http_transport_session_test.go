package common

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

const testHTTPMCPHeaderSessionID = "Mcp-Session-Id"

func TestHTTPInitializeCreatesSessionAndRejectsMissingSession(t *testing.T) {
	server := NewHTTPServer("test", "dev", testToolProvider{})
	sessionID := initializeHTTPSession(t, server)
	if sessionID == "" {
		t.Fatal("initialize session ID is empty")
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(
		`{"jsonrpc":"2.0","id":2,"method":"ping"}`,
	))
	server.handleMCP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("missing-session status = %d, want %d; body=%s", rec.Code, http.StatusBadRequest, rec.Body.String())
	}
}

func TestHTTPInitializeNotificationDoesNotCreateSession(t *testing.T) {
	server := NewHTTPServer("test", "dev", testToolProvider{})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(
		`{"jsonrpc":"2.0","method":"initialize","params":{"protocolVersion":"2025-11-25","capabilities":{},"clientInfo":{"name":"test"}}}`,
	))
	server.handleMCP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("initialize notification status = %d, want %d; body=%s", rec.Code, http.StatusBadRequest, rec.Body.String())
	}
	if got := len(server.sessions); got != 0 {
		t.Fatalf("sessions = %d, want 0", got)
	}
}

func TestHTTPIdleSessionReaperRefreshesAccessAndPreservesInFlightCall(t *testing.T) {
	provider := newCancellationTestProvider()
	server := NewHTTPServer("test", "dev", provider)
	server.sessionIdleTTL = time.Minute
	sessionID := initializeHTTPSession(t, server)
	session := requireHTTPSessionForTest(t, server, sessionID)

	session.mu.Lock()
	session.lastAccess = time.Now().Add(-2 * server.sessionIdleTTL)
	session.mu.Unlock()
	request := httptest.NewRequest(http.MethodPost, "/mcp", nil)
	request.Header.Set(testHTTPMCPHeaderSessionID, sessionID)
	if _, _, err := server.requireSession(request); err != nil {
		t.Fatalf("requireSession() error = %v", err)
	}
	server.reapIdleSessions(time.Now())
	waitForHTTPSessionCount(t, server, 1)

	done := startHTTPToolCall(t, server, sessionID, 31, "idle-in-flight", context.Background())
	waitProviderEvent(t, provider.started, "idle-in-flight")
	session.mu.Lock()
	session.lastAccess = time.Now().Add(-2 * server.sessionIdleTTL)
	session.mu.Unlock()
	server.reapIdleSessions(time.Now())
	waitForHTTPSessionCount(t, server, 1)

	cancelHTTPToolCall(t, server, sessionID, 31)
	waitProviderEvent(t, provider.canceled, "idle-in-flight")
	waitHTTPResponse(t, done)
	session.mu.Lock()
	session.lastAccess = time.Now().Add(-2 * server.sessionIdleTTL)
	session.mu.Unlock()
	server.reapIdleSessions(time.Now())
	waitForHTTPSessionCount(t, server, 0)
}

func TestHTTPIdleSessionReaperEvictsIdleSessionsAndStopTerminatesRemainder(t *testing.T) {
	provider := newCancellationTestProvider()
	server := NewHTTPServer("test", "dev", provider)
	server.sessionIdleTTL = 15 * time.Millisecond
	server.sessionReapInterval = time.Millisecond
	if _, err := server.Start(context.Background(), "127.0.0.1:0"); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	t.Cleanup(func() {
		stopCtx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := server.Stop(stopCtx); err != nil {
			t.Errorf("Stop() error = %v", err)
		}
	})

	initializeHTTPSession(t, server)
	waitForHTTPSessionCount(t, server, 0)

	sessionID := initializeHTTPSession(t, server)
	done := startHTTPToolCall(t, server, sessionID, 32, "stop-in-flight", context.Background())
	waitProviderEvent(t, provider.started, "stop-in-flight")
	stopCtx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := server.Stop(stopCtx); err != nil {
		t.Fatalf("Stop() error = %v", err)
	}
	waitProviderEvent(t, provider.canceled, "stop-in-flight")
	waitHTTPResponse(t, done)
	waitForHTTPSessionCount(t, server, 0)
	select {
	case <-server.sessionReaperDone:
	case <-time.After(time.Second):
		t.Fatal("session reaper did not stop")
	}
}

func TestHTTPCancellationIsIsolatedBySession(t *testing.T) {
	provider := newCancellationTestProvider()
	server := NewHTTPServer("test", "dev", provider)
	sessionA := initializeHTTPSession(t, server)
	sessionB := initializeHTTPSession(t, server)

	doneA := startHTTPToolCall(t, server, sessionA, 7, "session-a", context.Background())
	waitProviderEvent(t, provider.started, "session-a")
	doneB := startHTTPToolCall(t, server, sessionB, 7, "session-b", context.Background())
	waitProviderEvent(t, provider.started, "session-b")

	cancelHTTPToolCall(t, server, sessionA, 7)
	waitProviderEvent(t, provider.canceled, "session-a")
	select {
	case got := <-provider.canceled:
		t.Fatalf("session A cancellation crossed into %q", got)
	case <-time.After(100 * time.Millisecond):
	}
	waitHTTPResponse(t, doneA)

	cancelHTTPToolCall(t, server, sessionB, 7)
	waitProviderEvent(t, provider.canceled, "session-b")
	waitHTTPResponse(t, doneB)
}

func TestHTTPDuplicateRequestIDIsIsolatedBySession(t *testing.T) {
	provider := newCancellationTestProvider()
	server := NewHTTPServer("test", "dev", provider)
	sessionA := initializeHTTPSession(t, server)
	sessionB := initializeHTTPSession(t, server)

	doneA := startHTTPToolCall(t, server, sessionA, 9, "first-a", context.Background())
	waitProviderEvent(t, provider.started, "first-a")

	duplicate := invokeHTTPToolCall(t, server, sessionA, 9, "duplicate-a", context.Background())
	if duplicate.Code != http.StatusOK || !strings.Contains(duplicate.Body.String(), "duplicate active request id") {
		t.Fatalf("same-session duplicate = status %d body %s", duplicate.Code, duplicate.Body.String())
	}
	if got := provider.callCount("duplicate-a"); got != 0 {
		t.Fatalf("same-session duplicate provider calls = %d, want 0", got)
	}

	doneB := startHTTPToolCall(t, server, sessionB, 9, "first-b", context.Background())
	waitProviderEvent(t, provider.started, "first-b")
	cancelHTTPToolCall(t, server, sessionA, 9)
	waitProviderEvent(t, provider.canceled, "first-a")
	waitHTTPResponse(t, doneA)
	cancelHTTPToolCall(t, server, sessionB, 9)
	waitProviderEvent(t, provider.canceled, "first-b")
	waitHTTPResponse(t, doneB)
}

func TestHTTPClientDisconnectCancelsToolCall(t *testing.T) {
	provider := newCancellationTestProvider()
	server := NewHTTPServer("test", "dev", provider)
	sessionID := initializeHTTPSession(t, server)
	ctx, cancel := context.WithCancel(context.Background())
	done := startHTTPToolCall(t, server, sessionID, 12, "disconnect", ctx)
	waitProviderEvent(t, provider.started, "disconnect")

	cancel()
	waitProviderEvent(t, provider.canceled, "disconnect")
	waitHTTPResponse(t, done)
}

func TestHTTPNetworkDisconnectCancelsToolCall(t *testing.T) {
	provider := newCancellationTestProvider()
	server := NewHTTPServer("test", "dev", provider)
	addr, err := server.Start(context.Background(), "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	t.Cleanup(func() {
		stopCtx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := server.Stop(stopCtx); err != nil {
			t.Errorf("Stop() error = %v", err)
		}
	})
	client := &http.Client{}
	sessionID := initializeHTTPNetworkSession(t, client, "http://"+addr+"/mcp")
	ctx, cancel := context.WithCancel(context.Background())
	raw, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      14,
		"method":  "tools/call",
		"params":  map[string]any{"name": "network-disconnect", "arguments": map[string]any{}},
	})
	if err != nil {
		t.Fatalf("marshal tools/call: %v", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://"+addr+"/mcp", bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("NewRequestWithContext() error = %v", err)
	}
	req.Header.Set(testHTTPMCPHeaderSessionID, sessionID)
	done := make(chan error, 1)
	var requestGroup sync.WaitGroup
	requestGroup.Go(func() {
		resp, requestErr := client.Do(req)
		if resp != nil {
			_ = resp.Body.Close()
		}
		done <- requestErr
	})
	t.Cleanup(requestGroup.Wait)
	waitProviderEvent(t, provider.started, "network-disconnect")

	cancel()
	waitProviderEvent(t, provider.canceled, "network-disconnect")
	select {
	case requestErr := <-done:
		if requestErr == nil {
			t.Fatal("network request error = nil, want disconnect cancellation")
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for disconnected HTTP request")
	}
}

func TestHTTPMalformedCancellationIsContainedWithinSession(t *testing.T) {
	provider := newCancellationTestProvider()
	server := NewHTTPServer("test", "dev", provider)
	sessionID := initializeHTTPSession(t, server)
	done := startHTTPToolCall(t, server, sessionID, 13, "malformed-cancel", context.Background())
	waitProviderEvent(t, provider.started, "malformed-cancel")

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(
		`{"jsonrpc":"2.0","method":"notifications/cancelled","params":{"requestId":13,"reason":{"invalid":true}}}`,
	))
	req.Header.Set(testHTTPMCPHeaderSessionID, sessionID)
	server.handleMCP(rec, req)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("malformed cancel status = %d, want %d; body=%s", rec.Code, http.StatusAccepted, rec.Body.String())
	}
	select {
	case got := <-provider.canceled:
		t.Fatalf("malformed cancellation canceled %q", got)
	case <-time.After(100 * time.Millisecond):
	}

	cancelHTTPToolCall(t, server, sessionID, 13)
	waitProviderEvent(t, provider.canceled, "malformed-cancel")
	waitHTTPResponse(t, done)
}

func TestHTTPDeleteSessionCancelsOnlyThatSession(t *testing.T) {
	provider := newCancellationTestProvider()
	server := NewHTTPServer("test", "dev", provider)
	sessionA := initializeHTTPSession(t, server)
	sessionB := initializeHTTPSession(t, server)
	doneA := startHTTPToolCall(t, server, sessionA, 21, "delete-a", context.Background())
	waitProviderEvent(t, provider.started, "delete-a")
	doneB := startHTTPToolCall(t, server, sessionB, 21, "keep-b", context.Background())
	waitProviderEvent(t, provider.started, "keep-b")

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodDelete, "/mcp", nil)
	req.Header.Set(testHTTPMCPHeaderSessionID, sessionA)
	server.handleMCP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("DELETE status = %d, want %d; body=%s", rec.Code, http.StatusNoContent, rec.Body.String())
	}
	waitProviderEvent(t, provider.canceled, "delete-a")
	waitHTTPResponse(t, doneA)
	select {
	case got := <-provider.canceled:
		t.Fatalf("session deletion crossed into %q", got)
	case <-time.After(100 * time.Millisecond):
	}

	cancelHTTPToolCall(t, server, sessionB, 21)
	waitProviderEvent(t, provider.canceled, "keep-b")
	waitHTTPResponse(t, doneB)
}

func initializeHTTPSession(t *testing.T, server *HTTPServer) string {
	t.Helper()
	return initializeHTTPSessionWithBearer(t, server, "")
}

func initializeHTTPSessionWithBearer(t *testing.T, server *HTTPServer, bearerToken string) string {
	t.Helper()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25","capabilities":{},"clientInfo":{"name":"test"}}}`,
	))
	if bearerToken != "" {
		req.Header.Set("Authorization", "Bearer "+bearerToken)
	}
	server.handleMCP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("initialize status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	sessionID := rec.Header().Get(testHTTPMCPHeaderSessionID)
	if sessionID == "" {
		t.Fatal("initialize did not return Mcp-Session-Id")
	}
	return sessionID
}

func initializeHTTPNetworkSession(t *testing.T, client *http.Client, endpoint string) string {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, endpoint, strings.NewReader(
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25","capabilities":{},"clientInfo":{"name":"network-test"}}}`,
	))
	if err != nil {
		t.Fatalf("NewRequest(initialize) error = %v", err)
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("initialize request error = %v", err)
	}
	defer resp.Body.Close()
	if _, err := io.Copy(io.Discard, resp.Body); err != nil {
		t.Fatalf("read initialize response: %v", err)
	}
	sessionID := resp.Header.Get(testHTTPMCPHeaderSessionID)
	if resp.StatusCode != http.StatusOK || sessionID == "" {
		t.Fatalf("initialize status = %d session = %q, want 200 and session", resp.StatusCode, sessionID)
	}
	return sessionID
}

func attachInitializedHTTPSession(t *testing.T, server *HTTPServer, req *http.Request) {
	t.Helper()
	req.Header.Set(testHTTPMCPHeaderSessionID, initializeHTTPSession(t, server))
}

func startHTTPToolCall(
	t *testing.T,
	server *HTTPServer,
	sessionID string,
	id int,
	name string,
	ctx context.Context,
) <-chan *httptest.ResponseRecorder {
	t.Helper()
	done := make(chan *httptest.ResponseRecorder, 1)
	var requestGroup sync.WaitGroup
	requestGroup.Go(func() {
		done <- invokeHTTPToolCall(t, server, sessionID, id, name, ctx)
	})
	t.Cleanup(requestGroup.Wait)
	return done
}

func invokeHTTPToolCall(
	t *testing.T,
	server *HTTPServer,
	sessionID string,
	id int,
	name string,
	ctx context.Context,
) *httptest.ResponseRecorder {
	t.Helper()
	raw, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"method":  "tools/call",
		"params":  map[string]any{"name": name, "arguments": map[string]any{}},
	})
	if err != nil {
		t.Fatalf("marshal tools/call: %v", err)
	}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/mcp", bytes.NewReader(raw)).WithContext(ctx)
	req.Header.Set(testHTTPMCPHeaderSessionID, sessionID)
	server.handleMCP(rec, req)
	return rec
}

func cancelHTTPToolCall(t *testing.T, server *HTTPServer, sessionID string, requestID int) {
	t.Helper()
	raw, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"method":  "notifications/cancelled",
		"params":  map[string]any{"requestId": requestID},
	})
	if err != nil {
		t.Fatalf("marshal cancellation: %v", err)
	}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/mcp", bytes.NewReader(raw))
	req.Header.Set(testHTTPMCPHeaderSessionID, sessionID)
	server.handleMCP(rec, req)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("cancel status = %d, want %d; body=%s", rec.Code, http.StatusAccepted, rec.Body.String())
	}
}

func waitHTTPResponse(t *testing.T, done <-chan *httptest.ResponseRecorder) *httptest.ResponseRecorder {
	t.Helper()
	select {
	case rec := <-done:
		return rec
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for HTTP response")
		return nil
	}
}

func requireHTTPSessionForTest(t *testing.T, server *HTTPServer, sessionID string) *httpMCPSession {
	t.Helper()
	server.sessionsMu.RLock()
	session := server.sessions[sessionID]
	server.sessionsMu.RUnlock()
	if session == nil {
		t.Fatalf("session %q is not active", sessionID)
	}
	return session
}

func waitForHTTPSessionCount(t *testing.T, server *HTTPServer, want int) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for {
		server.sessionsMu.RLock()
		got := len(server.sessions)
		server.sessionsMu.RUnlock()
		if got == want {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("sessions = %d, want %d", got, want)
		}
		time.Sleep(time.Millisecond)
	}
}
