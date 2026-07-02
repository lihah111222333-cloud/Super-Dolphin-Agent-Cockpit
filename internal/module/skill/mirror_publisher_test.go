package skill

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
)

func TestSkillMirrorPublisherPublishesProjectAndPersonalTargets(t *testing.T) {
	project := t.TempDir()
	home := t.TempDir()
	superHome := filepath.Join(home, ".super-dolphin")
	writeSkillWithSupportFiles(t, filepath.Join(project, ".agent", "skills", "build"), "build")
	writeSkillWithSupportFiles(t, filepath.Join(superHome, "skills", "personal", "user", "notes"), "notes")
	records, err := newCanonicalStore(superHome).scan(project)
	if err != nil {
		t.Fatalf("scan canonical records: %v", err)
	}

	claudeRoot := filepath.Join(project, ".claude", "skills")
	codexRoot := providerProjectMirrorRoot(SkillProviderCodex, project)
	personalRoot := filepath.Join(superHome, "providers", "claude", "skills")
	report, err := PublishSkillMirrors(context.Background(), records, []SkillMirrorTarget{
		{TargetID: "claude:project:repo", Provider: SkillProviderClaude, Scope: skillScopeProject, Root: claudeRoot, CanonicalRootID: "repo"},
		{TargetID: "codex:project:repo", Provider: SkillProviderCodex, Scope: skillScopeProject, Root: codexRoot, CanonicalRootID: "repo"},
		{TargetID: "claude:app-managed:owner", Provider: SkillProviderClaude, Scope: skillScopePersonal, Root: personalRoot, CanonicalRootID: "sd_owner:owner"},
	})
	if err != nil {
		t.Fatalf("PublishSkillMirrors: %v", err)
	}

	assertMirrorFile(t, filepath.Join(claudeRoot, "build", skillMainFile), false)
	assertMirrorFile(t, filepath.Join(codexRoot, "build", "references", "guide.md"), false)
	assertMirrorFile(t, filepath.Join(claudeRoot, "build", "templates", "prompt.md"), false)
	assertMirrorFile(t, filepath.Join(codexRoot, "build", "scripts", "run.sh"), true)
	assertMirrorFile(t, filepath.Join(codexRoot, "build", "scripts", "data.txt"), false)
	assertMirrorFile(t, filepath.Join(personalRoot, "notes", skillMainFile), false)
	assertPublishedReportItem(t, report.Published, "claude:project:repo", SkillProviderClaude, skillScopeProject, "build", "project/build")
	assertPublishedReportItem(t, report.Published, "codex:project:repo", SkillProviderCodex, skillScopeProject, "build", "project/build")
	assertPublishedReportItem(t, report.Published, "claude:app-managed:owner", SkillProviderClaude, skillScopePersonal, "notes", "personal/user/notes")

	manifest, err := readSkillMirrorManifest(filepath.Join(codexRoot, skillMirrorManifestFile))
	if err != nil {
		t.Fatalf("read codex manifest: %v", err)
	}
	buildRecord := findCanonicalRecord(t, records, "build", skillScopeProject, "")
	canonicalHash, err := stableMirrorDirectoryHash(buildRecord.Dir)
	if err != nil {
		t.Fatalf("stableMirrorDirectoryHash(canonical build): %v", err)
	}
	if manifest.Skills["build"].CanonicalHash != canonicalHash {
		t.Fatalf("manifest canonical_hash = %q, want stable canonical hash %q", manifest.Skills["build"].CanonicalHash, canonicalHash)
	}
}

func TestSkillMirrorPublisherWritesCanonicalNameAndDisplayName(t *testing.T) {
	project := t.TempDir()
	writeSkillContent(t, filepath.Join(project, ".agent", "skills", "Docker 容器化部署"), "Docker 容器化部署", "# legacy display\n")
	records, err := newCanonicalStore("").scan(project)
	if err != nil {
		t.Fatalf("scan canonical records: %v", err)
	}
	if len(records) != 1 || records[0].Name != "docker-容器化部署" || records[0].info.DisplayName != "Docker 容器化部署" {
		t.Fatalf("records = %+v", records)
	}
	root := providerProjectMirrorRoot(SkillProviderCodex, project)

	_, err = PublishSkillMirrors(context.Background(), records, []SkillMirrorTarget{
		{TargetID: "codex:project:repo", Provider: SkillProviderCodex, Scope: skillScopeProject, Root: root, CanonicalRootID: "repo"},
	})
	if err != nil {
		t.Fatalf("PublishSkillMirrors: %v", err)
	}

	assertFileContent(t, filepath.Join(root, "docker-容器化部署", skillMainFile), "---\nname: docker-容器化部署\ndisplay_name: \"Docker 容器化部署\"\n---\n# legacy display\n")
}

