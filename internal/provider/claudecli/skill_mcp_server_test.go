package claudecli

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/anthropic-ai/super-agent-v3/pkg/skillmetrics"
	"github.com/anthropic-ai/super-agent-v3/pkg/skilltool"
)

type fakeSkillHostRPC struct {
	calls      int
	lastMethod string
	lastParams map[string]any
	result     json.RawMessage
	err        error
}

func (f *fakeSkillHostRPC) Call(_ context.Context, method string, params any) (json.RawMessage, error) {
	f.calls++
	f.lastMethod = method
	raw, _ := json.Marshal(params)
	_ = json.Unmarshal(raw, &f.lastParams)
	if f.err != nil {
		return nil, f.err
	}
	return append(json.RawMessage(nil), f.result...), nil
}

func TestSkillMCPServer_ToolsListStaticNoHostRPC(t *testing.T) {
	t.Parallel()

	fake := &fakeSkillHostRPC{}
	provider := newSkillMCPToolProvider(skillMCPRuntime{}, fake)
	tools, err := provider.ListTools(context.Background())
	if err != nil {
		t.Fatalf("ListTools() error = %v", err)
	}
	if fake.calls != 0 {
		t.Fatalf("ListTools() called host RPC %d times, want 0", fake.calls)
	}
	if len(tools) != 2 || tools[0].Name != skilltool.ToolNameExpandBody || tools[1].Name != skilltool.ToolNameReadResource {
		t.Fatalf("tools = %#v, want static skill tools", tools)
	}
	for _, tool := range tools {
		var schema map[string]any
		if err := json.Unmarshal(tool.InputSchema, &schema); err != nil {
			t.Fatalf("schema for %s is invalid JSON: %v", tool.Name, err)
		}
		props, _ := schema["properties"].(map[string]any)
		if _, ok := props["cwd"]; ok {
			t.Fatalf("schema for %s exposes runtime cwd: %#v", tool.Name, props)
		}
	}
}

func TestSkillMCPServer_ExpandBodyLazyCallsHostRPC(t *testing.T) {
	t.Parallel()

	fake := &fakeSkillHostRPC{result: json.RawMessage(`{"name":"demo","content":"body","total_bytes":4}`)}
	provider := newSkillMCPToolProvider(skillMCPRuntime{
		CWD:      "/repo",
		AgentID:  "agent-1",
		ThreadID: "thread-1",
	}, fake)
	provider.now = func() time.Time { return time.Unix(0, 123) }

	got, err := provider.CallTool(context.Background(), skilltool.ToolNameExpandBody, json.RawMessage(`{"name":"demo","anchor":"Usage","max_bytes":256}`))
	if err != nil {
		t.Fatalf("CallTool() error = %v", err)
	}
	if fake.calls != 1 || fake.lastMethod != skillExpandBodyRPCMethod {
		t.Fatalf("host calls = %d method = %q, want one %s", fake.calls, fake.lastMethod, skillExpandBodyRPCMethod)
	}
	if fake.lastParams["cwd"] != "/repo" || fake.lastParams["agentId"] != "agent-1" || fake.lastParams["threadId"] != "thread-1" {
		t.Fatalf("runtime params not injected: %#v", fake.lastParams)
	}
	if _, ok := fake.lastParams["turnId"]; ok {
		t.Fatalf("runtime params leaked per-turn turnId into stable MCP tool call params: %#v", fake.lastParams)
	}
	if fake.lastParams["name"] != "demo" || fake.lastParams["anchor"] != "Usage" || fake.lastParams["max_bytes"] != float64(256) {
		t.Fatalf("model params not forwarded correctly: %#v", fake.lastParams)
	}
	callID, _ := fake.lastParams["callId"].(string)
	if !strings.HasPrefix(callID, skillMCPGeneratedCallIDPrefix+"-skill-expand-body-") {
		t.Fatalf("callId = %q, want generated skill MCP call id", callID)
	}
	raw, ok := got.(json.RawMessage)
	if !ok {
		t.Fatalf("CallTool() result type = %T, want json.RawMessage", got)
	}
	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatalf("result JSON invalid: %v", err)
	}
	if payload["content"] != "body" {
		t.Fatalf("result payload = %#v", payload)
	}
}

