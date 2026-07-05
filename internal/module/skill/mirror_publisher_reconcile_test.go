package skill

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	providershared "github.com/anthropic-ai/super-agent-v3/internal/provider/shared"
	auditstore "github.com/anthropic-ai/super-agent-v3/internal/store/auditlog"
	"github.com/kelindar/event"
	"go.uber.org/fx"
)

func TestProjectMirrorCapsTrustFrontmatter(t *testing.T) {
	project := t.TempDir()
	superHome := filepath.Join(t.TempDir(), ".super-dolphin")
	cases := map[string]string{"trust-scope-signed": "trust_scope: signed", "trustscope-verified": "trustscope: verified", "trust-verified": "trust: VERIFIED", "trust-user": "trust: user", "trust-signed": "trust: signed", "trust-trusted": "trust: trusted"}
	for name, trustLine := range cases {
		skillDir := filepath.Join(project, ".agents", "skills", name)
		content := "---\nname: " + name + "\n" + trustLine + "\n---\n# " + name + "\n"
		writeFileWithMode(t, filepath.Join(skillDir, skillMainFile), content, 0o644)
	}
	writeFileWithMode(t, filepath.Join(superHome, "skills", "personal", "user", "personal-elevated", skillMainFile), "---\nname: personal-elevated\ntrust: signed\n---\n# personal\n", 0o644)
	records, err := newCanonicalStore(superHome).scan(project)
	if err != nil {
		t.Fatalf("scan canonical records: %v", err)
	}
	codexRoot := testCodexProjectMirrorRoot(project)
	personalRoot := filepath.Join(superHome, "providers", "codex", "skills")

	if _, err := PublishSkillMirrors(context.Background(), records, []SkillMirrorTarget{{
		TargetID:        "codex:project:" + RepoFingerprint(project),
		Provider:        SkillProviderCodex,
		Scope:           skillScopeProject,
		Root:            codexRoot,
		CanonicalRootID: RepoFingerprint(project),
	}, {
		TargetID:        "codex:app-managed:owner",
		Provider:        SkillProviderCodex,
		Scope:           skillScopePersonal,
		Root:            personalRoot,
		CanonicalRootID: "sd_owner:owner",
	}}); err != nil {
		t.Fatalf("PublishSkillMirrors: %v", err)
	}
	for name := range cases {
		gotPath := filepath.Join(codexRoot, name, skillMainFile)
		assertFileContent(t, gotPath, "---\nname: "+name+"\ntrust: project\n---\n# "+name+"\n")
	}
	assertFileContent(t, filepath.Join(personalRoot, "personal-elevated", skillMainFile), "---\nname: personal-elevated\ntrust: signed\n---\n# personal\n")
}

