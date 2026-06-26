package codexapp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
	"github.com/gorilla/websocket"
)

type rpcMethodRecorder struct {
	mu      sync.Mutex
	methods []string
}

func (r *rpcMethodRecorder) record(method string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.methods = append(r.methods, method)
}

func (r *rpcMethodRecorder) snapshot() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.methods...)
}

func (r *rpcMethodRecorder) count(method string) int {
	count := 0
	for _, got := range r.snapshot() {
		if got == method {
			count++
		}
	}
	return count
}

func (r *rpcMethodRecorder) contains(method string) bool {
	for _, got := range r.snapshot() {
		if got == method {
			return true
		}
	}
	return false
}

type codexRPCHandler func(jsonRPCMessage) (json.RawMessage, bool)

func newCodexTestRPCServer(t *testing.T, handler codexRPCHandler) *httptest.Server {
	t.Helper()
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		serveCodexTestRPCConn(t, conn, handler)
	}))
}

func serveCodexTestRPCConn(t *testing.T, conn *websocket.Conn, handler codexRPCHandler) {
	t.Helper()
	defer conn.Close()
	for {
		_, rawBytes, err := conn.ReadMessage()
		if err != nil {
			return
		}
		msg, ok := decodeRecoveryTransportRPCMessage(rawBytes)
		if !ok {
			continue
		}
		result, respond := handler(msg)
		if !respond || len(msg.ID) == 0 {
			continue
		}
		if !writeRecoveryTransportRPCResponse(t, conn, msg, result) {
			return
		}
	}
}

func decodeRecoveryTransportRPCMessage(raw []byte) (jsonRPCMessage, bool) {
	var msg jsonRPCMessage
	if err := json.Unmarshal(raw, &msg); err != nil {
		return jsonRPCMessage{}, false
	}
	return msg, true
}

func writeRecoveryTransportRPCResponse(t *testing.T, conn *websocket.Conn, msg jsonRPCMessage, result json.RawMessage) bool {
	t.Helper()
	resp, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      json.RawMessage(append([]byte(nil), msg.ID...)),
		"result":  json.RawMessage(append([]byte(nil), result...)),
	})
	if err != nil {
		t.Fatalf("marshal response: %v", err)
	}
	return conn.WriteMessage(websocket.TextMessage, resp) == nil
}

func recordMethodsAndOK(recorder *rpcMethodRecorder) codexRPCHandler {
	return func(msg jsonRPCMessage) (json.RawMessage, bool) {
		if strings.TrimSpace(msg.Method) == "" {
			return nil, false
		}
		recorder.record(msg.Method)
		return mustJSON(map[string]any{"ok": true}), true
	}
}

func okIDOnlyHandler(msg jsonRPCMessage) (json.RawMessage, bool) {
	return mustJSON(map[string]any{"ok": true}), len(msg.ID) > 0
}

func newOneShotCodexTestRPCServer(t *testing.T) *httptest.Server {
	t.Helper()
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		respondToFirstCodexTestRPCMessage(t, conn)
	}))
}

func respondToFirstCodexTestRPCMessage(t *testing.T, conn *websocket.Conn) {
	t.Helper()
	defer conn.Close()
	for {
		_, rawBytes, err := conn.ReadMessage()
		if err != nil {
			return
		}
		msg, ok := decodeRecoveryTransportRPCMessage(rawBytes)
		if !ok || len(msg.ID) == 0 {
			continue
		}
		writeRecoveryTransportRPCResponse(t, conn, msg, mustJSON(map[string]any{"ok": true}))
		return
	}
}

func newTestTransport(t *testing.T, ctx context.Context, server *httptest.Server) *transport {
	t.Helper()
	transport, err := newTransport(ctx, "ws"+strings.TrimPrefix(server.URL, "http"))
	if err != nil {
		t.Fatalf("newTransport() error = %v", err)
	}
	t.Cleanup(func() { _ = transport.Kill() })
	return transport
}

func TestTransportReconnectReinitializes(t *testing.T) {
	t.Parallel()

	recorder := &rpcMethodRecorder{methods: make([]string, 0, 4)}
	server := newCodexTestRPCServer(t, recordMethodsAndOK(recorder))
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	transport := newTestTransport(t, ctx, server)

	if err := transport.reconnect(ctx); err != nil {
		t.Fatalf("reconnect() error = %v", err)
	}

	if got := recorder.count("initialize"); got != 2 {
		t.Fatalf("initialize count = %d, want 2 after startup+reconnect; methods=%v", got, recorder.snapshot())
	}
}

