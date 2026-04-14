package memory

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	turndto "github.com/anthropic-ai/super-agent-v3/internal/dto/turn"
)

func TestMemoryLifecycleHooksOnTurnEndWritesExplicitMemory(t *testing.T) {
	root := filepath.Join(t.TempDir(), "memory-root")
	projectRoot := filepath.Join(t.TempDir(), "project")
	hooks := NewMemoryLifecycleHooks(&Config{
		Enabled:     true,
		RootDir:     root,
		ProjectRoot: projectRoot,
	}, nil, nil)

	hooks.onTurnEnd(context.Background(), turndto.TurnCompleted{
		Success: true,
		Message: "记住了：你偏好简洁直接的回复风格。",
	})

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

func TestMemoryLifecycleHooksOnTurnEndSkipsNonIntent(t *testing.T) {
	root := filepath.Join(t.TempDir(), "memory-root")
	projectRoot := filepath.Join(t.TempDir(), "project")
	hooks := NewMemoryLifecycleHooks(&Config{
		Enabled:     true,
		RootDir:     root,
		ProjectRoot: projectRoot,
	}, nil, nil)

	hooks.onTurnEnd(context.Background(), turndto.TurnCompleted{
		Success: true,
		Message: "我会先检查代码，再给你方案。",
	})

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
	hooks := NewMemoryLifecycleHooks(&Config{
		Enabled:             true,
		RootDir:             root,
		ProjectRoot:         projectRoot,
		AutoMemPathOverride: override,
	}, nil, nil)

	hooks.onTurnEnd(context.Background(), turndto.TurnCompleted{
		Success: true,
		Message: "记住了：你喜欢在审计报告里看到对比表。",
	})

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
