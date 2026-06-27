package skill

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestPersonalCanonicalExplicitReadAndWriteUsesPersonalType(t *testing.T) {
	setSkillTestUserHome(t)
	project := t.TempDir()
	home := t.TempDir()
	superDolphinHome := filepath.Join(home, ".super-dolphin")
	svc := &service{
		projectRoot:       project,
		projectSkillsRoot: defaultProjectSkillsRoot(project),
		superDolphinHome:  superDolphinHome,
		auditStore:        &capturingSkillAuditStore{},
	}

	_, err := svc.WriteLocal(skillTestContext(project), "notes", "---\nname: notes\n---\npersonal body", skillScopePersonal, personalSkillTypeUser)
	if err != nil {
		t.Fatalf("WriteLocal(personal/user): %v", err)
	}
	personalSkillFile := filepath.Join(superDolphinHome, "skills", "personal", "user", "notes", skillMainFile)
	assertExists(t, personalSkillFile)

	result, err := svc.ReadLocal(skillTestContext(project), personalSkillFile)
	if err != nil {
		t.Fatalf("ReadLocal(personal absolute path): %v", err)
	}
	payload := result.(map[string]any)["skill"].(map[string]any)
	if got := payload["content"].(string); got != "---\nname: notes\n---\npersonal body" {
		t.Fatalf("ReadLocal content = %q", got)
	}
}

func TestWriteLocalPersonalWriteTimeMirrorRespectsProjectDisablePersonalPolicy(t *testing.T) {
	setSkillTestUserHome(t)
	project := t.TempDir()
	superDolphinHome := filepath.Join(t.TempDir(), ".super-dolphin")
	writeCanonicalSkill(t, filepath.Join(superDolphinHome, "skills", "personal", personalSkillTypeUser, "alpha"), "alpha")
	writeCanonicalSkill(t, filepath.Join(superDolphinHome, "skills", "personal", personalSkillTypeUser, "beta"), "beta")
	svc := &service{
		projectRoot:       project,
		projectSkillsRoot: defaultProjectSkillsRoot(project),
		superDolphinHome:  superDolphinHome,
		auditStore:        &capturingSkillAuditStore{},
	}

	initial := svc.publishWriteTimeMirrorsForScope(context.Background(), project, skillScopePersonal, personalSkillTypeUser, "alpha")
	if len(initial.Conflicts) > 0 {
		t.Fatalf("initial personal mirror publish conflicts = %+v", initial.Conflicts)
	}
	for _, provider := range []SkillProvider{SkillProviderClaude, SkillProviderCodex} {
		assertFileContent(t, filepath.Join(providerPersonalMirrorRoot(provider), "alpha", skillMainFile), "---\nname: alpha\n---\n# alpha\n")
		assertFileContent(t, filepath.Join(providerPersonalMirrorRoot(provider), "beta", skillMainFile), "---\nname: beta\n---\n# beta\n")
		assertPersonalMirrorManifestEntry(t, provider, "alpha", "personal/user/alpha")
	}
	writeProjectSkillPolicy(t, project, projectSkillPolicy{
		Version: 1,
		DisablePersonalForProject: []projectSkillPolicyDisabledPersonal{{
			Name:         "alpha",
			PersonalType: personalSkillTypeUser,
		}},
	})

	_, err := svc.WriteLocal(skillTestContext(project), "beta", "---\nname: beta\n---\n# beta updated\n", skillScopePersonal, personalSkillTypeUser)
	if err != nil {
		t.Fatalf("WriteLocal(personal beta): %v", err)
	}

	for _, provider := range []SkillProvider{SkillProviderClaude, SkillProviderCodex} {
		assertMissing(t, filepath.Join(providerPersonalMirrorRoot(provider), "alpha", skillMainFile))
		assertFileContent(t, filepath.Join(providerPersonalMirrorRoot(provider), "beta", skillMainFile), "---\nname: beta\n---\n# beta updated\n")
		assertPersonalMirrorManifestMissing(t, provider, "alpha")
		assertPersonalMirrorManifestEntry(t, provider, "beta", "personal/user/beta")
	}
}

