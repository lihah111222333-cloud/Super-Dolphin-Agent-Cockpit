package rpc

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/creachadair/jrpc2/handler"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/config"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/observability"
	pkglogger "github.com/lihah111222333-cloud/super-dolphin-agent/pkg/logger"
)

type testRPCTraceRecorder struct{ svc *observability.Service }

func (r testRPCTraceRecorder) Enabled() bool {
	return r.svc != nil && r.svc.Enabled()
}

func (r testRPCTraceRecorder) RecordTrace(ctx context.Context, record TraceRecord) error {
	return r.svc.Record(ctx, observability.TraceEvent{
		Timestamp:    record.Timestamp,
		TraceID:      record.TraceID,
		SpanID:       record.SpanID,
		ParentSpanID: record.ParentSpanID,
		Kind:         record.Kind,
		Phase:        record.Phase,
		Method:       record.Method,
		DurationMS:   record.DurationMS,
		Status:       observability.Status(record.Status),
		Error:        record.Error,
		Code: observability.CodeAnchor{
			File:     record.Code.File,
			Function: record.Code.Function,
			Line:     record.Code.Line,
		},
		Metadata: observability.Metadata(record.Metadata),
	})
}

func assertRPCTraceEvent(t *testing.T, event observability.TraceEvent) {
	t.Helper()
	if event.TraceID != "4bf92f3577b34da6a3ce929d0e0e4736" || event.SpanID == "" || event.SpanID == "00f067aa0ba902b7" || event.ParentSpanID != "00f067aa0ba902b7" {
		t.Fatalf("trace context = (%q,%q,%q), want RPC child of incoming span", event.TraceID, event.SpanID, event.ParentSpanID)
	}
	if event.Method != "thread/start" {
		t.Fatalf("method = %q, want thread/start", event.Method)
	}
	if event.Code.File == "" || event.Code.Function == "" || event.Code.Line == 0 {
		t.Fatalf("code anchor missing: %#v", event.Code)
	}
}

func assertRPCTracePayloadExcludes(t *testing.T, event observability.TraceEvent, forbidden ...string) {
	t.Helper()
	encoded, err := json.Marshal(event)
	if err != nil {
		t.Fatalf("Marshal event: %v", err)
	}
	payload := string(encoded)
	for _, current := range forbidden {
		if strings.Contains(payload, current) {
			t.Fatalf("trace event leaked %q: %s", current, payload)
		}
	}
}

type rpcFailingTraceSink struct{ err error }

func (s rpcFailingTraceSink) Append(context.Context, observability.TraceEvent) error { return s.err }

func TestServerDispatchTraceWriteFailureDoesNotBlockHandler(t *testing.T) {
	cfg, err := observability.ParseConfig(observability.EnvMap{"OBS_TRACING_ENABLED": "true"})
	if err != nil {
		t.Fatalf("ParseConfig: %v", err)
	}
	svc := observability.NewService(cfg, observability.WithSink(rpcFailingTraceSink{err: errors.New("trace sink unavailable")}))
	server := NewServer(Params{Config: &config.Config{RPCAddr: "127.0.0.1:0"}, TraceRecorder: testRPCTraceRecorder{svc}})
	server.Register(handler.Map{"thread/start": StrictHandler(func(ctx context.Context, _ struct{}) (map[string]bool, error) {
		trace, ok := observability.TraceFromContext(ctx)
		if !ok {
			t.Fatal("observability trace context missing")
		}
		if trace.TraceID != "4bf92f3577b34da6a3ce929d0e0e4736" || trace.ParentSpanID != "00f067aa0ba902b7" || trace.SpanID == "" {
			t.Fatalf("observability trace context = %#v", trace)
		}
		return map[string]bool{"ok": true}, nil
	})})

	ctx := pkglogger.WithTraceContext(context.Background(), "4bf92f3577b34da6a3ce929d0e0e4736", "00f067aa0ba902b7", "parent-span")
	if _, err := server.Dispatch(ctx, "thread/start", json.RawMessage(`{}`)); err != nil {
		t.Fatalf("Dispatch() error = %v, want trace sink failure to be best-effort", err)
	}
}

func TestServerDispatchRecordsTraceEventsWithoutRawParamsPreview(t *testing.T) {
	cfg, err := observability.ParseConfig(observability.EnvMap{
		"OBS_TRACING_ENABLED": "true",
	})
	if err != nil {
		t.Fatalf("ParseConfig: %v", err)
	}
	svc := observability.NewService(cfg)
	server := NewServer(Params{
		Config:        &config.Config{RPCAddr: "127.0.0.1:0"},
		TraceRecorder: testRPCTraceRecorder{svc},
	})
	server.Register(handler.Map{"thread/start": StrictHandler(func(_ context.Context, req struct {
		CWD              string `json:"cwd"`
		BaseInstructions string `json:"baseInstructions"`
	}) (map[string]string, error) {
		return map[string]string{"threadId": "thread-1", "cwd": req.CWD}, nil
	})})

	ctx := pkglogger.WithTraceContext(context.Background(), "4bf92f3577b34da6a3ce929d0e0e4736", "00f067aa0ba902b7", "parent-span")
	params := json.RawMessage(`{"cwd":"/tmp/project","baseInstructions":"secret prompt from user","userText":"do not leak"}`)
	if _, err := server.Dispatch(ctx, "thread/start", params); err == nil {
		t.Fatal("Dispatch error = nil, want strict handler failure")
	}

	result := svc.Query(context.Background(), observability.Query{TraceID: "4bf92f3577b34da6a3ce929d0e0e4736"})
	events := result.Events
	if len(events) != 2 {
		t.Fatalf("trace event count = %d, want 2: %#v", len(events), events)
	}
	if events[0].Kind != "backend.rpc.dispatch.start" || events[1].Kind != "backend.rpc.dispatch.failed" {
		t.Fatalf("event kinds = %q, %q; want dispatch start/failed", events[0].Kind, events[1].Kind)
	}
	for _, event := range events {
		assertRPCTraceEvent(t, event)
		assertRPCTracePayloadExcludes(t, event, "secret prompt from user", "do not leak", "params_preview", "ParamsPreview", "rpcParamPreview")
	}
}
