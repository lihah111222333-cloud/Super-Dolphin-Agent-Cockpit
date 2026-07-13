package codexapp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/kelindar/event"
	contract "github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
	tooldto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/tool"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/rpc"
	pkglogger "github.com/lihah111222333-cloud/super-dolphin-agent/pkg/logger"
)

type approvalRespondRecorder struct {
	mu     sync.Mutex
	params []map[string]any
}

func (r *approvalRespondRecorder) add(params map[string]any) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.params = append(r.params, params)
}

func (r *approvalRespondRecorder) snapshot() []map[string]any {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]map[string]any(nil), r.params...)
}

func startApprovalRespondRecorderServer(t *testing.T, recorder *approvalRespondRecorder) *httptest.Server {
	t.Helper()
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		serveApprovalRespondRecorder(conn, recorder)
	}))
}

func serveApprovalRespondRecorder(conn *websocket.Conn, recorder *approvalRespondRecorder) {
	for {
		_, rawBytes, err := conn.ReadMessage()
		if err != nil {
			return
		}
		msg, ok := decodeApprovalRecorderMessage(rawBytes)
		if !ok {
			continue
		}
		if !handleApprovalRecorderMessage(conn, recorder, msg) {
			return
		}
	}
}

func decodeApprovalRecorderMessage(rawBytes []byte) (jsonRPCMessage, bool) {
	var msg jsonRPCMessage
	if err := json.Unmarshal(rawBytes, &msg); err != nil {
		return jsonRPCMessage{}, false
	}
	return msg, true
}

func handleApprovalRecorderMessage(conn *websocket.Conn, recorder *approvalRespondRecorder, msg jsonRPCMessage) bool {
	if strings.TrimSpace(msg.Method) == "approval/respond" {
		recorder.add(decodeApprovalRespondParams(msg.Params))
	}
	if len(msg.ID) == 0 {
		return true
	}
	resp := mustJSON(map[string]any{
		"jsonrpc": "2.0",
		"id":      json.RawMessage(append([]byte(nil), msg.ID...)),
		"result":  map[string]any{"ok": true},
	})
	return conn.WriteMessage(websocket.TextMessage, resp) == nil
}

func decodeApprovalRespondParams(raw json.RawMessage) map[string]any {
	var params map[string]any
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &params)
	}
	return params
}

func newApprovalRecorderSession(t *testing.T, serverURL string, approvals *rpc.ApprovalManager) *session {
	t.Helper()
	s, err := newSession(context.Background(), pkglogger.Get(), "ws"+strings.TrimPrefix(serverURL, "http"), "agent-1", nil, approvals, nil)
	if err != nil {
		t.Fatalf("newSession() error = %v", err)
	}
	return s
}

func commandApprovalPayload(requestID int64, callID, command string) []byte {
	return mustJSON(map[string]any{
		"requestId": requestID,
		"callId":    callID,
		"command":   command,
		"toolName":  "shell",
		"turnId":    "turn-1",
	})
}

func TestApprovalPayloadMalformedReturnsErrorDecision(t *testing.T) {
	recorder := &approvalRespondRecorder{}
	server := startApprovalRespondRecorderServer(t, recorder)
	defer server.Close()

	s := newApprovalRecorderSession(t, server.URL, testApprovalManager())
	hookCalled := false
	s.approvalDecisionHook = func(context.Context, rpc.ApprovalRequest) (contract.ApprovalDecision, error) {
		hookCalled = true
		return rpcDecision(true, "unexpected"), nil
	}
	s.runtime.Start()
	defer closeCodexTestSession(t, s)

	payload := mustJSON(map[string]any{
		"requestId": int64(91),
		"callId":    map[string]any{"bad": "shape"},
		"toolName":  "shell",
		"turnId":    "turn-1",
	})
	if err := s.requestToolApproval("item/commandExecution/requestApproval", payload); err != nil {
		t.Fatalf("requestToolApproval() error = %v", err)
	}
	if hookCalled {
		t.Fatal("approval hook was called for malformed provider payload")
	}

	params := waitForApprovalRespondParams(t, recorder, 1)
	if got := params[0]["requestId"]; got != float64(91) {
		t.Fatalf("approval/respond requestId = %#v, want 91", got)
	}
	if got := params[0]["approved"]; got != false {
		t.Fatalf("approval/respond approved = %#v, want false", got)
	}
	decision, ok := params[0]["decision"].(string)
	if !ok || !strings.Contains(decision, "approval_parse_failed") {
		t.Fatalf("approval/respond decision = %#v, want approval_parse_failed string", params[0]["decision"])
	}
}