func TestReadLocalNameSameNameConflictFailsClosed(t *testing.T) {
	project := t.TempDir()
	superDolphinHome := filepath.Join(t.TempDir(), ".super-dolphin")
	writeCanonicalSkill(t, filepath.Join(project, ".agent", "skills", "build"), "build")
	writeCanonicalSkill(t, filepath.Join(superDolphinHome, "skills", "personal", "user", "build"), "build")
	svc := &service{projectRoot: project, projectSkillsRoot: defaultProjectSkillsRoot(project), superDolphinHome: superDolphinHome}

	_, err := svc.ReadLocal(skillTestContext(project), "build")
	if !errors.Is(err, ErrSkillSameNameConflict) {
		t.Fatalf("ReadLocal same-name error = %v, want ErrSkillSameNameConflict", err)
	}
}

func TestReadLocalNameUsesResolvedProjectPolicy(t *testing.T) {
	project := t.TempDir()
	superDolphinHome := filepath.Join(t.TempDir(), ".super-dolphin")
	writeSkillContent(t, filepath.Join(project, ".agent", "skills", "build"), "build", "project body")
	writeSkillContent(t, filepath.Join(superDolphinHome, "skills", "personal", "user", "build"), "build", "personal body")
	writeProjectSkillPolicy(t, project, projectSkillPolicy{
		Version: 1,
		DisablePersonalForProject: []projectSkillPolicyDisabledPersonal{{
			Name:         "build",
			PersonalType: personalSkillTypeUser,
		}},
	})
	svc := &service{projectRoot: project, projectSkillsRoot: defaultProjectSkillsRoot(project), superDolphinHome: superDolphinHome}

	result, err := svc.ReadLocal(skillTestContext(project), "build")
	if err != nil {
		t.Fatalf("ReadLocal resolved by policy: %v", err)
	}
	payload := result.(map[string]any)["skill"].(map[string]any)
	if got := payload["content"].(string); !strings.Contains(got, "project body") || strings.Contains(got, "personal body") {
		t.Fatalf("ReadLocal content = %q, want project body only", got)
	}
}

func TestReadLocalAbsolutePathSameNameConflictFailsClosed(t *testing.T) {
	project := t.TempDir()
	superDolphinHome := filepath.Join(t.TempDir(), ".super-dolphin")
	writeCanonicalSkill(t, filepath.Join(project, ".agent", "skills", "build"), "build")
	personalDir := filepath.Join(superDolphinHome, "skills", "personal", "user", "build")
	writeCanonicalSkill(t, personalDir, "build")
	svc := &service{projectRoot: project, projectSkillsRoot: defaultProjectSkillsRoot(project), superDolphinHome: superDolphinHome}

	_, err := svc.ReadLocal(skillTestContext(project), filepath.Join(personalDir, skillMainFile))
	if !errors.Is(err, ErrSkillSameNameConflict) {
		t.Fatalf("ReadLocal absolute conflict error = %v, want ErrSkillSameNameConflict", err)
	}
}

func TestListLocalFilesAbsolutePathSameNameConflictFailsClosed(t *testing.T) {
	project := t.TempDir()
	superDolphinHome := filepath.Join(t.TempDir(), ".super-dolphin")
	writeCanonicalSkill(t, filepath.Join(project, ".agent", "skills", "build"), "build")
	personalDir := filepath.Join(superDolphinHome, "skills", "personal", "user", "build")
	writeCanonicalSkill(t, personalDir, "build")
	svc := &service{projectRoot: project, projectSkillsRoot: defaultProjectSkillsRoot(project), superDolphinHome: superDolphinHome}

	_, err := svc.ListLocalFiles(skillTestContext(project), listSkillFilesParams{Dir: personalDir})
	if !errors.Is(err, ErrSkillSameNameConflict) {
		t.Fatalf("ListLocalFiles absolute conflict error = %v, want ErrSkillSameNameConflict", err)
	}
}

