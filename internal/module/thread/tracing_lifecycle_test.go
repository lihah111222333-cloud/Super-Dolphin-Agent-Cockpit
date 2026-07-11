package thread

import (
	"context"
	"sync"
	"testing"

	platformobs "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/observability"
)

type threadTraceSink struct {
	mu     sync.Mutex
	events []platformobs.TraceEvent
}

func (s *threadTraceSink) Append(_ context.Context, event platformobs.TraceEvent) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events = append(s.events, event)
	return nil
}

func (s *threadTraceSink) snapshot() []platformobs.TraceEvent {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]platformobs.TraceEvent, len(s.events))
	copy(out, s.events)
	return out
}

func TestThreadStartTraceErrorHasAnchorAndStatus(t *testing.T) {
	t.Parallel()

	sink := &threadTraceSink{}
	svc := NewService(silentLogger(), nil, nil, nil, nil, nil, nil, nil).(*service)
	svc.tracing = platformobs.NewService(mustThreadTraceConfig(t), platformobs.WithSink(sink))
	_, err := svc.Start(context.Background(), StartRequest{Provider: "codex", AgentID: "agent-trace-start"})
	if err == nil {
		t.Fatal("Start() error = nil, want missing cwd error")
	}
	events := threadEventsByKind(sink.snapshot(), "thread.start")
	if len(events) != 2 {
		t.Fatalf("thread.start events = %d, want 2", len(events))
	}
	done := events[1]
	if done.Phase != "error" || done.Status != platformobs.StatusError || done.AgentID != "agent-trace-start" {
		t.Fatalf("thread.start error event = %#v", done)
	}
	if done.Code.File == "" || done.Code.Function == "" || done.Code.Line == 0 {
		t.Fatalf("code anchor missing: %#v", done.Code)
	}
}

func TestThreadSpawnIfNeededTraceDoneForNoop(t *testing.T) {
	t.Parallel()

	sink := &threadTraceSink{}
	store := &stubThreadStore{thread: &ThreadRecord{ThreadID: "thread-trace-spawn", Status: statusStopped, PendingLaunch: true}}
	svc := &service{threadStore: store, tracing: platformobs.NewService(mustThreadTraceConfig(t), platformobs.WithSink(sink))}
	launched, _, err := svc.SpawnIfNeeded(context.Background(), "thread-trace-spawn", "secret user text", "")
	if err != nil {
		t.Fatalf("SpawnIfNeeded() error = %v", err)
	}
	if launched {
		t.Fatal("SpawnIfNeeded() launched = true, want false")
	}
	events := threadEventsByKind(sink.snapshot(), "thread.spawn_if_needed")
	if len(events) != 2 {
		t.Fatalf("thread.spawn_if_needed events = %d, want 2", len(events))
	}
	if events[1].Phase != "done" || events[1].Status != platformobs.StatusOK || events[1].ThreadID != "thread-trace-spawn" {
		t.Fatalf("thread.spawn_if_needed done event = %#v", events[1])
	}
}

func threadEventsByKind(events []platformobs.TraceEvent, kind string) []platformobs.TraceEvent {
	out := make([]platformobs.TraceEvent, 0, len(events))
	for _, event := range events {
		if event.Kind == kind {
			out = append(out, event)
		}
	}
	return out
}

func mustThreadTraceConfig(t *testing.T) platformobs.Config {
	t.Helper()
	cfg, err := platformobs.ParseConfig(platformobs.EnvMap{"OBS_TRACING_ENABLED": "1"})
	if err != nil {
		t.Fatalf("ParseConfig() error = %v", err)
	}
	return cfg
}