func TestSkillMCPServer_ReadResourceLazyCallsHostRPC(t *testing.T) {
	t.Parallel()

	fake := &fakeSkillHostRPC{result: json.RawMessage(`{"name":"demo","path":"ref.md","content":"resource","total_bytes":8}`)}
	provider := newSkillMCPToolProvider(skillMCPRuntime{
		CWD:      "/repo",
		AgentID:  "agent-1",
		ThreadID: "thread-1",
	}, fake)
	provider.now = func() time.Time { return time.Unix(0, 456) }

	got, err := provider.CallTool(context.Background(), skilltool.ToolNameReadResource, json.RawMessage(`{"name":"demo","path":"ref.md","max_bytes":128}`))
	if err != nil {
		t.Fatalf("CallTool() error = %v", err)
	}
	if fake.calls != 1 || fake.lastMethod != skillReadResourceRPCMethod {
		t.Fatalf("host calls = %d method = %q, want one %s", fake.calls, fake.lastMethod, skillReadResourceRPCMethod)
	}
	if fake.lastParams["cwd"] != "/repo" || fake.lastParams["agentId"] != "agent-1" || fake.lastParams["threadId"] != "thread-1" {
		t.Fatalf("runtime params not injected: %#v", fake.lastParams)
	}
	if fake.lastParams["name"] != "demo" || fake.lastParams["path"] != "ref.md" || fake.lastParams["max_bytes"] != float64(128) {
		t.Fatalf("model params not forwarded correctly: %#v", fake.lastParams)
	}
	callID, _ := fake.lastParams["callId"].(string)
	if !strings.HasPrefix(callID, skillMCPGeneratedCallIDPrefix+"-skill-read-resource-") {
		t.Fatalf("callId = %q, want generated skill MCP call id", callID)
	}
	raw, ok := got.(json.RawMessage)
	if !ok {
		t.Fatalf("CallTool() result type = %T, want json.RawMessage", got)
	}
	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatalf("result JSON invalid: %v", err)
	}
	if payload["content"] != "resource" {
		t.Fatalf("result payload = %#v", payload)
	}
}

func TestSkillMCPServer_RejectsModelRuntimeFields(t *testing.T) {
	t.Parallel()

	// 移除 DisallowUnknownFields 后，LLM 发送的额外字段（包括运行时字段如 cwd）
	// 应被静默忽略，由 provider 注入真实的运行时值。
	fake := &fakeSkillHostRPC{result: json.RawMessage(`{}`)}
	provider := newSkillMCPToolProvider(skillMCPRuntime{CWD: "/repo"}, fake)
	if _, err := provider.CallTool(context.Background(), skilltool.ToolNameExpandBody, json.RawMessage(`{"name":"demo","cwd":"/evil"}`)); err != nil {
		t.Fatalf("CallTool() error = %v, want extra fields silently ignored", err)
	}
	if fake.calls != 1 {
		t.Fatalf("host RPC calls = %d, want 1", fake.calls)
	}
	// 关键：注入的 cwd 应该是运行时值 /repo 而非 LLM 传入的 /evil。
	if fake.lastParams["cwd"] != "/repo" {
		t.Fatalf("cwd = %v, want /repo (runtime-injected, not model-provided)", fake.lastParams["cwd"])
	}
}

func TestSkillMCPServer_FirstTurnWithoutSkillDoesNotCallExpandRPC(t *testing.T) {
	t.Parallel()

	fake := &fakeSkillHostRPC{}
	provider := newSkillMCPToolProvider(skillMCPRuntime{CWD: "/repo"}, fake)
	if _, err := provider.ListTools(context.Background()); err != nil {
		t.Fatalf("ListTools() error = %v", err)
	}
	if fake.calls != 0 {
		t.Fatalf("host RPC calls before any tools/call = %d, want 0", fake.calls)
	}
}