func TestMirrorRootSymlinkIsRejected(t *testing.T) {
	project := t.TempDir()
	writeSkillWithSupportFiles(t, filepath.Join(project, ".agent", "skills", "build"), "build")
	records, err := newCanonicalStore("").scan(project)
	if err != nil {
		t.Fatalf("scan canonical records: %v", err)
	}
	realRoot := filepath.Join(t.TempDir(), "provider-home", "skills")
	if err := os.MkdirAll(realRoot, 0o755); err != nil {
		t.Fatalf("MkdirAll real mirror root: %v", err)
	}
	linkedRoot := filepath.Join(project, ".claude", "skills")
	if err := os.MkdirAll(filepath.Dir(linkedRoot), 0o755); err != nil {
		t.Fatalf("MkdirAll linked parent: %v", err)
	}
	if err := os.Symlink(realRoot, linkedRoot); err != nil {
		skipIfSymlinkPrivilegeNotHeld(t, err)
		t.Fatalf("Symlink valid mirror root: %v", err)
	}

	report, err := PublishSkillMirrors(context.Background(), records, []SkillMirrorTarget{
		{TargetID: "claude:project:repo", Provider: SkillProviderClaude, Scope: skillScopeProject, Root: linkedRoot, CanonicalRootID: "repo"},
	})
	if err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("PublishSkillMirrors symlink root error = %v, want rejection before resolving mirror root", err)
	}

	if len(report.Published) != 0 {
		t.Fatalf("Published = %+v, want no writes through symlink mirror root", report.Published)
	}
	assertMissing(t, filepath.Join(realRoot, "build", skillMainFile))
}

func TestSkillMirrorPublisherDoesNotPublishManualOnlySkills(t *testing.T) {
	project := t.TempDir()
	manualDir := filepath.Join(project, ".agent", "skills", "manual-only")
	if err := os.MkdirAll(manualDir, 0o755); err != nil {
		t.Fatalf("MkdirAll manual skill: %v", err)
	}
	content := "---\nname: manual-only\ndisable_model_invocation: true\n---\n# manual only\n"
	if err := os.WriteFile(filepath.Join(manualDir, skillMainFile), []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile manual skill: %v", err)
	}
	records, err := newCanonicalStore("").scan(project)
	if err != nil {
		t.Fatalf("scan canonical records: %v", err)
	}
	root := providerProjectMirrorRoot(SkillProviderCodex, project)

	report, err := PublishSkillMirrors(context.Background(), records, []SkillMirrorTarget{
		{TargetID: "codex:project:repo", Provider: SkillProviderCodex, Scope: skillScopeProject, Root: root, CanonicalRootID: "repo"},
	})
	if err != nil {
		t.Fatalf("PublishSkillMirrors: %v", err)
	}

	assertMissing(t, filepath.Join(root, "manual-only", skillMainFile))
	if len(report.Published) != 0 {
		t.Fatalf("published = %+v, want none for manual-only skill", report.Published)
	}
}