func TestReadLocalAbsolutePathExcludedByProjectPolicyFailsClosed(t *testing.T) {
	project := t.TempDir()
	superDolphinHome := filepath.Join(t.TempDir(), ".super-dolphin")
	writeSkillContent(t, filepath.Join(project, ".agent", "skills", "build"), "build", "project body")
	personalDir := filepath.Join(superDolphinHome, "skills", "personal", "user", "build")
	writeSkillContent(t, personalDir, "build", "personal body")
	writeProjectSkillPolicy(t, project, projectSkillPolicy{
		Version: 1,
		DisablePersonalForProject: []projectSkillPolicyDisabledPersonal{{
			Name:         "build",
			PersonalType: personalSkillTypeUser,
		}},
	})
	svc := &service{projectRoot: project, projectSkillsRoot: defaultProjectSkillsRoot(project), superDolphinHome: superDolphinHome}

	_, err := svc.ReadLocal(skillTestContext(project), filepath.Join(personalDir, skillMainFile))
	if err == nil || !strings.Contains(err.Error(), "not in effective skill set") {
		t.Fatalf("ReadLocal disabled personal path error = %v, want effective-set rejection", err)
	}
}

func TestWriteLocalPersonalAuditIntentFailureLeavesCanonicalUntouched(t *testing.T) {
	setSkillTestUserHome(t)
	project := t.TempDir()
	superDolphinHome := filepath.Join(t.TempDir(), ".super-dolphin")
	audit := &capturingSkillAuditStore{insertErr: errors.New("audit down")}
	svc := &service{projectRoot: project, projectSkillsRoot: defaultProjectSkillsRoot(project), superDolphinHome: superDolphinHome, auditStore: audit}

	_, err := svc.WriteLocal(skillTestContext(project), "notes", "---\nname: notes\n---\nbody", skillScopePersonal, personalSkillTypeUser)
	if err == nil {
		t.Fatalf("WriteLocal(personal) error = nil, want audit failure")
	}
	assertMissing(t, filepath.Join(superDolphinHome, "skills", "personal", "user", "notes", skillMainFile))
}

func TestWriteLocalRejectsPersonalAbsolutePathWithoutPersonalTarget(t *testing.T) {
	project := t.TempDir()
	superDolphinHome := filepath.Join(t.TempDir(), ".super-dolphin")
	personalDir := filepath.Join(superDolphinHome, "skills", "personal", "user", "notes")
	writeSkillContent(t, personalDir, "notes", "before")
	audit := &capturingSkillAuditStore{}
	svc := &service{projectRoot: project, projectSkillsRoot: defaultProjectSkillsRoot(project), superDolphinHome: superDolphinHome, auditStore: audit}

	_, err := svc.WriteLocal(skillTestContext(project), filepath.Join(personalDir, skillMainFile), "---\nname: notes\n---\nafter")
	if err == nil || !strings.Contains(err.Error(), "does not match requested scope") {
		t.Fatalf("WriteLocal personal absolute path error = %v, want scope mismatch", err)
	}
	data, readErr := os.ReadFile(filepath.Join(personalDir, skillMainFile))
	if readErr != nil {
		t.Fatalf("ReadFile personal skill: %v", readErr)
	}
	if strings.Contains(string(data), "after") {
		t.Fatalf("personal canonical changed without explicit personal target: %q", data)
	}
	if len(audit.inserts) != 0 {
		t.Fatalf("audit inserts = %+v, want none before target validation", audit.inserts)
	}
}

func TestWriteLocalPersonalWritesRecoveryAndAudit(t *testing.T) {
	setSkillTestUserHome(t)
	project := t.TempDir()
	superDolphinHome := filepath.Join(t.TempDir(), ".super-dolphin")
	audit := &capturingSkillAuditStore{}
	svc := &service{projectRoot: project, projectSkillsRoot: defaultProjectSkillsRoot(project), superDolphinHome: superDolphinHome, auditStore: audit}

	_, err := svc.WriteLocal(skillTestContext(project), "notes", "---\nname: notes\n---\nbody", skillScopePersonal, personalSkillTypeUser)
	if err != nil {
		t.Fatalf("WriteLocal(personal): %v", err)
	}
	targetDir := filepath.Join(superDolphinHome, "skills", "personal", "user", "notes")
	assertExists(t, filepath.Join(targetDir, skillMainFile))
	assertExists(t, filepath.Join(targetDir, personalSkillRecoveryRecordFile))
	assertSkillMutationAuditActions(t, audit.inserts, "personal_write_intent", "personal_write_finalize")
}

