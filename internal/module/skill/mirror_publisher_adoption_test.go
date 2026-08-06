package skill

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
)

func TestSkillMirrorPublisherAdoptsIdenticalUnmanagedPersonalMirror(t *testing.T) {
	project := t.TempDir()
	superHome := filepath.Join(t.TempDir(), ".super-dolphin")
	writeSkillWithSupportFiles(t, filepath.Join(superHome, "skills", "personal", "user", "notes"), "notes")
	records, err := newCanonicalStore(superHome).scan(project)
	if err != nil {
		t.Fatalf("scan canonical records: %v", err)
	}
	record := findCanonicalRecord(t, records, "notes", skillScopePersonal, personalSkillTypeUser)
	root := filepath.Join(t.TempDir(), ".agents", "skills")
	if err := copyCanonicalSkillDir(record.Dir, filepath.Join(root, "notes"), skillScopePersonal); err != nil {
		t.Fatalf("copy unmanaged personal mirror: %v", err)
	}
	target := SkillMirrorTarget{TargetID: "codex:user-global:owner", Provider: SkillProviderCodex, Scope: skillScopePersonal, Root: root, CanonicalRootID: "sd_owner:owner"}

	report, err := PublishSkillMirrors(NewMirrorRootLockRegistry(), context.Background(), records, []SkillMirrorTarget{target})
	if err != nil {
		t.Fatalf("PublishSkillMirrors: %v", err)
	}

	if len(report.Conflicts) > 0 {
		t.Fatalf("conflicts = %+v, want none for identical personal mirror takeover", report.Conflicts)
	}
	assertSkippedReportItem(t, report.Skipped, "codex:user-global:owner", SkillProviderCodex, skillScopePersonal, "notes", "personal/user/notes")
	manifest, err := readSkillMirrorManifest(filepath.Join(root, skillMirrorManifestFile))
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	entry := manifest.Skills["notes"]
	if !entry.Owned || entry.CanonicalID != "personal/user/notes" || entry.CanonicalHash == "" || entry.MirrorHash == "" {
		t.Fatalf("manifest entry = %+v, want adopted owned personal mirror", entry)
	}
}

func TestSkillMirrorPublisherAdoptsIdenticalUnmanagedProjectMirror(t *testing.T) {
	project := t.TempDir()
	writeSkillWithSupportFiles(t, filepath.Join(project, ".agents", "skills", "build"), "build")
	records, err := newCanonicalStore("").scan(project)
	if err != nil {
		t.Fatalf("scan canonical records: %v", err)
	}
	record := findCanonicalRecord(t, records, "build", skillScopeProject, "")
	root := testCodexProjectMirrorRoot(project)
	if err := copyCanonicalSkillDir(record.Dir, filepath.Join(root, "build"), skillScopeProject); err != nil {
		t.Fatalf("copy unmanaged project mirror: %v", err)
	}

	report, err := PublishSkillMirrors(NewMirrorRootLockRegistry(), context.Background(), records, []SkillMirrorTarget{
		{TargetID: "codex:project:repo", Provider: SkillProviderCodex, Scope: skillScopeProject, Root: root, CanonicalRootID: "repo"},
	})
	if err != nil {
		t.Fatalf("PublishSkillMirrors: %v", err)
	}

	if len(report.Conflicts) > 0 {
		t.Fatalf("conflicts = %+v, want none for identical project mirror takeover", report.Conflicts)
	}
	assertSkippedReportItem(t, report.Skipped, "codex:project:repo", SkillProviderCodex, skillScopeProject, "build", "project/build")
	manifest, err := readSkillMirrorManifest(filepath.Join(root, skillMirrorManifestFile))
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	entry := manifest.Skills["build"]
	if !entry.Owned || entry.CanonicalID != "project/build" || entry.CanonicalHash == "" || entry.MirrorHash == "" {
		t.Fatalf("manifest entry = %+v, want adopted owned project mirror", entry)
	}
}

