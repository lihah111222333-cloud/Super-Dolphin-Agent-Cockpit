package skill

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	auditstore "github.com/anthropic-ai/super-agent-v3/internal/store/auditlog"
	"github.com/kelindar/event"
	"go.uber.org/fx"
)

func TestProjectMirrorCapsTrustFrontmatter(t *testing.T) {
	project := t.TempDir()
	superHome := filepath.Join(t.TempDir(), ".super-dolphin")
	cases := map[string]string{"trust-scope-signed": "trust_scope: signed", "trustscope-verified": "trustscope: verified", "trust-verified": "trust: VERIFIED", "trust-user": "trust: user", "trust-signed": "trust: signed", "trust-trusted": "trust: trusted"}
	for name, trustLine := range cases {
		skillDir := filepath.Join(project, ".agent", "skills", name)
		content := "---\nname: " + name + "\n" + trustLine + "\n---\n# " + name + "\n"
		writeFileWithMode(t, filepath.Join(skillDir, skillMainFile), content, 0o644)
	}
	writeFileWithMode(t, filepath.Join(superHome, "skills", "personal", "user", "personal-elevated", skillMainFile), "---\nname: personal-elevated\ntrust: signed\n---\n# personal\n", 0o644)
	svc := &service{projectRoot: project, projectSkillsRoot: defaultProjectSkillsRoot(project), superDolphinHome: superHome}
	codexRoot := filepath.Join(project, ".codex", "skills")
	providerHome := filepath.Join(superHome, "providers", "codex")
	personalRoot := filepath.Join(superHome, "providers", "codex", "skills")

	if _, err := svc.ReconcileProviderMirrors(context.Background(), project, []contract.SkillProviderMirrorTarget{{
		Provider: "codex", HomeRoot: providerHome, SkillsRoot: codexRoot,
	}, {
		Provider: "codex", HomeRoot: providerHome, SkillsRoot: personalRoot,
	}}); err != nil {
		t.Fatalf("ReconcileProviderMirrors: %v", err)
	}
	for name := range cases {
		gotPath := filepath.Join(codexRoot, name, skillMainFile)
		assertFileContent(t, gotPath, "---\nname: "+name+"\ntrust: project\n---\n# "+name+"\n")
	}
	assertFileContent(t, filepath.Join(personalRoot, "personal-elevated", skillMainFile), "---\nname: personal-elevated\ntrust: signed\n---\n# personal\n")
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
		writeFileWithMode(t, filepath.Join(project, ".agent", "skills", tc.name, skillMainFile), content, 0o644)
	}
	unchangedProjectContent := "---\nname: " + unchangedProjectName + "\ntrust: project\n---\n# project\n"
	personalContent := "---\nname: " + personalName + "\ntrust: signed\n---\n# personal\n"
	writeFileWithMode(t, filepath.Join(project, ".agent", "skills", unchangedProjectName, skillMainFile), unchangedProjectContent, 0o644)
	writeFileWithMode(t, filepath.Join(superHome, "skills", "personal", "user", personalName, skillMainFile), personalContent, 0o644)
	records, err := newCanonicalStore(superHome).scan(project)
	if err != nil {
		t.Fatalf("scan canonical records: %v", err)
	}
	personalRecord := findCanonicalRecord(t, records, personalName, skillScopePersonal, personalSkillTypeUser)
	svc := &service{projectRoot: project, projectSkillsRoot: defaultProjectSkillsRoot(project), superDolphinHome: superHome}
	providerHome := filepath.Join(superHome, "providers", "codex")
	codexRoot := filepath.Join(project, ".codex", "skills")
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

	report, err := svc.ReconcileProviderMirrors(context.Background(), project, []contract.SkillProviderMirrorTarget{{
		Provider: "codex", HomeRoot: providerHome, SkillsRoot: codexRoot,
	}, {
		Provider: "codex", HomeRoot: providerHome, SkillsRoot: personalRoot,
	}})
	if err != nil {
		t.Fatalf("ReconcileProviderMirrors: %v", err)
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

type managedMirrorFixture struct {
	record  canonicalSkillRecord
	content string
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

func TestReconcileProviderMirrorsRejectsPersonalSystemHome(t *testing.T) {
	project := t.TempDir()
	superHome := filepath.Join(t.TempDir(), ".super-dolphin")
	writeSkillWithSupportFiles(t, filepath.Join(superHome, "skills", "personal", "user", "notes"), "notes")
	svc := &service{projectRoot: project, projectSkillsRoot: defaultProjectSkillsRoot(project), superDolphinHome: superHome}
	systemHome := filepath.Join(t.TempDir(), ".claude")

	_, err := svc.ReconcileProviderMirrors(context.Background(), project, []contract.SkillProviderMirrorTarget{
		{Provider: "claude", HomeRoot: systemHome, SkillsRoot: filepath.Join(systemHome, "skills")},
	})
	if err == nil || !strings.Contains(err.Error(), "app-managed") {
		t.Fatalf("ReconcileProviderMirrors error = %v, want app-managed rejection", err)
	}
	assertMissing(t, filepath.Join(systemHome, "skills", "notes", skillMainFile))
}

func TestSkillModuleExposesMirrorReconcilerThroughFx(t *testing.T) {
	var reconciler contract.SkillMirrorReconciler
	app := fx.New(
		fx.NopLogger,
		fx.Provide(func() *contract.Config { return &contract.Config{ProjectRoot: t.TempDir()} }),
		fx.Provide(func() *event.Dispatcher { return event.NewDispatcher() }),
		fx.Provide(func() auditstore.Store { return &capturingSkillAuditStore{} }),
		fx.Provide(func() contract.ApprovalRequester { return &stubApprovalRequester{} }),
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
