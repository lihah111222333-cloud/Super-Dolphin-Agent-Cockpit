package memory

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
	"strings"
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
		ExtractOnStop:       true,
		RootDir:             t.TempDir(),
		ProjectRoot:         projectRoot,
		AutoMemPathOverride: privateRoot,
		Features:            MemoryFeatureFlags{TeamMemory: true},
	}
	if err := os.MkdirAll(privateRoot, 0o755); err != nil {
		t.Fatalf("MkdirAll(privateRoot) error = %v", err)
	}

	privateStore, err := newDiskStore(privateRoot, nil)
	if err != nil {
		t.Fatalf("newDiskStore(privateRoot, nil) error = %v", err)
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
	teamStore, err := newDiskStore(teamRoot, nil)
	if err != nil {
		t.Fatalf("newDiskStore(teamRoot, nil) error = %v", err)
	}
	if _, err := teamStore.CreateStructured(MemoryWriteRequest{
		Name:        "Core dashboard owner",
		Description: "Who owns the dashboard area",
		Type:        MemoryTypeProject,
		Body:        "fact\nWhy: onboarding and review routing.\nHow to apply: ask the dashboard owner for cross-team changes.",
	}); err != nil {
		t.Fatalf("CreateStructured(team) error = %v", err)
	}

	snapshot, err := buildUIMemorySnapshot(context.Background(), newServiceWithConsolidator(cfg, nil, nil, nil), nil, projectRoot)
	if err != nil {
		t.Fatalf("buildUIMemorySnapshot() error = %v", err)
	}
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

func TestMergeUIMemoryEntriesUsesTeamGuardWhenKeptEntryIsTeam(t *testing.T) {
	projectRoot := t.TempDir()
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
	secret := "sk-proj-abcdefghijklmnopqrstuvwxyz1234567890"
	commonBody := "Shared team merge guard content common phrase common phrase common phrase.\nWhy: merge validation should pass before guarded team write.\nHow to apply: reject merged content containing secrets."
	keptBody := commonBody + "\nTeam kept clean marker."
	absorbedBody := commonBody + "\nPrivate absorbed marker includes token " + secret + "."

	writeTestTopicFile(t, keptPath, testMemoryEntry("Team Guard Kept", "team kept", MemoryTypeProject, keptBody))
	writeTestTopicFile(t, absorbedPath, testMemoryEntry("Private Guard Absorbed", "private absorbed", MemoryTypeProject, absorbedBody))
	if _, err := UpdateMemoryIndex(teamRoot); err != nil {
		t.Fatalf("UpdateMemoryIndex(teamRoot) error = %v", err)
	}
	if _, err := UpdateMemoryIndex(privateRoot); err != nil {
		t.Fatalf("UpdateMemoryIndex(privateRoot) error = %v", err)
	}

	var logBuf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logBuf, &slog.HandlerOptions{}))
	_, err = mergeUIMemoryEntries(context.Background(), memoryHandlerDeps{
		Service: newServiceWithConsolidator(cfg, nil, nil, nil),
		Logger:  logger,
	}, uiMemoryEntryMergeParams{
		CWD:     projectRoot,
		TargetA: "team",
		PathA:   memoryEntryDisplayPath(teamRoot, keptPath),
		TargetB: "private",
		PathB:   memoryEntryDisplayPath(privateRoot, absorbedPath),
	})
	if !errors.Is(err, errDurableMemorySaveFailed) {
		t.Fatalf("mergeUIMemoryEntries() error = %v, want %v", err, errDurableMemorySaveFailed)
	}
	if !strings.Contains(logBuf.String(), ErrTeamMemSecretDetected.Error()) {
		t.Fatalf("merge log = %q, want TeamMemoryGuard rejection %q", logBuf.String(), ErrTeamMemSecretDetected.Error())
	}
	kept, readErr := readMemoryEntryFile(keptPath)
	if readErr != nil {
		t.Fatalf("read kept after rejected merge error = %v", readErr)
	}
	if kept.Content != keptBody {
		t.Fatalf("team kept content changed after rejected merge:\n%s", kept.Content)
	}
	if strings.Contains(kept.Content, secret) {
		t.Fatalf("team kept content contains secret after rejected merge:\n%s", kept.Content)
	}
	if _, err := os.Stat(absorbedPath); err != nil {
		t.Fatalf("absorbed private entry was deleted after rejected team write: %v", err)
	}
}

