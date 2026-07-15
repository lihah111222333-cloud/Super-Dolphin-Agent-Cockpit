package codexapp

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	dto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/provider"
	pkglogger "github.com/lihah111222333-cloud/super-dolphin-agent/pkg/logger"
)

func TestForceCompletePinsActiveTurnBeforeRemoteCall(t *testing.T) {
	fixture := newBlockingForceCompleteFixture(t)
	defer fixture.close()

	s := newForceCompleteTestSession(t, fixture.url())
	s.runtime.Start()
	defer closeCodexTestSession(t, s)

	oldTurn, newTurn := configureForceCompleteTurns(s, "turn-old", "turn-new", "turn-old")
	done := startForceComplete(t, s)
	fixture.awaitStarted(t)
	fixture.assertNextTurnID(t, "turn-old")

	setForceCompleteActiveTurn(s, "turn-new")
	fixture.release()
	assertForceCompleteFinished(t, done)
	assertTurnDone(t, oldTurn, "old active turn was not completed")
	assertTurnOpen(t, newTurn, "new active turn was completed; ForceComplete did not pin the original active turn")
	assertPinnedForceCompleteSessionState(t, s)
}

func TestForceCompleteFallsBackWhenTurnIDRejected(t *testing.T) {
	fixture := newRejectingForceCompleteFixture(t)
	defer fixture.close()

	s := newForceCompleteTestSession(t, fixture.url())
	s.runtime.Start()
	defer closeCodexTestSession(t, s)

	active := configureSingleForceCompleteTurn(s, "turn-1")
	if err := s.ForceComplete(context.Background(), dto.ForceCompleteRequest{ThreadID: "thread-1"}); err != nil {
		t.Fatalf("ForceComplete() error = %v", err)
	}

	fixture.assertFirstTurnID(t, "turn-1")
	fixture.assertFallbackThreadID(t, "thread-1")
	assertTurnDone(t, active, "forceComplete fallback did not finish active turn")
}

func TestForceCompleteIgnoresStaleProviderID(t *testing.T) {
	fixture := newCountingForceCompleteFixture(t)
	defer fixture.close()

	s := newForceCompleteTestSession(t, fixture.url())
	defer closeCodexTestSession(t, s)

	oldTurn, newTurn := configureForceCompleteTurns(s, "turn-old", "turn-new", "turn-new")
	err := s.ForceComplete(context.Background(), dto.ForceCompleteRequest{ThreadID: "thread-1", ProviderID: "turn-old"})
	if !errors.Is(err, ErrForceCompleteTargetNotFound) {
		t.Fatalf("ForceComplete() error = %v, want ErrForceCompleteTargetNotFound", err)
	}

	fixture.assertNoForceComplete(t)
	assertTurnOpen(t, oldTurn, "stale ProviderID completed old turn")
	assertTurnOpen(t, newTurn, "stale ProviderID completed active turn")
	assertStaleProviderSessionState(t, s)
}

// TestForceCompleteNoTargetReturnsError verifies no-target force complete is not a silent success.
func TestForceCompleteNoTargetReturnsError(t *testing.T) {
	fixture := newCountingForceCompleteFixture(t)
	defer fixture.close()

	s := newForceCompleteTestSession(t, fixture.url())
	defer closeCodexTestSession(t, s)

	err := s.ForceComplete(context.Background(), dto.ForceCompleteRequest{ThreadID: "thread-1"})
	if err == nil || !strings.Contains(err.Error(), "force complete target not found") {
		t.Fatalf("ForceComplete() error = %v, want force complete target not found", err)
	}
	fixture.assertNoForceComplete(t)
}

type forceCompleteResponseMode int

const (
	forceCompleteRespondOK forceCompleteResponseMode = iota
	forceCompleteResponseWritten
	forceCompleteStopServing
)

type forceCompleteHandler interface {
	handleForceComplete(conn *websocket.Conn, msg jsonRPCMessage, params map[string]any) forceCompleteResponseMode
}

type blockingForceCompleteFixture struct {
	server    *httptest.Server
	started   chan struct{}
	releaseCh chan struct{}
	once      sync.Once
	params    chan map[string]any
}

func newBlockingForceCompleteFixture(t *testing.T) *blockingForceCompleteFixture {
	t.Helper()
	fixture := &blockingForceCompleteFixture{
		started:   make(chan struct{}),
		releaseCh: make(chan struct{}),
		params:    make(chan map[string]any, 1),
	}
	fixture.server = httptest.NewServer(http.HandlerFunc(fixture.serve))
	return fixture
}

