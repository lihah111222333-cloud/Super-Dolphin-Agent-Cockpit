package toolbridge

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
	dto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/mcp"
)

func TestMemoryWriteHostToolRegistry_DisabledToolsHideButRejectStaleCall(t *testing.T) {
	reg := NewMemoryWriteHostToolRegistry(&stubAgentMemoryWriter{}, MemoryWriteHostToolOptions{Enabled: true, ToolsEnabled: false})

	if tools := reg.ListHostTools(); tools != nil {
		t.Fatalf("ListHostTools() = %+v, want nil when tools disabled", tools)
	}
	if !reg.HasTool(ToolNameMemoryWrite) {
		t.Fatalf("HasTool(%q) = false, want true for stale call handling", ToolNameMemoryWrite)
	}
	_, err := reg.CallHostTool(context.Background(), HostToolCall{Name: ToolNameMemoryWrite})
	if contract.AgentMemoryErrorCode(err) != "tools_disabled" {
		t.Fatalf("CallHostTool() error = %v, want tools_disabled", err)
	}
}

func TestMemoryWriteHostToolRegistry_ListSchemaAndCall(t *testing.T) {
	writer := &stubAgentMemoryWriter{}
	reg := NewMemoryWriteHostToolRegistry(writer, MemoryWriteHostToolOptions{Enabled: true, ToolsEnabled: true})
	tools := reg.ListHostTools()
	assertMemoryWriteToolSchema(t, reg, tools)
	result := callMemoryWriteHostTool(t, reg)
	assertHostToolsMemoryWriteRequest(t, writer)
	assertMemoryWriteResult(t, result)
}

func TestMemoryWriteReportsDeleteFailureAsPartial(t *testing.T) {
	writer := &stubAgentMemoryWriter{
		result: contract.AgentMemoryWriteResult{
			Path:           "feedback/daily-report-style.md",
			RequestedScope: contract.MemoryScopeUser,
			ActualTarget:   "private",
			Type:           contract.MemoryTypeFeedback,
		},
		err: contract.NewAgentMemoryError("partial", opaqueMemoryWriteFailure{cause: contract.ErrMemoryOverflowDeleteFailed}),
	}
	reg := NewMemoryWriteHostToolRegistry(writer, MemoryWriteHostToolOptions{Enabled: true, ToolsEnabled: true})
	result := callMemoryWriteHostTool(t, reg)

	payload, ok := result.(map[string]any)
	if !ok {
		t.Fatalf("result type = %T, want map partial payload", result)
	}
	if payload["success"] != false || payload["partial"] != true || payload["degraded"] != true {
		t.Fatalf("partial payload = %#v, want visible unsuccessful partial/degraded result", payload)
	}
	if payload["code"] != "partial" || payload["path"] != "feedback/daily-report-style.md" || payload["actualTarget"] != "private" {
		t.Fatalf("partial payload = %#v, want code/path/target metadata", payload)
	}
	if errText, _ := payload["error"].(string); !strings.Contains(errText, "memory_overflow_delete_failed") {
		t.Fatalf("partial payload error = %#v, want typed delete failure", payload["error"])
	}
}

func TestPartialMemoryWriteErrorMessageUsesContractFailureIdentity(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		cause error
		want  string
	}{
		{cause: contract.ErrMemoryOverflowDeleteFailed, want: "memory_overflow_delete_failed"},
		{cause: contract.ErrMemoryOverflowMergeFailed, want: "memory_overflow_merge_failed"},
		{cause: contract.ErrMemoryIndexUpdateFailed, want: "memory_index_update_failed"},
	} {
		err := contract.NewAgentMemoryError("partial", opaqueMemoryWriteFailure{cause: tc.cause})
		if got := partialMemoryWriteErrorMessage(err); got != tc.want {
			t.Fatalf("partialMemoryWriteErrorMessage() = %q, want %q", got, tc.want)
		}
	}
}

type opaqueMemoryWriteFailure struct {
	cause error
}

func (e opaqueMemoryWriteFailure) Error() string { return "opaque memory write failure" }

func (e opaqueMemoryWriteFailure) Unwrap() error { return e.cause }

func assertMemoryWriteToolSchema(t *testing.T, reg *MemoryWriteHostToolRegistry, tools []dto.MCPTool) {
	t.Helper()
	if len(tools) != 1 {
		t.Fatalf("ListHostTools() len = %d, want 1", len(tools))
	}
	if tools[0].Name != ToolNameMemoryWrite {
		t.Fatalf("tool name = %q, want %q", tools[0].Name, ToolNameMemoryWrite)
	}
	var schema map[string]any
	if err := json.Unmarshal(tools[0].InputSchema, &schema); err != nil {
		t.Fatalf("schema json invalid: %v", err)
	}
	required, _ := schema["required"].([]any)
	if !containsAnyString(required, "description") {
		t.Fatalf("schema required = %#v, want description required", required)
	}
	if !reg.HasTool(ToolNameMemoryWrite) {
		t.Fatalf("HasTool(%q) = false, want true", ToolNameMemoryWrite)
	}
}