func TestSkillMCPServer_ObservabilityCounters(t *testing.T) {
	skillmetrics.ResetForTesting()
	t.Cleanup(skillmetrics.ResetForTesting)

	success := newSkillMCPToolProvider(skillMCPRuntime{CWD: "/repo"}, &fakeSkillHostRPC{result: json.RawMessage(`{"name":"demo","content":"body"}`)})
	if _, err := success.CallTool(context.Background(), skilltool.ToolNameExpandBody, json.RawMessage(`{"name":"demo"}`)); err != nil {
		t.Fatalf("success CallTool() error = %v", err)
	}

	approval := newSkillMCPToolProvider(skillMCPRuntime{CWD: "/repo"}, &fakeSkillHostRPC{err: &skillHostRPCError{
		Code:    skillApprovalRequiredRPCCode,
		Message: "skill artifact approval required",
		Data:    json.RawMessage(`{"CallID":"call-1","ToolName":"skill_expand_body"}`),
	}})
	if _, err := approval.CallTool(context.Background(), skilltool.ToolNameExpandBody, json.RawMessage(`{"name":"demo"}`)); err != nil {
		t.Fatalf("approval CallTool() error = %v", err)
	}

	// 移除 DisallowUnknownFields 后，额外字段不再触发 decode error。
	// 改用 unknown tool name 作为 error 路径的覆盖。
	invalid := newSkillMCPToolProvider(skillMCPRuntime{CWD: "/repo"}, &fakeSkillHostRPC{})
	if _, err := invalid.CallTool(context.Background(), "nonexistent_tool", json.RawMessage(`{"name":"demo"}`)); err == nil {
		t.Fatal("invalid CallTool() error = nil, want unknown tool rejection")
	}

	snap := skillmetrics.Read()
	if snap.SkillMCPToolCallTotal != 3 || snap.SkillMCPToolSuccessTotal != 1 || snap.SkillMCPApprovalRequiredTotal != 1 || snap.SkillMCPToolErrorTotal != 1 {
		t.Fatalf("skill MCP observability counters mismatch: %+v", snap)
	}
}

func TestSkillMCPServer_ApprovalRequiredReturnsStructuredEnvelope(t *testing.T) {
	t.Parallel()

	fake := &fakeSkillHostRPC{err: &skillHostRPCError{
		Code:    skillApprovalRequiredRPCCode,
		Message: "skill artifact approval required",
		Data:    json.RawMessage(`{"CallID":"call-1","ToolName":"skill_expand_body"}`),
	}}
	provider := newSkillMCPToolProvider(skillMCPRuntime{CWD: "/repo"}, fake)
	provider.now = func() time.Time { return time.Unix(0, 1) }

	got, err := provider.CallTool(context.Background(), skilltool.ToolNameExpandBody, json.RawMessage(`{"name":"demo"}`))
	if err != nil {
		t.Fatalf("CallTool() error = %v", err)
	}
	envelope, ok := got.(map[string]any)
	if !ok {
		t.Fatalf("CallTool() result type = %T, want envelope map", got)
	}
	if envelope["kind"] != "approval_required" || envelope["status"] != "required" || envelope["rpc_code"] != skillApprovalRequiredRPCCode {
		t.Fatalf("approval envelope = %#v", envelope)
	}
	if _, ok := envelope["approval"].(map[string]any); !ok {
		t.Fatalf("approval data missing from envelope: %#v", envelope)
	}
}

func TestSkillMCPServer_StartupLatencyBudget(t *testing.T) {
	t.Parallel()

	provider := newSkillMCPToolProvider(skillMCPRuntime{}, &fakeSkillHostRPC{})
	start := time.Now()
	if _, err := provider.ListTools(context.Background()); err != nil {
		t.Fatalf("ListTools() error = %v", err)
	}
	if elapsed := time.Since(start); elapsed > 200*time.Millisecond {
		t.Fatalf("static tools/list took %s, want <= 200ms", elapsed)
	}
}

