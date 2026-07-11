package claudecli

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
	agentdto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/agent"

	"github.com/kelindar/event"

	dto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/provider"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/provider/unified"
)

func TestHandleSystemInitRawRecordsProviderSessionUUID(t *testing.T) {
	const sessionID = "11111111-2222-3333-4444-555555555555"
	recovery := &recordingSessionRecoveryReporter{}
	tr := &transport{}
	s := &session{
		agentID:         "agent-1",
		threadID:        "agent-1",
		publicThreadID:  "agent-1",
		sessionID:       "agent-1",
		threadReady:     make(chan struct{}),
		transport:       tr,
		recovery:        recovery,
		suppressedTurns: map[string]struct{}{},
	}
	defer func() {
		if tr.done != nil {
			close(tr.done)
		}
	}()

	s.handleSystemInitRaw(tr, dto.RawProviderEvent{EventType: "system:init", Data: map[string]any{"session_id": sessionID, "timestamp": "2026-04-13T00:00:00Z"}})

	if recovery.recordAgentID != "agent-1" || recovery.recordSessionUUID != sessionID {
		t.Fatalf("recorded recovery = (%q,%q), want (agent-1,%s)", recovery.recordAgentID, recovery.recordSessionUUID, sessionID)
	}
}

func TestHandleSystemInitRawStartsLogWatcherAndUsesRuntimeContextWindow(t *testing.T) {
	dir := t.TempDir()
	sessionID := "11111111-2222-3333-4444-555555555555"
	writeWatcherIntegrationLog(t, dir, sessionID)

	bus := event.NewDispatcher()
	defer func() { _ = bus.Close() }()
	dispatcher := unified.NewEventDispatcher(bus, nil)
	RegisterTranslators(dispatcher)

	rawEvents := subscribeLogWatcherEvents(t, bus)

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

	s.handleSystemInitRaw(tr, dto.RawProviderEvent{EventType: "system:init", Data: map[string]any{"session_id": sessionID, "context_window": 888, "timestamp": "2026-04-13T00:00:00Z"}})
	assertLogWatcherEvent(t, rawEvents)
}

func writeWatcherIntegrationLog(t *testing.T, dir, sessionID string) {
	t.Helper()
	projectDir := filepath.Join(dir, "projects", "proj")
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(%q) error = %v", projectDir, err)
	}
	path := filepath.Join(projectDir, sessionID+".jsonl")
	if err := os.WriteFile(path, append(watcherIntegrationLine(t, sessionID, "2026-04-13T00:00:00Z", 2, 3), '\n'), 0o644); err != nil {
		t.Fatalf("WriteFile(%q) error = %v", path, err)
	}
}

func subscribeLogWatcherEvents(t *testing.T, bus *event.Dispatcher) <-chan dto.BusRawProviderEvent {
	t.Helper()
	rawEvents := make(chan dto.BusRawProviderEvent, 8)
	cancel := event.Subscribe(bus, func(ev dto.BusRawProviderEvent) {
		if ev.Event.EventType == "tokens:log_watcher" {
			rawEvents <- ev
		}
	})
	t.Cleanup(cancel)
	return rawEvents
}

func assertLogWatcherEvent(t *testing.T, rawEvents <-chan dto.BusRawProviderEvent) {
	t.Helper()
	select {
	case ev := <-rawEvents:
		data, _ := ev.Event.Data.(map[string]any)
		assertWatcherPayload(t, data)
	case <-time.After(2 * time.Second):
		t.Fatal("tokens:log_watcher event was not published")
	}
}