func TestTransportInitializeTimeoutReturnsErrorWithoutRepeatedReadPanic(t *testing.T) {
	t.Parallel()

	initializeReceived := make(chan struct{}, 1)
	releaseServerConn := make(chan struct{})
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		for {
			_, rawBytes, err := conn.ReadMessage()
			if err != nil {
				return
			}
			msg, ok := decodeRecoveryTransportRPCMessage(rawBytes)
			if !ok || strings.TrimSpace(msg.Method) != "initialize" {
				continue
			}
			initializeReceived <- struct{}{}
			<-releaseServerConn
			return
		}
	}))
	defer func() {
		close(releaseServerConn)
		server.CloseClientConnections()
		server.Close()
	}()

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	var err error
	var panicked any
	func() {
		defer func() { panicked = recover() }()
		_, err = newTransport(ctx, "ws"+strings.TrimPrefix(server.URL, "http"))
	}()

	if panicked != nil {
		t.Fatalf("newTransport() panicked = %v, want returned error", panicked)
	}
	if err == nil {
		t.Fatal("newTransport() error = nil, want initialize timeout error")
	}
	select {
	case <-initializeReceived:
	default:
		t.Fatal("server did not receive initialize request")
	}
}

func TestRecoveryCheckHealthUsesWebSocketPingOnly(t *testing.T) {
	t.Parallel()

	recorder := &rpcMethodRecorder{methods: make([]string, 0, 2)}
	server := newCodexTestRPCServer(t, recordMethodsAndOK(recorder))
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	transport := newTestTransport(t, ctx, server)

	recovery := &recoveryManager{transport: transport}
	if err := recovery.CheckHealth(ctx); err != nil {
		t.Fatalf("CheckHealth() error = %v", err)
	}

	if recorder.contains("app/list") {
		t.Fatalf("CheckHealth() sent app/list; methods=%v", recorder.snapshot())
	}
	if got := recorder.count("initialize"); got != 1 {
		t.Fatalf("initialize count = %d, want 1; methods=%v", got, recorder.snapshot())
	}
}

func TestTransportReconnectDoesNotDispatchConnectionDeadForSupersededReader(t *testing.T) {
	t.Parallel()

	server := newCodexTestRPCServer(t, okIDOnlyHandler)
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	transport := newTestTransport(t, ctx, server)

	dead, done := startConnectionDeadReadLoop(ctx, transport)
	waitForTransportLooping(t, transport)

	if err := transport.reconnect(ctx); err != nil {
		t.Fatalf("reconnect() error = %v", err)
	}
	waitForReadLoopDone(t, done)
	assertNoConnectionDead(t, dead)
}

func TestSupersededReadLoopDoesNotFailPendingCallsFromReplacementSocket(t *testing.T) {
	t.Parallel()

	transport := &transport{serverURL: "ws://127.0.0.1:1"}
	oldSocket := &websocket.Conn{}
	transport.ws = &websocket.Conn{}
	pending := &pendingCall{done: make(chan struct{})}
	transport.pending.Store("initialize", pending)
	defer transport.pending.Delete("initialize")

	transport.endReadLoop(context.Background(), nil, oldSocket, errors.New("old socket closed"), "old socket closed")

	select {
	case <-pending.done:
		t.Fatal("superseded read loop failed a pending call from the replacement socket")
	default:
	}
}

func TestTransportPassiveDisconnectDispatchesConnectionDead(t *testing.T) {
	t.Parallel()

	server := newOneShotCodexTestRPCServer(t)
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	transport := newTestTransport(t, ctx, server)

	dead := make(chan RawMessage, 1)
	transport.ReadLoop(ctx, connectionDeadRecorder(dead))
	assertConnectionDeadReceived(t, dead)
}

func startConnectionDeadReadLoop(ctx context.Context, transport *transport) (<-chan RawMessage, <-chan struct{}) {
	dead := make(chan RawMessage, 1)
	done := make(chan struct{})
	go func() {
		defer close(done)
		transport.ReadLoop(ctx, connectionDeadRecorder(dead))
	}()
	return dead, done
}

func connectionDeadRecorder(dead chan<- RawMessage) func(context.Context, Responder, RawMessage) {
	return func(_ context.Context, _ Responder, msg RawMessage) {
		if strings.TrimSpace(msg.Method) == "connection.dead" {
			dead <- msg
		}
	}
}

func waitForTransportLooping(t *testing.T, transport *transport) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for !transport.looping.Load() && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if !transport.looping.Load() {
		t.Fatal("read loop did not start")
	}
}

func waitForReadLoopDone(t *testing.T, done <-chan struct{}) {
	t.Helper()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("read loop did not exit after reconnect closed old socket")
	}
}

