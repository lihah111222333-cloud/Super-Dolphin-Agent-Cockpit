package memory

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/kelindar/event"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
	uidto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/ui"
)

func TestMergeUIMemoryEntriesPreservesDeleteAndRollbackFailures(t *testing.T) {
	projectRoot := newTestGitProjectRoot(t)
	privateRoot := filepath.Join(t.TempDir(), "private")
	cfg := &Config{
		Enabled:             true,
		EnableTools:         true,
		ProjectRoot:         projectRoot,
		RootDir:             t.TempDir(),
		AutoMemPathOverride: privateRoot,
		Features:            MemoryFeatureFlags{TeamMemory: true},
	}
	teamRoot, err := configuredTeamMemRoot(cfg, contract.BuildCtx{CWD: projectRoot})
	if err != nil {
		t.Fatalf("configuredTeamMemRoot() error = %v", err)
	}

	keptPath := filepath.Join(teamRoot, string(MemoryTypeProject), "team-kept.md")
	absorbedPath := filepath.Join(privateRoot, string(MemoryTypeProject), "private-absorbed.md")
	commonBody := "Shared partial failure content common phrase common phrase common phrase.\nWhy: merge validation should pass before durable mutation.\nHow to apply: preserve both delete and rollback failures."
	unsafeOriginal := commonBody + "\nRollback token sk-proj-abcdefghijklmnopqrstuvwxyz1234567890."
	absorbedBody := commonBody + "\nAbsorbed clean marker."
	writeTestTopicFile(t, keptPath, testMemoryEntry("Partial Failure Kept", "team kept", MemoryTypeProject, unsafeOriginal))
	writeTestTopicFile(t, absorbedPath, testMemoryEntry("Partial Failure Absorbed", "private absorbed", MemoryTypeProject, absorbedBody))
	if _, err := UpdateMemoryIndex(teamRoot); err != nil {
		t.Fatalf("UpdateMemoryIndex(teamRoot) error = %v", err)
	}
	if _, err := UpdateMemoryIndex(privateRoot); err != nil {
		t.Fatalf("UpdateMemoryIndex(privateRoot) error = %v", err)
	}
	makeAbsorbedEntryDeleteFail(t, absorbedPath)

	_, err = mergeUIMemoryEntries(context.Background(), memoryHandlerDeps{
		Service: newServiceWithConsolidator(cfg, nil, nil, nil),
	}, uiMemoryEntryMergeParams{
		CWD:               projectRoot,
		TargetA:           "team",
		PathA:             memoryEntryDisplayPath(teamRoot, keptPath),
		TargetB:           "private",
		PathB:             memoryEntryDisplayPath(privateRoot, absorbedPath),
		MergedDescription: "safe merged description",
		MergedContent:     commonBody + "\nSafe merged override.",
	})
	if !errors.Is(err, ErrDurableMemoryMergePartialFailure) {
		t.Fatalf("mergeUIMemoryEntries() error = %v, want %v", err, ErrDurableMemoryMergePartialFailure)
	}
	if !errors.Is(err, errDurableMemoryDeleteFailed) {
		t.Fatalf("mergeUIMemoryEntries() error = %v, want %v", err, errDurableMemoryDeleteFailed)
	}
	if !errors.Is(err, ErrTeamMemSecretDetected) {
		t.Fatalf("mergeUIMemoryEntries() error = %v, want rollback cause %v", err, ErrTeamMemSecretDetected)
	}
}

func TestMergeUIMemoryEntriesRefreshesConsumersAfterDeleteFailure(t *testing.T) {
	projectRoot := newTestGitProjectRoot(t)
	privateRoot := filepath.Join(t.TempDir(), "private")
	cfg := &Config{
		Enabled:             true,
		EnableTools:         true,
		ProjectRoot:         projectRoot,
		RootDir:             t.TempDir(),
		AutoMemPathOverride: privateRoot,
		Features:            MemoryFeatureFlags{TeamMemory: true},
	}
	teamRoot, err := configuredTeamMemRoot(cfg, contract.BuildCtx{CWD: projectRoot})
	if err != nil {
		t.Fatalf("configuredTeamMemRoot() error = %v", err)
	}
	keptPath := filepath.Join(privateRoot, string(MemoryTypeProject), "kept.md")
	absorbedPath := filepath.Join(teamRoot, string(MemoryTypeProject), "absorbed.md")
	commonBody := "Shared refresh content common phrase common phrase common phrase.\nWhy: merge reaches the durable mutation boundary.\nHow to apply: refresh all consumers after rollback."
	writeTestTopicFile(t, keptPath, testMemoryEntry("Refresh Kept", "kept", MemoryTypeProject, commonBody+"\nKept marker."))
	writeTestTopicFile(t, absorbedPath, testMemoryEntry("Refresh Absorbed", "absorbed", MemoryTypeProject, commonBody+"\nAbsorbed marker."))
	if _, err := UpdateMemoryIndex(privateRoot); err != nil {
		t.Fatalf("UpdateMemoryIndex(privateRoot) error = %v", err)
	}
	if _, err := UpdateMemoryIndex(teamRoot); err != nil {
		t.Fatalf("UpdateMemoryIndex(teamRoot) error = %v", err)
	}
	makeAbsorbedEntryDeleteFail(t, absorbedPath)

	dispatcher := event.NewDispatcher()
	t.Cleanup(func() { _ = dispatcher.Close() })
	changed := make(chan uidto.UIMemoryChanged, 1)
	cancel := event.Subscribe(dispatcher, func(ev uidto.UIMemoryChanged) { changed <- ev })
	t.Cleanup(cancel)
	sections := &recordingSectionInvalidator{}

	_, err = mergeUIMemoryEntries(context.Background(), memoryHandlerDeps{
		Service:    newServiceWithConsolidator(cfg, nil, nil, nil),
		Sections:   sections,
		Dispatcher: dispatcher,
	}, uiMemoryEntryMergeParams{
		CWD:     projectRoot,
		TargetA: "private",
		PathA:   memoryEntryDisplayPath(privateRoot, keptPath),
		TargetB: "team",
		PathB:   memoryEntryDisplayPath(teamRoot, absorbedPath),
	})
	if !errors.Is(err, errDurableMemoryDeleteFailed) {
		t.Fatalf("mergeUIMemoryEntries() error = %v, want %v", err, errDurableMemoryDeleteFailed)
	}
	sections.mu.Lock()
	calls := append([]recordedInvalidateCall(nil), sections.calls...)
	sections.mu.Unlock()
	if len(calls) != 1 {
		t.Fatalf("invalidate calls = %d, want 1", len(calls))
	}
	select {
	case ev := <-changed:
		if ev.Action != "merge" {
			t.Fatalf("UIMemoryChanged.Action = %q, want merge", ev.Action)
		}
	case <-time.After(time.Second):
		t.Fatal("UIMemoryChanged event not published after delete failure")
	}
}
