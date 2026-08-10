package skill

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	uidto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/ui"
)

func TestSkillMirrorReconcilerDetectsProjectAndPersonalDriftActions(t *testing.T) {
	project := t.TempDir()
	home := t.TempDir()
	superHome := filepath.Join(home, ".super-dolphin")
	writeSkillWithSupportFiles(t, filepath.Join(project, ".agents", "skills", "build"), "build")
	writeSkillWithSupportFiles(t, filepath.Join(superHome, "skills", "personal", "user", "notes"), "notes")
	records, err := newCanonicalStore(superHome).scan(project)
	if err != nil {
		t.Fatalf("scan canonical records: %v", err)
	}
	projectTarget := SkillMirrorTarget{TargetID: "codex:project:repo", Provider: SkillProviderCodex, Scope: skillScopeProject, Root: testCodexProjectMirrorRoot(project), CanonicalRootID: "repo"}
	personalTarget := SkillMirrorTarget{TargetID: "claude:app-managed:owner", Provider: SkillProviderClaude, Scope: skillScopePersonal, Root: filepath.Join(superHome, "providers", "claude", "skills"), CanonicalRootID: "sd_owner:owner"}
	if _, err := PublishSkillMirrors(NewMirrorRootLockRegistry(), context.Background(), records, []SkillMirrorTarget{projectTarget, personalTarget}); err != nil {
		t.Fatalf("PublishSkillMirrors initial: %v", err)
	}
	writeFileWithMode(t, filepath.Join(projectTarget.Root, "build", "references", "guide.md"), "project drift\n", 0o644)
	writeFileWithMode(t, filepath.Join(personalTarget.Root, "notes", "references", "guide.md"), "personal drift\n", 0o644)

	conflicts, err := DetectSkillMirrorConflicts(records, []SkillMirrorTarget{projectTarget, personalTarget})
	if err != nil {
		t.Fatalf("DetectSkillMirrorConflicts: %v", err)
	}

	projectConflict := findMirrorConflict(t, conflicts, "mirror_drift", "build", skillScopeProject)
	assertMirrorConflictActions(t, projectConflict, "sync_back_to_canonical", "canonical_overwrite_mirror", "save_as_new_skill")
	personalConflict := findMirrorConflict(t, conflicts, "mirror_drift", "notes", skillScopePersonal)
	if personalConflict.PersonalType != personalSkillTypeUser {
		t.Fatalf("personal conflict personal_type = %q, want user; conflict=%+v", personalConflict.PersonalType, personalConflict)
	}
	assertMirrorConflictActions(t, personalConflict, "sync_back_to_personal", "personal_overwrite_mirror", "save_as_new_personal_skill")
}

func TestManagedMirrorOwnedFalseIsDriftEvenWhenHashMatches(t *testing.T) {
	project := t.TempDir()
	writeSkillWithSupportFiles(t, filepath.Join(project, ".agents", "skills", "build"), "build")
	records, err := newCanonicalStore("").scan(project)
	if err != nil {
		t.Fatalf("scan canonical records: %v", err)
	}
	target := SkillMirrorTarget{TargetID: "codex:project:repo", Provider: SkillProviderCodex, Scope: skillScopeProject, Root: testCodexProjectMirrorRoot(project), CanonicalRootID: "repo"}
	if _, err := PublishSkillMirrors(NewMirrorRootLockRegistry(), context.Background(), records, []SkillMirrorTarget{target}); err != nil {
		t.Fatalf("PublishSkillMirrors initial: %v", err)
	}
	manifest, err := readSkillMirrorManifest(filepath.Join(target.Root, skillMirrorManifestFile))
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	entry := manifest.Skills["build"]
	entry.Owned = false
	manifest.Skills["build"] = entry
	if err := writeSkillMirrorManifest(filepath.Join(target.Root, skillMirrorManifestFile), manifest); err != nil {
		t.Fatalf("write manifest: %v", err)
	}

	conflicts, err := DetectSkillMirrorConflicts(records, []SkillMirrorTarget{target})
	if err != nil {
		t.Fatalf("DetectSkillMirrorConflicts: %v", err)
	}

	conflict := findMirrorConflict(t, conflicts, "mirror_drift", "build", skillScopeProject)
	if conflict.MirrorHash != entry.MirrorHash || conflict.CanonicalHash != entry.CanonicalHash {
		t.Fatalf("conflict hashes = canonical:%q mirror:%q, want manifest hashes canonical:%q mirror:%q",
			conflict.CanonicalHash, conflict.MirrorHash, entry.CanonicalHash, entry.MirrorHash)
	}
}

func TestSkillMirrorReconcilerIgnoresIdenticalUnmanagedProjectSameName(t *testing.T) {
	project := t.TempDir()
	writeSkillWithSupportFiles(t, filepath.Join(project, ".agents", "skills", "build"), "build")
	records, err := newCanonicalStore("").scan(project)
	if err != nil {
		t.Fatalf("scan canonical records: %v", err)
	}
	fingerprint := RepoFingerprint(project)
	target := SkillMirrorTarget{TargetID: "codex:project:" + fingerprint, Provider: SkillProviderCodex, Scope: skillScopeProject, Root: testCodexProjectMirrorRoot(project), CanonicalRootID: fingerprint}
	writeSkillWithSupportFiles(t, filepath.Join(target.Root, "build"), "build")

	conflicts, err := DetectSkillMirrorConflicts(records, []SkillMirrorTarget{target})
	if err != nil {
		t.Fatalf("DetectSkillMirrorConflicts: %v", err)
	}

	if len(conflicts) != 0 {
		t.Fatalf("conflicts = %+v, want none for identical unmanaged project same-name mirror", conflicts)
	}
}

