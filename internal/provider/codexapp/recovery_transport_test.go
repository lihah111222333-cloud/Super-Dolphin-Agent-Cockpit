package codexapp

import (
	"context"
	"encoding/json"
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

func TestTransportReconnectReinitializes(t *testing.T) {
	t.Parallel()

	var mu sync.Mutex
	methods := make([]string, 0, 4)
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
			if strings.TrimSpace(msg.Method) == "" {
				continue
			}
			mu.Lock()
			methods = append(methods, msg.Method)
			mu.Unlock()
			if len(msg.ID) == 0 {
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
			if err := conn.WriteMessage(websocket.TextMessage, resp); err != nil {
				return
			}
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

	if err := transport.reconnect(ctx); err != nil {
		t.Fatalf("reconnect() error = %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	initializeCount := 0
	for _, method := range methods {
		if method == "initialize" {
			initializeCount++
		}
	}
	if initializeCount != 2 {
		t.Fatalf("initialize count = %d, want 2 after startup+reconnect; methods=%v", initializeCount, methods)
	}
}

func TestTransportReconnectDoesNotDispatchConnectionDeadForSupersededReader(t *testing.T) {
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
			if err := conn.WriteMessage(websocket.TextMessage, resp); err != nil {
				return
			}
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

	dead := make(chan RawMessage, 1)
	done := make(chan struct{})
	go func() {
		defer close(done)
		transport.ReadLoop(ctx, func(_ context.Context, _ Responder, msg RawMessage) {
			if strings.TrimSpace(msg.Method) == "connection.dead" {
				dead <- msg
			}
		})
	}()

	deadline := time.Now().Add(time.Second)
	for !transport.looping.Load() && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if !transport.looping.Load() {
		t.Fatal("read loop did not start")
	}

	if err := transport.reconnect(ctx); err != nil {
		t.Fatalf("reconnect() error = %v", err)
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("read loop did not exit after reconnect closed old socket")
	}
	select {
	case msg := <-dead:
		t.Fatalf("unexpected connection.dead from superseded reader: %+v", msg)
	default:
	}
}

func TestTransportPassiveDisconnectDispatchesConnectionDead(t *testing.T) {
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

	dead := make(chan RawMessage, 1)
	transport.ReadLoop(ctx, func(_ context.Context, _ Responder, msg RawMessage) {
		if strings.TrimSpace(msg.Method) == "connection.dead" {
			dead <- msg
		}
	})
	select {
	case <-dead:
	case <-time.After(time.Second):
		t.Fatal("passive disconnect did not dispatch connection.dead")
	}
}

func TestSessionAttemptRecoveryReplaysPendingTurn(t *testing.T) {
	var mu sync.Mutex
	turnStarts := 0
	initializeCalls := 0
	threadResumes := 0
	upgrader2 := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader2.Upgrade(w, r, nil)
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
			var result json.RawMessage
			switch msg.Method {
			case "initialize":
				mu.Lock()
				initializeCalls++
				mu.Unlock()
				result = mustJSON(map[string]any{"ok": true})
			case "thread/resume":
				mu.Lock()
				threadResumes++
				mu.Unlock()
				result = mustJSON(map[string]any{"thread": map[string]any{"id": "thread-1"}})
			case "turn/start":
				mu.Lock()
				turnStarts++
				current := turnStarts
				mu.Unlock()
				result = mustJSON(map[string]any{"turn": map[string]any{"id": fmt.Sprintf("turn-%d", current)}})
			default:
				result = mustJSON(map[string]any{"ok": true})
			}
			if len(msg.ID) == 0 {
				continue
			}
			resp, err := json.Marshal(map[string]any{
				"jsonrpc": "2.0",
				"id":      json.RawMessage(append([]byte(nil), msg.ID...)),
				"result":  json.RawMessage(append([]byte(nil), result...)),
			})
			if err != nil {
				t.Fatalf("marshal response: %v", err)
			}
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
	// P22 P1c: mirror driver.StartSession's explicit runtime.Start().
	s.runtime.Start()
	defer closeCodexTestSession(t, s)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

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

	s.setThreadID("thread-1")
	beforeReadAt := time.Now().Add(-time.Second).UnixNano()
	s.lastReadAt.Store(beforeReadAt)
	s.mu.Lock()
	s.suppressed["stale-turn"] = struct{}{}
	s.mu.Unlock()

	if err := s.attemptRecovery("test recovery"); err != nil {
		t.Fatalf("attemptRecovery() error = %v", err)
	}

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
	s.mu.Lock()
	suppressedLen := len(s.suppressed)
	s.mu.Unlock()
	if suppressedLen != 0 {
		t.Fatalf("suppressed size = %d, want 0 after successful recovery", suppressedLen)
	}

	mu.Lock()
	defer mu.Unlock()
	if turnStarts != 2 {
		t.Fatalf("turn/start count = %d, want 2", turnStarts)
	}
	if initializeCalls != 2 {
		t.Fatalf("initialize count = %d, want 2", initializeCalls)
	}
	if threadResumes != 1 {
		t.Fatalf("thread/resume count = %d, want 1", threadResumes)
	}
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
