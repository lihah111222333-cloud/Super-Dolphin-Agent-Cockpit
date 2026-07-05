package observability

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
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

func TestServiceQueryTailReadErrorPreservesDegradationDiagnostics(t *testing.T) {
	cfg := Config{IndexMaxEvents: 2, IndexMaxTraceEvents: 2, IndexMaxThreadEvents: 2, IndexMaxSlowEvents: 2, IndexMaxErrorEvents: 2, MetadataMaxBytes: 4096, StringMaxBytes: 512, QueryTailTimeoutMS: 100, QueryTailMaxConcurrency: 1}
	tail := QueryTailReaderFunc(func(context.Context, Query) (QueryResult, error) {
		return QueryResult{
			Source:           QuerySourceJSONLTail,
			TailFilesScanned: 3,
			TailTimedOut:     true,
		}, errors.New("tail reader unavailable")
	})
	svc := NewService(cfg, WithTailReader(tail))
	if err := svc.Record(context.Background(), TraceEvent{TraceID: "trace", Method: "memory", Status: StatusOK}); err != nil {
		t.Fatalf("Record: %v", err)
	}

	got := svc.Query(context.Background(), Query{TraceID: "trace", IncludeTail: true})

	if got.Source != QuerySourceMemory || methods(got.Events) != "memory" {
		t.Fatalf("query result = %+v, want memory result preserved", got)
	}
	if got.TailFilesScanned != 3 || !got.TailTimedOut {
		t.Fatalf("tail diagnostics = files:%d timeout:%v, want files and timeout preserved", got.TailFilesScanned, got.TailTimedOut)
	}
	if len(got.TailDecodeErrors) != 1 || !strings.Contains(got.TailDecodeErrors[0].Error, "tail reader unavailable") {
		t.Fatalf("tail errors = %+v, want visible tail reader error", got.TailDecodeErrors)
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

func TestServiceQueryTailCoalescesInflightButDoesNotCacheCompletedResult(t *testing.T) {
	cfg := Config{IndexMaxEvents: 2, IndexMaxTraceEvents: 2, IndexMaxThreadEvents: 2, IndexMaxSlowEvents: 2, IndexMaxErrorEvents: 2, MetadataMaxBytes: 4096, StringMaxBytes: 512, QueryTailTimeoutMS: 1000, QueryTailMaxConcurrency: 2}
	started := make(chan struct{})
	release := make(chan struct{})
	var calls atomic.Int32
	tail := newBlockingSameTraceTailReader(started, release, &calls)
	svc := NewService(cfg, WithTailReader(tail))
	query := Query{TraceID: "same", IncludeTail: true}
	results := make(chan QueryResult, 2)
	var wg sync.WaitGroup
	wg.Add(2)
	queriesDone := make(chan struct{})
	t.Cleanup(func() {
		select {
		case <-queriesDone:
		case <-time.After(time.Second):
			t.Fatal("tail coalescing query goroutines did not stop")
		}
	})

	go func() {
		defer wg.Done()
		results <- svc.Query(context.Background(), query)
	}()
	waitForSignal(t, started, "first tail read to start")

	secondCtx, secondWaiting := newDoneSignalContext(context.Background())
	go func() {
		defer wg.Done()
		results <- svc.Query(secondCtx, query)
	}()
	waitForSignal(t, secondWaiting, "second query to wait on in-flight tail read")
	close(release)

	first := receiveQueryResult(t, results)
	second := receiveQueryResult(t, results)
	assertQueryMethods(t, first, "tail-shared", "first concurrent")
	assertQueryMethods(t, second, "tail-shared", "second concurrent")
	assertTailCalls(t, &calls, 1, "after concurrent query")

	after := svc.Query(context.Background(), query)
	assertQueryMethods(t, after, "tail-after", "sequential")
	assertTailCalls(t, &calls, 2, "after sequential query")
	wg.Wait()
	close(queriesDone)
}

func TestServiceQueryTailReReadsJSONLForSameQuery(t *testing.T) {
	dir := t.TempDir()
	cfg := Config{IndexMaxEvents: 2, IndexMaxTraceEvents: 2, IndexMaxThreadEvents: 2, IndexMaxSlowEvents: 2, IndexMaxErrorEvents: 2, MetadataMaxBytes: 4096, StringMaxBytes: 512, QueryTailTimeoutMS: 100, QueryTailMaxConcurrency: 1}
	svc := NewService(cfg, WithTailReader(JSONLTailReader{Dir: dir, MaxBytes: 1024 * 1024}))
	query := Query{TraceID: "disk-tail", IncludeTail: true}

	first := svc.Query(context.Background(), query)
	if len(first.Events) != 0 {
		t.Fatalf("first events = %#v, want none before JSONL file exists", first.Events)
	}

	writeJSONL(t, filepath.Join(dir, "trace-2026-06-03.jsonl"), TraceEvent{SchemaVersion: SchemaVersion, TraceID: "disk-tail", Method: "jsonl-fresh", Status: StatusOK})

	second := svc.Query(context.Background(), query)
	if second.Source != QuerySourceJSONLTail {
		t.Fatalf("second source = %q, want jsonl_tail", second.Source)
	}
	if got := methods(second.Events); got != "jsonl-fresh" {
		t.Fatalf("second events = %q (%#v), want fresh JSONL tail event", got, second.Events)
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

func TestServiceQueryRereadsTailForRepeatedLatestQueries(t *testing.T) {
	cfg := Config{IndexMaxEvents: 2, IndexMaxTraceEvents: 2, IndexMaxThreadEvents: 2, IndexMaxSlowEvents: 2, IndexMaxErrorEvents: 2, MetadataMaxBytes: 4096, StringMaxBytes: 512, QueryTailTimeoutMS: 100, QueryTailMaxConcurrency: 1}
	calls := 0
	tail := QueryTailReaderFunc(func(context.Context, Query) (QueryResult, error) {
		calls++
		method := "tail-old"
		if calls > 1 {
			method = "tail-new"
		}
		return QueryResult{Source: QuerySourceJSONLTail, Events: []TraceEvent{{TraceID: "trace", Method: method}}}, nil
	})
	svc := NewService(cfg, WithTailReader(tail))

	first := svc.Query(context.Background(), Query{TraceID: "trace", IncludeTail: true})
	second := svc.Query(context.Background(), Query{TraceID: "trace", IncludeTail: true})

	if calls != 2 {
		t.Fatalf("tail reader calls = %d, want 2", calls)
	}
	if methods(first.Events) != "tail-old" {
		t.Fatalf("first events = %q", methods(first.Events))
	}
	if methods(second.Events) != "tail-new" {
		t.Fatalf("second events = %q, want latest tail result", methods(second.Events))
	}
}

func newBlockingSameTraceTailReader(started chan<- struct{}, release <-chan struct{}, calls *atomic.Int32) QueryTailReaderFunc {
	return func(ctx context.Context, query Query) (QueryResult, error) {
		if query.TraceID != "same" {
			return QueryResult{Source: QuerySourceJSONLTail}, errors.New("unexpected tail query trace")
		}
		call := calls.Add(1)
		signalFirstTailRead(call, started)
		if err := waitForTailRelease(ctx, release); err != nil {
			return QueryResult{Source: QuerySourceJSONLTail}, err
		}
		return QueryResult{Source: QuerySourceJSONLTail, Events: []TraceEvent{{TraceID: "same", Method: tailMethodForCall(call)}}}, nil
	}
}

func signalFirstTailRead(call int32, started chan<- struct{}) {
	if call == 1 {
		close(started)
	}
}

func waitForTailRelease(ctx context.Context, release <-chan struct{}) error {
	select {
	case <-release:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func tailMethodForCall(call int32) string {
	if call == 1 {
		return "tail-shared"
	}
	return "tail-after"
}

type doneSignalContext struct {
	context.Context
	once   sync.Once
	called chan struct{}
}

func newDoneSignalContext(ctx context.Context) (*doneSignalContext, <-chan struct{}) {
	called := make(chan struct{})
	return &doneSignalContext{Context: ctx, called: called}, called
}

func (ctx *doneSignalContext) Done() <-chan struct{} {
	ctx.once.Do(func() { close(ctx.called) })
	return ctx.Context.Done()
}

func waitForSignal(t *testing.T, signal <-chan struct{}, name string) {
	t.Helper()
	select {
	case <-signal:
	case <-time.After(5 * time.Second):
		t.Fatalf("timed out waiting for %s", name)
	}
}

func receiveQueryResult(t *testing.T, results <-chan QueryResult) QueryResult {
	t.Helper()
	select {
	case result := <-results:
		return result
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for query result")
		return QueryResult{}
	}
}

func assertQueryMethods(t *testing.T, result QueryResult, want string, label string) {
	t.Helper()
	if got := methods(result.Events); got != want {
		t.Fatalf("%s events = %q (%#v), want %q", label, got, result.Events, want)
	}
}

func assertTailCalls(t *testing.T, calls *atomic.Int32, want int32, label string) {
	t.Helper()
	if got := calls.Load(); got != want {
		t.Fatalf("tail calls %s = %d, want %d", label, got, want)
	}
}