func TestSkillMirrorReconcilerDetectsUnmanagedProjectSameNameAsProjectVersionConflict(t *testing.T) {
	project := t.TempDir()
	writeSkillWithSupportFiles(t, filepath.Join(project, ".agents", "skills", "build"), "build")
	records, err := newCanonicalStore("").scan(project)
	if err != nil {
		t.Fatalf("scan canonical records: %v", err)
	}
	fingerprint := RepoFingerprint(project)
	target := SkillMirrorTarget{TargetID: "codex:project:" + fingerprint, Provider: SkillProviderCodex, Scope: skillScopeProject, Root: testCodexProjectMirrorRoot(project), CanonicalRootID: fingerprint}
	writeSkillWithSupportFiles(t, filepath.Join(target.Root, "build"), "build")
	writeFileWithMode(t, filepath.Join(target.Root, "build", "references", "guide.md"), "provider edit\n", 0o644)

	conflicts, err := DetectSkillMirrorConflicts(records, []SkillMirrorTarget{target})
	if err != nil {
		t.Fatalf("DetectSkillMirrorConflicts: %v", err)
	}

	conflict := findMirrorConflict(t, conflicts, "unmanaged_same_name", "build", skillScopeProject)
	assertMirrorConflictActions(t, conflict, "view_diff", "sync_back_to_canonical", "canonical_overwrite_mirror", "save_as_new_skill")
	if conflict.PreviewHash == "" {
		t.Fatalf("preview_hash is empty: %+v", conflict)
	}
}

func TestSkillMirrorReconcilerDetectsOrphanUnmanagedProviderSkill(t *testing.T) {
	project := t.TempDir()
	fingerprint := RepoFingerprint(project)
	target := SkillMirrorTarget{TargetID: "codex:project:" + fingerprint, Provider: SkillProviderCodex, Scope: skillScopeProject, Root: testCodexProjectMirrorRoot(project), CanonicalRootID: fingerprint}
	writeSkillWithSupportFiles(t, filepath.Join(target.Root, "scratch"), "scratch")

	conflicts, err := DetectSkillMirrorConflicts(nil, []SkillMirrorTarget{target})
	if err != nil {
		t.Fatalf("DetectSkillMirrorConflicts: %v", err)
	}

	conflict := findMirrorConflict(t, conflicts, "unmanaged_provider_skill", "scratch", skillScopeProject)
	if conflict.CanonicalID != "" {
		t.Fatalf("orphan unmanaged canonical_id = %q, want empty", conflict.CanonicalID)
	}
	assertMirrorConflictActions(t, conflict, "view_unmanaged", "import_to_personal_imported", "import_to_project", "takeover_provider_skill")
}

func TestSkillMirrorReconcilerReportsTopLevelSymlinkAndContinuesScanning(t *testing.T) {
	project := t.TempDir()
	target := SkillMirrorTarget{TargetID: "codex:project:repo", Provider: SkillProviderCodex, Scope: skillScopeProject, Root: testCodexProjectMirrorRoot(project), CanonicalRootID: "repo"}
	outside := filepath.Join(t.TempDir(), "scratch")
	writeSkillWithSupportFiles(t, outside, "scratch")
	if err := os.MkdirAll(target.Root, 0o755); err != nil {
		t.Fatalf("MkdirAll target root: %v", err)
	}
	if err := os.Symlink(outside, filepath.Join(target.Root, "scratch")); err != nil {
		skipIfSymlinkPrivilegeNotHeld(t, err)
		t.Fatalf("Symlink unmanaged skill: %v", err)
	}
	writeSkillWithSupportFiles(t, filepath.Join(target.Root, "notes"), "notes")

	conflicts, err := DetectSkillMirrorConflicts(nil, []SkillMirrorTarget{target})
	if err != nil {
		t.Fatalf("DetectSkillMirrorConflicts: %v", err)
	}
	symlinkConflict := findMirrorConflict(t, conflicts, "mirror_entry_symlink", "scratch", skillScopeProject)
	if symlinkConflict.MirrorPath != filepath.ToSlash(filepath.Join(target.Root, "scratch")) {
		t.Fatalf("symlink mirror path = %q", symlinkConflict.MirrorPath)
	}
	assertMirrorConflictActions(t, symlinkConflict, "view_unmanaged")
	findMirrorConflict(t, conflicts, "unmanaged_provider_skill", "notes", skillScopeProject)
}

