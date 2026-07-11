package thread

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/kelindar/event"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
	dto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/provider"
	threaddto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/thread"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/bus"
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
	bindings := &stubBindingStore{binding: &BindingRecord{AgentID: "agent-1", Provider: "codex", ProviderThreadID: "thread-1", CodexThreadID: "thread-1"}}
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

func TestCompactInvalidatesPromptAssemblyAfterSuccess(t *testing.T) {
	t.Parallel()

	promptAssembly := &stubPromptAssemblyService{}
	session := &compactEventSession{stubSession: stubSession{threadID: "thread-1"}}
	sessions := &stubSessionProvider{session: session}
	bindings := &stubBindingStore{binding: &BindingRecord{AgentID: "agent-1", Provider: "codex", ProviderThreadID: "thread-1", CodexThreadID: "thread-1"}}
	svc := NewServiceWithPromptAssembly(silentLogger(), nil, bindings, sessions, nil, nil, nil, nil, promptAssembly, nil, nil)

	if _, err := svc.Compact(context.Background(), "thread-1", ""); err != nil {
		t.Fatalf("Compact() error = %v", err)
	}
	if got := promptAssembly.invalidated; len(got) != 1 || got[0] != contract.InvalidateCompact {
		t.Fatalf("Invalidate calls = %#v, want [%q]", got, contract.InvalidateCompact)
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