func TestReconcileProviderMirrorsUsesRequestCWDWhenServiceProjectRootDiffers(t *testing.T) {
	serviceProject := filepath.Join(t.TempDir(), "service-project")
	requestProject := filepath.Join(t.TempDir(), "request-project")
	if err := os.MkdirAll(filepath.Join(serviceProject, ".git"), 0o755); err != nil {
		t.Fatalf("MkdirAll service .git: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(requestProject, ".git"), 0o755); err != nil {
		t.Fatalf("MkdirAll request .git: %v", err)
	}
	superHome := filepath.Join(t.TempDir(), ".super-dolphin")
	providerHome := filepath.Join(superHome, "providers", "codex")
	writeSkillWithSupportFiles(t, filepath.Join(requestProject, ".agents", "skills", "build"), "build")
	svc := &service{projectRoot: serviceProject, projectSkillsRoot: defaultProjectSkillsRoot(serviceProject), superDolphinHome: superHome}
	targets, err := providershared.ProviderMirrorTargets(providershared.ProviderCodex, requestProject, providerHome)
	if err != nil {
		t.Fatalf("ProviderMirrorTargets: %v", err)
	}

	report, err := svc.ReconcileProviderMirrors(context.Background(), requestProject, targets)
	if err != nil {
		t.Fatalf("ReconcileProviderMirrors request cwd: %v", err)
	}
	if len(report.Conflicts) != 0 {
		t.Fatalf("conflicts = %+v", report.Conflicts)
	}

	requestProjectRoot := providerProjectMirrorRoot(SkillProviderCodex, requestProject)
	assertMirrorFile(t, filepath.Join(requestProjectRoot, "build", skillMainFile), false)
	if _, err := readSkillMirrorManifest(filepath.Join(requestProjectRoot, skillMirrorManifestFile)); err != nil {
		t.Fatalf("read request project self manifest: %v", err)
	}
	assertMissing(t, filepath.Join(providerProjectMirrorRoot(SkillProviderCodex, serviceProject), "build", skillMainFile))
}

func TestReconcileProviderMirrorsPublishesPackagedProjectMirrorToWritableHome(t *testing.T) {
	resources := filepath.Join(t.TempDir(), "Super Dolphin.app", "Contents", "Resources")
	superHome := filepath.Join(t.TempDir(), ".super-dolphin")
	if err := os.MkdirAll(resources, 0o755); err != nil {
		t.Fatalf("MkdirAll resources: %v", err)
	}
	writeSkillWithSupportFiles(t, filepath.Join(resources, ".agents", "skills", "packaged"), "packaged")
	t.Setenv("SUPER_DOLPHIN_HOME", superHome)
	t.Setenv("PROJECT_ROOT", resources)
	t.Setenv("SUPER_DOLPHIN_RUNTIME_MODE", "packaged")
	t.Setenv("SUPER_DOLPHIN_PACKAGED_CODEX_IDENTITY", "1")
	svc := &service{projectRoot: resources, projectSkillsRoot: defaultProjectSkillsRoot(resources), superDolphinHome: superHome}
	targets, err := providershared.ProviderMirrorTargets(providershared.ProviderCodex, resources, filepath.Join(superHome, "providers", "codex"))
	if err != nil {
		t.Fatalf("ProviderMirrorTargets: %v", err)
	}

	if _, err := svc.ReconcileProviderMirrors(context.Background(), resources, targets); err != nil {
		t.Fatalf("ReconcileProviderMirrors packaged: %v", err)
	}

	writableProjectRoot := filepath.Join(superHome, "provider-mirrors", "project", "codex", "skills")
	assertMirrorFile(t, filepath.Join(writableProjectRoot, "packaged", skillMainFile), false)
	assertMirrorFile(t, filepath.Join(resources, ".agents", "skills", "packaged", skillMainFile), false)
}

func TestPersonalMirrorDriftBlocksProviderStartup(t *testing.T) {
	project := t.TempDir()
	superHome := filepath.Join(t.TempDir(), ".super-dolphin")
	writeSkillWithSupportFiles(t, filepath.Join(superHome, "skills", "personal", "user", "notes"), "notes")
	records, err := newCanonicalStore(superHome).scan(project)
	if err != nil {
		t.Fatalf("scan canonical records: %v", err)
	}
	target := SkillMirrorTarget{
		TargetID:        "codex:app-managed:owner",
		Provider:        SkillProviderCodex,
		Scope:           skillScopePersonal,
		Root:            filepath.Join(superHome, "providers", "codex", "skills"),
		CanonicalRootID: "sd_owner:owner",
	}
	if _, err := PublishSkillMirrors(context.Background(), records, []SkillMirrorTarget{target}); err != nil {
		t.Fatalf("PublishSkillMirrors initial: %v", err)
	}
	if err := os.WriteFile(filepath.Join(target.Root, "notes", "references", "guide.md"), []byte("personal edit"), 0o644); err != nil {
		t.Fatalf("WriteFile personal drift: %v", err)
	}

	report, err := PublishSkillMirrors(context.Background(), records, []SkillMirrorTarget{target})
	if err != nil {
		t.Fatalf("PublishSkillMirrors drift: %v", err)
	}

	assertFileContent(t, filepath.Join(target.Root, "notes", "references", "guide.md"), "personal edit")
	assertConflictReportItem(t, report.Conflicts, target.TargetID, SkillProviderCodex, skillScopePersonal, "notes", "personal/user/notes", "drift")
	personalDrift := findReportItem(t, report.Conflicts, target.TargetID, SkillProviderCodex, skillScopePersonal, "notes", "personal/user/notes")
	if personalDrift.ConflictKind != "drift" || personalDrift.OldHash == "" {
		t.Fatalf("personal drift conflict item = %+v, want drift kind with old hash", personalDrift)
	}
	if len(report.Skipped) != 0 {
		t.Fatalf("skipped = %+v, want none for provider-readable personal drift", report.Skipped)
	}
	if err := providershared.EnsureNoSkillMirrorConflicts(contract.SkillMirrorReport(report)); err == nil || !strings.Contains(err.Error(), "drift") {
		t.Fatalf("EnsureNoSkillMirrorConflicts personal drift error = %v, want provider startup blocked", err)
	}
}

func TestDeletedPersonalMirrorDriftBlocksProviderStartup(t *testing.T) {
	project := t.TempDir()
	superHome := filepath.Join(t.TempDir(), ".super-dolphin")
	canonicalDir := filepath.Join(superHome, "skills", "personal", "user", "notes")
	writeSkillWithSupportFiles(t, canonicalDir, "notes")
	target := SkillMirrorTarget{
		TargetID:        "codex:app-managed:owner",
		Provider:        SkillProviderCodex,
		Scope:           skillScopePersonal,
		Root:            filepath.Join(superHome, "providers", "codex", "skills"),
		CanonicalRootID: "sd_owner:owner",
	}
	report := publishDeletedWithDriftMirror(t, project, superHome, canonicalDir, target, "notes", "personal deleted drift")

	assertFileContent(t, filepath.Join(target.Root, "notes", "references", "guide.md"), "personal deleted drift")
	assertConflictReportItem(t, report.Conflicts, target.TargetID, SkillProviderCodex, skillScopePersonal, "notes", "personal/user/notes", "drift")
	personalDrift := findReportItem(t, report.Conflicts, target.TargetID, SkillProviderCodex, skillScopePersonal, "notes", "personal/user/notes")
	if personalDrift.ConflictKind != "drift" || personalDrift.OldHash == "" {
		t.Fatalf("personal deleted drift conflict item = %+v, want drift kind with old hash", personalDrift)
	}
	if len(report.Skipped) != 0 {
		t.Fatalf("skipped = %+v, want none for provider-readable personal deleted drift", report.Skipped)
	}
	if err := providershared.EnsureNoSkillMirrorConflicts(contract.SkillMirrorReport(report)); err == nil || !strings.Contains(err.Error(), "drift") {
		t.Fatalf("EnsureNoSkillMirrorConflicts personal deleted drift error = %v, want provider startup blocked", err)
	}
}

func TestSkillMirrorPublisherReportsProjectManagedDriftAsBlockingConflict(t *testing.T) {
	project := t.TempDir()
	writeSkillWithSupportFiles(t, filepath.Join(project, ".agents", "skills", "build"), "build")
	records, err := newCanonicalStore("").scan(project)
	if err != nil {
		t.Fatalf("scan canonical records: %v", err)
	}
	target := SkillMirrorTarget{
		TargetID:        "codex:project:repo",
		Provider:        SkillProviderCodex,
		Scope:           skillScopeProject,
		Root:            testCodexProjectMirrorRoot(project),
		CanonicalRootID: "repo",
	}
	if _, err := PublishSkillMirrors(context.Background(), records, []SkillMirrorTarget{target}); err != nil {
		t.Fatalf("PublishSkillMirrors initial: %v", err)
	}
	if err := os.WriteFile(filepath.Join(target.Root, "build", "references", "guide.md"), []byte("project edit"), 0o644); err != nil {
		t.Fatalf("WriteFile project drift: %v", err)
	}

	report, err := PublishSkillMirrors(context.Background(), records, []SkillMirrorTarget{target})
	if err != nil {
		t.Fatalf("PublishSkillMirrors drift: %v", err)
	}

	assertFileContent(t, filepath.Join(target.Root, "build", "references", "guide.md"), "project edit")
	assertConflictReportItem(t, report.Conflicts, target.TargetID, SkillProviderCodex, skillScopeProject, "build", "project/build", "drift")
	if err := providershared.EnsureNoSkillMirrorConflicts(contract.SkillMirrorReport(report)); err == nil || !strings.Contains(err.Error(), "drift") {
		t.Fatalf("EnsureNoSkillMirrorConflicts project drift error = %v, want provider startup blocked", err)
	}
}

func TestSkillMirrorPublisherReportsProjectDeletedWithDriftAsBlockingConflict(t *testing.T) {
	project := t.TempDir()
	canonicalDir := filepath.Join(project, ".agents", "skills", "build")
	writeSkillWithSupportFiles(t, canonicalDir, "build")
	target := SkillMirrorTarget{
		TargetID:        "codex:project:repo",
		Provider:        SkillProviderCodex,
		Scope:           skillScopeProject,
		Root:            testCodexProjectMirrorRoot(project),
		CanonicalRootID: "repo",
	}
	report := publishDeletedWithDriftMirror(t, project, "", canonicalDir, target, "build", "project deleted drift")

	assertFileContent(t, filepath.Join(target.Root, "build", "references", "guide.md"), "project deleted drift")
	assertConflictReportItem(t, report.Conflicts, target.TargetID, SkillProviderCodex, skillScopeProject, "build", "project/build", "drift")
	if err := providershared.EnsureNoSkillMirrorConflicts(contract.SkillMirrorReport(report)); err == nil || !strings.Contains(err.Error(), "drift") {
		t.Fatalf("EnsureNoSkillMirrorConflicts project deleted drift error = %v, want provider startup blocked", err)
	}
}

func TestReconcileProviderMirrorsRepairsManagedProjectMirrorTrustCap(t *testing.T) {
	project := t.TempDir()
	superHome := filepath.Join(t.TempDir(), ".super-dolphin")
	cases := []struct {
		name      string
		trustLine string
	}{
		{name: "legacy-trust-scope-signed", trustLine: "trust_scope: signed"},
		{name: "legacy-trustscope-verified", trustLine: "trustscope: verified"},
		{name: "legacy-trust-verified", trustLine: "trust: VERIFIED"},
		{name: "legacy-trust-user", trustLine: "trust: user"},
		{name: "legacy-trust-signed", trustLine: "trust: signed"},
		{name: "legacy-trust-trusted", trustLine: "trust: trusted"},
	}
	unchangedProjectName := "already-project-trust"
	personalName := "legacy-personal-trust"
	projectContents := make(map[string]string, len(cases))
	for _, tc := range cases {
		content := "---\nname: " + tc.name + "\n" + tc.trustLine + "\n---\n# project\n"
		projectContents[tc.name] = content
		writeFileWithMode(t, filepath.Join(project, ".agents", "skills", tc.name, skillMainFile), content, 0o644)
	}
	unchangedProjectContent := "---\nname: " + unchangedProjectName + "\ntrust: project\n---\n# project\n"
	personalContent := "---\nname: " + personalName + "\ntrust: signed\n---\n# personal\n"
	writeFileWithMode(t, filepath.Join(project, ".agents", "skills", unchangedProjectName, skillMainFile), unchangedProjectContent, 0o644)
	writeFileWithMode(t, filepath.Join(superHome, "skills", "personal", "user", personalName, skillMainFile), personalContent, 0o644)
	records, err := newCanonicalStore(superHome).scan(project)
	if err != nil {
		t.Fatalf("scan canonical records: %v", err)
	}
	personalRecord := findCanonicalRecord(t, records, personalName, skillScopePersonal, personalSkillTypeUser)
	codexRoot := testCodexProjectMirrorRoot(project)
	providerHome := filepath.Join(superHome, "providers", "codex")
	personalRoot := filepath.Join(providerHome, "skills")
	owner, err := resolveOwnerIdentity(superHome, defaultOwnerOSUID(), defaultAppProfile())
	if err != nil {
		t.Fatalf("resolveOwnerIdentity: %v", err)
	}
	projectTarget := SkillMirrorTarget{TargetID: "codex:project:" + RepoFingerprint(project), Provider: SkillProviderCodex, Scope: skillScopeProject, Root: codexRoot, CanonicalRootID: RepoFingerprint(project)}
	personalTarget := SkillMirrorTarget{TargetID: "codex:app-managed:" + owner.OwnerKey, Provider: SkillProviderCodex, Scope: skillScopePersonal, Root: personalRoot, CanonicalRootID: owner.OwnerKey}
	projectFixtures := make([]managedMirrorFixture, 0, len(cases)+1)
	for _, tc := range cases {
		projectFixtures = append(projectFixtures, managedMirrorFixture{
			record:  findCanonicalRecord(t, records, tc.name, skillScopeProject, ""),
			content: projectContents[tc.name],
		})
	}
	unchangedProjectRecord := findCanonicalRecord(t, records, unchangedProjectName, skillScopeProject, "")
	projectFixtures = append(projectFixtures, managedMirrorFixture{record: unchangedProjectRecord, content: unchangedProjectContent})
	writeManagedMirrorFixtures(t, projectTarget, projectFixtures...)
	writeManagedMirrorFixtures(t, personalTarget, managedMirrorFixture{record: personalRecord, content: personalContent})

	report, err := PublishSkillMirrors(context.Background(), records, []SkillMirrorTarget{projectTarget, personalTarget})
	if err != nil {
		t.Fatalf("PublishSkillMirrors: %v", err)
	}

	for _, tc := range cases {
		assertPublishedReportItem(t, report.Published, projectTarget.TargetID, SkillProviderCodex, skillScopeProject, tc.name, "project/"+tc.name)
		assertFileContent(t, filepath.Join(codexRoot, tc.name, skillMainFile), "---\nname: "+tc.name+"\ntrust: project\n---\n# project\n")
		assertManagedMirrorManifestHash(t, projectTarget, findCanonicalRecord(t, records, tc.name, skillScopeProject, ""))
	}
	assertSkippedReportItem(t, report.Skipped, projectTarget.TargetID, SkillProviderCodex, skillScopeProject, unchangedProjectName, "project/"+unchangedProjectName)
	assertSkippedReportItem(t, report.Skipped, "codex:app-managed:"+personalTarget.CanonicalRootID, SkillProviderCodex, skillScopePersonal, personalName, "personal/user/"+personalName)
	assertFileContent(t, filepath.Join(codexRoot, unchangedProjectName, skillMainFile), unchangedProjectContent)
	assertFileContent(t, filepath.Join(personalRoot, personalName, skillMainFile), personalContent)
	assertManagedMirrorManifestHash(t, personalTarget, personalRecord)
}

func TestSkillMirrorPublisherRejectsSymlinkAncestorWhenDirectParentMissing(t *testing.T) {
	base := t.TempDir()
	realParent := filepath.Join(base, "real-parent")
	if err := os.MkdirAll(realParent, 0o755); err != nil {
		t.Fatalf("MkdirAll real parent: %v", err)
	}
	linkParent := filepath.Join(base, "link-parent")
	if err := os.Symlink(realParent, linkParent); err != nil {
		skipIfSymlinkPrivilegeNotHeld(t, err)
		t.Fatalf("Symlink mirror ancestor: %v", err)
	}
	root := filepath.Join(linkParent, "missing", ".claude", "skills")

	err := prepareMirrorRoot(root)
	if err == nil || !strings.Contains(strings.ToLower(err.Error()), "symlink") {
		t.Fatalf("prepareMirrorRoot error = %v, want symlink rejection", err)
	}
	if _, err := os.Stat(filepath.Join(realParent, "missing")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("real parent missing dir stat err = %v, want not exist", err)
	}
}

type managedMirrorFixture struct {
	record  canonicalSkillRecord
	content string
}

func publishDeletedWithDriftMirror(t *testing.T, project, superHome, canonicalDir string, target SkillMirrorTarget, name, driftContent string) SkillMirrorReport {
	t.Helper()
	records, err := newCanonicalStore(superHome).scan(project)
	if err != nil {
		t.Fatalf("scan canonical records: %v", err)
	}
	if _, err := PublishSkillMirrors(context.Background(), records, []SkillMirrorTarget{target}); err != nil {
		t.Fatalf("PublishSkillMirrors initial: %v", err)
	}
	if err := os.RemoveAll(canonicalDir); err != nil {
		t.Fatalf("RemoveAll canonical: %v", err)
	}
	if err := os.WriteFile(filepath.Join(target.Root, name, "references", "guide.md"), []byte(driftContent), 0o644); err != nil {
		t.Fatalf("WriteFile deleted drift: %v", err)
	}
	recordsAfterDelete, err := newCanonicalStore(superHome).scan(project)
	if err != nil {
		t.Fatalf("scan canonical records after delete: %v", err)
	}
	report, err := PublishSkillMirrors(context.Background(), recordsAfterDelete, []SkillMirrorTarget{target})
	if err != nil {
		t.Fatalf("PublishSkillMirrors deleted drift: %v", err)
	}
	return report
}

func writeManagedMirrorFixtures(t *testing.T, target SkillMirrorTarget, fixtures ...managedMirrorFixture) {
	t.Helper()
	manifest := newSkillMirrorManifest(target)
	for _, fixture := range fixtures {
		mirrorDir := filepath.Join(target.Root, fixture.record.Name)
		writeFileWithMode(t, filepath.Join(mirrorDir, skillMainFile), fixture.content, 0o644)
		canonicalHash, err := stableMirrorDirectoryHash(fixture.record.Dir)
		if err != nil {
			t.Fatalf("stableMirrorDirectoryHash canonical: %v", err)
		}
		mirrorHash, err := stableMirrorDirectoryHash(mirrorDir)
		if err != nil {
			t.Fatalf("stableMirrorDirectoryHash mirror: %v", err)
		}
		manifest.Skills[fixture.record.Name] = mirrorManifestEntry(fixture.record, canonicalHash, mirrorHash)
	}
	if err := writeSkillMirrorManifest(filepath.Join(target.Root, skillMirrorManifestFile), manifest); err != nil {
		t.Fatalf("writeSkillMirrorManifest: %v", err)
	}
}

func assertManagedMirrorManifestHash(t *testing.T, target SkillMirrorTarget, record canonicalSkillRecord) {
	t.Helper()
	manifest, err := readSkillMirrorManifest(filepath.Join(target.Root, skillMirrorManifestFile))
	if err != nil {
		t.Fatalf("readSkillMirrorManifest: %v", err)
	}
	entry, ok := manifest.Skills[record.Name]
	if !ok {
		t.Fatalf("manifest missing %q in %+v", record.Name, manifest.Skills)
	}
	canonicalHash, err := stableMirrorDirectoryHash(record.Dir)
	if err != nil {
		t.Fatalf("stableMirrorDirectoryHash canonical: %v", err)
	}
	mirrorHash, err := stableMirrorDirectoryHash(filepath.Join(target.Root, record.Name))
	if err != nil {
		t.Fatalf("stableMirrorDirectoryHash mirror: %v", err)
	}
	if entry.CanonicalHash != canonicalHash || entry.MirrorHash != mirrorHash {
		t.Fatalf("manifest hashes for %q = canonical:%q mirror:%q, want canonical:%q mirror:%q", record.Name, entry.CanonicalHash, entry.MirrorHash, canonicalHash, mirrorHash)
	}
}

func writeSkillWithSupportFiles(t *testing.T, dir, name string) {
	t.Helper()
	writeSkillContent(t, dir, name, "# "+name+"\n")
	writeFileWithMode(t, filepath.Join(dir, "references", "guide.md"), "guide\n", 0o755)
	writeFileWithMode(t, filepath.Join(dir, "templates", "prompt.md"), "prompt\n", 0o755)
	writeFileWithMode(t, filepath.Join(dir, "scripts", "run.sh"), "#!/bin/sh\n", 0o755)
	writeFileWithMode(t, filepath.Join(dir, "scripts", "data.txt"), "data\n", 0o755)
}

func writeFileWithMode(t *testing.T, path, content string, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll(%s): %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(content), mode); err != nil {
		t.Fatalf("WriteFile(%s): %v", path, err)
	}
}