func TestProjectSuppressedPersonalSourceIDsProjectKeepSelectedCoversFuturePersonalTypes(t *testing.T) {
	suppressed, err := projectSuppressedPersonalSourceIDs(projectSkillPolicy{
		Version: 1,
		KeepSelected: []projectSkillKeepSelected{{
			Name:              "same",
			SelectedSourceID:  "project/same",
			ExcludedSourceIDs: []string{"personal/user/same"},
		}},
	})
	if err != nil {
		t.Fatalf("projectSuppressedPersonalSourceIDs: %v", err)
	}
	for _, personalType := range activePersonalSkillTypes() {
		sourceID := canonicalSourceID(canonicalSkillRecord{Name: "same", Scope: skillScopePersonal, PersonalType: personalType})
		if _, ok := suppressed[sourceID]; !ok {
			t.Fatalf("suppressed ids missing %s: %+v", sourceID, suppressed)
		}
	}
}

func TestReconcileProviderMirrorsProjectPolicyFailsClosedBeforeMutatingSharedPersonalMirror(t *testing.T) {
	project := t.TempDir()
	projectB := t.TempDir()
	superHome := filepath.Join(t.TempDir(), ".super-dolphin")
	writeSkillWithSupportFiles(t, filepath.Join(superHome, "skills", "personal", "user", "same"), "same")
	svc := &service{projectRoot: project, projectSkillsRoot: defaultProjectSkillsRoot(project), superDolphinHome: superHome, mirrorLocks: NewMirrorRootLockRegistry()}
	personalHome := filepath.Join(superHome, "providers", "claude")
	personalRoot := filepath.Join(personalHome, "skills")
	records, err := newCanonicalStore(superHome).scan(project)
	if err != nil {
		t.Fatalf("scan canonical records: %v", err)
	}
	owner, err := resolveOwnerIdentity(superHome, defaultOwnerOSUID(), defaultAppProfile())
	if err != nil {
		t.Fatalf("resolveOwnerIdentity: %v", err)
	}
	personalRecord := findCanonicalRecord(t, records, "same", skillScopePersonal, personalSkillTypeUser)
	writeManagedMirrorFixtures(t, SkillMirrorTarget{
		TargetID:        "claude:user-global:" + owner.OwnerKey,
		Provider:        SkillProviderClaude,
		Scope:           skillScopePersonal,
		Root:            personalRoot,
		CanonicalRootID: owner.OwnerKey,
	}, managedMirrorFixture{record: personalRecord, content: "---\nname: same\n---\n# same\n"})
	assertMirrorFile(t, filepath.Join(personalRoot, "same", skillMainFile), false)
	if _, err := writeProjectDisablePersonalPolicy(project, "same", personalSkillTypeUser); err != nil {
		t.Fatalf("writeProjectDisablePersonalPolicy: %v", err)
	}

	_, err = svc.ReconcileProviderMirrors(context.Background(), project, []contract.SkillProviderMirrorTarget{
		{Provider: "claude", HomeRoot: personalHome, SkillsRoot: personalRoot},
	})
	if err == nil {
		t.Fatal("ReconcileProviderMirrors with project suppression succeeded, want fail-fast")
	}
	if !errors.Is(err, errProjectPolicyCannotMutateSharedPersonalMirror) {
		t.Fatalf("ReconcileProviderMirrors error = %v, want shared personal mirror policy error", err)
	}
	assertMirrorFile(t, filepath.Join(personalRoot, "same", skillMainFile), false)

	svcB := &service{projectRoot: projectB, projectSkillsRoot: defaultProjectSkillsRoot(projectB), superDolphinHome: superHome, mirrorLocks: NewMirrorRootLockRegistry()}
	if _, err := svcB.ReconcileProviderMirrors(context.Background(), projectB, []contract.SkillProviderMirrorTarget{
		{Provider: "claude", HomeRoot: personalHome, SkillsRoot: personalRoot},
	}); err != nil {
		t.Fatalf("project B reconcile after project A rejection: %v", err)
	}
	assertMirrorFile(t, filepath.Join(personalRoot, "same", skillMainFile), false)
}

