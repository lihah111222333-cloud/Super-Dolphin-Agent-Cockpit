package mcpcontrol

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/mcp"
	platformdb "github.com/anthropic-ai/super-agent-v3/internal/platform/db"
	"github.com/creachadair/jrpc2"
)

type stubAgentContextSource struct {
	snapshot *contract.AgentSnapshot
	err      error
	gotID    string
}

type captureSystemLogSink struct {
	entry SystemLogEntry
	err   error
	calls int
}

func (s *captureSystemLogSink) InsertSystemLog(_ context.Context, entry SystemLogEntry) error {
	s.calls++
	s.entry = entry
	return s.err
}

func (s *stubAgentContextSource) GetAgentSnapshot(agentID string) (*contract.AgentSnapshot, error) {
	s.gotID = agentID
	if s.err != nil {
		return nil, s.err
	}
	if s.snapshot == nil {
		return nil, errors.New("missing snapshot")
	}
	cloned := *s.snapshot
	return &cloned, nil
}

func TestRegistryContextProvider_UsesRequestedAgentSnapshotForRuntimeScope(t *testing.T) {
	source := &stubAgentContextSource{
		snapshot: &contract.AgentSnapshot{
			ID:       "agent-42",
			ThreadID: "thread-42",
			PID:      4242,
			State:    "running",
		},
	}
	resp, err := (registryContextProvider{agents: source}).GetContext(context.Background(), &ToolInstance{
		AgentID:    "shared",
		BinaryName: "mcp-orch",
		ClientKind: "orch",
		PeerKind:   dto.PeerKindTool,
		PID:        99,
		Status:     dto.StatusActive,
	}, dto.ContextRequest{
		AgentID: "agent-42",
		Scope:   dto.ScopeAgentRuntime,
	})
	if err != nil {
		t.Fatalf("GetContext() error = %v", err)
	}

	var payload map[string]any
	if err := json.Unmarshal(resp.Payload, &payload); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	if got := payload["agent_id"]; got != "agent-42" {
		t.Fatalf("payload.agent_id = %#v, want agent-42", got)
	}
	if got := payload["pid"]; got != float64(4242) {
		t.Fatalf("payload.pid = %#v, want 4242", got)
	}
	if got := payload["status"]; got != "running" {
		t.Fatalf("payload.status = %#v, want running", got)
	}
	if source.gotID != "agent-42" {
		t.Fatalf("GetAgentSnapshot() agent_id = %q, want agent-42", source.gotID)
	}
}

func TestRegistryContextProvider_UsesLeaseScopedAgentIDWhenHintMissing(t *testing.T) {
	resp, err := (registryContextProvider{}).GetContext(context.Background(), &ToolInstance{
		AgentID:    "lease-agent",
		BinaryName: "mcp-orch",
		ClientKind: "orch",
		PeerKind:   dto.PeerKindTool,
		PID:        99,
		Status:     dto.StatusActive,
	}, dto.ContextRequest{
		Scope: dto.ScopeAgentRuntime,
	})
	if err != nil {
		t.Fatalf("GetContext() error = %v", err)
	}

	var payload map[string]any
	if err := json.Unmarshal(resp.Payload, &payload); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	if got := payload["agent_id"]; got != "lease-agent" {
		t.Fatalf("payload.agent_id = %#v, want lease-agent", got)
	}
}