func TestSkillMirrorReconcilerDetectsPersonalUnmanagedSameNameAgainstCanonical(t *testing.T) {
	project := t.TempDir()
	superHome := filepath.Join(t.TempDir(), ".super-dolphin")
	writeSkillWithSupportFiles(t, filepath.Join(superHome, "skills", "personal", personalSkillTypeUser, "notes"), "notes")
	records, err := newCanonicalStore(superHome).scan(project)
	if err != nil {
		t.Fatalf("scan canonical records: %v", err)
	}
	target := SkillMirrorTarget{TargetID: "claude:app-managed:owner", Provider: SkillProviderClaude, Scope: skillScopePersonal, Root: filepath.Join(superHome, "providers", "claude", "skills"), CanonicalRootID: "sd_owner:owner"}
	writeSkillWithSupportFiles(t, filepath.Join(target.Root, "notes"), "notes")

	conflicts, err := DetectSkillMirrorConflicts(records, []SkillMirrorTarget{target})
	if err != nil {
		t.Fatalf("DetectSkillMirrorConflicts: %v", err)
	}

	conflict := findMirrorConflict(t, conflicts, "unmanaged_same_name", "notes", skillScopePersonal)
	if conflict.PersonalType != personalSkillTypeUser || conflict.CanonicalID != "personal/user/notes" {
		t.Fatalf("personal unmanaged conflict = %+v, want user canonical", conflict)
	}
	assertMirrorConflictActions(t, conflict, "view_unmanaged", "import_to_personal_imported")
}

func TestDetectSkillMirrorConflictsDoesNotHashPersonalOrphanDirs(t *testing.T) {
	const testMirrorScanEntryBudget = 512

	root := filepath.Join(t.TempDir(), "provider", "skills")
	orphan := filepath.Join(root, "scratch")
	for i := 0; i <= testMirrorScanEntryBudget; i++ {
		writeFileWithMode(t, filepath.Join(orphan, "references", fmt.Sprintf("file-%03d.md", i)), "ignored orphan\n", 0o644)
	}
	target := SkillMirrorTarget{TargetID: "codex:user-global:owner", Provider: SkillProviderCodex, Scope: skillScopePersonal, Root: root, CanonicalRootID: "sd_owner:owner"}

	conflicts, err := DetectSkillMirrorConflicts(nil, []SkillMirrorTarget{target})
	if err != nil {
		t.Fatalf("DetectSkillMirrorConflicts personal orphan: %v", err)
	}
	if len(conflicts) != 0 {
		t.Fatalf("conflicts = %+v, want personal orphan mirror ignored before recursive hashing", conflicts)
	}
}

func TestDetectSkillMirrorConflictsCapsRootEntries(t *testing.T) {
	const testMirrorScanEntryBudget = 512

	root := filepath.Join(t.TempDir(), "provider", "skills")
	for i := 0; i <= testMirrorScanEntryBudget; i++ {
		writeSkillWithSupportFiles(t, filepath.Join(root, fmt.Sprintf("skill-%03d", i)), fmt.Sprintf("skill-%03d", i))
	}
	target := SkillMirrorTarget{TargetID: "codex:project:repo", Provider: SkillProviderCodex, Scope: skillScopeProject, Root: root, CanonicalRootID: "repo"}

	_, err := DetectSkillMirrorConflicts(nil, []SkillMirrorTarget{target})

	if err == nil {
		t.Fatalf("DetectSkillMirrorConflicts over budget error = nil, want typed truncation")
	}
	var coded interface {
		MirrorScanCode() string
	}
	if !errors.As(err, &coded) || coded.MirrorScanCode() != "mirror_scan_truncated" {
		t.Fatalf("DetectSkillMirrorConflicts error = %T %v, want mirror_scan_truncated", err, err)
	}
}

func TestDetectSkillMirrorConflictsUsesCustomScanBudget(t *testing.T) {
	root := filepath.Join(t.TempDir(), "provider", "skills")
	writeSkillWithSupportFiles(t, filepath.Join(root, "alpha"), "alpha")
	writeSkillWithSupportFiles(t, filepath.Join(root, "beta"), "beta")
	target := SkillMirrorTarget{TargetID: "codex:project:repo", Provider: SkillProviderCodex, Scope: skillScopeProject, Root: root, CanonicalRootID: "repo"}

	_, err := DetectSkillMirrorConflictsWithBudget(nil, []SkillMirrorTarget{target}, SkillMirrorScanBudget{MaxRootEntries: 1})

	if err == nil {
		t.Fatalf("DetectSkillMirrorConflictsWithBudget error = nil, want typed truncation")
	}
	var coded interface {
		MirrorScanCode() string
	}
	if !errors.As(err, &coded) || coded.MirrorScanCode() != "mirror_scan_truncated" {
		t.Fatalf("DetectSkillMirrorConflictsWithBudget error = %T %v, want mirror_scan_truncated", err, err)
	}
}

