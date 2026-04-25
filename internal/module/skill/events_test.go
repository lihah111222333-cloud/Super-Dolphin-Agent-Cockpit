package skill

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"
	"time"

	uidto "github.com/anthropic-ai/super-agent-v3/internal/dto/ui"
	"github.com/kelindar/event"
)

func TestWriteLocalPublishesSkillsChanged(t *testing.T) {
	dispatcher := event.NewDispatcher()
	defer func() { _ = dispatcher.Close() }()

	got := make(chan uidto.SkillsChanged, 1)
	cancel := event.Subscribe(dispatcher, func(ev uidto.SkillsChanged) { got <- ev })
	defer cancel()

	projectRoot := t.TempDir()
	svc := NewService(projectRoot).(*service)
	svc.root = t.TempDir()
	svc.projectSkillsRoot = defaultProjectSkillsRoot(projectRoot)
	svc.bindDispatcher(dispatcher)
	if _, err := svc.WriteLocal(skillTestContext(projectRoot), "demo-skill", "# Demo", skillScopeProject); err != nil {
		t.Fatalf("WriteLocal() error = %v", err)
	}

	ev := mustReceiveSkillsChanged(t, got)
	if ev.Name != "demo-skill" || ev.Action != "write" || ev.SkillsDir == "" || ev.Count != 1 {
		t.Fatalf("skills changed event = %#v", ev)
	}
	if !reflect.DeepEqual(ev.Actions, []string{"write"}) {
		t.Fatalf("skills changed event = %#v", ev)
	}
}

func TestPublishSkillsChangedDebouncesBurst(t *testing.T) {
	dispatcher := event.NewDispatcher()
	defer func() { _ = dispatcher.Close() }()

	got := make(chan uidto.SkillsChanged, 2)
	cancel := event.Subscribe(dispatcher, func(ev uidto.SkillsChanged) { got <- ev })
	defer cancel()

	svc := NewService("").(*service)
	svc.bindDispatcher(dispatcher)
	svc.publishSkillsChanged(context.Background(), "local_write", "first", skillScopeSystem)
	svc.publishSkillsChanged(context.Background(), "import_dir", "second", skillScopeSystem)

	ev := mustReceiveSkillsChanged(t, got)
	if ev.Action != "" || ev.Name != "" || ev.Count != 2 {
		t.Fatalf("debounced event = %#v", ev)
	}
	if !reflect.DeepEqual(ev.Actions, []string{"write", "import"}) {
		t.Fatalf("debounced event = %#v", ev)
	}

	select {
	case extra := <-got:
		t.Fatalf("unexpected extra skills changed event = %#v", extra)
	case <-time.After(200 * time.Millisecond):
	}
}

