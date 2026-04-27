package memory

import (
	"path/filepath"
	"testing"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	retrieval "github.com/anthropic-ai/super-agent-v3/internal/module/memory/retrieval"
)

// Phase 4.0 baseline test helpers — extracted from
// phase4_baseline_invalidation_test.go / phase4_baseline_team_test.go so
// the assertion plumbing stays in one place. Reviewer F flagged that
// keeping helpers next to the first user couples them to that file's
// lifecycle (rename / split would break cross-file callers).
//
// CONTRACT NOTE — exact-once vs last-call ⊇ split (reviewer F):
//   * UI RPC mutation paths use `recordingSectionInvalidator` (records
//     the full call slice). Helper `assertRecordedInvalidation` enforces
//     exact-once: callers MUST reset rec.calls before the path under
//     test, and exactly one matching call must remain.
//   * Consolidation paths use `sectionInvalidatorStub` (last-write-wins:
//     `s.reason = reason`, `s.names = names`). The consolidator may
//     trigger invalidate multiple times within one RunConsolidation
//     (lifecycle hooks + autoDream); only the last call survives.
//     Tests there assert reason==InvalidateMemoryWrite ∧ names⊇expected
//     directly via `snapshot()` — exact-once is mathematically
//     unavailable on that stub and would be wrong contract anyway.
//
// Both stubs share the same wire format (reason + section names); the
// difference is purely how many calls each retains.

func sectionSet(names []string) map[string]struct{} {
	out := make(map[string]struct{}, len(names))
	for _, n := range names {
		out[n] = struct{}{}
	}
	return out
}

// assertRecordedInvalidation enforces the exact-once contract on the
// UI RPC stub. Callers must reset rec.calls before exercising the path
// under test; helper then asserts that the recorder saw exactly one
// call matching (reason, names⊇wantSections). Reviewer B's upgrade from
// the earlier OR semantics ("any historical match passes") so future
// setup paths that pre-warm invalidations cannot silently swallow
// regressions.
func assertRecordedInvalidation(
	t *testing.T,
	rec *recordingSectionInvalidator,
	when string,
	wantReason contract.InvalidateReason,
	wantSections ...string,
) {
	t.Helper()
	rec.mu.Lock()
	calls := append([]recordedInvalidateCall(nil), rec.calls...)
	rec.mu.Unlock()
	matchCount := 0
	for _, call := range calls {
		if call.reason != wantReason {
			continue
		}
		got := sectionSet(call.names)
		all := true
		for _, want := range wantSections {
			if _, ok := got[want]; !ok {
				all = false
				break
			}
		}
		if all {
			matchCount++
		}
	}
	if matchCount != 1 {
		t.Fatalf("%s: matchCount=%d, want exactly 1; reason=%q sections⊇%v; calls=%#v",
			when, matchCount, wantReason, wantSections, calls)
	}
}

// assertRecordedNoSections asserts that the recorder NEVER recorded an
// invalidation that contains any of the disallowedSections. Used for
// disjoint counter-baselines (e.g. agent-memory paths must not bleed
// into durable Memory/MemoryContext/MemoryEntrypoint sections).
func assertRecordedNoSections(
	t *testing.T,
	rec *recordingSectionInvalidator,
	when string,
	disallowedSections ...string,
) {
	t.Helper()
	rec.mu.Lock()
	calls := append([]recordedInvalidateCall(nil), rec.calls...)
	rec.mu.Unlock()
	for _, call := range calls {
		got := sectionSet(call.names)
		for _, banned := range disallowedSections {
			if _, ok := got[banned]; ok {
				t.Fatalf("%s: invalidation leaked disallowed section %q; call=%#v", when, banned, call)
			}
		}
	}
}

func newPhase4UIDeps(t *testing.T) (memoryHandlerDeps, string, string) {
	t.Helper()
	projectRoot := t.TempDir()
	privateRoot := filepath.Join(t.TempDir(), "private")
	cfg := &Config{
		Enabled:             true,
		EnableTools:         true,
		RootDir:             t.TempDir(),
		ProjectRoot:         projectRoot,
		AutoMemPathOverride: privateRoot,
	}
	deps := memoryHandlerDeps{
		Service:  newServiceWithConsolidator(cfg, nil, nil, nil),
		Sections: &recordingSectionInvalidator{},
	}
	return deps, projectRoot, privateRoot
}

// findEntriesByName returns all manifest entries with a matching
// frontmatter Name (case-sensitive). Used by the cross-scope fixture
// baseline to assert presence without coupling to BuildManifest's exact
// entry count (which can include auxiliary files like the index).
func findEntriesByName(entries []retrieval.MemoryEntry, name string) []retrieval.MemoryEntry {
	out := make([]retrieval.MemoryEntry, 0, 1)
	for _, e := range entries {
		if e.Frontmatter.Name == name {
			out = append(out, e)
		}
	}
	return out
}
