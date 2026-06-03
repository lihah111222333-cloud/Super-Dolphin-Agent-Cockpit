package observability

import (
	"context"
	"testing"
	"time"
)

type recordingSink struct{ events []TraceEvent }

func (s *recordingSink) Append(_ context.Context, event TraceEvent) error {
	s.events = append(s.events, event)
	return nil
}

func TestServiceRecordSanitizesBeforeIndexAndSink(t *testing.T) {
	cfg, err := ParseConfig(EnvMap{"OBS_TRACING_ENABLED": "1", "OBS_INDEX_MAX_EVENTS": "4", "OBS_INDEX_MAX_TRACE_EVENTS": "4", "OBS_INDEX_MAX_THREAD_EVENTS": "4"})
	if err != nil {
		t.Fatalf("ParseConfig: %v", err)
	}
	sink := &recordingSink{}
	svc := NewService(cfg, WithSink(sink), WithSampler(NewSampler(SamplerConfig{HighFrequencyKeepEvery: 1})))
	if err := svc.Record(context.Background(), TraceEvent{TraceID: "trace token=secret", ThreadID: "thread", Method: "Authorization: Bearer abc.def", Status: StatusError}); err != nil {
		t.Fatalf("Record: %v", err)
	}
	got := svc.Query(context.Background(), Query{ThreadID: "thread"})
	if got.Source != QuerySourceMemory {
		t.Fatalf("source = %q", got.Source)
	}
	if len(got.Events) != 1 || len(sink.events) != 1 {
		t.Fatalf("indexed=%d sink=%d, want 1 each", len(got.Events), len(sink.events))
	}
	if got.Events[0].TraceID != sink.events[0].TraceID || got.Events[0].Method != sink.events[0].Method {
		t.Fatalf("index and sink saw different sanitized events: index=%+v sink=%+v", got.Events[0], sink.events[0])
	}
	if got.Events[0].TraceID == "trace token=secret" || got.Events[0].Method == "Authorization: Bearer abc.def" {
		t.Fatalf("unsanitized event reached service outputs: %+v", got.Events[0])
	}
}

func TestServiceQueryReportsJSONLTailSourceWhenOnlyTailHasEvents(t *testing.T) {
	cfg := Config{IndexMaxEvents: 2, IndexMaxTraceEvents: 2, IndexMaxThreadEvents: 2, IndexMaxSlowEvents: 2, IndexMaxErrorEvents: 2, MetadataMaxBytes: 4096, StringMaxBytes: 512, QueryTailTimeoutMS: 100, QueryTailMaxConcurrency: 1}
	tail := QueryTailReaderFunc(func(_ context.Context, query Query) (QueryResult, error) {
		if query.TraceID != "missing" {
			t.Fatalf("tail query trace = %q", query.TraceID)
		}
		return QueryResult{Source: QuerySourceJSONLTail, Events: []TraceEvent{{TraceID: "missing", Method: "tail"}}}, nil
	})
	svc := NewService(cfg, WithTailReader(tail))
	got := svc.Query(context.Background(), Query{TraceID: "missing", IncludeTail: true})
	if got.Source != QuerySourceJSONLTail {
		t.Fatalf("source = %q, want jsonl_tail", got.Source)
	}
	if methods(got.Events) != "tail" {
		t.Fatalf("events = %#v", got.Events)
	}
}

func TestServiceQueryTailDoesNotReuseStaleResult(t *testing.T) {
	cfg := Config{IndexMaxEvents: 2, IndexMaxTraceEvents: 2, IndexMaxThreadEvents: 2, IndexMaxSlowEvents: 2, IndexMaxErrorEvents: 2, MetadataMaxBytes: 4096, StringMaxBytes: 512, QueryTailTimeoutMS: 100, QueryTailMaxConcurrency: 1}
	calls := 0
	tail := QueryTailReaderFunc(func(_ context.Context, query Query) (QueryResult, error) {
		if query.TraceID != "tail-changing" {
			t.Fatalf("tail query trace = %q", query.TraceID)
		}
		calls++
		if calls == 1 {
			return QueryResult{Source: QuerySourceJSONLTail, Events: []TraceEvent{{TraceID: "tail-changing", Method: "tail-first"}}}, nil
		}
		return QueryResult{Source: QuerySourceJSONLTail, Events: []TraceEvent{{TraceID: "tail-changing", Method: "tail-second"}}}, nil
	})
	svc := NewService(cfg, WithTailReader(tail))
	query := Query{TraceID: "tail-changing", IncludeTail: true}

	first := svc.Query(context.Background(), query)
	if methods(first.Events) != "tail-first" {
		t.Fatalf("first events = %q (%#v), want first tail result", methods(first.Events), first.Events)
	}
	second := svc.Query(context.Background(), query)
	if methods(second.Events) != "tail-second" {
		t.Fatalf("second events = %q (%#v), want second tail result", methods(second.Events), second.Events)
	}
	if calls != 2 {
		t.Fatalf("tail calls = %d, want 2", calls)
	}
}

