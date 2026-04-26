package codexapp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	contract "github.com/anthropic-ai/super-agent-v3/internal/contract"
	tooldto "github.com/anthropic-ai/super-agent-v3/internal/dto/tool"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/rpc"
	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
	"github.com/gorilla/websocket"
	"github.com/kelindar/event"
)

func TestRequestApprovalDecisionAutoDeclinesWithoutFrontend(t *testing.T) {
	s := &session{
		approvals: rpc.NewApprovalManager(nil, nil),
		ctx:       context.Background(),
	}

	decision, err := s.requestApprovalDecision(rpc.ApprovalRequest{CallID: "call-1"})
	if err != nil {
		t.Fatalf("requestApprovalDecision() error = %v", err)
	}
	if decision.Approved == nil || *decision.Approved {
		t.Fatalf("requestApprovalDecision() approved = %v, want false", decision.Approved)
	}
	if decision.Reason != "decline" {
		t.Fatalf("requestApprovalDecision() reason = %q, want %q", decision.Reason, "decline")
	}
}

func TestRequestApprovalDecisionAutoRespondsRequestUserInputWhenPolicyNever(t *testing.T) {
	s := &session{
		agentID:    "agent-1",
		approvals:  rpc.NewApprovalManager(nil, nil),
		ctx:        context.Background(),
		suppressed: map[string]struct{}{},
		turns:      map[string]*turnHandle{},
	}
	s.setApprovalPolicy("never")

	req, _, ok := s.buildApprovalRequest("request_user_input", map[string]any{"requestId": int64(1), "message": "continue"})
	if !ok {
		t.Fatal("buildApprovalRequest() ok = false, want true")
	}
	if req.ApprovalPolicy != "never" {
		t.Fatalf("buildApprovalRequest() approval policy = %q, want %q", req.ApprovalPolicy, "never")
	}

	decision, err := s.requestApprovalDecision(req)
	if err != nil {
		t.Fatalf("requestApprovalDecision() error = %v", err)
	}
	if decision.Approved == nil || !*decision.Approved {
		t.Fatalf("requestApprovalDecision() approved = %v, want true", decision.Approved)
	}
	if decision.Reason != "auto_approved" {
		t.Fatalf("requestApprovalDecision() reason = %q, want %q", decision.Reason, "auto_approved")
	}
}

func TestOnNotificationApprovalRequestPublishesRequestedOnce(t *testing.T) {
	bus := event.NewDispatcher()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	s := &session{
		agentID:    "agent-1",
		approvals:  rpc.NewApprovalManager(nil, bus),
		ctx:        ctx,
		suppressed: map[string]struct{}{},
		turns:      map[string]*turnHandle{},
	}

	requested := make(chan tooldto.ToolApprovalRequested, 4)
	cancelSub := event.Subscribe(bus, func(ev tooldto.ToolApprovalRequested) {
		requested <- ev
	})
	defer cancelSub()

	s.onNotification("item/commandExecution/requestApproval", []byte(`{"requestId":1,"command":"echo hi","toolName":"shell","turnId":"turn-1"}`))

	var first tooldto.ToolApprovalRequested
	select {
	case first = <-requested:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for ToolApprovalRequested")
	}
	if first.RequestID != 1 {
		t.Fatalf("first requestID = %d, want 1", first.RequestID)
	}

	select {
	case extra := <-requested:
		t.Fatalf("received duplicate ToolApprovalRequested event: %+v", extra)
	case <-time.After(100 * time.Millisecond):
	}

	cancel()
}

func TestAlienThreadEventThreadReportsIncomingThreadID(t *testing.T) {
	s := &session{}
	s.threadID.Store("own-thread")

	eventThread, ok := s.alienThreadEventThread([]byte(`{"threadId":"other-thread"}`))
	if !ok {
		t.Fatal("alienThreadEventThread() ok = false, want true")
	}
	if eventThread != "other-thread" {
		t.Fatalf("alienThreadEventThread() eventThread = %q, want other-thread", eventThread)
	}
}

