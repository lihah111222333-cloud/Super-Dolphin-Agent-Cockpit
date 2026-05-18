package skill

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	platformrpc "github.com/anthropic-ai/super-agent-v3/internal/platform/rpc"
)

func TestSkillResolutionApplyRPCSyncBackUsesPreviewProof(t *testing.T) {
	project, server, canonicalDir, _, svc := setupResolutionPreviewDriftFixtureWithService(t)
	svc.auditStore = &capturingSkillAuditStore{}
	item := findResolutionItem(t, dispatchResolutionList(t, server, project).Items, "mirror_drift", "drift", skillScopeProject)
	preview := dispatchResolutionPreview(t, server, project, item.ConflictID, ResolutionSyncBackCanonical)
	proof := preview.Items[0]

	report := dispatchResolutionApply(t, server, project, item.ConflictID, "drift", ResolutionSyncBackCanonical, proof, "")

	if report.ResultingHash == "" || report.Action != ResolutionSyncBackCanonical {
		t.Fatalf("resolution_apply report = %+v, want resulting hash/action", report)
	}
	assertFileContent(t, filepath.Join(canonicalDir, "references", "guide.md"), "project drift\n")
}

func TestSkillResolutionApplyRPCUsesRequestCWDForProjectTarget(t *testing.T) {
	project, server, canonicalDir, _, svc := setupResolutionPreviewDriftFixtureWithService(t)
	otherProject := t.TempDir()
	svc.projectRoot = otherProject
	svc.projectSkillsRoot = defaultProjectSkillsRoot(otherProject)
	svc.auditStore = &capturingSkillAuditStore{}
	item := findResolutionItem(t, dispatchResolutionList(t, server, project).Items, "mirror_drift", "drift", skillScopeProject)
	preview := dispatchResolutionPreview(t, server, project, item.ConflictID, ResolutionSyncBackCanonical)

	report := dispatchResolutionApply(t, server, project, item.ConflictID, "drift", ResolutionSyncBackCanonical, preview.Items[0], "")

	if report.ResultingHash == "" {
		t.Fatalf("resolution_apply report = %+v, want resulting hash", report)
	}
	assertFileContent(t, filepath.Join(canonicalDir, "references", "guide.md"), "project drift\n")
}

func TestSkillResolutionApplyRPCRejectsPreviewHashMismatch(t *testing.T) {
	project, server, canonicalDir, _, svc := setupResolutionPreviewDriftFixtureWithService(t)
	svc.auditStore = &capturingSkillAuditStore{}
	beforeHash := skillDirContentHash(canonicalDir)
	item := findResolutionItem(t, dispatchResolutionList(t, server, project).Items, "mirror_drift", "drift", skillScopeProject)
	preview := dispatchResolutionPreview(t, server, project, item.ConflictID, ResolutionSyncBackCanonical)
	proof := preview.Items[0]
	proof.PreviewHash = "sha256:not-the-preview"

	_, err := dispatchResolutionApplyRaw(t, server, project, item.ConflictID, "drift", ResolutionSyncBackCanonical, proof, "")
	if err == nil || !strings.Contains(err.Error(), "preview hash mismatch") {
		t.Fatalf("resolution_apply hash mismatch error = %v, want mismatch", err)
	}
	if afterHash := skillDirContentHash(canonicalDir); afterHash != beforeHash {
		t.Fatalf("canonical mutated after rejected apply: before=%s after=%s", beforeHash, afterHash)
	}
}

func TestSkillResolutionApplyRPCRequiresPreviewIDAndHash(t *testing.T) {
	project, server, _, _, svc := setupResolutionPreviewDriftFixtureWithService(t)
	svc.auditStore = &capturingSkillAuditStore{}
	item := findResolutionItem(t, dispatchResolutionList(t, server, project).Items, "mirror_drift", "drift", skillScopeProject)
	preview := dispatchResolutionPreview(t, server, project, item.ConflictID, ResolutionSyncBackCanonical)
	proof := preview.Items[0]

	missingID := proof
	missingID.PreviewID = ""
	_, err := dispatchResolutionApplyRaw(t, server, project, item.ConflictID, "drift", ResolutionSyncBackCanonical, missingID, "")
	if err == nil || !strings.Contains(err.Error(), "preview_id") {
		t.Fatalf("resolution_apply missing preview_id error = %v, want preview_id rejection", err)
	}

	missingHash := proof
	missingHash.PreviewHash = ""
	_, err = dispatchResolutionApplyRaw(t, server, project, item.ConflictID, "drift", ResolutionSyncBackCanonical, missingHash, "")
	if err == nil || !strings.Contains(err.Error(), "preview_hash") {
		t.Fatalf("resolution_apply missing preview_hash error = %v, want preview_hash rejection", err)
	}
}