func (f *blockingForceCompleteFixture) serve(w http.ResponseWriter, r *http.Request) {
	serveForceCompleteWebSocket(w, r, f)
}

func (f *blockingForceCompleteFixture) handleForceComplete(_ *websocket.Conn, _ jsonRPCMessage, params map[string]any) forceCompleteResponseMode {
	storeForceCompleteParams(f.params, params)
	f.once.Do(func() { close(f.started) })
	<-f.releaseCh
	return forceCompleteRespondOK
}

func (f *blockingForceCompleteFixture) url() string {
	return "ws" + strings.TrimPrefix(f.server.URL, "http")
}

func (f *blockingForceCompleteFixture) close() {
	f.server.Close()
}

func (f *blockingForceCompleteFixture) awaitStarted(t *testing.T) {
	t.Helper()
	select {
	case <-f.started:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for turn/forceComplete call")
	}
}

func (f *blockingForceCompleteFixture) release() {
	close(f.releaseCh)
}

func (f *blockingForceCompleteFixture) assertNextTurnID(t *testing.T, want string) {
	t.Helper()
	params := receiveForceCompleteParams(t, f.params, "timed out waiting for turn/forceComplete params")
	if params["turnId"] != want {
		t.Fatalf("remote forceComplete turnId = %#v, want %s", params["turnId"], want)
	}
}

type rejectingForceCompleteFixture struct {
	server *httptest.Server
	params chan map[string]any
}

func newRejectingForceCompleteFixture(t *testing.T) *rejectingForceCompleteFixture {
	t.Helper()
	fixture := &rejectingForceCompleteFixture{params: make(chan map[string]any, 2)}
	fixture.server = httptest.NewServer(http.HandlerFunc(fixture.serve))
	return fixture
}

func (f *rejectingForceCompleteFixture) serve(w http.ResponseWriter, r *http.Request) {
	serveForceCompleteWebSocket(w, r, f)
}

func (f *rejectingForceCompleteFixture) handleForceComplete(conn *websocket.Conn, msg jsonRPCMessage, params map[string]any) forceCompleteResponseMode {
	storeForceCompleteParams(f.params, params)
	if _, hasTurnID := params["turnId"]; hasTurnID {
		if !writeForceCompleteError(conn, msg, "extra field turnId not permitted") {
			return forceCompleteStopServing
		}
		return forceCompleteResponseWritten
	}
	return forceCompleteRespondOK
}

func (f *rejectingForceCompleteFixture) url() string {
	return "ws" + strings.TrimPrefix(f.server.URL, "http")
}

func (f *rejectingForceCompleteFixture) close() {
	f.server.Close()
}

func (f *rejectingForceCompleteFixture) assertFirstTurnID(t *testing.T, want string) {
	t.Helper()
	params := receiveForceCompleteParams(t, f.params, "timed out waiting for first turn/forceComplete")
	if params["turnId"] != want {
		t.Fatalf("first forceComplete params = %#v, want turnId %s", params, want)
	}
}

func (f *rejectingForceCompleteFixture) assertFallbackThreadID(t *testing.T, want string) {
	t.Helper()
	params := receiveForceCompleteParams(t, f.params, "timed out waiting for fallback turn/forceComplete")
	if _, hasTurnID := params["turnId"]; hasTurnID {
		t.Fatalf("fallback forceComplete params = %#v, want no turnId", params)
	}
	if params["threadId"] != want {
		t.Fatalf("fallback forceComplete threadId = %#v, want %s", params["threadId"], want)
	}
}

type countingForceCompleteFixture struct {
	server *httptest.Server
	calls  chan struct{}
}

func newCountingForceCompleteFixture(t *testing.T) *countingForceCompleteFixture {
	t.Helper()
	fixture := &countingForceCompleteFixture{calls: make(chan struct{}, 1)}
	fixture.server = httptest.NewServer(http.HandlerFunc(fixture.serve))
	return fixture
}

func (f *countingForceCompleteFixture) serve(w http.ResponseWriter, r *http.Request) {
	serveForceCompleteWebSocket(w, r, f)
}

