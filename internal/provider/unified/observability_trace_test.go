package unified

import (
	"testing"

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
