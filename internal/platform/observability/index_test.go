package observability

import "testing"

func TestIndexEvictsGlobalAndCleansSecondaryReferences(t *testing.T) {
	idx := NewIndex(Config{IndexMaxEvents: 3, IndexMaxTraceEvents: 2, IndexMaxThreadEvents: 2, IndexMaxSlowEvents: 2, IndexMaxErrorEvents: 2})
	idx.Add(TraceEvent{TraceID: "trace-evicted", ThreadID: "thread-evicted", Method: "first", Status: StatusOK})
	idx.Add(TraceEvent{TraceID: "trace-a", ThreadID: "thread-a", Method: "second", Status: StatusOK})
	idx.Add(TraceEvent{TraceID: "trace-b", ThreadID: "thread-b", Method: "third", Status: StatusOK})
	idx.Add(TraceEvent{TraceID: "trace-c", ThreadID: "thread-c", Method: "fourth", Status: StatusOK})

	got := idx.Query(Query{TraceID: "trace-a"})
	if got.Source != QuerySourceMemory {
		t.Fatalf("source = %q, want memory", got.Source)
	}
	if len(got.Events) != 1 || got.Events[0].Method != "second" {
		t.Fatalf("trace-a events = %#v, want only second after stale global eviction", got.Events)
	}
	if evicted := idx.Query(Query{TraceID: "trace-evicted"}); len(evicted.Events) != 0 {
		t.Fatalf("trace-evicted events = %#v, want empty after global eviction", evicted.Events)
	}
	if idx.TraceKeyCount() != 3 {
		t.Fatalf("TraceKeyCount = %d, want only three live trace keys", idx.TraceKeyCount())
	}
}

func TestIndexCapsPerTraceSlowAndErrorQueries(t *testing.T) {
	idx := NewIndex(Config{IndexMaxEvents: 10, IndexMaxTraceEvents: 2, IndexMaxThreadEvents: 3, IndexMaxSlowEvents: 2, IndexMaxErrorEvents: 2})
	idx.Add(TraceEvent{TraceID: "trace-a", ThreadID: "thread-a", Method: "ok-1", Status: StatusOK})
	idx.Add(TraceEvent{TraceID: "trace-a", ThreadID: "thread-a", Method: "slow-1", Status: StatusSlow})
	idx.Add(TraceEvent{TraceID: "trace-a", ThreadID: "thread-a", Method: "error-1", Status: StatusError})
	idx.Add(TraceEvent{TraceID: "trace-a", ThreadID: "thread-a", Method: "slow-2", Status: StatusSlow})
	idx.Add(TraceEvent{TraceID: "trace-a", ThreadID: "thread-a", Method: "panic-1", Status: StatusPanic})

	byTrace := idx.Query(Query{TraceID: "trace-a"})
	if methods(byTrace.Events) != "slow-2,panic-1" {
		t.Fatalf("trace cap methods = %q", methods(byTrace.Events))
	}
	if slow := idx.Query(Query{Slow: true}); methods(slow.Events) != "slow-1,slow-2" {
		t.Fatalf("slow methods = %q", methods(slow.Events))
	}
	if errors := idx.Query(Query{Errors: true}); methods(errors.Events) != "error-1,panic-1" {
		t.Fatalf("error methods = %q", methods(errors.Events))
	}
}

func TestIndexQueryAppliesCombinedPredicates(t *testing.T) {
	idx := NewIndex(Config{IndexMaxEvents: 10, IndexMaxTraceEvents: 10, IndexMaxThreadEvents: 10, IndexMaxSlowEvents: 10, IndexMaxErrorEvents: 10})
	idx.Add(TraceEvent{TraceID: "trace-a", ThreadID: "thread-a", Method: "ok-a", Status: StatusOK})
	idx.Add(TraceEvent{TraceID: "trace-a", ThreadID: "thread-a", Method: "slow-a", Status: StatusSlow})
	idx.Add(TraceEvent{TraceID: "trace-a", ThreadID: "thread-b", Method: "slow-b", Status: StatusSlow})
	idx.Add(TraceEvent{TraceID: "trace-a", ThreadID: "thread-a", Method: "error-a", Status: StatusError})

	slowInThread := idx.Query(Query{TraceID: "trace-a", ThreadID: "thread-a", Slow: true})
	if methods(slowInThread.Events) != "slow-a" {
		t.Fatalf("slow thread methods = %q, want slow-a", methods(slowInThread.Events))
	}
	errorsInTrace := idx.Query(Query{TraceID: "trace-a", Errors: true})
	if methods(errorsInTrace.Events) != "error-a" {
		t.Fatalf("trace error methods = %q, want error-a", methods(errorsInTrace.Events))
	}
	slowAndErrors := idx.Query(Query{TraceID: "trace-a", Slow: true, Errors: true})
	if len(slowAndErrors.Events) != 0 {
		t.Fatalf("slow+errors events = %#v, want none", slowAndErrors.Events)
	}
}

func TestIndexMissingTraceReturnsEmptyMemorySource(t *testing.T) {
	idx := NewIndex(Config{IndexMaxEvents: 2, IndexMaxTraceEvents: 2, IndexMaxThreadEvents: 2, IndexMaxSlowEvents: 2, IndexMaxErrorEvents: 2})
	got := idx.Query(Query{TraceID: "missing"})
	if got.Source != QuerySourceMemory {
		t.Fatalf("source = %q, want memory", got.Source)
	}
	if len(got.Events) != 0 {
		t.Fatalf("events = %#v, want empty", got.Events)
	}
}

func methods(events []TraceEvent) string {
	out := ""
	for i, event := range events {
		if i > 0 {
			out += ","
		}
		out += event.Method
	}
	return out
}