func callMemoryWriteHostTool(t *testing.T, reg *MemoryWriteHostToolRegistry) any {
	t.Helper()
	args := mustMarshal(t, map[string]any{
		"name":        "daily-report-style",
		"description": "Report style preference",
		"content":     "Prefer concise daily status.\nWhy: user asked.\nHow to apply: keep reports short.",
		"type":        "feedback",
	})
	result, err := reg.CallHostTool(context.Background(), HostToolCall{
		Name:      ToolNameMemoryWrite,
		Arguments: args,
		AgentID:   "agent-1",
		ThreadID:  "thread-1",
		CWD:       "/repo",
		CallID:    "call-1",
	})
	if err != nil {
		t.Fatalf("CallHostTool() error = %v", err)
	}
	return result
}

func assertHostToolsMemoryWriteRequest(t *testing.T, writer *stubAgentMemoryWriter) {
	t.Helper()
	if writer.calls != 1 {
		t.Fatalf("writer calls = %d, want 1", writer.calls)
	}
	assertMemoryWriteContentRequest(t, writer.last)
	assertMemoryWriteMetadataRequest(t, writer.last)
}

func assertMemoryWriteContentRequest(t *testing.T, req contract.AgentMemoryWriteRequest) {
	t.Helper()
	if req.Name != "daily-report-style" || req.Description != "Report style preference" || req.Type != contract.MemoryTypeFeedback || req.Scope != contract.MemoryScopeUser {
		t.Fatalf("writer content request = %+v", req)
	}
}

func assertMemoryWriteMetadataRequest(t *testing.T, req contract.AgentMemoryWriteRequest) {
	t.Helper()
	if req.AgentID != "agent-1" || req.ThreadID != "thread-1" || req.CWD != "/repo" || req.CallID != "call-1" || req.Source != "agent_tool" {
		t.Fatalf("writer metadata request = %+v", req)
	}
}

func assertMemoryWriteResult(t *testing.T, result any) {
	t.Helper()
	res, ok := result.(contract.AgentMemoryWriteResult)
	if !ok {
		t.Fatalf("result type = %T, want contract.AgentMemoryWriteResult", result)
	}
	if res.ActualTarget != "private" || res.Type != contract.MemoryTypeFeedback {
		t.Fatalf("result = %+v", res)
	}
}

func TestMemoryWriteHostToolRegistry_RejectsPathAndLocalScope(t *testing.T) {
	reg := NewMemoryWriteHostToolRegistry(&stubAgentMemoryWriter{}, MemoryWriteHostToolOptions{Enabled: true, ToolsEnabled: true})
	tests := []struct {
		name string
		args map[string]any
		code string
	}{
		{
			name: "path traversal field",
			args: map[string]any{"name": "x", "description": "d", "content": "c", "type": "feedback", "path": "../../x"},
			code: "invalid_input",
		},
		{
			name: "local scope unsupported",
			args: map[string]any{"name": "x", "description": "d", "content": "c", "type": "feedback", "scope": "local"},
			code: "unsupported_scope",
		},
		{
			name: "feedback project mismatch",
			args: map[string]any{"name": "x", "description": "d", "content": "c", "type": "feedback", "scope": "project"},
			code: "invalid_input",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := reg.CallHostTool(context.Background(), HostToolCall{Name: ToolNameMemoryWrite, Arguments: mustMarshal(t, tt.args)})
			if err == nil {
				t.Fatal("CallHostTool() error = nil, want validation error")
			}
			if code := contract.AgentMemoryErrorCode(err); code != tt.code {
				t.Fatalf("error code = %q, want %q (err=%v)", code, tt.code, err)
			}
		})
	}
}

func TestCompositeHostToolRegistry_HostOrderAndCall(t *testing.T) {
	first := &stubHostToolRegistry{hasToolName: "dup", tools: []dto.MCPTool{{Name: "dup", Description: "first"}}, result: map[string]any{"source": "first"}}
	second := &stubHostToolRegistry{hasToolName: "dup", tools: []dto.MCPTool{{Name: "dup", Description: "second"}}}
	reg := NewCompositeHostToolRegistry(first, second)
	tools := reg.ListHostTools()
	if len(tools) != 1 || tools[0].Description != "first" {
		t.Fatalf("composite tools = %+v, want first duplicate only", tools)
	}
	result, err := reg.CallHostTool(context.Background(), HostToolCall{Name: "dup"})
	if err != nil {
		t.Fatalf("CallHostTool() error = %v", err)
	}
	if first.calls != 1 || second.calls != 0 {
		t.Fatalf("calls first=%d second=%d, want first only", first.calls, second.calls)
	}
	got, _ := result.(map[string]any)
	if got["source"] != "first" {
		t.Fatalf("result = %#v", result)
	}
}