// TestRequestToolApprovalRejectsStringRequestIDWithoutTruncating 锁定审批 id 的权限边界：
// provider 传来的字符串 requestId 不能被截断或解析成合法审批请求。
func TestRequestToolApprovalRejectsStringRequestIDWithoutTruncating(t *testing.T) {
	recorder := &approvalRespondRecorder{}
	server := startApprovalRespondRecorderServer(t, recorder)
	defer server.Close()

	s := newApprovalRecorderSession(t, server.URL, testApprovalManager())
	hookCalled := false
	s.approvalDecisionHook = func(context.Context, rpc.ApprovalRequest) (contract.ApprovalDecision, error) {
		hookCalled = true
		return rpcDecision(true, "unexpected"), nil
	}
	s.runtime.Start()
	defer closeCodexTestSession(t, s)

	payload := []byte(`{"requestId":"91","callId":"call-91","toolName":"shell","turnId":"turn-1"}`)
	err := s.requestToolApproval("item/commandExecution/requestApproval", payload)
	if err == nil || !strings.Contains(err.Error(), "approval request identity is required") {
		t.Fatalf("requestToolApproval() error = %v, want approval request identity failure", err)
	}
	if hookCalled {
		t.Fatal("approval hook was called for string requestId")
	}
	if got := recorder.snapshot(); len(got) != 0 {
		t.Fatalf("approval/respond calls = %#v, want none for string requestId", got)
	}
}

func TestRequestToolApprovalDedupesProcessedRequestID(t *testing.T) {
	recorder := &approvalRespondRecorder{}
	server := startApprovalRespondRecorderServer(t, recorder)
	defer server.Close()

	bus := event.NewDispatcher()
	s := newApprovalRecorderSession(t, server.URL, rpc.NewApprovalManager(nil, bus))
	s.runtime.Start()
	defer closeCodexTestSession(t, s)

	requested := make(chan tooldto.ToolApprovalRequested, 2)
	cancelSub := event.Subscribe(bus, func(ev tooldto.ToolApprovalRequested) { requested <- ev })
	defer cancelSub()

	payload := commandApprovalPayload(1, "call-1", "echo hi")
	duplicate := commandApprovalPayload(1, "call-1-replayed", "echo hi")
	if err := s.requestToolApproval("item/commandExecution/requestApproval", payload); err != nil {
		t.Fatalf("first requestToolApproval() error = %v", err)
	}
	if err := s.requestToolApproval("item/commandExecution/requestApproval", duplicate); err != nil {
		t.Fatalf("second requestToolApproval() error = %v", err)
	}

	assertRequestedOnce(t, requested, 1)
	assertProcessedApprovalCachedDecline(t, s, payload)
	assertApprovalRespondDeclinesSanitized(t, waitForApprovalRespondParams(t, recorder, 2))
}