func assertMirrorFile(t *testing.T, path string, wantExecutable bool) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat(%s): %v", path, err)
	}
	if !info.Mode().IsRegular() {
		t.Fatalf("%s mode = %v, want regular file", path, info.Mode())
	}
	if runtime.GOOS == "windows" {
		return
	}
	gotExecutable := info.Mode().Perm()&0o111 != 0
	if gotExecutable != wantExecutable {
		t.Fatalf("%s executable = %v, want %v (mode %v)", path, gotExecutable, wantExecutable, info.Mode().Perm())
	}
}

func assertFileContent(t *testing.T, path, want string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile(%s): %v", path, err)
	}
	if string(data) != want {
		t.Fatalf("%s content = %q, want %q", path, string(data), want)
	}
}

func assertPublishedReportItem(t *testing.T, items []SkillMirrorReportItem, targetID string, provider SkillProvider, scope, rel, canonicalID string) {
	t.Helper()
	assertReportItem(t, items, targetID, provider, scope, rel, canonicalID, "")
}

func assertDeletedReportItem(t *testing.T, items []SkillMirrorReportItem, targetID string, provider SkillProvider, scope, rel, canonicalID string) {
	t.Helper()
	assertReportItem(t, items, targetID, provider, scope, rel, canonicalID, "")
}