func TestRollbackMergedEntryUsesTeamGuardWhenTargetIsTeam(t *testing.T) {
	projectRoot := t.TempDir()
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
	cleanBody := "Clean rollback placeholder.\nWhy: the existing team file should remain unchanged.\nHow to apply: reject unsafe rollback writes."
	writeTestTopicFile(t, keptPath, testMemoryEntry("Team Rollback Kept", "team kept", MemoryTypeProject, cleanBody))

	unsafeRollback := testMemoryEntry("Team Rollback Kept", "team kept", MemoryTypeProject,
		"Unsafe rollback body.\nWhy: rollback must still use TeamMemoryGuard.\nHow to apply: reject token sk-proj-abcdefghijklmnopqrstuvwxyz1234567890.")
	err = rollbackMergedEntry(newServiceWithConsolidator(cfg, nil, nil, nil), teamRoot, "team", memoryEntryDisplayPath(teamRoot, keptPath), unsafeRollback)

	if !errors.Is(err, ErrTeamMemSecretDetected) {
		t.Fatalf("rollbackMergedEntry(team) error = %v, want %v", err, ErrTeamMemSecretDetected)
	}
	kept, readErr := readMemoryEntryFile(keptPath)
	if readErr != nil {
		t.Fatalf("read kept after rejected rollback error = %v", readErr)
	}
	if kept.Content != cleanBody {
		t.Fatalf("team kept content changed after rejected rollback:\n%s", kept.Content)
	}
}

