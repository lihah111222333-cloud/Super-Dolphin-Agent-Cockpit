package memory

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
)

// Phase 4.0 baseline tests for the consolidation paths (spike 1 report
// flagged these as needing explicit invalidation assertions): RunConsolidation
// (sync, service.go:152-170) ends with `dreamHooks.invalidateMemorySections()`;
// launchAutoDreamTask (async via runtimesafe.SafeGo, auto_dream_task.go:281)
// calls `h.invalidateMemorySections()` after consolidation succeeds. Both
// must invalidate Memory + MemoryContext + MemoryEntrypoint per successful
// run.
//
// CONTRACT NOTE — last-call ⊇ vs exact-once (reviewer F): consolidation
// uses `sectionInvalidatorStub` which is last-write-wins (`s.reason =
// reason`, `s.names = names`). The consolidator may invalidate multiple
// times within one RunConsolidation (lifecycle hooks + autoDream). Tests
// here assert reason==InvalidateMemoryWrite ∧ names⊇expected on the
// LAST recorded call — exact-once is mathematically unavailable on this
// stub and would be the wrong contract anyway. UI RPC paths use the
// recording stub + exact-once; see `phase4_baseline_invalidation_test.go`.
// Failure counter-baseline: extractFn returning err must NOT trigger
// invalidate (no partial-write noise).

func newPhase4ConsolidationFixture(t *testing.T) (*MemoryLifecycleHooks, *sectionInvalidatorStub, string) {
	t.Helper()
	root := newTestMemoryRoot(t)
	// One pre-existing fixture entry so the consolidator has something to
	// reason about; the ExtractFunc returns an empty memories list so the
	// run is a no-op rewrite, which is enough to drive invalidation.
	writeExtractFixture(t, filepath.Join(root, "feedback", "phase4-baseline.md"), testMemoryEntry(
		"Phase4 baseline fixture",
		"baseline",
		MemoryTypeFeedback,
		"Phase4 baseline fixture\nWhy: drive consolidation through to invalidate.",
	))
	writeMemoryIndexFixture(t, root, "- [Phase4 baseline fixture](feedback/phase4-baseline.md)")
	extractFn := func(context.Context, string) (string, error) {
		return `{"memories":[]}`, nil
	}
	invalidator := &sectionInvalidatorStub{}
	hooks := newMemoryLifecycleHooks(
		&Config{Enabled: true, RootDir: root},
		// consolidator carries its own extractFn so that the service-side
		// RunConsolidation path (which passes extractFn=nil and relies on
		// resolveExtractFunc) can drive consolidation through.
		newAutoDreamConsolidator(NewMemoryExtractor(), extractFn),
		nil,
		nil,
		nil,
		invalidator,
		NewMemoryExtractor(),
		NewManifestBuilder(),
	)
	hooks.extractFn = extractFn
	return hooks, invalidator, root
}

func TestPhase4BaselineRunConsolidationInvalidatesDurableSections(t *testing.T) {
	hooks, invalidator, _ := newPhase4ConsolidationFixture(t)
	svc := newServiceWithConsolidator(hooks.cfg, nil, hooks.consolidator, hooks)
	if err := svc.RunConsolidation(context.Background()); err != nil {
		t.Fatalf("RunConsolidation() error = %v", err)
	}
	reason, names := invalidator.snapshot()
	if reason != contract.InvalidateMemoryWrite {
		t.Fatalf("invalidator.reason = %q, want %q", reason, contract.InvalidateMemoryWrite)
	}
	requiredSections := []string{
		contract.DynamicSectionMemory,
		contract.DynamicSectionMemoryContext,
		contract.DynamicSectionMemoryEntrypoint,
	}
	got := sectionSet(names)
	for _, want := range requiredSections {
		if _, ok := got[want]; !ok {
			t.Fatalf("invalidator.names = %#v, want to include %q", names, want)
		}
	}
}

// TestPhase4BaselineRunConsolidationFailureDoesNotInvalidate is a
// counter-baseline (reviewer F): when the extract function returns an
// error, RunConsolidation must NOT trigger invalidate — the on-disk
// state has not changed, so signalling a write would propagate stale
// noise downstream.
func TestPhase4BaselineRunConsolidationFailureDoesNotInvalidate(t *testing.T) {
	root := newTestMemoryRoot(t)
	writeExtractFixture(t, filepath.Join(root, "feedback", "phase4-fail.md"), testMemoryEntry(
		"Phase4 failure baseline",
		"baseline",
		MemoryTypeFeedback,
		"Phase4 failure baseline\nWhy: drive consolidation through, then fail.",
	))
	failingExtractFn := func(context.Context, string) (string, error) {
		return "", errors.New("phase4 baseline injected extract failure")
	}
	invalidator := &sectionInvalidatorStub{}
	hooks := newMemoryLifecycleHooks(
		&Config{Enabled: true, RootDir: root},
		newAutoDreamConsolidator(NewMemoryExtractor(), failingExtractFn),
		nil,
		nil,
		nil,
		invalidator,
		NewMemoryExtractor(),
		NewManifestBuilder(),
	)
	hooks.extractFn = failingExtractFn
	svc := newServiceWithConsolidator(hooks.cfg, nil, hooks.consolidator, hooks)
	if err := svc.RunConsolidation(context.Background()); err == nil {
		t.Fatal("RunConsolidation() = nil, want error from failing extractFn")
	}
	reason, names := invalidator.snapshot()
	if reason != "" || len(names) != 0 {
		t.Fatalf("failure path leaked invalidate: reason=%q names=%#v", reason, names)
	}
}