func TestWriteLocalPersonalFinalizeFailureRollsBackNewCanonical(t *testing.T) {
	setSkillTestUserHome(t)
	project := t.TempDir()
	superDolphinHome := filepath.Join(t.TempDir(), ".super-dolphin")
	audit := &capturingSkillAuditStore{failOnAction: "personal_write_finalize", insertErr: errors.New("audit finalize down")}
	svc := &service{projectRoot: project, projectSkillsRoot: defaultProjectSkillsRoot(project), superDolphinHome: superDolphinHome, auditStore: audit}

	_, err := svc.WriteLocal(skillTestContext(project), "notes", "---\nname: notes\n---\nbody", skillScopePersonal, personalSkillTypeUser)

	if err == nil || !strings.Contains(err.Error(), "audit finalize down") {
		t.Fatalf("WriteLocal(personal) error = %v, want finalize audit failure", err)
	}
	assertMissing(t, filepath.Join(superDolphinHome, "skills", "personal", "user", "notes", skillMainFile))
}

func TestWriteLocalPersonalFinalizeFailureRestoresExistingCanonical(t *testing.T) {
	setSkillTestUserHome(t)
	project := t.TempDir()
	superDolphinHome := filepath.Join(t.TempDir(), ".super-dolphin")
	targetDir := filepath.Join(superDolphinHome, "skills", "personal", personalSkillTypeUser, "notes")
	writeSkillContent(t, targetDir, "notes", "before")
	audit := &capturingSkillAuditStore{failOnAction: "personal_write_finalize", insertErr: errors.New("audit finalize down")}
	svc := &service{projectRoot: project, projectSkillsRoot: defaultProjectSkillsRoot(project), superDolphinHome: superDolphinHome, auditStore: audit}

	_, err := svc.WriteLocal(skillTestContext(project), "notes", "---\nname: notes\n---\nafter", skillScopePersonal, personalSkillTypeUser)

	if err == nil || !strings.Contains(err.Error(), "audit finalize down") {
		t.Fatalf("WriteLocal(personal overwrite) error = %v, want finalize audit failure", err)
	}
	assertFileContent(t, filepath.Join(targetDir, skillMainFile), "---\nname: notes\n---\nbefore")
}

func TestWriteLocalPersonalWriteFailureRestoresExistingCanonical(t *testing.T) {
	setSkillTestUserHome(t)
	project := t.TempDir()
	superDolphinHome := filepath.Join(t.TempDir(), ".super-dolphin")
	targetDir := filepath.Join(superDolphinHome, "skills", "personal", personalSkillTypeUser, "notes")
	targetFile := filepath.Join(targetDir, skillMainFile)
	writeSkillContent(t, targetDir, "notes", "before")
	if err := os.Chmod(targetFile, 0o400); err != nil {
		t.Fatalf("Chmod target read-only: %v", err)
	}
	defer func() { _ = os.Chmod(targetFile, 0o600) }()
	audit := &capturingSkillAuditStore{}
	svc := &service{projectRoot: project, projectSkillsRoot: defaultProjectSkillsRoot(project), superDolphinHome: superDolphinHome, auditStore: audit}

	_, err := svc.WriteLocal(skillTestContext(project), "notes", "---\nname: notes\n---\nafter", skillScopePersonal, personalSkillTypeUser)

	if err == nil {
		t.Fatalf("WriteLocal(personal overwrite) error = %v, want write failure", err)
	}
	assertFileContent(t, targetFile, "---\nname: notes\n---\nbefore")
}