func TestSkillMirrorPublisherDoesNotOverwriteUnmanagedOrDriftedMirrors(t *testing.T) {
	project := t.TempDir()
	writeSkillWithSupportFiles(t, filepath.Join(project, ".agent", "skills", "build"), "build")
	records, err := newCanonicalStore("").scan(project)
	if err != nil {
		t.Fatalf("scan canonical records: %v", err)
	}
	root := providerProjectMirrorRoot(SkillProviderCodex, project)
	unmanaged := filepath.Join(root, "build")
	if err := os.MkdirAll(unmanaged, 0o755); err != nil {
		t.Fatalf("MkdirAll unmanaged: %v", err)
	}
	if err := os.WriteFile(filepath.Join(unmanaged, skillMainFile), []byte("user copy"), 0o644); err != nil {
		t.Fatalf("WriteFile unmanaged: %v", err)
	}

	report, err := PublishSkillMirrors(context.Background(), records, []SkillMirrorTarget{
		{TargetID: "codex:project:repo", Provider: SkillProviderCodex, Scope: skillScopeProject, Root: root, CanonicalRootID: "repo"},
	})
	if err != nil {
		t.Fatalf("PublishSkillMirrors unmanaged: %v", err)
	}
	assertFileContent(t, filepath.Join(unmanaged, skillMainFile), "user copy")
	assertConflictReportItem(t, report.Conflicts, "codex:project:repo", SkillProviderCodex, skillScopeProject, "build", "project/build", "unmanaged")

	if err := os.RemoveAll(root); err != nil {
		t.Fatalf("RemoveAll root: %v", err)
	}
	report, err = PublishSkillMirrors(context.Background(), records, []SkillMirrorTarget{
		{TargetID: "codex:project:repo", Provider: SkillProviderCodex, Scope: skillScopeProject, Root: root, CanonicalRootID: "repo"},
	})
	if err != nil {
		t.Fatalf("PublishSkillMirrors initial publish: %v", err)
	}
	if len(report.Published) != 1 {
		t.Fatalf("published = %+v, want one initial publish", report.Published)
	}
	if err := os.WriteFile(filepath.Join(root, "build", "references", "guide.md"), []byte("local edit"), 0o644); err != nil {
		t.Fatalf("WriteFile drift: %v", err)
	}
	report, err = PublishSkillMirrors(context.Background(), records, []SkillMirrorTarget{
		{TargetID: "codex:project:repo", Provider: SkillProviderCodex, Scope: skillScopeProject, Root: root, CanonicalRootID: "repo"},
	})
	if err != nil {
		t.Fatalf("PublishSkillMirrors drift: %v", err)
	}
	assertFileContent(t, filepath.Join(root, "build", "references", "guide.md"), "local edit")
	assertConflictReportItem(t, report.Conflicts, "codex:project:repo", SkillProviderCodex, skillScopeProject, "build", "project/build", "drift")
}

