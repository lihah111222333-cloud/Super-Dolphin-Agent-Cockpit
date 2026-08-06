package skill

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSkillMirrorReconcilerReportsProjectManifestTargetMismatchAsConflict(t *testing.T) {
	fixture := setupProjectManifestTargetMismatchFixture(t)

	conflicts, err := DetectSkillMirrorConflicts([]canonicalSkillRecord{fixture.record}, []SkillMirrorTarget{fixture.target})
	if err != nil {
		t.Fatalf("DetectSkillMirrorConflicts: %v", err)
	}
	findMirrorConflict(t, conflicts, skillConflictUnmanagedSameName, "build", skillScopeProject)
	assertProjectMismatchedManifestNotOwned(t, fixture)
}

func TestSkillMirrorPublisherReportsProjectManifestTargetMismatchConflictWithoutTakingOwnership(t *testing.T) {
	fixture := setupProjectManifestTargetMismatchFixture(t)

	report, err := PublishSkillMirrors(NewMirrorRootLockRegistry(), context.Background(), []canonicalSkillRecord{fixture.record}, []SkillMirrorTarget{fixture.target})
	if err != nil {
		t.Fatalf("PublishSkillMirrors: %v", err)
	}

	assertConflictReportItem(t, report.Conflicts, fixture.target.TargetID, fixture.target.Provider, skillScopeProject, "build", "project/build", "unmanaged")
	assertProjectMismatchedManifestNotOwned(t, fixture)
}

func TestSkillMirrorReconcilerRepairsPersonalManifestTargetMismatch(t *testing.T) {
	fixture := setupPersonalManifestTargetMismatchFixture(t)

	conflicts, err := DetectSkillMirrorConflicts([]canonicalSkillRecord{fixture.record}, []SkillMirrorTarget{fixture.target})
	if err != nil {
		t.Fatalf("DetectSkillMirrorConflicts: %v", err)
	}
	if len(conflicts) != 0 {
		t.Fatalf("conflicts = %+v, want none after personal manifest repair", conflicts)
	}
	assertRepairedManifestTarget(t, fixture, string(SkillProviderCodex), "sd_owner:repo-b", "personal/user/notes")
}

func TestSkillResolutionApplyRPCRejectsTakeoverForProviderManifestTargetMismatchBeforeCanonicalWrite(t *testing.T) {
	project, server, claudeRoot, _, svc := setupProviderManifestMismatchResolutionFixtureWithService(t)
	canonicalSkillFile := filepath.Join(project, ".agents", "skills", "build", skillMainFile)
	beforeCanonical := mustReadFileString(t, canonicalSkillFile)
	writeFileWithMode(t, filepath.Join(claudeRoot, "build", skillMainFile), "---\nname: build\n---\n# provider build\n", 0o644)
	item := findResolutionItem(t, dispatchResolutionList(t, server, project).Items, "unmanaged_same_name", "build", skillScopeProject)
	proof := storeTakeoverPreviewForProviderManifestMismatch(t, svc, item, project)

	_, err := dispatchResolutionApplyRaw(t, server, project, item.ConflictID, "build", ResolutionTakeoverProvider, proof, "")
	if err == nil || !strings.Contains(err.Error(), "manifest target mismatch") {
		t.Fatalf("takeover apply error = %v, want manifest target mismatch", err)
	}
	assertFileContent(t, canonicalSkillFile, beforeCanonical)
	assertProviderManifestTargetMismatchUnchanged(t, claudeRoot)
}

type manifestTargetRepairFixture struct {
	root               string
	target             SkillMirrorTarget
	record             canonicalSkillRecord
	canonicalHash      string
	expectedMirrorHash string
}

func setupProjectManifestTargetMismatchFixture(t *testing.T) manifestTargetRepairFixture {
	t.Helper()
	project := t.TempDir()
	root := filepath.Join(project, ".claude", "skills")
	skillDir := filepath.Join(project, ".agents", "skills", "build")
	writeSkillWithSupportFiles(t, skillDir, "build")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatalf("MkdirAll root: %v", err)
	}
	canonicalHash := mustStableMirrorDirectoryHash(t, skillDir)
	if _, err := replaceMirrorSkillDir(root, "build", skillDir, skillScopeProject); err != nil {
		t.Fatalf("replaceMirrorSkillDir: %v", err)
	}
	writeMismatchedProjectMirrorManifest(t, root)
	return manifestTargetRepairFixture{
		root:               root,
		target:             SkillMirrorTarget{TargetID: "claude:project:repo-b", Provider: SkillProviderClaude, Scope: skillScopeProject, Root: root, CanonicalRootID: "repo-b"},
		record:             canonicalSkillRecord{Name: "build", Scope: skillScopeProject, Dir: skillDir},
		canonicalHash:      canonicalHash,
		expectedMirrorHash: mustStableMirrorDirectoryHash(t, filepath.Join(root, "build")),
	}
}

