package shared

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/observability"
	pkglogger "github.com/lihah111222333-cloud/super-dolphin-agent/pkg/logger"
)

type failingTraceSink struct{}

func (failingTraceSink) Append(context.Context, observability.TraceEvent) error {
	return errors.New("trace sink unavailable")
}

func TestRecordTraceAddsProviderCorrelationAndNoPayload(t *testing.T) {
	cfg, err := observability.ParseConfig(observability.EnvMap{"OBS_TRACING_ENABLED": "1", "OBS_INDEX_MAX_EVENTS": "10", "OBS_INDEX_MAX_TRACE_EVENTS": "10", "OBS_INDEX_MAX_THREAD_EVENTS": "10"})
	if err != nil {
		t.Fatalf("ParseConfig: %v", err)
	}
	svc := observability.NewService(cfg)
	ctx := observability.ContextWithSpan(context.Background(), "trace-provider", "parent", "root")
	RecordTrace(ctx, &TraceSpanCounter{}, svc, observability.TraceEvent{Method: "provider.turn.run", Status: observability.StatusOK, Metadata: map[string]any{"input_count": int64(1)}}, "codex", observability.CodeAnchor{File: "internal/provider/codexapp/session.go", Function: "codexapp.(*session).StartTurn", Line: 321})
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

func TestRecordTraceLogsRecordErrors(t *testing.T) {
	cfg, err := observability.ParseConfig(observability.EnvMap{"OBS_TRACING_ENABLED": "1", "OBS_INDEX_MAX_EVENTS": "10", "OBS_INDEX_MAX_TRACE_EVENTS": "10", "OBS_INDEX_MAX_THREAD_EVENTS": "10"})
	if err != nil {
		t.Fatalf("ParseConfig: %v", err)
	}
	var logs bytes.Buffer
	previousLogger := pkglogger.Get()
	pkglogger.SetForTest(slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { pkglogger.SetForTest(previousLogger) })
	svc := observability.NewService(cfg, observability.WithSink(failingTraceSink{}))

	RecordTrace(context.Background(), &TraceSpanCounter{}, svc, observability.TraceEvent{Method: "provider.turn.run", Status: observability.StatusOK}, "codex", observability.CodeAnchor{File: "test.go", Function: "test", Line: 1})

	got := logs.String()
	if !strings.Contains(got, "observability: trace record failed") || !strings.Contains(got, "provider.shared") || !strings.Contains(got, "provider.turn.run") || !strings.Contains(got, "trace sink unavailable") {
		t.Fatalf("logs = %q, want visible trace record failure warning", got)
	}
}
