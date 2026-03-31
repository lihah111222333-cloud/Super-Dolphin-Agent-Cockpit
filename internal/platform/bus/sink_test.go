package bus

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"
	"time"

	agentdto "github.com/anthropic-ai/super-agent-v3/internal/dto/agent"
	tooldto "github.com/anthropic-ai/super-agent-v3/internal/dto/tool"
	uidto "github.com/anthropic-ai/super-agent-v3/internal/dto/ui"
	"github.com/kelindar/event"
)

type sinkLogEntry struct {
	Level     string `json:"level"`
	Msg       string `json:"msg"`
	EventType string `json:"event_type"`
}

func TestLogSinkHighFrequencyEventsUseDebugLevel(t *testing.T) {
	t.Parallel()

	dispatcher := NewDispatcher()
	t.Cleanup(func() {
		_ = dispatcher.Close()
	})

	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	sink := NewLogSink(dispatcher, logger)
	t.Cleanup(sink.Close)

	event.Publish(dispatcher, agentdto.StateChanged{})
	event.Publish(dispatcher, tooldto.ToolCallBegin{})
	event.Publish(dispatcher, uidto.UITokensUpdated{})

	entries := waitForBusLogEntries(t, &buf, 3)
	if got := levelForEvent(t, entries, "StateChanged"); got != "INFO" {
		t.Fatalf("StateChanged level = %q, want %q", got, "INFO")
	}
	if got := levelForEvent(t, entries, "ToolCallBegin"); got != "DEBUG" {
		t.Fatalf("ToolCallBegin level = %q, want %q", got, "DEBUG")
	}
	if got := levelForEvent(t, entries, "UITokensUpdated"); got != "DEBUG" {
		t.Fatalf("UITokensUpdated level = %q, want %q", got, "DEBUG")
	}
}

func waitForBusLogEntries(t *testing.T, buf *bytes.Buffer, want int) []sinkLogEntry {
	t.Helper()

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		entries := parseBusLogEntries(buf.String())
		if len(entries) >= want {
			return entries
		}
		time.Sleep(10 * time.Millisecond)
	}

	entries := parseBusLogEntries(buf.String())
	t.Fatalf("bus log entries = %d, want at least %d; raw=%s", len(entries), want, buf.String())
	return nil
}

func parseBusLogEntries(raw string) []sinkLogEntry {
	lines := strings.Split(strings.TrimSpace(raw), "\n")
	entries := make([]sinkLogEntry, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var entry sinkLogEntry
		if json.Unmarshal([]byte(line), &entry) != nil {
			continue
		}
		if entry.Msg == "bus event" {
			entries = append(entries, entry)
		}
	}
	return entries
}

func levelForEvent(t *testing.T, entries []sinkLogEntry, suffix string) string {
	t.Helper()

	for _, entry := range entries {
		if strings.HasSuffix(entry.EventType, suffix) {
			return entry.Level
		}
	}
	t.Fatalf("event suffix %q not found in entries %#v", suffix, entries)
	return ""
}
