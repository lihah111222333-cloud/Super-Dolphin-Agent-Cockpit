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

func TestRequestToolApprovalDedupesProcessedRequestID(t *testing.T) {
	var mu sync.Mutex
	approvalRespondCalls := 0
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
				mu.Lock()
				approvalRespondCalls++
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
	if err := s.requestToolApproval("item/commandExecution/requestApproval", payload); err != nil {
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
	entry := s.processedApprovals[processedApprovalKey("call-1", 1)]
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
}