func assertNoConnectionDead(t *testing.T, dead <-chan RawMessage) {
	t.Helper()
	select {
	case msg := <-dead:
		t.Fatalf("unexpected connection.dead from superseded reader: %+v", msg)
	default:
	}
}

func assertConnectionDeadReceived(t *testing.T, dead <-chan RawMessage) {
	t.Helper()
	select {
	case <-dead:
	case <-time.After(time.Second):
		t.Fatal("passive disconnect did not dispatch connection.dead")
	}
}

func TestTransportClosingDisconnectDoesNotDispatchConnectionDead(t *testing.T) {
	t.Parallel()

	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		for {
			_, rawBytes, err := conn.ReadMessage()
			if err != nil {
				return
			}
			var msg jsonRPCMessage
			if err := json.Unmarshal(rawBytes, &msg); err != nil || len(msg.ID) == 0 {
				continue
			}
			resp, err := json.Marshal(map[string]any{
				"jsonrpc": "2.0",
				"id":      json.RawMessage(append([]byte(nil), msg.ID...)),
				"result":  map[string]any{"ok": true},
			})
			if err != nil {
				t.Fatalf("marshal response: %v", err)
			}
			_ = conn.WriteMessage(websocket.TextMessage, resp)
			return
		}
	}))
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	transport, err := newTransport(ctx, "ws"+strings.TrimPrefix(server.URL, "http"))
	if err != nil {
		t.Fatalf("newTransport() error = %v", err)
	}
	defer func() { _ = transport.Kill() }()

	transport.closing.Store(true)
	dead := make(chan RawMessage, 1)
	transport.ReadLoop(ctx, func(_ context.Context, _ Responder, msg RawMessage) {
		if strings.TrimSpace(msg.Method) == "connection.dead" {
			dead <- msg
		}
	})
	select {
	case msg := <-dead:
		t.Fatalf("unexpected connection.dead during shutdown: %+v", msg)
	default:
	}
}

func TestTransportClosingErrorDoesNotTriggerReconnect(t *testing.T) {
	if shouldReconnect(errors.New("codexapp: transport closing")) {
		t.Fatal("transport closing must not trigger recovery")
	}
}

func TestSessionAttemptRecoveryReplaysPendingTurn(t *testing.T) {
	recorder := &sessionRecoveryRPCRecorder{}
	server := newCodexTestRPCServer(t, recorder.handle)
	defer server.Close()

	s := newRecoveryTestSession(t, server)
	defer closeCodexTestSession(t, s)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	handle := startRecoveryTestTurn(t, ctx, s)
	beforeReadAt := prepareRecoveryReplayState(s)

	if err := s.attemptRecovery("test recovery"); err != nil {
		t.Fatalf("attemptRecovery() error = %v", err)
	}

	assertRecoveryReplayedTurn(t, s, handle, beforeReadAt)
	recorder.assertCounts(t)
}

type sessionRecoveryRPCRecorder struct {
	mu              sync.Mutex
	turnStarts      int
	initializeCalls int
	threadResumes   int
	threadResumeCWD string
}

func (r *sessionRecoveryRPCRecorder) handle(msg jsonRPCMessage) (json.RawMessage, bool) {
	if len(msg.ID) == 0 {
		return nil, false
	}
	switch msg.Method {
	case "initialize":
		r.incrementInitialize()
		return mustJSON(map[string]any{"ok": true}), true
	case "thread/resume":
		r.incrementThreadResume(msg.Params)
		return mustJSON(map[string]any{"thread": map[string]any{"id": "thread-1"}}), true
	case "turn/start":
		current := r.incrementTurnStart()
		return mustJSON(map[string]any{"turn": map[string]any{"id": fmt.Sprintf("turn-%d", current)}}), true
	default:
		return mustJSON(map[string]any{"ok": true}), true
	}
}

func (r *sessionRecoveryRPCRecorder) incrementInitialize() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.initializeCalls++
}

func (r *sessionRecoveryRPCRecorder) incrementThreadResume(params json.RawMessage) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.threadResumes++
	var decoded threadResumeParams
	if err := json.Unmarshal(params, &decoded); err == nil {
		r.threadResumeCWD = strings.TrimSpace(decoded.Cwd)
	}
}

func (r *sessionRecoveryRPCRecorder) incrementTurnStart() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.turnStarts++
	return r.turnStarts
}