func TestSkillResolutionApplyRPCRestoresCanonicalDeletedWithDrift(t *testing.T) {
	project, server, svc := setupCanonicalDeletedDriftFixtureWithService(t)
	svc.auditStore = &capturingSkillAuditStore{}
	item := findResolutionItem(t, dispatchResolutionList(t, server, project).Items, "canonical_deleted_with_drift", "deleted", skillScopeProject)
	preview := dispatchResolutionPreviewNamed(t, server, project, item.ConflictID, "deleted", ResolutionSyncBackCanonical)
	proof := preview.Items[0]

	report := dispatchResolutionApply(t, server, project, item.ConflictID, "deleted", ResolutionSyncBackCanonical, proof, "")

	if report.ResultingHash == "" {
		t.Fatalf("resolution_apply deleted sync report = %+v, want resulting hash", report)
	}
	assertFileContent(t, filepath.Join(project, ".agent", "skills", "deleted", "references", "guide.md"), "project drift\n")
}

func TestSkillResolutionApplyRPCConfirmsDeleteDriftedMirror(t *testing.T) {
	project, server, svc := setupCanonicalDeletedDriftFixtureWithService(t)
	svc.auditStore = &capturingSkillAuditStore{}
	item := findResolutionItem(t, dispatchResolutionList(t, server, project).Items, "canonical_deleted_with_drift", "deleted", skillScopeProject)
	preview := dispatchResolutionPreviewNamed(t, server, project, item.ConflictID, "deleted", ResolutionConfirmDeleteDriftedMirror)

	report := dispatchResolutionApply(t, server, project, item.ConflictID, "deleted", ResolutionConfirmDeleteDriftedMirror, preview.Items[0], "")

	if report.Action != ResolutionConfirmDeleteDriftedMirror {
		t.Fatalf("resolution_apply confirm-delete report = %+v, want action", report)
	}
	if _, err := os.Stat(filepath.Join(project, ".codex", "skills", "deleted")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("deleted mirror stat error = %v, want not exist", err)
	}
	manifest, err := readSkillMirrorManifest(filepath.Join(project, ".codex", "skills", skillMirrorManifestFile))
	if err != nil {
		t.Fatalf("read mirror manifest: %v", err)
	}
	if _, ok := manifest.Skills["deleted"]; ok {
		t.Fatalf("deleted mirror still present in manifest: %+v", manifest.Skills)
	}
}

func TestSkillResolutionApplyRPCSaveAsNewCanonicalDeletedClearsOriginalMirror(t *testing.T) {
	project, server, svc := setupCanonicalDeletedDriftFixtureWithService(t)
	svc.auditStore = &capturingSkillAuditStore{}
	item := findResolutionItem(t, dispatchResolutionList(t, server, project).Items, "canonical_deleted_with_drift", "deleted", skillScopeProject)
	preview := dispatchResolutionPreviewNamedWithName(t, server, project, item.ConflictID, "deleted", ResolutionSaveAsNewSkill, "deleted-copy")

	report := dispatchResolutionApply(t, server, project, item.ConflictID, "deleted", ResolutionSaveAsNewSkill, preview.Items[0], "deleted-copy")

	if report.ResultingHash == "" || report.PartialFailure {
		t.Fatalf("resolution_apply deleted save-as-new report = %+v, want complete resulting hash", report)
	}
	assertFileContent(t, filepath.Join(project, ".agent", "skills", "deleted-copy", "references", "guide.md"), "project drift\n")
	if _, err := os.Stat(filepath.Join(project, ".codex", "skills", "deleted")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("original deleted mirror stat error = %v, want not exist", err)
	}
	manifest, err := readSkillMirrorManifest(filepath.Join(project, ".codex", "skills", skillMirrorManifestFile))
	if err != nil {
		t.Fatalf("read mirror manifest: %v", err)
	}
	if _, ok := manifest.Skills["deleted"]; ok {
		t.Fatalf("deleted mirror still present in manifest after save-as-new: %+v", manifest.Skills)
	}
}