func TestRegistryContextProvider_UsesRequestedAgentSnapshotForThreadBinding(t *testing.T) {
	resp, err := (registryContextProvider{agents: &stubAgentContextSource{
		snapshot: &contract.AgentSnapshot{
			ID:       "agent-42",
			ThreadID: "thread-42",
			State:    "running",
		},
	}}).GetContext(context.Background(), &ToolInstance{
		AgentID:  "shared",
		ThreadID: "thread-shared",
		Lease: dto.LeaseKey{
			InstanceID: "instance-1",
			Generation: 7,
		},
	}, dto.ContextRequest{
		AgentID: "agent-42",
		Scope:   dto.ScopeThreadBinding,
	})
	if err != nil {
		t.Fatalf("GetContext() error = %v", err)
	}

	var payload map[string]any
	if err := json.Unmarshal(resp.Payload, &payload); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	if got := payload["agent_id"]; got != "agent-42" {
		t.Fatalf("payload.agent_id = %#v, want agent-42", got)
	}
	if got := payload["thread_id"]; got != "thread-42" {
		t.Fatalf("payload.thread_id = %#v, want thread-42", got)
	}
	if got := payload["instance_id"]; got != "instance-1" {
		t.Fatalf("payload.instance_id = %#v, want instance-1", got)
	}
}

func TestRegistryContextProvider_ReturnsAgentNotFoundWhenSourceMissing(t *testing.T) {
	_, err := (registryContextProvider{}).GetContext(context.Background(), &ToolInstance{
		AgentID: "shared",
	}, dto.ContextRequest{
		AgentID: "agent-42",
		Scope:   dto.ScopeAgentRuntime,
	})
	if err == nil || !strings.Contains(err.Error(), "agent not found") {
		t.Fatalf("GetContext() error = %v, want agent not found", err)
	}
}

func TestDefaultLogSinkRedactsPeerFields(t *testing.T) {
	var buf bytes.Buffer
	systemLogs := &captureSystemLogSink{}
	sink := defaultLogSink{
		logger:     slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})),
		systemLogs: systemLogs,
	}

	err := sink.HandleLog(context.Background(), &ToolInstance{
		Lease:      dto.LeaseKey{InstanceID: "peer-1", Generation: 2},
		BinaryName: "mcp-lsp",
		ClientKind: "lsp",
	}, dto.LogNotify{
		Level:   "INFO",
		Message: "peer emitted diagnostic",
		Fields: map[string]any{
			"token":  "sk-abcdefghijklmnopqrstuvwxyz",
			"detail": "plain diagnostic",
		},
	})
	if err != nil {
		t.Fatalf("HandleLog() error = %v", err)
	}
	raw := buf.String()
	if strings.Contains(raw, "sk-") {
		t.Fatalf("mcp control log leaked secret: %s", raw)
	}
	if !strings.Contains(raw, `"token":"[REDACTED]"`) || !strings.Contains(raw, "plain diagnostic") {
		t.Fatalf("mcp control log = %s, want redacted token and safe detail", raw)
	}
	if systemLogs.calls != 1 {
		t.Fatalf("system log calls = %d, want 1", systemLogs.calls)
	}
	if extra := string(systemLogs.entry.Extra); strings.Contains(extra, "sk-") || !strings.Contains(extra, `"token":"[REDACTED]"`) {
		t.Fatalf("system log extra = %s, want redacted token", extra)
	}
}

func TestDefaultLogSinkRequiresSystemLogSink(t *testing.T) {
	t.Parallel()

	err := (defaultLogSink{
		logger: slog.New(slog.NewJSONHandler(&bytes.Buffer{}, &slog.HandlerOptions{Level: slog.LevelDebug})),
	}).HandleLog(context.Background(), &ToolInstance{}, dto.LogNotify{
		Level:   "INFO",
		Message: "peer emitted diagnostic",
	})
	if err == nil || !strings.Contains(err.Error(), "mcp log sink is not configured") {
		t.Fatalf("HandleLog() error = %v, want missing system log sink", err)
	}
	var rpcErr *jrpc2.Error
	if !errors.As(err, &rpcErr) {
		t.Fatalf("HandleLog() error = %T, want *jrpc2.Error", err)
	}
	if got := int(rpcErr.Code); got != dto.ErrCodeInternal {
		t.Fatalf("HandleLog() code = %d, want %d", got, dto.ErrCodeInternal)
	}
}

