package codexapp

// 这个文件只覆盖 Codex dynamic-tool transport 的 generic fixture；生产 skill
// 链路走 provider-native mirror，不通过 dynamic tool 发布。

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

	providerdto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
	turndto "github.com/anthropic-ai/super-agent-v3/internal/dto/turn"
	codexprotocol "github.com/anthropic-ai/super-agent-v3/internal/provider/codexapp/protocol"
	"github.com/anthropic-ai/super-agent-v3/internal/provider/unified"
	"github.com/gorilla/websocket"
	"github.com/kelindar/event"
)

const testGenericDynamicToolName = "test_dynamic_echo"

func TestDynamicTools_ModelE2E_ResultReturnsToModel(t *testing.T) {
	got := runDynamicSkillToolsModelE2E(t, dynamicSkillModelScenario{
		toolResult: dynamicSkillSuccessToolResult("skill body from approved cache"),
	})

	result := decodeDynamicSkillToolResponseResult(t, got.toolResponse)
	if success, _ := result["success"].(bool); !success {
		t.Fatalf("tool response success = %#v, want true; result=%#v", result["success"], result)
	}
	if text := dynamicSkillToolResponseText(t, result); !strings.Contains(text, "skill body from approved cache") {
		t.Fatalf("tool response text = %q, want skill body", text)
	}
	if got.toolParams["_agentId"] != "agent-1" || got.toolParams["_callId"] != "tool-call-1" || got.toolParams["turnId"] != "turn-1" {
		t.Fatalf("tool call params metadata = %#v", got.toolParams)
	}
}

func TestDynamicSkillTools_ModelE2E_ApprovalApprovedContinuesFinalAnswer(t *testing.T) {
	const final = "Final answer after reading skill body."
	got := runDynamicSkillToolsModelE2E(t, dynamicSkillModelScenario{
		toolResult: dynamicSkillSuccessToolResult("approved skill body"),
		finalDelta: final,
	})

	result := decodeDynamicSkillToolResponseResult(t, got.toolResponse)
	if text := dynamicSkillToolResponseText(t, result); !strings.Contains(text, "approved skill body") {
		t.Fatalf("tool response text = %q, want approved body", text)
	}
	if got.finalDelta.Delta != final {
		t.Fatalf("final delta = %q, want %q", got.finalDelta.Delta, final)
	}
	if got.finalDelta.Stream != "message" || got.finalDelta.ThreadID != "agent-1" || got.finalDelta.AgentID != "agent-1" || got.finalDelta.TurnID != "turn-1" {
		t.Fatalf("final delta header = %+v", got.finalDelta)
	}
}

func TestDynamicSkillTools_ModelE2E_ApprovalDeniedReturnsStructuredToolResult(t *testing.T) {
	got := runDynamicSkillToolsModelE2E(t, dynamicSkillModelScenario{
		toolResult: dynamicSkillDeniedToolResult(),
	})

	result := decodeDynamicSkillToolResponseResult(t, got.toolResponse)
	if success, _ := result["success"].(bool); success {
		t.Fatalf("tool response success = true, want false; result=%#v", result)
	}
	text := dynamicSkillToolResponseText(t, result)
	var envelope map[string]any
	if err := json.Unmarshal([]byte(text), &envelope); err != nil {
		t.Fatalf("decode structured denied text %q: %v", text, err)
	}
	if envelope["kind"] != "approval_denied" || envelope["tool"] != testGenericDynamicToolName {
		t.Fatalf("denied envelope = %#v", envelope)
	}
}

type dynamicSkillModelScenario struct {
	toolResult any
	finalDelta string
}

type dynamicSkillModelResult struct {
	toolParams   map[string]any
	toolResponse jsonRPCMessage
	finalDelta   turndto.TurnOutputDelta
}

type dynamicSkillModelRecorder struct {
	mu                sync.Mutex
	threadStartParams map[string]any
	toolResponses     chan jsonRPCMessage
}

func newDynamicSkillModelRecorder() *dynamicSkillModelRecorder {
	return &dynamicSkillModelRecorder{toolResponses: make(chan jsonRPCMessage, 1)}
}