func TestSkillResolutionApplyRPCImportsUnmanagedProviderSkill(t *testing.T) {
	project, server, svc := setupUnmanagedProviderConflictFixtureWithService(t)
	svc.auditStore = &capturingSkillAuditStore{}
	item := findResolutionItem(t, dispatchResolutionList(t, server, project).Items, "unmanaged_same_name", "scratch", skillScopeProject)
	preview := dispatchResolutionPreviewProviderNamed(t, server, project, item.ConflictID, "scratch", "claude", ResolutionImportPersonal)

	report := dispatchResolutionApply(t, server, project, item.ConflictID, "scratch", ResolutionImportPersonal, preview.Items[0], "")

	if report.ResultingHash == "" {
		t.Fatalf("resolution_apply import report = %+v, want resulting hash", report)
	}
	assertFileContent(t, filepath.Join(svc.resolvedSuperDolphinHome(), "skills", "personal", personalSkillTypeImported, "scratch", skillMainFile), "---\nname: scratch\n---\n# scratch\n")
	assertFileContent(t, filepath.Join(svc.resolvedSuperDolphinHome(), "providers", "claude", "skills", "scratch", skillMainFile), "---\nname: scratch\n---\n# scratch\n")
	assertFileContent(t, filepath.Join(svc.resolvedSuperDolphinHome(), "providers", "codex", "skills", "scratch", skillMainFile), "---\nname: scratch\n---\n# scratch\n")
}

func TestSkillResolutionApplyRPCTakesOverUnmanagedProviderSkill(t *testing.T) {
	project, server, svc := setupUnmanagedProviderConflictFixtureWithService(t)
	svc.auditStore = &capturingSkillAuditStore{}
	item := findResolutionItem(t, dispatchResolutionList(t, server, project).Items, "unmanaged_same_name", "scratch", skillScopeProject)
	preview := dispatchResolutionPreviewProviderNamed(t, server, project, item.ConflictID, "scratch", "claude", ResolutionTakeoverProvider)

	report := dispatchResolutionApply(t, server, project, item.ConflictID, "scratch", ResolutionTakeoverProvider, preview.Items[0], "")

	if report.ResultingHash == "" {
		t.Fatalf("resolution_apply takeover report = %+v, want resulting hash", report)
	}
	manifest, err := readSkillMirrorManifest(filepath.Join(project, ".claude", "skills", skillMirrorManifestFile))
	if err != nil {
		t.Fatalf("read takeover manifest: %v", err)
	}
	if entry := manifest.Skills["scratch"]; !entry.Owned || entry.MirrorHash != report.ResultingHash {
		t.Fatalf("takeover manifest entry = %+v, want owned mirror hash %s", entry, report.ResultingHash)
	}
	assertFileContent(t, filepath.Join(project, ".agent", "skills", "scratch", skillMainFile), "---\nname: scratch\n---\n# scratch\n")
}

