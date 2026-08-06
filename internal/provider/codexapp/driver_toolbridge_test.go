package codexapp

import (
	"context"
	"encoding/json"
	"maps"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	contract "github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
	dto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/provider"
	codexprotocol "github.com/lihah111222333-cloud/super-dolphin-agent/internal/provider/codexapp/protocol"
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
	serverURL := startToolBridgeRPCServer(t, recorder)
	manager := &ServerManager{}

	listToolsCalls := 0
	got := requireToolBridgeDriver(t, newDriver(testLoggerRuntime(t), nil, nil, testApprovalManager(), nil, manager, newSingleURLPoolForTest(t, serverURL), &recordingSkillMirrorReconciler{}, nil, func(context.Context) ([]codexprotocol.DynamicToolSchema, error) {
		listToolsCalls++
		return []codexprotocol.DynamicToolSchema{{
			Name:        "tool.echo",
			Description: "echo payload",
			InputSchema: json.RawMessage(`{"type":"object"}`),
		}}, nil
	}))
	if got.listTools == nil {
		t.Fatal("listTools = nil, want configured")
	}
	if manager.getToolHandler() != nil {
		t.Fatal("toolHandler = non-nil, want nil")
	}

	sessionAny, err := got.StartSession(context.Background(), dto.StartSessionRequest{
		AgentID:       "agent-1",
		CWD:           t.TempDir(),
		StartAssembly: validStartAssemblyForTest(),
	})
	if err != nil {
		t.Fatalf("StartSession() error = %v", err)
	}
	s := requireCodexSession(t, sessionAny, "StartSession")
	defer closeCodexTestSession(t, s)

	assertDynamicToolsStartSession(t, recorder, s, listToolsCalls)
}

func TestToolBridge_StartSession_ChatModeCarriesDynamicTools(t *testing.T) {
	recorder := &toolBridgeRPCRecorder{}
	serverURL := startToolBridgeRPCServer(t, recorder)
	manager := &ServerManager{}

	listToolsCalls := 0
	got := requireToolBridgeDriver(t, newDriver(testLoggerRuntime(t), nil, nil, testApprovalManager(), nil, manager, newSingleURLPoolForTest(t, serverURL), &recordingSkillMirrorReconciler{}, nil, func(context.Context) ([]codexprotocol.DynamicToolSchema, error) {
		listToolsCalls++
		return []codexprotocol.DynamicToolSchema{{
			Name:        "tool.echo",
			Description: "echo payload",
			InputSchema: json.RawMessage(`{"type":"object"}`),
		}}, nil
	}))

	sessionAny, err := got.StartSession(context.Background(), dto.StartSessionRequest{
		AgentID:         "agent-1",
		CWD:             t.TempDir(),
		StartAssembly:   validStartAssemblyForTest(),
		ToolSurfaceMode: contract.ToolSurfaceModeChat,
	})
	if err != nil {
		t.Fatalf("StartSession() error = %v", err)
	}
	s := requireCodexSession(t, sessionAny, "StartSession")
	defer closeCodexTestSession(t, s)

	assertDynamicToolsStartSession(t, recorder, s, listToolsCalls)
}

func TestToolBridge_StartSession_PreparesScopedCodexSurface(t *testing.T) {
	recorder := &toolBridgeRPCRecorder{}
	serverURL := startToolBridgeRPCServer(t, recorder)
	manager := &ServerManager{}

	var gotScope contract.CodexToolSurfaceScope
	var gotBindScope contract.CodexToolSurfaceScope
	d := requireToolBridgeDriver(t, newDriver(testLoggerRuntime(t), nil, nil, testApprovalManager(), nil, manager, newSingleURLPoolForTest(t, serverURL), &recordingSkillMirrorReconciler{}, nil, nil))
	d.prepareTools = func(_ context.Context, scope contract.CodexToolSurfaceScope) ([]codexprotocol.DynamicToolSchema, error) {
		gotScope = scope
		return []codexprotocol.DynamicToolSchema{{Name: "grep", InputSchema: json.RawMessage(`{"type":"object"}`)}}, nil
	}
	d.bindTools = func(scope contract.CodexToolSurfaceScope) error {
		gotBindScope = scope
		return nil
	}
	workDir := t.TempDir()
	extraDir := t.TempDir()

	sessionAny, err := d.StartSession(context.Background(), dto.StartSessionRequest{
		AgentID:       "agent-1",
		CWD:           workDir,
		StartAssembly: validStartAssemblyForTest(),
		Config: map[string]any{
			"binary_dir":                   "/tmp/super-agent-bin",
			"additionalWorkingDirectories": []string{extraDir},
		},
	})
	if err != nil {
		t.Fatalf("StartSession() error = %v", err)
	}
	s := requireCodexSession(t, sessionAny, "StartSession")
	defer closeCodexTestSession(t, s)

	assertStartSurfaceScope(t, gotScope, gotBindScope, workDir, extraDir)
	if len(gotScope.Manifest.Binaries) == 0 {
		t.Fatalf("surface manifest = %#v, want stdio binaries", gotScope.Manifest)
	}
	if gotScope.Manifest.Binaries[0].URL != "" {
		t.Fatalf("surface manifest first URL = %q, want stdio command", gotScope.Manifest.Binaries[0].URL)
	}
	assertDynamicToolsStartSession(t, recorder, s, 1)
}