func TestSkillMCPMode_StdioSmokeInitializeListCallAndEOF(t *testing.T) {
	requests := make(chan skillJSONRPCRequest, 1)
	addr := startSkillHostRPCRecordingServer(t, requests, `{"jsonrpc":"2.0","id":1,"result":{"name":"demo","content":"body","total_bytes":4}}`)
	t.Setenv("GO_AGENT_CTL_RPC_ADDR", addr)
	t.Setenv("GO_AGENT_SKILL_MCP_CWD", "/repo")
	t.Setenv("GO_AGENT_SKILL_MCP_AGENT_ID", "agent-1")
	t.Setenv("GO_AGENT_SKILL_MCP_THREAD_ID", "thread-1")

	stdin := strings.NewReader(strings.Join([]string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05"}}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}`,
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"skill_expand_body","arguments":{"name":"demo","anchor":"Usage"}}}`,
	}, "\n") + "\n")
	var stdout bytes.Buffer
	if err := RunSkillMCPMode(context.Background(), stdin, &stdout); err != nil {
		t.Fatalf("RunSkillMCPMode() error = %v", err)
	}

	responses := decodeSkillMCPResponses(t, stdout.Bytes())
	if len(responses) != 3 {
		t.Fatalf("responses len = %d, want 3; raw=%s", len(responses), stdout.String())
	}
	if responses[0]["id"] != float64(1) || responses[1]["id"] != float64(2) || responses[2]["id"] != float64(3) {
		t.Fatalf("response ids = %#v", responses)
	}
	initResult, _ := responses[0]["result"].(map[string]any)
	serverInfo, _ := initResult["serverInfo"].(map[string]any)
	if serverInfo["name"] != skillMCPServerName {
		t.Fatalf("initialize serverInfo = %#v, want skill server", serverInfo)
	}
	listResult, _ := responses[1]["result"].(map[string]any)
	tools, _ := listResult["tools"].([]any)
	if len(tools) != 2 {
		t.Fatalf("tools/list result = %#v, want two static skill tools", listResult)
	}
	callResult, _ := responses[2]["result"].(map[string]any)
	content, _ := callResult["content"].([]any)
	if len(content) != 1 {
		t.Fatalf("tools/call result = %#v, want one text content item", callResult)
	}
	textItem, _ := content[0].(map[string]any)
	text, _ := textItem["text"].(string)
	if !strings.Contains(text, `"content":"body"`) {
		t.Fatalf("tools/call text = %q, want host RPC result body", text)
	}

	select {
	case req := <-requests:
		if req.Method != skillExpandBodyRPCMethod {
			t.Fatalf("host RPC method = %q, want %q", req.Method, skillExpandBodyRPCMethod)
		}
		var params map[string]any
		if err := json.Unmarshal(req.Params, &params); err != nil {
			t.Fatalf("host RPC params invalid JSON: %v", err)
		}
		if params["cwd"] != "/repo" || params["agentId"] != "agent-1" || params["threadId"] != "thread-1" {
			t.Fatalf("host RPC runtime params = %#v", params)
		}
		if params["name"] != "demo" || params["anchor"] != "Usage" {
			t.Fatalf("host RPC model params = %#v", params)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for host RPC request")
	}
}

func decodeSkillMCPResponses(t *testing.T, raw []byte) []map[string]any {
	t.Helper()
	dec := json.NewDecoder(bytes.NewReader(raw))
	var out []map[string]any
	for {
		var resp map[string]any
		err := dec.Decode(&resp)
		if err == io.EOF {
			return out
		}
		if err != nil {
			t.Fatalf("decode MCP response stream: %v; raw=%s", err, string(raw))
		}
		out = append(out, resp)
	}
}

func TestSkillHostRPCClient_ValidatesHostResponse(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		response string
		wantErr  string
	}{
		{name: "unknown response field", response: `{"jsonrpc":"2.0","id":1,"result":{},"extra":true}`, wantErr: "unknown field"},
		{name: "invalid jsonrpc", response: `{"jsonrpc":"1.0","id":1,"result":{}}`, wantErr: "invalid host rpc jsonrpc version"},
		{name: "mismatched id", response: `{"jsonrpc":"2.0","id":2,"result":{}}`, wantErr: "host rpc response id = 2, want 1"},
		{name: "host error", response: `{"jsonrpc":"2.0","id":1,"error":{"code":-31002,"message":"approval required","data":{"CallID":"call-1"}}}`, wantErr: "approval required"},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			addr := startSkillHostRPCTestServer(t, tt.response)
			_, err := (skillHostRPCClient{addr: addr}).Call(context.Background(), skillExpandBodyRPCMethod, map[string]any{"name": "demo"})
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("Call() error = %v, want containing %q", err, tt.wantErr)
			}
		})
	}
}

func startSkillHostRPCTestServer(t *testing.T, response string) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen() error = %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		_, _ = conn.Write([]byte(response + "\n"))
	}()
	return ln.Addr().String()
}

func startSkillHostRPCRecordingServer(t *testing.T, requests chan<- skillJSONRPCRequest, response string) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen() error = %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		var req skillJSONRPCRequest
		if err := json.NewDecoder(conn).Decode(&req); err == nil {
			requests <- req
		}
		_, _ = conn.Write([]byte(response + "\n"))
	}()
	return ln.Addr().String()
}