func TestMergeUIMemoryEntriesRollsBackKeptEntryWhenDeleteAbsorbedFails(t *testing.T) {
	projectRoot := t.TempDir()

	privateRoot := filepath.Join(t.TempDir(), "private")
	cfg := &Config{
		Enabled:             true,
		EnableTools:         true,
		ProjectRoot:         projectRoot,
		RootDir:             t.TempDir(),
		AutoMemPathOverride: privateRoot,
		Features:            MemoryFeatureFlags{TeamMemory: true},
	}
	keptPath := filepath.Join(privateRoot, string(MemoryTypeProject), "kept.md")
	teamRoot, err := configuredTeamMemRoot(cfg, contract.BuildCtx{CWD: projectRoot})
	if err != nil {
		t.Fatalf("configuredTeamMemRoot() error = %v", err)
	}
	absorbedPath := filepath.Join(teamRoot, string(MemoryTypeProject), "absorbed.md")
	commonBody := "Shared rollback content common phrase common phrase common phrase.\nWhy: merge validation should pass before the delete step.\nHow to apply: rollback the kept write on merge failure."
	keptBody := commonBody + "\nKept original marker."
	absorbedBody := commonBody + "\nAbsorbed unique marker."

	writeTestTopicFile(t, keptPath, testMemoryEntry("Rollback Kept", "kept", MemoryTypeProject, keptBody))
	writeTestTopicFile(t, absorbedPath, testMemoryEntry("Rollback Absorbed", "absorbed", MemoryTypeProject, absorbedBody))
	if _, err := UpdateMemoryIndex(privateRoot); err != nil {
		t.Fatalf("UpdateMemoryIndex(privateRoot) error = %v", err)
	}
	if _, err := UpdateMemoryIndex(teamRoot); err != nil {
		t.Fatalf("UpdateMemoryIndex(teamRoot) error = %v", err)
	}
	originalPrivateIndex := readIndexEntries(t, privateRoot)
	originalTeamIndex := readIndexEntries(t, teamRoot)

	absorbedDir := filepath.Dir(absorbedPath)
	if err := os.Chmod(absorbedDir, 0o555); err != nil {
		t.Fatalf("Chmod(absorbedDir read-only) error = %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(absorbedDir, 0o755) })

	_, err = mergeUIMemoryEntries(context.Background(), memoryHandlerDeps{Service: newServiceWithConsolidator(cfg, nil, nil, nil)}, uiMemoryEntryMergeParams{
		CWD:     projectRoot,
		TargetA: "private",
		PathA:   memoryEntryDisplayPath(privateRoot, keptPath),
		TargetB: "team",
		PathB:   memoryEntryDisplayPath(teamRoot, absorbedPath),
	})
	if err == nil {
		t.Fatal("mergeUIMemoryEntries() error = nil, want delete failure")
	}
	kept, readErr := readMemoryEntryFile(keptPath)
	if readErr != nil {
		t.Fatalf("read kept after failed merge error = %v", readErr)
	}
	if kept.Content != keptBody {
		t.Fatalf("kept content changed after failed merge:\n%s", kept.Content)
	}
	if strings.Contains(kept.Content, "Absorbed unique marker") {
		t.Fatalf("kept content contains absorbed marker after failed merge:\n%s", kept.Content)
	}
	absorbed, readErr := readMemoryEntryFile(absorbedPath)
	if readErr != nil {
		t.Fatalf("read absorbed after failed merge error = %v", readErr)
	}
	if absorbed.Content != absorbedBody {
		t.Fatalf("absorbed content changed after failed merge:\n%s", absorbed.Content)
	}

	privateIndex := readIndexEntries(t, privateRoot)
	if !reflect.DeepEqual(privateIndex, originalPrivateIndex) {
		t.Fatalf("private index after failed merge = %#v, want original %#v", privateIndex, originalPrivateIndex)
	}
	teamIndex := readIndexEntries(t, teamRoot)
	if !reflect.DeepEqual(teamIndex, originalTeamIndex) {
		t.Fatalf("team index after failed merge = %#v, want original %#v", teamIndex, originalTeamIndex)
	}
}

func TestMergeUIMemoryEntriesUpdatesAndDeletesRequestedPathsWhenDuplicateNamesExist(t *testing.T) {
	projectRoot := t.TempDir()
	privateRoot := filepath.Join(t.TempDir(), "private")
	cfg := &Config{
		Enabled:             true,
		EnableTools:         true,
		ProjectRoot:         projectRoot,
		RootDir:             t.TempDir(),
		AutoMemPathOverride: privateRoot,
	}
	keptOldPath := filepath.Join(privateRoot, string(MemoryTypeProject), "kept-old.md")
	keptNewPath := filepath.Join(privateRoot, string(MemoryTypeProject), "kept-new.md")
	absorbedOldPath := filepath.Join(privateRoot, string(MemoryTypeProject), "absorbed-old.md")
	absorbedNewPath := filepath.Join(privateRoot, string(MemoryTypeProject), "absorbed-new.md")
	writeTestTopicFile(t, keptOldPath, testMemoryEntry("Kept Duplicate", "old kept", MemoryTypeProject, "Shared merge content for requested kept path.\nWhy: requested kept path must be updated.\nHow to apply: update the older kept file."))
	writeTestTopicFile(t, keptNewPath, testMemoryEntry("Kept Duplicate", "new kept", MemoryTypeProject, "Shared merge content for non-requested kept path.\nWhy: newer duplicate should not be updated.\nHow to apply: keep this file untouched."))
	writeTestTopicFile(t, absorbedOldPath, testMemoryEntry("Absorbed Duplicate", "old absorbed", MemoryTypeProject, "Shared merge content for requested absorbed path.\nWhy: requested absorbed path must be removed.\nHow to apply: delete the older absorbed file."))
	writeTestTopicFile(t, absorbedNewPath, testMemoryEntry("Absorbed Duplicate", "new absorbed", MemoryTypeProject, "Shared merge content for non-requested absorbed path.\nWhy: newer absorbed duplicate should remain.\nHow to apply: keep this file untouched."))
	oldTime := time.Now().Add(-time.Hour)
	newTime := time.Now()
	for _, path := range []string{keptOldPath, absorbedOldPath} {
		if err := os.Chtimes(path, oldTime, oldTime); err != nil {
			t.Fatalf("Chtimes(%q) error = %v", path, err)
		}
	}
	for _, path := range []string{keptNewPath, absorbedNewPath} {
		if err := os.Chtimes(path, newTime, newTime); err != nil {
			t.Fatalf("Chtimes(%q) error = %v", path, err)
		}
	}

	merged, err := mergeUIMemoryEntries(context.Background(), memoryHandlerDeps{Service: newServiceWithConsolidator(cfg, nil, nil, nil)}, uiMemoryEntryMergeParams{

		CWD:     projectRoot,
		TargetA: "private",
		PathA:   memoryEntryDisplayPath(privateRoot, keptOldPath),
		TargetB: "private",
		PathB:   memoryEntryDisplayPath(privateRoot, absorbedOldPath),
	})
	if err != nil {
		t.Fatalf("mergeUIMemoryEntries() error = %v", err)
	}
	if merged.Path != memoryEntryDisplayPath(privateRoot, keptOldPath) {
		t.Fatalf("merged.Path = %q, want requested kept path", merged.Path)
	}
	keptOld, err := readMemoryEntryFile(keptOldPath)

	if err != nil {
		t.Fatalf("read requested kept path error = %v", err)
	}
	if !strings.Contains(keptOld.Content, "requested absorbed path") {
		t.Fatalf("requested kept path was not merged:\n%s", keptOld.Content)
	}
	keptNew, err := readMemoryEntryFile(keptNewPath)
	if err != nil {
		t.Fatalf("read newer kept path error = %v", err)
	}
	if strings.Contains(keptNew.Content, "requested absorbed path") {
		t.Fatalf("newer kept duplicate was modified:\n%s", keptNew.Content)
	}
	if _, err := os.Stat(absorbedOldPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("requested absorbed path still exists or stat error = %v", err)
	}
	if _, err := os.Stat(absorbedNewPath); err != nil {
		t.Fatalf("newer absorbed duplicate was deleted or inaccessible: %v", err)
	}
}

func TestMergeUIMemoryEntriesRejectsDifferentTypesAndKeepsBoth(t *testing.T) {
	projectRoot := t.TempDir()
	privateRoot := filepath.Join(t.TempDir(), "private")
	cfg := &Config{
		Enabled:             true,
		EnableTools:         true,
		ProjectRoot:         projectRoot,
		RootDir:             t.TempDir(),
		AutoMemPathOverride: privateRoot,
	}
	store, err := newDiskStore(privateRoot, nil)
	if err != nil {
		t.Fatalf("newDiskStore(privateRoot, nil) error = %v", err)
	}
	feedback, err := store.CreateStructured(MemoryWriteRequest{
		Name:        "Verification preference",
		Description: "How the user wants verification handled",
		Type:        MemoryTypeFeedback,
		Body:        "Run guarded verification before success claims.\nWhy: verification avoids false completion reports.\nHow to apply: run the relevant guard before final summaries.",
	})
	if err != nil {
		t.Fatalf("CreateStructured(feedback) error = %v", err)
	}
	project, err := store.CreateStructured(MemoryWriteRequest{
		Name:        "Verification project fact",
		Description: "Project verification workflow context",
		Type:        MemoryTypeProject,
		Body:        "Run guarded verification before success claims.\nWhy: verification avoids false completion reports.\nHow to apply: run the relevant guard before final summaries.",
	})
	if err != nil {
		t.Fatalf("CreateStructured(project) error = %v", err)
	}

	_, err = mergeUIMemoryEntries(context.Background(), memoryHandlerDeps{Service: newServiceWithConsolidator(cfg, nil, nil, nil)}, uiMemoryEntryMergeParams{
		CWD:     projectRoot,
		TargetA: "private",
		PathA:   memoryEntryDisplayPath(privateRoot, feedback.FilePath),
		TargetB: "private",
		PathB:   memoryEntryDisplayPath(privateRoot, project.FilePath),
	})
	if err == nil {
		t.Fatal("mergeUIMemoryEntries() error = nil, want type mismatch rejection")
	}
	if _, _, err := readUIMemoryEntryByName(privateRoot, "Verification preference"); err != nil {
		t.Fatalf("feedback entry missing after rejected merge: %v", err)
	}
	if _, _, err := readUIMemoryEntryByName(privateRoot, "Verification project fact"); err != nil {
		t.Fatalf("project entry missing after rejected merge: %v", err)
	}
}

func TestMergeUIMemoryEntriesRejectsDissimilarEntriesAndKeepsBoth(t *testing.T) {
	projectRoot := t.TempDir()
	privateRoot := filepath.Join(t.TempDir(), "private")
	cfg := &Config{
		Enabled:             true,
		EnableTools:         true,
		ProjectRoot:         projectRoot,
		RootDir:             t.TempDir(),
		AutoMemPathOverride: privateRoot,
	}
	store, err := newDiskStore(privateRoot, nil)
	if err != nil {
		t.Fatalf("newDiskStore(privateRoot, nil) error = %v", err)
	}
	first, err := store.CreateStructured(MemoryWriteRequest{
		Name:        "Reply language",
		Description: "Preferred response language",
		Type:        MemoryTypeFeedback,
		Body:        "Answer the user in Chinese by default.\nWhy: the user prefers Chinese collaboration.\nHow to apply: use Chinese for user-facing summaries.",
	})
	if err != nil {
		t.Fatalf("CreateStructured(first) error = %v", err)
	}
	second, err := store.CreateStructured(MemoryWriteRequest{
		Name:        "Database engine",
		Description: "Project database choice",
		Type:        MemoryTypeFeedback,
		Body:        "Use PostgreSQL migrations for schema changes.\nWhy: the backend relies on PostgreSQL-specific migrations.\nHow to apply: do not write MySQL-only migration syntax.",
	})
	if err != nil {
		t.Fatalf("CreateStructured(second) error = %v", err)
	}

	_, err = mergeUIMemoryEntries(context.Background(), memoryHandlerDeps{Service: newServiceWithConsolidator(cfg, nil, nil, nil)}, uiMemoryEntryMergeParams{
		CWD:     projectRoot,
		TargetA: "private",
		PathA:   memoryEntryDisplayPath(privateRoot, first.FilePath),
		TargetB: "private",
		PathB:   memoryEntryDisplayPath(privateRoot, second.FilePath),
	})
	if err == nil {
		t.Fatal("mergeUIMemoryEntries() error = nil, want dissimilar rejection")
	}
	if _, _, err := readUIMemoryEntryByName(privateRoot, "Reply language"); err != nil {
		t.Fatalf("first entry missing after rejected merge: %v", err)
	}
	if _, _, err := readUIMemoryEntryByName(privateRoot, "Database engine"); err != nil {
		t.Fatalf("second entry missing after rejected merge: %v", err)
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