func assertStartSurfaceScope(t *testing.T, gotScope, gotBindScope contract.CodexToolSurfaceScope, workDir, extraDir string) {
	t.Helper()
	if gotScope.AgentID != "agent-1" || gotScope.CWD != workDir {
		t.Fatalf("surface scope = %#v, want agent/cwd", gotScope)
	}
	if gotScope.SurfaceID == "" {
		t.Fatalf("surface scope = %#v, want surface id", gotScope)
	}
	if gotBindScope.SurfaceID != gotScope.SurfaceID ||
		gotBindScope.AgentID != "agent-1" ||
		gotBindScope.ProviderThreadID != "provider-thread-1" ||
		gotBindScope.CWD != workDir {
		t.Fatalf("surface bind scope = %#v, want same surface id plus agent/provider thread/cwd", gotBindScope)
	}
	wantRoots := []string{workDir, extraDir}
	if !maps.Equal(sliceSet(gotScope.WorkspaceRoots), sliceSet(wantRoots)) ||
		!maps.Equal(sliceSet(gotBindScope.WorkspaceRoots), sliceSet(wantRoots)) {
		t.Fatalf("surface roots = prepare %#v bind %#v, want %#v", gotScope.WorkspaceRoots, gotBindScope.WorkspaceRoots, wantRoots)
	}
}

func sliceSet(values []string) map[string]bool {
	out := make(map[string]bool, len(values))
	for _, value := range values {
		out[value] = true
	}
	return out
}

func TestToolBridge_StartSession_InjectsMemoryReadDynamicTool(t *testing.T) {
	recorder := &toolBridgeRPCRecorder{}
	serverURL := startToolBridgeRPCServer(t, recorder)
	manager := &ServerManager{}
	got := requireToolBridgeDriver(t, newDriver(testLoggerRuntime(t), nil, nil, testApprovalManager(), nil, manager, newSingleURLPoolForTest(t, serverURL), &recordingSkillMirrorReconciler{}, nil, func(context.Context) ([]codexprotocol.DynamicToolSchema, error) {
		return []codexprotocol.DynamicToolSchema{{
			Name:        "memory_read",
			Description: "host direct memory read",
			InputSchema: json.RawMessage(`{"type":"object"}`),
		}}, nil
	}))

	sessionAny, err := got.StartSession(context.Background(), dto.StartSessionRequest{
		AgentID:       "agent-1",
		CWD:           t.TempDir(),
		StartAssembly: validStartAssemblyForTest(),
	})
	if err != nil {
		t.Fatalf("StartSession() error = %v", err)
	}
	s, ok := sessionAny.(*session)
	if !ok {
		t.Fatalf("StartSession() type = %T, want *session", sessionAny)
	}
	defer closeCodexTestSession(t, s)

	params := recorder.threadStartParamsSnapshot()
	tools, ok := params["dynamicTools"].([]any)
	if !ok {
		t.Fatalf("dynamicTools = %#v, want array", params["dynamicTools"])
	}
	for _, raw := range tools {
		tool, ok := raw.(map[string]any)
		if ok && tool["name"] == "memory_read" {
			return
		}
	}
	t.Fatalf("dynamicTools = %#v, want memory_read", tools)
}

const testDynamicToolName = "test_dynamic_echo"

