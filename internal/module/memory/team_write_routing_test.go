package memory

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
)

func TestTeamWriteIntentRoutesProjectMemoryToTeamStore(t *testing.T) {
	withTeamMemoryRuntimeReady(t, true)
	projectRoot := t.TempDir()
	autoRoot := filepath.Join(t.TempDir(), "automem")
	cfg := &Config{
		Enabled:             true,
		RootDir:             t.TempDir(),
		ProjectRoot:         projectRoot,
		AutoMemPathOverride: autoRoot,
		Features:            MemoryFeatureFlags{TeamMemory: true},
	}
	hooks := &MemoryLifecycleHooks{
		cfg:                 cfg,
		team:                NewTeamMemoryManager(cfg),
		rootDir:             cfg.RootDir,
		projectRoot:         cfg.ProjectRoot,
		autoMemPathOverride: cfg.AutoMemPathOverride,
	}
	intent := SaveIntent{
		Detected: true,
		Type:     MemoryTypeProject,
		Content:  "Release target is April 15, 2026.",
	}
	entry := buildExplicitMemoryWrite(intent)
	if err := hooks.writeIntent(context.Background(), "thread-1", intent); err != nil {
		t.Fatalf("writeIntent() error = %v", err)
	}

	privateStore, err := NewDiskStore(autoRoot)
	if err != nil {
		t.Fatalf("NewDiskStore(private) error = %v", err)
	}
	teamStore, err := NewDiskStore(filepath.Join(autoRoot, teamMemoryRootDirName))
	if err != nil {
		t.Fatalf("NewDiskStore(team) error = %v", err)
	}
	teamEntry, err := teamStore.Read(entry.Name)
	if err != nil {
		t.Fatalf("teamStore.Read(%q) error = %v", entry.Name, err)
	}
	teamRoot := filepath.Join(autoRoot, teamMemoryRootDirName)
	if !strings.HasPrefix(teamEntry.FilePath, teamRoot+string(filepath.Separator)) {
		t.Fatalf("team entry path = %q, want prefix %q", teamEntry.FilePath, teamRoot+string(filepath.Separator))
	}
	privateEntry, err := privateStore.Read(entry.Name)
	if err != nil {
		t.Fatalf("privateStore.Read(%q) error = %v", entry.Name, err)
	}
	if privateEntry.FilePath != teamEntry.FilePath {
		t.Fatalf("privateStore.Read(%q) path = %q, want %q", entry.Name, privateEntry.FilePath, teamEntry.FilePath)
	}
}

func TestTeamWriteIntentKeepsUserMemoryPrivate(t *testing.T) {
	withTeamMemoryRuntimeReady(t, true)
	projectRoot := t.TempDir()
	autoRoot := filepath.Join(t.TempDir(), "automem")
	cfg := &Config{
		Enabled:             true,
		RootDir:             t.TempDir(),
		ProjectRoot:         projectRoot,
		AutoMemPathOverride: autoRoot,
		Features:            MemoryFeatureFlags{TeamMemory: true},
	}
	hooks := &MemoryLifecycleHooks{
		cfg:                 cfg,
		team:                NewTeamMemoryManager(cfg),
		rootDir:             cfg.RootDir,
		projectRoot:         cfg.ProjectRoot,
		autoMemPathOverride: cfg.AutoMemPathOverride,
	}
	intent := SaveIntent{
		Detected: true,
		Type:     MemoryTypeUser,
		Content:  "User prefers concise status summaries.",
	}
	entry := buildExplicitMemoryWrite(intent)
	if err := hooks.writeIntent(context.Background(), "thread-2", intent); err != nil {
		t.Fatalf("writeIntent() error = %v", err)
	}

	privateStore, err := NewDiskStore(autoRoot)
	if err != nil {
		t.Fatalf("NewDiskStore(private) error = %v", err)
	}
	teamStore, err := NewDiskStore(filepath.Join(autoRoot, teamMemoryRootDirName))
	if err != nil {
		t.Fatalf("NewDiskStore(team) error = %v", err)
	}
	if _, err := privateStore.Read(entry.Name); err != nil {
		t.Fatalf("privateStore.Read(%q) error = %v", entry.Name, err)
	}
	if _, err := teamStore.Read(entry.Name); !errors.Is(err, ErrMemoryNotFound) {
		t.Fatalf("teamStore.Read(%q) error = %v, want %v", entry.Name, err, ErrMemoryNotFound)
	}
}
