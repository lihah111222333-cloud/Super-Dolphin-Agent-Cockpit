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
// invalidate Memory + MemoryContext + MemoryEntrypoint; agent paths
// invalidate AgentMemory. Counter-baselines: agent paths must NOT
// touch the durable trio (disjoint reviewer F).
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

func TestPhase4BaselineSaveUIAgentMemoryInvalidatesAgentSection(t *testing.T) {
	projectRoot := t.TempDir()
	cfg := &Config{
		Enabled:     true,
		ProjectRoot: projectRoot,
		RootDir:     t.TempDir(),
	}
	deps := memoryHandlerDeps{
		Service:  newServiceWithConsolidator(cfg, nil, nil, nil),
		Sections: &recordingSectionInvalidator{},
	}
	rec := deps.Sections.(*recordingSectionInvalidator)

	if _, err := saveUIAgentMemory(context.Background(), deps, uiAgentMemorySaveParams{
		CWD:       projectRoot,
		Scope:     "project",
		AgentType: "Phase4Baseline",
		Content:   "Lock down agent-memory save invalidation as a Phase 4.0 baseline.",
	}); err != nil {
		t.Fatalf("saveUIAgentMemory() error = %v", err)
	}
	assertRecordedInvalidation(t, rec, "after saveUIAgentMemory",
		contract.InvalidateMemoryWrite,
		contract.DynamicSectionAgentMemory,
	)
}

func TestPhase4BaselineDeleteUIAgentMemoryInvalidatesAgentSection(t *testing.T) {
	projectRoot := t.TempDir()
	cfg := &Config{
		Enabled:     true,
		ProjectRoot: projectRoot,
		RootDir:     t.TempDir(),
	}
	deps := memoryHandlerDeps{
		Service:  newServiceWithConsolidator(cfg, nil, nil, nil),
		Sections: &recordingSectionInvalidator{},
	}
	rec := deps.Sections.(*recordingSectionInvalidator)

	if _, err := saveUIAgentMemory(context.Background(), deps, uiAgentMemorySaveParams{
		CWD:       projectRoot,
		Scope:     "project",
		AgentType: "Phase4BaselineDelete",
		Content:   "Will be deleted by the baseline test.",
	}); err != nil {
		t.Fatalf("saveUIAgentMemory(fixture) error = %v", err)
	}

	// Reset so we only observe the delete-time signal.
	rec.mu.Lock()
	rec.calls = nil
	rec.mu.Unlock()

	if err := deleteUIAgentMemory(context.Background(), deps, uiAgentMemoryDeleteParams{
		CWD:       projectRoot,
		Scope:     "project",
		AgentType: "Phase4BaselineDelete",
	}); err != nil {
		t.Fatalf("deleteUIAgentMemory() error = %v", err)
	}
	assertRecordedInvalidation(t, rec, "after deleteUIAgentMemory",
		contract.InvalidateMemoryWrite,
		contract.DynamicSectionAgentMemory,
	)
}

// TestPhase4BaselineSaveUIAgentMemoryDoesNotInvalidateDurableSections is
// a disjoint counter-baseline (reviewer F): the agent-memory path must
// NOT bleed into the durable Memory/MemoryContext/MemoryEntrypoint
// sections. Locks the section-name partitioning so a future Phase 4.1
// ranking change that accidentally fans out invalidation across scopes
// will trip this guard.
func TestPhase4BaselineSaveUIAgentMemoryDoesNotInvalidateDurableSections(t *testing.T) {
	projectRoot := t.TempDir()
	cfg := &Config{
		Enabled:     true,
		ProjectRoot: projectRoot,
		RootDir:     t.TempDir(),
	}
	deps := memoryHandlerDeps{
		Service:  newServiceWithConsolidator(cfg, nil, nil, nil),
		Sections: &recordingSectionInvalidator{},
	}
	rec := deps.Sections.(*recordingSectionInvalidator)

	if _, err := saveUIAgentMemory(context.Background(), deps, uiAgentMemorySaveParams{
		CWD:       projectRoot,
		Scope:     "project",
		AgentType: "Phase4DisjointAgent",
		Content:   "Disjoint baseline: agent path must not bleed into durable sections.",
	}); err != nil {
		t.Fatalf("saveUIAgentMemory() error = %v", err)
	}
	assertRecordedNoSections(t, rec, "after saveUIAgentMemory",
		contract.DynamicSectionMemory,
		contract.DynamicSectionMemoryContext,
		contract.DynamicSectionMemoryEntrypoint,
	)
}
