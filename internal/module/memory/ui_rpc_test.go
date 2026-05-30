package memory

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestBuildUIMemorySnapshotIncludesDurableAndAgentMemories(t *testing.T) {
	projectRoot := t.TempDir()
	privateRoot := filepath.Join(t.TempDir(), "private")
	cfg := newUIMemorySnapshotConfig(t, projectRoot, privateRoot)
	if err := os.MkdirAll(privateRoot, 0o755); err != nil {
		t.Fatalf("MkdirAll(privateRoot) error = %v", err)
	}

	privateStore := mustNewTestDiskStore(t, privateRoot)
	createStructuredMemoryForTest(t, privateStore, MemoryWriteRequest{
		Name:        "Keep replies concise",
		Description: "User prefers direct answers",
		Type:        MemoryTypeFeedback,
		Body:        "rule\nWhy: concise answers reduce back-and-forth.\nHow to apply: lead with the fix.",
	}, "private")

	teamRoot := mustConfiguredTeamMemoryRoot(t, cfg)
	if err := os.MkdirAll(teamRoot, 0o755); err != nil {
		t.Fatalf("MkdirAll(teamRoot) error = %v", err)
	}
	teamStore := mustNewTestDiskStore(t, teamRoot)
	createStructuredMemoryForTest(t, teamStore, MemoryWriteRequest{
		Name:        "Core dashboard owner",
		Description: "Who owns the dashboard area",
		Type:        MemoryTypeProject,
		Body:        "fact\nWhy: onboarding and review routing.\nHow to apply: ask the dashboard owner for cross-team changes.",
	}, "team")

	snapshot, err := buildUIMemorySnapshot(context.Background(), newServiceWithConsolidator(cfg, nil, nil, nil), nil, projectRoot)
	if err != nil {
		t.Fatalf("buildUIMemorySnapshot() error = %v", err)
	}
	assertDurableAndAgentMemorySnapshot(t, snapshot)
}

func TestRegisterUIMemoryMutationHandlersDoesNotExposeSharedFilePromote(t *testing.T) {
	handlers := registerUIMemoryMutationHandlers(memoryHandlerDeps{})
	if _, ok := handlers["ui/memory/shared-file/promote"]; ok {
		t.Fatal("ui/memory/shared-file/promote should not be registered")
	}
	if _, ok := handlers["ui/memory/shared-file/get"]; !ok {
		t.Fatal("ui/memory/shared-file/get should remain registered")
	}
	if _, ok := handlers["ui/memory/shared-file/delete"]; !ok {
		t.Fatal("ui/memory/shared-file/delete should remain registered")
	}
}

func newUIMemorySnapshotConfig(t *testing.T, projectRoot, privateRoot string) *Config {
	t.Helper()
	return &Config{
		Enabled:             true,
		EnableTools:         true,
		ExtractOnStop:       true,
		RootDir:             t.TempDir(),
		ProjectRoot:         projectRoot,
		AutoMemPathOverride: privateRoot,
		Features:            MemoryFeatureFlags{TeamMemory: true},
	}
}

func mustNewTestDiskStore(t *testing.T, root string) *diskStore {
	t.Helper()
	store, err := newDiskStore(root, nil)
	if err != nil {
		t.Fatalf("newDiskStore(%q, nil) error = %v", root, err)
	}
	return store
}

func createStructuredMemoryForTest(t *testing.T, store *diskStore, req MemoryWriteRequest, label string) {
	t.Helper()
	if _, err := store.CreateStructured(req); err != nil {
		t.Fatalf("CreateStructured(%s) error = %v", label, err)
	}
}

func mustConfiguredTeamMemoryRoot(t *testing.T, cfg *Config) string {
	t.Helper()
	teamRoot, err := configuredTeamMemRoot(cfg)
	if err != nil {
		t.Fatalf("configuredTeamMemRoot() error = %v", err)
	}
	return teamRoot
}

