package observability

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"go.uber.org/fx"
	"go.uber.org/fx/fxtest"

	platformconfig "github.com/anthropic-ai/super-agent-v3/internal/platform/config"
	platformobs "github.com/anthropic-ai/super-agent-v3/internal/platform/observability"
	platformrpc "github.com/anthropic-ai/super-agent-v3/internal/platform/rpc"
	"github.com/creachadair/jrpc2/handler"
)

type recordingSink struct{ events []platformobs.TraceEvent }

func (s *recordingSink) Append(_ context.Context, event platformobs.TraceEvent) error {
	s.events = append(s.events, event)
	return nil
}

func TestNewHandlersReturnsRPCHandlerMapResult(t *testing.T) {
	svc := platformobs.NewDisabledService(platformobs.Config{DisabledReason: "unit"})
	result := NewHandlers(svc)
	for _, method := range []string{"observability/trace/get", "observability/thread/recent", "observability/slow/list", "observability/error/list", "observability/status", "observability/frontend/ingest"} {
		if _, ok := result.Handlers[method]; !ok {
			t.Fatalf("%s not registered", method)
		}
	}
}

func TestModuleRegistersHandlersThroughRPCGroup(t *testing.T) {
	var maps []handler.Map
	app := fxtest.New(t,
		Module,
		fx.Supply(platformobs.NewDisabledService(platformobs.Config{DisabledReason: "unit"})),
		fx.Populate(fx.Annotate(&maps, fx.ParamTags(`group:"rpc_handlers"`))),
	)
	app.RequireStart().RequireStop()
	if len(maps) != 1 {
		t.Fatalf("handler maps = %d, want 1", len(maps))
	}
	if _, ok := maps[0]["observability/status"]; !ok {
		t.Fatalf("observability/status missing from rpc_handlers group")
	}
}

func TestStatusRPCReportsDisabledService(t *testing.T) {
	server := newTestRPCServer(t, platformobs.NewDisabledService(platformobs.Config{DisabledReason: "unit disabled"}))
	raw, err := server.Dispatch(t.Context(), "observability/status", json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("Dispatch status: %v", err)
	}
	var got platformobs.ServiceStatus
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("unmarshal status: %v", err)
	}
	if got.Enabled || got.DisabledReason != "unit disabled" {
		t.Fatalf("status = %+v", got)
	}
}

func TestTraceGetRPCSummarizesSlowErrorsAndCodeAnchors(t *testing.T) {
	svc := newRecordingService(&recordingSink{})
	seedTraceEvents(t, svc)
	server := newTestRPCServer(t, svc)
	got := dispatchQuery(t, server, "observability/trace/get", json.RawMessage(`{"traceId":"trace-1","limit":10,"includeTail":false}`))
	if got.Source != platformobs.QuerySourceMemory || len(got.Events) != 3 || got.TotalDurationMS <= 0 {
		t.Fatalf("trace response = %+v", got)
	}
	if got.SlowestEvents[0].Method != "rpc.dispatch" || got.Errors[0].Method != "tool.call.end" {
		t.Fatalf("summary slow=%+v errors=%+v", got.SlowestEvents, got.Errors)
	}
	if got.Events[1].Code.File == "" || len(got.Events[2].Stack) != 1 {
		t.Fatalf("missing code anchor or compact stack: %+v", got.Events)
	}
}

func TestTraceGetMissingTraceReturnsEmptyNonErrorResult(t *testing.T) {
	server := newTestRPCServer(t, newRecordingService(&recordingSink{}))
	got := dispatchQuery(t, server, "observability/trace/get", json.RawMessage(`{"trace_id":"missing","include_tail":false}`))
	if got.Source != platformobs.QuerySourceMemory || len(got.Events) != 0 || got.Truncated {
		t.Fatalf("missing trace response = %+v", got)
	}
}

func TestThreadRecentAndListRPCsUseBoundedQueries(t *testing.T) {
	svc := newRecordingService(&recordingSink{})
	seedTraceEvents(t, svc)
	server := newTestRPCServer(t, svc)
	thread := dispatchQuery(t, server, "observability/thread/recent", json.RawMessage(`{"threadId":"thread-1","limit":2,"includeTail":false}`))
	slow := dispatchQuery(t, server, "observability/slow/list", json.RawMessage(`{"limit":5,"component":"rpc"}`))
	errs := dispatchQuery(t, server, "observability/error/list", json.RawMessage(`{"limit":5,"component":"tool"}`))
	if len(thread.Events) != 2 || len(slow.Events) != 1 || len(errs.Events) != 1 {
		t.Fatalf("thread=%+v slow=%+v errors=%+v", thread, slow, errs)
	}
}