func TestPhase4BaselineLaunchAutoDreamTaskInvalidatesDurableSections(t *testing.T) {
	root := newTestMemoryRoot(t)
	now := time.Date(2026, 4, 15, 9, 0, 0, 0, time.UTC)
	writeExtractFixture(t, filepath.Join(root, "feedback", "phase4-baseline.md"), testMemoryEntry(
		"Phase4 baseline fixture",
		"baseline",
		MemoryTypeFeedback,
		"Phase4 baseline fixture\nWhy: drive auto-dream consolidation through to invalidate.",
	))
	writeMemoryIndexFixture(t, root, "- [Phase4 baseline fixture](feedback/phase4-baseline.md)")
	if err := recordConsolidation(root, now.Add(-48*time.Hour)); err != nil {
		t.Fatalf("recordConsolidation() error = %v", err)
	}
	store := &autoDreamThreadStoreStub{
		thread: newAutoDreamRootThread(t, "thread-current", now, map[string]any{"threadKind": "main"}),
		threads: append(
			autoDreamOtherRootThreads(t, now, 5),
			*newAutoDreamRootThread(t, "thread-current", now, map[string]any{"threadKind": "main"}),
		),
	}
	invalidator := &sectionInvalidatorStub{}
	hooks := newMemoryLifecycleHooks(
		&Config{Enabled: true, RootDir: root},
		NewAutoDreamConsolidator(NewMemoryExtractor()),
		nil,
		nil,
		store,
		invalidator,
		NewMemoryExtractor(),
		NewManifestBuilder(),
	)
	hooks.timeNow = func() time.Time { return now }
	hooks.extractFn = func(context.Context, string) (string, error) {
		return `{"memories":[]}`, nil
	}
	started, err := hooks.maybeScheduleAutoDream(context.Background(), "thread-current")
	if err != nil {
		t.Fatalf("maybeScheduleAutoDream() error = %v", err)
	}
	if !started {
		t.Fatal("maybeScheduleAutoDream() = false, want true with five other sessions + 48h since last consolidation")
	}
	waitCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := hooks.waitDreamTask(waitCtx); err != nil {
		t.Fatalf("waitDreamTask() error = %v", err)
	}
	reason, names := invalidator.snapshot()
	if reason != contract.InvalidateMemoryWrite {
		t.Fatalf("invalidator.reason = %q, want %q", reason, contract.InvalidateMemoryWrite)
	}
	requiredSections := []string{
		contract.DynamicSectionMemory,
		contract.DynamicSectionMemoryContext,
		contract.DynamicSectionMemoryEntrypoint,
	}
	got := sectionSet(names)
	for _, want := range requiredSections {
		if _, ok := got[want]; !ok {
			t.Fatalf("invalidator.names = %#v, want to include %q", names, want)
		}
	}
}

// TestPhase4BaselineLaunchAutoDreamTaskFailureDoesNotInvalidate is the
// async counterpart to RunConsolidationFailureDoesNotInvalidate (reviewer
// G follow-up): when the auto-dream task fails inside SafeGo, the
// `if err != nil { return }` short-circuit at auto_dream_task.go:291-296
// must skip invalidate. Without this counter, a future bug that swaps
// the order of error handling and invalidateMemorySections would silently
// pass the success-path baseline.
func TestPhase4BaselineLaunchAutoDreamTaskFailureDoesNotInvalidate(t *testing.T) {
	root := newTestMemoryRoot(t)
	now := time.Date(2026, 4, 15, 9, 0, 0, 0, time.UTC)
	writeExtractFixture(t, filepath.Join(root, "feedback", "phase4-dream-fail.md"), testMemoryEntry(
		"Phase4 dream failure baseline",
		"baseline",
		MemoryTypeFeedback,
		"Phase4 dream failure baseline\nWhy: drive auto-dream through, then fail.",
	))
	if err := recordConsolidation(root, now.Add(-48*time.Hour)); err != nil {
		t.Fatalf("recordConsolidation() error = %v", err)
	}
	store := &autoDreamThreadStoreStub{
		thread: newAutoDreamRootThread(t, "thread-current", now, map[string]any{"threadKind": "main"}),
		threads: append(
			autoDreamOtherRootThreads(t, now, 5),
			*newAutoDreamRootThread(t, "thread-current", now, map[string]any{"threadKind": "main"}),
		),
	}
	invalidator := &sectionInvalidatorStub{}
	hooks := newMemoryLifecycleHooks(
		&Config{Enabled: true, RootDir: root},
		NewAutoDreamConsolidator(NewMemoryExtractor()),
		nil,
		nil,
		store,
		invalidator,
		NewMemoryExtractor(),
		NewManifestBuilder(),
	)
	hooks.timeNow = func() time.Time { return now }
	hooks.extractFn = func(context.Context, string) (string, error) {
		return "", errors.New("phase4 baseline injected dream extract failure")
	}
	started, err := hooks.maybeScheduleAutoDream(context.Background(), "thread-current")
	if err != nil {
		t.Fatalf("maybeScheduleAutoDream() error = %v", err)
	}
	if !started {
		t.Fatal("maybeScheduleAutoDream() = false, want true with five other sessions + 48h since last consolidation")
	}
	waitCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := hooks.waitDreamTask(waitCtx); err != nil {
		t.Fatalf("waitDreamTask() error = %v", err)
	}
	reason, names := invalidator.snapshot()
	if reason != "" || len(names) != 0 {
		t.Fatalf("dream failure path leaked invalidate: reason=%q names=%#v", reason, names)
	}
}