func TestSkillMirrorPublisherDeletesAndRegeneratesOwnedMirrors(t *testing.T) {
	project := t.TempDir()
	skillDir := filepath.Join(project, ".agent", "skills", "build")
	writeSkillWithSupportFiles(t, skillDir, "build")
	records, err := newCanonicalStore("").scan(project)
	if err != nil {
		t.Fatalf("scan canonical records: %v", err)
	}
	root := providerProjectMirrorRoot(SkillProviderCodex, project)
	target := SkillMirrorTarget{TargetID: "codex:project:repo", Provider: SkillProviderCodex, Scope: skillScopeProject, Root: root, CanonicalRootID: "repo"}
	if _, err := PublishSkillMirrors(context.Background(), records, []SkillMirrorTarget{target}); err != nil {
		t.Fatalf("PublishSkillMirrors initial: %v", err)
	}
	if err := os.RemoveAll(filepath.Join(root, "build")); err != nil {
		t.Fatalf("RemoveAll mirror skill: %v", err)
	}
	report, err := PublishSkillMirrors(context.Background(), records, []SkillMirrorTarget{target})
	if err != nil {
		t.Fatalf("PublishSkillMirrors regenerate: %v", err)
	}
	assertMirrorFile(t, filepath.Join(root, "build", skillMainFile), false)
	assertPublishedReportItem(t, report.Published, "codex:project:repo", SkillProviderCodex, skillScopeProject, "build", "project/build")

	if err := os.RemoveAll(skillDir); err != nil {
		t.Fatalf("RemoveAll canonical skill: %v", err)
	}
	records, err = newCanonicalStore("").scan(project)
	if err != nil {
		t.Fatalf("rescan canonical records: %v", err)
	}
	report, err = PublishSkillMirrors(context.Background(), records, []SkillMirrorTarget{target})
	if err != nil {
		t.Fatalf("PublishSkillMirrors delete: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "build")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("mirror dir stat err = %v, want not exist", err)
	}
	assertDeletedReportItem(t, report.Deleted, "codex:project:repo", SkillProviderCodex, skillScopeProject, "build", "project/build")
}

func TestSkillMirrorPublisherSkipsUnchangedOwnedMirrors(t *testing.T) {
	project := t.TempDir()
	writeSkillWithSupportFiles(t, filepath.Join(project, ".agent", "skills", "build"), "build")
	records, err := newCanonicalStore("").scan(project)
	if err != nil {
		t.Fatalf("scan canonical records: %v", err)
	}
	target := SkillMirrorTarget{TargetID: "codex:project:repo", Provider: SkillProviderCodex, Scope: skillScopeProject, Root: providerProjectMirrorRoot(SkillProviderCodex, project), CanonicalRootID: "repo"}
	if _, err := PublishSkillMirrors(context.Background(), records, []SkillMirrorTarget{target}); err != nil {
		t.Fatalf("PublishSkillMirrors initial: %v", err)
	}
	info, err := os.Stat(filepath.Join(target.Root, "build"))
	if err != nil {
		t.Fatalf("Stat mirror dir: %v", err)
	}
	initialModTime := info.ModTime()

	report, err := PublishSkillMirrors(context.Background(), records, []SkillMirrorTarget{target})
	if err != nil {
		t.Fatalf("PublishSkillMirrors unchanged: %v", err)
	}

	assertSkippedReportItem(t, report.Skipped, "codex:project:repo", SkillProviderCodex, skillScopeProject, "build", "project/build")
	if got := len(report.Published); got != 0 {
		t.Fatalf("published = %+v, want none for unchanged mirror", report.Published)
	}
	info, err = os.Stat(filepath.Join(target.Root, "build"))
	if err != nil {
		t.Fatalf("Stat mirror dir after skip: %v", err)
	}
	if !info.ModTime().Equal(initialModTime) {
		t.Fatalf("mirror dir modtime changed on skip: before=%s after=%s", initialModTime, info.ModTime())
	}
}

func TestSkillMirrorPublisherFailsClosedOnSymlinksAndPathEscape(t *testing.T) {
	project := t.TempDir()
	writeSkillWithSupportFiles(t, filepath.Join(project, ".agent", "skills", "build"), "build")
	records, err := newCanonicalStore("").scan(project)
	if err != nil {
		t.Fatalf("scan canonical records: %v", err)
	}
	legacyRoot := filepath.Join(project, ".claude", "skills")
	if err := os.MkdirAll(filepath.Dir(legacyRoot), 0o755); err != nil {
		t.Fatalf("MkdirAll legacy parent: %v", err)
	}
	if err := os.Symlink(filepath.Join(project, ".multi-agent", "skills-cache"), legacyRoot); err != nil {
		skipIfSymlinkPrivilegeNotHeld(t, err)
		t.Fatalf("Symlink legacy root: %v", err)
	}
	_, err = PublishSkillMirrors(context.Background(), records, []SkillMirrorTarget{
		{TargetID: "claude:project:repo", Provider: SkillProviderClaude, Scope: skillScopeProject, Root: legacyRoot, CanonicalRootID: "repo"},
	})
	if err == nil {
		t.Fatalf("PublishSkillMirrors legacy symlink root succeeded, want fail closed")
	}

	badRoot := providerProjectMirrorRoot(SkillProviderCodex, project)
	if err := os.MkdirAll(badRoot, 0o755); err != nil {
		t.Fatalf("MkdirAll bad root: %v", err)
	}
	if err := os.Symlink(t.TempDir(), filepath.Join(badRoot, "build")); err != nil {
		skipIfSymlinkPrivilegeNotHeld(t, err)
		t.Fatalf("Symlink final mirror dir: %v", err)
	}
	_, err = PublishSkillMirrors(context.Background(), records, []SkillMirrorTarget{
		{TargetID: "codex:project:repo", Provider: SkillProviderCodex, Scope: skillScopeProject, Root: badRoot, CanonicalRootID: "repo"},
	})
	if err == nil {
		t.Fatalf("PublishSkillMirrors symlink final dir succeeded, want fail closed")
	}

	escapeRoot := filepath.Join(project, ".agents", "escape")
	_, err = PublishSkillMirrors(context.Background(), records, []SkillMirrorTarget{
		{TargetID: "codex:project:repo", Provider: SkillProviderCodex, Scope: skillScopeProject, Root: escapeRoot, CanonicalRootID: "repo"},
		{TargetID: "codex:project:escape", Provider: SkillProviderCodex, Scope: skillScopeProject, Root: filepath.Join(escapeRoot, ".."), CanonicalRootID: "repo"},
	})
	if err == nil {
		t.Fatalf("PublishSkillMirrors accepted unsafe relative target root")
	}
}

func TestSkillMirrorPublisherRejectsParentSymlinkRoot(t *testing.T) {
	project := t.TempDir()
	writeSkillWithSupportFiles(t, filepath.Join(project, ".agent", "skills", "build"), "build")
	records, err := newCanonicalStore("").scan(project)
	if err != nil {
		t.Fatalf("scan canonical records: %v", err)
	}
	root := filepath.Join(project, ".agents-parent-symlink", "skills")
	if err := os.Symlink(t.TempDir(), filepath.Dir(root)); err != nil {
		skipIfSymlinkPrivilegeNotHeld(t, err)
		t.Fatalf("Symlink parent mirror dir: %v", err)
	}

	_, err = PublishSkillMirrors(context.Background(), records, []SkillMirrorTarget{
		{TargetID: "codex:project:repo", Provider: SkillProviderCodex, Scope: skillScopeProject, Root: root, CanonicalRootID: "repo"},
	})
	if err == nil {
		t.Fatalf("PublishSkillMirrors parent symlink root succeeded, want fail closed")
	}
}

func TestSkillMirrorPublisherRejectsCanonicalSymlinkEntries(t *testing.T) {
	project := t.TempDir()
	dir := filepath.Join(project, ".agent", "skills", "build")
	writeSkillWithSupportFiles(t, dir, "build")
	if err := os.Symlink(filepath.Join(t.TempDir(), "secret"), filepath.Join(dir, "references", "escape.md")); err != nil {
		skipIfSymlinkPrivilegeNotHeld(t, err)
		t.Fatalf("Symlink canonical entry: %v", err)
	}
	records, err := newCanonicalStore("").scan(project)
	if err != nil {
		t.Fatalf("scan canonical records: %v", err)
	}

	_, err = PublishSkillMirrors(context.Background(), records, []SkillMirrorTarget{
		{TargetID: "codex:project:repo", Provider: SkillProviderCodex, Scope: skillScopeProject, Root: providerProjectMirrorRoot(SkillProviderCodex, project), CanonicalRootID: "repo"},
	})
	if err == nil {
		t.Fatalf("PublishSkillMirrors accepted canonical symlink entry")
	}
}

func TestSkillMirrorPublisherStopsOnOversizedCanonicalFile(t *testing.T) {
	project := t.TempDir()
	dir := filepath.Join(project, ".agent", "skills", "build")
	writeSkillContent(t, dir, "build", "# build\n")
	hugeFile := filepath.Join(dir, "references", "huge.bin")
	if err := os.MkdirAll(filepath.Dir(hugeFile), 0o755); err != nil {
		t.Fatalf("MkdirAll oversized support dir: %v", err)
	}
	if err := os.WriteFile(hugeFile, make([]byte, maxSkillFileBytes+1), 0o644); err != nil {
		t.Fatalf("WriteFile oversized support file: %v", err)
	}
	record := canonicalSkillRecord{
		Name:        "build",
		Scope:       skillScopeProject,
		Dir:         dir,
		SkillFile:   filepath.Join(dir, skillMainFile),
		ContentHash: "content",
		DirHash:     "dir",
		info:        SkillInfo{Name: "build"},
	}
	root := providerProjectMirrorRoot(SkillProviderCodex, project)

	_, err := PublishSkillMirrors(context.Background(), []canonicalSkillRecord{record}, []SkillMirrorTarget{
		{TargetID: "codex:project:repo", Provider: SkillProviderCodex, Scope: skillScopeProject, Root: root, CanonicalRootID: "repo"},
	})
	if err == nil || !strings.Contains(err.Error(), "too large") {
		t.Fatalf("PublishSkillMirrors oversized file error = %v, want too large", err)
	}
	assertMissing(t, filepath.Join(root, "build", skillMainFile))
}

func TestSkillMirrorPublisherRejectsUnsafeManifestSkillNames(t *testing.T) {
	project := t.TempDir()
	writeSkillWithSupportFiles(t, filepath.Join(project, ".agent", "skills", "build"), "build")
	records, err := newCanonicalStore("").scan(project)
	if err != nil {
		t.Fatalf("scan canonical records: %v", err)
	}
	root := providerProjectMirrorRoot(SkillProviderCodex, project)
	target := SkillMirrorTarget{TargetID: "codex:project:repo", Provider: SkillProviderCodex, Scope: skillScopeProject, Root: root, CanonicalRootID: "repo"}
	if _, err := PublishSkillMirrors(context.Background(), records, []SkillMirrorTarget{target}); err != nil {
		t.Fatalf("PublishSkillMirrors initial: %v", err)
	}
	manifest, err := readSkillMirrorManifest(filepath.Join(root, skillMirrorManifestFile))
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	entry := manifest.Skills["build"]
	delete(manifest.Skills, "build")
	manifest.Skills["../escape"] = entry
	data, err := json.Marshal(manifest)
	if err != nil {
		t.Fatalf("Marshal manifest: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, skillMirrorManifestFile), data, 0o644); err != nil {
		t.Fatalf("WriteFile manifest: %v", err)
	}

	_, err = PublishSkillMirrors(context.Background(), nil, []SkillMirrorTarget{target})
	if err == nil || !strings.Contains(err.Error(), "unsafe skill name") {
		t.Fatalf("PublishSkillMirrors unsafe manifest error = %v, want unsafe skill name", err)
	}
	if _, err := os.Stat(root); err != nil {
		t.Fatalf("mirror root stat after rejected manifest: %v", err)
	}
}

func TestSkillMirrorReportJSONUsesStableSnakeCaseFields(t *testing.T) {
	report := SkillMirrorReport{Published: []SkillMirrorReportItem{{
		TargetID:           "codex:project:repo",
		Provider:           SkillProviderCodex,
		Scope:              skillScopeProject,
		RelativeMirrorPath: "build",
		CanonicalID:        "project/build",
		OldHash:            "old",
		NewHash:            "new",
		ConflictKind:       "publish_error",
		Error:              "detail",
	}}}

	data, err := json.Marshal(report)
	if err != nil {
		t.Fatalf("Marshal report: %v", err)
	}
	body := string(data)
	for _, want := range []string{`"published"`, `"target_id"`, `"relative_mirror_path"`, `"canonical_id"`, `"old_hash"`, `"new_hash"`, `"conflict_kind"`, `"error"`} {
		if !strings.Contains(body, want) {
			t.Fatalf("report json = %s, missing %s", body, want)
		}
	}
	for _, forbidden := range []string{`"Published"`, `"TargetID"`, `"RelativeMirrorPath"`, `"CanonicalID"`} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("report json = %s, contains Go field %s", body, forbidden)
		}
	}
}