func TestReconcileProviderMirrorsProjectKeepSelectedFailsClosedBeforeMutatingSharedPersonalMirror(t *testing.T) {
	project := t.TempDir()
	superHome := filepath.Join(t.TempDir(), ".super-dolphin")
	writeSkillWithSupportFiles(t, filepath.Join(superHome, "skills", "personal", "user", "same"), "same")
	svc := &service{projectRoot: project, projectSkillsRoot: defaultProjectSkillsRoot(project), superDolphinHome: superHome, mirrorLocks: NewMirrorRootLockRegistry()}
	personalHome := filepath.Join(superHome, "providers", "claude")
	personalRoot := filepath.Join(personalHome, "skills")
	records, err := newCanonicalStore(superHome).scan(project)
	if err != nil {
		t.Fatalf("scan canonical records: %v", err)
	}
	writeProjectSkillPolicy(t, project, projectKeepSelectedPolicy("same", "project/same", "personal/user/same"))
	owner, err := resolveOwnerIdentity(superHome, defaultOwnerOSUID(), defaultAppProfile())
	if err != nil {
		t.Fatalf("resolveOwnerIdentity: %v", err)
	}
	personalRecord := findCanonicalRecord(t, records, "same", skillScopePersonal, personalSkillTypeUser)
	writeManagedMirrorFixtures(t, SkillMirrorTarget{
		TargetID:        "claude:user-global:" + owner.OwnerKey,
		Provider:        SkillProviderClaude,
		Scope:           skillScopePersonal,
		Root:            personalRoot,
		CanonicalRootID: owner.OwnerKey,
	}, managedMirrorFixture{record: personalRecord, content: "---\nname: same\n---\n# same\n"})

	_, err = svc.ReconcileProviderMirrors(context.Background(), project, []contract.SkillProviderMirrorTarget{
		{Provider: "claude", HomeRoot: personalHome, SkillsRoot: personalRoot},
	})
	if err == nil {
		t.Fatal("ReconcileProviderMirrors with project keep-selected suppression succeeded, want fail-fast")
	}
	if !errors.Is(err, errProjectPolicyCannotMutateSharedPersonalMirror) {
		t.Fatalf("ReconcileProviderMirrors error = %v, want shared personal mirror policy error", err)
	}
	assertMirrorFile(t, filepath.Join(personalRoot, "same", skillMainFile), false)
}

func TestPrepareWriteTimeMirrorPublishFailsClosedBeforeMutatingSharedPersonalMirror(t *testing.T) {
	project := t.TempDir()
	superHome := filepath.Join(t.TempDir(), ".super-dolphin")
	writeSkillWithSupportFiles(t, filepath.Join(superHome, "skills", "personal", "user", "same"), "same")
	svc := &service{projectRoot: project, projectSkillsRoot: defaultProjectSkillsRoot(project), superDolphinHome: superHome, mirrorLocks: NewMirrorRootLockRegistry()}
	personalHome := filepath.Join(superHome, "providers", "claude")
	personalRoot := filepath.Join(personalHome, "skills")
	records, err := newCanonicalStore(superHome).scan(project)
	if err != nil {
		t.Fatalf("scan canonical records: %v", err)
	}
	owner, err := resolveOwnerIdentity(superHome, defaultOwnerOSUID(), defaultAppProfile())
	if err != nil {
		t.Fatalf("resolveOwnerIdentity: %v", err)
	}
	personalRecord := findCanonicalRecord(t, records, "same", skillScopePersonal, personalSkillTypeUser)
	target := SkillMirrorTarget{
		TargetID:        "claude:app-managed:" + owner.OwnerKey,
		Provider:        SkillProviderClaude,
		Scope:           skillScopePersonal,
		Root:            personalRoot,
		CanonicalRootID: owner.OwnerKey,
	}
	writeManagedMirrorFixtures(t, target, managedMirrorFixture{record: personalRecord, content: "---\nname: same\n---\n# same\n"})
	if _, err := writeProjectDisablePersonalPolicy(project, "same", personalSkillTypeUser); err != nil {
		t.Fatalf("write project policy: %v", err)
	}

	report, ok := svc.prepareWriteTimeMirrorPublish(project, []SkillMirrorTarget{target}, newCanonicalStore(superHome), skillScopePersonal, personalSkillTypeUser, "same")
	if ok {
		t.Fatal("prepareWriteTimeMirrorPublish succeeded, want fail-fast")
	}
	if len(report.Conflicts) == 0 {
		t.Fatalf("write-time report = %+v, want blocking conflict", report)
	}
	assertMirrorFile(t, filepath.Join(personalRoot, "same", skillMainFile), false)
}
