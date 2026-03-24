package codexapp

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	tooldto "github.com/anthropic-ai/super-agent-v3/internal/dto/tool"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/rpc"
	"github.com/kelindar/event"
	"golang.org/x/net/websocket"
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

func TestRequestToolApprovalDedupesProcessedRequestID(t *testing.T) {
	var mu sync.Mutex
	approvalRespondCalls := 0
	server := httptest.NewServer(websocket.Handler(func(conn *websocket.Conn) {
		defer conn.Close()
		for {
			var raw string
			if err := websocket.Message.Receive(conn, &raw); err != nil {
				return
			}
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
			if err := websocket.Message.Send(conn, string(resp)); err != nil {
				return
			}
		}
	}))
	defer server.Close()

	bus := event.NewDispatcher()
	s, err := newSession(slog.Default(), "ws"+strings.TrimPrefix(server.URL, "http"), "agent-1", nil, rpc.NewApprovalManager(nil, bus))
	if err != nil {
		t.Fatalf("newSession() error = %v", err)
	}
	defer closeCodexTestSession(t, s)

	requested := make(chan tooldto.ToolApprovalRequested, 2)
	cancelSub := event.Subscribe(bus, func(ev tooldto.ToolApprovalRequested) {
		requested <- ev
	})
	defer cancelSub()

	payload := mustJSON(map[string]any{
		"requestId": int64(1),
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
	decision := s.processedApprovals[1]
	s.approvalMu.Unlock()
	if decision.Approved == nil || *decision.Approved {
		t.Fatalf("cached decision = %#v, want decline", decision)
	}

	mu.Lock()
	defer mu.Unlock()
	if approvalRespondCalls != 2 {
		t.Fatalf("approval/respond calls = %d, want 2", approvalRespondCalls)
	}
}