func TestWriteLocalPersonalOverwriteCreatesRecoverySnapshotParent(t *testing.T) {
	setSkillTestUserHome(t)
	project := t.TempDir()
	superDolphinHome := filepath.Join(t.TempDir(), ".super-dolphin")
	audit := &capturingSkillAuditStore{}
	svc := &service{projectRoot: project, projectSkillsRoot: defaultProjectSkillsRoot(project), superDolphinHome: superDolphinHome, auditStore: audit}
	targetDir := filepath.Join(superDolphinHome, "skills", "personal", personalSkillTypeUser, "执行计划")
	writeSkillContent(t, targetDir, "执行计划", "before body")
	assertMissing(t, filepath.Join(filepath.Dir(targetDir), personalSkillRecoverySnapshotDir))

	_, err := svc.WriteLocal(skillTestContext(project), "执行计划", "---\nname: 执行计划\n---\nafter body", skillScopePersonal, personalSkillTypeUser)
	if err != nil {
		t.Fatalf("WriteLocal(personal/user overwrite): %v", err)
	}

	assertFileContent(t, filepath.Join(targetDir, skillMainFile), "---\nname: 执行计划\n---\nafter body")
	matches, err := filepath.Glob(filepath.Join(filepath.Dir(targetDir), personalSkillRecoverySnapshotDir, "执行计划-*", skillMainFile))
	if err != nil {
		t.Fatalf("glob recovery snapshot: %v", err)
	}
	if len(matches) != 1 {
		t.Fatalf("recovery snapshot matches = %v, want one backup", matches)
	}
	assertFileContent(t, matches[0], "---\nname: 执行计划\n---\nbefore body")
	assertSkillMutationAuditActions(t, audit.inserts, "personal_write_intent", "personal_write_finalize")
}

func TestWriteLocalRejectsCatalogOnlyHubTarget(t *testing.T) {
	project := t.TempDir()
	superDolphinHome := filepath.Join(t.TempDir(), ".super-dolphin")
	svc := &service{projectRoot: project, projectSkillsRoot: defaultProjectSkillsRoot(project), superDolphinHome: superDolphinHome}

	_, err := svc.WriteLocal(skillTestContext(project), "执行计划", "---\nname: 执行计划\n---\nbody", skillScopePersonal, personalSkillTypeHub)

	if !errors.Is(err, ErrInvalidSkillScope) {
		t.Fatalf("WriteLocal(personal/hub) error = %v, want ErrInvalidSkillScope", err)
	}
	assertMissing(t, filepath.Join(superDolphinHome, "skills", "personal", personalSkillTypeHub, "执行计划", skillMainFile))
}

func TestImportLocalDirPersonalAuditIntentFailureLeavesCanonicalUntouched(t *testing.T) {
	setSkillTestUserHome(t)
	project := t.TempDir()
	superDolphinHome := filepath.Join(t.TempDir(), ".super-dolphin")
	source := filepath.Join(t.TempDir(), "source-skill")
	writeCanonicalSkill(t, source, "imported-note")
	audit := &capturingSkillAuditStore{insertErr: errors.New("audit down")}
	svc := &service{projectRoot: project, projectSkillsRoot: defaultProjectSkillsRoot(project), superDolphinHome: superDolphinHome, auditStore: audit}

	result, err := svc.ImportLocalDir(skillTestContext(project), importSkillDirParams{
		Path:         source,
		Scope:        skillScopePersonal,
		PersonalType: personalSkillTypeImported,
	})
	if err != nil {
		t.Fatalf("ImportLocalDir wrapper error = %v", err)
	}
	failures := result.(map[string]any)["failures"].([]map[string]any)
	if len(failures) != 1 || !strings.Contains(failures[0]["error"].(string), "audit down") {
		t.Fatalf("failures = %+v, want audit failure", failures)
	}
	assertMissing(t, filepath.Join(superDolphinHome, "skills", "personal", "imported", "source-skill", skillMainFile))
}