type stubAgentMemoryReader struct {
	calls        int
	last         contract.MemoryReadRequest
	err          error
	enabled      bool
	toolsEnabled bool
}

func (s *stubAgentMemoryReader) ReadAgentMemory(_ context.Context, req contract.MemoryReadRequest) (contract.MemoryReadResult, error) {
	s.calls++
	s.last = req
	if s.err != nil {
		return contract.MemoryReadResult{}, s.err
	}
	return contract.MemoryReadResult{Entry: &contract.MemoryEntry{Name: req.Name, Type: req.Type, Content: "memory content"}, SourcePath: "feedback/read.md", IndexHit: true}, nil
}

func (s *stubAgentMemoryReader) MemoryReadEnabled() bool {
	return s == nil || s.enabled
}

func (s *stubAgentMemoryReader) MemoryReadToolsEnabled() bool {
	return s == nil || s.toolsEnabled
}

func TestMemoryReadHostToolRegistry_ListSchemaAndCall(t *testing.T) {
	reader := &stubAgentMemoryReader{enabled: true, toolsEnabled: true}
	reg := NewMemoryReadHostToolRegistry(reader, MemoryReadHostToolOptions{Enabled: true, ToolsEnabled: true})
	tools := reg.ListHostTools()
	assertMemoryReadToolSchema(t, tools)
	result := callMemoryReadHostTool(t, reg)
	assertMemoryReadRequest(t, reader)
	if _, ok := result.(contract.MemoryReadResult); !ok {
		t.Fatalf("result type = %T, want contract.MemoryReadResult", result)
	}
}

func assertMemoryReadToolSchema(t *testing.T, tools []dto.MCPTool) {
	t.Helper()
	if len(tools) != 1 || tools[0].Name != ToolNameMemoryRead {
		t.Fatalf("ListHostTools() = %+v, want memory_read", tools)
	}
	var schema map[string]any
	if err := json.Unmarshal(tools[0].InputSchema, &schema); err != nil {
		t.Fatalf("schema json error = %v", err)
	}
	properties := schema["properties"].(map[string]any)
	assertMemoryReadSchemaProperties(t, properties)
	assertMemoryReadScopeEnum(t, properties["scope"].(map[string]any))
	if schema["additionalProperties"] != false {
		t.Fatalf("additionalProperties = %#v, want false", schema["additionalProperties"])
	}
}

func assertMemoryReadSchemaProperties(t *testing.T, properties map[string]any) {
	t.Helper()
	for _, key := range []string{"name", "path", "scope", "type"} {
		if _, ok := properties[key]; !ok {
			t.Fatalf("schema properties missing %q: %#v", key, properties)
		}
	}
}

func assertMemoryReadScopeEnum(t *testing.T, scopeSchema map[string]any) {
	t.Helper()
	if containsAnyString(scopeSchema["enum"].([]any), "private") || containsAnyString(scopeSchema["enum"].([]any), "project") || containsAnyString(scopeSchema["enum"].([]any), "local") {
		t.Fatalf("scope enum = %#v, want only public supported scopes", scopeSchema["enum"])
	}
}

func callMemoryReadHostTool(t *testing.T, reg *MemoryReadHostToolRegistry) any {
	t.Helper()
	result, err := reg.CallHostTool(context.Background(), HostToolCall{
		Name:      ToolNameMemoryRead,
		Arguments: mustMarshal(t, map[string]any{"name": "daily-report-style", "scope": "team", "type": "project"}),
		AgentID:   "agent-1",
		ThreadID:  "thread-1",
		CWD:       "/repo",
		CallID:    "call-1",
	})
	if err != nil {
		t.Fatalf("CallHostTool() error = %v", err)
	}
	return result
}

func assertMemoryReadRequest(t *testing.T, reader *stubAgentMemoryReader) {
	t.Helper()
	if reader.calls != 1 || reader.last.Name != "daily-report-style" || reader.last.Scope != contract.MemoryScopeTeam || reader.last.Type != contract.MemoryTypeProject || reader.last.CWD != "/repo" || reader.last.AgentID != "agent-1" {
		t.Fatalf("reader request = %+v calls=%d", reader.last, reader.calls)
	}
}

func TestMemoryReadHostToolRegistry_ListHiddenWhenDisabled(t *testing.T) {
	reader := &stubAgentMemoryReader{}
	cases := []MemoryReadHostToolOptions{{Enabled: false, ToolsEnabled: true}, {Enabled: true, ToolsEnabled: false}}
	for _, opts := range cases {
		reg := NewMemoryReadHostToolRegistry(reader, opts)
		if got := reg.ListHostTools(); len(got) != 0 {
			t.Fatalf("ListHostTools() = %+v, want hidden for opts %+v", got, opts)
		}
		if !reg.HasTool(ToolNameMemoryRead) {
			t.Fatalf("HasTool(%q) = false, want true for stale call handling", ToolNameMemoryRead)
		}
	}
}

