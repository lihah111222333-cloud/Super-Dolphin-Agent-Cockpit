package toolbridge

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	"github.com/anthropic-ai/super-agent-v3/internal/dto/mcp"
	providerdto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/mcpcontrol"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/observability"
)

func TestObservabilityTraceHostToolListsOnlyWhenEnabledButHandlesDisabledStaleCall(t *testing.T) {
	disabled := observability.NewDisabledService(observability.Config{DisabledReason: "trace disabled", QueryTailTimeoutMS: 100, QueryTailMaxConcurrency: 1})
	reg := NewObservabilityTraceHostToolRegistry(disabled)

	if tools := reg.ListHostTools(); tools != nil {
		t.Fatalf("ListHostTools() = %+v, want nil for disabled tracing", tools)
	}
	if !reg.HasTool(ToolNameObservabilityTraceGet) {
		t.Fatalf("HasTool(%q) = false, want stale disabled calls handled by host", ToolNameObservabilityTraceGet)
	}
	if reg.RequiresCWD(ToolNameObservabilityTraceGet) {
		t.Fatalf("RequiresCWD(%q) = true, want cwd optional", ToolNameObservabilityTraceGet)
	}

	result, err := reg.CallHostTool(context.Background(), HostToolCall{Name: ToolNameObservabilityTraceGet, Arguments: json.RawMessage(`{"trace_id":"trace-disabled"}`)})
	if err != nil {
		t.Fatalf("CallHostTool disabled: %v", err)
	}
	diagnosis := result.(observability.TraceDiagnosis)
	if !diagnosis.Degraded || diagnosis.TailError != "trace disabled" {
		t.Fatalf("disabled diagnosis = %+v, want explicit degraded result", diagnosis)
	}
}

func TestObservabilityTraceHostToolDisabledStaleCallRoutesThroughHostDispatch(t *testing.T) {
	disabled := observability.NewDisabledService(observability.Config{DisabledReason: "trace disabled", QueryTailTimeoutMS: 100, QueryTailMaxConcurrency: 1})
	h := &Handler{hostTools: NewObservabilityTraceHostToolRegistry(disabled)}

	got, err := h.callHostTool(context.Background(), ToolCallRequest{Name: ToolNameObservabilityTraceGet, Arguments: json.RawMessage(`{"trace_id":"trace-disabled"}`)})
	if err != nil {
		t.Fatalf("callHostTool disabled: %v", err)
	}
	if got == nil || !got.Success {
		t.Fatalf("disabled stale host result = %#v, want successful degraded diagnosis payload", got)
	}
	diagnosis := decodeTraceDiagnosisStructured(t, got.StructuredContent)
	if !diagnosis.Degraded || diagnosis.TailError != "trace disabled" {
		t.Fatalf("structured disabled diagnosis = %+v, want explicit degraded result", diagnosis)
	}
}

func TestObservabilityTraceHostOnlyRouteDoesNotFallBackToPeer(t *testing.T) {
	registry := &stubRegistry{peers: []*mcpcontrol.ToolInstance{{
		Peer: &stubPeer{callbackFn: func(context.Context, string, any, any) error {
			t.Fatal("peer callback called for reserved observability trace tool")
			return nil
		}},
	}}}
	h := &Handler{registry: registry}

	got, err := h.routeToolCall(context.Background(), ToolCallRequest{Name: ToolNameObservabilityTraceGet, Arguments: json.RawMessage(`{"trace_id":"trace"}`)})
	if err != nil {
		t.Fatalf("routeToolCall() error = %v", err)
	}
	if got == nil || got.Success {
		t.Fatalf("routeToolCall() = %#v, want host-only unavailable error", got)
	}
	if len(registry.gotKinds) != 0 {
		t.Fatalf("peer registry was consulted for host-only trace tool: %#v", registry.gotKinds)
	}
	envelope := decodeToolResultEnvelope(t, got)
	if envelope["code"] != "trace_unavailable" {
		t.Fatalf("error envelope = %#v, want trace_unavailable", envelope)
	}
}

