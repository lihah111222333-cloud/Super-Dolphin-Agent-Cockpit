package skill

import (
	"context"
	"encoding/json"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/kelindar/event"
	uidto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/ui"
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
	if ev.Name != "demo-skill" || ev.Action != "write" || ev.SkillsDir != "" || ev.Count != 1 {
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
	releaseFlush := blockSkillsChangedFlushForTest(t, svc)
	svc.publishSkillsChanged(context.Background(), "local_write", "first", skillScopePersonal)
	svc.publishSkillsChanged(context.Background(), "import_dir", "second", skillScopePersonal)
	flushSkillsChangedNowForTest(svc)
	releaseFlush()

	ev := mustReceiveSkillsChanged(t, got)
	if ev.Action != "" || ev.Name != "" || ev.Count != 2 {
		t.Fatalf("debounced event = %#v", ev)
	}
	if !reflect.DeepEqual(ev.Actions, []string{"write", "import"}) {
		t.Fatalf("debounced event = %#v", ev)
	}

	assertNoExtraSkillsChanged(t, got)
}

func TestPublishSkillsChangedDedupesRepeatedActions(t *testing.T) {
	dispatcher := event.NewDispatcher()
	defer func() { _ = dispatcher.Close() }()

	got := make(chan uidto.SkillsChanged, 2)
	cancel := event.Subscribe(dispatcher, func(ev uidto.SkillsChanged) { got <- ev })
	defer cancel()

	svc := NewService("").(*service)
	svc.bindDispatcher(dispatcher)
	releaseFlush := blockSkillsChangedFlushForTest(t, svc)
	svc.publishSkillsChanged(context.Background(), "local_write", "first", skillScopePersonal)
	svc.publishSkillsChanged(context.Background(), "write", "second", skillScopePersonal)
	flushSkillsChangedNowForTest(svc)
	releaseFlush()

	ev := mustReceiveSkillsChanged(t, got)
	if ev.Action != "write" || ev.Count != 1 {
		t.Fatalf("deduped event = %#v", ev)
	}
	if !reflect.DeepEqual(ev.Actions, []string{"write"}) {
		t.Fatalf("deduped event = %#v", ev)
	}

	assertNoExtraSkillsChanged(t, got)
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

func assertNoExtraSkillsChanged(t *testing.T, ch <-chan uidto.SkillsChanged) {
	t.Helper()
	select {
	case extra := <-ch:
		t.Fatalf("unexpected extra skills changed event = %#v", extra)
	case <-time.After(200 * time.Millisecond):
	}
}

func assertSkillsEventBasics(t *testing.T, label string, ev uidto.SkillsChanged, scope, name, action string, count int) {
	t.Helper()
	if ev.Scope != scope {
		t.Fatalf("%s scope = %q, want %q; ev=%#v", label, ev.Scope, scope, ev)
	}
	if ev.Name != name {
		t.Fatalf("%s name = %q, want %q; ev=%#v", label, ev.Name, name, ev)
	}
	if ev.Count != count || ev.Action != action {
		t.Fatalf("%s action/count mismatch; ev=%#v", label, ev)
	}
}

func assertProjectRepoEvent(t *testing.T, label string, ev uidto.SkillsChanged, projectRoot string) {
	t.Helper()
	if ev.Cwd != "" {
		t.Fatalf("%s cwd leaked absolute path; ev=%#v", label, ev)
	}
	if ev.RepoFingerprint != RepoFingerprint(projectRoot) || ev.RelativePath != "." {
		t.Fatalf("%s repo location mismatch; ev=%#v want fp=%q rel=.", label, ev, RepoFingerprint(projectRoot))
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

// P0b Step 6: personal-scope publish emits Scope="personal" + Cwd="".
func TestServiceEmitsScopedSkillsChangedPersonal(t *testing.T) {
	dispatcher := event.NewDispatcher()
	defer func() { _ = dispatcher.Close() }()

	got := make(chan uidto.SkillsChanged, 1)
	cancel := event.Subscribe(dispatcher, func(ev uidto.SkillsChanged) { got <- ev })
	defer cancel()

	svc := NewService("").(*service)
	svc.bindDispatcher(dispatcher)

	svc.publishSkillsChanged(context.Background(), "remote_write", "sysname", skillScopePersonal)

	ev := mustReceiveSkillsChanged(t, got)
	if ev.Scope != "personal" {
		t.Fatalf("scope = %q, want personal", ev.Scope)
	}
	if ev.Cwd != "" {
		t.Fatalf("cwd = %q, want empty for personal scope", ev.Cwd)
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
	releaseFlush := blockSkillsChangedFlushForTest(t, svc)

	ctx := skillTestContext(projectRoot)
	svc.publishSkillsChanged(ctx, "local_write", "first", skillScopeProject)
	svc.publishSkillsChanged(context.Background(), "remote_write", "second", skillScopePersonal)
	flushSkillsChangedNowForTest(svc)
	releaseFlush()

	first := mustReceiveSkillsChanged(t, got)
	assertSkillsEventBasics(t, "first", first, "project", "first", "write", 1)
	assertProjectRepoEvent(t, "first", first, projectRoot)

	second := mustReceiveSkillsChanged(t, got)
	assertSkillsEventBasics(t, "second", second, "personal", "second", "write", 1)
	if second.Cwd != "" {
		t.Fatalf("second cwd = %q, want empty (personal); ev=%#v", second.Cwd, second)
	}

	assertNoExtraSkillsChanged(t, got)
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
	releaseFlush := blockSkillsChangedFlushForTest(t, svc)

	svc.publishSkillsChanged(skillTestContext(projectRootA), "local_write", "first", skillScopeProject)
	svc.publishSkillsChanged(skillTestContext(projectRootB), "import_dir", "second", skillScopeProject)
	flushSkillsChangedNowForTest(svc)
	releaseFlush()

	first := mustReceiveSkillsChanged(t, got)
	assertSkillsEventBasics(t, "first", first, "project", "first", "write", 1)
	assertProjectRepoEvent(t, "first", first, projectRootA)

	second := mustReceiveSkillsChanged(t, got)
	assertSkillsEventBasics(t, "second", second, "project", "second", "import", 1)
	assertProjectRepoEvent(t, "second", second, projectRootB)

	assertNoExtraSkillsChanged(t, got)
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
	releaseFlush := blockSkillsChangedFlushForTest(t, svc)

	ctx := skillTestContext(projectRoot)
	svc.publishSkillsChanged(ctx, "local_write", "first", skillScopeProject)
	svc.publishSkillsChanged(ctx, "import_dir", "second", skillScopeProject)
	flushSkillsChangedNowForTest(svc)
	releaseFlush()

	ev := mustReceiveSkillsChanged(t, got)
	assertProjectRepoEvent(t, "coalesced", ev, projectRoot)
	assertSkillsEventBasics(t, "coalesced", ev, "project", "", "", 2)
	if ev.Name != "" || ev.Action != "" || ev.Count != 2 {
		t.Fatalf("coalesced summary mismatch; ev=%#v", ev)
	}
	if !reflect.DeepEqual(ev.Actions, []string{"write", "import"}) {
		t.Fatalf("coalesced actions mismatch; ev=%#v", ev)
	}

	assertNoExtraSkillsChanged(t, got)
}

func blockSkillsChangedFlushForTest(t *testing.T, svc *service) func() {
	t.Helper()
	release := make(chan struct{})
	var once sync.Once
	svc.skillsChangedDelay = func() { <-release }
	releaseFlush := func() { once.Do(func() { close(release) }) }
	t.Cleanup(releaseFlush)
	return releaseFlush
}

func flushSkillsChangedNowForTest(svc *service) {
	svc.skillsChangedMu.Lock()
	seq := svc.skillsChangedSeq
	svc.skillsChangedMu.Unlock()
	svc.flushSkillsChanged(seq)
}
