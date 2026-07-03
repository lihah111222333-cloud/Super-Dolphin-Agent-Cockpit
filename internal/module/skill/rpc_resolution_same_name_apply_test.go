package skill

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	platformrpc "github.com/anthropic-ai/super-agent-v3/internal/platform/rpc"
)

func TestSkillResolutionApplySameNameKeepsProjectForCurrentProject(t *testing.T) {
	project, server, svc := setupSameNameResolutionFixture(t)
	publishSameNameMirrorsForTest(t, svc, project)
	item := findResolutionItem(t, dispatchResolutionList(t, server, project).Items, "same_name", "same", "")
	projectSource := findResolutionSource(t, item, skillScopeProject, "")
	preview := dispatchResolutionPreviewWithKeepSource(t, server, project, item.ConflictID, projectSource.CanonicalID)

	report := dispatchResolutionApplyWithSourceIDs(t, server, project, item.ConflictID, ResolutionKeepSelected, preview.Items[0], projectSource.CanonicalID, "")

	if report.Action != ResolutionKeepSelected || report.ResultingHash == "" {
		t.Fatalf("same-name project keep report = %+v, want action/resulting hash", report)
	}
	assertMissing(t, filepath.Join(svc.resolvedSuperDolphinHome(), "skills", "personal", personalSkillTypeUser, "same", skillMainFile))
	assertOnlySkillInfo(t, svc, project, "same", skillScopeProject, "")
	assertFileContent(t, filepath.Join(project, ".claude", "skills", "same", skillMainFile), "---\nname: same\n---\n# project same\n")
	assertFileContent(t, filepath.Join(providerProjectMirrorRoot(SkillProviderCodex, project), "same", skillMainFile), "---\nname: same\n---\n# project same\n")
	assertMissing(t, filepath.Join(providerPersonalMirrorRoot(SkillProviderClaude), "same"))
	assertMissing(t, filepath.Join(providerPersonalMirrorRoot(SkillProviderCodex), "same"))
}

func TestSkillResolutionApplySameNameKeepsSelectedPersonal(t *testing.T) {
	project, server, svc := setupSameNameResolutionFixture(t)
	publishSameNameMirrorsForTest(t, svc, project)
	item := findResolutionItem(t, dispatchResolutionList(t, server, project).Items, "same_name", "same", "")
	personalSource := findResolutionSource(t, item, skillScopePersonal, personalSkillTypeUser)
	preview := dispatchResolutionPreviewWithKeepSource(t, server, project, item.ConflictID, personalSource.CanonicalID)

	report := dispatchResolutionApplyWithSourceIDs(t, server, project, item.ConflictID, ResolutionKeepSelected, preview.Items[0], personalSource.CanonicalID, "")

	if report.Action != ResolutionKeepSelected || report.ResultingHash == "" {
		t.Fatalf("same-name keep selected report = %+v, want action/resulting hash", report)
	}
	assertMissing(t, filepath.Join(project, ".agents", "skills", "same", skillMainFile))
	assertOnlySkillInfo(t, svc, project, "same", skillScopePersonal, personalSkillTypeUser)
	assertFileContent(t, filepath.Join(providerPersonalMirrorRoot(SkillProviderClaude), "same", skillMainFile), "---\nname: same\n---\n# personal same\n")
	assertFileContent(t, filepath.Join(providerPersonalMirrorRoot(SkillProviderCodex), "same", skillMainFile), "---\nname: same\n---\n# personal same\n")
	assertMissing(t, filepath.Join(project, ".claude", "skills", "same"))
	assertMissing(t, filepath.Join(providerProjectMirrorRoot(SkillProviderCodex, project), "same"))
}