func TestTraceGetRPCReportsTailSourceAndTruncation(t *testing.T) {
	tail := platformobs.QueryTailReaderFunc(func(context.Context, platformobs.Query) (platformobs.QueryResult, error) {
		return platformobs.QueryResult{Source: platformobs.QuerySourceJSONLTail, Events: []platformobs.TraceEvent{{TraceID: "tail-trace", Method: "tail"}}, Truncated: true}, nil
	})
	svc := platformobs.NewService(testTraceConfig(), platformobs.WithTailReader(tail))
	server := newTestRPCServer(t, svc)
	got := dispatchQuery(t, server, "observability/trace/get", json.RawMessage(`{"traceId":"tail-trace"}`))
	if got.Source != platformobs.QuerySourceJSONLTail || !got.Truncated || len(got.Events) != 1 {
		t.Fatalf("tail response = %+v", got)
	}
}

func TestFrontendIngestSanitizesAllowlistedFields(t *testing.T) {
	sink := &recordingSink{}
	server := newTestRPCServer(t, newRecordingService(sink))
	resp := dispatchIngest(t, server, json.RawMessage(`{"events":[{"kind":"ui/log","trace_id":"trace token=secret","method":"Authorization: Bearer abc.def","status":"error","metadata":{"token":"secret","safe":"ok"}}]}`))
	if !resp.Enabled || resp.Recorded != 1 || resp.Dropped != 0 {
		t.Fatalf("response = %+v", resp)
	}
	assertSanitizedFrontendEvent(t, sink)
}

func assertSanitizedFrontendEvent(t *testing.T, sink *recordingSink) {
	t.Helper()
	if len(sink.events) != 1 {
		t.Fatalf("sink events = %d, want 1", len(sink.events))
	}
	event := sink.events[0]
	if event.Kind != "frontend" {
		t.Fatalf("Kind = %q, want frontend", event.Kind)
	}
	if strings.Contains(event.TraceID, "secret") || strings.Contains(event.Method, "abc.def") {
		t.Fatalf("event was not sanitized: %+v", event)
	}
	if event.Metadata["token"] != "[REDACTED]" {
		t.Fatalf("metadata token = %#v", event.Metadata["token"])
	}
}

func TestFrontendIngestRejectsUnknownFields(t *testing.T) {
	server := newTestRPCServer(t, platformobs.NewService(platformobs.Config{MetadataMaxBytes: 4096, StringMaxBytes: 64}))
	_, err := server.Dispatch(t.Context(), "observability/frontend/ingest", json.RawMessage(`{"events":[{"message":"raw ui log"}]}`))
	if err == nil {
		t.Fatalf("ingest accepted unknown raw ui/log field")
	}
}

func TestFrontendIngestTrimsOversizedBatch(t *testing.T) {
	sink := &recordingSink{}
	svc := platformobs.NewService(platformobs.Config{MetadataMaxBytes: 4096, StringMaxBytes: 64}, platformobs.WithSink(sink), platformobs.WithSampler(platformobs.NewSampler(platformobs.SamplerConfig{HighFrequencyKeepEvery: 1})))
	server := newTestRPCServer(t, svc)
	events := make([]map[string]any, maxFrontendIngestEvents+3)
	for i := range events {
		events[i] = map[string]any{"trace_id": "trace", "status": "ok"}
	}
	payload, err := json.Marshal(map[string]any{"events": events})
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	raw, err := server.Dispatch(t.Context(), "observability/frontend/ingest", payload)
	if err != nil {
		t.Fatalf("Dispatch ingest: %v", err)
	}
	var resp frontendIngestResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		t.Fatalf("unmarshal ingest: %v", err)
	}
	if resp.Recorded != maxFrontendIngestEvents || resp.Dropped != 3 {
		t.Fatalf("response = %+v", resp)
	}
	if len(sink.events) != maxFrontendIngestEvents {
		t.Fatalf("recorded events = %d", len(sink.events))
	}
}

