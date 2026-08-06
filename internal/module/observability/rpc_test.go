package observability

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"go.uber.org/fx"
	"go.uber.org/fx/fxtest"

	"github.com/creachadair/jrpc2/handler"
	platformconfig "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/config"
	platformobs "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/observability"
	platformrpc "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/rpc"
)

type recordingSink struct{ events []platformobs.TraceEvent }

func (s *recordingSink) Append(_ context.Context, event platformobs.TraceEvent) error {
	s.events = append(s.events, event)
	return nil
}

func TestNewHandlersReturnsRPCHandlerMapResult(t *testing.T) {
	svc := platformobs.NewDisabledService(platformobs.Config{DisabledReason: "unit"})
	result := NewHandlers(svc)
	for _, method := range []string{"observability/trace/get", "observability/thread/recent", "observability/recent/list", "observability/slow/list", "observability/error/list", "observability/status", "observability/frontend/ingest"} {
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

func TestRecentListRPCFiltersLatestEventsForSystemLog(t *testing.T) {
	svc := newRecordingService(&recordingSink{})
	seedTraceEvents(t, svc)
	recordTrace(t, svc, platformobs.TraceEvent{
		Timestamp:  time.Date(2026, 6, 2, 0, 0, 1, 0, time.UTC),
		TraceID:    "trace-2",
		SpanID:     "span-frontend",
		Kind:       "frontend",
		Phase:      "frontend.rpc.failed",
		Method:     "thread/start",
		ThreadID:   "thread-2",
		DurationMS: 33,
		Status:     platformobs.StatusError,
		Error:      "thread start failed",
		Metadata:   map[string]any{"req_id": 42},
	})
	server := newTestRPCServer(t, svc)

	got := dispatchQuery(t, server, "observability/recent/list", json.RawMessage(`{"limit":5,"status":"error","component":"frontend","keyword":"thread/start","includeTail":false}`))

	if got.Source != platformobs.QuerySourceMemory || len(got.Events) != 1 {
		t.Fatalf("recent response = %+v", got)
	}
	event := got.Events[0]
	if event.TraceID != "trace-2" || event.Method != "thread/start" || event.Status != platformobs.StatusError {
		t.Fatalf("recent event = %+v", event)
	}
	if got.Errors[0].Method != "thread/start" || got.TotalDurationMS != 33 {
		t.Fatalf("recent summary = %+v", got)
	}
}

func TestRecentListRPCSuppressesInternalLifecycleNoiseByDefault(t *testing.T) {
	svc := newRecordingService(&recordingSink{})
	base := time.Date(2026, 6, 2, 0, 0, 0, 0, time.UTC)
	recordTrace(t, svc, platformobs.TraceEvent{
		Timestamp:  base,
		TraceID:    "trace-request",
		SpanID:     "span-request",
		Kind:       "frontend",
		Phase:      "frontend.rpc.done",
		Method:     "thread/start",
		DurationMS: 18,
		Status:     platformobs.StatusOK,
	})
	recordTrace(t, svc, platformobs.TraceEvent{
		Timestamp:  base.Add(time.Millisecond),
		TraceID:    "trace-lifecycle",
		SpanID:     "span-lifecycle",
		Kind:       "bus",
		Phase:      "bus.event.lifecycle",
		Method:     "bus.event.lifecycle",
		DurationMS: 0,
		Status:     platformobs.StatusOK,
	})
	recordTrace(t, svc, platformobs.TraceEvent{
		Timestamp:  base.Add(2 * time.Millisecond),
		TraceID:    "trace-patch",
		SpanID:     "span-patch",
		Kind:       "uistate",
		Phase:      "uistate.patch.emit",
		Method:     "uistate.patch.emit",
		DurationMS: 0,
		Status:     platformobs.StatusError,
	})
	server := newTestRPCServer(t, svc)

	got := dispatchQuery(t, server, "observability/recent/list", json.RawMessage(`{"limit":10,"includeTail":false}`))

	if len(got.Events) != 1 || got.Events[0].TraceID != "trace-request" {
		t.Fatalf("recent events = %+v, want only user request event", got.Events)
	}

	lifecycle := dispatchQuery(t, server, "observability/recent/list", json.RawMessage(`{"limit":10,"keyword":"lifecycle","includeTail":false}`))
	if len(lifecycle.Events) != 1 || lifecycle.Events[0].TraceID != "trace-lifecycle" {
		t.Fatalf("explicit lifecycle search = %+v, want lifecycle event", lifecycle.Events)
	}
}

func TestRecentListRPCLimitCountsTraceRowsAfterFiltering(t *testing.T) {
	base := time.Date(2026, 6, 2, 0, 0, 0, 0, time.UTC)
	var seenTailLimit int
	tail := platformobs.QueryTailReaderFunc(func(_ context.Context, query platformobs.Query) (platformobs.QueryResult, error) {
		seenTailLimit = query.Limit
		events := make([]platformobs.TraceEvent, 0, 120)
		for trace := range 60 {
			for span := range 2 {
				events = append(events, platformobs.TraceEvent{
					Timestamp: base.Add(time.Duration(trace*10+span) * time.Millisecond),
					TraceID:   fmt.Sprintf("trace-%02d", trace),
					SpanID:    fmt.Sprintf("span-%02d-%d", trace, span),
					Kind:      "frontend",
					Phase:     "frontend.rpc.done",
					Method:    "ui/sidebar/get",
					Status:    platformobs.StatusOK,
				})
			}
		}
		return platformobs.QueryResult{Source: platformobs.QuerySourceJSONLTail, Events: events}, nil
	})
	svc := platformobs.NewService(testTraceConfig(), platformobs.WithTailReader(tail))
	server := newTestRPCServer(t, svc)

	got := dispatchQuery(t, server, "observability/recent/list", json.RawMessage(`{"limit":50}`))

	if seenTailLimit < 2500 {
		t.Fatalf("tail query limit = %d, want expanded raw window for recent trace rows", seenTailLimit)
	}
	traces := make(map[string]struct{})
	for _, event := range got.Events {
		traces[event.TraceID] = struct{}{}
	}
	if len(traces) != 50 {
		t.Fatalf("recent trace rows = %d from %d events, want 50 traces", len(traces), len(got.Events))
	}
	if _, ok := traces["trace-10"]; !ok {
		t.Fatalf("oldest selected trace missing; selected=%v", traces)
	}
	if _, ok := traces["trace-09"]; ok {
		t.Fatalf("response included more than the latest 50 traces; selected=%v", traces)
	}
}

func TestRecentListRPCPushesSparseUIFiltersBeforeTailLimit(t *testing.T) {
	base := time.Date(2026, 6, 2, 0, 0, 0, 0, time.UTC)
	tailEvents := []platformobs.TraceEvent{{
		Timestamp: base,
		TraceID:   "trace-sparse",
		SpanID:    "span-sparse",
		Kind:      "frontend",
		Method:    "rare/method",
		AgentID:   "agent-rare",
		Status:    platformobs.StatusError,
		Metadata:  map[string]any{"note": "needle"},
	}}
	for n := range maxQueryLimit {
		tailEvents = append(tailEvents, platformobs.TraceEvent{
			Timestamp: base.Add(time.Duration(n+1) * time.Millisecond),
			TraceID:   fmt.Sprintf("trace-common-%03d", n),
			SpanID:    fmt.Sprintf("span-common-%03d", n),
			Kind:      "backend",
			Method:    "common/method",
			AgentID:   "agent-common",
			Status:    platformobs.StatusOK,
		})
	}
	tail := platformobs.QueryTailReaderFunc(func(_ context.Context, query platformobs.Query) (platformobs.QueryResult, error) {
		events, truncated := applyTailWindowForQuery(tailEvents, query)
		return platformobs.QueryResult{Source: platformobs.QuerySourceJSONLTail, Events: events, Truncated: truncated}, nil
	})
	svc := platformobs.NewService(testTraceConfig(), platformobs.WithTailReader(tail))
	server := newTestRPCServer(t, svc)

	got := dispatchQuery(t, server, "observability/recent/list", json.RawMessage(`{"limit":1,"status":"error","component":"frontend","method":"rare/method","agentId":"agent-rare","keyword":"needle"}`))

	if len(got.Events) != 1 || got.Events[0].TraceID != "trace-sparse" {
		t.Fatalf("recent sparse events = %+v, want trace-sparse despite raw tail window", got.Events)
	}
}

func TestRecentListRPCIncludesLatestEventsWithoutTraceID(t *testing.T) {
	base := time.Date(2026, 6, 3, 14, 0, 1, 0, time.Local)
	tail := platformobs.QueryTailReaderFunc(func(context.Context, platformobs.Query) (platformobs.QueryResult, error) {
		return platformobs.QueryResult{
			Source: platformobs.QuerySourceJSONLTail,
			Events: []platformobs.TraceEvent{
				{
					Timestamp: base,
					TraceID:   "trace-old",
					Kind:      "frontend",
					Phase:     "frontend.rpc.done",
					Method:    "thread/start",
					Status:    platformobs.StatusOK,
				},
				{
					Timestamp: base.Add(15 * time.Minute),
					Kind:      "provider",
					Phase:     "provider.session.ready",
					Method:    "provider.session.ready",
					Status:    platformobs.StatusOK,
				},
			},
		}, nil
	})
	svc := platformobs.NewService(testTraceConfig(), platformobs.WithTailReader(tail))
	server := newTestRPCServer(t, svc)

	got := dispatchQuery(t, server, "observability/recent/list", json.RawMessage(`{"limit":10}`))

	if len(got.Events) != 2 {
		t.Fatalf("recent events = %+v, want old traced event and latest untraced event", got.Events)
	}
	if got.Events[0].Method != "provider.session.ready" || got.Events[0].TraceID != "" {
		t.Fatalf("latest recent event = %+v, want untraced provider session event first", got.Events[0])
	}
	if got.Events[1].TraceID != "trace-old" {
		t.Fatalf("old traced event = %+v, want trace-old second", got.Events[1])
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

func TestTraceGetRPCReportsTailReadDegradation(t *testing.T) {
	tail := platformobs.QueryTailReaderFunc(func(context.Context, platformobs.Query) (platformobs.QueryResult, error) {
		return platformobs.QueryResult{
			Source:           platformobs.QuerySourceJSONLTail,
			TailFilesScanned: 4,
			TailTimedOut:     true,
		}, errors.New("tail reader unavailable")
	})
	svc := platformobs.NewService(testTraceConfig(), platformobs.WithTailReader(tail))
	server := newTestRPCServer(t, svc)

	raw, err := server.Dispatch(t.Context(), "observability/trace/get", json.RawMessage(`{"traceId":"tail-degraded","limit":10}`))
	if err != nil {
		t.Fatalf("Dispatch trace/get: %v", err)
	}
	var envelope map[string]any
	if err := json.Unmarshal(raw, &envelope); err != nil {
		t.Fatalf("unmarshal trace/get: %v", err)
	}
	if envelope["degraded"] != true {
		t.Fatalf("degraded = %#v in %#v, want true", envelope["degraded"], envelope)
	}
	if envelope["tailError"] != "tail reader unavailable" {
		t.Fatalf("tailError = %#v, want visible tail error", envelope["tailError"])
	}
	if envelope["tailTimedOut"] != true || envelope["tailFilesScanned"] != float64(4) {
		t.Fatalf("tail diagnostics = timeout:%#v files:%#v, want preserved", envelope["tailTimedOut"], envelope["tailFilesScanned"])
	}
}

func TestTraceGetRPCDoesNotReuseStaleTailResult(t *testing.T) {
	calls := 0
	tail := platformobs.QueryTailReaderFunc(func(_ context.Context, query platformobs.Query) (platformobs.QueryResult, error) {
		if query.TraceID != "tail-changing" {
			t.Fatalf("tail query trace = %q", query.TraceID)
		}
		calls++
		method := "second"
		if calls == 1 {
			method = "first"
		}
		return platformobs.QueryResult{
			Source: platformobs.QuerySourceJSONLTail,
			Events: []platformobs.TraceEvent{{
				TraceID: "tail-changing",
				Method:  method,
			}},
		}, nil
	})
	svc := platformobs.NewService(testTraceConfig(), platformobs.WithTailReader(tail))
	server := newTestRPCServer(t, svc)
	payload := json.RawMessage(`{"traceId":"tail-changing","limit":10}`)

	first := dispatchQuery(t, server, "observability/trace/get", payload)
	if len(first.Events) != 1 || first.Events[0].Method != "first" {
		t.Fatalf("first tail response = %+v, want method first", first)
	}
	second := dispatchQuery(t, server, "observability/trace/get", payload)
	if len(second.Events) != 1 || second.Events[0].Method != "second" {
		t.Fatalf("second tail response = %+v, want method second", second)
	}
	if calls != 2 {
		t.Fatalf("tail calls = %d, want 2", calls)
	}
}

func TestRecentListRPCDoesNotReuseStaleTailResult(t *testing.T) {
	base := time.Date(2026, 6, 2, 0, 0, 0, 0, time.UTC)
	calls := 0
	tail := platformobs.QueryTailReaderFunc(func(context.Context, platformobs.Query) (platformobs.QueryResult, error) {
		calls++
		traceID := "second"
		if calls == 1 {
			traceID = "first"
		}
		return platformobs.QueryResult{
			Source: platformobs.QuerySourceJSONLTail,
			Events: []platformobs.TraceEvent{{
				Timestamp: base.Add(time.Duration(calls) * time.Millisecond),
				TraceID:   traceID,
				Kind:      "frontend",
				Phase:     "frontend.rpc.done",
				Method:    "ui/sidebar/get",
				Status:    platformobs.StatusOK,
			}},
		}, nil
	})
	svc := platformobs.NewService(testTraceConfig(), platformobs.WithTailReader(tail))
	server := newTestRPCServer(t, svc)
	payload := json.RawMessage(`{"limit":10}`)

	first := dispatchQuery(t, server, "observability/recent/list", payload)
	if len(first.Events) != 1 || first.Events[0].TraceID != "first" {
		t.Fatalf("first recent response = %+v, want trace first", first)
	}
	second := dispatchQuery(t, server, "observability/recent/list", payload)
	if len(second.Events) != 1 || second.Events[0].TraceID != "second" {
		t.Fatalf("second recent response = %+v, want trace second", second)
	}
	if calls != 2 {
		t.Fatalf("tail calls = %d, want 2", calls)
	}
}

func TestFrontendIngestSanitizesAllowlistedFields(t *testing.T) {
	sink := &recordingSink{}
	server := newTestRPCServer(t, newRecordingService(sink))
	resp := dispatchIngest(t, server, json.RawMessage(`{"events":[{"ts":"2026-08-06T10:11:12Z","kind":"ui/log","trace_id":"trace token=secret","method":"Authorization: Bearer abc.def","status":"error","metadata":{"token":"secret","safe":"ok","prompt":"draft private prompt payload","tool_result":"tool returned private payload","file_path":"/home/alice/private.txt"}}]}`))
	if !resp.Enabled || resp.Recorded != 1 || resp.Dropped != 0 {
		t.Fatalf("response = %+v", resp)
	}
	assertSanitizedFrontendEvent(t, sink)
}

func TestFrontendIngestRejectsMissingRequiredFieldsWithoutRecording(t *testing.T) {
	tests := []struct {
		name    string
		payload json.RawMessage
	}{
		{
			name:    "missing status",
			payload: json.RawMessage(`{"events":[{"ts":"2026-08-06T10:11:12Z","trace_id":"trace"}]}`),
		},
		{
			name:    "missing timestamp",
			payload: json.RawMessage(`{"events":[{"status":"ok","trace_id":"trace"}]}`),
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			sink := &recordingSink{}
			server := newTestRPCServer(t, newRecordingService(sink))
			if _, err := server.Dispatch(t.Context(), "observability/frontend/ingest", test.payload); err == nil {
				t.Fatal("Dispatch ingest error = nil, want missing required field rejection")
			}
			if len(sink.events) != 0 {
				t.Fatalf("sink events = %d, want 0 after rejected ingest", len(sink.events))
			}
		})
	}
}

func TestFrontendIngestPreservesRequiredFields(t *testing.T) {
	sink := &recordingSink{}
	server := newTestRPCServer(t, newRecordingService(sink))
	const timestamp = "2026-08-06T10:11:12.345Z"
	resp := dispatchIngest(t, server, json.RawMessage(`{"events":[{"ts":"2026-08-06T10:11:12.345Z","status":"error","trace_id":"trace"}]}`))
	if !resp.Enabled || resp.Recorded != 1 || resp.Dropped != 0 {
		t.Fatalf("response = %+v", resp)
	}
	if len(sink.events) != 1 {
		t.Fatalf("sink events = %d, want 1", len(sink.events))
	}
	wantTimestamp, err := time.Parse(time.RFC3339Nano, timestamp)
	if err != nil {
		t.Fatalf("parse timestamp: %v", err)
	}
	if got := sink.events[0]; !got.Timestamp.Equal(wantTimestamp) || got.Status != platformobs.StatusError {
		t.Fatalf("recorded event = %+v, want preserved timestamp and error status", got)
	}
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
	for _, key := range []string{"prompt", "tool_result", "file_path"} {
		if event.Metadata[key] != "[REDACTED]" {
			t.Fatalf("metadata %s = %#v, want redacted", key, event.Metadata[key])
		}
	}
	if event.Metadata["safe"] != "ok" {
		t.Fatalf("metadata safe = %#v, want ok", event.Metadata["safe"])
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
		events[i] = map[string]any{"ts": "2026-08-06T10:11:12Z", "trace_id": "trace", "status": "ok"}
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
	raw, err := server.Dispatch(t.Context(), "observability/frontend/ingest", json.RawMessage(`{"events":[{"ts":"2026-08-06T10:11:12Z","status":"ok","trace_id":"trace"},{"ts":"2026-08-06T10:11:12Z","status":"ok","trace_id":"trace2"}]}`))
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
	recordTrace(t, svc, platformobs.TraceEvent{Timestamp: base.Add(130 * time.Millisecond), TraceID: "trace-1", SpanID: "span-tool", Kind: "tool", Method: "tool.call.end", ThreadID: "thread-1", CallID: "call-1", ToolName: "file", DurationMS: 15, Status: platformobs.StatusError, Error: "tool call failed", Stack: []platformobs.StackFrame{{File: "internal/platform/toolbridge/handler.go", Function: "(*Handler).HandleToolCall", Line: 99}}})
}

func recordTrace(t *testing.T, svc *platformobs.Service, event platformobs.TraceEvent) {
	t.Helper()
	if err := svc.Record(t.Context(), event); err != nil {
		t.Fatalf("Record: %v", err)
	}
}

func applyTailWindowForQuery(events []platformobs.TraceEvent, query platformobs.Query) ([]platformobs.TraceEvent, bool) {
	filtered := make([]platformobs.TraceEvent, 0, len(events))
	for _, event := range events {
		if eventMatchesComponent(event, query.Component) &&
			eventMatchesStatus(event, string(query.Status)) &&
			eventMatchesText(event.Method, query.Method) &&
			eventMatchesText(event.AgentID, query.AgentID) &&
			eventMatchesKeyword(event, query.Keyword) {
			filtered = append(filtered, event)
		}
	}
	if query.Limit > 0 && len(filtered) > query.Limit {
		return filtered[len(filtered)-query.Limit:], true
	}
	return filtered, false
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