func TestImportLocalDirPersonalUsesPersonalType(t *testing.T) {
	setSkillTestUserHome(t)
	project := t.TempDir()
	home := t.TempDir()
	superDolphinHome := filepath.Join(home, ".super-dolphin")
	source := filepath.Join(t.TempDir(), "source-skill")
	writeCanonicalSkill(t, source, "imported-note")
	svc := &service{
		projectRoot:       project,
		projectSkillsRoot: defaultProjectSkillsRoot(project),
		superDolphinHome:  superDolphinHome,
		auditStore:        &capturingSkillAuditStore{},
	}

	result, err := svc.ImportLocalDir(skillTestContext(project), importSkillDirParams{
		Path:         source,
		Scope:        skillScopePersonal,
		PersonalType: personalSkillTypeImported,
	})
	if err != nil {
		t.Fatalf("ImportLocalDir(personal/imported): %v", err)
	}
	payload := result.(map[string]any)
	if len(payload["imported"].([]map[string]any)) != 1 {
		t.Fatalf("import result = %+v", result)
	}
	report := mustMirrorPublishReport(t, payload)
	owner, err := resolveOwnerIdentity(superDolphinHome, defaultOwnerOSUID(), defaultAppProfile())
	if err != nil {
		t.Fatalf("resolve owner: %v", err)
	}
	assertPublishedReportItem(t, report.Published, "claude:user-global:"+owner.OwnerKey, SkillProviderClaude, skillScopePersonal, "source-skill", "personal/imported/source-skill")
	assertPublishedReportItem(t, report.Published, "codex:user-global:"+owner.OwnerKey, SkillProviderCodex, skillScopePersonal, "source-skill", "personal/imported/source-skill")
	assertExists(t, filepath.Join(superDolphinHome, "skills", "personal", "imported", "source-skill", skillMainFile))
	assertFileContent(t, filepath.Join(providerPersonalMirrorRoot(SkillProviderClaude), "source-skill", skillMainFile), "---\nname: source-skill\n---\n# imported-note\n")
	assertFileContent(t, filepath.Join(providerPersonalMirrorRoot(SkillProviderCodex), "source-skill", skillMainFile), "---\nname: source-skill\n---\n# imported-note\n")
}

func TestImportLocalDirPersonalFinalizeFailureRollsBackCanonical(t *testing.T) {
	setSkillTestUserHome(t)
	project := t.TempDir()
	superDolphinHome := filepath.Join(t.TempDir(), ".super-dolphin")
	source := filepath.Join(t.TempDir(), "source-skill")
	writeCanonicalSkill(t, source, "imported-note")
	audit := &capturingSkillAuditStore{failOnAction: "personal_import_finalize", insertErr: errors.New("audit finalize down")}
	svc := &service{projectRoot: project, projectSkillsRoot: defaultProjectSkillsRoot(project), superDolphinHome: superDolphinHome, auditStore: audit}

	result, err := svc.ImportLocalDir(skillTestContext(project), importSkillDirParams{
		Path:         source,
		Scope:        skillScopePersonal,
		PersonalType: personalSkillTypeImported,
	})

	if err != nil {
		t.Fatalf("ImportLocalDir wrapper error = %v", err)
	}
	failures := result.(map[string]any)["failures"].([]map[string]any)
	if len(failures) != 1 || !strings.Contains(failures[0]["error"].(string), "audit finalize down") {
		t.Fatalf("failures = %+v, want finalize audit failure", failures)
	}
	assertMissing(t, filepath.Join(superDolphinHome, "skills", "personal", "imported", "source-skill", skillMainFile))
}

func TestDeletePersonalAuditIntentFailureLeavesCanonicalUntouched(t *testing.T) {
	setSkillTestUserHome(t)
	project := t.TempDir()
	superDolphinHome := filepath.Join(t.TempDir(), ".super-dolphin")
	personalRoot := filepath.Join(superDolphinHome, "skills", "personal", personalSkillTypeUser)
	writeCanonicalSkill(t, filepath.Join(personalRoot, "build"), "build")
	audit := &capturingSkillAuditStore{insertErr: errors.New("audit down")}
	svc := &service{
		projectRoot:       project,
		projectSkillsRoot: defaultProjectSkillsRoot(project),
		superDolphinHome:  superDolphinHome,
		auditStore:        audit,
	}

	_, err := svc.DeleteLocal(skillTestContext(project), DeleteSkillParams{Name: "build", Scope: skillScopePersonal, PersonalType: personalSkillTypeUser})
	if err == nil {
		t.Fatalf("DeleteLocal(personal) error = nil, want audit failure")
	}
	assertExists(t, filepath.Join(personalRoot, "build", skillMainFile))
	assertNoArchiveContainsSkill(t, superDolphinHome, filepath.Join(skillScopePersonal, personalSkillTypeUser, "build"))
}