func (f *countingForceCompleteFixture) handleForceComplete(_ *websocket.Conn, _ jsonRPCMessage, _ map[string]any) forceCompleteResponseMode {
	select {
	case f.calls <- struct{}{}:
	default:
	}
	return forceCompleteRespondOK
}

func (f *countingForceCompleteFixture) url() string {
	return "ws" + strings.TrimPrefix(f.server.URL, "http")
}

func (f *countingForceCompleteFixture) close() {
	f.server.Close()
}

func (f *countingForceCompleteFixture) assertNoForceComplete(t *testing.T) {
	t.Helper()
	select {
	case <-f.calls:
		t.Fatal("stale ProviderID sent remote turn/forceComplete")
	case <-time.After(100 * time.Millisecond):
	}
}

func serveForceCompleteWebSocket(w http.ResponseWriter, r *http.Request, handler forceCompleteHandler) {
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer conn.Close()
	for {
		msg, ok := readForceCompleteMessage(conn)
		if !ok {
			return
		}
		if len(msg.ID) == 0 {
			continue
		}
		mode := forceCompleteRespondOK
		if isForceCompleteMethod(msg) {
			mode = handler.handleForceComplete(conn, msg, extractForceCompleteParams(msg))
		}
		if mode == forceCompleteStopServing {
			return
		}
		if mode == forceCompleteResponseWritten {
			continue
		}
		if !writeForceCompleteResult(conn, msg) {
			return
		}
	}
}

func readForceCompleteMessage(conn *websocket.Conn) (jsonRPCMessage, bool) {
	_, rawBytes, err := conn.ReadMessage()
	if err != nil {
		return jsonRPCMessage{}, false
	}
	var msg jsonRPCMessage
	if err := json.Unmarshal(rawBytes, &msg); err != nil || len(msg.ID) == 0 {
		return jsonRPCMessage{}, true
	}
	return msg, true
}

func isForceCompleteMethod(msg jsonRPCMessage) bool {
	return strings.TrimSpace(msg.Method) == "turn/forceComplete"
}

func extractForceCompleteParams(msg jsonRPCMessage) map[string]any {
	var params map[string]any
	if len(msg.Params) > 0 {
		_ = json.Unmarshal(msg.Params, &params)
	}
	return params
}

func storeForceCompleteParams(ch chan<- map[string]any, params map[string]any) {
	select {
	case ch <- params:
	default:
	}
}

func writeForceCompleteResult(conn *websocket.Conn, msg jsonRPCMessage) bool {
	resp := mustJSON(map[string]any{
		"jsonrpc": "2.0",
		"id":      json.RawMessage(append([]byte(nil), msg.ID...)),
		"result":  map[string]any{"ok": true},
	})
	return conn.WriteMessage(websocket.TextMessage, resp) == nil
}

func writeForceCompleteError(conn *websocket.Conn, msg jsonRPCMessage, message string) bool {
	resp := mustJSON(map[string]any{
		"jsonrpc": "2.0",
		"id":      json.RawMessage(append([]byte(nil), msg.ID...)),
		"error": map[string]any{
			"code":    -32602,
			"message": message,
		},
	})
	return conn.WriteMessage(websocket.TextMessage, resp) == nil
}

func receiveForceCompleteParams(t *testing.T, ch <-chan map[string]any, timeoutMessage string) map[string]any {
	t.Helper()
	select {
	case params := <-ch:
		return params
	case <-time.After(time.Second):
		t.Fatal(timeoutMessage)
		return nil
	}
}

func newForceCompleteTestSession(t *testing.T, serverURL string) *session {
	t.Helper()
	s, err := newSession(context.Background(), pkglogger.Get(), serverURL, "agent-1", nil, testApprovalManager(), nil)
	if err != nil {
		t.Fatalf("newSession() error = %v", err)
	}
	return s
}

func configureForceCompleteTurns(s *session, oldProviderID, newProviderID, activeProviderID string) (*turnHandle, *turnHandle) {
	oldTurn := newTurnHandle("local-old", oldProviderID)
	newTurn := newTurnHandle("local-new", newProviderID)
	s.mu.Lock()
	s.turns[oldProviderID] = oldTurn
	s.turns[newProviderID] = newTurn
	s.activeTurnID = activeProviderID
	s.mu.Unlock()
	return oldTurn, newTurn
}

func configureSingleForceCompleteTurn(s *session, providerID string) *turnHandle {
	active := newTurnHandle("local-1", providerID)
	s.mu.Lock()
	s.turns[providerID] = active
	s.activeTurnID = providerID
	s.mu.Unlock()
	return active
}

