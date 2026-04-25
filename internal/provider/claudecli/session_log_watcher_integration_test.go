package claudecli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/kelindar/event"

	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
	"github.com/anthropic-ai/super-agent-v3/internal/provider/unified"
)

func TestHandleSystemInitRawStartsLogWatcherAndUsesRuntimeContextWindow(t *testing.T) {
	dir := t.TempDir()

	sessionID := "session-1"
	projectDir := filepath.Join(dir, "projects", "proj")
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(%q) error = %v", projectDir, err)
	}
	path := filepath.Join(projectDir, sessionID+".jsonl")
	if err := os.WriteFile(path, append(watcherIntegrationLine(t, sessionID, "2026-04-13T00:00:00Z", 2, 3), '\n'), 0o644); err != nil {
		t.Fatalf("WriteFile(%q) error = %v", path, err)
	}

	bus := event.NewDispatcher()
	defer func() { _ = bus.Close() }()
	dispatcher := unified.NewEventDispatcher(bus, nil)
	RegisterTranslators(dispatcher)

	rawEvents := make(chan dto.BusRawProviderEvent, 8)
	cancel := event.Subscribe(bus, func(ev dto.BusRawProviderEvent) {
		if ev.Event.EventType == "tokens:log_watcher" {
			rawEvents <- ev
		}
	})
	defer cancel()

	tr := &transport{}
	s := &session{
		agentID:         "agent-1",
		threadID:        sessionID,
		publicThreadID:  "thread-public",
		sessionID:       sessionID,
		threadReady:     make(chan struct{}),
		transport:       tr,
		history:         &historyBackend{sessionDir: dir},
		eventDispatcher: dispatcher,
		suppressedTurns: map[string]struct{}{},
	}
	s.markThreadReady()

	s.handleSystemInitRaw(tr, dto.RawProviderEvent{EventType: "system:init", Data: map[string]any{"session_id": sessionID, "context_window": 888}})

	select {
	case ev := <-rawEvents:
		data, _ := ev.Event.Data.(map[string]any)
		if data["thread_id"] != "thread-public" {
			t.Fatalf("thread_id = %#v, want thread-public", data["thread_id"])
		}
		if data["context_window"] != 888 {
			t.Fatalf("context_window = %#v, want 888", data["context_window"])
		}
		if data["input_tokens"] != 2 || data["output_tokens"] != 3 || data["total_tokens"] != 5 {
			t.Fatalf("token payload = %#v, want input=2 output=3 total=5", data)
		}
	case <-time.After(time.Second):
		t.Fatal("tokens:log_watcher event was not published")
	}
}

func TestDispatchTokenUsageIfCurrentRejectsStaleIdentity(t *testing.T) {
	bus := event.NewDispatcher()
	defer func() { _ = bus.Close() }()
	dispatcher := unified.NewEventDispatcher(bus, nil)
	RegisterTranslators(dispatcher)

	rawEvents := make(chan dto.BusRawProviderEvent, 1)
	cancel := event.Subscribe(bus, func(ev dto.BusRawProviderEvent) {
		if ev.Event.EventType == "tokens:log_watcher" {
			rawEvents <- ev
		}
	})
	defer cancel()

	tr := &transport{}
	s := &session{
		threadID:        "session-1",
		publicThreadID:  "thread-public",
		sessionID:       "session-1",
		transport:       tr,
		logWatcherGen:   2,
		eventDispatcher: dispatcher,
		history:         &historyBackend{sessionDir: t.TempDir()},
	}
	s.dispatchTokenUsageIfCurrent(tr, logWatcherIdentity{sessionID: "other", threadID: "other"}, 2, sessionLogUsage{Timestamp: "2026-04-13T00:00:00Z", InputTokens: 1, OutputTokens: 1, TotalTokens: 2})
	select {
	case ev := <-rawEvents:
		t.Fatalf("unexpected raw event = %#v", ev)
	case <-time.After(100 * time.Millisecond):
	}
}

func watcherIntegrationLine(t *testing.T, sessionID, timestamp string, input, output int) []byte {
	t.Helper()
	raw, err := json.Marshal(map[string]any{
		"type":      "assistant",
		"sessionId": sessionID,
		"timestamp": timestamp,
		"message": map[string]any{
			"usage": map[string]any{
				"input_tokens":  input,
				"output_tokens": output,
			},
		},
	})
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	return raw
}