func TestReconcileProviderMirrorsDerivesProjectAndPersonalTargetIDs(t *testing.T) {
	project := t.TempDir()
	superHome := filepath.Join(t.TempDir(), ".super-dolphin")
	writeSkillWithSupportFiles(t, filepath.Join(project, ".agent", "skills", "build"), "build")
	writeSkillWithSupportFiles(t, filepath.Join(superHome, "skills", "personal", "user", "notes"), "notes")
	owner, err := resolveOwnerIdentity(superHome, defaultOwnerOSUID(), defaultAppProfile())
	if err != nil {
		t.Fatalf("resolveOwnerIdentity: %v", err)
	}
	svc := &service{projectRoot: project, projectSkillsRoot: defaultProjectSkillsRoot(project), superDolphinHome: superHome}
	codexProjectRoot := providerProjectMirrorRoot(SkillProviderCodex, project)
	claudePersonalHome := filepath.Join(superHome, "providers", "claude")
	claudePersonalRoot := filepath.Join(claudePersonalHome, "skills")

	report, err := svc.ReconcileProviderMirrors(context.Background(), project, []contract.SkillProviderMirrorTarget{
		{Provider: "codex", HomeRoot: filepath.Join(project, "spoofed-home-does-not-affect-target-id"), SkillsRoot: codexProjectRoot},
		{Provider: "claude", HomeRoot: claudePersonalHome, SkillsRoot: claudePersonalRoot},
	})
	if err != nil {
		t.Fatalf("ReconcileProviderMirrors: %v", err)
	}

	assertPublishedReportItem(t, report.Published, "codex:project:"+RepoFingerprint(project), SkillProviderCodex, skillScopeProject, "build", "project/build")
	assertPublishedReportItem(t, report.Published, "claude:app-managed:"+owner.OwnerKey, SkillProviderClaude, skillScopePersonal, "notes", "personal/user/notes")
	assertMirrorFile(t, filepath.Join(codexProjectRoot, "build", skillMainFile), false)
	assertMirrorFile(t, filepath.Join(claudePersonalRoot, "notes", skillMainFile), false)
}