func TestSkillMirrorReconcilerDetectsCanonicalDeletedAndMultiMirrorDriftKinds(t *testing.T) {
	project := t.TempDir()
	writeSkillWithSupportFiles(t, filepath.Join(project, ".agents", "skills", "build"), "build")
	records, err := newCanonicalStore("").scan(project)
	if err != nil {
		t.Fatalf("scan canonical records: %v", err)
	}
	claudeTarget := SkillMirrorTarget{TargetID: "claude:project:repo", Provider: SkillProviderClaude, Scope: skillScopeProject, Root: filepath.Join(project, ".claude", "skills"), CanonicalRootID: "repo"}
	codexTarget := SkillMirrorTarget{TargetID: "codex:project:repo", Provider: SkillProviderCodex, Scope: skillScopeProject, Root: testCodexProjectMirrorRoot(project), CanonicalRootID: "repo"}
	if _, err := PublishSkillMirrors(NewMirrorRootLockRegistry(), context.Background(), records, []SkillMirrorTarget{claudeTarget, codexTarget}); err != nil {
		t.Fatalf("PublishSkillMirrors initial: %v", err)
	}
	writeFileWithMode(t, filepath.Join(claudeTarget.Root, "build", "references", "guide.md"), "claude drift\n", 0o644)
	writeFileWithMode(t, filepath.Join(codexTarget.Root, "build", "references", "guide.md"), "codex drift\n", 0o644)

	conflicts, err := DetectSkillMirrorConflicts(records, []SkillMirrorTarget{claudeTarget, codexTarget})
	if err != nil {
		t.Fatalf("DetectSkillMirrorConflicts multi: %v", err)
	}
	findMirrorConflict(t, conflicts, "multi_mirror_drift", "build", skillScopeProject)

	if err := os.RemoveAll(filepath.Join(project, ".agents", "skills", "build")); err != nil {
		t.Fatalf("RemoveAll canonical: %v", err)
	}
	records = nil
	conflicts, err = DetectSkillMirrorConflicts(records, []SkillMirrorTarget{claudeTarget})
	if err != nil {
		t.Fatalf("DetectSkillMirrorConflicts deleted: %v", err)
	}
	findMirrorConflict(t, conflicts, "canonical_deleted_with_drift", "build", skillScopeProject)
}

func TestSkillMirrorReconcilerMatchesPersonalManifestByPersonalType(t *testing.T) {
	root := t.TempDir()
	agentDir := filepath.Join(root, "canonical", "agent", "notes")
	userDir := filepath.Join(root, "canonical", "user", "notes")
	mirrorRoot := filepath.Join(root, "provider", "skills")
	mirrorDir := filepath.Join(mirrorRoot, "notes")
	writeSkillWithSupportFiles(t, agentDir, "notes")
	writeFileWithMode(t, filepath.Join(agentDir, "references", "guide.md"), "agent\n", 0o644)
	writeSkillWithSupportFiles(t, userDir, "notes")
	writeFileWithMode(t, filepath.Join(userDir, "references", "guide.md"), "user\n", 0o644)
	writeSkillWithSupportFiles(t, mirrorDir, "notes")
	writeFileWithMode(t, filepath.Join(mirrorDir, "references", "guide.md"), "agent\n", 0o644)
	agentHash, err := stableMirrorDirectoryHash(agentDir)
	if err != nil {
		t.Fatalf("stableMirrorDirectoryHash agent: %v", err)
	}
	mirrorHash, err := stableMirrorDirectoryHash(mirrorDir)
	if err != nil {
		t.Fatalf("stableMirrorDirectoryHash mirror: %v", err)
	}
	target := SkillMirrorTarget{TargetID: "claude:app-managed:owner", Provider: SkillProviderClaude, Scope: skillScopePersonal, Root: mirrorRoot, CanonicalRootID: "sd_owner:owner"}
	if err := writeSkillMirrorManifest(filepath.Join(mirrorRoot, skillMirrorManifestFile), SkillMirrorManifest{
		Version:         1,
		Manager:         "super-dolphin",
		Scope:           skillScopePersonal,
		Provider:        string(SkillProviderClaude),
		CanonicalRootID: "sd_owner:owner",
		Skills: map[string]SkillMirrorEntry{"notes": {
			CanonicalID:   "personal/agent/notes",
			CanonicalHash: agentHash,
			MirrorHash:    mirrorHash,
			SourceType:    skillScopePersonal,
			PersonalType:  personalSkillTypeAgent,
			Owned:         true,
		}},
	}); err != nil {
		t.Fatalf("write manifest: %v", err)
	}

	conflicts, err := DetectSkillMirrorConflicts([]canonicalSkillRecord{
		{Name: "notes", Scope: skillScopePersonal, PersonalType: personalSkillTypeAgent, Dir: agentDir, DirHash: mustSkillDirContentHash(t, agentDir)},
		{Name: "notes", Scope: skillScopePersonal, PersonalType: personalSkillTypeUser, Dir: userDir, DirHash: mustSkillDirContentHash(t, userDir)},
	}, []SkillMirrorTarget{target})
	if err != nil {
		t.Fatalf("DetectSkillMirrorConflicts: %v", err)
	}
	if len(conflicts) != 0 {
		t.Fatalf("conflicts = %+v, want none for unchanged agent mirror", conflicts)
	}
}

func TestSkillMirrorReconcilerConvertsSameNameConflicts(t *testing.T) {
	conflicts := MirrorConflictsFromCanonical([]canonicalSkillConflict{{
		Kind: "same_name",
		Name: "build",
		Sources: []canonicalSkillConflictSource{
			{Name: "build", Scope: skillScopeProject, ContentHash: "a"},
			{Name: "build", Scope: skillScopePersonal, PersonalType: personalSkillTypeUser, ContentHash: "b"},
		},
	}})

	conflict := findMirrorConflict(t, conflicts, "same_name", "build", "")
	if len(conflict.Sources) != 2 {
		t.Fatalf("same_name sources = %+v, want two", conflict.Sources)
	}
}