func TestSkillResolutionApplySameNameKeepSelectedPersonalRemovesProjectDuplicateOnlyInCurrentProject(t *testing.T) {
	setSkillTestUserHome(t)
	projectA := t.TempDir()
	projectB := t.TempDir()
	superHome := filepath.Join(t.TempDir(), ".super-dolphin")
	svc := &service{projectRoot: projectA, projectSkillsRoot: defaultProjectSkillsRoot(projectA), superDolphinHome: superHome, http: &http.Client{}}
	server := newSkillRPCTestServer(t, svc)
	writeSkillContent(t, filepath.Join(projectA, ".agents", "skills", "same"), "same", "# project a same\n")
	writeSkillContent(t, filepath.Join(projectB, ".agents", "skills", "same"), "same", "# project b same\n")
	writeSkillContent(t, filepath.Join(superHome, "skills", "personal", "user", "same"), "same", "# personal same\n")

	item := findResolutionItem(t, dispatchResolutionList(t, server, projectA).Items, "same_name", "same", "")
	personalSource := findResolutionSource(t, item, skillScopePersonal, personalSkillTypeUser)
	preview := dispatchResolutionPreviewWithKeepSource(t, server, projectA, item.ConflictID, personalSource.CanonicalID)

	report := dispatchResolutionApplyWithSourceIDs(t, server, projectA, item.ConflictID, ResolutionKeepSelected, preview.Items[0], personalSource.CanonicalID, "")

	if report.Action != ResolutionKeepSelected || report.ResultingHash == "" {
		t.Fatalf("same-name keep selected report = %+v, want action/resulting hash", report)
	}
	assertMissing(t, filepath.Join(projectA, ".agents", "skills", "same", skillMainFile))

	_, conflicts, err := newCanonicalStoreForOwner(superHome, defaultOwnerOSUID(), defaultAppProfile()).EffectiveSet(context.Background(), projectB)
	if err != nil {
		t.Fatalf("EffectiveSet projectB: %v", err)
	}
	assertSameNameConflict(t, conflicts, "same",
		canonicalSkillConflictSource{Scope: skillScopeProject, PersonalType: "", Name: "same"},
		canonicalSkillConflictSource{Scope: skillScopePersonal, PersonalType: personalSkillTypeUser, Name: "same"},
	)
}

func TestSkillResolutionApplySameNameKeepSelectedProjectRemovesPersonalDuplicate(t *testing.T) {
	project, server, svc := setupSameNameResolutionFixture(t)
	publishSameNameMirrorsForTest(t, svc, project)
	item := findResolutionItem(t, dispatchResolutionList(t, server, project).Items, "same_name", "same", "")
	projectSource := findResolutionSource(t, item, skillScopeProject, "")
	preview := dispatchResolutionPreviewWithKeepSource(t, server, project, item.ConflictID, projectSource.CanonicalID)

	report := dispatchResolutionApplyWithSourceIDs(t, server, project, item.ConflictID, ResolutionKeepSelected, preview.Items[0], projectSource.CanonicalID, "")

	if report.Action != ResolutionKeepSelected || report.ResultingHash == "" {
		t.Fatalf("same-name keep project report = %+v, want action/resulting hash", report)
	}
	assertMissing(t, filepath.Join(svc.resolvedSuperDolphinHome(), "skills", "personal", personalSkillTypeUser, "same", skillMainFile))
	if _, err := os.Stat(filepath.Join(svc.resolvedSuperDolphinHome(), "skills", personalSkillPolicyFile)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("personal policy stat err = %v, want not exist for project selection", err)
	}
}

func TestSkillResolutionApplySameNameRenameSelectedSource(t *testing.T) {
	project, server, svc := setupSameNameResolutionFixture(t)
	publishSameNameMirrorsForTest(t, svc, project)
	item := findResolutionItem(t, dispatchResolutionList(t, server, project).Items, "same_name", "same", "")
	personalSource := findResolutionSource(t, item, skillScopePersonal, personalSkillTypeUser)
	preview := dispatchResolutionPreviewWithRenameSource(t, server, project, item.ConflictID, personalSource.CanonicalID, "same-private")

	report := dispatchResolutionApplyWithName(t, server, project, item.ConflictID, ResolutionRenamePersonal, preview.Items[0], personalSource.CanonicalID, "same-private")

	if report.Action != ResolutionRenamePersonal || report.ResultingHash == "" {
		t.Fatalf("same-name rename report = %+v, want action/resulting hash", report)
	}
	assertMissing(t, filepath.Join(svc.resolvedSuperDolphinHome(), "skills", "personal", personalSkillTypeUser, "same", skillMainFile))
	assertFileContent(t, filepath.Join(svc.resolvedSuperDolphinHome(), "skills", "personal", personalSkillTypeUser, "same-private", skillMainFile), "---\nname: same-private\n---\n# personal same\n")
	assertHasSkillInfo(t, mustListSkillInventory(t, svc, project), "same", skillScopeProject, "")
	assertHasSkillInfo(t, mustListSkillInventory(t, svc, project), "same-private", skillScopePersonal, personalSkillTypeUser)
}

