package codexapp

import (
	"context"
	"encoding/json"
	"maps"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
	codexprotocol "github.com/anthropic-ai/super-agent-v3/internal/provider/codexapp/protocol"
	"github.com/gorilla/websocket"
)

func TestToolBridge_Initialize_ExperimentalAPI(t *testing.T) {
	t.Parallel()

	params := initializeParams()
	caps, ok := params["capabilities"].(map[string]any)
	if !ok {
		t.Fatalf("capabilities = %#v, want map[string]any", params["capabilities"])
	}
	enabled, ok := caps["experimentalApi"].(bool)
	if !ok || !enabled {
		t.Fatalf("experimentalApi = %#v, want true", caps["experimentalApi"])
	}
}

func TestToolBridge_StartSession_UsesDynamicTools(t *testing.T) {
	recorder := &toolBridgeRPCRecorder{}
	t.Setenv("CODEX_APP_SERVER_URL", startToolBridgeRPCServer(t, recorder))
	manager := &ServerManager{}

	listToolsCalls := 0
	got, ok := newDriver(nil, nil, nil, nil, manager, func(context.Context) ([]codexprotocol.DynamicToolSchema, error) {
		listToolsCalls++
		return []codexprotocol.DynamicToolSchema{{
			Name:        "tool.echo",
			Description: "echo payload",
			InputSchema: json.RawMessage(`{"type":"object"}`),
		}}, nil
	}).(*driver)
	if !ok {
		t.Fatalf("newDriver() type = %T, want *driver", newDriver(nil, nil, nil, nil, manager, func(context.Context) ([]codexprotocol.DynamicToolSchema, error) {
			return nil, nil
		}))
	}
	if got.listTools == nil {
		t.Fatal("listTools = nil, want configured")
	}
	if manager.getToolHandler() != nil {
		t.Fatal("toolHandler = non-nil, want nil")
	}

	sessionAny, err := got.StartSession(context.Background(), dto.StartSessionRequest{AgentID: "agent-1"})
	if err != nil {
		t.Fatalf("StartSession() error = %v", err)
	}
	s, ok := sessionAny.(*session)
	if !ok {
		t.Fatalf("StartSession() type = %T, want *session", sessionAny)
	}
	defer closeCodexTestSession(t, s)

	if listToolsCalls != 1 {
		t.Fatalf("listTools calls = %d, want 1", listToolsCalls)
	}
	if calls := recorder.calls("thread/start"); calls != 1 {
		t.Fatalf("thread/start calls = %d, want 1", calls)
	}
	params := recorder.threadStartParamsSnapshot()
	if len(params) == 0 {
		t.Fatal("thread/start params not recorded")
	}
	tools, ok := params["dynamicTools"].([]any)
	if !ok || len(tools) != 1 {
		t.Fatalf("dynamicTools = %#v, want one tool", params["dynamicTools"])
	}
	if s.ThreadID() != "provider-thread-1" {
		t.Fatalf("ThreadID() = %q, want provider-thread-1", s.ThreadID())
	}
}

type toolBridgeRPCRecorder struct {
	mu                sync.Mutex
	callCount         map[string]int
	initializeParams  map[string]any
	threadStartParams map[string]any
}

func (r *toolBridgeRPCRecorder) record(method string, raw json.RawMessage) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.callCount == nil {
		r.callCount = make(map[string]int)
	}
	r.callCount[method]++
	if len(raw) == 0 {
		return
	}
	var params map[string]any
	if err := json.Unmarshal(raw, &params); err != nil {
		return
	}
	switch method {
	case "initialize":
		r.initializeParams = cloneAnyMap(params)
	case "thread/start":
		r.threadStartParams = cloneAnyMap(params)
	}
}

func (r *toolBridgeRPCRecorder) calls(method string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.callCount[method]
}

func (r *toolBridgeRPCRecorder) threadStartParamsSnapshot() map[string]any {
	r.mu.Lock()
	defer r.mu.Unlock()
	return cloneAnyMap(r.threadStartParams)
}

func startToolBridgeRPCServer(t *testing.T, recorder *toolBridgeRPCRecorder) string {
	t.Helper()

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
			recorder.record(msg.Method, msg.Params)
			if len(msg.ID) == 0 {
				continue
			}
			result := map[string]any{"ok": true}
			if msg.Method == "thread/start" {
				result = map[string]any{"thread": map[string]any{"id": "provider-thread-1"}}
			}
			resp, err := json.Marshal(map[string]any{
				"jsonrpc": "2.0",
				"id":      json.RawMessage(append([]byte(nil), msg.ID...)),
				"result":  mustJSON(result),
			})
			if err != nil {
				t.Fatalf("marshal response: %v", err)
			}
			if err := conn.WriteMessage(websocket.TextMessage, resp); err != nil {
				return
			}
		}
	}))
	t.Cleanup(server.Close)
	return "ws" + strings.TrimPrefix(server.URL, "http")
}

func cloneAnyMap(src map[string]any) map[string]any {
	if len(src) == 0 {
		return nil
	}
	return maps.Clone(src)
}