func TestObservabilityTraceHostOnlyToolFiltersPeerList(t *testing.T) {
	registry := &stubKindRegistry{peers: map[string][]*mcpcontrol.ToolInstance{
		mcp.ClientKindOrch: {listToolsPeer([]mcp.MCPTool{{Name: ToolNameObservabilityTraceGet, Description: "peer trace"}, {Name: "orchestration_launch_agent", Description: "launch"}}, nil)},
		mcp.ClientKindLSP:  {listToolsPeer([]mcp.MCPTool{{Name: "grep", Description: "grep"}}, nil)},
	}}
	h := &Handler{registry: registry}

	tools, err := h.ListToolsForCodex(context.Background())
	if err != nil {
		t.Fatalf("ListToolsForCodex() error = %v", err)
	}
	for _, tool := range tools {
		if tool.Name == ToolNameObservabilityTraceGet {
			t.Fatalf("peer reserved trace tool leaked into Codex tools: %+v", tools)
		}
	}
}

func TestObservabilityTraceHostOnlyToolFiltersPeerListReservedAliases(t *testing.T) {
	registry := &stubKindRegistry{peers: map[string][]*mcpcontrol.ToolInstance{
		mcp.ClientKindOrch: {listToolsPeer(nil, nil)},
		mcp.ClientKindLSP: {listToolsPeer([]mcp.MCPTool{
			{Name: "mcp__lsp__observability_trace_get", Description: "peer trace alias"},
			{Name: "grep", Description: "grep"},
		}, nil)},
	}}
	h := &Handler{registry: registry}

	tools, err := h.ListToolsForCodex(context.Background())
	if err != nil {
		t.Fatalf("ListToolsForCodex() error = %v", err)
	}
	if countDynamicToolName(tools, "mcp__lsp__observability_trace_get") != 0 {
		t.Fatalf("peer reserved trace alias leaked into Codex tools: %+v", tools)
	}
	if got := countDynamicToolName(tools, "grep"); got != 1 {
		t.Fatalf("grep count = %d, want 1; tools=%+v", got, tools)
	}
}

func TestObservabilityTraceHostOnlyToolFiltersPreparedCodexSurface(t *testing.T) {
	for _, tc := range []struct {
		name      string
		hostTools HostToolRegistry
		wantTrace int
	}{
		{
			name:      "enabled host tool shadows peer duplicate",
			hostTools: NewObservabilityTraceHostToolRegistry(observability.NewService(observability.Config{QueryTailTimeoutMS: 100, QueryTailMaxConcurrency: 1})),
			wantTrace: 1,
		},
		{
			name:      "disabled host tool still reserves peer name",
			hostTools: NewObservabilityTraceHostToolRegistry(observability.NewDisabledService(observability.Config{DisabledReason: "trace disabled", QueryTailTimeoutMS: 100, QueryTailMaxConcurrency: 1})),
			wantTrace: 0,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			lsp := &fakeMCPClient{tools: []mcp.MCPTool{
				{Name: ToolNameObservabilityTraceGet, Description: "peer trace", InputSchema: json.RawMessage(`{"type":"object"}`)},
				{Name: "grep", Description: "grep", InputSchema: json.RawMessage(`{"type":"object"}`)},
			}}
			h := &Handler{
				hostTools:          tc.hostTools,
				stdioClientFactory: fakeClientFactory(map[string]mcpClient{"lsp": lsp}),
			}

			tools, err := h.PrepareCodexToolSurface(context.Background(), contract.CodexToolSurfaceScope{
				AgentID: "agent-1",
				CWD:     "/repo",
				Manifest: providerdto.MCPManifest{Binaries: []providerdto.MCPBinary{{
					Name:    "lsp",
					Command: []string{"mcp-lsp"},
				}}},
			})
			if err != nil {
				t.Fatalf("PrepareCodexToolSurface() error = %v", err)
			}
			if got := countDynamicToolName(tools, ToolNameObservabilityTraceGet); got != tc.wantTrace {
				t.Fatalf("prepared trace tool count = %d, want %d; tools=%+v", got, tc.wantTrace, tools)
			}
			if got := countDynamicToolName(tools, "grep"); got != 1 {
				t.Fatalf("prepared grep tool count = %d, want 1; tools=%+v", got, tools)
			}
		})
	}
}

