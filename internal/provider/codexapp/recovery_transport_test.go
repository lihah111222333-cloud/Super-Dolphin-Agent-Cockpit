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

func TestSessionAttemptRecoveryReplaysPendingTurn(t *testing.T) {
	var mu sync.Mutex
	turnStarts := 0
	initializeCalls := 0
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

	s, err := newSession(context.Background(), pkglogger.Get(), "ws"+strings.TrimPrefix(server.URL, "http"), "agent-1", nil, nil)
	if err != nil {
		t.Fatalf("newSession() error = %v", err)
	}
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

	if err := s.attemptRecovery("test recovery"); err != nil {
		t.Fatalf("attemptRecovery() error = %v", err)
	}

	if got := handle.ProviderID(); got != "turn-2" {
		t.Fatalf("ProviderID() after replay = %q, want turn-2", got)
	}
	if got := s.activeTurnID; got != "turn-2" {
		t.Fatalf("activeTurnID = %q, want turn-2", got)
	}

	mu.Lock()
	defer mu.Unlock()
	if turnStarts != 2 {
		t.Fatalf("turn/start count = %d, want 2", turnStarts)
	}
	if initializeCalls != 2 {
		t.Fatalf("initialize count = %d, want 2", initializeCalls)
	}
}