func TestPublishSkillsChangedDedupesRepeatedActions(t *testing.T) {
	dispatcher := event.NewDispatcher()
	defer func() { _ = dispatcher.Close() }()

	got := make(chan uidto.SkillsChanged, 2)
	cancel := event.Subscribe(dispatcher, func(ev uidto.SkillsChanged) { got <- ev })
	defer cancel()

	svc := NewService("").(*service)
	svc.bindDispatcher(dispatcher)
	svc.publishSkillsChanged(context.Background(), "local_write", "first", skillScopeSystem)
	svc.publishSkillsChanged(context.Background(), "write", "second", skillScopeSystem)

	ev := mustReceiveSkillsChanged(t, got)
	if ev.Action != "write" || ev.Count != 1 {
		t.Fatalf("deduped event = %#v", ev)
	}
	if !reflect.DeepEqual(ev.Actions, []string{"write"}) {
		t.Fatalf("deduped event = %#v", ev)
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

// P0b Step 6: SkillsChanged JSON round-trip preserves Scope/Cwd.
func TestSkillsChangedJSONRoundTripWithScopeCwd(t *testing.T) {
	original := uidto.SkillsChanged{
		SkillsDir: "/tmp/skills",
		Name:      "demo",
		Action:    "write",
		Actions:   []string{"write"},
		Count:     1,
		Scope:     "project",
		Cwd:       "/tmp/repo-a",
	}
	encoded, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	var decoded uidto.SkillsChanged
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}
	if decoded.Scope != "project" || decoded.Cwd != "/tmp/repo-a" {
		t.Fatalf("round-trip lost scope/cwd: %#v", decoded)
	}
	if decoded.Action != "write" || decoded.Count != 1 {
		t.Fatalf("round-trip lost legacy fields: %#v", decoded)
	}
}

// P0b Step 6: legacy JSON without scope/cwd fields decodes cleanly with empties.
func TestSkillsChangedJSONBackwardCompatibleEmptyScope(t *testing.T) {
	const legacy = `{"action":"write","count":1,"name":"demo"}`
	var decoded uidto.SkillsChanged
	if err := json.Unmarshal([]byte(legacy), &decoded); err != nil {
		t.Fatalf("legacy decode: %v", err)
	}
	if decoded.Scope != "" || decoded.Cwd != "" {
		t.Fatalf("legacy decode populated scope/cwd: %#v", decoded)
	}
	if decoded.Action != "write" || decoded.Name != "demo" || decoded.Count != 1 {
		t.Fatalf("legacy decode lost fields: %#v", decoded)
	}
}

// P0b Step 6: project-scope WriteLocal emits Scope="project" + Cwd populated.
func TestServiceEmitsScopedSkillsChangedProject(t *testing.T) {
	dispatcher := event.NewDispatcher()
	defer func() { _ = dispatcher.Close() }()

	got := make(chan uidto.SkillsChanged, 1)
	cancel := event.Subscribe(dispatcher, func(ev uidto.SkillsChanged) { got <- ev })
	defer cancel()

	projectRoot := t.TempDir()
	svc := NewService(projectRoot).(*service)
	svc.root = t.TempDir()
	svc.projectSkillsRoot = defaultProjectSkillsRoot(projectRoot)
	svc.bindDispatcher(dispatcher)

	if _, err := svc.WriteLocal(skillTestContext(projectRoot), "scope-project", "# hi", skillScopeProject); err != nil {
		t.Fatalf("WriteLocal(project) error = %v", err)
	}
	ev := mustReceiveSkillsChanged(t, got)
	if ev.Scope != "project" {
		t.Fatalf("scope = %q, want project; ev=%#v", ev.Scope, ev)
	}
	if ev.Cwd != projectRoot {
		t.Fatalf("cwd = %q, want %q", ev.Cwd, projectRoot)
	}
}

// P0b Step 6: system-scope publish emits Scope="system" + Cwd="".
func TestServiceEmitsScopedSkillsChangedSystem(t *testing.T) {
	dispatcher := event.NewDispatcher()
	defer func() { _ = dispatcher.Close() }()

	got := make(chan uidto.SkillsChanged, 1)
	cancel := event.Subscribe(dispatcher, func(ev uidto.SkillsChanged) { got <- ev })
	defer cancel()

	svc := NewService("").(*service)
	svc.bindDispatcher(dispatcher)

	svc.publishSkillsChanged(context.Background(), "remote_write", "sysname", skillScopeSystem)

	ev := mustReceiveSkillsChanged(t, got)
	if ev.Scope != "system" {
		t.Fatalf("scope = %q, want system", ev.Scope)
	}
	if ev.Cwd != "" {
		t.Fatalf("cwd = %q, want empty for system scope", ev.Cwd)
	}
}

// P0b Step 6: cross-scope events must not merge into one — the override path
// in mergeSkillsChanged forces the second event to fully replace the first.
func TestServiceCrossScopeOverridesMerge(t *testing.T) {
	dispatcher := event.NewDispatcher()
	defer func() { _ = dispatcher.Close() }()

	got := make(chan uidto.SkillsChanged, 2)
	cancel := event.Subscribe(dispatcher, func(ev uidto.SkillsChanged) { got <- ev })
	defer cancel()

	projectRoot := t.TempDir()
	svc := NewService(projectRoot).(*service)
	svc.root = t.TempDir()
	svc.projectSkillsRoot = defaultProjectSkillsRoot(projectRoot)
	svc.bindDispatcher(dispatcher)

	ctx := skillTestContext(projectRoot)
	svc.publishSkillsChanged(ctx, "local_write", "first", skillScopeProject)
	svc.publishSkillsChanged(context.Background(), "remote_write", "second", skillScopeSystem)

	ev := mustReceiveSkillsChanged(t, got)
	if ev.Scope != "system" {
		t.Fatalf("override scope = %q, want system; ev=%#v", ev.Scope, ev)
	}
	if ev.Name != "second" {
		t.Fatalf("override name = %q, want second", ev.Name)
	}
	if ev.Cwd != "" {
		t.Fatalf("override cwd = %q, want empty (system)", ev.Cwd)
	}
	if ev.Count != 1 {
		t.Fatalf("override count = %d, want 1 (no merge)", ev.Count)
	}

	select {
	case extra := <-got:
		t.Fatalf("unexpected extra skills changed event = %#v", extra)
	case <-time.After(200 * time.Millisecond):
	}
}