func TestReconcileProviderMirrorsReportsSameNameConflictButContinuesSafeMirrorWrites(t *testing.T) {
	project := t.TempDir()
	superHome := filepath.Join(t.TempDir(), ".super-dolphin")
	svc := &service{projectRoot: project, projectSkillsRoot: defaultProjectSkillsRoot(project), superDolphinHome: superHome}
	codexProjectRoot := providerProjectMirrorRoot(SkillProviderCodex, project)
	targets := []contract.SkillProviderMirrorTarget{{
		Provider:   "codex",
		SkillsRoot: codexProjectRoot,
	}}

	writeSkillWithSupportFiles(t, filepath.Join(project, ".agent", "skills", "old"), "old")
	if _, err := svc.ReconcileProviderMirrors(context.Background(), project, targets); err != nil {
		t.Fatalf("initial ReconcileProviderMirrors: %v", err)
	}
	assertMirrorFile(t, filepath.Join(codexProjectRoot, "old", skillMainFile), false)

	if err := os.RemoveAll(filepath.Join(project, ".agent", "skills", "old")); err != nil {
		t.Fatalf("RemoveAll old canonical skill: %v", err)
	}
	writeSkillWithSupportFiles(t, filepath.Join(project, ".agent", "skills", "safe"), "safe")
	writeSkillWithSupportFiles(t, filepath.Join(project, ".agent", "skills", "build"), "build")
	writeSkillWithSupportFiles(t, filepath.Join(superHome, "skills", "personal", "user", "build"), "build")

	report, err := svc.ReconcileProviderMirrors(context.Background(), project, targets)
	if err != nil {
		t.Fatalf("conflicted ReconcileProviderMirrors: %v", err)
	}

	assertConflictReportItem(t, report.Conflicts, "codex:project:"+RepoFingerprint(project), SkillProviderCodex, skillScopeProject, "build", "", "same_name")
	assertMirrorFile(t, filepath.Join(codexProjectRoot, "safe", skillMainFile), false)
	assertMissing(t, filepath.Join(codexProjectRoot, "old", skillMainFile))
	assertPublishedReportItem(t, report.Published, "codex:project:"+RepoFingerprint(project), SkillProviderCodex, skillScopeProject, "safe", "project/safe")
	assertDeletedReportItem(t, report.Deleted, "codex:project:"+RepoFingerprint(project), SkillProviderCodex, skillScopeProject, "old", "project/old")
}