func TestObservabilityTraceDisabledStaleHandleToolCallBypassesPreparedSurface(t *testing.T) {
	disabled := observability.NewDisabledService(observability.Config{DisabledReason: "trace disabled", QueryTailTimeoutMS: 100, QueryTailMaxConcurrency: 1})
	lsp := &fakeMCPClient{tools: []mcp.MCPTool{
		{Name: ToolNameObservabilityTraceGet, Description: "peer trace", InputSchema: json.RawMessage(`{"type":"object"}`)},
		{Name: "grep", Description: "grep", InputSchema: json.RawMessage(`{"type":"object"}`)},
	}}
	h := &Handler{
		hostTools:          NewObservabilityTraceHostToolRegistry(disabled),
		stdioClientFactory: fakeClientFactory(map[string]mcpClient{"lsp": lsp}),
	}

	tools, err := h.PrepareCodexToolSurface(context.Background(), contract.CodexToolSurfaceScope{
		AgentID:          "agent-1",
		ProviderThreadID: "provider-thread-1",
		CWD:              "/repo",
		Manifest: providerdto.MCPManifest{Binaries: []providerdto.MCPBinary{{
			Name:    "lsp",
			Command: []string{"mcp-lsp"},
		}}},
	})
	if err != nil {
		t.Fatalf("PrepareCodexToolSurface() error = %v", err)
	}
	if got := countDynamicToolName(tools, ToolNameObservabilityTraceGet); got != 0 {
		t.Fatalf("prepared trace tool count = %d, want 0 for disabled tracing; tools=%+v", got, tools)
	}

	result, err := h.HandleToolCall(context.Background(), contract.ToolCallRawMessage{Params: json.RawMessage(`{"name":"observability_trace_get","arguments":{"trace_id":"trace-disabled"},"_agentId":"agent-1","_threadId":"provider-thread-1","_callId":"call-1","_cwd":"/repo"}`)})
	if err != nil {
		t.Fatalf("HandleToolCall() error = %v", err)
	}
	got, ok := result.(*ToolCallResult)
	if !ok {
		t.Fatalf("HandleToolCall() result = %T, want *ToolCallResult", result)
	}
	diagnosis := decodeTraceDiagnosisStructured(t, got.StructuredContent)
	if !diagnosis.Degraded || diagnosis.TailError != "trace disabled" {
		t.Fatalf("structured disabled diagnosis = %+v, want explicit degraded result", diagnosis)
	}
	if lsp.calledWith(ToolNameObservabilityTraceGet) {
		t.Fatalf("peer trace tool was called through prepared surface: calls=%#v", lsp.calls)
	}
}

func TestObservabilityTraceHostOnlyToolFiltersPreparedCodexSurfaceReservedAliases(t *testing.T) {
	disabled := observability.NewDisabledService(observability.Config{DisabledReason: "trace disabled", QueryTailTimeoutMS: 100, QueryTailMaxConcurrency: 1})
	lsp := &fakeMCPClient{tools: []mcp.MCPTool{
		{Name: "mcp__lsp__observability_trace_get", Description: "peer trace alias", InputSchema: json.RawMessage(`{"type":"object"}`)},
		{Name: "grep", Description: "grep", InputSchema: json.RawMessage(`{"type":"object"}`)},
	}}
	h := &Handler{
		hostTools:          NewObservabilityTraceHostToolRegistry(disabled),
		stdioClientFactory: fakeClientFactory(map[string]mcpClient{"lsp": lsp}),
	}

	tools, err := h.PrepareCodexToolSurface(context.Background(), contract.CodexToolSurfaceScope{
		AgentID: "agent-1",
		CWD:     "/repo",
		Manifest: providerdto.MCPManifest{Binaries: []providerdto.MCPBinary{{
			Name:    "lsp",
			Command: []string{"mcp-lsp"},
		}}},
	})
	if err != nil {
		t.Fatalf("PrepareCodexToolSurface() error = %v", err)
	}
	if countDynamicToolName(tools, "mcp__lsp__observability_trace_get") != 0 {
		t.Fatalf("reserved wrapped trace alias leaked into prepared surface: tools=%+v", tools)
	}
	if got := countDynamicToolName(tools, "grep"); got != 1 {
		t.Fatalf("prepared grep tool count = %d, want 1; tools=%+v", got, tools)
	}
}