func TestAlienThreadEventThreadIgnoresOwnOrMissingThreadID(t *testing.T) {
	s := &session{}
	s.threadID.Store("own-thread")

	for _, params := range []string{
		`{"threadId":"own-thread"}`,
		`{"turnId":"turn-1"}`,
		`{"threadId":" "}`,
		`{`,
	} {
		if eventThread, ok := s.alienThreadEventThread([]byte(params)); ok {
			t.Fatalf("alienThreadEventThread(%s) = (%q, true), want false", params, eventThread)
		}
	}
}

func TestBeginProcessedApprovalDedupesByCallIDAndRequestID(t *testing.T) {
	s := &session{processedApprovals: map[string]*processedApprovalEntry{}}

	key := processedApprovalKey("call-1", 1)
	first, firstOwner := s.beginProcessedApproval(key)
	second, secondOwner := s.beginProcessedApproval(key)

	if !firstOwner || secondOwner {
		t.Fatalf("owners = %v, %v; want true, false", firstOwner, secondOwner)
	}
	if first == nil || first != second {
		t.Fatalf("entries = %p, %p; want same non-nil entry", first, second)
	}
}

func TestBeginProcessedApprovalClearsCacheAtCapacity(t *testing.T) {
	s := &session{processedApprovals: map[string]*processedApprovalEntry{}}

	for i := 0; i < processedApprovalLimit; i++ {
		key := processedApprovalKey("call-"+strconv.Itoa(i), int64(i+1))
		entry, owner := s.beginProcessedApproval(key)
		if entry == nil || !owner {
			t.Fatalf("beginProcessedApproval(%q) = (%#v, %v), want new owner entry", key, entry, owner)
		}
		approved := false
		s.finishProcessedApproval(key, entry, rpcDecision(approved, "decline"), nil)
	}

	lastKey := processedApprovalKey("call-overflow", int64(processedApprovalLimit+1))
	lastEntry, owner := s.beginProcessedApproval(lastKey)
	if lastEntry == nil || !owner {
		t.Fatalf("beginProcessedApproval(%q) = (%#v, %v), want new owner entry", lastKey, lastEntry, owner)
	}

	s.approvalMu.Lock()
	defer s.approvalMu.Unlock()
	if got := len(s.processedApprovals); got != 1 {
		t.Fatalf("processed approvals = %d, want 1 after capacity reset", got)
	}
	if s.processedApprovals[lastKey] != lastEntry {
		t.Fatalf("processed approvals[%q] = %#v, want %#v", lastKey, s.processedApprovals[lastKey], lastEntry)
	}
}

func TestBeginProcessedApprovalCapacityKeepsPendingEntries(t *testing.T) {
	s := &session{processedApprovals: map[string]*processedApprovalEntry{}}

	pendingKey := processedApprovalKey("call-pending", 1)
	pendingEntry, owner := s.beginProcessedApproval(pendingKey)
	if pendingEntry == nil || !owner {
		t.Fatalf("beginProcessedApproval(%q) = (%#v, %v), want pending owner entry", pendingKey, pendingEntry, owner)
	}

	for i := 1; i < processedApprovalLimit; i++ {
		key := processedApprovalKey("call-"+strconv.Itoa(i), int64(i+1))
		entry, owner := s.beginProcessedApproval(key)
		if entry == nil || !owner {
			t.Fatalf("beginProcessedApproval(%q) = (%#v, %v), want new owner entry", key, entry, owner)
		}
		approved := false
		s.finishProcessedApproval(key, entry, rpcDecision(approved, "decline"), nil)
	}

	lastKey := processedApprovalKey("call-overflow-pending", int64(processedApprovalLimit+1))
	lastEntry, owner := s.beginProcessedApproval(lastKey)
	if lastEntry == nil || !owner {
		t.Fatalf("beginProcessedApproval(%q) = (%#v, %v), want new owner entry", lastKey, lastEntry, owner)
	}

	s.approvalMu.Lock()
	defer s.approvalMu.Unlock()
	if got := len(s.processedApprovals); got != 2 {
		t.Fatalf("processed approvals = %d, want 2 after purging completed entries", got)
	}
	if s.processedApprovals[pendingKey] != pendingEntry {
		t.Fatalf("pending entry for %q was removed", pendingKey)
	}
	if s.processedApprovals[lastKey] != lastEntry {
		t.Fatalf("processed approvals[%q] = %#v, want %#v", lastKey, s.processedApprovals[lastKey], lastEntry)
	}
}

