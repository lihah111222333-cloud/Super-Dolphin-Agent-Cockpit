package memory

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	shared "github.com/anthropic-ai/super-agent-v3/internal/dto/shared"
	turndto "github.com/anthropic-ai/super-agent-v3/internal/dto/turn"
)

func TestRememberIntentWritesImmediatelyFromUserInput(t *testing.T) {
	root := filepath.Join(t.TempDir(), "memory-root")
	projectRoot := filepath.Join(t.TempDir(), "project")
	hooks := newMemoryLifecycleHooks(&Config{
		Enabled:     true,
		RootDir:     root,
		ProjectRoot: projectRoot,
	}, nil, nil, nil, nil, nil, nil, nil)

	ev := userTurnInputEvent("thread-1", "turn-1", "记住：你偏好简洁直接的回复风格。")
	hooks.onTurnInputReceived(context.Background(), ev)
	hooks.onTurnEnd(context.Background(), turndto.TurnCompleted{TurnHeader: ev.TurnHeader, Success: true})

	storeRoot, err := resolvedStoreRoot(root, projectRoot, "")
	if err != nil {
		t.Fatalf("resolvedStoreRoot() error = %v", err)
	}
	entries, err := scanMemoryEntries(storeRoot)
	if err != nil {
		t.Fatalf("scanMemoryEntries() error = %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("scanMemoryEntries() entries = %d, want 1", len(entries))
	}
	if got, want := entries[0].Content, "你偏好简洁直接的回复风格。"; got != want {
		t.Fatalf("Content = %q, want %q", got, want)
	}
	if got, want := entries[0].Type(), MemoryTypeUser; got != want {
		t.Fatalf("Type = %q, want %q", got, want)
	}
	if got := readIndexEntries(t, storeRoot); len(got) != 1 {
		t.Fatalf("ReadMemoryIndex() entries = %d, want 1", len(got))
	}
}

func TestRememberIntentHonorsSkipIndex(t *testing.T) {
	root := filepath.Join(t.TempDir(), "memory-root")
	projectRoot := filepath.Join(t.TempDir(), "project")
	hooks := newMemoryLifecycleHooks(&Config{
		Enabled:     true,
		SkipIndex:   true,
		RootDir:     root,
		ProjectRoot: projectRoot,
	}, nil, nil, nil, nil, nil, nil, nil)

	ev := userTurnInputEvent("thread-1", "turn-1", "记住：你偏好简洁直接的回复风格。")
	hooks.onTurnInputReceived(context.Background(), ev)

	storeRoot, err := resolvedStoreRoot(root, projectRoot, "")
	if err != nil {
		t.Fatalf("resolvedStoreRoot() error = %v", err)
	}
	entries, err := scanMemoryEntries(storeRoot)
	if err != nil {
		t.Fatalf("scanMemoryEntries() error = %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("scanMemoryEntries() entries = %d, want 1", len(entries))
	}
	if _, err := os.Stat(memoryIndexPath(storeRoot)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("Stat(MEMORY.md) error = %v, want %v", err, os.ErrNotExist)
	}
}

func TestForgetIntentDeletesMatchingMemory(t *testing.T) {
	root := filepath.Join(t.TempDir(), "memory-root")
	projectRoot := filepath.Join(t.TempDir(), "project")
	hooks := newMemoryLifecycleHooks(&Config{
		Enabled:     true,
		RootDir:     root,
		ProjectRoot: projectRoot,
	}, nil, nil, nil, nil, nil, nil, nil)
	store, err := hooks.diskStore()
	if err != nil {
		t.Fatalf("diskStore() error = %v", err)
	}
	entry := buildExplicitMemoryWrite(SaveIntent{Detected: true, Content: "你偏好简洁直接的回复风格。", Type: MemoryTypeUser})
	if _, err := store.CreateStructured(entry); err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	ev := userTurnInputEvent("thread-1", "turn-2", "忘记：简洁直接的回复风格。")
	hooks.onTurnInputReceived(context.Background(), ev)
	hooks.onTurnEnd(context.Background(), turndto.TurnCompleted{TurnHeader: ev.TurnHeader, Success: true})

	storeRoot, err := resolvedStoreRoot(root, projectRoot, "")
	if err != nil {
		t.Fatalf("resolvedStoreRoot() error = %v", err)
	}
	entries, err := scanMemoryEntries(storeRoot)
	if err != nil {
		t.Fatalf("scanMemoryEntries() error = %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("scanMemoryEntries() entries = %d, want 0", len(entries))
	}
}

func TestMemoryLifecycleHooksOnTurnEndUsesAutoMemPathOverride(t *testing.T) {
	root := filepath.Join(t.TempDir(), "memory-root")
	projectRoot := filepath.Join(t.TempDir(), "project")
	override := filepath.Join(t.TempDir(), "override", "memory")
	hooks := newMemoryLifecycleHooks(&Config{
		Enabled:             true,
		RootDir:             root,
		ProjectRoot:         projectRoot,
		AutoMemPathOverride: override,
	}, nil, nil, nil, nil, nil, nil, nil)

	ev := userTurnInputEvent("thread-1", "turn-1", "记住：你喜欢在审计报告里看到对比表。")
	hooks.onTurnInputReceived(context.Background(), ev)

	storeRoot, err := resolvedStoreRoot(root, projectRoot, override)
	if err != nil {
		t.Fatalf("resolvedStoreRoot() error = %v", err)
	}
	entries, err := scanMemoryEntries(storeRoot)
	if err != nil {
		t.Fatalf("scanMemoryEntries() error = %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("scanMemoryEntries() entries = %d, want 1", len(entries))
	}
	indexPath := filepath.Join(storeRoot, memoryIndexFileName)
	if _, err := os.Stat(indexPath); err != nil {
		t.Fatalf("Stat(%q) error = %v", indexPath, err)
	}
}

func userTurnInputEvent(threadID, turnID, text string) turndto.TurnInputReceived {
	return turndto.TurnInputReceived{
		TurnHeader: shared.TurnHeader{
			AgentHeader: shared.AgentHeader{
				ThreadHeader: shared.ThreadHeader{ThreadID: threadID},
			},
			TurnIDHeader: shared.TurnIDHeader{TurnID: turnID},
		},
		InputType: "message",
		Source:    "user",
		Text:      text,
	}
}
