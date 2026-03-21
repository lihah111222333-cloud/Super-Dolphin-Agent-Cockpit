package codexapp

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
	"golang.org/x/net/websocket"
)

func TestTransportReconnectReinitializes(t *testing.T) {
	t.Parallel()

	var mu sync.Mutex
	methods := make([]string, 0, 4)
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
			if err := websocket.Message.Send(conn, string(resp)); err != nil {
				return
			}
		}
	}))
	defer server.Close()

	transport, err := newTransport("ws" + strings.TrimPrefix(server.URL, "http"))
	if err != nil {
		t.Fatalf("newTransport() error = %v", err)
	}
	defer func() { _ = transport.Kill() }()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
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
	if initializeCount != 1 {
		t.Fatalf("initialize count = %d, want 1 after reconnect; methods=%v", initializeCount, methods)
	}
}

func TestSessionAttemptRecoveryReplaysPendingTurn(t *testing.T) {
	var mu sync.Mutex
	turnStarts := 0
	initializeCalls := 0
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
			if err := websocket.Message.Send(conn, string(resp)); err != nil {
				return
			}
		}
	}))
	defer server.Close()

	s, err := newSession(slog.Default(), "ws"+strings.TrimPrefix(server.URL, "http"), "agent-1", nil, nil)
	if err != nil {
		t.Fatalf("newSession() error = %v", err)
	}
	defer closeCodexTestSession(t, s)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := initializeSession(ctx, s.transport); err != nil {
		t.Fatalf("initializeSession() error = %v", err)
	}

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