func rpcDecision(approved bool, reason string) contract.ApprovalDecision {
	return contract.ApprovalDecision{Approved: &approved, Reason: reason}
}

type blockingApprovalRequester struct {
	mu       sync.Mutex
	once     sync.Once
	calls    int
	requests []rpc.ApprovalRequest
	started  chan struct{}
	release  chan struct{}
	decision contract.ApprovalDecision
}

func (b *blockingApprovalRequester) RequestApproval(ctx context.Context, req rpc.ApprovalRequest) (contract.ApprovalDecision, error) {
	b.mu.Lock()
	b.calls++
	b.requests = append(b.requests, req)
	b.mu.Unlock()
	b.once.Do(func() { close(b.started) })
	select {
	case <-ctx.Done():
		return contract.ApprovalDecision{}, ctx.Err()
	case <-b.release:
		return b.decision, nil
	}
}

func (b *blockingApprovalRequester) callCount() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.calls
}

func TestProcessedApprovalRequestKeyPreservesNestedCallID(t *testing.T) {
	base := rpc.ApprovalRequest{
		SourceMethod: "item/commandExecution/requestApproval",
		CallID:       "volatile-top-level-a",
		Payload: map[string]any{
			"requestId": int64(5),
			"callId":    "volatile-top-level-a",
			"toolName":  "custom",
			"arguments": map[string]any{"callId": "business-a"},
		},
	}
	replayed := base
	replayed.CallID = "volatile-top-level-b"
	replayed.Payload = map[string]any{
		"requestId": int64(5),
		"callId":    "volatile-top-level-b",
		"toolName":  "custom",
		"arguments": map[string]any{"callId": "business-a"},
	}
	if got, want := processedApprovalRequestKey(base, 5), processedApprovalRequestKey(replayed, 5); got != want {
		t.Fatalf("top-level volatile callId changed key: got %q want %q", got, want)
	}

	differentNested := base
	differentNested.Payload = map[string]any{
		"requestId": int64(5),
		"callId":    "volatile-top-level-a",
		"toolName":  "custom",
		"arguments": map[string]any{"callId": "business-b"},
	}
	if got, wantDifferentFrom := processedApprovalRequestKey(differentNested, 5), processedApprovalRequestKey(base, 5); got == wantDifferentFrom {
		t.Fatalf("nested business callId was ignored in key: both %q", got)
	}
}