func TestSkillResolutionApplyRPCSaveAsNewClearsOriginalMirrorDrift(t *testing.T) {
	project, server, canonicalDir, mirrorDir, svc := setupResolutionPreviewDriftFixtureWithService(t)
	svc.auditStore = &capturingSkillAuditStore{}
	item := findResolutionItem(t, dispatchResolutionList(t, server, project).Items, "mirror_drift", "drift", skillScopeProject)
	preview := dispatchResolutionPreviewWithName(t, server, project, item.ConflictID, ResolutionSaveAsNewSkill, "drift-copy")

	report := dispatchResolutionApply(t, server, project, item.ConflictID, "drift", ResolutionSaveAsNewSkill, preview.Items[0], "drift-copy")

	if report.ResultingHash == "" {
		t.Fatalf("resolution_apply save-as-new report = %+v, want resulting hash", report)
	}
	assertFileContent(t, filepath.Join(project, ".agent", "skills", "drift-copy", "references", "guide.md"), "project drift\n")
	assertFileContent(t, filepath.Join(mirrorDir, "references", "guide.md"), "guide\n")
	manifest, err := readSkillMirrorManifest(filepath.Join(project, ".codex", "skills", skillMirrorManifestFile))
	if err != nil {
		t.Fatalf("read mirror manifest: %v", err)
	}
	entry := manifest.Skills["drift"]
	mirrorHash, err := stableMirrorDirectoryHash(mirrorDir)
	if err != nil {
		t.Fatalf("stableMirrorDirectoryHash mirror: %v", err)
	}
	canonicalHash, err := stableMirrorDirectoryHash(canonicalDir)
	if err != nil {
		t.Fatalf("stableMirrorDirectoryHash canonical: %v", err)
	}
	if entry.MirrorHash != mirrorHash || entry.CanonicalHash != canonicalHash {
		t.Fatalf("manifest drift entry = %+v, want mirror=%s canonical=%s", entry, mirrorHash, canonicalHash)
	}
	records, err := newCanonicalStore(svc.resolvedSuperDolphinHome()).scan(project)
	if err != nil {
		t.Fatalf("scan canonical records: %v", err)
	}
	target := SkillMirrorTarget{TargetID: "codex:project:" + RepoFingerprint(project), Provider: SkillProviderCodex, Scope: skillScopeProject, Root: filepath.Join(project, ".codex", "skills"), CanonicalRootID: RepoFingerprint(project)}
	conflicts, err := DetectSkillMirrorConflicts(records, []SkillMirrorTarget{target})
	if err != nil {
		t.Fatalf("DetectSkillMirrorConflicts: %v", err)
	}
	if len(conflicts) != 0 {
		t.Fatalf("conflicts after save-as-new = %+v, want none; canonical=%s", conflicts, canonicalDir)
	}
}

func TestSkillResolutionApplySameNameKeepsProjectForCurrentProject(t *testing.T) {
	project, server, svc := setupSameNameResolutionFixture(t)
	publishSameNameMirrorsForTest(t, svc, project)
	item := findResolutionItem(t, dispatchResolutionList(t, server, project).Items, "same_name", "same", "")
	personalSource := findResolutionSource(t, item, skillScopePersonal, personalSkillTypeUser)
	preview := dispatchResolutionPreviewWithDisableTarget(t, server, project, item.ConflictID, personalSource.CanonicalID)

	report := dispatchResolutionApplyWithSourceIDs(t, server, project, item.ConflictID, ResolutionDisablePersonalForProject, preview.Items[0], "", personalSource.CanonicalID)

	if report.Action != ResolutionDisablePersonalForProject || report.ResultingHash == "" {
		t.Fatalf("same-name project keep report = %+v, want action/resulting hash", report)
	}
	infos, err := svc.ListSkills(skillTestContext(project))
	if err != nil {
		t.Fatalf("ListSkills after same-name project keep: %v", err)
	}
	assertHasSkillInfo(t, infos, "same", skillScopeProject, "")
	assertFileContent(t, filepath.Join(project, ".claude", "skills", "same", skillMainFile), "---\nname: same\n---\n# project same\n")
	assertFileContent(t, filepath.Join(project, ".codex", "skills", "same", skillMainFile), "---\nname: same\n---\n# project same\n")
	assertMissing(t, filepath.Join(svc.resolvedSuperDolphinHome(), "providers", "claude", "skills", "same"))
	assertMissing(t, filepath.Join(svc.resolvedSuperDolphinHome(), "providers", "codex", "skills", "same"))
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
	infos, err := svc.ListSkills(skillTestContext(project))
	if err != nil {
		t.Fatalf("ListSkills after same-name keep selected: %v", err)
	}
	assertHasSkillInfo(t, infos, "same", skillScopePersonal, personalSkillTypeUser)
	assertFileContent(t, filepath.Join(svc.resolvedSuperDolphinHome(), "providers", "claude", "skills", "same", skillMainFile), "---\nname: same\n---\n# personal same\n")
	assertFileContent(t, filepath.Join(svc.resolvedSuperDolphinHome(), "providers", "codex", "skills", "same", skillMainFile), "---\nname: same\n---\n# personal same\n")
	assertMissing(t, filepath.Join(project, ".claude", "skills", "same"))
	assertMissing(t, filepath.Join(project, ".codex", "skills", "same"))
}