func (r *dynamicSkillModelRecorder) recordThreadStart(raw json.RawMessage) {
	var params map[string]any
	if err := json.Unmarshal(raw, &params); err != nil {
		return
	}
	r.mu.Lock()
	r.threadStartParams = cloneAnyMap(params)
	r.mu.Unlock()
}

func (r *dynamicSkillModelRecorder) threadStartParamsSnapshot() map[string]any {
	r.mu.Lock()
	defer r.mu.Unlock()
	return cloneAnyMap(r.threadStartParams)
}

func (r *dynamicSkillModelRecorder) recordToolResponse(msg jsonRPCMessage) {
	select {
	case r.toolResponses <- msg:
	default:
	}
}

func runDynamicSkillToolsModelE2E(t *testing.T, scenario dynamicSkillModelScenario) dynamicSkillModelResult {
	t.Helper()
	bus := event.NewDispatcher()
	t.Cleanup(func() { _ = bus.Close() })
	dispatcher := unified.NewEventDispatcher(bus, nil)
	RegisterTranslators(dispatcher)

	finalDeltas := make(chan turndto.TurnOutputDelta, 1)
	cancelDelta := event.Subscribe(bus, func(ev turndto.TurnOutputDelta) {
		if ev.Stream == "message" {
			finalDeltas <- ev
		}
	})
	t.Cleanup(cancelDelta)

	toolCalls := make(chan RawMessage, 1)
	manager := &ServerManager{}
	manager.SetToolHandler(func(_ context.Context, msg RawMessage) (any, error) {
		select {
		case toolCalls <- msg:
		default:
		}
		if scenario.toolResult == nil {
			return nil, fmt.Errorf("test tool result not configured")
		}
		return scenario.toolResult, nil
	})

	recorder := newDynamicSkillModelRecorder()
	serverURL := startDynamicSkillModelServer(t, recorder, scenario.finalDelta)
	drv := newDriver(nil, dispatcher, nil, nil, manager, newSingleURLPoolForTest(t, serverURL), &recordingSkillMirrorReconciler{}, nil, func(context.Context) ([]codexprotocol.DynamicToolSchema, error) {
		return []codexprotocol.DynamicToolSchema{{
			Name:        testGenericDynamicToolName,
			Description: "Echo test payload through dynamic tool transport.",
			InputSchema: json.RawMessage(`{"type":"object"}`),
		}}, nil
	}).(*driver)

	sessionAny, err := drv.StartSession(context.Background(), providerdto.StartSessionRequest{
		AgentID:       "agent-1",
		CWD:           t.TempDir(),
		StartAssembly: validStartAssemblyForTest(),
	})
	if err != nil {
		t.Fatalf("StartSession() error = %v", err)
	}
	s := sessionAny.(*session)
	defer closeCodexTestSession(t, s)
	assertGenericDynamicToolFixtureAdvertised(t, recorder.threadStartParamsSnapshot())

	if _, err := s.StartTurn(context.Background(), providerdto.TurnRequest{
		ThreadID: s.ThreadID(),
		Inputs:   []providerdto.InputItem{{Type: "text", Content: "Use demo skill."}},
	}); err != nil {
		t.Fatalf("StartTurn() error = %v", err)
	}

	toolMsg := waitDynamicSkillRawToolCall(t, toolCalls)
	toolParams := decodeDynamicSkillToolParams(t, toolMsg.Params)
	toolResponse := waitDynamicSkillToolResponse(t, recorder.toolResponses)

	var final turndto.TurnOutputDelta
	if strings.TrimSpace(scenario.finalDelta) != "" {
		final = waitDynamicSkillFinalDelta(t, finalDeltas)
	}
	return dynamicSkillModelResult{toolParams: toolParams, toolResponse: toolResponse, finalDelta: final}
}

func startDynamicSkillModelServer(t *testing.T, recorder *dynamicSkillModelRecorder, finalDelta string) string {
	t.Helper()
	codexHome := t.TempDir()
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	var writeMu sync.Mutex
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handleDynamicSkillModelConnection(t, dynamicSkillModelHandlerConfig{
			upgrader:   upgrader,
			writeMu:    &writeMu,
			recorder:   recorder,
			codexHome:  codexHome,
			finalDelta: finalDelta,
		}, w, r)
	}))
	t.Cleanup(server.Close)
	return "ws" + strings.TrimPrefix(server.URL, "http")
}