func assertRequestedOnce(t *testing.T, requested <-chan tooldto.ToolApprovalRequested, requestID int64) {
	t.Helper()
	select {
	case ev := <-requested:
		if ev.RequestID != requestID {
			t.Fatalf("first requestID = %d, want %d", ev.RequestID, requestID)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for ToolApprovalRequested")
	}
	select {
	case extra := <-requested:
		t.Fatalf("received duplicate ToolApprovalRequested event: %+v", extra)
	case <-time.After(100 * time.Millisecond):
	}
}

func assertProcessedApprovalCachedDecline(t *testing.T, s *session, payload []byte) {
	t.Helper()
	s.approvalMu.Lock()
	defer s.approvalMu.Unlock()
	if got := len(s.processedApprovals); got != 1 {
		t.Fatalf("processed approvals = %d, want 1", got)
	}
	req, requestID, ok := s.buildApprovalRequest("item/commandExecution/requestApproval", decodeEventPayload(payload))
	if !ok {
		t.Fatal("buildApprovalRequest() ok = false, want true")
	}
	entry := s.processedApprovals[processedApprovalRequestKey(req, requestID)]
	if entry == nil || !entry.done {
		t.Fatalf("processed approval entry = %#v, want completed entry", entry)
	}
	if entry.decision.Approved == nil || *entry.decision.Approved {
		t.Fatalf("cached decision = %#v, want decline", entry.decision)
	}
}

func assertApprovalRespondDeclinesSanitized(t *testing.T, params []map[string]any) {
	t.Helper()
	if len(params) != 2 {
		t.Fatalf("approval/respond params captured = %d, want 2", len(params))
	}
	for idx, param := range params {
		if got := param["requestId"]; got != float64(1) {
			t.Fatalf("approval/respond[%d] requestId = %#v, want 1", idx, got)
		}
		if got := param["approved"]; got != false {
			t.Fatalf("approval/respond[%d] approved = %#v, want false", idx, got)
		}
		assertApprovalRespondHasNoInternalKeys(t, idx, param)
	}
}

func assertApprovalRespondHasNoInternalKeys(t *testing.T, idx int, params map[string]any) {
	t.Helper()
	for _, internalKey := range []string{"uiType", "uiText", "uiCommand", "uiFiles", "uiExitCode", "internal", "worker"} {
		if _, ok := params[internalKey]; ok {
			t.Fatalf("approval/respond[%d] leaked internal key %q in params %#v", idx, internalKey, params)
		}
	}
}

func TestRequestToolApprovalDoesNotReuseDecisionWhenRequestIDIsReusedForDifferentPayload(t *testing.T) {
	recorder := &approvalRespondRecorder{}
	server := startApprovalRespondRecorderServer(t, recorder)
	defer server.Close()

	s := newApprovalRecorderSession(t, server.URL, testApprovalManager())
	hookCalls := installSequentialApprovalHook(t, s, []contract.ApprovalDecision{
		rpcDecision(false, "safe declined"),
		rpcDecision(true, "danger reviewed"),
	})
	s.runtime.Start()
	defer closeCodexTestSession(t, s)

	first := commandApprovalPayload(77, "call-safe", "cat README.md")
	second := commandApprovalPayload(77, "call-danger", "rm -rf /tmp/example")
	if err := s.requestToolApproval("item/commandExecution/requestApproval", first); err != nil {
		t.Fatalf("first requestToolApproval() error = %v", err)
	}
	if err := s.requestToolApproval("item/commandExecution/requestApproval", second); err != nil {
		t.Fatalf("second requestToolApproval() error = %v", err)
	}
	if *hookCalls != 2 {
		t.Fatalf("approval hook calls = %d, want 2 for reused requestId with different payload", *hookCalls)
	}
	assertApprovalRespondApprovedValues(t, waitForApprovalRespondParams(t, recorder, 2), []bool{false, true})
}

func installSequentialApprovalHook(t *testing.T, s *session, decisions []contract.ApprovalDecision) *int {
	t.Helper()
	hookCalls := 0
	s.approvalDecisionHook = func(context.Context, rpc.ApprovalRequest) (contract.ApprovalDecision, error) {
		if hookCalls >= len(decisions) {
			t.Fatalf("approval hook called too many times: %d", hookCalls+1)
		}
		decision := decisions[hookCalls]
		hookCalls++
		return decision, nil
	}
	return &hookCalls
}

func assertApprovalRespondApprovedValues(t *testing.T, params []map[string]any, want []bool) {
	t.Helper()
	if len(params) != len(want) {
		t.Fatalf("approval/respond calls = %d, want %d", len(params), len(want))
	}
	for idx, wantApproved := range want {
		if got := params[idx]["approved"]; got != wantApproved {
			t.Fatalf("approval/respond[%d] approved = %#v, want %v", idx, got, wantApproved)
		}
	}
}

func TestRequestToolApprovalDedupesInFlightRequestID(t *testing.T) {
	recorder := &approvalRespondRecorder{}
	server := startApprovalRespondRecorderServer(t, recorder)
	defer server.Close()

	requester := &blockingApprovalRequester{
		started:  make(chan struct{}),
		release:  make(chan struct{}),
		decision: rpcDecision(false, "decline"),
	}
	s := newApprovalRecorderSession(t, server.URL, testApprovalManager())
	s.approvalDecisionHook = requester.RequestApproval
	s.runtime.Start()
	defer closeCodexTestSession(t, s)

	payload := commandApprovalPayload(42, "call-inflight", "echo hi")
	ownerDone := requestToolApprovalAsync(t, s, payload)
	waitForApprovalRequesterStarted(t, requester.started)
	waiterDone := requestToolApprovalAsync(t, s, payload)
	assertApprovalRequestStillWaiting(t, waiterDone)

	close(requester.release)
	waitApprovalRequestDone(t, "owner", ownerDone)
	waitApprovalRequestDone(t, "waiter", waiterDone)

	if got := requester.callCount(); got != 1 {
		t.Fatalf("RequestApproval calls = %d, want 1", got)
	}
	assertInFlightProcessedApprovalDone(t, s, payload)
	assertApprovalRespondRequestIDs(t, waitForApprovalRespondParams(t, recorder, 2), 42)
}

func requestToolApprovalAsync(t testing.TB, s *session, payload []byte) <-chan error {
	t.Helper()
	done := make(chan error, 1)
	goroutines := newTestGoroutineGroup(t)
	goroutines.Go(func() {
		done <- s.requestToolApprovalWithContext(context.Background(), "item/commandExecution/requestApproval", payload)
	})
	return done
}

func waitForApprovalRequesterStarted(t *testing.T, started <-chan struct{}) {
	t.Helper()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for owner approval request")
	}
}