func TestSkillResolutionApplySameNameKeepSelectedPersonalDoesNotLeakAcrossProjects(t *testing.T) {
	projectA := t.TempDir()
	projectB := t.TempDir()
	superHome := filepath.Join(t.TempDir(), ".super-dolphin")
	svc := &service{projectRoot: projectA, projectSkillsRoot: defaultProjectSkillsRoot(projectA), superDolphinHome: superHome, http: &http.Client{}}
	server := newSkillRPCTestServer(t, svc)
	writeSkillContent(t, filepath.Join(projectA, ".agent", "skills", "same"), "same", "# project a same\n")
	writeSkillContent(t, filepath.Join(projectB, ".agent", "skills", "same"), "same", "# project b same\n")
	writeSkillContent(t, filepath.Join(superHome, "skills", "personal", "user", "same"), "same", "# personal same\n")

	item := findResolutionItem(t, dispatchResolutionList(t, server, projectA).Items, "same_name", "same", "")
	personalSource := findResolutionSource(t, item, skillScopePersonal, personalSkillTypeUser)
	preview := dispatchResolutionPreviewWithKeepSource(t, server, projectA, item.ConflictID, personalSource.CanonicalID)

	report := dispatchResolutionApplyWithSourceIDs(t, server, projectA, item.ConflictID, ResolutionKeepSelected, preview.Items[0], personalSource.CanonicalID, "")

	if report.Action != ResolutionKeepSelected || report.ResultingHash == "" {
		t.Fatalf("same-name keep selected report = %+v, want action/resulting hash", report)
	}
	if _, err := os.Stat(filepath.Join(projectA, ".agent", "skills", projectSkillPolicyFile)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("project policy stat err = %v, want not exist for personal selection", err)
	}
	policyData, err := os.ReadFile(filepath.Join(superHome, "skills", personalSkillPolicyFile))
	if err != nil {
		t.Fatalf("ReadFile personal policy: %v", err)
	}
	policyBody := string(policyData)
	for _, want := range []string{`"keep_selected"`, `"selected_source_id": "personal/user/same"`, `"excluded_source_ids"`, `"project/same"`} {
		if !strings.Contains(policyBody, want) {
			t.Fatalf("personal policy = %s, missing %s", policyBody, want)
		}
	}

	_, conflicts, err := newCanonicalStoreForOwner(superHome, defaultOwnerOSUID(), defaultAppProfile()).EffectiveSet(context.Background(), projectB)
	if err != nil {
		t.Fatalf("EffectiveSet projectB: %v", err)
	}
	assertSameNameConflict(t, conflicts, "same",
		canonicalSkillConflictSource{Scope: skillScopeProject, PersonalType: "", Name: "same"},
		canonicalSkillConflictSource{Scope: skillScopePersonal, PersonalType: personalSkillTypeUser, Name: "same"},
	)
}

func TestSkillResolutionApplySameNameKeepSelectedProjectWritesOnlyProjectPolicy(t *testing.T) {
	project, server, svc := setupSameNameResolutionFixture(t)
	publishSameNameMirrorsForTest(t, svc, project)
	item := findResolutionItem(t, dispatchResolutionList(t, server, project).Items, "same_name", "same", "")
	projectSource := findResolutionSource(t, item, skillScopeProject, "")
	preview := dispatchResolutionPreviewWithKeepSource(t, server, project, item.ConflictID, projectSource.CanonicalID)

	report := dispatchResolutionApplyWithSourceIDs(t, server, project, item.ConflictID, ResolutionKeepSelected, preview.Items[0], projectSource.CanonicalID, "")

	if report.Action != ResolutionKeepSelected || report.ResultingHash == "" {
		t.Fatalf("same-name keep project report = %+v, want action/resulting hash", report)
	}
	policyData, err := os.ReadFile(filepath.Join(project, ".agent", "skills", projectSkillPolicyFile))
	if err != nil {
		t.Fatalf("ReadFile project policy: %v", err)
	}
	if body := string(policyData); !strings.Contains(body, `"selected_source_id": "project/same"`) || strings.Contains(body, `"selected_source_id": "personal/user/same"`) {
		t.Fatalf("project policy = %s, want selected project only", body)
	}
	if _, err := os.Stat(filepath.Join(svc.resolvedSuperDolphinHome(), "skills", personalSkillPolicyFile)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("personal policy stat err = %v, want not exist for project selection", err)
	}
}

