package thread

import (
	"context"
	"strings"
	"testing"
	"time"

	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
	threaddto "github.com/anthropic-ai/super-agent-v3/internal/dto/thread"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/bus"
	bindingstore "github.com/anthropic-ai/super-agent-v3/internal/store/binding"
	"github.com/kelindar/event"
)

func TestCompactPublishesCompactedEvent(t *testing.T) {
	t.Parallel()

	dispatcher := event.NewDispatcher()
	defer func() { _ = dispatcher.Close() }()

	got := make(chan threaddto.Compacted, 1)
	cancel := event.Subscribe(dispatcher, func(ev threaddto.Compacted) { got <- ev })
	defer cancel()

	session := &compactEventSession{stubSession: stubSession{threadID: "thread-1"}}
	sessions := &stubSessionProvider{session: session}
	bindings := &stubBindingStore{binding: &bindingstore.Binding{AgentID: "agent-1", Provider: "codex", ProviderThreadID: "thread-1", CodexThreadID: "thread-1"}}
	svc := NewService(silentLogger(), nil, bindings, sessions, nil, nil, nil, bus.NewThreadEmitters(dispatcher))

	result, err := svc.Compact(context.Background(), "thread-1", "")
	if err != nil {
		t.Fatalf("Compact() error = %v", err)
	}
	if !result.Compacted {
		t.Fatalf("Compact() result = %#v, want compacted=true", result)
	}

	ev := mustReceiveCompacted(t, got)
	if ev.ThreadID != "thread-1" || ev.AfterTokens >= ev.BeforeTokens {
		t.Fatalf("compacted event = %#v", ev)
	}
}

type compactEventSession struct {
	stubSession
	compacted bool
}

func (s *compactEventSession) CompactThread(context.Context, string, string) error {
	s.compacted = true
	return nil
}

func (s *compactEventSession) ReadHistory(context.Context, string, int) ([]dto.Message, error) {
	size := 160
	if s.compacted {
		size = 32
	}
	return []dto.Message{{Content: strings.Repeat("x", size)}}, nil
}

func mustReceiveCompacted(t *testing.T, ch <-chan threaddto.Compacted) threaddto.Compacted {
	t.Helper()

	select {
	case ev := <-ch:
		return ev
	case <-time.After(time.Second):
		t.Fatal("expected compacted event")
		return threaddto.Compacted{}
	}
}