func TestSkillResolutionApplySameNameKeepSelectedProjectDuplicateByDirectory(t *testing.T) {
	skipWindowsShortMirrorIntegration(t)
	setSkillTestUserHome(t)

	project := t.TempDir()
	superHome := filepath.Join(t.TempDir(), ".super-dolphin")
	svc := &service{projectRoot: project, projectSkillsRoot: defaultProjectSkillsRoot(project), superDolphinHome: superHome, http: &http.Client{}}
	server := newSkillRPCTestServer(t, svc)
	writeSkillContent(t, filepath.Join(project, ".agents", "skills", "security-engineer"), "安全工程师规范", "# a\n")
	writeSkillContent(t, filepath.Join(project, ".agents", "skills", "security-standards"), "安全工程师规范", "# b\n")
	item := findResolutionItem(t, dispatchResolutionList(t, server, project).Items, "same_name", "安全工程师规范", "")
	source := findResolutionSource(t, item, skillScopeProject, "")
	if source.CanonicalID != "project/security-engineer" {
		source = findResolutionSourceByID(t, item, "project/security-standards")
	}
	preview := dispatchResolutionPreviewWithKeepSource(t, server, project, item.ConflictID, source.CanonicalID)

	report := dispatchResolutionApplyWithSourceIDs(t, server, project, item.ConflictID, ResolutionKeepSelected, preview.Items[0], source.CanonicalID, "")

	if report.Action != ResolutionKeepSelected || report.ResultingHash == "" {
		t.Fatalf("same-name project duplicate keep report = %+v, want action/resulting hash", report)
	}
	infos, err := svc.ListSkills(skillTestContext(project))
	if err != nil {
		t.Fatalf("ListSkills after project duplicate keep selected: %v", err)
	}
	if len(infos) != 1 || infos[0].Name != "安全工程师规范" {
		t.Fatalf("ListSkills = %+v, want selected project duplicate only", infos)
	}
}

func setupSameNameResolutionFixture(t *testing.T) (string, *platformrpc.Server, *service) {
	t.Helper()
	setSkillTestUserHome(t)
	project := t.TempDir()
	superHome := filepath.Join(t.TempDir(), ".super-dolphin")
	svc := &service{projectRoot: project, projectSkillsRoot: defaultProjectSkillsRoot(project), superDolphinHome: superHome, http: &http.Client{}}
	writeSkillContent(t, filepath.Join(project, ".agents", "skills", "same"), "same", "# project same\n")
	writeSkillContent(t, filepath.Join(superHome, "skills", "personal", "user", "same"), "same", "# personal same\n")
	return project, newSkillRPCTestServer(t, svc), svc
}

func publishSameNameMirrorsForTest(t *testing.T, svc *service, project string) {
	t.Helper()
	records, err := newCanonicalStore(svc.resolvedSuperDolphinHome()).scan(project)
	if err != nil {
		t.Fatalf("scan canonical records: %v", err)
	}
	targets := []SkillMirrorTarget{
		{TargetID: "claude:project:" + RepoFingerprint(project), Provider: SkillProviderClaude, Scope: skillScopeProject, Root: providerProjectMirrorRoot(SkillProviderClaude, project), CanonicalRootID: RepoFingerprint(project)},
		{TargetID: "claude:user-global:test", Provider: SkillProviderClaude, Scope: skillScopePersonal, Root: providerPersonalMirrorRoot(SkillProviderClaude), CanonicalRootID: "sd_owner:test"},
		{TargetID: "codex:user-global:test", Provider: SkillProviderCodex, Scope: skillScopePersonal, Root: providerPersonalMirrorRoot(SkillProviderCodex), CanonicalRootID: "sd_owner:test"},
	}
	report, err := PublishSkillMirrors(context.Background(), records, targets)
	if err != nil {
		t.Fatalf("PublishSkillMirrors same-name fixture: %v", err)
	}
	if len(report.Conflicts) > 0 {
		t.Fatalf("same-name fixture mirror publish conflicts = %+v", report.Conflicts)
	}
}