func TestDefaultLogSinkPersistsSystemLogTraceFields(t *testing.T) {
	t.Parallel()

	systemLogs := &captureSystemLogSink{}
	sink := defaultLogSink{
		logger:     slog.New(slog.NewJSONHandler(&bytes.Buffer{}, &slog.HandlerOptions{Level: slog.LevelDebug})),
		systemLogs: systemLogs,
	}
	err := sink.HandleLog(context.Background(), &ToolInstance{
		Lease:      dto.LeaseKey{InstanceID: "peer-1", Generation: 2},
		BinaryName: "mcp-lsp",
		ClientKind: "lsp",
		AgentID:    "agent-1",
		ThreadID:   "thread-1",
		PID:        1234,
	}, dto.LogNotify{
		Level:   "WARN",
		Message: "peer emitted diagnostic",
		Seq:     7,
		TS:      1_700_000_000_000,
		Fields: map[string]any{
			"trace_id":       "trace-1",
			"span_id":        "span-1",
			"parent_span_id": "parent-1",
			"duration_ms":    float64(42),
			"tool_name":      "definition",
			"token":          "sk-abcdefghijklmnopqrstuvwxyz",
		},
	})
	if err != nil {
		t.Fatalf("HandleLog() error = %v", err)
	}
	if systemLogs.calls != 1 {
		t.Fatalf("system log calls = %d, want 1", systemLogs.calls)
	}
	entry := systemLogs.entry
	assertMCPControlSystemLogEntry(t, entry)
	if entry.DurationMs == nil || *entry.DurationMs != 42 {
		t.Fatalf("system log duration = %v, want 42", entry.DurationMs)
	}
	assertMCPControlSystemLogExtra(t, entry)
}

func assertMCPControlSystemLogEntry(t *testing.T, entry SystemLogEntry) {
	t.Helper()
	for _, check := range []struct{ name, got, want string }{
		{name: "level", got: entry.Level, want: "warn"},
		{name: "logger", got: entry.Logger, want: "mcp-control"},
		{name: "source", got: entry.Source, want: "mcp-control"},
		{name: "component", got: entry.Component, want: "mcp-lsp"},
		{name: "agent", got: entry.AgentID, want: "agent-1"},
		{name: "thread", got: entry.ThreadID, want: "thread-1"},
		{name: "trace", got: entry.TraceID, want: "trace-1"},
		{name: "span", got: entry.SpanID, want: "span-1"},
		{name: "parent span", got: entry.ParentSpanID, want: "parent-1"},
		{name: "event", got: entry.EventType, want: dto.MethodLog},
		{name: "tool", got: entry.ToolName, want: "definition"},
	} {
		if check.got != check.want {
			t.Fatalf("system log %s = %q, want %q; entry=%+v", check.name, check.got, check.want, entry)
		}
	}
}

func assertMCPControlSystemLogExtra(t *testing.T, entry SystemLogEntry) {
	t.Helper()
	var extra map[string]any
	if err := json.Unmarshal(entry.Extra, &extra); err != nil {
		t.Fatalf("json.Unmarshal(extra) error = %v", err)
	}
	fields, ok := extra["fields"].(map[string]any)
	if !ok {
		t.Fatalf("extra.fields = %#v, want object", extra["fields"])
	}
	if fields["span_id"] != "span-1" || fields["parent_span_id"] != "parent-1" {
		t.Fatalf("extra.fields trace spans = %#v", fields)
	}
	if fields["token"] != "[REDACTED]" || strings.Contains(string(entry.Extra), "sk-") || strings.Contains(entry.Raw, "sk-") {
		t.Fatalf("system log extra leaked secret or missed redaction: raw=%s extra=%s", entry.Raw, entry.Extra)
	}
}