func assertDurableAndAgentMemorySnapshot(t *testing.T, snapshot UIMemorySnapshot) {
	t.Helper()
	if !snapshot.Overview.Enabled || !snapshot.Overview.ToolsEnabled || !snapshot.Overview.AutoDreamEnabled {
		t.Fatalf("Overview = %#v, want enabled tools-enabled auto-dream snapshot", snapshot.Overview)
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
}

func TestBuildUIMemorySnapshotAutoDreamReflectsConfigGates(t *testing.T) {
	projectRoot := t.TempDir()
	privateRoot := filepath.Join(t.TempDir(), "private")
	if err := os.MkdirAll(privateRoot, 0o755); err != nil {
		t.Fatalf("MkdirAll(privateRoot) error = %v", err)
	}

	cases := []struct {
		name          string
		enabled       bool
		extractOnStop bool
		wantAuto      bool
	}{
		{"all-on", true, true, true},
		{"extract-on-stop-off", true, false, false},
		{"system-off", false, true, false},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			cfg := &Config{
				Enabled:             tc.enabled,
				EnableTools:         true,
				ExtractOnStop:       tc.extractOnStop,
				RootDir:             t.TempDir(),
				ProjectRoot:         projectRoot,
				AutoMemPathOverride: privateRoot,
			}
			snapshot, err := buildUIMemorySnapshot(context.Background(), newServiceWithConsolidator(cfg, nil, nil, nil), nil, projectRoot)
			if err != nil {
				t.Fatalf("buildUIMemorySnapshot() error = %v", err)
			}
			if got := snapshot.Overview.AutoDreamEnabled; got != tc.wantAuto {
				t.Fatalf("Overview.AutoDreamEnabled = %v, want %v (cfg=%+v)", got, tc.wantAuto, cfg)
			}
			if snapshot.Overview.AutoDreamIntent != nil {
				t.Fatalf("Overview.AutoDreamIntent = %v, want nil with no persisted intent", *snapshot.Overview.AutoDreamIntent)
			}
		})
	}
}