func assertSkippedReportItem(t *testing.T, items []SkillMirrorReportItem, targetID string, provider SkillProvider, scope, rel, canonicalID string) {
	t.Helper()
	assertReportItem(t, items, targetID, provider, scope, rel, canonicalID, "")
}

func assertConflictReportItem(t *testing.T, items []SkillMirrorReportItem, targetID string, provider SkillProvider, scope, rel, canonicalID, kind string) {
	t.Helper()
	assertReportItem(t, items, targetID, provider, scope, rel, canonicalID, kind)
}

func findConflictReportItem(t *testing.T, items []SkillMirrorReportItem, targetID string, provider SkillProvider, scope, rel, canonicalID, kind string) SkillMirrorReportItem {
	t.Helper()
	for _, item := range items {
		if sameReportLocation(item, targetID, provider, scope, rel, canonicalID) && item.ConflictKind == kind {
			return item
		}
	}
	t.Fatalf("missing conflict item target=%q provider=%q scope=%q rel=%q canonical=%q kind=%q in %+v", targetID, provider, scope, rel, canonicalID, kind, items)
	return SkillMirrorReportItem{}
}

func findReportItem(t *testing.T, items []SkillMirrorReportItem, targetID string, provider SkillProvider, scope, rel, canonicalID string) SkillMirrorReportItem {
	t.Helper()
	for _, item := range items {
		if sameReportLocation(item, targetID, provider, scope, rel, canonicalID) {
			return item
		}
	}
	t.Fatalf("missing report item target=%q provider=%q scope=%q rel=%q canonical=%q in %+v", targetID, provider, scope, rel, canonicalID, items)
	return SkillMirrorReportItem{}
}