func TestDefaultLogSinkRejectsInvalidStructuredFields(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		req  dto.LogNotify
		want string
	}{
		{
			name: "unknown level",
			req:  dto.LogNotify{Level: "NOTICE", Message: "bad level"},
			want: "level",
		},
		{
			name: "fractional duration",
			req: dto.LogNotify{
				Level:   "INFO",
				Message: "bad duration",
				Fields:  map[string]any{"duration_ms": 1.5},
			},
			want: "duration_ms",
		},
		{
			name: "traceparent mismatch",
			req: dto.LogNotify{
				Level:   "INFO",
				Message: "bad trace",
				Fields: map[string]any{
					"trace_id":    "11111111111111111111111111111111",
					"traceparent": "00-22222222222222222222222222222222-3333333333333333-01",
				},
			},
			want: "trace_id does not match traceparent",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := (defaultLogSink{
				logger:     slog.New(slog.NewJSONHandler(&bytes.Buffer{}, nil)),
				systemLogs: &captureSystemLogSink{},
			}).HandleLog(context.Background(), &ToolInstance{}, tc.req)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("HandleLog() error = %v, want containing %q", err, tc.want)
			}
			var rpcErr *jrpc2.Error
			if !errors.As(err, &rpcErr) {
				t.Fatalf("HandleLog() error = %T, want *jrpc2.Error", err)
			}
			if got := int(rpcErr.Code); got != dto.ErrCodeInvalidParams {
				t.Fatalf("HandleLog() code = %d, want %d", got, dto.ErrCodeInvalidParams)
			}
		})
	}
}

func TestDefaultLogSinkDerivesTraceFieldsFromTraceparent(t *testing.T) {
	t.Parallel()

	systemLogs := &captureSystemLogSink{}
	err := (defaultLogSink{
		logger:     slog.New(slog.NewJSONHandler(&bytes.Buffer{}, nil)),
		systemLogs: systemLogs,
	}).HandleLog(context.Background(), &ToolInstance{}, dto.LogNotify{
		Level:   "INFO",
		Message: "traceparent log",
		Fields: map[string]any{
			"traceparent": "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01",
		},
	})
	if err != nil {
		t.Fatalf("HandleLog() error = %v", err)
	}
	if systemLogs.entry.TraceID != "4bf92f3577b34da6a3ce929d0e0e4736" || systemLogs.entry.SpanID != "00f067aa0ba902b7" {
		t.Fatalf("trace fields = trace:%q span:%q", systemLogs.entry.TraceID, systemLogs.entry.SpanID)
	}
}

func TestValidateHookSubscribeRequest_ReturnsInvalidParams(t *testing.T) {
	err := validateHookSubscribeRequest(dto.HookSubscribeRequest{})
	var rpcErr *jrpc2.Error
	if !errors.As(err, &rpcErr) {
		t.Fatalf("validateHookSubscribeRequest() error = %T, want *jrpc2.Error", err)
	}
	if got := int(rpcErr.Code); got != dto.ErrCodeInvalidParams {
		t.Fatalf("validateHookSubscribeRequest() code = %d, want %d", got, dto.ErrCodeInvalidParams)
	}

}

func TestValidateHookResolveRequest_ReturnsInvalidParams(t *testing.T) {
	err := validateHookResolveRequest(dto.HookResolveRequest{})
	var rpcErr *jrpc2.Error
	if !errors.As(err, &rpcErr) {
		t.Fatalf("validateHookResolveRequest() error = %T, want *jrpc2.Error", err)
	}
	if got := int(rpcErr.Code); got != dto.ErrCodeInvalidParams {
		t.Fatalf("validateHookResolveRequest() code = %d, want %d", got, dto.ErrCodeInvalidParams)
	}

}

func TestMapHookHandlerError_StoreErrorReturnsInternal(t *testing.T) {
	err := mapHookHandlerError("resolve", platformdb.WrapStoreError(errors.New("boom"), "save", "hook_pending_review"))
	var rpcErr *jrpc2.Error
	if !errors.As(err, &rpcErr) {
		t.Fatalf("mapHookHandlerError() error = %T, want *jrpc2.Error", err)
	}
	if got := int(rpcErr.Code); got != dto.ErrCodeInternal {
		t.Fatalf("mapHookHandlerError() code = %d, want %d", got, dto.ErrCodeInternal)
	}
}
