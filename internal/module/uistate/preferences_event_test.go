package uistate

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/kelindar/event"
	uidto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/ui"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/bus"
)

func TestSetPreferencePublishesPreferenceChangedEvent(t *testing.T) {
	t.Parallel()

	dispatcher := bus.NewDispatcher()
	svc := &service{
		state:         UIState{},
		fallbackPrefs: map[string]json.RawMessage{},
		patchSeq:      map[string]int64{},
	}
	svc.bindDispatcher(dispatcher)

	events := make(chan uidto.UIPreferencesChanged, 1)
	cancel := event.Subscribe(dispatcher, func(ev uidto.UIPreferencesChanged) {
		events <- ev
	})
	defer cancel()

	err := svc.SetPreference(withPreferenceScope(context.Background(), "/tmp/project"), " settings.provider.active ", "claude")
	if err != nil {
		t.Fatalf("SetPreference() error = %v", err)
	}

	select {
	case ev := <-events:
		if ev.Cwd != "/tmp/project" {
			t.Fatalf("event cwd = %q, want %q", ev.Cwd, "/tmp/project")
		}
		if ev.Key != "settings.provider.active" {
			t.Fatalf("event key = %q, want %q", ev.Key, "settings.provider.active")
		}
		if ev.Value != "claude" {
			t.Fatalf("event value = %#v, want %q", ev.Value, "claude")
		}
	case <-time.After(time.Second):
		t.Fatal("SetPreference() did not publish UIPreferencesChanged")
	}
}
