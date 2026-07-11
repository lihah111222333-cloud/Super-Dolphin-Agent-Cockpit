package bus

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kelindar/event"
	agentdto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/agent"
	shareddto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/shared"
	threaddto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/thread"
	tooldto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/tool"
	turndto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/turn"
	uidto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/ui"
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

func TestLogSinkLogsSafeEventPreview(t *testing.T) {
	dispatcher := NewDispatcher()
	t.Cleanup(func() { _ = dispatcher.Close() })

	var buf lockedBuffer
	logger := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	sink := mustNewLogSink(t, LogSinkDeps{Dispatcher: dispatcher, Logger: logger})
	t.Cleanup(sink.Close)

	event.Publish(dispatcher, tooldto.ToolCallBegin{ArgumentsPreview: `{"token":"sk-abcdefghijklmnopqrstuvwxyz"}`})

	_ = waitForBusLogEntries(t, &buf, 1)
	raw := buf.String()
	if strings.Contains(raw, "sk-") {
		t.Fatalf("bus log leaked raw secret: %s", raw)
	}
	if strings.Contains(raw, "event_preview") || strings.Contains(raw, "ArgumentsPreview") {
		t.Fatalf("bus log persisted event preview payload: %s", raw)
	}
	if !strings.Contains(raw, "event_summary") {
		t.Fatalf("bus log = %s, want safe event_summary", raw)
	}
	if strings.Contains(raw, `"event":`) {
		t.Fatalf("bus log persisted raw event object: %s", raw)
	}
}

// TestBusLogArgsDoNotIncludeEventPreview 锁住 bus 日志字段契约。
// 生产日志必须写 allowlist summary，而不是可包含任意 payload 的 event_preview。
func TestBusLogArgsDoNotIncludeEventPreview(t *testing.T) {
	args := busEventLogArgs(tooldto.ToolCallBegin{ArgumentsPreview: `{"token":"sk-l05-secret"}`})
	if logArgsContainKey(args, "event_preview") {
		t.Fatalf("busEventLogArgs() = %#v, want no event_preview field", args)
	}
	if !logArgsContainKey(args, "event_summary") {
		t.Fatalf("busEventLogArgs() = %#v, want event_summary field", args)
	}
}

// TestBusSafeSummaryOmitsCWDPromptAndDelta 覆盖真实 DTO 的敏感字段。
// cwd、用户 prompt 文本和流式 delta 都不应通过 bus 结构化日志落盘。
func TestBusSafeSummaryOmitsCWDPromptAndDelta(t *testing.T) {
	dispatcher := NewDispatcher()
	t.Cleanup(func() { _ = dispatcher.Close() })

	var buf lockedBuffer
	logger := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	sink := mustNewLogSink(t, LogSinkDeps{Dispatcher: dispatcher, Logger: logger})
	t.Cleanup(sink.Close)

	header := shareddto.AgentHeader{
		ThreadHeader: shareddto.ThreadHeader{ThreadID: "thread-1"},
		AgentID:      "agent-1",
	}
	event.Publish(dispatcher, agentdto.AgentLaunched{
		AgentSessionHeader: shareddto.AgentSessionHeader{AgentHeader: header, SessionID: "session-1"},
		CWD:                "/Users/l05/private/project",
		Model:              "sonnet",
	})
	event.Publish(dispatcher, turndto.TurnInputReceived{
		TurnHeader: shareddto.TurnHeader{AgentHeader: header, TurnIDHeader: shareddto.TurnIDHeader{TurnID: "turn-1"}},
		InputType:  "text",
		Text:       "prompt with api key sk-l05-prompt",
	})
	event.Publish(dispatcher, turndto.TurnOutputDelta{
		TurnHeader: shareddto.TurnHeader{AgentHeader: header, TurnIDHeader: shareddto.TurnIDHeader{TurnID: "turn-1"}},
		Stream:     "assistant",
		Delta:      "streaming answer with sk-l05-delta",
	})

	_ = waitForBusLogEntries(t, &buf, 3)
	raw := buf.String()
	for _, forbidden := range []string{
		"/Users/l05/private/project",
		"prompt with api key",
		"streaming answer",
		"sk-l05-prompt",
		"sk-l05-delta",
		`"cwd"`,
		`"Text"`,
		`"delta"`,
		"event_preview",
	} {
		if strings.Contains(raw, forbidden) {
			t.Fatalf("bus log leaked %q: %s", forbidden, raw)
		}
	}
	for _, required := range []string{"event_summary", "thread-1", "agent-1", "turn-1"} {
		if !strings.Contains(raw, required) {
			t.Fatalf("bus log missing safe summary field %q: %s", required, raw)
		}
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

// logArgsContainKey 判断 slog attr 列表里是否包含指定字段。
func logArgsContainKey(args []any, key string) bool {
	for _, arg := range args {
		attr, ok := arg.(slog.Attr)
		if !ok {
			continue
		}
		if attr.Key == key {
			return true
		}
	}
	return false
}