func TestSkillMirrorReconcilerSyncBackFailsClosedWithoutAudit(t *testing.T) {
	project := t.TempDir()
	skillDir := filepath.Join(project, ".agents", "skills", "build")
	writeSkillWithSupportFiles(t, skillDir, "build")
	records, err := newCanonicalStore("").scan(project)
	if err != nil {
		t.Fatalf("scan canonical records: %v", err)
	}
	target := SkillMirrorTarget{TargetID: "codex:project:repo", Provider: SkillProviderCodex, Scope: skillScopeProject, Root: testCodexProjectMirrorRoot(project), CanonicalRootID: "repo"}
	if _, err := PublishSkillMirrors(NewMirrorRootLockRegistry(), context.Background(), records, []SkillMirrorTarget{target}); err != nil {
		t.Fatalf("PublishSkillMirrors initial: %v", err)
	}
	writeFileWithMode(t, filepath.Join(target.Root, "build", "references", "guide.md"), "mirror edit\n", 0o644)
	beforeHash := mustSkillDirContentHash(t, skillDir)
	previewHash, err := stableMirrorDirectoryHash(filepath.Join(target.Root, "build"))
	if err != nil {
		t.Fatalf("stableMirrorDirectoryHash: %v", err)
	}
	svc := &service{projectRoot: project, projectSkillsRoot: defaultProjectSkillsRoot(project), mirrorLocks: NewMirrorRootLockRegistry()}

	_, err = ResolveSkillMirrorDrift(context.Background(), svc, SkillMirrorResolutionRequest{
		Action:      "sync_back_to_canonical",
		Name:        "build",
		Target:      target,
		PreviewHash: previewHash,
	})
	if err == nil || !strings.Contains(err.Error(), "audit") {
		t.Fatalf("ResolveSkillMirrorDrift error = %v, want audit failure", err)
	}
	if afterHash := mustSkillDirContentHash(t, skillDir); afterHash != beforeHash {
		t.Fatalf("canonical mutated on audit failure: before=%s after=%s", beforeHash, afterHash)
	}
}

func TestSkillMirrorReconcilerSyncBackUpdatesManagedMirrorManifest(t *testing.T) {
	project := t.TempDir()
	skillDir := filepath.Join(project, ".agents", "skills", "build")
	writeSkillWithSupportFiles(t, skillDir, "build")
	records, err := newCanonicalStore("").scan(project)
	if err != nil {
		t.Fatalf("scan canonical records: %v", err)
	}
	fingerprint := RepoFingerprint(project)
	target := SkillMirrorTarget{TargetID: "codex:project:" + fingerprint, Provider: SkillProviderCodex, Scope: skillScopeProject, Root: testCodexProjectMirrorRoot(project), CanonicalRootID: fingerprint}
	if _, err := PublishSkillMirrors(NewMirrorRootLockRegistry(), context.Background(), records, []SkillMirrorTarget{target}); err != nil {
		t.Fatalf("PublishSkillMirrors initial: %v", err)
	}
	mirrorDir := filepath.Join(target.Root, "build")
	writeFileWithMode(t, filepath.Join(mirrorDir, "references", "guide.md"), "mirror edit\n", 0o644)
	previewHash, err := stableMirrorDirectoryHash(mirrorDir)
	if err != nil {
		t.Fatalf("stableMirrorDirectoryHash: %v", err)
	}
	svc := &service{projectRoot: project, projectSkillsRoot: defaultProjectSkillsRoot(project), auditStore: &capturingSkillAuditStore{}, mirrorLocks: NewMirrorRootLockRegistry()}

	report, err := ResolveSkillMirrorDrift(context.Background(), svc, SkillMirrorResolutionRequest{
		Action:      "sync_back_to_canonical",
		Name:        "build",
		Target:      target,
		PreviewHash: previewHash,
	})
	if err != nil {
		t.Fatalf("ResolveSkillMirrorDrift: %v report=%+v", err, report)
	}
	if report.ResultingHash == "" {
		t.Fatalf("sync-back report missing resulting hash: %+v", report)
	}
	assertManagedMirrorManifestEntry(t, target.Root, "build", report.ResultingHash, mustStableMirrorDirectoryHash(t, mirrorDir))
	assertNoMirrorConflictsForRecord(t, canonicalSkillRecord{Name: "build", Scope: skillScopeProject, Dir: skillDir, DirHash: report.ResultingHash}, target)
}

func TestSkillMirrorReconcilerSyncBackCopyFailureKeepsCanonical(t *testing.T) {
	project := t.TempDir()
	skillDir := filepath.Join(project, ".agents", "skills", "build")
	writeSkillWithSupportFiles(t, skillDir, "build")
	beforeHash := mustSkillDirContentHash(t, skillDir)
	records, err := newCanonicalStore("").scan(project)
	if err != nil {
		t.Fatalf("scan canonical records: %v", err)
	}
	target := SkillMirrorTarget{TargetID: "codex:project:repo", Provider: SkillProviderCodex, Scope: skillScopeProject, Root: testCodexProjectMirrorRoot(project), CanonicalRootID: "repo"}
	if _, err := PublishSkillMirrors(NewMirrorRootLockRegistry(), context.Background(), records, []SkillMirrorTarget{target}); err != nil {
		t.Fatalf("PublishSkillMirrors initial: %v", err)
	}
	mirrorDir := filepath.Join(target.Root, "build")
	writeFileWithMode(t, filepath.Join(mirrorDir, "references", "guide.md"), "mirror edit\n", 0o644)
	err = replaceSkillDirFromMirrorWithCopy(mirrorDir, skillDir, func(string, string) (int, int64, error) {
		return 0, 0, fmt.Errorf("injected copy failure")
	})
	if err == nil || !strings.Contains(err.Error(), "injected copy failure") {
		t.Fatalf("replaceSkillDirFromMirrorWithCopy error = %v, want injected copy failure", err)
	}
	if afterHash := mustSkillDirContentHash(t, skillDir); afterHash != beforeHash {
		t.Fatalf("canonical mutated on copy failure: before=%s after=%s", beforeHash, afterHash)
	}
}

