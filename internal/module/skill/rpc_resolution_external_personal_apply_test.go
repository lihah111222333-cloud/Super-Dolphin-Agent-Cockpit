package skill

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	platformrpc "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/rpc"
)

func TestSkillResolutionApplyRPCUseProjectSharedRemovesExternalPersonalSameName(t *testing.T) {
	project, server, externalDir, _ := setupExternalPersonalProjectConflictFixtureWithService(t)
	item := findResolutionItem(t, dispatchResolutionList(t, server, project).Items, "external_personal_project_same_name", "build", skillScopePersonal)
	preview := dispatchExternalPersonalProjectPreview(t, server, project, item.ConflictID, "use_project_shared_skill", "")

	report := dispatchExternalPersonalProjectApply(t, server, project, item.ConflictID, "use_project_shared_skill", preview.Items[0], "")

	if report.Action != "use_project_shared_skill" || report.ResultingHash == "" {
		t.Fatalf("use project shared report = %+v, want action/resulting hash", report)
	}
	if _, err := os.Stat(externalDir); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("external provider dir stat err = %v, want removed", err)
	}
	assertFileContent(t, filepath.Join(project, ".agents", "skills", "build", skillMainFile), "---\nname: build\n---\n# build\n")
	assertResolutionItemAbsent(t, dispatchResolutionList(t, server, project).Items, "external_personal_project_same_name", "build", skillScopePersonal)
}

func TestSkillResolutionApplyRPCUseProjectSharedRemovesAllExternalPersonalProviderCopies(t *testing.T) {
	project, server, claudeDir, _ := setupExternalPersonalProjectConflictFixtureWithService(t)
	codexDir := filepath.Join(providerPersonalMirrorRoot(SkillProviderCodex), "build")
	writeSkillWithSupportFiles(t, codexDir, "build")
	writeFileWithMode(t, filepath.Join(codexDir, "references", "guide.md"), "codex external personal edit\n", 0o644)
	item := findResolutionItem(t, dispatchResolutionList(t, server, project).Items, "external_personal_project_same_name", "build", skillScopePersonal)
	if len(item.ProviderEntries) != 2 {
		t.Fatalf("provider entries = %+v, want claude and codex copies", item.ProviderEntries)
	}
	preview := dispatchExternalPersonalProjectPreview(t, server, project, item.ConflictID, "use_project_shared_skill", "")

	report := dispatchExternalPersonalProjectApply(t, server, project, item.ConflictID, "use_project_shared_skill", preview.Items[0], "")

	if report.Action != "use_project_shared_skill" || report.ResultingHash == "" {
		t.Fatalf("use project shared report = %+v, want action/resulting hash", report)
	}
	if _, err := os.Stat(claudeDir); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("claude external provider dir stat err = %v, want removed", err)
	}
	if _, err := os.Stat(codexDir); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("codex external provider dir stat err = %v, want removed", err)
	}
	assertResolutionItemAbsent(t, dispatchResolutionList(t, server, project).Items, "external_personal_project_same_name", "build", skillScopePersonal)
}

func TestSkillResolutionApplyRPCSavesExternalPersonalSameNameAsNewPersonalSkill(t *testing.T) {
	project, server, externalDir, svc := setupExternalPersonalProjectConflictFixtureWithService(t)
	item := findResolutionItem(t, dispatchResolutionList(t, server, project).Items, "external_personal_project_same_name", "build", skillScopePersonal)
	preview := dispatchExternalPersonalProjectPreview(t, server, project, item.ConflictID, ResolutionSaveAsNewPersonal, "build-private")

	report := dispatchExternalPersonalProjectApply(t, server, project, item.ConflictID, ResolutionSaveAsNewPersonal, preview.Items[0], "build-private")

	if report.Action != ResolutionSaveAsNewPersonal || report.ResultingHash == "" {
		t.Fatalf("save external personal report = %+v, want action/resulting hash", report)
	}
	assertFileContent(t, filepath.Join(svc.resolvedSuperDolphinHome(), "skills", "personal", personalSkillTypeImported, "build-private", "references", "guide.md"), "external personal edit\n")
	if _, err := os.Stat(externalDir); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("external provider dir stat err = %v, want moved out after save-as-new", err)
	}
	assertResolutionItemAbsent(t, dispatchResolutionList(t, server, project).Items, "external_personal_project_same_name", "build", skillScopePersonal)
}