func TestRequestToolApprovalDedupesProcessedRequestID(t *testing.T) {
	var mu sync.Mutex
	approvalRespondCalls := 0
	approvalRespondParams := make([]map[string]any, 0, 2)
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
			raw := string(rawBytes)
			var msg jsonRPCMessage
			if err := json.Unmarshal([]byte(raw), &msg); err != nil {
				continue
			}
			if strings.TrimSpace(msg.Method) == "approval/respond" {
				var params map[string]any
				if len(msg.Params) > 0 {
					_ = json.Unmarshal(msg.Params, &params)
				}
				mu.Lock()
				approvalRespondCalls++
				approvalRespondParams = append(approvalRespondParams, params)
				mu.Unlock()
			}
			if len(msg.ID) == 0 {
				continue
			}
			resp := mustJSON(map[string]any{
				"jsonrpc": "2.0",
				"id":      json.RawMessage(append([]byte(nil), msg.ID...)),
				"result":  map[string]any{"ok": true},
			})
			if err := conn.WriteMessage(websocket.TextMessage, resp); err != nil {
				return
			}
		}
	}))
	defer server.Close()

	bus := event.NewDispatcher()
	s, err := newSession(context.Background(), pkglogger.Get(), "ws"+strings.TrimPrefix(server.URL, "http"), "agent-1", nil, rpc.NewApprovalManager(nil, bus), nil)
	if err != nil {
		t.Fatalf("newSession() error = %v", err)
	}
	// P22 P1c: this test receives inbound notifications, so the reader must
	// be running. Mirror driver.StartSession's explicit runtime.Start().
	s.runtime.Start()
	defer closeCodexTestSession(t, s)

	requested := make(chan tooldto.ToolApprovalRequested, 2)
	cancelSub := event.Subscribe(bus, func(ev tooldto.ToolApprovalRequested) {
		requested <- ev
	})
	defer cancelSub()

	payload := mustJSON(map[string]any{
		"requestId": int64(1),
		"callId":    "call-1",
		"command":   "echo hi",
		"toolName":  "shell",
		"turnId":    "turn-1",
	})
	if err := s.requestToolApproval("item/commandExecution/requestApproval", payload); err != nil {
		t.Fatalf("first requestToolApproval() error = %v", err)
	}
	duplicateWithChangedCallID := mustJSON(map[string]any{
		"requestId": int64(1),
		"callId":    "call-1-replayed",
		"command":   "echo hi",
		"toolName":  "shell",
		"turnId":    "turn-1",
	})
	if err := s.requestToolApproval("item/commandExecution/requestApproval", duplicateWithChangedCallID); err != nil {
		t.Fatalf("second requestToolApproval() error = %v", err)
	}

	select {
	case ev := <-requested:
		if ev.RequestID != 1 {
			t.Fatalf("first requestID = %d, want 1", ev.RequestID)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for ToolApprovalRequested")
	}
	select {
	case extra := <-requested:
		t.Fatalf("received duplicate ToolApprovalRequested event: %+v", extra)
	case <-time.After(100 * time.Millisecond):
	}

	s.approvalMu.Lock()
	if got := len(s.processedApprovals); got != 1 {
		s.approvalMu.Unlock()
		t.Fatalf("processed approvals = %d, want 1", got)
	}
	req, requestID, ok := s.buildApprovalRequest("item/commandExecution/requestApproval", decodeEventPayload(payload))
	if !ok {
		s.approvalMu.Unlock()
		t.Fatal("buildApprovalRequest() ok = false, want true")
	}
	entry := s.processedApprovals[processedApprovalRequestKey(req, requestID)]
	s.approvalMu.Unlock()
	if entry == nil || !entry.done {
		t.Fatalf("processed approval entry = %#v, want completed entry", entry)
	}
	decision := entry.decision
	if decision.Approved == nil || *decision.Approved {
		t.Fatalf("cached decision = %#v, want decline", decision)
	}

	mu.Lock()
	defer mu.Unlock()
	if approvalRespondCalls != 2 {
		t.Fatalf("approval/respond calls = %d, want 2", approvalRespondCalls)
	}
	if len(approvalRespondParams) != 2 {
		t.Fatalf("approval/respond params captured = %d, want 2", len(approvalRespondParams))
	}
	for idx, params := range approvalRespondParams {
		if got := params["requestId"]; got != float64(1) {
			t.Fatalf("approval/respond[%d] requestId = %#v, want 1", idx, got)
		}
		if got := params["approved"]; got != false {
			t.Fatalf("approval/respond[%d] approved = %#v, want false", idx, got)
		}
		for _, internalKey := range []string{"uiType", "uiText", "uiCommand", "uiFiles", "uiExitCode", "internal", "worker"} {
			if _, ok := params[internalKey]; ok {
				t.Fatalf("approval/respond[%d] leaked internal key %q in params %#v", idx, internalKey, params)
			}
		}
	}
}