func assertApprovalRequestStillWaiting(t *testing.T, done <-chan error) {
	t.Helper()
	select {
	case err := <-done:
		t.Fatalf("waiter finished before owner released: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
}

func waitApprovalRequestDone(t *testing.T, name string, done <-chan error) {
	t.Helper()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("%s requestToolApprovalWithContext() error = %v", name, err)
		}
	case <-time.After(time.Second):
		t.Fatalf("timed out waiting for %s requestToolApprovalWithContext", name)
	}
}

func assertInFlightProcessedApprovalDone(t *testing.T, s *session, payload []byte) {
	t.Helper()
	s.approvalMu.Lock()
	defer s.approvalMu.Unlock()
	req, requestID, ok := s.buildApprovalRequest("item/commandExecution/requestApproval", decodeEventPayload(payload))
	if !ok {
		t.Fatal("buildApprovalRequest() ok = false, want true")
	}
	entry := s.processedApprovals[processedApprovalRequestKey(req, requestID)]
	if len(s.processedApprovals) != 1 || entry == nil || !entry.done {
		t.Fatalf("processed approvals len=%d entry=%#v, want one completed entry", len(s.processedApprovals), entry)
	}
}

func assertApprovalRespondRequestIDs(t *testing.T, params []map[string]any, requestID int64) {
	t.Helper()
	if len(params) != 2 {
		t.Fatalf("approval/respond calls = %d, want 2", len(params))
	}
	for idx, param := range params {
		if got := param["requestId"]; got != float64(requestID) {
			t.Fatalf("approval/respond[%d] requestId = %#v, want %d", idx, got, requestID)
		}
		if _, ok := param["request_id"]; ok {
			t.Fatalf("approval/respond[%d] leaked snake_case request_id in params %#v", idx, param)
		}
	}
}

func waitForApprovalRespondParams(t *testing.T, recorder *approvalRespondRecorder, want int) []map[string]any {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		params := recorder.snapshot()
		if len(params) >= want {
			return params
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("approval/respond calls = %d, want %d", len(recorder.snapshot()), want)
	return nil
}