func TestSkillMirrorReconcilerSyncBackReportsPartialFailureAfterMutation(t *testing.T) {
	project := t.TempDir()
	skillDir := filepath.Join(project, ".agents", "skills", "build")
	writeSkillWithSupportFiles(t, skillDir, "build")
	records, err := newCanonicalStore("").scan(project)
	if err != nil {
		t.Fatalf("scan canonical records: %v", err)
	}
	target := SkillMirrorTarget{TargetID: "codex:project:repo", Provider: SkillProviderCodex, Scope: skillScopeProject, Root: testCodexProjectMirrorRoot(project), CanonicalRootID: "repo"}
	if _, err := PublishSkillMirrors(NewMirrorRootLockRegistry(), context.Background(), records, []SkillMirrorTarget{target}); err != nil {
		t.Fatalf("PublishSkillMirrors initial: %v", err)
	}
	writeFileWithMode(t, filepath.Join(target.Root, "build", "references", "guide.md"), "mirror edit\n", 0o644)
	previewHash, err := stableMirrorDirectoryHash(filepath.Join(target.Root, "build"))
	if err != nil {
		t.Fatalf("stableMirrorDirectoryHash: %v", err)
	}
	audit := &failingActionAuditStore{failOnAction: "sync_back_to_canonical_finalize"}
	svc := &service{projectRoot: project, projectSkillsRoot: defaultProjectSkillsRoot(project), auditStore: audit, mirrorLocks: NewMirrorRootLockRegistry()}

	report, err := ResolveSkillMirrorDrift(context.Background(), svc, SkillMirrorResolutionRequest{
		Action:      "sync_back_to_canonical",
		Name:        "build",
		Target:      target,
		PreviewHash: previewHash,
	})
	if err == nil {
		t.Fatalf("ResolveSkillMirrorDrift error = nil, want finalize partial failure")
	}
	if !report.PartialFailure || report.ResultingHash == "" || report.FollowUpAction == "" {
		t.Fatalf("partial failure report = %+v", report)
	}
	assertFileContent(t, filepath.Join(skillDir, "references", "guide.md"), "mirror edit\n")
}

func TestSkillMirrorReconcilerImportValidatesPreviewAndProviderPath(t *testing.T) {
	project := t.TempDir()
	providerRoot := testCodexProjectMirrorRoot(project)
	unmanagedDir := filepath.Join(providerRoot, "scratch")
	writeSkillWithSupportFiles(t, unmanagedDir, "scratch")
	previewHash, err := stableMirrorDirectoryHash(unmanagedDir)
	if err != nil {
		t.Fatalf("stableMirrorDirectoryHash: %v", err)
	}
	audit := &capturingSkillAuditStore{}
	svc := &service{projectRoot: project, projectSkillsRoot: defaultProjectSkillsRoot(project), superDolphinHome: filepath.Join(t.TempDir(), ".super-dolphin"), auditStore: audit, mirrorLocks: NewMirrorRootLockRegistry()}
	target := SkillMirrorTarget{TargetID: "codex:project:repo", Provider: SkillProviderCodex, Scope: skillScopeProject, Root: providerRoot, CanonicalRootID: "repo"}

	if _, err := ImportUnmanagedProviderSkill(context.Background(), svc, SkillMirrorResolutionRequest{
		Action:      "import_to_project",
		Name:        "scratch",
		Target:      target,
		PreviewHash: "wrong",
	}); err == nil {
		t.Fatalf("ImportUnmanagedProviderSkill accepted wrong preview hash")
	}
	report, err := ImportUnmanagedProviderSkill(context.Background(), svc, SkillMirrorResolutionRequest{
		Action:      "import_to_project",
		Name:        "scratch",
		Target:      target,
		PreviewHash: previewHash,
	})
	if err != nil {
		t.Fatalf("ImportUnmanagedProviderSkill: %v report=%+v", err, report)
	}
	assertFileContent(t, filepath.Join(project, ".agents", "skills", "scratch", skillMainFile), "---\nname: scratch\n---\n# scratch\n")
	if _, err := ImportUnmanagedProviderSkill(context.Background(), svc, SkillMirrorResolutionRequest{
		Action:      "import_to_project",
		Name:        "scratch",
		Target:      target,
		PreviewHash: previewHash,
	}); err == nil {
		t.Fatalf("ImportUnmanagedProviderSkill overwrote existing canonical without explicit resolution")
	}

	symlinkRoot := filepath.Join(project, ".bad", "skills")
	if err := os.MkdirAll(filepath.Dir(symlinkRoot), 0o755); err != nil {
		t.Fatalf("MkdirAll symlink parent: %v", err)
	}
	if err := os.Symlink(providerRoot, symlinkRoot); err != nil {
		skipIfSymlinkPrivilegeNotHeld(t, err)
		t.Fatalf("Symlink provider root: %v", err)
	}
	_, err = ImportUnmanagedProviderSkill(context.Background(), svc, SkillMirrorResolutionRequest{
		Action:      "import_to_personal_imported",
		Name:        "scratch",
		Target:      SkillMirrorTarget{TargetID: "bad", Provider: SkillProviderCodex, Scope: skillScopeProject, Root: symlinkRoot, CanonicalRootID: "repo"},
		PreviewHash: previewHash,
	})
	if err == nil {
		t.Fatalf("ImportUnmanagedProviderSkill accepted symlinked provider root")
	}
}

