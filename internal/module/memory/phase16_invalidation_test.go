package memory

import (
	"context"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	shareddto "github.com/anthropic-ai/super-agent-v3/internal/dto/shared"
	tooldto "github.com/anthropic-ai/super-agent-v3/internal/dto/tool"
	turndto "github.com/anthropic-ai/super-agent-v3/internal/dto/turn"
)

// Phase 1.6 wired memory_entrypoint into every durable-write path. These
// tests assert the new invalidation fires; they complement the existing
// extractor-only test in TestMemoryLifecycleHooksExtractAndSaveInvalidatesPromptSections.

func newHooksWithInvalidator(t *testing.T) (*MemoryLifecycleHooks, *sectionInvalidatorStub, string) {
	t.Helper()
	root := filepath.Join(t.TempDir(), "memory-root")
	projectRoot := filepath.Join(t.TempDir(), "project")
	invalidator := &sectionInvalidatorStub{}
	hooks := newMemoryLifecycleHooks(
		&Config{Enabled: true, RootDir: root, ProjectRoot: projectRoot},
		nil,
		nil,
		nil,
		nil,
		invalidator,
		NewMemoryExtractor(),
		NewManifestBuilder(),
	)
	return hooks, invalidator, root
}

func assertInvalidatedEntrypoint(t *testing.T, inv *sectionInvalidatorStub, when string) {
	t.Helper()
	reason, names := inv.snapshot()
	if reason != contract.InvalidateMemoryWrite {
		t.Fatalf("%s: invalidator.reason = %q, want %q", when, reason, contract.InvalidateMemoryWrite)
	}
	if !slices.Contains(names, contract.DynamicSectionMemoryEntrypoint) {
		t.Fatalf("%s: invalidator.names = %#v, want to include %q", when, names, contract.DynamicSectionMemoryEntrypoint)
	}
}

func TestWriteDetectedIntentDiskWriteInvalidatesEntrypoint(t *testing.T) {
	hooks, invalidator, _ := newHooksWithInvalidator(t)
	evt := turndto.TurnCompleted{TurnHeader: shareddto.TurnHeader{
		AgentHeader:  shareddto.AgentHeader{ThreadHeader: shareddto.ThreadHeader{ThreadID: "thread-1"}},
		TurnIDHeader: shareddto.TurnIDHeader{TurnID: "turn-1"},
	}}
	intent := SaveIntent{
		Detected: true,
		Content:  "Use concise commit subjects (Phase 1.6 invalidation test).",
		Type:     MemoryTypeFeedback,
	}
	if err := hooks.writeDetectedIntent(context.Background(), evt, intent); err != nil {
		t.Fatalf("writeDetectedIntent() error = %v", err)
	}
	assertInvalidatedEntrypoint(t, invalidator, "after writeDetectedIntent")
}

func TestDeleteIntentInvalidatesEntrypoint(t *testing.T) {
	hooks, invalidator, _ := newHooksWithInvalidator(t)
	store, err := hooks.diskStore()
	if err != nil {
		t.Fatalf("diskStore() error = %v", err)
	}
	entry := buildExplicitMemoryWrite(SaveIntent{
		Detected: true,
		Content:  "Phase 1.6 deleteIntent fixture content.",
		Type:     MemoryTypeFeedback,
	})
	if _, err := store.CreateStructured(entry); err != nil {
		t.Fatalf("CreateStructured() error = %v", err)
	}
	// Reset invalidator so we only see the delete-time signal, not the
	// fixture-create call (which goes through a different store API).
	invalidator.reason = ""
	invalidator.names = nil

	if err := hooks.deleteIntent(context.Background(), "thread-1", ForgetIntent{
		Detected: true,
		Query:    "Phase 1.6 deleteIntent fixture",
	}); err != nil {
		t.Fatalf("deleteIntent() error = %v", err)
	}
	assertInvalidatedEntrypoint(t, invalidator, "after deleteIntent")
}

func TestOnTurnCompletedAutoMemWriteInvalidatesEntrypoint(t *testing.T) {
	hooks, invalidator, _ := newHooksWithInvalidator(t)
	autoRoot, err := resolvedStoreRoot(hooks.rootDir, hooks.projectRoot, hooks.autoMemPathOverride)
	if err != nil {
		t.Fatalf("resolvedStoreRoot() error = %v", err)
	}
	autoMemFile := filepath.Join(autoRoot, "feedback_phase16.md")

	threadID := "thread-1"
	turnID := "turn-1"
	hooks.onTurnStarted(turnStartedEvent(threadID, turnID))
	hooks.onToolDiffUpdated(tooldto.ToolDiffUpdated{
		ThreadID: threadID,
		Files:    []string{autoMemFile},
	})
	hooks.onTurnCompleted(context.Background(), turnCompletedEvent(threadID, turnID))
	assertInvalidatedEntrypoint(t, invalidator, "after onTurnCompleted with auto-mem write")
}

func TestOnTurnCompletedOutsideAutoMemDoesNotInvalidateEntrypoint(t *testing.T) {
	hooks, invalidator, _ := newHooksWithInvalidator(t)
	threadID := "thread-1"
	turnID := "turn-1"
	hooks.onTurnStarted(turnStartedEvent(threadID, turnID))
	// File outside the auto-mem root → consumeTurnTracking returns false →
	// no invalidation should fire.
	hooks.onToolDiffUpdated(tooldto.ToolDiffUpdated{
		ThreadID: threadID,
		Files:    []string{filepath.Join(t.TempDir(), "outside", "notes.md")},
	})
	hooks.onTurnCompleted(context.Background(), turnCompletedEvent(threadID, turnID))
	reason, names := invalidator.snapshot()
	if reason != "" || len(names) > 0 {
		t.Fatalf("invalidator unexpectedly fired for non-auto-mem write: reason=%q names=%v",
			reason, strings.Join(names, ","))
	}
}