func TestObservabilityTraceDisabledStaleAliasHandleToolCallBypassesPreparedSurface(t *testing.T) {
	disabled := observability.NewDisabledService(observability.Config{DisabledReason: "trace disabled", QueryTailTimeoutMS: 100, QueryTailMaxConcurrency: 1})
	lsp := &fakeMCPClient{tools: []mcp.MCPTool{{Name: "grep", Description: "grep", InputSchema: json.RawMessage(`{"type":"object"}`)}}}
	h := &Handler{
		hostTools:          NewObservabilityTraceHostToolRegistry(disabled),
		stdioClientFactory: fakeClientFactory(map[string]mcpClient{"lsp": lsp}),
	}

	_, err := h.PrepareCodexToolSurface(context.Background(), contract.CodexToolSurfaceScope{
		AgentID:          "agent-1",
		ProviderThreadID: "provider-thread-1",
		CWD:              "/repo",
		Manifest: providerdto.MCPManifest{Binaries: []providerdto.MCPBinary{{
			Name:    "lsp",
			Command: []string{"mcp-lsp"},
		}}},
	})
	if err != nil {
		t.Fatalf("PrepareCodexToolSurface() error = %v", err)
	}

	result, err := h.HandleToolCall(context.Background(), contract.ToolCallRawMessage{Params: json.RawMessage(`{"name":"mcp__lsp__observability_trace_get","arguments":{"trace_id":"trace-disabled"},"_agentId":"agent-1","_threadId":"provider-thread-1","_callId":"call-1","_cwd":"/repo"}`)})
	if err != nil {
		t.Fatalf("HandleToolCall() error = %v", err)
	}
	got, ok := result.(*ToolCallResult)
	if !ok {
		t.Fatalf("HandleToolCall() result = %T, want *ToolCallResult", result)
	}
	diagnosis := decodeTraceDiagnosisStructured(t, got.StructuredContent)
	if !diagnosis.Degraded || diagnosis.TailError != "trace disabled" {
		t.Fatalf("structured disabled diagnosis = %+v, want explicit degraded result", diagnosis)
	}
	if len(lsp.calls) != 0 {
		t.Fatalf("peer tools were called for reserved trace alias: calls=%#v", lsp.calls)
	}
}

func TestObservabilityTraceHostToolEnabledSchemaAndCall(t *testing.T) {
	svc := observability.NewService(observability.Config{IndexMaxEvents: 4, IndexMaxTraceEvents: 4, IndexMaxThreadEvents: 4, QueryTailTimeoutMS: 100, QueryTailMaxConcurrency: 1}, observability.WithSampler(observability.NewSampler(observability.SamplerConfig{HighFrequencyKeepEvery: 1})))
	if err := svc.Record(context.Background(), observability.TraceEvent{TraceID: "trace-tool", Method: "GET /tool", Status: observability.StatusOK, Code: observability.CodeAnchor{File: "/repo/internal/app.go", Function: "handler", Line: 1}}); err != nil {
		t.Fatalf("Record: %v", err)
	}
	reg := NewObservabilityTraceHostToolRegistry(svc)

	tool := requireTraceHostTool(t, reg)
	assertTraceHostToolSchema(t, tool.InputSchema)
	result, err := reg.CallHostTool(context.Background(), HostToolCall{Name: ToolNameObservabilityTraceGet, CWD: "/repo", Arguments: json.RawMessage(`{"trace_id":"trace-tool","include_stack":true,"force_refresh":false,"limit":1}`)})
	if err != nil {
		t.Fatalf("CallHostTool: %v", err)
	}
	diagnosis := result.(observability.TraceDiagnosis)
	if diagnosis.Source != observability.TraceDiagnosisSourceMemory || diagnosis.Timeline[0].Code.File != "internal/app.go" {
		t.Fatalf("diagnosis = %+v, want memory diagnosis with repo-relative anchor", diagnosis)
	}
}

