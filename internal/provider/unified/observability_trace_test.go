package unified

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/observability"
)

type runtimeConfigGenerationSession struct {
	*generationTestSession
	runtime map[string]any
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