func TestRequestToolApprovalDoesNotReuseDecisionWhenRequestIDIsReusedForDifferentPayload(t *testing.T) {
	var mu sync.Mutex
	approvalRespondParams := make([]map[string]any, 0, 2)
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
			if err := json.Unmarshal(rawBytes, &msg); err != nil {
				continue
			}
			if strings.TrimSpace(msg.Method) == "approval/respond" {
				var params map[string]any
				if len(msg.Params) > 0 {
					_ = json.Unmarshal(msg.Params, &params)
				}
				mu.Lock()
				approvalRespondParams = append(approvalRespondParams, params)
				mu.Unlock()
			}
			if len(msg.ID) == 0 {
				continue
			}
			resp := mustJSON(map[string]any{
				"jsonrpc": "2.0",
				"id":      json.RawMessage(append([]byte(nil), msg.ID...)),
				"result":  map[string]any{"ok": true},
			})
			if err := conn.WriteMessage(websocket.TextMessage, resp); err != nil {
				return
			}
		}
	}))
	defer server.Close()

	decisions := []contract.ApprovalDecision{rpcDecision(false, "safe declined"), rpcDecision(true, "danger reviewed")}
	var hookCalls int
	s, err := newSession(context.Background(), pkglogger.Get(), "ws"+strings.TrimPrefix(server.URL, "http"), "agent-1", nil, nil, nil)
	if err != nil {
		t.Fatalf("newSession() error = %v", err)
	}
	s.approvalDecisionHook = func(context.Context, rpc.ApprovalRequest) (contract.ApprovalDecision, error) {
		if hookCalls >= len(decisions) {
			t.Fatalf("approval hook called too many times: %d", hookCalls+1)
		}
		decision := decisions[hookCalls]
		hookCalls++
		return decision, nil
	}
	s.runtime.Start()
	defer closeCodexTestSession(t, s)

	first := mustJSON(map[string]any{
		"requestId": int64(77),
		"callId":    "call-safe",
		"command":   "cat README.md",
		"toolName":  "shell",
		"turnId":    "turn-1",
	})
	second := mustJSON(map[string]any{
		"requestId": int64(77),
		"callId":    "call-danger",
		"command":   "rm -rf /tmp/example",
		"toolName":  "shell",
		"turnId":    "turn-1",
	})
	if err := s.requestToolApproval("item/commandExecution/requestApproval", first); err != nil {
		t.Fatalf("first requestToolApproval() error = %v", err)
	}
	if err := s.requestToolApproval("item/commandExecution/requestApproval", second); err != nil {
		t.Fatalf("second requestToolApproval() error = %v", err)
	}
	if hookCalls != 2 {
		t.Fatalf("approval hook calls = %d, want 2 for reused requestId with different payload", hookCalls)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(approvalRespondParams) != 2 {
		t.Fatalf("approval/respond calls = %d, want 2", len(approvalRespondParams))
	}
	if got := approvalRespondParams[0]["approved"]; got != false {
		t.Fatalf("first approved = %#v, want false", got)
	}
	if got := approvalRespondParams[1]["approved"]; got != true {
		t.Fatalf("second approved = %#v, want true", got)
	}
}