func TestServiceQueryReportsMixedSourceWhenMemoryAndTailBothContribute(t *testing.T) {
	cfg := Config{IndexMaxEvents: 2, IndexMaxTraceEvents: 2, IndexMaxThreadEvents: 2, IndexMaxSlowEvents: 2, IndexMaxErrorEvents: 2, MetadataMaxBytes: 4096, StringMaxBytes: 512, QueryTailTimeoutMS: 100, QueryTailMaxConcurrency: 1}
	tail := QueryTailReaderFunc(func(context.Context, Query) (QueryResult, error) {
		return QueryResult{Source: QuerySourceJSONLTail, Events: []TraceEvent{{TraceID: "trace", Method: "tail"}}}, nil
	})
	svc := NewService(cfg, WithTailReader(tail))
	if err := svc.Record(context.Background(), TraceEvent{TraceID: "trace", Method: "memory", Status: StatusError}); err != nil {
		t.Fatalf("Record: %v", err)
	}
	got := svc.Query(context.Background(), Query{TraceID: "trace", IncludeTail: true})
	if got.Source != QuerySourceMixed {
		t.Fatalf("source = %q, want mixed", got.Source)
	}
	if methods(got.Events) != "memory,tail" {
		t.Fatalf("events = %#v", got.Events)
	}
}

func TestServiceQueryMixedTailDedupeLimitAndTruncation(t *testing.T) {
	cfg := Config{IndexMaxEvents: 8, IndexMaxTraceEvents: 8, IndexMaxThreadEvents: 8, IndexMaxSlowEvents: 8, IndexMaxErrorEvents: 8, MetadataMaxBytes: 4096, StringMaxBytes: 512, QueryTailTimeoutMS: 100, QueryTailMaxConcurrency: 1}
	base := time.Unix(1700000000, 0)
	duplicate := TraceEvent{Timestamp: base.Add(2 * time.Second), TraceID: "trace", SpanID: "span-2", Method: "memory-duplicate", Status: StatusOK}
	tail := QueryTailReaderFunc(func(context.Context, Query) (QueryResult, error) {
		return QueryResult{Source: QuerySourceJSONLTail, Events: []TraceEvent{
			{Timestamp: base.Add(1 * time.Second), TraceID: "trace", SpanID: "span-1", Method: "tail-older", Status: StatusOK},
			duplicate,
			{Timestamp: base.Add(4 * time.Second), TraceID: "trace", SpanID: "span-4", Method: "tail-newest", Status: StatusOK},
		}, Truncated: true}, nil
	})
	svc := NewService(cfg, WithTailReader(tail))
	for _, event := range []TraceEvent{
		{Timestamp: base, TraceID: "trace", SpanID: "span-0", Method: "memory-oldest", Status: StatusOK},
		duplicate,
		{Timestamp: base.Add(3 * time.Second), TraceID: "trace", SpanID: "span-3", Method: "memory-newer", Status: StatusOK},
	} {
		if err := svc.Record(context.Background(), event); err != nil {
			t.Fatalf("Record(%s): %v", event.Method, err)
		}
	}

	got := svc.Query(context.Background(), Query{TraceID: "trace", IncludeTail: true, Limit: 3})
	if got.Source != QuerySourceMixed {
		t.Fatalf("source = %q, want mixed", got.Source)
	}
	if !got.Truncated {
		t.Fatal("Truncated = false, want true")
	}
	if methods(got.Events) != "memory-duplicate,memory-newer,tail-newest" {
		t.Fatalf("events = %q (%#v), want deduped newest chronological three", methods(got.Events), got.Events)
	}
}