func setupPersonalManifestTargetMismatchFixture(t *testing.T) manifestTargetRepairFixture {
	t.Helper()
	superHome := filepath.Join(t.TempDir(), ".super-dolphin")
	root := filepath.Join(t.TempDir(), ".agents", "skills")
	skillDir := filepath.Join(superHome, "skills", "personal", "user", "notes")
	writeSkillWithSupportFiles(t, skillDir, "notes")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatalf("MkdirAll root: %v", err)
	}
	canonicalHash := mustStableMirrorDirectoryHash(t, skillDir)
	if _, err := replaceMirrorSkillDir(root, "notes", skillDir, skillScopePersonal); err != nil {
		t.Fatalf("replaceMirrorSkillDir: %v", err)
	}
	writeMismatchedPersonalMirrorManifest(t, root)
	return manifestTargetRepairFixture{
		root:               root,
		target:             SkillMirrorTarget{TargetID: "codex:user-global:repo-b", Provider: SkillProviderCodex, Scope: skillScopePersonal, Root: root, CanonicalRootID: "sd_owner:repo-b"},
		record:             canonicalSkillRecord{Name: "notes", Scope: skillScopePersonal, PersonalType: personalSkillTypeUser, Dir: skillDir},
		canonicalHash:      canonicalHash,
		expectedMirrorHash: mustStableMirrorDirectoryHash(t, filepath.Join(root, "notes")),
	}
}

func writeMismatchedProjectMirrorManifest(t *testing.T, root string) {
	t.Helper()
	if err := writeSkillMirrorManifest(filepath.Join(root, skillMirrorManifestFile), SkillMirrorManifest{
		Version:         1,
		Manager:         "super-dolphin",
		Scope:           skillScopeProject,
		Provider:        string(SkillProviderCodex),
		CanonicalRootID: "repo-a",
		Skills:          map[string]SkillMirrorEntry{},
	}); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
}

func writeMismatchedPersonalMirrorManifest(t *testing.T, root string) {
	t.Helper()
	if err := writeSkillMirrorManifest(filepath.Join(root, skillMirrorManifestFile), SkillMirrorManifest{
		Version:         1,
		Manager:         "super-dolphin",
		Scope:           skillScopePersonal,
		Provider:        string(SkillProviderClaude),
		CanonicalRootID: "sd_owner:repo-a",
		Skills:          map[string]SkillMirrorEntry{},
	}); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
}

func assertProjectMismatchedManifestNotOwned(t *testing.T, fixture manifestTargetRepairFixture) {
	t.Helper()
	manifest, err := readSkillMirrorManifest(filepath.Join(fixture.root, skillMirrorManifestFile))
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	if manifest.Provider != string(SkillProviderCodex) || manifest.CanonicalRootID != "repo-a" {
		t.Fatalf("manifest target = provider:%q root:%q, want original mismatched target", manifest.Provider, manifest.CanonicalRootID)
	}
	if entry, ok := manifest.Skills["build"]; ok && entry.Owned {
		t.Fatalf("manifest build entry = %+v, want project mismatch not owned", entry)
	}
}

func storeTakeoverPreviewForProviderManifestMismatch(t *testing.T, svc *service, item skillResolutionItem, project string) skillResolutionPreviewItem {
	t.Helper()
	previews, err := buildResolutionPreviewItems(item, skillResolutionPreviewParams{
		CWD:      project,
		Scope:    skillScopeProject,
		Provider: "claude",
		Name:     "build",
		Action:   ResolutionTakeoverProvider,
	}, svc.resolvedSuperDolphinHome())
	if err != nil {
		t.Fatalf("build takeover preview: %v", err)
	}
	return svc.storeResolutionPreview(item.ConflictID, previews[0])
}

func mustReadFileString(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(data)
}

func assertProviderManifestTargetMismatchUnchanged(t *testing.T, root string) {
	t.Helper()
	manifest, err := readSkillMirrorManifest(filepath.Join(root, skillMirrorManifestFile))
	if err != nil {
		t.Fatalf("read provider manifest: %v", err)
	}
	if manifest.Provider != string(SkillProviderClaude) || manifest.Scope != skillScopePersonal || manifest.CanonicalRootID != "sd_owner:old" {
		t.Fatalf("manifest target = provider:%q scope:%q root:%q, want original mismatched manifest", manifest.Provider, manifest.Scope, manifest.CanonicalRootID)
	}
	if entry, ok := manifest.Skills["build"]; ok && entry.Owned {
		t.Fatalf("manifest build entry = %+v, want project mismatch not owned", entry)
	}
}

func assertRepairedManifestTarget(t *testing.T, fixture manifestTargetRepairFixture, provider, canonicalRootID, canonicalID string) {
	t.Helper()
	manifest, err := readSkillMirrorManifest(filepath.Join(fixture.root, skillMirrorManifestFile))
	if err != nil {
		t.Fatalf("read repaired manifest: %v", err)
	}
	if manifest.Provider != provider || manifest.CanonicalRootID != canonicalRootID {
		t.Fatalf("repaired manifest target = provider:%q root:%q", manifest.Provider, manifest.CanonicalRootID)
	}
	name := filepath.Base(canonicalID)
	entry := manifest.Skills[name]
	if !entry.Owned || entry.CanonicalID != canonicalID || entry.CanonicalHash != fixture.canonicalHash || entry.MirrorHash != fixture.expectedMirrorHash {
		t.Fatalf("repaired manifest %s entry = %+v, want owned %s hashes %s/%s", name, entry, canonicalID, fixture.canonicalHash, fixture.expectedMirrorHash)
	}
}