func TestSkillMirrorReconcilerImportProjectUsesProviderMirrorProjectRoot(t *testing.T) {
	project := t.TempDir()
	otherProject := t.TempDir()
	providerRoot := testCodexProjectMirrorRoot(project)
	unmanagedDir := filepath.Join(providerRoot, "scratch")
	writeSkillWithSupportFiles(t, unmanagedDir, "scratch")
	previewHash, err := stableMirrorDirectoryHash(unmanagedDir)
	if err != nil {
		t.Fatalf("stableMirrorDirectoryHash: %v", err)
	}
	svc := &service{projectRoot: otherProject, projectSkillsRoot: defaultProjectSkillsRoot(otherProject), superDolphinHome: filepath.Join(t.TempDir(), ".super-dolphin"), auditStore: &capturingSkillAuditStore{}, mirrorLocks: NewMirrorRootLockRegistry()}
	target := SkillMirrorTarget{TargetID: "codex:project:repo", Provider: SkillProviderCodex, Scope: skillScopeProject, Root: providerRoot, CanonicalRootID: "repo"}

	if _, err := ImportUnmanagedProviderSkill(context.Background(), svc, SkillMirrorResolutionRequest{
		Action:      "import_to_project",
		Name:        "scratch",
		Target:      target,
		PreviewHash: previewHash,
	}); err != nil {
		t.Fatalf("ImportUnmanagedProviderSkill: %v", err)
	}

	assertFileContent(t, filepath.Join(project, ".agents", "skills", "scratch", skillMainFile), "---\nname: scratch\n---\n# scratch\n")
	if _, err := os.Stat(filepath.Join(otherProject, ".agents", "skills", "scratch")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("other project canonical stat error = %v, want not exist", err)
	}
}

func TestSkillMirrorReconcilerTakeoverValidatesPreviewAndWritesOwnership(t *testing.T) {
	project := t.TempDir()
	ownedRoot := filepath.Join(project, ".claude", "skills")
	ownedDir := filepath.Join(ownedRoot, "owned")
	writeSkillWithSupportFiles(t, ownedDir, "owned")
	ownedHash, err := stableMirrorDirectoryHash(ownedDir)
	if err != nil {
		t.Fatalf("stableMirrorDirectoryHash owned: %v", err)
	}
	audit := &capturingSkillAuditStore{}
	svc := &service{projectRoot: project, projectSkillsRoot: defaultProjectSkillsRoot(project), auditStore: audit, mirrorLocks: NewMirrorRootLockRegistry()}
	takeoverTarget := SkillMirrorTarget{TargetID: "claude:project:repo", Provider: SkillProviderClaude, Scope: skillScopeProject, Root: ownedRoot, CanonicalRootID: "repo"}

	if _, err := TakeoverProviderSkill(context.Background(), svc, SkillMirrorResolutionRequest{
		Action:      "takeover_provider_skill",
		Name:        "owned",
		Target:      takeoverTarget,
		PreviewHash: "wrong",
	}); err == nil {
		t.Fatalf("TakeoverProviderSkill accepted wrong preview hash")
	}
	report, err := TakeoverProviderSkill(context.Background(), svc, SkillMirrorResolutionRequest{
		Action:      "takeover_provider_skill",
		Name:        "owned",
		Target:      takeoverTarget,
		PreviewHash: ownedHash,
	})
	if err != nil {
		t.Fatalf("TakeoverProviderSkill: %v", err)
	}
	manifest, err := readSkillMirrorManifest(filepath.Join(ownedRoot, skillMirrorManifestFile))
	if err != nil {
		t.Fatalf("read takeover manifest: %v", err)
	}
	mirrorHash := mustStableMirrorDirectoryHash(t, filepath.Join(ownedRoot, "owned"))
	if !manifest.Skills["owned"].Owned || manifest.Skills["owned"].CanonicalHash != report.ResultingHash || manifest.Skills["owned"].MirrorHash != mirrorHash {
		t.Fatalf("takeover manifest entry = %+v, want canonical=%s mirror=%s", manifest.Skills["owned"], report.ResultingHash, mirrorHash)
	}
}

