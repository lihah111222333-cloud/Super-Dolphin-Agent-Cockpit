package shared

import (
	"context"
	"testing"

	"github.com/anthropic-ai/super-agent-v3/internal/platform/observability"
)

func TestRecordTraceAddsProviderCorrelationAndNoPayload(t *testing.T) {
	cfg, err := observability.ParseConfig(observability.EnvMap{"OBS_TRACING_ENABLED": "1", "OBS_INDEX_MAX_EVENTS": "10", "OBS_INDEX_MAX_TRACE_EVENTS": "10", "OBS_INDEX_MAX_THREAD_EVENTS": "10"})
	if err != nil {
		t.Fatalf("ParseConfig: %v", err)
	}
	svc := observability.NewService(cfg)
	ctx := observability.ContextWithSpan(context.Background(), "trace-provider", "parent", "root")
	RecordTrace(ctx, svc, observability.TraceEvent{Method: "provider.turn.run", Status: observability.StatusOK, Metadata: map[string]any{"input_count": int64(1)}}, "codex", observability.CodeAnchor{File: "internal/provider/codexapp/session.go", Function: "codexapp.(*session).StartTurn", Line: 321})
	events := svc.Query(context.Background(), observability.Query{TraceID: "trace-provider"}).Events
	if len(events) != 1 {
		t.Fatalf("events = %#v, want one", events)
	}
	if events[0].ParentSpanID != "parent" || events[0].Metadata["provider"] != "codex" {
		t.Fatalf("event = %+v, want provider correlation", events[0])
	}
	if _, ok := events[0].Metadata["prompt"]; ok {
		t.Fatalf("event leaked prompt metadata: %+v", events[0])
	}
}
