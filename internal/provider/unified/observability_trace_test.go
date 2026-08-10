package unified

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
	dto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/provider"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/observability"
)

type runtimeConfigGenerationSession struct {
	*generationTestSession
	runtime map[string]any
}

type configurableRuntimeSession struct {
	*runtimeConfigGenerationSession
	config dto.ThreadConfig
	models []string
}

func (s *configurableRuntimeSession) ReadConfig(context.Context, string) (dto.ThreadConfig, error) {
	return s.config, nil
}

func (s *configurableRuntimeSession) AllowedModels(context.Context) ([]string, error) {
	return s.models, nil
}

func TestTracedSessionForwardsProviderConfigCapabilities(t *testing.T) {
	model := "provider-only-model"
	base := &configurableRuntimeSession{
		runtimeConfigGenerationSession: &runtimeConfigGenerationSession{generationTestSession: &generationTestSession{threadID: "thread-config"}},
		config:                         dto.ThreadConfig{ThreadID: "thread-config", Provider: "codex"},
		models:                         []string{"gpt-5.5", model},
	}
	wrapped := (&Client{tracer: observability.NewDisabledService(observability.Config{})}).wrapSession("codex", base)
	reader := wrapped.(interface {
		ReadConfig(context.Context, string) (dto.ThreadConfig, error)
	})
	catalog := wrapped.(interface {
		AllowedModels(context.Context) ([]string, error)
	})
	if cfg, err := reader.ReadConfig(context.Background(), "thread-config"); err != nil || cfg.ThreadID != "thread-config" {
		t.Fatalf("ReadConfig() = %#v, %v", cfg, err)
	}
	if models, err := catalog.AllowedModels(context.Background()); err != nil || len(models) != 2 || models[1] != model {
		t.Fatalf("AllowedModels() = %#v, %v", models, err)
	}
}

func (s *runtimeConfigGenerationSession) RuntimeConfigSnapshot() map[string]any {
	return s.runtime
}

func TestTracedSessionForwardsRuntimeConfigSnapshot(t *testing.T) {
	base := &runtimeConfigGenerationSession{
		generationTestSession: &generationTestSession{threadID: "thread-runtime"},
		runtime: map[string]any{
			"codexHome":          "/Users/test/.codex",
			"codexInstanceKey":   "default",
			"codexModelProvider": "openai",
		},
	}
	wrapped := (&Client{tracer: observability.NewDisabledService(observability.Config{})}).wrapSession("codex", base)
	reader, ok := wrapped.(interface{ RuntimeConfigSnapshot() map[string]any })
	if !ok {
		t.Fatalf("wrapped session type %T does not expose RuntimeConfigSnapshot", wrapped)
	}
	got := reader.RuntimeConfigSnapshot()
	if got["codexHome"] != "/Users/test/.codex" ||
		got["codexInstanceKey"] != "default" ||
		got["codexModelProvider"] != "openai" {
		t.Fatalf("RuntimeConfigSnapshot() = %#v, want forwarded codex identity", got)
	}
}

type errorTraceSession struct {
	*generationTestSession
	err error
}

func (s *errorTraceSession) StartTurn(context.Context, dto.TurnRequest) (contract.TurnHandle, error) {
	return nil, s.err
}

func TestTracedSessionErrorTraceUsesSafePreviewField(t *testing.T) {
	cfg, err := observability.ParseConfig(observability.EnvMap{"OBS_TRACING_ENABLED": "1", "OBS_INDEX_MAX_EVENTS": "10", "OBS_INDEX_MAX_TRACE_EVENTS": "10", "OBS_INDEX_MAX_THREAD_EVENTS": "10"})
	if err != nil {
		t.Fatalf("ParseConfig: %v", err)
	}
	tracer := observability.NewService(cfg)
	base := &errorTraceSession{
		generationTestSession: &generationTestSession{threadID: "thread-error-preview"},
		err:                   errors.New("provider failed token=sk-abcdefghijklmnopqrstuvwxyz"),
	}
	wrapped := (&Client{tracer: tracer}).wrapSession("codex", base)

	_, gotErr := wrapped.StartTurn(observability.ContextWithSpan(context.Background(), "trace-provider-error-preview", "parent", "root"), dto.TurnRequest{ThreadID: "thread-error-preview", LocalID: "turn-1"})
	if gotErr == nil {
		t.Fatal("StartTurn() error = nil, want provider error")
	}

	events := tracer.Query(context.Background(), observability.Query{TraceID: "trace-provider-error-preview"}).Events
	if len(events) != 1 {
		t.Fatalf("events = %#v, want one provider trace", events)
	}
	preview, _ := events[0].Metadata[observability.ErrorPreviewField].(string)
	if !strings.Contains(preview, "provider failed") || strings.Contains(preview, "sk-") {
		t.Fatalf("error_preview = %q, want sanitized provider error detail", preview)
	}
	if events[0].Metadata[observability.ErrorCodeField] != "provider_error" {
		t.Fatalf("error_code = %#v, want provider_error", events[0].Metadata[observability.ErrorCodeField])
	}
}

func TestClientTraceCounterIsIsolatedAndSharedWithTracedSession(t *testing.T) {
	cfg, err := observability.ParseConfig(observability.EnvMap{"OBS_TRACING_ENABLED": "1", "OBS_INDEX_MAX_EVENTS": "10", "OBS_INDEX_MAX_TRACE_EVENTS": "10", "OBS_INDEX_MAX_THREAD_EVENTS": "10"})
	if err != nil {
		t.Fatalf("ParseConfig: %v", err)
	}
	firstTracer := observability.NewService(cfg)
	secondTracer := observability.NewService(cfg)
	first := newClient(nil, nil, nil, firstTracer)
	second := newClient(nil, nil, nil, secondTracer)
	ctx := observability.ContextWithSpan(context.Background(), "trace-client-counter", "parent", "root")

	first.recordProviderTrace(ctx, observability.TraceEvent{Method: "provider.session.acquire"})
	second.recordProviderTrace(ctx, observability.TraceEvent{Method: "provider.session.acquire"})
	wrapped := first.wrapSession("codex", &errorTraceSession{generationTestSession: &generationTestSession{threadID: "thread-counter"}, err: errors.New("provider failed")})
	if _, err := wrapped.StartTurn(ctx, dto.TurnRequest{ThreadID: "thread-counter", LocalID: "turn-counter"}); err == nil {
		t.Fatal("StartTurn() error = nil, want provider error")
	}

	firstEvents := firstTracer.Query(context.Background(), observability.Query{TraceID: "trace-client-counter"}).Events
	secondEvents := secondTracer.Query(context.Background(), observability.Query{TraceID: "trace-client-counter"}).Events
	if len(firstEvents) != 2 || len(secondEvents) != 1 {
		t.Fatalf("first events = %#v, second events = %#v, want 2 and 1", firstEvents, secondEvents)
	}
	if firstEvents[0].SpanID != secondEvents[0].SpanID || !strings.HasSuffix(firstEvents[0].SpanID, ":1") {
		t.Fatalf("first span = %q, second span = %q, want isolated counters starting at :1", firstEvents[0].SpanID, secondEvents[0].SpanID)
	}
	if !strings.HasSuffix(firstEvents[1].SpanID, ":2") {
		t.Fatalf("wrapped span = %q, want same client counter incremented to :2", firstEvents[1].SpanID)
	}
}