func TestReconcileProviderMirrorsDoesNotReportConflictedProjectSkillAsUnmanagedProviderSkill(t *testing.T) {
	project := t.TempDir()
	superHome := filepath.Join(t.TempDir(), ".super-dolphin")
	svc := &service{projectRoot: project, projectSkillsRoot: defaultProjectSkillsRoot(project), superDolphinHome: superHome}
	codexProjectRoot := providerProjectMirrorRoot(SkillProviderCodex, project)
	targets := []contract.SkillProviderMirrorTarget{{
		Provider:   "codex",
		SkillsRoot: codexProjectRoot,
	}}
	writeSkillWithSupportFiles(t, filepath.Join(project, ".agent", "skills", "build"), "build")
	writeSkillWithSupportFiles(t, filepath.Join(superHome, "skills", "personal", "user", "build"), "build")

	report, err := svc.ReconcileProviderMirrors(context.Background(), project, targets)
	if err != nil {
		t.Fatalf("ReconcileProviderMirrors: %v", err)
	}

	assertConflictReportItem(t, report.Conflicts, "codex:project:"+RepoFingerprint(project), SkillProviderCodex, skillScopeProject, "build", "", "same_name")
	for _, item := range report.Conflicts {
		if item.Provider == SkillProviderCodex && item.Scope == skillScopeProject && item.RelativeMirrorPath == "build" && item.ConflictKind == "unmanaged_provider_skill" {
			t.Fatalf("conflicts include project canonical skill as unmanaged provider skill: %+v", report.Conflicts)
		}
	}
}

func TestReconcileProviderMirrorsUsesAgentsPolicyRootAsCanonicalSelfMirror(t *testing.T) {
	project := t.TempDir()
	superHome := filepath.Join(t.TempDir(), ".super-dolphin")
	agentsRoot := filepath.Join(project, ".agents", "skills")
	writeSkillWithSupportFiles(t, filepath.Join(agentsRoot, "Agent工程学"), "Agent工程学")
	if _, err := writeSkillPolicyJSON(filepath.Join(agentsRoot, projectSkillPolicyFile), projectSkillPolicy{Version: 1}, 0o644); err != nil {
		t.Fatalf("write .agents project policy: %v", err)
	}
	svc := &service{projectRoot: project, projectSkillsRoot: defaultProjectSkillsRoot(project), superDolphinHome: superHome}

	report, err := svc.ReconcileProviderMirrors(context.Background(), project, []contract.SkillProviderMirrorTarget{{
		Provider:   "codex",
		SkillsRoot: agentsRoot,
	}})
	if err != nil {
		t.Fatalf("ReconcileProviderMirrors: %v", err)
	}

	if len(report.Conflicts) > 0 {
		t.Fatalf("conflicts = %+v, want none for canonical .agents self mirror", report.Conflicts)
	}
	manifest, err := readSkillMirrorManifest(filepath.Join(agentsRoot, skillMirrorManifestFile))
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	entry := manifest.Skills["Agent工程学"]
	if !entry.Owned || entry.CanonicalID != "project/Agent工程学" || entry.CanonicalHash == "" || entry.MirrorHash == "" {
		t.Fatalf("manifest entry = %+v, want canonical .agents skill recorded without self-copy", entry)
	}
	assertMirrorFile(t, filepath.Join(agentsRoot, "Agent工程学", skillMainFile), false)
}