func mustMirrorPublishReport(t *testing.T, result map[string]any) SkillMirrorReport {
	t.Helper()
	report, ok := result["mirror_publish"].(SkillMirrorReport)
	if !ok {
		t.Fatalf("mirror_publish = %#v, want SkillMirrorReport", result["mirror_publish"])
	}
	return report
}

func assertReportItem(t *testing.T, items []SkillMirrorReportItem, targetID string, provider SkillProvider, scope, rel, canonicalID, kind string) {
	t.Helper()
	for _, item := range items {
		if !sameReportLocation(item, targetID, provider, scope, rel, canonicalID) {
			continue
		}
		if kind != "" && item.ConflictKind != kind {
			t.Fatalf("report item = %+v, want conflict kind %q", item, kind)
		}
		if kind == "" && emptyReportHashes(item) {
			t.Fatalf("report item = %+v, want old or new hash", item)
		}
		return
	}
	t.Fatalf("missing report item target=%q provider=%q scope=%q rel=%q canonical=%q kind=%q in %+v", targetID, provider, scope, rel, canonicalID, kind, items)
}

func sameReportLocation(item SkillMirrorReportItem, targetID string, provider SkillProvider, scope, rel, canonicalID string) bool {
	return item.TargetID == targetID &&
		item.Provider == provider &&
		item.Scope == scope &&
		item.RelativeMirrorPath == rel &&
		item.CanonicalID == canonicalID
}

