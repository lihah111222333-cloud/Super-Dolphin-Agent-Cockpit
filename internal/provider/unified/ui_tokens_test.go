package unified

import (
	"testing"

	uidto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/ui"
)

func TestPublishUITokensUpdatedPrefersCodexTokenUsageLastForIO(t *testing.T) {
	t.Parallel()

	got := mustPublishTokensUpdated(t, map[string]any{
		"threadId": "thread-codex",
		"turnId":   "turn-codex",
		"tokenUsage": map[string]any{
			"total": map[string]any{
				"inputTokens":  70,
				"outputTokens": 50,
				"totalTokens":  120,
			},
			"last": map[string]any{
				"inputTokens":  7,
				"outputTokens": 5,
			},
		},
		"modelContextWindow": 100,
		"contextWindow":      80,
		"context_window":     60,
	})

	if got.ThreadID != "thread-codex" || got.TurnID != "turn-codex" {
		t.Fatalf("token event identity = %#v", got)
	}
	if got.InputTokens != 7 || got.OutputTokens != 5 {
		t.Fatalf("token event io = %#v", got)
	}
	if got.TotalTokens != 120 || got.ContextWindowTokens != 100 {
		t.Fatalf("token event totals = %#v", got)
	}
}

func TestPublishUITokensUpdatedSupportsLegacyUsagePayload(t *testing.T) {
	t.Parallel()

	got := mustPublishTokensUpdated(t, map[string]any{
		"threadId": "thread-legacy",
		"turnId":   "turn-legacy",
		"usage": map[string]any{
			"inputTokens":         11,
			"outputTokens":        6,
			"totalTokens":         17,
			"contextWindowTokens": 128,
		},
	})

	if got.ThreadID != "thread-legacy" || got.TurnID != "turn-legacy" {
		t.Fatalf("legacy token event identity = %#v", got)
	}
	if got.InputTokens != 11 || got.OutputTokens != 6 {
		t.Fatalf("legacy token event io = %#v", got)
	}
	if got.TotalTokens != 17 || got.ContextWindowTokens != 128 {
		t.Fatalf("legacy token event totals = %#v", got)
	}
}

func TestPublishUITokensUpdatedFallsBackToCodexTokenUsageLast(t *testing.T) {
	t.Parallel()

	got := mustPublishTokensUpdated(t, map[string]any{
		"threadId": "thread-last",
		"turnId":   "turn-last",
		"tokenUsage": map[string]any{
			"last": map[string]any{
				"inputTokens":  9,
				"outputTokens": 4,
				"totalTokens":  13,
			},
		},
	})

	if got.ThreadID != "thread-last" || got.TurnID != "turn-last" {
		t.Fatalf("last token event identity = %#v", got)
	}
	if got.InputTokens != 9 || got.OutputTokens != 4 || got.TotalTokens != 13 {
		t.Fatalf("last token event values = %#v", got)
	}
	if got.ContextWindowTokens != 0 {
		t.Fatalf("last token event window = %#v", got)
	}
}

func mustPublishTokensUpdated(t *testing.T, payload any) uidto.UITokensUpdated {
	t.Helper()

	var published []any
	PublishUITokensUpdated(payload, func(ev any) {
		published = append(published, ev)
	})
	if len(published) != 1 {
		t.Fatalf("published events = %#v", published)
	}
	got, ok := published[0].(uidto.UITokensUpdated)
	if !ok {
		t.Fatalf("published event type = %T", published[0])
	}
	return got
}