func TestDeletePersonalWritesAuditAndArchiveRecord(t *testing.T) {
	setSkillTestUserHome(t)
	project := t.TempDir()
	superDolphinHome := filepath.Join(t.TempDir(), ".super-dolphin")
	audit := &capturingSkillAuditStore{}
	svc := &service{
		projectRoot:       project,
		projectSkillsRoot: defaultProjectSkillsRoot(project),
		superDolphinHome:  superDolphinHome,
		auditStore:        audit,
	}
	if _, err := svc.WriteLocal(skillTestContext(project), "build", "---\nname: build\n---\nbody", skillScopePersonal, personalSkillTypeUser); err != nil {
		t.Fatalf("WriteLocal(personal): %v", err)
	}
	audit.inserts = nil

	result, err := svc.DeleteLocal(skillTestContext(project), DeleteSkillParams{Name: "build", Scope: skillScopePersonal, PersonalType: personalSkillTypeUser})
	if err != nil {
		t.Fatalf("DeleteLocal(personal): %v", err)
	}
	archiveDir := result.(map[string]any)["archive_dir"].(string)
	assertExists(t, filepath.Join(archiveDir, skillMainFile))
	assertSkillMutationAuditActions(t, audit.inserts, "personal_delete_intent", "personal_delete_finalize")
	report := mustMirrorPublishReport(t, result.(map[string]any))
	owner, err := resolveOwnerIdentity(superDolphinHome, defaultOwnerOSUID(), defaultAppProfile())
	if err != nil {
		t.Fatalf("resolve owner: %v", err)
	}
	assertPublishedReportItem(t, report.Deleted, "claude:user-global:"+owner.OwnerKey, SkillProviderClaude, skillScopePersonal, "build", "personal/user/build")
	assertPublishedReportItem(t, report.Deleted, "codex:user-global:"+owner.OwnerKey, SkillProviderCodex, skillScopePersonal, "build", "personal/user/build")
	assertMissing(t, filepath.Join(providerPersonalMirrorRoot(SkillProviderClaude), "build", skillMainFile))
	assertMissing(t, filepath.Join(providerPersonalMirrorRoot(SkillProviderCodex), "build", skillMainFile))

	var record personalSkillArchiveRecord
	readJSONFile(t, filepath.Join(archiveDir, personalSkillArchiveRecordFile), &record)
	if record.Scope != skillScopePersonal || record.PersonalType != personalSkillTypeUser || record.Name != "build" || record.CanonicalHash == "" {
		t.Fatalf("archive record = %+v", record)
	}
	if filepath.IsAbs(record.ArchivePath) || stringsContainsAny(record.ArchivePath, superDolphinHome, project) {
		t.Fatalf("archive record leaks absolute path: %+v", record)
	}
}

