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
	"time"

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
	got, ok := newDriver(nil, nil, nil, nil, manager, nil, nil, func(context.Context) ([]codexprotocol.DynamicToolSchema, error) {
		listToolsCalls++
		return []codexprotocol.DynamicToolSchema{{
			Name:        "tool.echo",
			Description: "echo payload",
			InputSchema: json.RawMessage(`{"type":"object"}`),
		}}, nil
	}).(*driver)
	if !ok {
		t.Fatalf("newDriver() type = %T, want *driver", newDriver(nil, nil, nil, nil, manager, nil, nil, func(context.Context) ([]codexprotocol.DynamicToolSchema, error) {
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

const testDynamicSkillExpandBodyToolName = "skill_expand_body"

func TestResumeSession_DynamicSkillToolsStillCallable(t *testing.T) {
	obs := runDynamicToolsResumeScenario(t)
	if obs.toolMethod != "item/tool/call" {
		t.Fatalf("tool method = %q, want item/tool/call", obs.toolMethod)
	}
	if obs.toolName != testDynamicSkillExpandBodyToolName {
		t.Fatalf("tool name = %q, want %s", obs.toolName, testDynamicSkillExpandBodyToolName)
	}
	if obs.toolAgentID != "agent-1" {
		t.Fatalf("tool agentId = %q, want agent-1", obs.toolAgentID)
	}
}

func TestThreadResume_AppServerRetainsStartDynamicTools(t *testing.T) {
	obs := runDynamicToolsResumeScenario(t)
	if !obs.startHadDynamicTools {
		t.Fatal("thread/start did not advertise dynamicTools")
	}
	if obs.resumeHadDynamicTools {
		t.Fatal("thread/resume unexpectedly sent dynamicTools; app-server retention should make this unnecessary")
	}
}

func TestThreadResume_DynamicToolsWireCompatibilityIsExplicit(t *testing.T) {
	raw := mustJSON(buildThreadResumeParams(dto.ResumeSessionRequest{ThreadID: "thread-1"}))
	var params map[string]any
	if err := json.Unmarshal(raw, &params); err != nil {
		t.Fatalf("json.Unmarshal(thread resume params) error = %v", err)
	}
	if _, ok := params["dynamicTools"]; ok {
		t.Fatalf("thread/resume params must not carry dynamicTools unless app-server wire support is explicit: %#v", params)
	}
}

type dynamicToolsResumeObservation struct {
	startHadDynamicTools  bool
	resumeHadDynamicTools bool
	toolMethod            string
	toolName              string
	toolAgentID           string
}

type dynamicToolsResumeState struct {
	mu                    sync.Mutex
	startHadDynamicTools  bool
	resumeHadDynamicTools bool
}

func (s *dynamicToolsResumeState) markStart(params map[string]any) {
	s.mu.Lock()
	defer s.mu.Unlock()
	tools, _ := params["dynamicTools"].([]any)
	for _, raw := range tools {
		tool, _ := raw.(map[string]any)
		if tool["name"] == testDynamicSkillExpandBodyToolName {
			s.startHadDynamicTools = true
			return
		}
	}
}

func (s *dynamicToolsResumeState) markResume(params map[string]any) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, s.resumeHadDynamicTools = params["dynamicTools"]
	return s.startHadDynamicTools
}

func (s *dynamicToolsResumeState) snapshot() (bool, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.startHadDynamicTools, s.resumeHadDynamicTools
}

func runDynamicToolsResumeScenario(t *testing.T) dynamicToolsResumeObservation {
	t.Helper()

	toolCalls := make(chan RawMessage, 1)
	toolResponses := make(chan jsonRPCMessage, 1)
	manager := &ServerManager{}
	manager.SetToolHandler(func(ctx context.Context, msg RawMessage) (any, error) {
		select {
		case toolCalls <- msg:
		default:
		}
		return map[string]any{"ok": true, "phase": "resume"}, nil
	})

	state := &dynamicToolsResumeState{}
	var writeMu sync.Mutex
	server := startDynamicToolsResumeServer(t, state, toolResponses, &writeMu)
	defer server.Close()

	d := &driver{
		serverURL: "ws" + strings.TrimPrefix(server.URL, "http"),
		manager:   manager,
		listTools: func(context.Context) ([]codexprotocol.DynamicToolSchema, error) {
			return []codexprotocol.DynamicToolSchema{dynamicSkillToolSchema()}, nil
		},
	}
	started, err := d.StartSession(context.Background(), dto.StartSessionRequest{AgentID: "agent-1"})
	if err != nil {
		t.Fatalf("StartSession() error = %v", err)
	}
	if s, ok := started.(*session); ok {
		closeCodexTestSession(t, s)
	}

	resumed, err := d.ResumeSession(context.Background(), dto.ResumeSessionRequest{
		Provider: "codex",
		AgentID:  "agent-1",
		ThreadID: "provider-thread-1",
	})
	if err != nil {
		t.Fatalf("ResumeSession() error = %v", err)
	}
	s, ok := resumed.(*session)
	if !ok {
		t.Fatalf("ResumeSession() type = %T, want *session", resumed)
	}
	defer closeCodexTestSession(t, s)

	var msg RawMessage
	select {
	case msg = <-toolCalls:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for dynamic skill tool call after resume")
	}
	assertToolResponse(t, toolResponses, "99")

	var params map[string]any
	if err := json.Unmarshal(msg.Params, &params); err != nil {
		t.Fatalf("json.Unmarshal(tool params) error = %v", err)
	}
	startHadDynamicTools, resumeHadDynamicTools := state.snapshot()
	return dynamicToolsResumeObservation{
		startHadDynamicTools:  startHadDynamicTools,
		resumeHadDynamicTools: resumeHadDynamicTools,
		toolMethod:            msg.Method,
		toolName:              stringValue(params, "name"),
		toolAgentID:           stringValue(params, "agentId"),
	}
}

func dynamicSkillToolSchema() codexprotocol.DynamicToolSchema {
	return codexprotocol.DynamicToolSchema{
		Name:        testDynamicSkillExpandBodyToolName,
		Description: "expand skill body",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"name":{"type":"string"}},"required":["name"]}`),
	}
}

func startDynamicToolsResumeServer(t *testing.T, state *dynamicToolsResumeState, toolResponses chan<- jsonRPCMessage, writeMu *sync.Mutex) *httptest.Server {
	t.Helper()
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
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
			if err := json.Unmarshal(rawBytes, &msg); err != nil {
				continue
			}
			if strings.TrimSpace(msg.Method) == "" {
				toolResponses <- msg
				continue
			}
			if len(msg.ID) == 0 {
				continue
			}
			var params map[string]any
			if len(msg.Params) > 0 {
				_ = json.Unmarshal(msg.Params, &params)
			}
			result := map[string]any{"ok": true}
			sendToolCall := false
			switch msg.Method {
			case "initialize":
			case "thread/start":
				state.markStart(params)
				result = map[string]any{"thread": map[string]any{"id": "provider-thread-1"}}
			case "thread/resume":
				sendToolCall = state.markResume(params)
				result = map[string]any{"thread": map[string]any{"id": "provider-thread-1"}}
			}
			resp := mustJSON(map[string]any{"jsonrpc": "2.0", "id": json.RawMessage(append([]byte(nil), msg.ID...)), "result": mustJSON(result)})
			writeMu.Lock()
			if err := conn.WriteMessage(websocket.TextMessage, resp); err != nil {
				writeMu.Unlock()
				return
			}
			writeMu.Unlock()
			if sendToolCall {
				sendCodexToolCall(t, writeMu, conn, 99, testDynamicSkillExpandBodyToolName, map[string]any{"name": "demo"})
			}
		}
	}))
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
