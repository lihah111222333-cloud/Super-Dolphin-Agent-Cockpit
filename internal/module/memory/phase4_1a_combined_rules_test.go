package memory

import (
	"bytes"
	"log/slog"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// Phase 4.1a baseline tests for combined-mode prompt rules + cross-scope
// same-name warn. Driven by reviewer feedback on Phase 4.1 plan:
//   - 子项 1+2: combined-only type-specific access guidance for feedback
//     and project memory types (rules.go combinedAccessRules end).
//   - 子项 3.1: combined save rules pre-check directive for same-name
//     feedback in the other scope (rules.go combinedSaveRules end).
//   - 子项 3.3: cross-scope same-name warn fires once per name (dedup'd
//     via the shared memory coordinator).

//
// Phase 4.1b (ranking scope boost + reviewer G P1.3 ranking pipeline) is
// out of scope here — it requires a PrefetchManager double-root refactor
// that single-root prefetch.go does not currently support.

func TestPhase4_1aCombinedFeedbackAccessLine(t *testing.T) {
	t.Parallel()
	engine := NewMemoryRuleEngine()
	out := engine.LoadMemoryPrompt(MemoryModeCombined, true, MemoryRuleOptions{
		AutoMemPath: "/tmp/auto",
		TeamMemPath: "/tmp/team",
	})
	if out == nil {
		t.Fatal("LoadMemoryPrompt(combined) returned nil")
	}
	const want = "next contributor working on this project"
	if !strings.Contains(*out, want) {
		t.Fatalf("combined prompt missing feedback access line %q:\n%s", want, *out)
	}
}

func TestPhase4_1aCombinedProjectAccessLine(t *testing.T) {
	t.Parallel()
	engine := NewMemoryRuleEngine()
	out := engine.LoadMemoryPrompt(MemoryModeCombined, true, MemoryRuleOptions{
		AutoMemPath: "/tmp/auto",
		TeamMemPath: "/tmp/team",
	})
	if out == nil {
		t.Fatal("LoadMemoryPrompt(combined) returned nil")
	}
	const want = "flag breaking changes for collaborators"
	if !strings.Contains(*out, want) {
		t.Fatalf("combined prompt missing project access line %q:\n%s", want, *out)
	}
}

func TestPhase4_1aCombinedSaveCrossScopePreCheck(t *testing.T) {
	t.Parallel()
	engine := NewMemoryRuleEngine()
	out := engine.LoadMemoryPrompt(MemoryModeCombined, true, MemoryRuleOptions{
		AutoMemPath: "/tmp/auto",
		TeamMemPath: "/tmp/team",
	})
	if out == nil {
		t.Fatal("LoadMemoryPrompt(combined) returned nil")
	}
	const want = "first scan the already-injected"
	if !strings.Contains(*out, want) {
		t.Fatalf("combined prompt missing cross-scope pre-check directive %q:\n%s", want, *out)
	}
}

func TestPhase4_1aStandardModeUnaffected(t *testing.T) {
	// Counter-baseline: type-specific combined-only lines must NOT leak
	// into standard mode. Standard mode has its own #### feedback access
	// line ("guide behavior so the user does not need to repeat ...") that
	// is intentionally narrower in scope.
	t.Parallel()
	engine := NewMemoryRuleEngine()
	out := engine.LoadMemoryPrompt(MemoryModeStandard, true, MemoryRuleOptions{})
	if out == nil {
		t.Fatal("LoadMemoryPrompt(standard) returned nil")
	}
	for _, leak := range []string{
		"next contributor working on this project",
		"flag breaking changes for collaborators",
		"first scan the already-injected",
	} {
		if strings.Contains(*out, leak) {
			t.Fatalf("standard mode leaked combined-only line %q:\n%s", leak, *out)
		}
	}
}

func TestPhase4_1aWarnCrossScopeSameNameTriggers(t *testing.T) {
	t.Parallel()
	primary, secondary, sharedName := newCrossScopeFixture(t, true /*bothHave*/)
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	hooks := newTestHooks(withLogger(logger))
	hooks.warnCrossScopeSameName(sharedName, primary, primary, secondary, "private", "team")
	got := buf.String()
	if !strings.Contains(got, "memory cross-scope same-name entry detected") {
		t.Fatalf("expected warn log, got:\n%s", got)
	}
	if !strings.Contains(got, sharedName) {
		t.Fatalf("warn log missing name=%q:\n%s", sharedName, got)
	}
	// Necessary: structured fields selected_scope + other_scope must be
	// present so operators can grep which scope drove the divergence.
	if !strings.Contains(got, "selected_scope=private") {
		t.Fatalf("warn log missing selected_scope=private:\n%s", got)
	}
	if !strings.Contains(got, "other_scope=team") {
		t.Fatalf("warn log missing other_scope=team:\n%s", got)
	}
	// Dedupe: second call with same name must not emit another warn line.
	bufBefore := buf.Len()
	hooks.warnCrossScopeSameName(sharedName, primary, primary, secondary, "private", "team")
	if buf.Len() != bufBefore {
		t.Fatalf("dedupe failed: second call re-emitted log:\n%s", buf.String()[bufBefore:])
	}
}

func TestPhase4_1aWarnCrossScopeSameNameNoSecondaryHit(t *testing.T) {
	// Counter-baseline: when only the selected store has the entry,
	// warnCrossScopeSameName must stay silent (no spurious warn).
	t.Parallel()
	primary, secondary, onlyName := newCrossScopeFixture(t, false /*bothHave*/)
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	hooks := newTestHooks(withLogger(logger))
	hooks.warnCrossScopeSameName(onlyName, primary, primary, secondary, "private", "team")
	if got := buf.String(); strings.Contains(got, "cross-scope same-name") {
		t.Fatalf("unexpected warn when only primary has entry:\n%s", got)
	}
}

// Phase 4.1a 3.3 follow-up (reviewer post-impl): under N-goroutine
// concurrent invocation, dedupe must still emit exactly one warn line.
// sync.Map.LoadOrStore is documented atomic, but this test pins the
// invariant against future regressions (e.g. someone replacing the map
// with a non-atomic check-then-set).
func TestPhase4_1aWarnCrossScopeSameNameConcurrentDedup(t *testing.T) {
	t.Parallel()
	primary, secondary, sharedName := newCrossScopeFixture(t, true)
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	hooks := newTestHooks(withLogger(logger))
	const concurrency = 20
	var wg sync.WaitGroup
	wg.Add(concurrency)
	for i := 0; i < concurrency; i++ {
		go func() {
			defer wg.Done()
			hooks.warnCrossScopeSameName(sharedName, primary, primary, secondary, "private", "team")
		}()
	}
	wg.Wait()
	count := strings.Count(buf.String(), "memory cross-scope same-name entry detected")
	if count != 1 {
		t.Fatalf("concurrent dedupe: warn count = %d, want 1\noutput:\n%s", count, buf.String())
	}
}

// Phase 4.1a 3.3 follow-up (reviewer post-impl): the warn helper is by
// design wired only into writeIntent (explicit-write paths). The delete
// path goes through deleteMemoryAcrossStores directly and must NOT
// pollute cross-scope warn dedupe or emit the warn log. If a future refactor
// accidentally adds warn to delete, this counter-baseline fails.

func TestPhase4_1aDeletePathDoesNotWarn(t *testing.T) {
	t.Parallel()
	primary, secondary, sharedName := newCrossScopeFixture(t, true)
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	coordinator := newDiskLockCoordinator()
	_ = newTestHooks(withLogger(logger), withLocks(coordinator))
	if err := deleteMemoryAcrossStores(sharedName, WriteOptions{}, primary, secondary); err != nil {
		t.Fatalf("deleteMemoryAcrossStores() error = %v", err)
	}

	if got := buf.String(); strings.Contains(got, "cross-scope same-name") {
		t.Fatalf("delete path unexpectedly emitted cross-scope warn:\n%s", got)
	}
	if !coordinator.markCrossScopeSameNameWarned(sharedName) {
		t.Fatalf("coordinator cross-scope warn dedupe polluted by delete path: name=%q", sharedName)
	}

}

// newCrossScopeFixture creates two disjoint disk stores. When bothHave is
// true, the same-name entry is created in both; otherwise only in primary.
// Returns (primary, secondary, sharedName).
func newCrossScopeFixture(t *testing.T, bothHave bool) (memoryStructuredStore, memoryStructuredStore, string) {
	t.Helper()
	primaryRoot := filepath.Join(t.TempDir(), "primary")
	secondaryRoot := filepath.Join(t.TempDir(), "secondary")
	primary, err := newDiskStore(primaryRoot, nil)
	if err != nil {
		t.Fatalf("newDiskStore(primary) error = %v", err)
	}
	secondary, err := newDiskStore(secondaryRoot, nil)
	if err != nil {
		t.Fatalf("newDiskStore(secondary) error = %v", err)
	}
	sharedName := "Phase4_1a cross-scope fixture"
	req := MemoryWriteRequest{
		Name:        sharedName,
		Description: "cross-scope warn fixture",
		Type:        MemoryTypeFeedback,
		Body:        "rule\nWhy: drive cross-scope detection.\nHow to apply: warn when both stores carry this entry.",
	}
	if _, err := primary.CreateStructured(req); err != nil {
		t.Fatalf("CreateStructured(primary) error = %v", err)
	}
	if bothHave {
		if _, err := secondary.CreateStructured(req); err != nil {
			t.Fatalf("CreateStructured(secondary) error = %v", err)
		}
	}
	return primary, secondary, sharedName
}
