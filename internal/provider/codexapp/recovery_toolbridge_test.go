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

	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
	"github.com/gorilla/websocket"
)

func waitCodexTestConn(t *testing.T, ch <-chan *websocket.Conn) *websocket.Conn {
	t.Helper()
	select {
	case conn := <-ch:
		return conn
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for websocket connection")
		return nil
	}
}

func sendCodexToolCall(t *testing.T, writeMu *sync.Mutex, conn *websocket.Conn, id int, name string, arguments map[string]any) {
	t.Helper()
	raw := mustJSON(map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"method":  "item/tool/call",
		"params": map[string]any{
			"name":      name,
			"arguments": arguments,
		},
	})
	writeMu.Lock()
	defer writeMu.Unlock()
	if err := conn.WriteMessage(websocket.TextMessage, raw); err != nil {
		t.Fatalf("WriteMessage(tool call) error = %v", err)
	}
}

func startRecoveryToolBridgeServer(t *testing.T, connections chan<- *websocket.Conn, responses chan<- jsonRPCMessage, mu *sync.Mutex, writeMu *sync.Mutex, initializeCount, threadResumes *int) *httptest.Server {
	t.Helper()
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		connections <- conn
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
			if strings.TrimSpace(msg.Method) == "" {
				responses <- msg
				continue
			}
			if len(msg.ID) == 0 {
				continue
			}
			var result json.RawMessage
			mu.Lock()
			switch msg.Method {
			case "initialize":
				*initializeCount++
				result = mustJSON(map[string]any{"ok": true})
			case "thread/resume":
				*threadResumes++
				result = mustJSON(map[string]any{"thread": map[string]any{"id": "thread-1"}})
			default:
				result = mustJSON(map[string]any{"ok": true})
			}
			mu.Unlock()
			resp := mustJSON(map[string]any{"jsonrpc": "2.0", "id": json.RawMessage(append([]byte(nil), msg.ID...)), "result": json.RawMessage(append([]byte(nil), result...))})
			writeMu.Lock()
			if err := conn.WriteMessage(websocket.TextMessage, resp); err != nil {
				writeMu.Unlock()
				return
			}
			writeMu.Unlock()
		}
	}))
}

func stopReadLoopForReconnect(t *testing.T, s *session) {
	t.Helper()
	s.transport.closed.Store(true)
	defer s.transport.closed.Store(false)
	s.transport.closeSocket()
	s.stopReadLoop()
	stopCtx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := s.waitReadLoopStopped(stopCtx); err != nil {
		t.Fatalf("waitReadLoopStopped() error = %v", err)
	}
}

func assertToolResponse(t *testing.T, responses <-chan jsonRPCMessage, wantID string) {
	t.Helper()
	select {
	case resp := <-responses:
		if string(resp.ID) != wantID {
			t.Fatalf("response id = %s, want %s", resp.ID, wantID)
		}
		var result map[string]any
		if err := json.Unmarshal(resp.Result, &result); err != nil {
			t.Fatalf("json.Unmarshal(response) error = %v", err)
		}
		if ok, _ := result["ok"].(bool); !ok {
			t.Fatalf("response result = %#v, want ok=true", result)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for tool response after recovery")
	}
}

func TestToolBridge_Recovery_CancelInflight(t *testing.T) {
	started := make(chan struct{}, 1)
	canceled := make(chan error, 1)
	manager := &ServerManager{}
	manager.SetToolHandler(func(ctx context.Context, msg RawMessage) (any, error) {
		if msg.Method != "item/tool/call" {
			return nil, fmt.Errorf("unexpected method: %s", msg.Method)
		}
		select {
		case started <- struct{}{}:
		default:
		}
		<-ctx.Done()
		select {
		case canceled <- ctx.Err():
		default:
		}
		return nil, ctx.Err()
	})

	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	var writeMu sync.Mutex
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
			resp := mustJSON(map[string]any{"jsonrpc": "2.0", "id": json.RawMessage(append([]byte(nil), msg.ID...)), "result": map[string]any{"ok": true}})
			writeMu.Lock()
			if err := conn.WriteMessage(websocket.TextMessage, resp); err != nil {
				writeMu.Unlock()
				return
			}
			writeMu.Unlock()
			if msg.Method == "initialize" {
				sendCodexToolCall(t, &writeMu, conn, 77, "lsp_ping", map[string]any{"value": 77})
			}
		}
	}))
	defer server.Close()

	s, err := newSession(context.Background(), pkglogger.Get(), "ws"+strings.TrimPrefix(server.URL, "http"), "agent-1", nil, nil, manager)
	if err != nil {
		t.Fatalf("newSession() error = %v", err)
	}
	defer func() {
		if s.ctx.Err() == nil {
			closeCodexTestSession(t, s)
		}
	}()

	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for in-flight tool call")
	}
	if err := s.Close(context.Background()); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	select {
	case got := <-canceled:
		if !errors.Is(got, context.Canceled) {
			t.Fatalf("tool ctx err = %v, want context canceled", got)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for tool ctx cancellation")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := s.waitReadLoopStopped(ctx); err != nil {
		t.Fatalf("waitReadLoopStopped() error = %v", err)
	}
}

func TestToolBridge_Recovery_ResumeToolCall(t *testing.T) {
	toolCalls := make(chan RawMessage, 1)
	responses := make(chan jsonRPCMessage, 1)
	connections := make(chan *websocket.Conn, 2)
	manager := &ServerManager{}
	manager.SetToolHandler(func(ctx context.Context, msg RawMessage) (any, error) {
		select {
		case toolCalls <- msg:
		default:
		}
		return map[string]any{"ok": true, "phase": "recovered"}, nil
	})

	var mu sync.Mutex
	var writeMu sync.Mutex
	initializeCount, threadResumes := 0, 0
	server := startRecoveryToolBridgeServer(t, connections, responses, &mu, &writeMu, &initializeCount, &threadResumes)
	defer server.Close()

	s, err := newSession(context.Background(), pkglogger.Get(), "ws"+strings.TrimPrefix(server.URL, "http"), "agent-1", nil, nil, manager)
	if err != nil {
		t.Fatalf("newSession() error = %v", err)
	}
	defer closeCodexTestSession(t, s)
	first := waitCodexTestConn(t, connections)
	s.setThreadID("thread-1")
	stopReadLoopForReconnect(t, s)
	reconnectCtx, reconnectCancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer reconnectCancel()
	if err := s.recovery.Reconnect(reconnectCtx); err != nil {
		t.Fatalf("Reconnect() error = %v", err)
	}
	second := waitCodexTestConn(t, connections)
	if first == second {
		t.Fatal("reconnect did not establish a new websocket connection")
	}
	s.startReadLoop()
	if err := s.resumeThreadAfterRecovery(s.ctx); err != nil {
		t.Fatalf("resumeThreadAfterRecovery() error = %v", err)
	}
	sendCodexToolCall(t, &writeMu, second, 88, "code_run", map[string]any{"command": "pwd"})
	select {
	case msg := <-toolCalls:
		if msg.Method != "item/tool/call" {
			t.Fatalf("tool method = %q, want item/tool/call", msg.Method)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for resumed tool call")
	}
	assertToolResponse(t, responses, "88")
	mu.Lock()
	defer mu.Unlock()
	if initializeCount != 2 {
		t.Fatalf("initialize count = %d, want 2", initializeCount)
	}
	if threadResumes != 1 {
		t.Fatalf("thread/resume count = %d, want 1", threadResumes)
	}
}
