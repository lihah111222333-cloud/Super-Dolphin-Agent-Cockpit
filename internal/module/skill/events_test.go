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

// P0b F12: project-scope WriteLocal emits Scope="project" with repo fingerprint and no absolute cwd.
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
	if ev.Cwd != "" {
		t.Fatalf("cwd leaked absolute path: %#v", ev)
	}
	if ev.RepoFingerprint != RepoFingerprint(projectRoot) || ev.RelativePath != "." {
		t.Fatalf("repo location mismatch; ev=%#v", ev)
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

// P0b F12: cross-scope events must flush as separate events so subscribers can
// attribute each mutation; overriding the first event would drop data.
func TestServiceCrossScopeFlushesBothEvents(t *testing.T) {
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

	first := mustReceiveSkillsChanged(t, got)
	if first.Scope != "project" {
		t.Fatalf("first scope = %q, want project; ev=%#v", first.Scope, first)
	}
	if first.Name != "first" {
		t.Fatalf("first name = %q, want first; ev=%#v", first.Name, first)
	}
	if first.Cwd != "" {
		t.Fatalf("first cwd leaked absolute path; ev=%#v", first)
	}
	if first.RepoFingerprint != RepoFingerprint(projectRoot) || first.RelativePath != "." {
		t.Fatalf("first repo location mismatch; ev=%#v", first)
	}
	if first.Count != 1 || first.Action != "write" {
		t.Fatalf("first action/count mismatch; ev=%#v", first)
	}

	second := mustReceiveSkillsChanged(t, got)
	if second.Scope != "system" {
		t.Fatalf("second scope = %q, want system; ev=%#v", second.Scope, second)
	}
	if second.Name != "second" {
		t.Fatalf("second name = %q, want second; ev=%#v", second.Name, second)
	}
	if second.Cwd != "" {
		t.Fatalf("second cwd = %q, want empty (system); ev=%#v", second.Cwd, second)
	}
	if second.Count != 1 || second.Action != "write" {
		t.Fatalf("second action/count mismatch; ev=%#v", second)
	}

	select {
	case extra := <-got:
		t.Fatalf("unexpected extra skills changed event = %#v", extra)
	case <-time.After(200 * time.Millisecond):
	}
}

// P0b F12: same-scope but cross-repo project events must flush separately.
func TestServiceCrossCwdFlushesBothEvents(t *testing.T) {
	dispatcher := event.NewDispatcher()
	defer func() { _ = dispatcher.Close() }()

	got := make(chan uidto.SkillsChanged, 2)
	cancel := event.Subscribe(dispatcher, func(ev uidto.SkillsChanged) { got <- ev })
	defer cancel()

	projectRootA := t.TempDir()
	projectRootB := t.TempDir()
	svc := NewService(projectRootA).(*service)
	svc.root = t.TempDir()
	svc.projectSkillsRoot = defaultProjectSkillsRoot(projectRootA)
	svc.bindDispatcher(dispatcher)

	svc.publishSkillsChanged(skillTestContext(projectRootA), "local_write", "first", skillScopeProject)
	svc.publishSkillsChanged(skillTestContext(projectRootB), "import_dir", "second", skillScopeProject)

	first := mustReceiveSkillsChanged(t, got)
	if first.Scope != "project" || first.Cwd != "" || first.RepoFingerprint != RepoFingerprint(projectRootA) || first.RelativePath != "." || first.Name != "first" {
		t.Fatalf("first event mismatch; ev=%#v want scope=project fp=%q rel=. name=first", first, RepoFingerprint(projectRootA))
	}
	if first.Count != 1 || first.Action != "write" {
		t.Fatalf("first action/count mismatch; ev=%#v", first)
	}

	second := mustReceiveSkillsChanged(t, got)
	if second.Scope != "project" || second.Cwd != "" || second.RepoFingerprint != RepoFingerprint(projectRootB) || second.RelativePath != "." || second.Name != "second" {
		t.Fatalf("second event mismatch; ev=%#v want scope=project fp=%q rel=. name=second", second, RepoFingerprint(projectRootB))
	}
	if second.Count != 1 || second.Action != "import" {
		t.Fatalf("second action/count mismatch; ev=%#v", second)
	}

	select {
	case extra := <-got:
		t.Fatalf("unexpected extra skills changed event = %#v", extra)
	case <-time.After(200 * time.Millisecond):
	}
}

// P0b F12: events in the same (Scope, RepoFingerprint, RelativePath) bucket should still coalesce.
func TestServiceMergeableEventsStillCoalesce(t *testing.T) {
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
	svc.publishSkillsChanged(ctx, "import_dir", "second", skillScopeProject)

	ev := mustReceiveSkillsChanged(t, got)
	if ev.Scope != "project" || ev.Cwd != "" || ev.RepoFingerprint != RepoFingerprint(projectRoot) || ev.RelativePath != "." {
		t.Fatalf("scope/repo location mismatch; ev=%#v want scope=project fp=%q rel=.", ev, RepoFingerprint(projectRoot))
	}
	if ev.Name != "" || ev.Action != "" || ev.Count != 2 {
		t.Fatalf("coalesced summary mismatch; ev=%#v", ev)
	}
	if !reflect.DeepEqual(ev.Actions, []string{"write", "import"}) {
		t.Fatalf("coalesced actions mismatch; ev=%#v", ev)
	}

	select {
	case extra := <-got:
		t.Fatalf("unexpected extra skills changed event = %#v", extra)
	case <-time.After(200 * time.Millisecond):
	}
}
