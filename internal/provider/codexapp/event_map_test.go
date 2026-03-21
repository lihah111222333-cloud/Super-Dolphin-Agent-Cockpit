package codexapp

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"

	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
)

func TestTranslateCodexEventWarnsOnUnknownRawEvent(t *testing.T) {
	var buf bytes.Buffer
	old := slog.Default()
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn}))
	slog.SetDefault(logger)
	t.Cleanup(func() { slog.SetDefault(old) })

	translateCodexEvent(dto.RawProviderEvent{
		EventType: "mystery/event",
		Data:      map[string]any{"foo": "bar"},
	}, func(any) {
		t.Fatal("unknown raw event should not publish typed event")
	})

	output := buf.String()
	if !strings.Contains(output, "unknown raw event") {
		t.Fatalf("warn output = %q, want unknown raw event warning", output)
	}
	if !strings.Contains(output, "mystery/event") {
		t.Fatalf("warn output = %q, want raw event type", output)
	}
}