func TestMemoryReadHostToolRegistry_StaleCallWhenFeatureDisabled(t *testing.T) {
	reg := NewMemoryReadHostToolRegistry(&stubAgentMemoryReader{}, MemoryReadHostToolOptions{Enabled: false, ToolsEnabled: true})
	_, err := reg.CallHostTool(context.Background(), HostToolCall{Name: ToolNameMemoryRead, Arguments: mustMarshal(t, map[string]any{"name": "x"})})
	if code := contract.AgentMemoryErrorCode(err); code != "feature_disabled" {
		t.Fatalf("error code = %q, want feature_disabled (err=%v)", code, err)
	}
}

func TestMemoryReadHostToolRegistry_StaleCallWhenToolsDisabled(t *testing.T) {
	reg := NewMemoryReadHostToolRegistry(&stubAgentMemoryReader{}, MemoryReadHostToolOptions{Enabled: true, ToolsEnabled: false})
	_, err := reg.CallHostTool(context.Background(), HostToolCall{Name: ToolNameMemoryRead, Arguments: mustMarshal(t, map[string]any{"name": "x"})})
	if code := contract.AgentMemoryErrorCode(err); code != "tools_disabled" {
		t.Fatalf("error code = %q, want tools_disabled (err=%v)", code, err)
	}
}

func TestMemoryReadHostToolRegistry_ReaderUnavailable(t *testing.T) {
	reg := &MemoryReadHostToolRegistry{opts: MemoryReadHostToolOptions{Enabled: true, ToolsEnabled: true}}
	_, err := reg.CallHostTool(context.Background(), HostToolCall{Name: ToolNameMemoryRead, Arguments: mustMarshal(t, map[string]any{"name": "x"})})
	if code := contract.AgentMemoryErrorCode(err); code != "reader_unavailable" {
		t.Fatalf("error code = %q, want reader_unavailable (err=%v)", code, err)
	}
}

func TestMemoryReadHostToolRegistry_InvalidInput(t *testing.T) {
	reg := NewMemoryReadHostToolRegistry(&stubAgentMemoryReader{enabled: true, toolsEnabled: true}, MemoryReadHostToolOptions{Enabled: true, ToolsEnabled: true})
	for _, args := range []json.RawMessage{json.RawMessage(`{"name":"x","scope":"private"}`), json.RawMessage(`{"name":"x","type":"bogus"}`), json.RawMessage(`not-json`)} {
		_, err := reg.CallHostTool(context.Background(), HostToolCall{Name: ToolNameMemoryRead, Arguments: args})
		if code := contract.AgentMemoryErrorCode(err); code != "invalid_input" {
			t.Fatalf("error code = %q, want invalid_input for args %s (err=%v)", code, string(args), err)
		}
	}
}

func TestMemoryReadHostToolRegistry_ReaderError(t *testing.T) {
	reader := &stubAgentMemoryReader{enabled: true, toolsEnabled: true, err: contract.NewAgentMemoryError("not_found", errors.New("missing"))}
	reg := NewMemoryReadHostToolRegistry(reader, MemoryReadHostToolOptions{Enabled: true, ToolsEnabled: true})
	_, err := reg.CallHostTool(context.Background(), HostToolCall{Name: ToolNameMemoryRead, Arguments: mustMarshal(t, map[string]any{"name": "missing"})})
	if code := contract.AgentMemoryErrorCode(err); code != "not_found" {
		t.Fatalf("error code = %q, want not_found (err=%v)", code, err)
	}
}

type stubAgentMemoryWriter struct {
	calls  int
	last   contract.AgentMemoryWriteRequest
	result contract.AgentMemoryWriteResult
	err    error
}

func (s *stubAgentMemoryWriter) WriteAgentMemory(_ context.Context, req contract.AgentMemoryWriteRequest) (contract.AgentMemoryWriteResult, error) {
	s.calls++
	s.last = req
	if s.result.Path == "" && s.result.ActualTarget == "" && s.result.Type == "" {
		s.result = contract.AgentMemoryWriteResult{Path: "feedback/daily-report-style.md", RequestedScope: req.Scope, ActualTarget: "private", Type: req.Type}
	}
	return s.result, s.err
}

func (s *stubAgentMemoryWriter) MemoryWriteEnabled() bool { return true }

func (s *stubAgentMemoryWriter) MemoryWriteToolsEnabled() bool { return true }

func containsAnyString(values []any, want string) bool {
	for _, value := range values {
		if s, ok := value.(string); ok && s == want {
			return true
		}
	}
	return false
}