type dynamicSkillModelHandlerConfig struct {
	upgrader   websocket.Upgrader
	writeMu    *sync.Mutex
	recorder   *dynamicSkillModelRecorder
	codexHome  string
	finalDelta string
}

func handleDynamicSkillModelConnection(t *testing.T, cfg dynamicSkillModelHandlerConfig, w http.ResponseWriter, r *http.Request) {
	t.Helper()
	conn, err := cfg.upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer conn.Close()
	state := dynamicSkillModelConnectionState{cfg: cfg, conn: conn}
	for {
		msg, ok := readDynamicSkillRPCMessage(conn)
		if !ok {
			return
		}
		if state.handleToolResponse(t, msg) {
			continue
		}
		if len(msg.ID) == 0 {
			continue
		}
		result, sendToolCall := dynamicSkillRPCResult(cfg, msg)
		writeDynamicSkillResponse(t, cfg.writeMu, conn, msg.ID, result)
		if sendToolCall {
			writeDynamicSkillToolCall(t, cfg.writeMu, conn)
		}
	}
}

type dynamicSkillModelConnectionState struct {
	cfg       dynamicSkillModelHandlerConfig
	conn      *websocket.Conn
	finalSent bool
}

func readDynamicSkillRPCMessage(conn *websocket.Conn) (jsonRPCMessage, bool) {
	_, rawBytes, err := conn.ReadMessage()
	if err != nil {
		return jsonRPCMessage{}, false
	}
	var msg jsonRPCMessage
	if err := json.Unmarshal(rawBytes, &msg); err != nil {
		return jsonRPCMessage{}, true
	}
	return msg, true
}

func (s *dynamicSkillModelConnectionState) handleToolResponse(t *testing.T, msg jsonRPCMessage) bool {
	t.Helper()
	if strings.TrimSpace(msg.Method) != "" || len(msg.ID) == 0 {
		return false
	}
	s.cfg.recorder.recordToolResponse(msg)
	if strings.TrimSpace(s.cfg.finalDelta) != "" && !s.finalSent {
		s.finalSent = true
		writeDynamicSkillNotification(t, s.cfg.writeMu, s.conn, "item/agentMessage/delta", map[string]any{
			"threadId": "provider-thread-1",
			"turnId":   "turn-1",
			"delta":    s.cfg.finalDelta,
		})
	}
	return true
}

func dynamicSkillRPCResult(cfg dynamicSkillModelHandlerConfig, msg jsonRPCMessage) (any, bool) {
	switch msg.Method {
	case "initialize":
		return map[string]any{"codexHome": cfg.codexHome}, false
	case "model/list":
		return validCodexModelListMap(), false
	case "thread/start":
		cfg.recorder.recordThreadStart(msg.Params)
		return map[string]any{"thread": map[string]any{"id": "provider-thread-1"}}, false
	case "turn/start":
		return map[string]any{"turn": map[string]any{"id": "turn-1"}}, true
	default:
		return map[string]any{"ok": true}, false
	}
}

func writeDynamicSkillToolCall(t *testing.T, writeMu *sync.Mutex, conn *websocket.Conn) {
	t.Helper()
	writeDynamicSkillRequest(t, writeMu, conn, "tool-call-1", "item/tool/call", map[string]any{
		"name":      testGenericDynamicToolName,
		"callId":    "tool-call-1",
		"turnId":    "turn-1",
		"arguments": map[string]any{"payload": "demo"},
	})
}

func writeDynamicSkillResponse(t *testing.T, writeMu *sync.Mutex, conn *websocket.Conn, id json.RawMessage, result any) {
	t.Helper()
	writeDynamicSkillPayload(t, writeMu, conn, map[string]any{"jsonrpc": "2.0", "id": json.RawMessage(append([]byte(nil), id...)), "result": result})
}

func writeDynamicSkillRequest(t *testing.T, writeMu *sync.Mutex, conn *websocket.Conn, id string, method string, params map[string]any) {
	t.Helper()
	writeDynamicSkillPayload(t, writeMu, conn, map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params})
}

func writeDynamicSkillNotification(t *testing.T, writeMu *sync.Mutex, conn *websocket.Conn, method string, params map[string]any) {
	t.Helper()
	writeDynamicSkillPayload(t, writeMu, conn, map[string]any{"jsonrpc": "2.0", "method": method, "params": params})
}