func TestBuildUIMemorySnapshotSurfacesPersistedAutoDreamIntent(t *testing.T) {
	projectRoot := t.TempDir()
	privateRoot := filepath.Join(t.TempDir(), "private")
	if err := os.MkdirAll(privateRoot, 0o755); err != nil {
		t.Fatalf("MkdirAll(privateRoot) error = %v", err)
	}
	root := t.TempDir()
	if err := WriteAutoDreamIntent(root, true); err != nil {
		t.Fatalf("WriteAutoDreamIntent error = %v", err)
	}
	cfg := &Config{
		Enabled:             true,
		EnableTools:         true,
		ExtractOnStop:       false, // intent overrides this only via NewConfig; here we just check surfacing.
		RootDir:             root,
		ProjectRoot:         projectRoot,
		AutoMemPathOverride: privateRoot,
	}
	snapshot, err := buildUIMemorySnapshot(context.Background(), newServiceWithConsolidator(cfg, nil, nil, nil), nil, projectRoot)
	if err != nil {
		t.Fatalf("buildUIMemorySnapshot() error = %v", err)
	}
	if snapshot.Overview.AutoDreamIntent == nil || *snapshot.Overview.AutoDreamIntent != true {
		t.Fatalf("Overview.AutoDreamIntent = %v, want *true", snapshot.Overview.AutoDreamIntent)
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

func TestUIMemoryEntryRejectsTeamTargetWhenTeamMemoryDisabled(t *testing.T) {
	projectRoot := t.TempDir()
	privateRoot := filepath.Join(t.TempDir(), "private")
	cfg := &Config{
		Enabled:             true,
		EnableTools:         true,
		ProjectRoot:         projectRoot,
		RootDir:             t.TempDir(),
		AutoMemPathOverride: privateRoot,
		Features:            MemoryFeatureFlags{TeamMemory: false},
	}
	_, err := upsertUIMemoryEntry(context.Background(), memoryHandlerDeps{Service: newServiceWithConsolidator(cfg, nil, nil, nil)}, uiMemoryEntryUpsertParams{
		CWD:         projectRoot,
		Target:      "team",
		Name:        "Team memory disabled",
		Description: "Should not write team memory when disabled",
		Type:        "project",
		Content:     "Team writes are disabled.\nWhy: the feature gate is off.\nHow to apply: reject target=team mutations.",
	})
	if err == nil {
		t.Fatal("upsertUIMemoryEntry(team disabled) error = nil, want rejection")
	}
	if _, statErr := os.Stat(filepath.Join(privateRoot, teamMemoryRootDirName)); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("team memory root stat error = %v, want not exist", statErr)
	}
}

func TestUpdateUIMemoryEntryReturnsRequestedPathWhenDuplicateNamesExist(t *testing.T) {
	projectRoot := t.TempDir()
	privateRoot := filepath.Join(t.TempDir(), "private")
	cfg := &Config{
		Enabled:             true,
		EnableTools:         true,
		ProjectRoot:         projectRoot,
		RootDir:             t.TempDir(),
		AutoMemPathOverride: privateRoot,
	}
	olderPath := filepath.Join(privateRoot, string(MemoryTypeProject), "edit-old.md")
	newerPath := filepath.Join(privateRoot, string(MemoryTypeProject), "edit-new.md")
	writeTestTopicFile(t, olderPath, testMemoryEntry("Editable Duplicate", "old duplicate", MemoryTypeProject, "old body\nWhy: old duplicate.\nHow to apply: update this specific old file."))
	writeTestTopicFile(t, newerPath, testMemoryEntry("Editable Duplicate", "new duplicate", MemoryTypeProject, "new body\nWhy: newer duplicate should remain untouched.\nHow to apply: do not update this file."))
	oldTime := time.Now().Add(-time.Hour)
	newTime := time.Now()
	if err := os.Chtimes(olderPath, oldTime, oldTime); err != nil {
		t.Fatalf("Chtimes(olderPath) error = %v", err)
	}
	if err := os.Chtimes(newerPath, newTime, newTime); err != nil {
		t.Fatalf("Chtimes(newerPath) error = %v", err)
	}

	updated, err := upsertUIMemoryEntry(context.Background(), memoryHandlerDeps{Service: newServiceWithConsolidator(cfg, nil, nil, nil)}, uiMemoryEntryUpsertParams{
		CWD:          projectRoot,
		Target:       "private",
		ExistingPath: memoryEntryDisplayPath(privateRoot, olderPath),
		Name:         "Editable Duplicate",
		Description:  "old duplicate edited",
		Type:         "project",
		Content:      "edited old body\nWhy: old duplicate should be updated by path.\nHow to apply: return the requested path after saving.",
	})
	if err != nil {
		t.Fatalf("upsertUIMemoryEntry(update duplicate) error = %v", err)
	}
	if updated.Path != memoryEntryDisplayPath(privateRoot, olderPath) {
		t.Fatalf("updated.Path = %q, want requested path", updated.Path)
	}
	older, err := readMemoryEntryFile(olderPath)
	if err != nil {
		t.Fatalf("read older path error = %v", err)
	}
	if !strings.Contains(older.Content, "edited old body") {
		t.Fatalf("older path was not updated:\n%s", older.Content)
	}
	newer, err := readMemoryEntryFile(newerPath)
	if err != nil {
		t.Fatalf("read newer path error = %v", err)
	}
	if strings.Contains(newer.Content, "edited old body") {
		t.Fatalf("newer duplicate was modified:\n%s", newer.Content)
	}
}

func TestDeleteUIMemoryEntryDeletesRequestedPathWhenDuplicateNamesExist(t *testing.T) {
	projectRoot := t.TempDir()
	privateRoot := filepath.Join(t.TempDir(), "private")
	cfg := &Config{
		Enabled:             true,
		EnableTools:         true,
		ProjectRoot:         projectRoot,
		RootDir:             t.TempDir(),
		AutoMemPathOverride: privateRoot,
	}
	olderPath := filepath.Join(privateRoot, string(MemoryTypeProject), "duplicate-old.md")
	newerPath := filepath.Join(privateRoot, string(MemoryTypeProject), "duplicate-new.md")
	writeTestTopicFile(t, olderPath, testMemoryEntry("Duplicate Memory", "old duplicate", MemoryTypeProject, "old body\nWhy: old duplicate.\nHow to apply: keep the old file until specifically deleted."))
	writeTestTopicFile(t, newerPath, testMemoryEntry("Duplicate Memory", "new duplicate", MemoryTypeProject, "new body\nWhy: new duplicate.\nHow to apply: keep the new file unless its path is requested."))
	oldTime := time.Now().Add(-time.Hour)
	newTime := time.Now()
	if err := os.Chtimes(olderPath, oldTime, oldTime); err != nil {
		t.Fatalf("Chtimes(olderPath) error = %v", err)
	}
	if err := os.Chtimes(newerPath, newTime, newTime); err != nil {
		t.Fatalf("Chtimes(newerPath) error = %v", err)
	}

	err := deleteUIMemoryEntry(context.Background(), memoryHandlerDeps{Service: newServiceWithConsolidator(cfg, nil, nil, nil)}, uiMemoryEntryDeleteParams{
		CWD:    projectRoot,
		Target: "private",
		Path:   memoryEntryDisplayPath(privateRoot, olderPath),
	})
	if err != nil {
		t.Fatalf("deleteUIMemoryEntry() error = %v", err)
	}
	if _, err := os.Stat(olderPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("older path still exists or stat error = %v", err)
	}
	if _, err := os.Stat(newerPath); err != nil {
		t.Fatalf("newer path was deleted or inaccessible: %v", err)
	}
}

func TestComputeUIMemoryHealthUsesFullContentNotPreviewOnly(t *testing.T) {
	projectRoot := t.TempDir()
	privateRoot := filepath.Join(t.TempDir(), "private")
	cfg := &Config{
		Enabled:             true,
		EnableTools:         true,
		RootDir:             t.TempDir(),
		ProjectRoot:         projectRoot,
		AutoMemPathOverride: privateRoot,
	}
	store, err := newDiskStore(privateRoot, nil)
	if err != nil {
		t.Fatalf("newDiskStore(privateRoot, nil) error = %v", err)
	}
	sharedPreview := strings.Repeat("sharedpreview ", 40)
	alphaTail := uniqueTokenRun("alpha", 120)
	betaTail := uniqueTokenRun("beta", 120)
	_, err = store.CreateStructured(MemoryWriteRequest{
		Name:        "Long memory A",
		Description: "Long memory first",
		Type:        MemoryTypeProject,
		Body:        sharedPreview + "\n\n" + alphaTail + "\nWhy: " + alphaTail + "\nHow to apply: " + alphaTail,
	})
	if err != nil {
		t.Fatalf("CreateStructured(first) error = %v", err)
	}
	_, err = store.CreateStructured(MemoryWriteRequest{
		Name:        "Long memory B",
		Description: "Long memory second",
		Type:        MemoryTypeProject,
		Body:        sharedPreview + "\n\n" + betaTail + "\nWhy: " + betaTail + "\nHow to apply: " + betaTail,
	})
	if err != nil {
		t.Fatalf("CreateStructured(second) error = %v", err)
	}

	snapshot, err := buildUIMemorySnapshot(context.Background(), newServiceWithConsolidator(cfg, nil, nil, nil), nil, projectRoot)
	if err != nil {
		t.Fatalf("buildUIMemorySnapshot() error = %v", err)
	}
	if got := len(snapshot.Overview.Health.SimilarGroups); got != 0 {
		t.Fatalf("SimilarGroups = %#v, want none when only the truncated preview matches", snapshot.Overview.Health.SimilarGroups)
	}
}

func uniqueTokenRun(prefix string, count int) string {
	parts := make([]string, 0, count)
	for i := 0; i < count; i++ {
		parts = append(parts, fmt.Sprintf("%s%03d", prefix, i))
	}
	return strings.Join(parts, " ")
}

func TestReadUIMemoryEntryByPathRejectsEntrypointIndex(t *testing.T) {
	root := t.TempDir()
	writeTestTopicFile(t, filepath.Join(root, string(MemoryTypeProject), "actual.md"), testMemoryEntry("Actual Entry", "actual", MemoryTypeProject, "Actual body.\nWhy: valid topic files should still load.\nHow to apply: read the topic path."))
	if _, err := UpdateMemoryIndex(root); err != nil {
		t.Fatalf("UpdateMemoryIndex() error = %v", err)
	}

	if _, _, err := readUIMemoryEntryByPath(root, "private", memoryIndexFileName); !errors.Is(err, ErrInvalidMemoryReadPath) {
		t.Fatalf("readUIMemoryEntryByPath(MEMORY.md) error = %v, want %v", err, ErrInvalidMemoryReadPath)
	}
}

func TestDeleteUIMemoryEntryRejectsEntrypointIndex(t *testing.T) {
	projectRoot := t.TempDir()
	privateRoot := filepath.Join(t.TempDir(), "private")
	cfg := &Config{

		Enabled:             true,
		EnableTools:         true,
		ProjectRoot:         projectRoot,
		RootDir:             t.TempDir(),
		AutoMemPathOverride: privateRoot,
	}
	entryPath := filepath.Join(privateRoot, string(MemoryTypeProject), "actual.md")
	writeTestTopicFile(t, entryPath, testMemoryEntry("Actual Entry", "actual", MemoryTypeProject, "Actual body.\nWhy: valid topic files should survive rejected index deletion.\nHow to apply: reject MEMORY.md deletes from UI."))
	if _, err := UpdateMemoryIndex(privateRoot); err != nil {
		t.Fatalf("UpdateMemoryIndex() error = %v", err)
	}

	err := deleteUIMemoryEntry(context.Background(), memoryHandlerDeps{Service: newServiceWithConsolidator(cfg, nil, nil, nil)}, uiMemoryEntryDeleteParams{
		CWD:    projectRoot,
		Target: "private",
		Path:   memoryIndexFileName,
	})
	if !errors.Is(err, errDurableMemoryDeleteFailed) {
		t.Fatalf("deleteUIMemoryEntry(MEMORY.md) error = %v, want %v", err, errDurableMemoryDeleteFailed)
	}
	if _, err := os.Stat(memoryIndexPath(privateRoot)); err != nil {
		t.Fatalf("MEMORY.md was removed or inaccessible after rejected delete: %v", err)
	}
	if _, err := readMemoryEntryFile(entryPath); err != nil {
		t.Fatalf("topic entry was removed after rejected delete: %v", err)
	}
}