func TestResumeSession_DynamicSkillToolsStillCallable(t *testing.T) {
	obs := runDynamicToolsResumeScenario(t)
	if obs.toolMethod != "item/tool/call" {
		t.Fatalf("tool method = %q, want item/tool/call", obs.toolMethod)
	}
	if obs.toolName != testDynamicToolName {
		t.Fatalf("tool name = %q, want %s", obs.toolName, testDynamicToolName)
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
	raw := mustJSON(mustBuildThreadResumeParams(t, dto.ResumeSessionRequest{
		ThreadID:       "thread-1",
		PromptSnapshot: validResumePromptSnapshotForTest(),
	}))
	var params map[string]any
	if err := json.Unmarshal(raw, &params); err != nil {
		t.Fatalf("json.Unmarshal(thread resume params) error = %v", err)
	}
	if _, ok := params["dynamicTools"]; ok {
		t.Fatalf("thread/resume params must not carry dynamicTools unless app-server wire support is explicit: %#v", params)
	}
}

func TestResumeSession_RebuildsCodexToolSurfaceWithoutDynamicTools(t *testing.T) {
	state := &dynamicToolsResumeState{}
	var writeMu sync.Mutex
	server := startDynamicToolsResumeServer(t, state, make(chan jsonRPCMessage, 1), &writeMu)
	defer server.Close()

	workDir := t.TempDir()
	extraDir := t.TempDir()
	var gotScope contract.CodexToolSurfaceScope
	d := &driver{logRuntime: testLoggerRuntime(t),
		approvals:    testApprovalManager(),
		pool:         newSingleURLPoolForTest(t, "ws"+strings.TrimPrefix(server.URL, "http")),
		mirror:       &recordingSkillMirrorReconciler{},
		skillMetrics: testSkillMetrics(t),
		prepareTools: func(_ context.Context, scope contract.CodexToolSurfaceScope) ([]codexprotocol.DynamicToolSchema, error) {
			gotScope = scope
			return []codexprotocol.DynamicToolSchema{{Name: "grep", InputSchema: json.RawMessage(`{"type":"object"}`)}}, nil
		},
	}

	resumed, err := d.ResumeSession(context.Background(), dto.ResumeSessionRequest{
		Provider:           "codex",
		AgentID:            "agent-1",
		ThreadID:           "local-thread-1",
		ProviderThreadID:   "provider-thread-1",
		CWD:                workDir,
		PromptSnapshot:     validResumePromptSnapshotForTest(),
		CodexHome:          t.TempDir(),
		CodexInstanceKey:   "default",
		CodexModelProvider: "openai",
		Config: map[string]any{
			"additionalWorkingDirectories": []any{extraDir},
		},
	})
	if err != nil {
		t.Fatalf("ResumeSession() error = %v", err)
	}
	s := requireCodexSession(t, resumed, "ResumeSession")
	defer closeCodexTestSession(t, s)

	if gotScope.AgentID != "agent-1" || gotScope.ProviderThreadID != "provider-thread-1" || gotScope.CWD != workDir {
		t.Fatalf("surface scope = %#v, want resumed agent/provider thread/cwd", gotScope)
	}
	wantRoots := []string{workDir, extraDir}
	if !maps.Equal(sliceSet(gotScope.WorkspaceRoots), sliceSet(wantRoots)) {
		t.Fatalf("surface roots = %#v, want %#v", gotScope.WorkspaceRoots, wantRoots)
	}
	var roots []string
	if err := json.Unmarshal([]byte(gotScope.Manifest.Binaries[0].Env["GO_AGENT_LSP_ROOTS"]), &roots); err != nil {
		t.Fatalf("decode GO_AGENT_LSP_ROOTS: %v", err)
	}
	if !maps.Equal(sliceSet(roots), sliceSet(wantRoots)) {
		t.Fatalf("GO_AGENT_LSP_ROOTS = %#v, want %#v", roots, wantRoots)
	}
	if got := s.RuntimeConfigSnapshot()["additionalWorkingDirectories"]; !reflect.DeepEqual(got, []any{extraDir}) {
		t.Fatalf("runtime additionalWorkingDirectories = %#v, want %q", got, extraDir)
	}
	_, resumeHadDynamicTools := state.snapshot()
	if resumeHadDynamicTools {
		t.Fatal("thread/resume unexpectedly sent dynamicTools while rebuilding local surface")
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
		if tool["name"] == testDynamicToolName {
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
	pool := newSingleURLPoolForTest(t, "ws"+strings.TrimPrefix(server.URL, "http"))

	d := &driver{logRuntime: testLoggerRuntime(t),
		approvals:    testApprovalManager(),
		manager:      manager,
		pool:         pool,
		mirror:       &recordingSkillMirrorReconciler{},
		skillMetrics: testSkillMetrics(t),
		listTools: func(context.Context) ([]codexprotocol.DynamicToolSchema, error) {
			return []codexprotocol.DynamicToolSchema{dynamicSkillToolSchema()}, nil
		},
	}
	workDir := t.TempDir()
	started, err := d.StartSession(context.Background(), dto.StartSessionRequest{
		AgentID:       "agent-1",
		CWD:           workDir,
		StartAssembly: validStartAssemblyForTest(),
	})
	if err != nil {
		t.Fatalf("StartSession() error = %v", err)
	}
	if s, ok := started.(*session); ok {
		closeCodexTestSession(t, s)
	}

	resumed, err := d.ResumeSession(context.Background(), dto.ResumeSessionRequest{
		Provider:           "codex",
		AgentID:            "agent-1",
		ThreadID:           "provider-thread-1",
		ProviderThreadID:   "provider-thread-1",
		CWD:                workDir,
		PromptSnapshot:     validResumePromptSnapshotForTest(),
		CodexHome:          t.TempDir(),
		CodexInstanceKey:   "default",
		CodexModelProvider: "openai",
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
		toolAgentID:           firstTestStringValue(params, "_agentId", "agentId"),
	}
}

func dynamicSkillToolSchema() codexprotocol.DynamicToolSchema {
	return codexprotocol.DynamicToolSchema{
		Name:        testDynamicToolName,
		Description: "echo test payload",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"payload":{"type":"string"}},"required":["payload"]}`),
	}
}

func firstTestStringValue(values map[string]any, keys ...string) string {
	for _, key := range keys {
		if value := stringValue(values, key); value != "" {
			return value
		}
	}
	return ""
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
		serveDynamicToolsResumeConn(t, conn, state, toolResponses, writeMu)
	}))
}

func requireToolBridgeDriver(t *testing.T, raw any) *driver {
	t.Helper()
	got, ok := raw.(*driver)
	if !ok {
		t.Fatalf("newDriver(testLoggerRuntime(t), ) type = %T, want *driver", raw)
	}
	got.skillMetrics = testSkillMetrics(t)
	return got
}

func requireCodexSession(t *testing.T, raw any, op string) *session {
	t.Helper()
	s, ok := raw.(*session)
	if !ok {
		t.Fatalf("%s() type = %T, want *session", op, raw)
	}
	return s
}

func assertDynamicToolsStartSession(t *testing.T, recorder *toolBridgeRPCRecorder, s *session, listToolsCalls int) {
	t.Helper()
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
	if !ok {
		t.Fatalf("dynamicTools = %#v, want array", params["dynamicTools"])
	}
	if len(tools) != 1 {
		t.Fatalf("dynamicTools = %#v, want one tool", params["dynamicTools"])
	}
	if s.ThreadID() != "provider-thread-1" {
		t.Fatalf("ThreadID() = %q, want provider-thread-1", s.ThreadID())
	}
}

func serveDynamicToolsResumeConn(t *testing.T, conn *websocket.Conn, state *dynamicToolsResumeState, toolResponses chan<- jsonRPCMessage, writeMu *sync.Mutex) {
	t.Helper()
	for {
		if !handleDynamicToolsResumeMessage(t, conn, state, toolResponses, writeMu) {
			return
		}
	}
}

func handleDynamicToolsResumeMessage(t *testing.T, conn *websocket.Conn, state *dynamicToolsResumeState, toolResponses chan<- jsonRPCMessage, writeMu *sync.Mutex) bool {
	t.Helper()
	_, rawBytes, err := conn.ReadMessage()
	if err != nil {
		return false
	}
	msg, ok := decodeDynamicToolsResumeMessage(rawBytes)
	if !ok {
		return true
	}
	if strings.TrimSpace(msg.Method) == "" {
		toolResponses <- msg
		return true
	}
	if len(msg.ID) == 0 {
		return true
	}
	params := decodeJSONRPCParams(msg.Params)
	result, sendToolCall := dynamicToolsResumeResult(state, msg.Method, params)
	if !writeDynamicToolsResumeResponse(conn, writeMu, msg.ID, result) {
		return false
	}
	if sendToolCall {
		sendCodexToolCall(t, writeMu, conn, 99, testDynamicToolName, map[string]any{"payload": "demo"})
	}
	return true
}

func decodeDynamicToolsResumeMessage(rawBytes []byte) (jsonRPCMessage, bool) {
	var msg jsonRPCMessage
	if err := json.Unmarshal(rawBytes, &msg); err != nil {
		return jsonRPCMessage{}, false
	}
	return msg, true
}

func decodeJSONRPCParams(raw json.RawMessage) map[string]any {
	var params map[string]any
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &params)
	}
	return params
}

func dynamicToolsResumeResult(state *dynamicToolsResumeState, method string, params map[string]any) (map[string]any, bool) {
	result := map[string]any{"ok": true}
	switch method {
	case "model/list":
		result = validCodexModelListMap()
	case "thread/start":
		state.markStart(params)
		result = map[string]any{"thread": map[string]any{"id": "provider-thread-1"}}
	case "thread/resume":
		return map[string]any{
			"thread":         map[string]any{"id": "provider-thread-1"},
			"approvalPolicy": "never",
		}, state.markResume(params)
	}
	return result, false
}

func writeDynamicToolsResumeResponse(conn *websocket.Conn, writeMu *sync.Mutex, id json.RawMessage, result map[string]any) bool {
	resp := mustJSON(map[string]any{
		"jsonrpc": "2.0",
		"id":      json.RawMessage(append([]byte(nil), id...)),
		"result":  mustJSON(result),
	})
	writeMu.Lock()
	defer writeMu.Unlock()
	return conn.WriteMessage(websocket.TextMessage, resp) == nil
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
			switch msg.Method {
			case "model/list":
				result = validCodexModelListMap()
			case "thread/start":
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
