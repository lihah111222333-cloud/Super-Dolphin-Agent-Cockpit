package turn

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
	platformobs "github.com/anthropic-ai/super-agent-v3/internal/platform/observability"
)

type turnTraceSink struct {
	mu     sync.Mutex
	events []platformobs.TraceEvent
}

func (s *turnTraceSink) Append(_ context.Context, event platformobs.TraceEvent) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events = append(s.events, event)
	return nil
}

func (s *turnTraceSink) snapshot() []platformobs.TraceEvent {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]platformobs.TraceEvent, len(s.events))
	copy(out, s.events)
	return out
}

func TestTurnPrepareTraceCountsOnlyAndNoPromptPayload(t *testing.T) {
	t.Parallel()

	sink := &turnTraceSink{}
	svc := NewServiceWithPromptAssembly(silentLogger(), &stubPromptAssemblyService{}).(*service)
	svc.tracing = platformobs.NewService(mustTurnTraceConfig(t), platformobs.WithSink(sink))
	session := &stubSession{threadID: "thread-trace-1"}
	secretPrompt := "user secret memory payload"
	req, err := svc.PrepareTurn(context.Background(), session, PrepareInput{
		Prompt:          secretPrompt,
		Inputs:          []InputItem{{Type: "text", Content: "input secret"}},
		Files:           []string{"README.md"},
		Images:          []string{"https://example.test/a.png"},
		Skills:          []dto.SkillRef{{Name: "skill-a", Prompt: "skill prompt secret"}},
		CandidateSkills: []dto.SkillRef{{Name: "skill-b", Prompt: "candidate prompt secret"}},
	})
	if err != nil {
		t.Fatalf("PrepareTurn() error = %v", err)
	}

	events := eventsByKind(sink.snapshot(), "turn.prepare")
	if len(events) != 2 {
		t.Fatalf("turn.prepare events = %d, want 2", len(events))
	}
	done := events[len(events)-1]
	if done.Phase != "done" || done.Status != platformobs.StatusOK || done.DurationMS < 0 {
		t.Fatalf("done event = %#v", done)
	}
	assertPreparedTurnID(t, done, req.LocalID)
	assertMetadataCount(t, done.Metadata, "input_count", 1)
	assertMetadataCount(t, done.Metadata, "file_count", 1)
	assertMetadataCount(t, done.Metadata, "image_count", 1)
	assertMetadataCount(t, done.Metadata, "skill_count", 2)
	assertMetadataPresent(t, done.Metadata, "manifest_tool_count")
	raw, _ := json.Marshal(events)
	text := string(raw)
	assertTraceExcludes(t, text, secretPrompt, "input secret", "skill prompt secret", "candidate prompt secret")
	if done.Code.File == "" || done.Code.Function == "" || done.Code.Line == 0 {
		t.Fatalf("code anchor missing: %#v", done.Code)
	}
}

func TestTurnStartAndWatchTraceStatus(t *testing.T) {
	t.Parallel()

	sink := &turnTraceSink{}
	svc := NewServiceWithPromptAssembly(silentLogger(), &stubPromptAssemblyService{}).(*service)
	svc.tracing = platformobs.NewService(mustTurnTraceConfig(t), platformobs.WithSink(sink))
	handle := newStubTurnHandle("turn-local-1", "provider-turn-1")
	session := &stubSession{threadID: "thread-trace-2", startTurn: func(context.Context, dto.TurnRequest) (contract.TurnHandle, error) {
		return handle, nil
	}}
	_, err := svc.StartTurn(context.Background(), session, dto.TurnRequest{LocalID: "turn-local-1", ThreadID: "thread-trace-2"})
	if err != nil {
		t.Fatalf("StartTurn() error = %v", err)
	}
	handle.complete(nil)
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if len(eventsByKind(sink.snapshot(), "turn.watch.completed")) >= 2 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	startEvents := eventsByKind(sink.snapshot(), "turn.start")
	if len(startEvents) != 2 || startEvents[1].Status != platformobs.StatusOK || startEvents[1].TurnID != "turn-local-1" {
		t.Fatalf("turn.start events = %#v", startEvents)
	}
	watchEvents := eventsByKind(sink.snapshot(), "turn.watch.completed")
	if len(watchEvents) != 2 || watchEvents[1].Status != platformobs.StatusOK || watchEvents[1].ThreadID != "thread-trace-2" {
		t.Fatalf("turn.watch.completed events = %#v", watchEvents)
	}
}

func assertPreparedTurnID(t *testing.T, event platformobs.TraceEvent, want string) {
	t.Helper()
	if event.TurnID != want || event.TurnID == "" {
		t.Fatalf("turn.prepare turn_id = %q, want prepared local id %q", event.TurnID, want)
	}
}

func assertTraceExcludes(t *testing.T, text string, forbidden ...string) {
	t.Helper()
	for _, value := range forbidden {
		if strings.Contains(text, value) {
			t.Fatalf("trace contains forbidden payload %q: %s", value, text)
		}
	}
}

func eventsByKind(events []platformobs.TraceEvent, kind string) []platformobs.TraceEvent {
	out := make([]platformobs.TraceEvent, 0, len(events))
	for _, event := range events {
		if event.Kind == kind {
			out = append(out, event)
		}
	}
	return out
}

func assertMetadataCount(t *testing.T, metadata map[string]any, key string, want int) {
	t.Helper()
	got := assertMetadataPresent(t, metadata, key)
	if metadataInt(got) != want {
		t.Fatalf("metadata[%s] = %v, want %d", key, got, want)
	}
}

func assertMetadataPresent(t *testing.T, metadata map[string]any, key string) any {
	t.Helper()
	got, ok := metadata[key]
	if !ok {
		t.Fatalf("metadata[%s] missing in %#v", key, metadata)
	}
	return got
}

func metadataInt(value any) int {
	switch typed := value.(type) {
	case int:
		return typed
	case int64:
		return int(typed)
	case float64:
		return int(typed)
	default:
		return -1
	}
}

func mustTurnTraceConfig(t *testing.T) platformobs.Config {
	t.Helper()
	cfg, err := platformobs.ParseConfig(platformobs.EnvMap{"OBS_TRACING_ENABLED": "1"})
	if err != nil {
		t.Fatalf("ParseConfig() error = %v", err)
	}
	return cfg
}
