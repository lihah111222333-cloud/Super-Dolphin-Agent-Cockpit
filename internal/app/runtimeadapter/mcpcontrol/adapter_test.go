package mcpcontroladapter

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/mcpcontrol"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/store/systemlog"
)

func TestProvideMCPControlSystemLogSinkRejectsNilStore(t *testing.T) {
	t.Parallel()

	sink, err := provideMCPControlSystemLogSink(nil)
	if err == nil {
		t.Fatal("provideMCPControlSystemLogSink(nil) error = nil, want fail-fast error")
	}
	if sink != nil {
		t.Fatalf("provideMCPControlSystemLogSink(nil) sink = %#v, want nil", sink)
	}
}

func TestMCPControlSystemLogSinkProjectsFieldsAndPreservesStoreError(t *testing.T) {
	t.Parallel()

	insertErr := errors.New("insert system log failed")
	store := &systemLogStoreStub{insertErr: insertErr}
	sink, err := provideMCPControlSystemLogSink(store)
	if err != nil {
		t.Fatalf("provideMCPControlSystemLogSink() error = %v", err)
	}
	duration := int32(37)
	extra := json.RawMessage(`{"attempt":2}`)
	entry := mcpcontrol.SystemLogEntry{
		Level: "error", Logger: "mcp", Message: "failed", Raw: "raw", Source: "ctl",
		Component: "tool", AgentID: "agent-1", ThreadID: "thread-1", TraceID: "trace-1",
		SpanID: "span-1", ParentSpanID: "parent-1", EventType: "tool.call", ToolName: "read",
		DurationMs: &duration, Extra: extra,
	}

	err = sink.InsertSystemLog(context.Background(), entry)
	if !errors.Is(err, insertErr) {
		t.Fatalf("InsertSystemLog() error = %v, want %v", err, insertErr)
	}
	want := systemlog.InsertParams{
		Level: "error", Logger: "mcp", Message: "failed", Raw: "raw", Source: "ctl",
		Component: "tool", AgentID: "agent-1", ThreadID: "thread-1", TraceID: "trace-1",
		SpanID: "span-1", ParentSpanID: "parent-1", EventType: "tool.call", ToolName: "read",
		DurationMs: &duration, Extra: extra,
	}
	if !reflect.DeepEqual(store.got, want) {
		t.Fatalf("InsertSystemLog() params = %#v, want %#v", store.got, want)
	}
}

type systemLogStoreStub struct {
	systemlog.Store
	got       systemlog.InsertParams
	insertErr error
}

func (s *systemLogStoreStub) Insert(_ context.Context, params systemlog.InsertParams) error {
	s.got = params
	return s.insertErr
}