func TestSkillMirrorReconcilerTakeoverDivergedProviderSkillClearsConflict(t *testing.T) {
	project := t.TempDir()
	canonicalDir := filepath.Join(project, ".agents", "skills", "owned")
	writeSkillWithSupportFiles(t, canonicalDir, "owned")
	providerRoot := filepath.Join(project, ".claude", "skills")
	providerDir := filepath.Join(providerRoot, "owned")
	writeSkillWithSupportFiles(t, providerDir, "owned")
	writeFileWithMode(t, filepath.Join(providerDir, "references", "guide.md"), "provider edit\n", 0o644)
	previewHash, err := stableMirrorDirectoryHash(providerDir)
	if err != nil {
		t.Fatalf("stableMirrorDirectoryHash provider: %v", err)
	}
	svc := &service{projectRoot: project, projectSkillsRoot: defaultProjectSkillsRoot(project), auditStore: &capturingSkillAuditStore{}, mirrorLocks: NewMirrorRootLockRegistry()}
	target := SkillMirrorTarget{TargetID: "claude:project:repo", Provider: SkillProviderClaude, Scope: skillScopeProject, Root: providerRoot, CanonicalRootID: "repo"}

	report, err := TakeoverProviderSkill(context.Background(), svc, SkillMirrorResolutionRequest{
		Action:      "takeover_provider_skill",
		Name:        "owned",
		Target:      target,
		PreviewHash: previewHash,
	})
	if err != nil {
		t.Fatalf("TakeoverProviderSkill: %v report=%+v", err, report)
	}

	assertFileContent(t, filepath.Join(canonicalDir, "references", "guide.md"), "provider edit\n")
	mirrorHash := mustStableMirrorDirectoryHash(t, providerDir)
	assertManagedMirrorManifestEntry(t, target.Root, "owned", report.ResultingHash, mirrorHash)
	assertNoMirrorConflictsForRecord(t, canonicalSkillRecord{Name: "owned", Scope: skillScopeProject, Dir: canonicalDir, DirHash: report.ResultingHash}, target)
}

func TestSkillsChangedJSONRoundTripWithPersonalType(t *testing.T) {
	original := uidtoSkillsChangedForPersonalType("demo", personalSkillTypeAgent)
	encoded, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	var decoded uidto.SkillsChanged
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}
	if decoded.PersonalType != personalSkillTypeAgent {
		t.Fatalf("round-trip personal_type = %q, want agent; decoded=%#v", decoded.PersonalType, decoded)
	}
}

func TestPublishSkillsChangedSeparatesPersonalTypes(t *testing.T) {
	first := normalizeSkillsChanged(uidtoSkillsChangedForPersonalType("same", personalSkillTypeUser))
	second := normalizeSkillsChanged(uidtoSkillsChangedForPersonalType("same", personalSkillTypeAgent))
	if skillsChangedMergeable(first, second) {
		t.Fatalf("personal/user and personal/agent changes with same name should not merge")
	}
}

func findMirrorConflict(t *testing.T, conflicts []SkillMirrorConflict, kind, name, scope string) SkillMirrorConflict {
	t.Helper()
	for _, conflict := range conflicts {
		if conflict.Kind == kind && conflict.Name == name && (scope == "" || conflict.Scope == scope) {
			return conflict
		}
	}
	t.Fatalf("missing conflict kind=%q name=%q scope=%q in %+v", kind, name, scope, conflicts)
	return SkillMirrorConflict{}
}

func assertMirrorConflictActions(t *testing.T, conflict SkillMirrorConflict, want ...string) {
	t.Helper()
	var got []string
	for _, action := range conflict.Actions {
		got = append(got, action.Action)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("conflict actions = %v, want %v; conflict=%+v", got, want, conflict)
	}
}

func assertManagedMirrorManifestEntry(t *testing.T, root, name, canonicalHash, mirrorHash string) {
	t.Helper()
	manifest, err := readSkillMirrorManifest(filepath.Join(root, skillMirrorManifestFile))
	if err != nil {
		t.Fatalf("read mirror manifest: %v", err)
	}
	entry := manifest.Skills[name]
	if entry.CanonicalHash != canonicalHash || entry.MirrorHash != mirrorHash {
		t.Fatalf("manifest entry = %+v, want canonical=%s mirror=%s", entry, canonicalHash, mirrorHash)
	}
}

func assertNoMirrorConflictsForRecord(t *testing.T, record canonicalSkillRecord, target SkillMirrorTarget) {
	t.Helper()
	conflicts, err := DetectSkillMirrorConflicts([]canonicalSkillRecord{record}, []SkillMirrorTarget{target})
	if err != nil {
		t.Fatalf("DetectSkillMirrorConflicts: %v", err)
	}
	if len(conflicts) != 0 {
		t.Fatalf("conflicts after sync-back = %+v, want none", conflicts)
	}
}

func uidtoSkillsChangedForPersonalType(name, personalType string) uidto.SkillsChanged {
	return uidto.SkillsChanged{
		Name:         name,
		Action:       "write",
		Actions:      []string{"write"},
		Count:        1,
		Scope:        skillScopePersonal,
		PersonalType: personalType,
	}
}

type failingActionAuditStore struct {
	failOnAction string
	inserts      []skillMutationAuditEntry
}

func (s *failingActionAuditStore) Insert(_ context.Context, p skillMutationAuditEntry) error {
	if s.failOnAction != "" && p.Action == s.failOnAction {
		return errors.New("injected audit failure")
	}
	s.inserts = append(s.inserts, p)
	return nil
}