func decodeTraceDiagnosisStructured(t *testing.T, raw json.RawMessage) observability.TraceDiagnosis {
	t.Helper()
	if len(raw) == 0 {
		t.Fatal("StructuredContent is empty")
	}
	var diagnosis observability.TraceDiagnosis
	if err := json.Unmarshal(raw, &diagnosis); err != nil {
		t.Fatalf("StructuredContent = %s, want TraceDiagnosis: %v", raw, err)
	}
	return diagnosis
}

func TestObservabilityTraceHostToolRejectsInvalidInput(t *testing.T) {
	reg := NewObservabilityTraceHostToolRegistry(observability.NewService(observability.Config{QueryTailTimeoutMS: 100, QueryTailMaxConcurrency: 1}))
	for _, tc := range []struct {
		name string
		args json.RawMessage
	}{
		{name: "missing trace", args: json.RawMessage(`{}`)},
		{name: "unknown field", args: json.RawMessage(`{"trace_id":"trace","extra":true}`)},
		{name: "limit too large", args: json.RawMessage(`{"trace_id":"trace","limit":201}`)},
		{name: "bad boolean", args: json.RawMessage(`{"trace_id":"trace","force_refresh":"yes"}`)},
		{name: "trailing JSON", args: json.RawMessage(`{"trace_id":"trace"} {}`)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := reg.CallHostTool(context.Background(), HostToolCall{Name: ToolNameObservabilityTraceGet, Arguments: tc.args})
			if err == nil || !strings.Contains(err.Error(), "invalid") {
				t.Fatalf("CallHostTool() error = %v, want invalid input", err)
			}
		})
	}
}

func countDynamicToolName(tools []contract.DynamicToolSchema, name string) int {
	count := 0
	for _, tool := range tools {
		if tool.Name == name {
			count++
		}
	}
	return count
}

func requireTraceHostTool(t *testing.T, reg *ObservabilityTraceHostToolRegistry) mcpToolView {
	t.Helper()
	tools := reg.ListHostTools()
	if len(tools) != 1 || tools[0].Name != ToolNameObservabilityTraceGet {
		t.Fatalf("ListHostTools() = %+v, want observability trace tool", tools)
	}
	return mcpToolView{Name: tools[0].Name, InputSchema: tools[0].InputSchema}
}

type mcpToolView struct {
	Name        string
	InputSchema json.RawMessage
}

func assertTraceHostToolSchema(t *testing.T, schema json.RawMessage) {
	t.Helper()
	var decoded map[string]any
	if err := json.Unmarshal(schema, &decoded); err != nil {
		t.Fatalf("InputSchema = %s, invalid JSON: %v", schema, err)
	}
	required, ok := decoded["required"].([]any)
	if !ok || len(required) != 1 || required[0] != "trace_id" {
		t.Fatalf("required = %#v, want trace_id only", decoded["required"])
	}
	if decoded["additionalProperties"] != false {
		t.Fatalf("additionalProperties = %#v, want false", decoded["additionalProperties"])
	}
	properties, ok := decoded["properties"].(map[string]any)
	if !ok {
		t.Fatalf("properties = %#v, want object", decoded["properties"])
	}
	limit, ok := properties["limit"].(map[string]any)
	if !ok {
		t.Fatalf("limit schema = %#v, want object", properties["limit"])
	}
	if limit["maximum"] != float64(observability.TraceDiagnosisMaxLimit) {
		t.Fatalf("limit maximum = %#v, want %d", limit["maximum"], observability.TraceDiagnosisMaxLimit)
	}
}