func dispatchResolutionApplyWithSourceIDs(t *testing.T, server *platformrpc.Server, project, conflictID, action string, proof skillResolutionPreviewItem, keepSourceID, disablePolicyTarget string) SkillMirrorResolutionReport {
	t.Helper()
	payload := map[string]string{
		"cwd":          project,
		"conflict_id":  conflictID,
		"name":         "same",
		"scope":        skillScopeProject,
		"action":       action,
		"preview_id":   proof.PreviewID,
		"preview_hash": proof.PreviewHash,
	}
	if keepSourceID != "" {
		payload["keep_source_id"] = keepSourceID
	}
	if disablePolicyTarget != "" {
		payload["disable_policy_target"] = disablePolicyTarget
	}
	data, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("Marshal same-name apply payload: %v", err)
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

func dispatchResolutionApplyWithName(t *testing.T, server *platformrpc.Server, project, conflictID, action string, proof skillResolutionPreviewItem, keepSourceID, newName string) SkillMirrorResolutionReport {
	t.Helper()
	payload := map[string]string{
		"cwd":            project,
		"conflict_id":    conflictID,
		"name":           "same",
		"scope":          skillScopeProject,
		"action":         action,
		"new_name":       newName,
		"keep_source_id": keepSourceID,
		"preview_id":     proof.PreviewID,
		"preview_hash":   proof.PreviewHash,
	}
	data, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("Marshal same-name apply payload: %v", err)
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

func dispatchResolutionPreviewWithKeepSource(t *testing.T, server *platformrpc.Server, project, conflictID, keepSourceID string) skillResolutionPreviewResult {
	t.Helper()
	raw, err := server.Dispatch(context.Background(), "skills/resolution_preview", mustRawJSON(t, map[string]any{
		"cwd":            project,
		"conflict_id":    conflictID,
		"name":           "same",
		"scope":          "project",
		"action":         ResolutionKeepSelected,
		"keep_source_id": keepSourceID,
	}))
	if err != nil {
		t.Fatalf("Dispatch keep_selected preview with keep_source_id: %v", err)
	}
	return unmarshalSingleResolutionPreview(t, raw, ResolutionKeepSelected)
}

func dispatchResolutionPreviewWithRenameSource(t *testing.T, server *platformrpc.Server, project, conflictID, keepSourceID, newName string) skillResolutionPreviewResult {
	t.Helper()
	raw, err := server.Dispatch(context.Background(), "skills/resolution_preview", mustRawJSON(t, map[string]any{
		"cwd":            project,
		"conflict_id":    conflictID,
		"name":           "same",
		"scope":          "project",
		"action":         ResolutionRenamePersonal,
		"keep_source_id": keepSourceID,
		"new_name":       newName,
	}))
	if err != nil {
		t.Fatalf("Dispatch rename_personal preview with keep_source_id: %v", err)
	}
	return unmarshalSingleResolutionPreview(t, raw, ResolutionRenamePersonal)
}

func assertOnlySkillInfo(t *testing.T, svc *service, project, name, scope, personalType string) {
	t.Helper()
	infos, err := svc.ListSkillInventory(skillTestContext(project))
	if err != nil {
		t.Fatalf("ListSkillInventory after same-name keep: %v", err)
	}
	if len(infos) != 1 {
		t.Fatalf("ListSkillInventory = %+v, want exactly one skill", infos)
	}
	assertHasSkillInfo(t, infos, name, scope, personalType)
	effective, err := svc.ListSkills(skillTestContext(project))
	if err != nil {
		t.Fatalf("ListSkills after same-name keep: %v", err)
	}
	assertHasSkillInfo(t, effective, name, scope, personalType)
}

func mustListSkillInventory(t *testing.T, svc *service, project string) []SkillInfo {
	t.Helper()
	infos, err := svc.ListSkillInventory(skillTestContext(project))
	if err != nil {
		t.Fatalf("ListSkillInventory: %v", err)
	}
	return infos
}

func findResolutionSource(t *testing.T, item skillResolutionItem, scope, personalType string) skillResolutionSource {
	t.Helper()
	for _, source := range item.Sources {
		if source.Scope == scope && source.PersonalType == personalType {
			return source
		}
	}
	t.Fatalf("missing resolution source scope=%q personal_type=%q in %+v", scope, personalType, item.Sources)
	return skillResolutionSource{}
}

func findResolutionSourceByID(t *testing.T, item skillResolutionItem, id string) skillResolutionSource {
	t.Helper()
	for _, source := range item.Sources {
		if source.CanonicalID == id {
			return source
		}
	}
	t.Fatalf("missing resolution source id=%q in %+v", id, item.Sources)
	return skillResolutionSource{}
}
