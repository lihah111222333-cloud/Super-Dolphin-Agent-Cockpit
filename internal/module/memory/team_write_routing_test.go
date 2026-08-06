package memory

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/module/memory/dedup"
)

func TestTeamWriteIntentRoutesProjectMemoryToTeamStore(t *testing.T) {
	projectRoot := newTestGitProjectRoot(t)
	autoRoot := filepath.Join(t.TempDir(), "automem")
	cfg := &Config{
		Enabled:             true,
		RootDir:             t.TempDir(),
		ProjectRoot:         projectRoot,
		AutoMemPathOverride: autoRoot,
		Features:            MemoryFeatureFlags{TeamMemory: true},
	}
	team := NewTeamMemoryManager(cfg)
	withTeamMemoryRuntimeReady(t, team, true)
	hooks := newTestHooks(withTestCfg(cfg), withTeam(team))
	intent := SaveIntent{
		Detected: true,
		Type:     MemoryTypeProject,
		Content:  "Release target is April 15, 2026.",
	}
	entry := buildExplicitMemoryWrite(intent)
	if err := hooks.writeIntent(context.Background(), "thread-1", intent); err != nil {
		t.Fatalf("writeIntent() error = %v", err)
	}

	privateStore, err := newDiskStore(autoRoot, nil)
	if err != nil {
		t.Fatalf("NewDiskStore(private) error = %v", err)
	}
	teamStore, err := newDiskStore(filepath.Join(autoRoot, teamMemoryRootDirName), nil)
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
	projectRoot := newTestGitProjectRoot(t)
	autoRoot := filepath.Join(t.TempDir(), "automem")
	cfg := &Config{
		Enabled:             true,
		RootDir:             t.TempDir(),
		ProjectRoot:         projectRoot,
		AutoMemPathOverride: autoRoot,
		Features:            MemoryFeatureFlags{TeamMemory: true},
	}
	team := NewTeamMemoryManager(cfg)
	withTeamMemoryRuntimeReady(t, team, true)
	hooks := newTestHooks(withTestCfg(cfg), withTeam(team))
	intent := SaveIntent{
		Detected: true,
		Type:     MemoryTypeUser,
		Content:  "User prefers concise status summaries.",
	}
	entry := buildExplicitMemoryWrite(intent)
	if err := hooks.writeIntent(context.Background(), "thread-2", intent); err != nil {
		t.Fatalf("writeIntent() error = %v", err)
	}

	privateStore, err := newDiskStore(autoRoot, nil)
	if err != nil {
		t.Fatalf("NewDiskStore(private) error = %v", err)
	}
	teamStore, err := newDiskStore(filepath.Join(autoRoot, teamMemoryRootDirName), nil)
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

func TestTeamWriteIntentOverflowMergesWithinTeamScope(t *testing.T) {
	projectRoot := newTestGitProjectRoot(t)
	autoRoot := filepath.Join(t.TempDir(), "automem")
	cfg := &Config{
		Enabled:             true,
		RootDir:             t.TempDir(),
		ProjectRoot:         projectRoot,
		AutoMemPathOverride: autoRoot,
		Features:            MemoryFeatureFlags{TeamMemory: true},
	}
	team := NewTeamMemoryManager(cfg)
	withTeamMemoryRuntimeReady(t, team, true)
	hooks := NewMemoryLifecycleHooks(memoryLifecycleHookParams{Config: cfg, Team: team})
	teamRoot := filepath.Join(autoRoot, teamMemoryRootDirName)
	teamStore, err := newDiskStore(teamRoot, nil)
	if err != nil {
		t.Fatalf("newDiskStore(team) error = %v", err)
	}
	for i := 0; i < dedup.MaxEntriesPerType; i++ {
		_, err := teamStore.CreateStructured(MemoryWriteRequest{
			Name:        fmt.Sprintf("team-rule-%02d", i),
			Description: "team overflow fixture",
			Type:        MemoryTypeProject,
			Body:        fmt.Sprintf("Team project context paragraph %02d.\nWhy: keeps team project context.\nHow to apply: use it for team decisions.", i),
		})
		if err != nil {
			t.Fatalf("CreateStructured(%d) error = %v", i, err)
		}
	}

	intent := SaveIntent{
		Detected: true,
		Type:     MemoryTypeProject,
		Content:  "Team project context paragraph 00.\nWhy: keeps team project context.\nHow to apply: use it for team decisions plus overflow merge.",
	}
	if err := hooks.writeIntent(context.Background(), "thread-team-overflow", intent); err != nil {
		t.Fatalf("writeIntent() error = %v", err)
	}

	entries, err := scanMemoryEntries(teamRoot)
	if err != nil {
		t.Fatalf("scanMemoryEntries(team) error = %v", err)
	}
	if len(entries) > dedup.MaxEntriesPerType {
		t.Fatalf("team entries = %d, want <= %d after overflow merge", len(entries), dedup.MaxEntriesPerType)
	}
}