func TestRequestToolApprovalDedupesInFlightRequestID(t *testing.T) {
	var mu sync.Mutex
	approvalRespondParams := make([]map[string]any, 0, 2)
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
			if err := json.Unmarshal(rawBytes, &msg); err != nil {
				continue
			}
			if strings.TrimSpace(msg.Method) == "approval/respond" {
				var params map[string]any
				if len(msg.Params) > 0 {
					_ = json.Unmarshal(msg.Params, &params)
				}
				mu.Lock()
				approvalRespondParams = append(approvalRespondParams, params)
				mu.Unlock()
			}
			if len(msg.ID) == 0 {
				continue
			}
			resp := mustJSON(map[string]any{
				"jsonrpc": "2.0",
				"id":      json.RawMessage(append([]byte(nil), msg.ID...)),
				"result":  map[string]any{"ok": true},
			})
			if err := conn.WriteMessage(websocket.TextMessage, resp); err != nil {
				return
			}
		}
	}))
	defer server.Close()

	requester := &blockingApprovalRequester{
		started:  make(chan struct{}),
		release:  make(chan struct{}),
		decision: rpcDecision(false, "decline"),
	}
	s, err := newSession(context.Background(), pkglogger.Get(), "ws"+strings.TrimPrefix(server.URL, "http"), "agent-1", nil, nil, nil)
	if err != nil {
		t.Fatalf("newSession() error = %v", err)
	}
	s.approvalDecisionHook = requester.RequestApproval
	s.runtime.Start()
	defer closeCodexTestSession(t, s)

	payload := mustJSON(map[string]any{
		"requestId": int64(42),
		"callId":    "call-inflight",
		"command":   "echo hi",
		"toolName":  "shell",
		"turnId":    "turn-1",
	})
	ownerDone := make(chan error, 1)
	go func() {
		ownerDone <- s.requestToolApprovalWithContext(context.Background(), "item/commandExecution/requestApproval", payload)
	}()

	select {
	case <-requester.started:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for owner approval request")
	}

	waiterDone := make(chan error, 1)
	go func() {
		waiterDone <- s.requestToolApprovalWithContext(context.Background(), "item/commandExecution/requestApproval", payload)
	}()
	select {
	case err := <-waiterDone:
		t.Fatalf("waiter finished before owner released: %v", err)
	case <-time.After(100 * time.Millisecond):
	}

	close(requester.release)
	for name, done := range map[string]<-chan error{"owner": ownerDone, "waiter": waiterDone} {
		select {
		case err := <-done:
			if err != nil {
				t.Fatalf("%s requestToolApprovalWithContext() error = %v", name, err)
			}
		case <-time.After(time.Second):
			t.Fatalf("timed out waiting for %s requestToolApprovalWithContext", name)
		}
	}

	if got := requester.callCount(); got != 1 {
		t.Fatalf("RequestApproval calls = %d, want 1", got)
	}
	s.approvalMu.Lock()
	req, requestID, ok := s.buildApprovalRequest("item/commandExecution/requestApproval", decodeEventPayload(payload))
	if !ok {
		s.approvalMu.Unlock()
		t.Fatal("buildApprovalRequest() ok = false, want true")
	}
	entry := s.processedApprovals[processedApprovalRequestKey(req, requestID)]
	processedLen := len(s.processedApprovals)
	s.approvalMu.Unlock()
	if processedLen != 1 || entry == nil || !entry.done {
		t.Fatalf("processed approvals len=%d entry=%#v, want one completed entry", processedLen, entry)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(approvalRespondParams) != 2 {
		t.Fatalf("approval/respond calls = %d, want 2", len(approvalRespondParams))
	}
	for idx, params := range approvalRespondParams {
		if got := params["requestId"]; got != float64(42) {
			t.Fatalf("approval/respond[%d] requestId = %#v, want 42", idx, got)
		}
		if _, ok := params["request_id"]; ok {
			t.Fatalf("approval/respond[%d] leaked snake_case request_id in params %#v", idx, params)
		}
	}
}

func TestSanitizeProviderPayloadApprovalRespondCanonicalizesRequestID(t *testing.T) {
	got, ok := sanitizeProviderPayload("approval/respond", map[string]any{
		"request_id": int64(11),
		"approved":   true,
	}).(map[string]any)
	if !ok {
		t.Fatalf("sanitizeProviderPayload() type = %T, want map[string]any", got)
	}
	if got["requestId"] != int64(11) {
		t.Fatalf("requestId = %#v, want 11", got["requestId"])
	}
	if _, ok := got["request_id"]; ok {
		t.Fatalf("request_id leaked in approval/respond payload: %#v", got)
	}

	got, ok = sanitizeProviderPayload("approval/respond", map[string]any{
		"request_id": int64(11),
		"requestId":  int64(12),
	}).(map[string]any)
	if !ok {
		t.Fatalf("sanitizeProviderPayload() type = %T, want map[string]any", got)
	}
	if got["requestId"] != int64(12) {
		t.Fatalf("requestId = %#v, want explicit camelCase 12", got["requestId"])
	}
	if _, ok := got["request_id"]; ok {
		t.Fatalf("request_id leaked when camelCase was present: %#v", got)
	}

	got, ok = sanitizeProviderPayload("turn/start", map[string]any{
		"request_id": int64(11),
		"requestId":  int64(12),
		"threadId":   "thread-1",
	}).(map[string]any)
	if !ok {
		t.Fatalf("sanitizeProviderPayload() type = %T, want map[string]any", got)
	}
	if _, ok := got["requestId"]; ok {
		t.Fatalf("requestId leaked for non-approval method: %#v", got)
	}
	if _, ok := got["request_id"]; ok {
		t.Fatalf("request_id leaked for non-approval method: %#v", got)
	}
	if got["threadId"] != "thread-1" {
		t.Fatalf("threadId = %#v, want thread-1", got["threadId"])
	}
}