func setupSameNameResolutionFixture(t *testing.T) (string, *platformrpc.Server, *service) {
	t.Helper()
	project := t.TempDir()
	superHome := filepath.Join(t.TempDir(), ".super-dolphin")
	svc := &service{projectRoot: project, projectSkillsRoot: defaultProjectSkillsRoot(project), superDolphinHome: superHome, http: &http.Client{}}
	writeSkillContent(t, filepath.Join(project, ".agent", "skills", "same"), "same", "# project same\n")
	writeSkillContent(t, filepath.Join(superHome, "skills", "personal", "user", "same"), "same", "# personal same\n")
	return project, newSkillRPCTestServer(t, svc), svc
}

func publishSameNameMirrorsForTest(t *testing.T, svc *service, project string) {
	t.Helper()
	projectReport := svc.publishWriteTimeMirrorsForScope(context.Background(), project, skillScopeProject, "", "same")
	if len(projectReport.Conflicts) > 0 {
		t.Fatalf("project mirror publish conflicts = %+v", projectReport.Conflicts)
	}
	personalReport := svc.publishWriteTimeMirrorsForScope(context.Background(), project, skillScopePersonal, personalSkillTypeUser, "same")
	if len(personalReport.Conflicts) > 0 {
		t.Fatalf("personal mirror publish conflicts = %+v", personalReport.Conflicts)
	}
}

func dispatchResolutionApply(t *testing.T, server *platformrpc.Server, project, conflictID, name, action string, proof skillResolutionPreviewItem, newName string) SkillMirrorResolutionReport {
	t.Helper()
	raw, err := dispatchResolutionApplyRaw(t, server, project, conflictID, name, action, proof, newName)
	if err != nil {
		t.Fatalf("Dispatch %s apply: %v", action, err)
	}
	var got SkillMirrorResolutionReport
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("Unmarshal %s apply: %v", action, err)
	}
	return got
}

func dispatchResolutionApplyRaw(t *testing.T, server *platformrpc.Server, project, conflictID, name, action string, proof skillResolutionPreviewItem, newName string) (json.RawMessage, error) {
	t.Helper()
	payload := map[string]string{
		"cwd":          project,
		"conflict_id":  conflictID,
		"name":         name,
		"scope":        skillScopeProject,
		"provider":     "codex",
		"action":       action,
		"preview_id":   proof.PreviewID,
		"preview_hash": proof.PreviewHash,
	}
	if newName != "" {
		payload["new_name"] = newName
	}
	data, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("Marshal apply payload: %v", err)
	}
	return server.Dispatch(context.Background(), "skills/resolution_apply", data)
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

func dispatchResolutionPreviewWithKeepSource(t *testing.T, server *platformrpc.Server, project, conflictID, keepSourceID string) skillResolutionPreviewResult {
	t.Helper()
	raw, err := server.Dispatch(context.Background(), "skills/resolution_preview", json.RawMessage(`{"cwd":"`+project+`","conflict_id":"`+conflictID+`","name":"same","scope":"project","action":"`+ResolutionKeepSelected+`","keep_source_id":"`+keepSourceID+`"}`))
	if err != nil {
		t.Fatalf("Dispatch keep_selected preview with keep_source_id: %v", err)
	}
	return unmarshalSingleResolutionPreview(t, raw, ResolutionKeepSelected)
}

func dispatchResolutionPreviewWithDisableTarget(t *testing.T, server *platformrpc.Server, project, conflictID, disablePolicyTarget string) skillResolutionPreviewResult {
	t.Helper()
	raw, err := server.Dispatch(context.Background(), "skills/resolution_preview", json.RawMessage(`{"cwd":"`+project+`","conflict_id":"`+conflictID+`","name":"same","scope":"project","action":"`+ResolutionDisablePersonalForProject+`","disable_policy_target":"`+disablePolicyTarget+`"}`))
	if err != nil {
		t.Fatalf("Dispatch disable_personal_for_project preview with disable_policy_target: %v", err)
	}
	return unmarshalSingleResolutionPreview(t, raw, ResolutionDisablePersonalForProject)
}

func unmarshalSingleResolutionPreview(t *testing.T, raw json.RawMessage, action string) skillResolutionPreviewResult {
	t.Helper()
	var got skillResolutionPreviewResult
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("Unmarshal %s preview: %v", action, err)
	}
	if len(got.Items) != 1 {
		t.Fatalf("%s preview items = %d, want 1", action, len(got.Items))
	}
	return got
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
