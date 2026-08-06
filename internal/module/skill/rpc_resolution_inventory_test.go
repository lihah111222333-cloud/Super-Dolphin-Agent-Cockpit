package skill

import (
	"net/http"
	"path/filepath"
	"testing"
)

func TestSkillResolutionListReportsPolicyHiddenSameNameConflicts(t *testing.T) {
	skipWindowsShortMirrorIntegration(t)
	setSkillTestUserHome(t)

	project := t.TempDir()
	superHome := filepath.Join(t.TempDir(), ".super-dolphin")
	svc := &service{projectRoot: project, projectSkillsRoot: defaultProjectSkillsRoot(project), superDolphinHome: superHome, http: &http.Client{}, mirrorLocks: NewMirrorRootLockRegistry()}
	writeSkillContent(t, filepath.Join(project, ".agents", "skills", "security-engineer"), "安全工程师规范", "# project a\n")
	writeSkillContent(t, filepath.Join(project, ".agents", "skills", "security-standards"), "安全工程师规范", "# project b\n")
	writeSkillContent(t, filepath.Join(project, ".agents", "skills", "plan"), "编写计划", "# project plan\n")
	writeSkillContent(t, filepath.Join(superHome, "skills", "personal", personalSkillTypeImported, "plan"), "编写计划", "# imported plan\n")
	writeProjectSkillPolicy(t, project, projectSkillPolicy{Version: 1, KeepSelected: []projectSkillKeepSelected{
		projectKeepSelected("安全工程师规范", "project/security-engineer", "project/security-standards"),
		projectKeepSelected("编写计划", "project/plan", "personal/imported/编写计划"),
	}})

	got := dispatchResolutionList(t, newSkillRPCTestServer(t, svc), project)

	projectSameName := findResolutionItem(t, got.Items, "same_name", "安全工程师规范", "")
	assertResolutionActions(t, projectSameName, ResolutionViewDiff, ResolutionKeepSelected, ResolutionRenamePersonal)
	assertResolutionSource(t, projectSameName, skillScopeProject, "", "project/security-engineer")
	assertResolutionSource(t, projectSameName, skillScopeProject, "", "project/security-standards")
	personalSameName := findResolutionItem(t, got.Items, "same_name", "编写计划", "")
	assertResolutionActions(t, personalSameName, ResolutionViewDiff, ResolutionKeepSelected, ResolutionRenamePersonal)
	assertResolutionSource(t, personalSameName, skillScopeProject, "", "project/plan")
	assertResolutionSource(t, personalSameName, skillScopePersonal, personalSkillTypeImported, "personal/imported/编写计划")
}
