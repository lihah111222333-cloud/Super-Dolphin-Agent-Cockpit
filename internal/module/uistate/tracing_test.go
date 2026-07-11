package uistate

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	sharedto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/shared"
	uidto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/ui"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/module/uistate/timeline"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/observability"
)

func TestUIStatePatchTraceExcludesPatchPayloadBody(t *testing.T) {
	t.Parallel()

	trace := newUITraceService()
	svc, _, err := NewService(nil, nil, nil, nil, nil, nil, WithObservability(trace))
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	svc.emitThreadPatch = func(uidto.UIThreadPatch) {}

	svc.emitThreadPatchEvent(uidto.UIThreadPatch{ThreadID: "thread-privacy", Source: "test", DiffText: "secret patch payload", TimelineItems: []uidto.PatchTimelineItem{{ID: "item-1", Text: "secret model output", Output: "secret tool result"}}})

	events := waitForUITraceEvents(t, trace, "uistate.patch.emit", 1)
	encoded := mustTraceString(t, events[0])
	for _, secret := range []string{"secret patch payload", "secret model output", "secret tool result"} {
		if strings.Contains(encoded, secret) {
			t.Fatalf("trace leaked payload %q: %s", secret, encoded)
		}
	}
	if events[0].ThreadID != "thread-privacy" {
		t.Fatalf("thread id = %q", events[0].ThreadID)
	}
}

func TestUIStateTimelineAndProjectionTraceUseIdentifiersOnly(t *testing.T) {
	t.Parallel()

	trace := newUITraceService()
	svc, _, err := NewService(nil, nil, nil, nil, nil, nil, WithObservability(trace))
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	svc.emitProjectionUpdated = func(uidto.UIProjectionUpdated) {}
	svc.timeline = timeline.New(nil, func(ev uidto.UITimelineAppended) { svc.recordTimelineAppendTrace(ev, 0) }, 0)

	svc.timeline.Append("thread-1", "agent-1", timeline.Item{ID: "item-1", Kind: "tool", CallID: "call-1", ToolName: "Edit", TurnID: "turn-1", Output: "secret tool result", Text: "secret model output"})
	svc.emitProjectionUpdatedEvents(uidto.UIProjectionUpdated{UIProjectionHeader: sharedto.UIProjectionHeader{ThreadHeader: sharedto.ThreadHeader{ThreadID: "thread-1"}, Projection: "timeline"}, Revision: 7})

	appendEvents := waitForUITraceEvents(t, trace, "uistate.timeline.append", 1)
	if appendEvents[0].ThreadID != "thread-1" || appendEvents[0].TurnID != "turn-1" || appendEvents[0].CallID != "call-1" || appendEvents[0].ToolName != "Edit" {
		t.Fatalf("timeline identifiers not recorded: %#v", appendEvents[0])
	}
	projectionEvents := waitForUITraceEvents(t, trace, "uistate.projection.updated", 1)
	if projectionEvents[0].ThreadID != "thread-1" {
		t.Fatalf("projection thread id = %q", projectionEvents[0].ThreadID)
	}
	encoded := mustTraceString(t, appendEvents[0]) + mustTraceString(t, projectionEvents[0])
	for _, secret := range []string{"secret tool result", "secret model output"} {
		if strings.Contains(encoded, secret) {
			t.Fatalf("trace leaked payload %q: %s", secret, encoded)
		}
	}
}

func TestUIStateTraceRecordFailureIsLogged(t *testing.T) {
	var logs bytes.Buffer
	trace := observability.NewService(
		observability.Config{IndexMaxEvents: 20, IndexMaxTraceEvents: 20, IndexMaxThreadEvents: 20},
		observability.WithSink(failingUITraceSink{}),
		observability.WithSampler(observability.NewSampler(observability.SamplerConfig{HighFrequencyKeepEvery: 1})),
	)
	svc, _, err := NewService(slog.New(slog.NewJSONHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug})), nil, nil, nil, nil, nil, WithObservability(trace))
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	svc.emitThreadPatch = func(uidto.UIThreadPatch) {}

	svc.emitThreadPatchEvent(uidto.UIThreadPatch{ThreadID: "thread-1", Source: "test"})

	raw := logs.String()
	if !strings.Contains(raw, "uistate trace record failed") || !strings.Contains(raw, "uistate.patch.emit") || !strings.Contains(raw, "thread-1") {
		t.Fatalf("logs = %s, want visible uistate trace failure", raw)
	}
	if strings.Contains(raw, "sk-") {
		t.Fatalf("logs leaked raw secret: %s", raw)
	}
}

type failingUITraceSink struct{}

func (failingUITraceSink) Append(context.Context, observability.TraceEvent) error {
	return errors.New("trace sink failed token=sk-abcdefghijklmnopqrstuvwxyz")
}

func newUITraceService() *observability.Service {
	return observability.NewService(
		observability.Config{IndexMaxEvents: 20, IndexMaxTraceEvents: 20, IndexMaxThreadEvents: 20},
		observability.WithSampler(observability.NewSampler(observability.SamplerConfig{HighFrequencyKeepEvery: 1})),
	)
}

func waitForUITraceEvents(t *testing.T, trace *observability.Service, method string, want int) []observability.TraceEvent {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		got := filterUITraceEvents(trace.Query(context.Background(), observability.Query{Limit: 100}).Events, method)
		if len(got) >= want {
			return got
		}
		time.Sleep(10 * time.Millisecond)
	}
	got := filterUITraceEvents(trace.Query(context.Background(), observability.Query{Limit: 100}).Events, method)
	t.Fatalf("trace events = %d, want at least %d for %s: %#v", len(got), want, method, got)
	return nil
}

func filterUITraceEvents(events []observability.TraceEvent, method string) []observability.TraceEvent {
	out := make([]observability.TraceEvent, 0, len(events))
	for _, ev := range events {
		if ev.Method == method {
			out = append(out, ev)
		}
	}
	return out
}

func mustTraceString(t *testing.T, ev observability.TraceEvent) string {
	t.Helper()
	data, err := json.Marshal(ev)
	if err != nil {
		t.Fatalf("marshal trace event: %v", err)
	}
	return string(data)
}
