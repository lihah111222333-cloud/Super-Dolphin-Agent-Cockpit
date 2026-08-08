package skill

import (
	"errors"
	"path/filepath"
	"testing"
)

func TestServiceListSkillInventoryIncludesPolicyHiddenProjectDuplicates(t *testing.T) {
	project := t.TempDir()
	superDolphinHome := filepath.Join(t.TempDir(), ".super-dolphin")
	leftDir := filepath.Join(project, ".agents", "skills", "security-engineer")
	rightDir := filepath.Join(project, ".agents", "skills", "security-standards")
	writeCanonicalSkill(t, leftDir, "安全工程师规范")
	writeCanonicalSkill(t, rightDir, "安全工程师规范")
	writeProjectSkillPolicy(t, project, projectKeepSelectedPolicy("安全工程师规范", "project/security-engineer", "project/security-standards"))
	svc := testInventoryService(project, superDolphinHome)

	inventory, err := svc.ListSkillInventory(skillTestContext(project))
	if err != nil {
		t.Fatalf("ListSkillInventory: %v", err)
	}
	assertHasSkillInfoDir(t, inventory, "安全工程师规范", skillScopeProject, "", leftDir)
	assertHasSkillInfoDir(t, inventory, "安全工程师规范", skillScopeProject, "", rightDir)

	effective, err := svc.ListSkills(skillTestContext(project))
	if err != nil {
		t.Fatalf("ListSkills: %v", err)
	}
	if len(effective) != 1 || !sameCleanPath(effective[0].Dir, leftDir) {
		t.Fatalf("ListSkills = %+v, want only selected project duplicate", effective)
	}
}

func TestServiceListSkillInventoryIncludesPolicyHiddenPersonalDuplicate(t *testing.T) {
	project := t.TempDir()
	superDolphinHome := filepath.Join(t.TempDir(), ".super-dolphin")
	projectDir := filepath.Join(project, ".agents", "skills", "plan")
	importedDir := filepath.Join(superDolphinHome, "skills", "personal", personalSkillTypeImported, "plan")
	writeCanonicalSkill(t, projectDir, "plan")
	writeCanonicalSkill(t, importedDir, "plan")
	writeProjectSkillPolicy(t, project, projectKeepSelectedPolicy("plan", "project/plan", "personal/imported/plan"))
	svc := testInventoryService(project, superDolphinHome)

	inventory, err := svc.ListSkillInventory(skillTestContext(project))
	if err != nil {
		t.Fatalf("ListSkillInventory: %v", err)
	}
	assertHasSkillInfoDir(t, inventory, "plan", skillScopeProject, "", projectDir)
	assertHasSkillInfoDir(t, inventory, "plan", skillScopePersonal, personalSkillTypeImported, importedDir)

	effective, err := svc.ListSkills(skillTestContext(project))
	if err != nil {
		t.Fatalf("ListSkills: %v", err)
	}
	assertHasSkillInfoDir(t, effective, "plan", skillScopeProject, "", projectDir)
	if hasSkillInfo(effective, "plan", skillScopePersonal, personalSkillTypeImported) {
		t.Fatalf("ListSkills = %+v, want policy-hidden imported duplicate absent", effective)
	}
}

func TestServiceListSkillsStillFailsClosedForUnresolvedSameNameConflict(t *testing.T) {
	project := t.TempDir()
	superDolphinHome := filepath.Join(t.TempDir(), ".super-dolphin")
	writeCanonicalSkill(t, filepath.Join(project, ".agents", "skills", "build"), "build")
	writeCanonicalSkill(t, filepath.Join(superDolphinHome, "skills", "personal", personalSkillTypeUser, "build"), "build")
	svc := testInventoryService(project, superDolphinHome)

	_, err := svc.ListSkills(skillTestContext(project))
	if !errors.Is(err, ErrSkillSameNameConflict) {
		t.Fatalf("ListSkills error = %v, want ErrSkillSameNameConflict", err)
	}
}

func testInventoryService(project, superDolphinHome string) *service {
	return &service{projectRoot: project, projectSkillsRoot: defaultProjectSkillsRoot(project), superDolphinHome: superDolphinHome}
}

func assertHasSkillInfoDir(t *testing.T, infos []SkillInfo, name, scope, personalType, dir string) {
	t.Helper()
	for _, info := range infos {
		if info.Name == name && info.Scope == scope && info.PersonalType == personalType && sameCleanPath(info.Dir, dir) {
			return
		}
	}
	t.Fatalf("missing skill info name=%q scope=%q personal_type=%q dir=%q in %+v", name, scope, personalType, dir, infos)
}
