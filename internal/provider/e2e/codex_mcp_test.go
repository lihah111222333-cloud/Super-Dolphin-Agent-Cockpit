package e2e

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

	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
	"github.com/anthropic-ai/super-agent-v3/internal/provider/codexapp"
	codexprotocol "github.com/anthropic-ai/super-agent-v3/internal/provider/codexapp/protocol"
	"github.com/gorilla/websocket"
)

func TestCodexStartSession_InjectsDynamicTools_E2E(t *testing.T) {
	recorder := &codexRPCRecorder{}
	t.Setenv("CODEX_APP_SERVER_URL", startCodexRPCServer(t, recorder))

	factory := codexapp.NewDriverFactory(nil, nil, nil, nil, nil, nil)
	factory.SetListTools(func(context.Context) ([]codexprotocol.DynamicToolSchema, error) {
		return []codexprotocol.DynamicToolSchema{{
			Name:        "tool.echo",
			Description: "echo payload",
			InputSchema: json.RawMessage(`{"type":"object"}`),
		}}, nil
	})

	session, err := factory.Create().StartSession(context.Background(), dto.StartSessionRequest{
		AgentID: "agent-1",
		CWD:     "/tmp/codex-e2e/work",
		StartAssembly: dto.StartAssembly{
			BaseInstructions:      "base prompt",
			DeveloperInstructions: "developer prompt",
		},
		Config: map[string]any{
			"mcp": map[string]any{"legacy": true},
		},
	})
	if err != nil {
		t.Fatalf("StartSession() error = %v", err)
	}
	t.Cleanup(func() { _ = session.Close(context.Background()) })

	if session.ThreadID() != "provider-thread-1" {
		t.Fatalf("ThreadID() = %q, want provider-thread-1", session.ThreadID())
	}
	if calls := recorder.calls("thread/start"); calls != 1 {
		t.Fatalf("thread/start calls = %d, want 1", calls)
	}

	params := recorder.threadStartParamsSnapshot()
	assertDynamicToolNames(t, params, []string{"tool.echo"})
	assertNoLegacyMCPKeys(t, params)
	if params["cwd"] != "/tmp/codex-e2e/work" {
		t.Fatalf("cwd = %#v, want /tmp/codex-e2e/work", params["cwd"])
	}
	if params["approvalPolicy"] != "never" {
		t.Fatalf("approvalPolicy = %#v, want never", params["approvalPolicy"])
	}
	if params["baseInstructions"] != "base prompt" {
		t.Fatalf("baseInstructions = %#v, want base prompt", params["baseInstructions"])
	}
	if params["developerInstructions"] != "developer prompt" {
		t.Fatalf("developerInstructions = %#v, want developer prompt", params["developerInstructions"])
	}
}

func TestCodexStartSession_PreservesUserConfigFields_E2E(t *testing.T) {
	recorder := &codexRPCRecorder{}
	t.Setenv("CODEX_APP_SERVER_URL", startCodexRPCServer(t, recorder))

	factory := codexapp.NewDriverFactory(nil, nil, nil, nil, nil, nil)
	factory.SetListTools(func(context.Context) ([]codexprotocol.DynamicToolSchema, error) {
		return []codexprotocol.DynamicToolSchema{
			{
				Name:        "tool.echo",
				Description: "echo payload",
				InputSchema: json.RawMessage(`{"type":"object"}`),
			},
			{
				Name:        "tool.sum",
				Description: "sum payload",
				InputSchema: json.RawMessage(`{"type":"object"}`),
			},
		}, nil
	})

	session, err := factory.Create().StartSession(context.Background(), dto.StartSessionRequest{
		AgentID: "agent-2",
		CWD:     "/tmp/codex-e2e/work",
		Model:   "gpt-5-codex",
		StartAssembly: dto.StartAssembly{
			BaseInstructions:      "base prompt",
			DeveloperInstructions: "developer prompt",
		},
		Config: map[string]any{
			"approval_policy": "on-request",
			"modelProvider":   "openai",
			"personality":     "reviewer",
			"summary":         "brief",
			"effort":          "high",
			"sandbox": map[string]any{
				"mode":           "workspace-write",
				"network_access": false,
			},
		},
	})
	if err != nil {
		t.Fatalf("StartSession() error = %v", err)
	}
	t.Cleanup(func() { _ = session.Close(context.Background()) })

	params := recorder.threadStartParamsSnapshot()
	assertDynamicToolNames(t, params, []string{"tool.echo", "tool.sum"})
	if params["model"] != "gpt-5-codex" {
		t.Fatalf("model = %#v, want gpt-5-codex", params["model"])
	}
	if params["approvalPolicy"] != "on-request" {
		t.Fatalf("approvalPolicy = %#v, want on-request", params["approvalPolicy"])
	}
	if params["modelProvider"] != "openai" {
		t.Fatalf("modelProvider = %#v, want openai", params["modelProvider"])
	}
	if params["personality"] != "reviewer" {
		t.Fatalf("personality = %#v, want reviewer", params["personality"])
	}
	if params["summary"] != "brief" {
		t.Fatalf("summary = %#v, want brief", params["summary"])
	}
	if params["effort"] != "high" {
		t.Fatalf("effort = %#v, want high", params["effort"])
	}
	sandbox, ok := params["sandbox"].(map[string]any)
	if !ok {
		t.Fatalf("sandbox = %#v, want object", params["sandbox"])
	}
	if sandbox["mode"] != "workspace-write" {
		t.Fatalf("sandbox.mode = %#v, want workspace-write", sandbox["mode"])
	}
	if sandbox["network_access"] != false {
		t.Fatalf("sandbox.network_access = %#v, want false", sandbox["network_access"])
	}
}

