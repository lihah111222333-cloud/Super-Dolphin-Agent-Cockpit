package skill

import (
	"net/http"
	"path/filepath"
	"testing"

	platformrpc "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/rpc"
)

func TestSkillResolutionListTreatsExternalPersonalProjectSameNameAsProjectChoice(t *testing.T) {
	project, server := setupExternalPersonalProjectConflictFixture(t)

	item := findResolutionItem(t, dispatchResolutionList(t, server, project).Items, "external_personal_project_same_name", "build", skillScopePersonal)

	assertResolutionActions(t, item, ResolutionViewDiff, ResolutionUseProjectSharedSkill, ResolutionUseExternalProviderSkill, ResolutionSaveAsNewPersonal)
	assertResolutionProviderEntry(t, item, string(SkillProviderClaude), "build")
	if !sameCleanPath(filepath.FromSlash(item.ProviderEntries[0].TargetPath), filepath.Join(project, ".agents", "skills", "build")) {
		t.Fatalf("target_path = %q, want project canonical skill", item.ProviderEntries[0].TargetPath)
	}
}

func setupExternalPersonalProjectConflictFixture(t *testing.T) (string, *platformrpc.Server) {
	t.Helper()
	project, server, _, _ := setupExternalPersonalProjectConflictFixtureWithService(t)
	return project, server
}

func setupExternalPersonalProjectConflictFixtureWithService(t *testing.T) (string, *platformrpc.Server, string, *service) {
	t.Helper()
	setSkillTestUserHome(t)
	project := t.TempDir()
	superHome := filepath.Join(t.TempDir(), ".super-dolphin")
	svc := &service{root: t.TempDir(), projectRoot: project, projectSkillsRoot: defaultProjectSkillsRoot(project), superDolphinHome: superHome, http: &http.Client{}, mirrorLocks: NewMirrorRootLockRegistry()}
	writeSkillWithSupportFiles(t, filepath.Join(project, ".agents", "skills", "build"), "build")
	externalDir := filepath.Join(providerPersonalMirrorRoot(SkillProviderClaude), "build")
	writeSkillWithSupportFiles(t, externalDir, "build")
	writeFileWithMode(t, filepath.Join(externalDir, "references", "guide.md"), "external personal edit\n", 0o644)
	return project, newSkillRPCTestServer(t, svc), externalDir, svc
}