func TestDeletePersonalFinalizeFailureRestoresCanonical(t *testing.T) {
	setSkillTestUserHome(t)
	project := t.TempDir()
	superDolphinHome := filepath.Join(t.TempDir(), ".super-dolphin")
	personalRoot := filepath.Join(superDolphinHome, "skills", "personal", personalSkillTypeUser)
	writeCanonicalSkill(t, filepath.Join(personalRoot, "build"), "build")
	audit := &capturingSkillAuditStore{failOnAction: "personal_delete_finalize", insertErr: errors.New("audit finalize down")}
	svc := &service{
		projectRoot:       project,
		projectSkillsRoot: defaultProjectSkillsRoot(project),
		superDolphinHome:  superDolphinHome,
		auditStore:        audit,
	}

	_, err := svc.DeleteLocal(skillTestContext(project), DeleteSkillParams{Name: "build", Scope: skillScopePersonal, PersonalType: personalSkillTypeUser})

	if err == nil || !strings.Contains(err.Error(), "audit finalize down") {
		t.Fatalf("DeleteLocal(personal) error = %v, want finalize audit failure", err)
	}
	assertExists(t, filepath.Join(personalRoot, "build", skillMainFile))
	assertNoArchiveContainsSkill(t, superDolphinHome, filepath.Join(skillScopePersonal, personalSkillTypeUser, "build"))
	assertMissing(t, filepath.Join(personalRoot, "build", personalSkillArchiveRecordFile))
}

func readJSONFile(t *testing.T, path string, dst any) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile(%s): %v", path, err)
	}
	if err := json.Unmarshal(data, dst); err != nil {
		t.Fatalf("Unmarshal(%s): %v", path, err)
	}
}

func assertPersonalMirrorManifestEntry(t *testing.T, provider SkillProvider, name, canonicalID string) {
	t.Helper()
	manifest, err := readSkillMirrorManifest(filepath.Join(providerPersonalMirrorRoot(provider), skillMirrorManifestFile))
	if err != nil {
		t.Fatalf("read %s personal mirror manifest: %v", provider, err)
	}
	entry, ok := manifest.Skills[name]
	if !ok {
		t.Fatalf("%s personal mirror manifest missing %q: %+v", provider, name, manifest.Skills)
	}
	if manifest.Scope != skillScopePersonal || manifest.Provider != string(provider) || !entry.Owned || entry.CanonicalID != canonicalID {
		t.Fatalf("%s personal mirror manifest entry %q = %+v in manifest %+v, want owned %s", provider, name, entry, manifest, canonicalID)
	}
}

func assertPersonalMirrorManifestMissing(t *testing.T, provider SkillProvider, name string) {
	t.Helper()
	manifest, err := readSkillMirrorManifest(filepath.Join(providerPersonalMirrorRoot(provider), skillMirrorManifestFile))
	if err != nil {
		t.Fatalf("read %s personal mirror manifest: %v", provider, err)
	}
	if _, ok := manifest.Skills[name]; ok {
		t.Fatalf("%s personal mirror manifest has %q: %+v", provider, name, manifest.Skills)
	}
}

func assertNoArchiveContainsSkill(t *testing.T, home, archiveSuffix string) {
	t.Helper()
	archiveRoot := filepath.Join(home, "skills", ".archive")
	matches, err := filepath.Glob(filepath.Join(archiveRoot, "*", archiveSuffix, skillMainFile))
	if err != nil {
		t.Fatalf("glob archive: %v", err)
	}
	if len(matches) != 0 {
		t.Fatalf("archive matches = %v, want none under %s", matches, archiveSuffix)
	}
}

type capturingSkillAuditStore struct {
	mu           sync.Mutex
	insertErr    error
	failOnAction string
	inserts      []skillMutationAuditEntry
}

func (s *capturingSkillAuditStore) Insert(_ context.Context, p skillMutationAuditEntry) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.insertErr != nil && (s.failOnAction == "" || s.failOnAction == p.Action) {
		return s.insertErr
	}
	s.inserts = append(s.inserts, p)
	return nil
}

func assertSkillMutationAuditActions(t *testing.T, inserts []skillMutationAuditEntry, want ...string) {
	t.Helper()
	if len(inserts) != len(want) {
		t.Fatalf("audit inserts = %+v, want actions %v", inserts, want)
	}
	for i, action := range want {
		if inserts[i].Action != action || inserts[i].EventType != skillMutationAuditEventType {
			t.Fatalf("audit insert %d = %+v, want action %s", i, inserts[i], action)
		}
	}
}

func stringsContainsAny(s string, values ...string) bool {
	for _, value := range values {
		if value != "" && strings.Contains(s, value) {
			return true
		}
	}
	return false
}