func (r *sessionRecoveryRPCRecorder) assertCounts(t *testing.T) {
	t.Helper()
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.turnStarts != 2 {
		t.Fatalf("turn/start count = %d, want 2", r.turnStarts)
	}
	if r.initializeCalls != 2 {
		t.Fatalf("initialize count = %d, want 2", r.initializeCalls)
	}
	if r.threadResumes != 1 {
		t.Fatalf("thread/resume count = %d, want 1", r.threadResumes)
	}
	if r.threadResumeCWD == "" || r.threadResumeCWD == "." {
		t.Fatalf("thread/resume cwd = %q, want runtime cwd", r.threadResumeCWD)
	}
}

func newRecoveryTestSession(t *testing.T, server *httptest.Server) *session {
	t.Helper()
	s, err := newSession(context.Background(), pkglogger.Get(), "ws"+strings.TrimPrefix(server.URL, "http"), "agent-1", nil, nil, nil)
	if err != nil {
		t.Fatalf("newSession() error = %v", err)
	}
	// 恢复路径也要显式启动 sidecar runtime，保持与 driver.StartSession 的生命周期一致。
	s.runtime.Start()
	s.setRuntimeConfigValue("cwd", t.TempDir())
	return s
}

func TestSessionResumeThreadAfterRecoveryRejectsMissingRuntimeCWD(t *testing.T) {
	s := &session{}
	s.setThreadID("thread-1")

	err := s.resumeThreadAfterRecovery(context.Background())
	if err == nil || !strings.Contains(err.Error(), "recovery cwd is required") {
		t.Fatalf("resumeThreadAfterRecovery() error = %v, want cwd required", err)
	}
}

type recoveryTestTurnHandle interface {
	ProviderID() string
}

func startRecoveryTestTurn(t *testing.T, ctx context.Context, s *session) recoveryTestTurnHandle {
	t.Helper()
	handle, err := s.StartTurn(ctx, dto.TurnRequest{
		ThreadID: "thread-1",
		Inputs:   []dto.InputItem{{Type: "text", Content: "hello"}},
	})
	if err != nil {
		t.Fatalf("StartTurn() error = %v", err)
	}
	if got := handle.ProviderID(); got != "turn-1" {
		t.Fatalf("initial ProviderID() = %q, want turn-1", got)
	}
	return handle
}

func prepareRecoveryReplayState(s *session) int64 {
	s.setThreadID("thread-1")
	beforeReadAt := time.Now().Add(-time.Second).UnixNano()
	s.lastReadAt.Store(beforeReadAt)
	s.mu.Lock()
	s.suppressed["stale-turn"] = struct{}{}
	s.mu.Unlock()
	return beforeReadAt
}

func assertRecoveryReplayedTurn(t *testing.T, s *session, handle recoveryTestTurnHandle, beforeReadAt int64) {
	t.Helper()
	if got := handle.ProviderID(); got != "turn-2" {
		t.Fatalf("ProviderID() after replay = %q, want turn-2", got)
	}
	if got := s.activeTurnID; got != "turn-2" {
		t.Fatalf("activeTurnID = %q, want turn-2", got)
	}
	if got := s.recoveryCount.Load(); got != 0 {
		t.Fatalf("recoveryCount = %d, want 0 after successful recovery", got)
	}
	if got := s.lastReadAt.Load(); got <= beforeReadAt {
		t.Fatalf("lastReadAt = %d, want value newer than %d after successful recovery", got, beforeReadAt)
	}
	if suppressedLen := suppressedTurnCount(s); suppressedLen != 0 {
		t.Fatalf("suppressed size = %d, want 0 after successful recovery", suppressedLen)
	}
}

func suppressedTurnCount(s *session) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.suppressed)
}

func TestSessionAttemptRecoveryStopsAfterMaxAttempts(t *testing.T) {
	handle := newTurnHandle("local-1", "provider-1")
	s := &session{
		turns:      map[string]*turnHandle{"provider-1": handle},
		suppressed: map[string]struct{}{},
	}

	for i := 0; i < maxRecoveryAttempts; i++ {
		err := s.attemptRecovery("test recovery")
		if err == nil || !strings.Contains(err.Error(), "recovery unavailable") {
			t.Fatalf("attempt %d error = %v, want recovery unavailable", i+1, err)
		}
	}

	err := s.attemptRecovery("test recovery")
	if err == nil || !strings.Contains(err.Error(), fmt.Sprintf("max recovery attempts (%d) exceeded", maxRecoveryAttempts)) {
		t.Fatalf("attempt %d error = %v, want max recovery attempts exceeded", maxRecoveryAttempts+1, err)
	}
	select {
	case <-handle.Done():
	default:
		t.Fatal("handle.Done() not closed after max recovery attempts")
	}
	if got := handle.Err(); got == nil || !strings.Contains(got.Error(), "max recovery attempts exceeded") {
		t.Fatalf("handle.Err() = %v, want max recovery attempts exceeded", got)
	}
}
