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
	// reader loop 由 SessionRuntime 持有；重连测试通过关闭 socket 并取消当前 reader ctx 模拟断线。
	// 等待 runtime 的 done channel 收口后再重连，避免旧 reader 和新连接并发消费同一会话。
	s.transport.closed.Store(true)
	defer s.transport.closed.Store(false)
	s.transport.closeSocket()
	if s.runtime != nil {
		s.runtime.cancelReader()
	}
	stopCtx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if s.runtime != nil {
		if err := s.runtime.waitReader(stopCtx); err != nil {
			t.Fatalf("runtime.waitReader() error = %v", err)
		}
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
	setCancelInflightHandler(manager, started, canceled)
	server := startCancelInflightToolBridgeServer(t)
	defer server.Close()

	s := newStartedRecoverySession(t, server.URL, manager)
	defer closeIfOpenCodexSession(t, s)

	waitForInflightToolCall(t, started)
	if err := s.Close(context.Background()); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	assertInflightCanceled(t, canceled)
	// Close 会 drain runtime reader；这里不再额外等待 reader，避免重复验证同一关闭边界。
}

func setCancelInflightHandler(manager *ServerManager, started chan<- struct{}, canceled chan<- error) {
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
}

func startCancelInflightToolBridgeServer(t *testing.T) *httptest.Server {
	t.Helper()
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	var writeMu sync.Mutex
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
}

func newStartedRecoverySession(t *testing.T, serverURL string, manager *ServerManager) *session {
	t.Helper()
	s, err := newSession(context.Background(), pkglogger.Get(), "ws"+strings.TrimPrefix(serverURL, "http"), "agent-1", nil, nil, manager)
	if err != nil {
		t.Fatalf("newSession() error = %v", err)
	}
	// 生产路径由 driver.StartSession/ResumeSession 启动 runtime；直接 newSession 的测试必须手动补齐。
	s.setThreadID("thread-1")
	s.setRuntimeConfigValue("cwd", t.TempDir())
	s.runtime.Start()
	return s
}

func closeIfOpenCodexSession(t *testing.T, s *session) {
	t.Helper()
	if s.ctx.Err() == nil {
		closeCodexTestSession(t, s)
	}
}

func waitForInflightToolCall(t *testing.T, started <-chan struct{}) {
	t.Helper()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for in-flight tool call")
	}
}

func assertInflightCanceled(t *testing.T, canceled <-chan error) {
	t.Helper()
	select {
	case got := <-canceled:
		if !errors.Is(got, context.Canceled) {
			t.Fatalf("tool ctx err = %v, want context canceled", got)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for tool ctx cancellation")
	}
}

func TestToolBridge_Recovery_ResumeToolCall(t *testing.T) {
	toolCalls := make(chan RawMessage, 1)
	responses := make(chan jsonRPCMessage, 1)
	connections := make(chan *websocket.Conn, 2)
	manager := &ServerManager{}
	setRecordingToolHandler(manager, toolCalls, "recovered")

	var mu sync.Mutex
	var writeMu sync.Mutex
	initializeCount, threadResumes := 0, 0
	server := startRecoveryToolBridgeServer(t, connections, responses, &mu, &writeMu, &initializeCount, &threadResumes)
	defer server.Close()

	s := newStartedRecoverySession(t, server.URL, manager)
	defer closeCodexTestSession(t, s)
	second := reconnectRecoverySession(t, s, connections)
	sendCodexToolCall(t, &writeMu, second, 88, "grep", map[string]any{"action": "text_search", "query": "needle"})
	assertRecoveredToolCall(t, toolCalls)
	assertToolResponse(t, responses, "88")
	assertRecoveryCounters(t, &mu, &initializeCount, &threadResumes)
}

func TestRecoveryResume_DynamicSkillToolsStillCallable(t *testing.T) {
	toolCalls := make(chan RawMessage, 1)
	responses := make(chan jsonRPCMessage, 1)
	connections := make(chan *websocket.Conn, 2)
	manager := &ServerManager{}
	setRecordingToolHandler(manager, toolCalls, "recovered-skill")

	var mu sync.Mutex
	var writeMu sync.Mutex
	initializeCount, threadResumes := 0, 0
	server := startRecoveryToolBridgeServer(t, connections, responses, &mu, &writeMu, &initializeCount, &threadResumes)
	defer server.Close()

	s := newStartedRecoverySession(t, server.URL, manager)
	defer closeCodexTestSession(t, s)
	second := reconnectRecoverySession(t, s, connections)
	sendCodexToolCall(t, &writeMu, second, 89, testDynamicToolName, map[string]any{"payload": "demo"})
	assertRecoveredDynamicSkillToolCall(t, toolCalls)
	assertToolResponse(t, responses, "89")
	assertRecoveryCounters(t, &mu, &initializeCount, &threadResumes)
}

func setRecordingToolHandler(manager *ServerManager, toolCalls chan<- RawMessage, phase string) {
	manager.SetToolHandler(func(ctx context.Context, msg RawMessage) (any, error) {
		select {
		case toolCalls <- msg:
		default:
		}
		return map[string]any{"ok": true, "phase": phase}, nil
	})
}

func reconnectRecoverySession(t *testing.T, s *session, connections <-chan *websocket.Conn) *websocket.Conn {
	t.Helper()
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
	// reader 归 runtime 管理；重连建立新 socket 后必须通过 runtime.restartReader() 重新接收消息。
	if s.runtime != nil && !s.runtime.restartReader() {
		t.Fatal("runtime.restartReader() returned false")
	}
	if err := s.resumeThreadAfterRecovery(s.ctx); err != nil {
		t.Fatalf("resumeThreadAfterRecovery() error = %v", err)
	}
	return second
}

func assertRecoveredToolCall(t *testing.T, toolCalls <-chan RawMessage) {
	t.Helper()
	select {
	case msg := <-toolCalls:
		if msg.Method != "item/tool/call" {
			t.Fatalf("tool method = %q, want item/tool/call", msg.Method)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for resumed tool call")
	}
}

func assertRecoveredDynamicSkillToolCall(t *testing.T, toolCalls <-chan RawMessage) {
	t.Helper()
	select {
	case msg := <-toolCalls:
		assertDynamicSkillToolCall(t, msg)
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for resumed dynamic skill tool call")
	}
}

func assertDynamicSkillToolCall(t *testing.T, msg RawMessage) {
	t.Helper()
	if msg.Method != "item/tool/call" {
		t.Fatalf("tool method = %q, want item/tool/call", msg.Method)
	}
	var params map[string]any
	if err := json.Unmarshal(msg.Params, &params); err != nil {
		t.Fatalf("json.Unmarshal(tool params) error = %v", err)
	}
	if stringValue(params, "name") != testDynamicToolName {
		t.Fatalf("tool name = %#v, want %s", params["name"], testDynamicToolName)
	}
}

func assertRecoveryCounters(t *testing.T, mu *sync.Mutex, initializeCount, threadResumes *int) {
	t.Helper()
	mu.Lock()
	defer mu.Unlock()
	if *initializeCount != 2 {
		t.Fatalf("initialize count = %d, want 2", *initializeCount)
	}
	if *threadResumes != 1 {
		t.Fatalf("thread/resume count = %d, want 1", *threadResumes)
	}
}
