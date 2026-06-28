package bus

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	agentdto "github.com/anthropic-ai/super-agent-v3/internal/dto/agent"
	threaddto "github.com/anthropic-ai/super-agent-v3/internal/dto/thread"
	tooldto "github.com/anthropic-ai/super-agent-v3/internal/dto/tool"
	turndto "github.com/anthropic-ai/super-agent-v3/internal/dto/turn"
	uidto "github.com/anthropic-ai/super-agent-v3/internal/dto/ui"
	"github.com/kelindar/event"
)

type sinkLogEntry struct {
	Level     string `json:"level"`
	Msg       string `json:"msg"`
	EventType string `json:"event_type"`
}

type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func TestLogSinkHighFrequencyEventsUseDebugLevel(t *testing.T) {
	t.Parallel()

	dispatcher := NewDispatcher()
	t.Cleanup(func() {
		_ = dispatcher.Close()
	})

	var buf lockedBuffer
	logger := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	sink := mustNewLogSink(t, LogSinkDeps{Dispatcher: dispatcher, Logger: logger})
	t.Cleanup(sink.Close)

	event.Publish(dispatcher, agentdto.StateChanged{})
	event.Publish(dispatcher, threaddto.MessagesPage{})
	event.Publish(dispatcher, turndto.ItemStarted{})
	event.Publish(dispatcher, turndto.ItemCompleted{})
	event.Publish(dispatcher, tooldto.ToolCallBegin{})
	event.Publish(dispatcher, uidto.UITokensUpdated{})

	entries := waitForBusLogEntries(t, &buf, 6)
	if got := levelForEvent(t, entries, "StateChanged"); got != "INFO" {
		t.Fatalf("StateChanged level = %q, want %q", got, "INFO")
	}
	if got := levelForEvent(t, entries, "MessagesPage"); got != "DEBUG" {
		t.Fatalf("MessagesPage level = %q, want %q", got, "DEBUG")
	}
	if got := levelForEvent(t, entries, "ItemStarted"); got != "DEBUG" {
		t.Fatalf("ItemStarted level = %q, want %q", got, "DEBUG")
	}
	if got := levelForEvent(t, entries, "ItemCompleted"); got != "DEBUG" {
		t.Fatalf("ItemCompleted level = %q, want %q", got, "DEBUG")
	}
	if got := levelForEvent(t, entries, "ToolCallBegin"); got != "DEBUG" {
		t.Fatalf("ToolCallBegin level = %q, want %q", got, "DEBUG")
	}
	if got := levelForEvent(t, entries, "UITokensUpdated"); got != "DEBUG" {
		t.Fatalf("UITokensUpdated level = %q, want %q", got, "DEBUG")
	}
}

func TestNewLogSinkRejectsNilDependencies(t *testing.T) {
	t.Parallel()

	dispatcher := NewDispatcher()
	t.Cleanup(func() {
		_ = dispatcher.Close()
	})
	logger := slog.New(slog.DiscardHandler)

	tests := []struct {
		name string
		deps LogSinkDeps
		want string
	}{
		{
			name: "nil dispatcher",
			deps: LogSinkDeps{Logger: logger},
			want: "nil dispatcher",
		},
		{
			name: "nil logger",
			deps: LogSinkDeps{Dispatcher: dispatcher},
			want: "nil logger",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sink, err := NewLogSink(tt.deps)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("NewLogSink() error = %v, want substring %q", err, tt.want)
			}
			if sink != nil {
				t.Fatalf("NewLogSink() sink = %#v, want nil on invalid deps", sink)
			}
		})
	}
}

func mustNewLogSink(t *testing.T, deps LogSinkDeps) *LogSink {
	t.Helper()
	sink, err := NewLogSink(deps)
	if err != nil {
		t.Fatalf("NewLogSink() error = %v", err)
	}
	return sink
}

func waitForBusLogEntries(t *testing.T, buf *lockedBuffer, want int) []sinkLogEntry {
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