func TestReconcileProviderMirrorsReportsOrphanUnmanagedProviderSkill(t *testing.T) {
	project := t.TempDir()
	superHome := filepath.Join(t.TempDir(), ".super-dolphin")
	svc := &service{projectRoot: project, projectSkillsRoot: defaultProjectSkillsRoot(project), superDolphinHome: superHome}
	codexProjectRoot := providerProjectMirrorRoot(SkillProviderCodex, project)
	writeSkillWithSupportFiles(t, filepath.Join(codexProjectRoot, "scratch"), "scratch")

	report, err := svc.ReconcileProviderMirrors(context.Background(), project, []contract.SkillProviderMirrorTarget{{
		Provider:   "codex",
		SkillsRoot: codexProjectRoot,
	}})
	if err != nil {
		t.Fatalf("ReconcileProviderMirrors: %v", err)
	}

	assertConflictReportItem(t, report.Conflicts, "codex:project:"+RepoFingerprint(project), SkillProviderCodex, skillScopeProject, "scratch", "", "unmanaged_provider_skill")
	assertMirrorFile(t, filepath.Join(codexProjectRoot, "scratch", skillMainFile), false)
}

func TestReconcileProviderMirrorsRecognizesProjectTargetThroughSymlinkCWD(t *testing.T) {
	realProject := filepath.Join(t.TempDir(), "real-project")
	if err := os.MkdirAll(realProject, 0o755); err != nil {
		t.Fatalf("MkdirAll real project: %v", err)
	}
	aliasProject := filepath.Join(t.TempDir(), "alias-project")
	if err := os.Symlink(realProject, aliasProject); err != nil {
		skipIfSymlinkPrivilegeNotHeld(t, err)
		t.Fatalf("Symlink project: %v", err)
	}
	writeSkillWithSupportFiles(t, filepath.Join(realProject, ".agent", "skills", "build"), "build")
	svc := &service{projectRoot: aliasProject, projectSkillsRoot: defaultProjectSkillsRoot(aliasProject), superDolphinHome: newTestSuperDolphinHome(t)}
	codexProjectRoot := providerProjectMirrorRoot(SkillProviderCodex, realProject)

	report, err := svc.ReconcileProviderMirrors(context.Background(), aliasProject, []contract.SkillProviderMirrorTarget{{
		Provider:   "codex",
		SkillsRoot: codexProjectRoot,
	}})
	if err != nil {
		t.Fatalf("ReconcileProviderMirrors symlink cwd: %v", err)
	}

	assertPublishedReportItem(t, report.Published, "codex:project:"+RepoFingerprint(realProject), SkillProviderCodex, skillScopeProject, "build", "project/build")
	assertMirrorFile(t, filepath.Join(codexProjectRoot, "build", skillMainFile), false)
}

func TestReconcileProviderMirrorsUsesProjectMirrorRootWhenCWDIsSubdir(t *testing.T) {
	project := t.TempDir()
	subdir := filepath.Join(project, "cmd", "agent-terminal")
	if err := os.MkdirAll(filepath.Join(project, ".git"), 0o755); err != nil {
		t.Fatalf("MkdirAll .git: %v", err)
	}
	if err := os.MkdirAll(subdir, 0o755); err != nil {
		t.Fatalf("MkdirAll subdir: %v", err)
	}
	superHome := filepath.Join(t.TempDir(), ".super-dolphin")
	writeSkillWithSupportFiles(t, filepath.Join(project, ".agent", "skills", "build"), "build")
	svc := &service{projectRoot: project, projectSkillsRoot: defaultProjectSkillsRoot(project), superDolphinHome: superHome}
	codexProjectRoot := providerProjectMirrorRoot(SkillProviderCodex, project)

	report, err := svc.ReconcileProviderMirrors(context.Background(), subdir, []contract.SkillProviderMirrorTarget{{
		Provider:   "codex",
		HomeRoot:   filepath.Join(superHome, "providers", "codex"),
		SkillsRoot: codexProjectRoot,
	}})
	if err != nil {
		t.Fatalf("ReconcileProviderMirrors subdir cwd: %v", err)
	}
	if len(report.Conflicts) > 0 {
		t.Fatalf("conflicts = %+v", report.Conflicts)
	}
	assertMirrorFile(t, filepath.Join(codexProjectRoot, "build", skillMainFile), false)
}