func startForceComplete(t *testing.T, s *session) chan error {
	t.Helper()
	done := make(chan error, 1)
	goroutines := newTestGoroutineGroup(t)
	goroutines.Go(func() {
		done <- s.ForceComplete(context.Background(), dto.ForceCompleteRequest{ThreadID: "thread-1"})
	})
	return done
}

func setForceCompleteActiveTurn(s *session, providerID string) {
	s.mu.Lock()
	s.activeTurnID = providerID
	s.mu.Unlock()
}

func assertForceCompleteFinished(t *testing.T, done <-chan error) {
	t.Helper()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("ForceComplete() error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for ForceComplete")
	}
}

func assertTurnDone(t *testing.T, turn *turnHandle, message string) {
	t.Helper()
	select {
	case <-turn.Done():
	case <-time.After(time.Second):
		t.Fatal(message)
	}
}

func assertTurnOpen(t *testing.T, turn *turnHandle, message string) {
	t.Helper()
	select {
	case <-turn.Done():
		t.Fatal(message)
	default:
	}
}

func assertPinnedForceCompleteSessionState(t *testing.T, s *session) {
	t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.activeTurnID != "turn-new" {
		t.Fatalf("activeTurnID = %q, want turn-new", s.activeTurnID)
	}
	if _, ok := s.turns["turn-new"]; !ok {
		t.Fatal("turn-new was removed from turns")
	}
	if _, ok := s.turns["turn-old"]; ok {
		t.Fatal("turn-old remains in turns after force complete")
	}
}

func assertStaleProviderSessionState(t *testing.T, s *session) {
	t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.activeTurnID != "turn-new" {
		t.Fatalf("activeTurnID = %q, want turn-new", s.activeTurnID)
	}
	if _, ok := s.turns["turn-old"]; !ok {
		t.Fatal("turn-old was removed from turns")
	}
	if _, ok := s.turns["turn-new"]; !ok {
		t.Fatal("turn-new was removed from turns")
	}
}

func TestRequestToolApprovalDedupeWaitReturnsOnCallerContextCancel(t *testing.T) {
	s := &session{
		ctx:                  context.Background(),
		approvalSessionScope: "test-session-scope",
		processedApprovals:   map[string]*processedApprovalEntry{},
	}
	s.setApprovalPolicy("on-request")
	payload := mustJSON(map[string]any{
		"requestId": int64(7),
		"callId":    "call-ctx",
		"command":   "echo hi",
	})
	req, requestID, ok := s.buildApprovalRequest("item/commandExecution/requestApproval", decodeEventPayload(payload))
	if !ok {
		t.Fatal("buildApprovalRequest() ok = false, want true")
	}
	key := processedApprovalRequestKey(req, requestID)
	s.processedApprovals[key] = &processedApprovalEntry{
		fingerprint: approvalRequestFingerprint(req, requestID),
		ready:       make(chan struct{}),
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := s.requestToolApprovalWithContext(ctx, "item/commandExecution/requestApproval", payload)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("requestToolApprovalWithContext() error = %v, want context.Canceled", err)
	}
}

func TestRequestToolApprovalDedupeWaitReturnsOnSessionContextCancel(t *testing.T) {
	sessionCtx, cancelSession := context.WithCancel(context.Background())
	cancelSession()
	s := &session{
		ctx:                  sessionCtx,
		approvalSessionScope: "test-session-scope",
		processedApprovals:   map[string]*processedApprovalEntry{},
	}
	s.setApprovalPolicy("on-request")
	payload := mustJSON(map[string]any{
		"requestId": int64(8),
		"callId":    "call-session",
		"command":   "echo hi",
	})
	req, requestID, ok := s.buildApprovalRequest("item/commandExecution/requestApproval", decodeEventPayload(payload))
	if !ok {
		t.Fatal("buildApprovalRequest() ok = false, want true")
	}
	key := processedApprovalRequestKey(req, requestID)
	s.processedApprovals[key] = &processedApprovalEntry{
		fingerprint: approvalRequestFingerprint(req, requestID),
		ready:       make(chan struct{}),
	}

	err := s.requestToolApprovalWithContext(context.Background(), "item/commandExecution/requestApproval", payload)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("requestToolApprovalWithContext() error = %v, want context.Canceled", err)
	}
}
