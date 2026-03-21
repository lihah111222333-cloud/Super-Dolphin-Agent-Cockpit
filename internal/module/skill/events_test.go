package skill

import (
	"context"
	"testing"
	"time"

	uidto "github.com/anthropic-ai/super-agent-v3/internal/dto/ui"
	"github.com/kelindar/event"
)

func TestWriteSkillContentPublishesSkillsChanged(t *testing.T) {
	dispatcher := event.NewDispatcher()
	defer func() { _ = dispatcher.Close() }()

	t.Setenv("CODEX_HOME", t.TempDir())
	got := make(chan uidto.SkillsChanged, 1)
	cancel := event.Subscribe(dispatcher, func(ev uidto.SkillsChanged) { got <- ev })
	defer cancel()

	svc := NewService(nil, "").(*service)
	svc.bindDispatcher(dispatcher)
	if _, err := svc.WriteSkillContent(context.Background(), "demo-skill", "# Demo"); err != nil {
		t.Fatalf("WriteSkillContent() error = %v", err)
	}

	ev := mustReceiveSkillsChanged(t, got)
	if ev.Name != "demo-skill" || ev.Action != "config_write" || ev.SkillsDir == "" {
		t.Fatalf("skills changed event = %#v", ev)
	}
}

func TestPublishSkillsChangedDebouncesBurst(t *testing.T) {
	dispatcher := event.NewDispatcher()
	defer func() { _ = dispatcher.Close() }()

	got := make(chan uidto.SkillsChanged, 2)
	cancel := event.Subscribe(dispatcher, func(ev uidto.SkillsChanged) { got <- ev })
	defer cancel()

	svc := NewService(nil, "").(*service)
	svc.bindDispatcher(dispatcher)
	svc.publishSkillsChanged("local_write", "first")
	svc.publishSkillsChanged("summary_write", "second")

	ev := mustReceiveSkillsChanged(t, got)
	if ev.Action != "summary_write" || ev.Name != "second" {
		t.Fatalf("debounced event = %#v", ev)
	}

	select {
	case extra := <-got:
		t.Fatalf("unexpected extra skills changed event = %#v", extra)
	case <-time.After(200 * time.Millisecond):
	}
}

func mustReceiveSkillsChanged(t *testing.T, ch <-chan uidto.SkillsChanged) uidto.SkillsChanged {
	t.Helper()

	select {
	case ev := <-ch:
		return ev
	case <-time.After(time.Second):
		t.Fatal("expected skills changed event")
		return uidto.SkillsChanged{}
	}
}