type codexRPCRecorder struct {
	mu                sync.Mutex
	callCount         map[string]int
	methodParams      map[string][]map[string]any
	threadStartParams map[string]any
}

func (r *codexRPCRecorder) record(method string, raw json.RawMessage) {
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
	if r.methodParams == nil {
		r.methodParams = make(map[string][]map[string]any)
	}
	r.methodParams[method] = append(r.methodParams[method], cloneAnyMap(params))
	if method != "thread/start" {
		return
	}
	r.threadStartParams = cloneAnyMap(params)
}

func (r *codexRPCRecorder) calls(method string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.callCount[method]
}

func (r *codexRPCRecorder) threadStartParamsSnapshot() map[string]any {
	r.mu.Lock()
	defer r.mu.Unlock()
	return cloneAnyMap(r.threadStartParams)
}

func (r *codexRPCRecorder) paramsSnapshot(method string, index int) map[string]any {
	r.mu.Lock()
	defer r.mu.Unlock()
	params := r.methodParams[method]
	if index < 0 || index >= len(params) {
		return nil
	}
	return cloneAnyMap(params[index])
}

func startCodexRPCServer(t *testing.T, recorder *codexRPCRecorder) string {
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
			var msg struct {
				ID     json.RawMessage `json:"id"`
				Method string          `json:"method"`
				Params json.RawMessage `json:"params"`
			}
			if err := json.Unmarshal(rawBytes, &msg); err != nil {
				continue
			}
			recorder.record(msg.Method, msg.Params)
			if len(msg.ID) == 0 {
				continue
			}
			result := map[string]any{"ok": true}
			switch msg.Method {
			case "thread/start":
				result = map[string]any{"thread": map[string]any{"id": "provider-thread-1"}}
			case "turn/start":
				result = map[string]any{"turn": map[string]any{"id": "provider-turn-1"}}
			}
			resp, err := json.Marshal(map[string]any{
				"jsonrpc": "2.0",
				"id":      json.RawMessage(append([]byte(nil), msg.ID...)),
				"result":  result,
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

func assertDynamicToolNames(t *testing.T, params map[string]any, want []string) {
	t.Helper()

	rawTools, ok := params["dynamicTools"].([]any)
	if !ok {
		t.Fatalf("dynamicTools = %#v, want array", params["dynamicTools"])
	}
	got := make([]string, 0, len(rawTools))
	for _, rawTool := range rawTools {
		tool, ok := rawTool.(map[string]any)
		if !ok {
			t.Fatalf("dynamicTools item = %#v, want object", rawTool)
		}
		name, _ := tool["name"].(string)
		got = append(got, name)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("dynamic tool names = %#v, want %#v", got, want)
	}
}

func assertNoLegacyMCPKeys(t *testing.T, params map[string]any) {
	t.Helper()
	for _, key := range []string{"mcp", "mcpConfig", "mcp_config", "mcpServers", "mcp_servers"} {
		if _, ok := params[key]; ok {
			t.Fatalf("%s = %#v, want omitted from thread/start", key, params[key])
		}
	}
}

func cloneAnyMap(src map[string]any) map[string]any {
	if len(src) == 0 {
		return nil
	}
	out := make(map[string]any, len(src))
	maps.Copy(out, src)
	return out
}
