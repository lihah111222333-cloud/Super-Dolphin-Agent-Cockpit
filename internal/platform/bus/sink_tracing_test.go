package bus

import (
	"context"
	"log/slog"
	"testing"
	"time"

	sharedto "github.com/anthropic-ai/super-agent-v3/internal/dto/shared"
	turndto "github.com/anthropic-ai/super-agent-v3/internal/dto/turn"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/observability"
	"github.com/kelindar/event"
)

func TestLogSinkRecordsLifecycleTraceIdentifiers(t *testing.T) {
	t.Parallel()

	dispatcher := NewDispatcher()
	t.Cleanup(func() { _ = dispatcher.Close() })
	trace := observability.NewService(observability.Config{IndexMaxEvents: 20, IndexMaxTraceEvents: 20, IndexMaxThreadEvents: 20})
	sink := NewLogSink(logSinkParams{Dispatcher: dispatcher, Logger: slog.New(slog.DiscardHandler), Trace: trace})
	t.Cleanup(sink.Close)

	event.Publish(dispatcher, turndto.TurnStarted{TurnHeader: sharedto.TurnHeader{AgentHeader: sharedto.AgentHeader{ThreadHeader: sharedto.ThreadHeader{ThreadID: "thread-1"}, AgentID: "agent-1"}, TurnIDHeader: sharedto.TurnIDHeader{TurnID: "turn-1"}}})

	events := waitForTraceEvents(t, trace, "bus.event.lifecycle", 1)
	got := events[0]
	if got.ThreadID != "thread-1" || got.AgentID != "agent-1" || got.TurnID != "turn-1" {
		t.Fatalf("trace identifiers = thread:%q agent:%q turn:%q", got.ThreadID, got.AgentID, got.TurnID)
	}
	if _, ok := got.Metadata["event"]; ok {
		t.Fatalf("trace metadata persisted raw event payload: %#v", got.Metadata)
	}
}

func TestLogSinkSummarizesHighFrequencyLifecycleTrace(t *testing.T) {
	t.Parallel()

	dispatcher := NewDispatcher()
	t.Cleanup(func() { _ = dispatcher.Close() })
	trace := observability.NewService(observability.Config{IndexMaxEvents: 20, IndexMaxTraceEvents: 20, IndexMaxThreadEvents: 20})
	sink := NewLogSink(logSinkParams{Dispatcher: dispatcher, Logger: slog.New(slog.DiscardHandler), Trace: trace})
	t.Cleanup(sink.Close)

	for range 105 {
		event.Publish(dispatcher, turndto.TurnOutputDelta{TurnHeader: sharedto.TurnHeader{AgentHeader: sharedto.AgentHeader{ThreadHeader: sharedto.ThreadHeader{ThreadID: "thread-stream"}, AgentID: "agent-stream"}, TurnIDHeader: sharedto.TurnIDHeader{TurnID: "turn-stream"}}, Stream: "output", Delta: "secret model output"})
	}

	events := waitForTraceEvents(t, trace, "bus.event.lifecycle", 1)
	if len(events) > 2 {
		t.Fatalf("high-frequency traces = %d, want summarized volume <= 2", len(events))
	}
	if events[0].Status != observability.StatusDroppedSummary {
		t.Fatalf("trace status = %q, want dropped summary", events[0].Status)
	}
	if raw := events[0].Metadata; raw["delta"] != nil || raw["event"] != nil {
		t.Fatalf("summary metadata leaked payload: %#v", raw)
	}
}

func waitForTraceEvents(t *testing.T, trace *observability.Service, method string, want int) []observability.TraceEvent {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		got := filterTraceEvents(trace.Query(context.Background(), observability.Query{Limit: 100}).Events, method)
		if len(got) >= want {
			return got
		}
		time.Sleep(10 * time.Millisecond)
	}
	got := filterTraceEvents(trace.Query(context.Background(), observability.Query{Limit: 100}).Events, method)
	t.Fatalf("trace events = %d, want at least %d: %#v", len(got), want, got)
	return nil
}

func filterTraceEvents(events []observability.TraceEvent, method string) []observability.TraceEvent {
	out := make([]observability.TraceEvent, 0, len(events))
	for _, ev := range events {
		if ev.Method == method {
			out = append(out, ev)
		}
	}
	return out
}