func writeDynamicSkillPayload(t *testing.T, writeMu *sync.Mutex, conn *websocket.Conn, payload map[string]any) {
	t.Helper()
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal websocket payload: %v", err)
	}
	writeMu.Lock()
	defer writeMu.Unlock()
	if err := conn.WriteMessage(websocket.TextMessage, raw); err != nil {
		return
	}
}

func waitDynamicSkillRawToolCall(t *testing.T, ch <-chan RawMessage) RawMessage {
	t.Helper()
	select {
	case msg := <-ch:
		return msg
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for model skill tool call")
		return RawMessage{}
	}
}

func waitDynamicSkillToolResponse(t *testing.T, ch <-chan jsonRPCMessage) jsonRPCMessage {
	t.Helper()
	select {
	case msg := <-ch:
		return msg
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for skill tool response")
		return jsonRPCMessage{}
	}
}

func waitDynamicSkillFinalDelta(t *testing.T, ch <-chan turndto.TurnOutputDelta) turndto.TurnOutputDelta {
	t.Helper()
	select {
	case ev := <-ch:
		return ev
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for final answer delta")
		return turndto.TurnOutputDelta{}
	}
}

func decodeDynamicSkillToolParams(t *testing.T, raw json.RawMessage) map[string]any {
	t.Helper()
	var params map[string]any
	if err := json.Unmarshal(raw, &params); err != nil {
		t.Fatalf("decode tool params %s: %v", string(raw), err)
	}
	if params["name"] != testGenericDynamicToolName {
		t.Fatalf("tool name = %#v, want %s", params["name"], testGenericDynamicToolName)
	}
	args, ok := params["arguments"].(map[string]any)
	if !ok || args["payload"] != "demo" {
		t.Fatalf("tool arguments = %#v, want demo payload", params["arguments"])
	}
	return params
}

func decodeDynamicSkillToolResponseResult(t *testing.T, msg jsonRPCMessage) map[string]any {
	t.Helper()
	if msg.Error != nil {
		t.Fatalf("tool response error = %+v", msg.Error)
	}
	var result map[string]any
	if err := json.Unmarshal(msg.Result, &result); err != nil {
		t.Fatalf("decode tool response result %s: %v", string(msg.Result), err)
	}
	return result
}

func dynamicSkillToolResponseText(t *testing.T, result map[string]any) string {
	t.Helper()
	items, ok := result["contentItems"].([]any)
	if !ok || len(items) != 1 {
		t.Fatalf("contentItems = %#v, want one item", result["contentItems"])
	}
	item, ok := items[0].(map[string]any)
	if !ok {
		t.Fatalf("content item = %#v, want object", items[0])
	}
	text, _ := item["text"].(string)
	if strings.TrimSpace(text) == "" {
		t.Fatalf("content item text empty: %#v", item)
	}
	return text
}

func assertGenericDynamicToolFixtureAdvertised(t *testing.T, params map[string]any) {
	t.Helper()
	tools, ok := params["dynamicTools"].([]any)
	if !ok || len(tools) != 1 {
		t.Fatalf("generic fixture dynamicTools = %#v, want one tool", params["dynamicTools"])
	}
	tool, ok := tools[0].(map[string]any)
	if !ok {
		t.Fatalf("generic fixture dynamicTools[0] = %#v, want object", tools[0])
	}
	if tool["name"] != testGenericDynamicToolName {
		t.Fatalf("generic fixture dynamic tool name = %#v, want %s", tool["name"], testGenericDynamicToolName)
	}
}

func dynamicSkillSuccessToolResult(content string) map[string]any {
	body, _ := json.Marshal(map[string]any{"name": "demo", "content": content})
	return map[string]any{
		"success": true,
		"contentItems": []map[string]any{{
			"type": "inputText",
			"text": string(body),
		}},
	}
}

func dynamicSkillDeniedToolResult() map[string]any {
	body, _ := json.Marshal(map[string]any{
		"kind":  "approval_denied",
		"tool":  testGenericDynamicToolName,
		"error": "dynamic tool approval denied: user denied",
	})
	return map[string]any{
		"success": false,
		"contentItems": []map[string]any{{
			"type": "inputText",
			"text": string(body),
		}},
	}
}