func TestSkillResolutionApplyRPCSavesExternalPersonalKeepsResolvedSameNameResolved(t *testing.T) {
	project, server, _, svc := setupExternalPersonalProjectConflictFixtureWithService(t)
	writeSkillContent(t, filepath.Join(project, ".agents", "skills", "same"), "same", "# project same\n")
	writeSkillContent(t, filepath.Join(svc.resolvedSuperDolphinHome(), "skills", "personal", personalSkillTypeUser, "same"), "same", "# personal same\n")
	same := findResolutionItem(t, dispatchResolutionList(t, server, project).Items, "same_name", "same", "")
	projectSource := findResolutionSource(t, same, skillScopeProject, "")
	samePreview := dispatchResolutionPreviewWithKeepSource(t, server, project, same.ConflictID, projectSource.CanonicalID)
	dispatchResolutionApplyWithSourceIDs(t, server, project, same.ConflictID, ResolutionKeepSelected, samePreview.Items[0], projectSource.CanonicalID, "")
	assertResolutionItemAbsent(t, dispatchResolutionList(t, server, project).Items, "same_name", "same", "")
	assertRuntimeSelectedSkill(t, svc, project, "same", filepath.Join(project, ".agents", "skills", "same"))

	item := findResolutionItem(t, dispatchResolutionList(t, server, project).Items, "external_personal_project_same_name", "build", skillScopePersonal)
	preview := dispatchExternalPersonalProjectPreview(t, server, project, item.ConflictID, ResolutionSaveAsNewPersonal, "build-private")

	dispatchExternalPersonalProjectApply(t, server, project, item.ConflictID, ResolutionSaveAsNewPersonal, preview.Items[0], "build-private")

	assertResolutionItemAbsent(t, dispatchResolutionList(t, server, project).Items, "same_name", "same", "")
	assertRuntimeSelectedSkill(t, svc, project, "same", filepath.Join(project, ".agents", "skills", "same"))
}

func TestSkillResolutionApplyRPCUseExternalPersonalReplacesProjectAndRemovesMirror(t *testing.T) {
	project, server, externalDir, _ := setupExternalPersonalProjectConflictFixtureWithService(t)
	item := findResolutionItem(t, dispatchResolutionList(t, server, project).Items, "external_personal_project_same_name", "build", skillScopePersonal)
	preview := dispatchExternalPersonalProjectPreview(t, server, project, item.ConflictID, ResolutionUseExternalProviderSkill, "")

	report := dispatchExternalPersonalProjectApply(t, server, project, item.ConflictID, ResolutionUseExternalProviderSkill, preview.Items[0], "")

	if report.Action != ResolutionUseExternalProviderSkill || report.ResultingHash == "" {
		t.Fatalf("use external report = %+v, want action/resulting hash", report)
	}
	assertFileContent(t, filepath.Join(project, ".agents", "skills", "build", "references", "guide.md"), "external personal edit\n")
	if _, err := os.Stat(externalDir); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("external provider dir stat err = %v, want moved into project", err)
	}
	assertResolutionItemAbsent(t, dispatchResolutionList(t, server, project).Items, "external_personal_project_same_name", "build", skillScopePersonal)
}

func dispatchExternalPersonalProjectPreview(t *testing.T, server *platformrpc.Server, project, conflictID, action, newName string) skillResolutionPreviewResult {
	t.Helper()
	payload := map[string]string{
		"cwd":         project,
		"conflict_id": conflictID,
		"name":        "build",
		"scope":       skillScopePersonal,
		"provider":    string(SkillProviderClaude),
		"action":      action,
	}
	if newName != "" {
		payload["new_name"] = newName
	}
	data, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("Marshal external personal preview payload: %v", err)
	}
	raw, err := server.Dispatch(context.Background(), "skills/resolution_preview", data)
	if err != nil {
		t.Fatalf("Dispatch %s preview: %v", action, err)
	}
	return unmarshalSingleResolutionPreview(t, raw, action)
}

func dispatchExternalPersonalProjectApply(t *testing.T, server *platformrpc.Server, project, conflictID, action string, proof skillResolutionPreviewItem, newName string) SkillMirrorResolutionReport {
	t.Helper()
	payload := map[string]string{
		"cwd":          project,
		"conflict_id":  conflictID,
		"name":         "build",
		"scope":        skillScopePersonal,
		"provider":     string(SkillProviderClaude),
		"action":       action,
		"preview_id":   proof.PreviewID,
		"preview_hash": proof.PreviewHash,
	}
	if newName != "" {
		payload["new_name"] = newName
	}
	data, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("Marshal external personal apply payload: %v", err)
	}
	raw, err := server.Dispatch(context.Background(), "skills/resolution_apply", data)
	if err != nil {
		t.Fatalf("Dispatch %s apply: %v", action, err)
	}
	var got SkillMirrorResolutionReport
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("Unmarshal %s apply: %v", action, err)
	}
	return got
}

func assertResolutionNameAbsent(t *testing.T, items []skillResolutionItem, name string) {
	t.Helper()
	for _, item := range items {
		if item.Name == name {
			t.Fatalf("resolution item for %q = %+v, want absent", name, item)
		}
	}
}

func assertResolutionSameNameVisible(t *testing.T, items []skillResolutionItem, name string) {
	t.Helper()
	item := findResolutionItem(t, items, skillConflictSameName, name, "")
	assertResolutionSource(t, item, skillScopeProject, "", "project/"+name)
	assertResolutionSource(t, item, skillScopePersonal, personalSkillTypeUser, "personal/user/"+name)
}

func assertRuntimeSelectedSkill(t *testing.T, svc *service, project, name, dir string) {
	t.Helper()
	infos, err := svc.ListSkills(skillTestContext(project))
	if err != nil {
		t.Fatalf("ListSkills: %v", err)
	}
	assertHasSkillInfoDir(t, infos, name, skillScopeProject, "", dir)
	if hasSkillInfo(infos, name, skillScopePersonal, personalSkillTypeUser) {
		t.Fatalf("ListSkills = %+v, want personal same suppressed at runtime", infos)
	}
}