func TestFrontendIngestDisabledServiceDropsWithoutRecording(t *testing.T) {
	server := newTestRPCServer(t, platformobs.NewDisabledService(platformobs.Config{DisabledReason: "unit disabled"}))
	raw, err := server.Dispatch(t.Context(), "observability/frontend/ingest", json.RawMessage(`{"events":[{"trace_id":"trace"},{"trace_id":"trace2"}]}`))
	if err != nil {
		t.Fatalf("Dispatch ingest disabled: %v", err)
	}
	var resp frontendIngestResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		t.Fatalf("unmarshal ingest: %v", err)
	}
	if resp.Enabled || resp.Recorded != 0 || resp.Dropped != 2 || resp.DisabledReason != "unit disabled" {
		t.Fatalf("response = %+v", resp)
	}
}

func seedTraceEvents(t *testing.T, svc *platformobs.Service) {
	t.Helper()
	base := time.Date(2026, 6, 2, 0, 0, 0, 0, time.UTC)
	recordTrace(t, svc, platformobs.TraceEvent{Timestamp: base, TraceID: "trace-1", SpanID: "span-ui", Kind: "wails", Method: "ui.call", ThreadID: "thread-1", DurationMS: 5, Status: platformobs.StatusOK})
	recordTrace(t, svc, platformobs.TraceEvent{Timestamp: base.Add(5 * time.Millisecond), TraceID: "trace-1", SpanID: "span-rpc", Kind: "rpc", Method: "rpc.dispatch", ThreadID: "thread-1", DurationMS: 120, Status: platformobs.StatusSlow, Code: platformobs.NewCodeAnchor("internal/platform/rpc/server.go", "(*Server).Dispatch", 270)})
	recordTrace(t, svc, platformobs.TraceEvent{Timestamp: base.Add(130 * time.Millisecond), TraceID: "trace-1", SpanID: "span-tool", Kind: "tool", Method: "tool.call.end", ThreadID: "thread-1", CallID: "call-1", ToolName: "lsp_file", DurationMS: 15, Status: platformobs.StatusError, Error: "tool call failed", Stack: []platformobs.StackFrame{{File: "internal/platform/toolbridge/handler.go", Function: "(*Handler).HandleToolCall", Line: 99}}})
}

func recordTrace(t *testing.T, svc *platformobs.Service, event platformobs.TraceEvent) {
	t.Helper()
	if err := svc.Record(t.Context(), event); err != nil {
		t.Fatalf("Record: %v", err)
	}
}

func newRecordingService(sink *recordingSink) *platformobs.Service {
	return platformobs.NewService(testTraceConfig(), platformobs.WithSink(sink), platformobs.WithSampler(platformobs.NewSampler(platformobs.SamplerConfig{HighFrequencyKeepEvery: 1})))
}

func testTraceConfig() platformobs.Config {
	return platformobs.Config{MetadataMaxBytes: 4096, StringMaxBytes: 64, IndexMaxEvents: 16, IndexMaxTraceEvents: 16, IndexMaxThreadEvents: 16, IndexMaxSlowEvents: 16, IndexMaxErrorEvents: 16, QueryTailTimeoutMS: 100, QueryTailMaxConcurrency: 1}
}

func dispatchQuery(t *testing.T, server *platformrpc.Server, method string, payload json.RawMessage) queryResponse {
	t.Helper()
	raw, err := server.Dispatch(t.Context(), method, payload)
	if err != nil {
		t.Fatalf("Dispatch %s: %v", method, err)
	}
	var resp queryResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		t.Fatalf("unmarshal %s: %v", method, err)
	}
	return resp
}

func dispatchIngest(t *testing.T, server *platformrpc.Server, payload json.RawMessage) frontendIngestResponse {
	t.Helper()
	raw, err := server.Dispatch(t.Context(), "observability/frontend/ingest", payload)
	if err != nil {
		t.Fatalf("Dispatch ingest: %v", err)
	}
	var resp frontendIngestResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		t.Fatalf("unmarshal ingest: %v", err)
	}
	return resp
}

func newTestRPCServer(t *testing.T, svc *platformobs.Service) *platformrpc.Server {
	t.Helper()
	server := platformrpc.NewServer(platformrpc.Params{Config: &platformconfig.Config{RPCAddr: "127.0.0.1:0"}})
	server.Register(NewHandlers(svc).Handlers)
	return server
}
