package memory

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	sharedfilestore "github.com/anthropic-ai/super-agent-v3/internal/store/sharedfile"
)

func TestBuildUIMemorySnapshotIncludesDurableAndAgentMemories(t *testing.T) {
	projectRoot := t.TempDir()
	privateRoot := filepath.Join(t.TempDir(), "private")
	cfg := &Config{
		Enabled:             true,
		EnableTools:         true,
		RootDir:             t.TempDir(),
		ProjectRoot:         projectRoot,
		AutoMemPathOverride: privateRoot,
		Features:            MemoryFeatureFlags{TeamMemory: true},
	}
	if err := os.MkdirAll(privateRoot, 0o755); err != nil {
		t.Fatalf("MkdirAll(privateRoot) error = %v", err)
	}

	privateStore, err := newDiskStore(privateRoot)
	if err != nil {
		t.Fatalf("newDiskStore(privateRoot) error = %v", err)
	}
	if _, err := privateStore.CreateStructured(MemoryWriteRequest{
		Name:        "Keep replies concise",
		Description: "User prefers direct answers",
		Type:        MemoryTypeFeedback,
		Body:        "rule\nWhy: concise answers reduce back-and-forth.\nHow to apply: lead with the fix.",
	}); err != nil {
		t.Fatalf("CreateStructured(private) error = %v", err)
	}

	teamRoot, err := configuredTeamMemRoot(cfg)
	if err != nil {
		t.Fatalf("configuredTeamMemRoot() error = %v", err)
	}
	if err := os.MkdirAll(teamRoot, 0o755); err != nil {
		t.Fatalf("MkdirAll(teamRoot) error = %v", err)
	}
	teamStore, err := newDiskStore(teamRoot)
	if err != nil {
		t.Fatalf("newDiskStore(teamRoot) error = %v", err)
	}
	if _, err := teamStore.CreateStructured(MemoryWriteRequest{
		Name:        "Core dashboard owner",
		Description: "Who owns the dashboard area",
		Type:        MemoryTypeProject,
		Body:        "fact\nWhy: onboarding and review routing.\nHow to apply: ask the dashboard owner for cross-team changes.",
	}); err != nil {
		t.Fatalf("CreateStructured(team) error = %v", err)
	}

	manager := NewAgentMemoryManager(cfg)
	projectAgentRoot, err := manager.GetAgentMemoryDir("Writer", MemoryScopeProject)
	if err != nil {
		t.Fatalf("GetAgentMemoryDir(project) error = %v", err)
	}
	if err := os.MkdirAll(projectAgentRoot, 0o755); err != nil {
		t.Fatalf("MkdirAll(projectAgentRoot) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(projectAgentRoot, "MEMORY.md"), []byte("Remember the preferred verification checklist."), 0o644); err != nil {
		t.Fatalf("WriteFile(project agent MEMORY.md) error = %v", err)
	}

	userAgentRoot, err := manager.GetAgentMemoryDir("Reviewer", MemoryScopeUser)
	if err != nil {
		t.Fatalf("GetAgentMemoryDir(user) error = %v", err)
	}
	if err := os.MkdirAll(userAgentRoot, 0o755); err != nil {
		t.Fatalf("MkdirAll(userAgentRoot) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(userAgentRoot, "MEMORY.md"), []byte("Remember reviewer-specific edge cases."), 0o644); err != nil {
		t.Fatalf("WriteFile(user agent MEMORY.md) error = %v", err)
	}

	snapshot, err := buildUIMemorySnapshot(context.Background(), newServiceWithConsolidator(cfg, nil, nil, nil), nil, projectRoot)
	if err != nil {
		t.Fatalf("buildUIMemorySnapshot() error = %v", err)
	}
	if !snapshot.Overview.Enabled || !snapshot.Overview.ToolsEnabled {
		t.Fatalf("Overview = %#v, want enabled tools-enabled snapshot", snapshot.Overview)
	}
	if got := len(snapshot.Private.Entries); got != 1 {
		t.Fatalf("len(private entries) = %d, want 1", got)
	}
	if got := snapshot.Private.Entries[0].Name; got != "Keep replies concise" {
		t.Fatalf("private entry name = %q, want %q", got, "Keep replies concise")
	}
	if got := len(snapshot.Team.Entries); got != 1 {
		t.Fatalf("len(team entries) = %d, want 1", got)
	}
	if got := snapshot.Team.Entries[0].Name; got != "Core dashboard owner" {
		t.Fatalf("team entry name = %q, want %q", got, "Core dashboard owner")
	}

	var (
		projectFound bool
		userFound    bool
	)
	for _, scope := range snapshot.AgentScopes {
		switch scope.Scope {
		case string(MemoryScopeProject):
			projectFound = len(scope.Entries) == 1 && scope.Entries[0].AgentType == "Writer"
		case string(MemoryScopeUser):
			userFound = len(scope.Entries) == 1 && scope.Entries[0].AgentType == "Reviewer"
		}
	}
	if !projectFound || !userFound {
		t.Fatalf("AgentScopes = %#v, want project Writer and user Reviewer entries", snapshot.AgentScopes)
	}
}

func TestUIMemoryEntryCRUD(t *testing.T) {
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

	created, err := upsertUIMemoryEntry(context.Background(), deps, uiMemoryEntryUpsertParams{
		CWD:         projectRoot,
		Target:      "private",
		Name:        "Release owner",
		Description: "Who owns production release decisions",
		Type:        "reference",
		Content:     "Release owner for this project is listed in the runbook.",
	})
	if err != nil {
		t.Fatalf("upsertUIMemoryEntry(create) error = %v", err)
	}
	if created.Path == "" || created.Name != "Release owner" {
		t.Fatalf("created = %#v", created)
	}

	loaded, err := getUIMemoryEntry(context.Background(), deps, uiMemoryEntryGetParams{
		CWD:    projectRoot,
		Target: "private",
		Path:   created.Path,
	})
	if err != nil {
		t.Fatalf("getUIMemoryEntry() error = %v", err)
	}
	if loaded.Content != "Release owner for this project is listed in the runbook." {
		t.Fatalf("loaded.Content = %q", loaded.Content)
	}

	updated, err := upsertUIMemoryEntry(context.Background(), deps, uiMemoryEntryUpsertParams{
		CWD:          projectRoot,
		Target:       "private",
		ExistingPath: created.Path,
		Name:         "Release owner",
		Description:  "Who owns production release approvals",
		Type:         "reference",
		Content:      "Primary source is the production runbook and release checklist.",
	})
	if err != nil {
		t.Fatalf("upsertUIMemoryEntry(update) error = %v", err)
	}
	if updated.Description != "Who owns production release approvals" {
		t.Fatalf("updated = %#v", updated)
	}

	if err := deleteUIMemoryEntry(context.Background(), deps, uiMemoryEntryDeleteParams{
		CWD:    projectRoot,
		Target: "private",
		Path:   updated.Path,
	}); err != nil {
		t.Fatalf("deleteUIMemoryEntry() error = %v", err)
	}
	if _, _, err := readUIMemoryEntryByName(privateRoot, "Release owner"); !errorsIsMemoryNotFound(err) {
		t.Fatalf("readUIMemoryEntryByName(after delete) error = %v, want not found", err)
	}
}

func TestUIAgentMemorySaveAndGet(t *testing.T) {
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
	saved, err := saveUIAgentMemory(context.Background(), deps, uiAgentMemorySaveParams{
		CWD:       projectRoot,
		Scope:     "project",
		AgentType: "Writer",
		Content:   "Remember the regression checklist for UI work.",
	})
	if err != nil {
		t.Fatalf("saveUIAgentMemory() error = %v", err)
	}
	if saved.AgentType != "Writer" || saved.Scope != "project" {
		t.Fatalf("saved = %#v", saved)
	}

	loaded, err := getUIAgentMemory(context.Background(), deps, uiAgentMemoryGetParams{
		CWD:       projectRoot,
		Scope:     "project",
		AgentType: "Writer",
	})
	if err != nil {
		t.Fatalf("getUIAgentMemory() error = %v", err)
	}
	if loaded.Content != "Remember the regression checklist for UI work." {
		t.Fatalf("loaded = %#v", loaded)
	}
}

func TestPromoteSharedFileToMemory(t *testing.T) {
	projectRoot := t.TempDir()
	privateRoot := filepath.Join(t.TempDir(), "private")
	cfg := &Config{
		Enabled:             true,
		EnableTools:         true,
		ProjectRoot:         projectRoot,
		RootDir:             t.TempDir(),
		AutoMemPathOverride: privateRoot,
	}
	now := time.Date(2026, 4, 20, 16, 0, 0, 0, time.UTC)
	deps := memoryHandlerDeps{
		Service: newServiceWithConsolidator(cfg, nil, nil, nil),
		SharedFiles: stubSharedFileReader{
			files: map[string]sharedfilestore.SharedFile{
				"handoff/release.txt": {
					Path:      "handoff/release.txt",
					Content:   "Grafana dashboard lives at https://grafana.example.com/team/core.",
					UpdatedBy: "alice",
					UpdatedAt: now,
				},
			},
		},
		Sections: &recordingSectionInvalidator{},
	}

	entry, err := promoteSharedFileToMemory(context.Background(), deps, uiSharedFilePromoteParams{
		CWD:         projectRoot,
		SharedPath:  "handoff/release.txt",
		Target:      "private",
		Name:        "Core Grafana dashboard",
		Description: "Where the team dashboard is maintained",
		Type:        "reference",
	})
	if err != nil {
		t.Fatalf("promoteSharedFileToMemory() error = %v", err)
	}
	if entry.Name != "Core Grafana dashboard" {
		t.Fatalf("entry = %#v", entry)
	}
	loaded, err := getUIMemoryEntry(context.Background(), deps, uiMemoryEntryGetParams{
		CWD:    projectRoot,
		Target: "private",
		Path:   entry.Path,
	})
	if err != nil {
		t.Fatalf("getUIMemoryEntry(promoted) error = %v", err)
	}
	if loaded.Content != "Grafana dashboard lives at https://grafana.example.com/team/core." {
		t.Fatalf("loaded.Content = %q", loaded.Content)
	}
}

type stubSharedFileReader struct {
	files map[string]sharedfilestore.SharedFile
}

func (s stubSharedFileReader) Get(_ context.Context, path string) (*sharedfilestore.SharedFile, error) {
	item, ok := s.files[path]
	if !ok {
		return nil, os.ErrNotExist
	}
	copy := item
	return &copy, nil
}

func (s stubSharedFileReader) List(context.Context, sharedfilestore.ListFilter) ([]sharedfilestore.SharedFile, error) {
	return nil, nil
}

// recordingSectionInvalidator implements contract.SectionInvalidator with
// the documented concurrent-safe contract; the mutex was added in Phase
// 2.0.1 to match production behaviour. UI RPC paths fan out under the
// shared section invalidator without external synchronization, so this
// stub has to too.
type recordingSectionInvalidator struct {
	mu    sync.Mutex
	calls []recordedInvalidateCall
}

type recordedInvalidateCall struct {
	reason contract.InvalidateReason
	names  []string
}

func (r *recordingSectionInvalidator) InvalidateSections(reason contract.InvalidateReason, names ...string) uint64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, recordedInvalidateCall{reason: reason, names: append([]string(nil), names...)})
	return uint64(len(r.calls))
}

func errorsIsMemoryNotFound(err error) bool {
	return errors.Is(err, ErrMemoryNotFound)
}