func assertWatcherPayload(t *testing.T, data map[string]any) {
	t.Helper()
	if data["thread_id"] != "thread-public" {
		t.Fatalf("thread_id = %#v, want thread-public", data["thread_id"])
	}
	if data["context_window"] != 888 {
		t.Fatalf("context_window = %#v, want 888", data["context_window"])
	}
	if data["input_tokens"] != 2 || data["output_tokens"] != 3 || data["total_tokens"] != 5 {
		t.Fatalf("token payload = %#v, want input=2 output=3 total=5", data)
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
		threadID:        "11111111-2222-3333-4444-555555555555",
		publicThreadID:  "thread-public",
		sessionID:       "11111111-2222-3333-4444-555555555555",
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

func TestDispatchTokenUsageIfCurrentPublishesProviderErrorForBadTimestamp(t *testing.T) {
	for _, tc := range []struct {
		name      string
		timestamp string
		wantCode  string
	}{
		{name: "missing", wantCode: "claude_log_missing_timestamp"},
		{name: "invalid", timestamp: "not-a-time", wantCode: "claude_log_invalid_timestamp"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rawEvents, agentErrors, session, tr := newLogWatcherTimestampErrorTest(t)
			session.dispatchTokenUsageIfCurrent(tr, logWatcherIdentity{sessionID: session.sessionID, threadID: session.threadID}, 2, sessionLogUsage{Timestamp: tc.timestamp, InputTokens: 1, OutputTokens: 1, TotalTokens: 2})
			got := requireLogWatcherAgentError(t, agentErrors)
			if got.Code != tc.wantCode {
				t.Fatalf("AgentError.Code = %q, want %s", got.Code, tc.wantCode)
			}
			select {
			case ev := <-rawEvents:
				t.Fatalf("unexpected token event for bad timestamp = %#v", ev)
			case <-time.After(100 * time.Millisecond):
			}
		})
	}
}

func newLogWatcherTimestampErrorTest(t *testing.T) (<-chan dto.BusRawProviderEvent, <-chan agentdto.AgentError, *session, *transport) {
	t.Helper()
	bus := event.NewDispatcher()
	t.Cleanup(func() { _ = bus.Close() })
	dispatcher := unified.NewEventDispatcher(bus, nil)
	RegisterTranslators(dispatcher)

	rawEvents := make(chan dto.BusRawProviderEvent, 1)
	agentErrors := make(chan agentdto.AgentError, 1)
	cancelRaw := event.Subscribe(bus, func(ev dto.BusRawProviderEvent) {
		if ev.Event.EventType == "tokens:log_watcher" {
			rawEvents <- ev
		}
	})
	t.Cleanup(cancelRaw)
	cancelErr := event.Subscribe(bus, func(ev agentdto.AgentError) { agentErrors <- ev })
	t.Cleanup(cancelErr)

	tr := &transport{}
	s := &session{
		threadID:        "11111111-2222-3333-4444-555555555555",
		publicThreadID:  "thread-public",
		sessionID:       "11111111-2222-3333-4444-555555555555",
		transport:       tr,
		logWatcherGen:   2,
		eventDispatcher: dispatcher,
		history:         &historyBackend{sessionDir: t.TempDir()},
	}
	return rawEvents, agentErrors, s, tr
}

func requireLogWatcherAgentError(t *testing.T, ch <-chan agentdto.AgentError) agentdto.AgentError {
	t.Helper()
	select {
	case ev := <-ch:
		return ev
	case <-time.After(time.Second):
		t.Fatal("AgentError was not published")
		return agentdto.AgentError{}
	}
}

type recordingSessionRecoveryReporter struct {
	recordAgentID     string
	recordSessionUUID string
	clearStaleAgentID string
}

func (r *recordingSessionRecoveryReporter) ClearStaleProviderThreadID(_ context.Context, agentID string) error {
	r.clearStaleAgentID = agentID
	return nil
}

func (r *recordingSessionRecoveryReporter) RecordProviderSessionUUID(_ context.Context, agentID, sessionUUID string) error {
	r.recordAgentID = agentID
	r.recordSessionUUID = sessionUUID
	return nil
}

var _ contract.SessionRecoveryReporter = (*recordingSessionRecoveryReporter)(nil)

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
