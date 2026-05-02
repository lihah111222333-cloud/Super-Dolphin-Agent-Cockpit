package memory

import (
	"context"
	"testing"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
)

// Phase 4.0 baseline tests: lock down the prompt-section invalidation
// contract on every UI RPC mutation entry. Spike 子项 1 (see p25 Phase
// 4.0a spike report) confirmed the invalidation chain is complete;
// these tests turn that audit into regression guards so a future Phase
// 4.1 / 自有.1 change cannot silently drop the invalidate call.
//
// Convention: assert (reason == InvalidateMemoryWrite) ∧ names ⊇
// expectedSections, **exact-once** (reviewer B upgrade). Durable paths
// invalidate Memory + MemoryContext + MemoryEntrypoint.

//
// Helpers (`sectionSet`, `assertRecordedInvalidation`,
// `assertRecordedNoSections`, `newPhase4UIDeps`, `findEntriesByName`)
// live in `phase4_baseline_helpers_test.go`.

func TestPhase4BaselineUpsertUIMemoryEntryInvalidatesDurableSections(t *testing.T) {
	deps, projectRoot, _ := newPhase4UIDeps(t)
	rec := deps.Sections.(*recordingSectionInvalidator)

	created, err := upsertUIMemoryEntry(context.Background(), deps, uiMemoryEntryUpsertParams{
		CWD:         projectRoot,
		Target:      "private",
		Name:        "Phase4 baseline create",
		Description: "create-side invalidation guard",
		Type:        "reference",
		Content:     "Lock down create-time invalidation as a Phase 4.0 baseline.",
	})
	if err != nil {
		t.Fatalf("upsertUIMemoryEntry(create) error = %v", err)
	}
	assertRecordedInvalidation(t, rec, "after upsert(create)",
		contract.InvalidateMemoryWrite,
		contract.DynamicSectionMemory,
		contract.DynamicSectionMemoryContext,
		contract.DynamicSectionMemoryEntrypoint,
	)

	// Reset and assert update path also invalidates.
	rec.mu.Lock()
	rec.calls = nil
	rec.mu.Unlock()

	if _, err := upsertUIMemoryEntry(context.Background(), deps, uiMemoryEntryUpsertParams{
		CWD:          projectRoot,
		Target:       "private",
		ExistingPath: created.Path,
		Name:         "Phase4 baseline create",
		Description:  "update-side invalidation guard",
		Type:         "reference",
		Content:      "Update path also must invalidate.",
	}); err != nil {
		t.Fatalf("upsertUIMemoryEntry(update) error = %v", err)
	}
	assertRecordedInvalidation(t, rec, "after upsert(update)",
		contract.InvalidateMemoryWrite,
		contract.DynamicSectionMemory,
		contract.DynamicSectionMemoryContext,
		contract.DynamicSectionMemoryEntrypoint,
	)
}

func TestPhase4BaselineDeleteUIMemoryEntryInvalidatesDurableSections(t *testing.T) {
	deps, projectRoot, _ := newPhase4UIDeps(t)
	rec := deps.Sections.(*recordingSectionInvalidator)

	created, err := upsertUIMemoryEntry(context.Background(), deps, uiMemoryEntryUpsertParams{
		CWD:         projectRoot,
		Target:      "private",
		Name:        "Phase4 baseline delete fixture",
		Description: "fixture for delete-time guard",
		Type:        "reference",
		Content:     "Will be deleted.",
	})
	if err != nil {
		t.Fatalf("upsertUIMemoryEntry(create fixture) error = %v", err)
	}

	// Reset so we only see the delete-time signal, not the create.
	rec.mu.Lock()
	rec.calls = nil
	rec.mu.Unlock()

	if err := deleteUIMemoryEntry(context.Background(), deps, uiMemoryEntryDeleteParams{
		CWD:    projectRoot,
		Target: "private",
		Path:   created.Path,
	}); err != nil {
		t.Fatalf("deleteUIMemoryEntry() error = %v", err)
	}
	assertRecordedInvalidation(t, rec, "after delete",
		contract.InvalidateMemoryWrite,
		contract.DynamicSectionMemory,
		contract.DynamicSectionMemoryContext,
		contract.DynamicSectionMemoryEntrypoint,
	)
}