func emptyReportHashes(item SkillMirrorReportItem) bool {
	return item.NewHash == "" && item.OldHash == ""
}

func TestReconcileProviderMirrorsRejectsPersonalSystemHome(t *testing.T) {
	project := t.TempDir()
	superHome := filepath.Join(t.TempDir(), ".super-dolphin")
	writeSkillWithSupportFiles(t, filepath.Join(superHome, "skills", "personal", "user", "notes"), "notes")
	svc := &service{projectRoot: project, projectSkillsRoot: defaultProjectSkillsRoot(project), superDolphinHome: superHome}
	systemHome := filepath.Join(t.TempDir(), ".claude")

	_, err := svc.ReconcileProviderMirrors(context.Background(), project, []contract.SkillProviderMirrorTarget{
		{Provider: "claude", HomeRoot: systemHome, SkillsRoot: filepath.Join(systemHome, "skills")},
	})
	if err == nil || !strings.Contains(err.Error(), "not allowed") {
		t.Fatalf("ReconcileProviderMirrors error = %v, want untrusted personal home rejection", err)
	}
	assertMissing(t, filepath.Join(systemHome, "skills", "notes", skillMainFile))
}

func TestSkillModuleExposesMirrorReconcilerThroughFx(t *testing.T) {
	var reconciler contract.SkillMirrorReconciler
	app := fx.New(
		fx.NopLogger,
		fx.Provide(func() *contract.Config { return &contract.Config{ProjectRoot: t.TempDir()} }),
		fx.Provide(func() *event.Dispatcher { return event.NewDispatcher() }),
		fx.Provide(func() auditstore.Store { return skillModuleAuditLogStoreStub{} }),
		Module,
		fx.Populate(&reconciler),
	)
	ctx := context.Background()
	if err := app.Start(ctx); err != nil {
		t.Fatalf("fx start: %v", err)
	}
	defer func() {
		if err := app.Stop(ctx); err != nil {
			t.Fatalf("fx stop: %v", err)
		}
	}()
	if reconciler == nil {
		t.Fatalf("contract.SkillMirrorReconciler was not populated")
	}
}

type skillModuleAuditLogStoreStub struct{}

func (skillModuleAuditLogStoreStub) List(context.Context, auditstore.ListFilter) ([]auditstore.AuditEvent, error) {
	return nil, nil
}

func (skillModuleAuditLogStoreStub) Insert(context.Context, auditstore.InsertParams) error {
	return nil
}
