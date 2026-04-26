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

	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
	"github.com/gorilla/websocket"
)

func TestForceCompletePinsActiveTurnBeforeRemoteCall(t *testing.T) {
	forceCompleteStarted := make(chan struct{})
	releaseForceComplete := make(chan struct{})
	var once sync.Once
	forceCompleteParams := make(chan map[string]any, 1)

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
			if strings.TrimSpace(msg.Method) == "turn/forceComplete" {
				var params map[string]any
				if len(msg.Params) > 0 {
					_ = json.Unmarshal(msg.Params, &params)
				}
				select {
				case forceCompleteParams <- params:
				default:
				}
				once.Do(func() { close(forceCompleteStarted) })
				<-releaseForceComplete
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

	s, err := newSession(context.Background(), pkglogger.Get(), "ws"+strings.TrimPrefix(server.URL, "http"), "agent-1", nil, nil, nil)
	if err != nil {
		t.Fatalf("newSession() error = %v", err)
	}
	s.runtime.Start()
	defer closeCodexTestSession(t, s)

	oldTurn := newTurnHandle("local-old", "turn-old")
	newTurn := newTurnHandle("local-new", "turn-new")
	s.mu.Lock()
	s.turns["turn-old"] = oldTurn
	s.turns["turn-new"] = newTurn
	s.activeTurnID = "turn-old"
	s.mu.Unlock()

	done := make(chan error, 1)
	go func() {
		done <- s.ForceComplete(context.Background(), dto.ForceCompleteRequest{ThreadID: "thread-1"})
	}()

	select {
	case <-forceCompleteStarted:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for turn/forceComplete call")
	}
	select {
	case params := <-forceCompleteParams:
		if params["turnId"] != "turn-old" {
			t.Fatalf("remote forceComplete turnId = %#v, want turn-old", params["turnId"])
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for turn/forceComplete params")
	}

	s.mu.Lock()
	s.activeTurnID = "turn-new"
	s.mu.Unlock()
	close(releaseForceComplete)

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("ForceComplete() error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for ForceComplete")
	}

	select {
	case <-oldTurn.Done():
	case <-time.After(time.Second):
		t.Fatal("old active turn was not completed")
	}
	select {
	case <-newTurn.Done():
		t.Fatal("new active turn was completed; ForceComplete did not pin the original active turn")
	default:
	}

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

func TestForceCompleteIgnoresStaleProviderID(t *testing.T) {
	forceCompleteCalls := make(chan struct{}, 1)

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
			if strings.TrimSpace(msg.Method) == "turn/forceComplete" {
				select {
				case forceCompleteCalls <- struct{}{}:
				default:
				}
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

	s, err := newSession(context.Background(), pkglogger.Get(), "ws"+strings.TrimPrefix(server.URL, "http"), "agent-1", nil, nil, nil)
	if err != nil {
		t.Fatalf("newSession() error = %v", err)
	}
	defer closeCodexTestSession(t, s)

	oldTurn := newTurnHandle("local-old", "turn-old")
	newTurn := newTurnHandle("local-new", "turn-new")
	s.mu.Lock()
	s.turns["turn-old"] = oldTurn
	s.turns["turn-new"] = newTurn
	s.activeTurnID = "turn-new"
	s.mu.Unlock()

	if err := s.ForceComplete(context.Background(), dto.ForceCompleteRequest{ThreadID: "thread-1", ProviderID: "turn-old"}); err != nil {
		t.Fatalf("ForceComplete() error = %v", err)
	}

	select {
	case <-forceCompleteCalls:
		t.Fatal("stale ProviderID sent remote turn/forceComplete")
	case <-time.After(100 * time.Millisecond):
	}
	select {
	case <-oldTurn.Done():
		t.Fatal("stale ProviderID completed old turn")
	default:
	}
	select {
	case <-newTurn.Done():
		t.Fatal("stale ProviderID completed active turn")
	default:
	}

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
		ctx:                context.Background(),
		processedApprovals: map[string]*processedApprovalEntry{},
	}
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
	s.processedApprovals[key] = &processedApprovalEntry{ready: make(chan struct{})}
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
		ctx:                sessionCtx,
		processedApprovals: map[string]*processedApprovalEntry{},
	}
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
	s.processedApprovals[key] = &processedApprovalEntry{ready: make(chan struct{})}

	err := s.requestToolApprovalWithContext(context.Background(), "item/commandExecution/requestApproval", payload)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("requestToolApprovalWithContext() error = %v, want context.Canceled", err)
	}
}
