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

	platformrpc "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/rpc"
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

func TestSkillResolutionApplyRPCJSONUsesCamelCase(t *testing.T) {
	project, server, _, _, svc := setupResolutionPreviewDriftFixtureWithService(t)
	svc.auditStore = &capturingSkillAuditStore{}
	item := findResolutionItem(t, dispatchResolutionList(t, server, project).Items, "mirror_drift", "drift", skillScopeProject)
	preview := dispatchResolutionPreview(t, server, project, item.ConflictID, ResolutionSyncBackCanonical)
	raw, err := dispatchResolutionApplyRaw(
		t,
		server,
		project,
		item.ConflictID,
		"drift",
		ResolutionSyncBackCanonical,
		preview.Items[0],
		"",
	)
	if err != nil {
		t.Fatalf("Dispatch resolution apply JSON: %v", err)
	}
	var payload map[string]json.RawMessage
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatalf("Unmarshal resolution apply JSON: %v", err)
	}
	wantKeys := []string{"action", "name", "resultingHash", "partialFailure", "followUpAction"}
	if len(payload) != len(wantKeys) {
		t.Fatalf("resolution apply JSON keys = %v, want exactly %v", payload, wantKeys)
	}
	for _, key := range wantKeys {
		if _, ok := payload[key]; !ok {
			t.Errorf("resolution apply JSON missing camelCase key %q: %s", key, raw)
		}
	}
	for _, key := range []string{"Action", "Name", "ResultingHash", "PartialFailure", "FollowUpAction"} {
		if _, ok := payload[key]; ok {
			t.Errorf("resolution apply JSON contains accidental PascalCase key %q: %s", key, raw)
		}
	}
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
	beforeHash := mustSkillDirContentHash(t, canonicalDir)
	item := findResolutionItem(t, dispatchResolutionList(t, server, project).Items, "mirror_drift", "drift", skillScopeProject)
	preview := dispatchResolutionPreview(t, server, project, item.ConflictID, ResolutionSyncBackCanonical)
	proof := preview.Items[0]
	proof.PreviewHash = "sha256:not-the-preview"

	_, err := dispatchResolutionApplyRaw(t, server, project, item.ConflictID, "drift", ResolutionSyncBackCanonical, proof, "")
	if err == nil || !strings.Contains(err.Error(), "preview hash mismatch") {
		t.Fatalf("resolution_apply hash mismatch error = %v, want mismatch", err)
	}
	if afterHash := mustSkillDirContentHash(t, canonicalDir); afterHash != beforeHash {
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
	assertFileContent(t, filepath.Join(project, ".agents", "skills", "deleted", "references", "guide.md"), "project drift\n")
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
	mirrorRoot := providerProjectMirrorRoot(SkillProviderClaude, project)
	if _, err := os.Stat(filepath.Join(mirrorRoot, "deleted")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("deleted mirror stat error = %v, want not exist", err)
	}
	manifest, err := readSkillMirrorManifest(filepath.Join(mirrorRoot, skillMirrorManifestFile))
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
	assertFileContent(t, filepath.Join(project, ".agents", "skills", "deleted-copy", "references", "guide.md"), "project drift\n")
	mirrorRoot := providerProjectMirrorRoot(SkillProviderClaude, project)
	if _, err := os.Stat(filepath.Join(mirrorRoot, "deleted")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("original deleted mirror stat error = %v, want not exist", err)
	}
	manifest, err := readSkillMirrorManifest(filepath.Join(mirrorRoot, skillMirrorManifestFile))
	if err != nil {
		t.Fatalf("read mirror manifest: %v", err)
	}
	if _, ok := manifest.Skills["deleted"]; ok {
		t.Fatalf("deleted mirror still present in manifest after save-as-new: %+v", manifest.Skills)
	}
}

func TestSkillResolutionApplyRPCImportsUnmanagedProviderSkill(t *testing.T) {
	project, server, svc := setupOrphanUnmanagedProviderConflictFixtureWithService(t)
	svc.auditStore = &capturingSkillAuditStore{}
	item := findResolutionItem(t, dispatchResolutionList(t, server, project).Items, "unmanaged_provider_skill", "scratch", skillScopeProject)
	preview := dispatchResolutionPreviewProviderNamed(t, server, project, item.ConflictID, "scratch", "claude", ResolutionImportPersonal)

	report := dispatchResolutionApply(t, server, project, item.ConflictID, "scratch", ResolutionImportPersonal, preview.Items[0], "")

	if report.ResultingHash == "" {
		t.Fatalf("resolution_apply import report = %+v, want resulting hash", report)
	}
	assertFileContent(t, filepath.Join(svc.resolvedSuperDolphinHome(), "skills", "personal", personalSkillTypeImported, "scratch", skillMainFile), "---\nname: scratch\n---\n# scratch\n")
	assertFileContent(t, filepath.Join(providerPersonalMirrorRoot(SkillProviderClaude), "scratch", skillMainFile), "---\nname: scratch\n---\n# scratch\n")
	assertFileContent(t, filepath.Join(providerPersonalMirrorRoot(SkillProviderCodex), "scratch", skillMainFile), "---\nname: scratch\n---\n# scratch\n")
}

func TestSkillResolutionApplyImportsLegacyDisplayNameProviderSkill(t *testing.T) {
	project := t.TempDir()
	providerRoot := testCodexProjectMirrorRoot(project)
	displayName := "Docker 容器化部署"
	canonicalName := "docker-容器化部署"
	unmanagedDir := filepath.Join(providerRoot, displayName)
	writeSkillContent(t, unmanagedDir, displayName, "# docker\n")
	previewHash, err := stableMirrorDirectoryHash(unmanagedDir)
	if err != nil {
		t.Fatalf("stableMirrorDirectoryHash: %v", err)
	}
	svc := &service{projectRoot: project, projectSkillsRoot: defaultProjectSkillsRoot(project), superDolphinHome: filepath.Join(t.TempDir(), ".super-dolphin"), auditStore: &capturingSkillAuditStore{}}
	target := SkillMirrorTarget{TargetID: "codex:project:repo", Provider: SkillProviderCodex, Scope: skillScopeProject, Root: providerRoot, CanonicalRootID: "repo"}

	report, err := ImportUnmanagedProviderSkill(context.Background(), svc, SkillMirrorResolutionRequest{
		Action:      "import_to_project",
		Name:        displayName,
		Target:      target,
		PreviewHash: previewHash,
	})
	if err != nil {
		t.Fatalf("ImportUnmanagedProviderSkill legacy display name: %v", err)
	}
	if report.Name != canonicalName {
		t.Fatalf("report name = %q, want canonical name %q", report.Name, canonicalName)
	}
	assertFileContent(t, filepath.Join(project, ".agents", "skills", canonicalName, skillMainFile), "---\nname: "+canonicalName+"\ndisplay_name: \"Docker 容器化部署\"\n---\n# docker\n")
}

func TestSkillResolutionApplyRPCTakesOverUnmanagedProviderSkill(t *testing.T) {
	project, server, svc := setupOrphanUnmanagedProviderConflictFixtureWithService(t)
	svc.auditStore = &capturingSkillAuditStore{}
	item := findResolutionItem(t, dispatchResolutionList(t, server, project).Items, "unmanaged_provider_skill", "scratch", skillScopeProject)
	preview := dispatchResolutionPreviewProviderNamed(t, server, project, item.ConflictID, "scratch", "claude", ResolutionTakeoverProvider)

	report := dispatchResolutionApply(t, server, project, item.ConflictID, "scratch", ResolutionTakeoverProvider, preview.Items[0], "")

	if report.ResultingHash == "" {
		t.Fatalf("resolution_apply takeover report = %+v, want resulting hash", report)
	}
	manifest, err := readSkillMirrorManifest(filepath.Join(project, ".claude", "skills", skillMirrorManifestFile))
	if err != nil {
		t.Fatalf("read takeover manifest: %v", err)
	}
	mirrorHash, err := stableMirrorDirectoryHash(filepath.Join(project, ".claude", "skills", "scratch"))
	if err != nil {
		t.Fatalf("stableMirrorDirectoryHash takeover mirror: %v", err)
	}
	if entry := manifest.Skills["scratch"]; !entry.Owned || entry.CanonicalHash != report.ResultingHash || entry.MirrorHash != mirrorHash {
		t.Fatalf("takeover manifest entry = %+v, want canonical=%s mirror=%s", entry, report.ResultingHash, mirrorHash)
	}
	assertFileContent(t, filepath.Join(project, ".agents", "skills", "scratch", skillMainFile), "---\nname: scratch\n---\n# scratch\n")
}

func TestSkillResolutionApplyRPCReplacesProviderRootSymlink(t *testing.T) {
	project, server, claudeRoot, legacyCache, audit := setupProviderRootSymlinkResolutionFixture(t)
	item := findResolutionItem(t, dispatchResolutionList(t, server, project).Items, "mirror_root_symlink", "Claude 项目技能目录", skillScopeProject)
	preview := dispatchResolutionPreviewProviderNamed(t, server, project, item.ConflictID, item.Name, "claude", ResolutionReplaceProviderRootSymlink)
	if preview.Items[0].PreviewID == "" || preview.Items[0].PreviewHash == "" {
		t.Fatalf("root symlink preview missing proof: %+v", preview.Items[0])
	}

	report := dispatchResolutionApply(t, server, project, item.ConflictID, item.Name, ResolutionReplaceProviderRootSymlink, preview.Items[0], "")

	assertProviderRootSymlinkReplaced(t, report, claudeRoot, legacyCache)
	assertSkillMutationAuditActions(t, audit.inserts, ResolutionReplaceProviderRootSymlink+"_intent", ResolutionReplaceProviderRootSymlink+"_finalize")
}

func TestSkillResolutionListRPCReportsProviderManifestTargetMismatch(t *testing.T) {
	project, server, claudeRoot, audit := setupProviderManifestMismatchResolutionFixture(t)
	list := dispatchResolutionList(t, server, project)

	for _, item := range list.Items {
		if item.Kind == "mirror_manifest_target_mismatch" {
			t.Fatalf("resolution_list exposed internal manifest mismatch item: %+v", item)
		}
	}
	item := findResolutionItem(t, list.Items, "unmanaged_same_name", "build", skillScopeProject)
	assertResolutionActions(t, item, ResolutionViewDiff, ResolutionSyncBackCanonical, ResolutionCanonicalOverwrite, ResolutionSaveAsNewSkill)
	manifest, err := readSkillMirrorManifest(filepath.Join(claudeRoot, skillMirrorManifestFile))
	if err != nil {
		t.Fatalf("read claude root manifest: %v", err)
	}
	if manifest.Provider != string(SkillProviderClaude) || manifest.Scope != skillScopePersonal || manifest.CanonicalRootID != "sd_owner:old" {
		t.Fatalf("manifest target = provider:%q scope:%q root:%q, want original mismatched manifest", manifest.Provider, manifest.Scope, manifest.CanonicalRootID)
	}
	if entry, ok := manifest.Skills["build"]; ok && entry.Owned {
		t.Fatalf("manifest build entry = %+v, want project mismatch not owned", entry)
	}
	assertFileContent(t, filepath.Join(claudeRoot, "build", skillMainFile), "---\nname: build\n---\n# build\n")
	assertSkillMutationAuditActions(t, audit.inserts)
}

func setupOrphanUnmanagedProviderConflictFixtureWithService(t *testing.T) (string, *platformrpc.Server, *service) {
	t.Helper()
	setSkillTestUserHome(t)
	project := t.TempDir()
	superHome := filepath.Join(t.TempDir(), ".super-dolphin")
	svc := &service{root: t.TempDir(), projectRoot: project, projectSkillsRoot: defaultProjectSkillsRoot(project), superDolphinHome: superHome, http: &http.Client{}}
	writeSkillWithSupportFiles(t, filepath.Join(project, ".claude", "skills", "scratch"), "scratch")
	return project, newSkillRPCTestServer(t, svc), svc
}

func setupProviderManifestMismatchResolutionFixture(t *testing.T) (string, *platformrpc.Server, string, *capturingSkillAuditStore) {
	t.Helper()
	project, server, claudeRoot, audit, _ := setupProviderManifestMismatchResolutionFixtureWithService(t)
	return project, server, claudeRoot, audit
}

func setupProviderManifestMismatchResolutionFixtureWithService(t *testing.T) (string, *platformrpc.Server, string, *capturingSkillAuditStore, *service) {
	t.Helper()
	setSkillTestUserHome(t)
	project := t.TempDir()
	superHome := filepath.Join(t.TempDir(), ".super-dolphin")
	audit := &capturingSkillAuditStore{}
	svc := &service{
		projectRoot:       project,
		projectSkillsRoot: defaultProjectSkillsRoot(project),
		superDolphinHome:  superHome,
		http:              &http.Client{},
		auditStore:        audit,
	}
	writeSkillWithSupportFiles(t, filepath.Join(project, ".agents", "skills", "build"), "build")
	claudeRoot := providerProjectMirrorRoot(SkillProviderClaude, project)
	if err := os.MkdirAll(claudeRoot, 0o755); err != nil {
		t.Fatalf("MkdirAll claude root: %v", err)
	}
	if _, err := replaceMirrorSkillDir(claudeRoot, "build", filepath.Join(project, ".agents", "skills", "build"), skillScopeProject); err != nil {
		t.Fatalf("replaceMirrorSkillDir build: %v", err)
	}
	if err := writeSkillMirrorManifest(filepath.Join(claudeRoot, skillMirrorManifestFile), SkillMirrorManifest{
		Version:         1,
		Manager:         "super-dolphin",
		Scope:           skillScopePersonal,
		Provider:        string(SkillProviderClaude),
		CanonicalRootID: "sd_owner:old",
		Skills:          map[string]SkillMirrorEntry{},
	}); err != nil {
		t.Fatalf("write mismatched manifest: %v", err)
	}
	return project, newSkillRPCTestServer(t, svc), claudeRoot, audit, svc
}

func setupProviderRootSymlinkResolutionFixture(t *testing.T) (string, *platformrpc.Server, string, string, *capturingSkillAuditStore) {
	t.Helper()
	setSkillTestUserHome(t)
	project := t.TempDir()
	superHome := filepath.Join(t.TempDir(), ".super-dolphin")
	audit := &capturingSkillAuditStore{}
	svc := &service{
		projectRoot:       project,
		projectSkillsRoot: defaultProjectSkillsRoot(project),
		superDolphinHome:  superHome,
		http:              &http.Client{},
		auditStore:        audit,
	}
	writeSkillWithSupportFiles(t, filepath.Join(project, ".agents", "skills", "build"), "build")
	legacyCache := filepath.Join(t.TempDir(), "skills-cache")
	writeSkillWithSupportFiles(t, filepath.Join(legacyCache, "legacy"), "legacy")
	claudeRoot := providerProjectMirrorRoot(SkillProviderClaude, project)
	if err := os.MkdirAll(filepath.Dir(claudeRoot), 0o755); err != nil {
		t.Fatalf("MkdirAll claude root parent: %v", err)
	}
	if err := os.Symlink(legacyCache, claudeRoot); err != nil {
		skipIfSymlinkPrivilegeNotHeld(t, err)
		t.Fatalf("Symlink legacy root: %v", err)
	}
	return project, newSkillRPCTestServer(t, svc), claudeRoot, legacyCache, audit
}

func assertProviderRootSymlinkReplaced(t *testing.T, report SkillMirrorResolutionReport, claudeRoot, legacyCache string) {
	t.Helper()
	if report.Action != ResolutionReplaceProviderRootSymlink || report.ResultingHash == "" {
		t.Fatalf("resolution_apply root symlink report = %+v, want action/resulting hash", report)
	}
	info, err := os.Lstat(claudeRoot)
	if err != nil {
		t.Fatalf("Lstat claude root: %v", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		t.Fatalf("claude root mode = %s, want real directory", info.Mode())
	}
	assertFileContent(t, filepath.Join(claudeRoot, "build", skillMainFile), "---\nname: build\n---\n# build\n")
	assertFileContent(t, filepath.Join(legacyCache, "legacy", skillMainFile), "---\nname: legacy\n---\n# legacy\n")
	manifest, err := readSkillMirrorManifest(filepath.Join(claudeRoot, skillMirrorManifestFile))
	if err != nil {
		t.Fatalf("read claude root manifest: %v", err)
	}
	if entry := manifest.Skills["build"]; !entry.Owned || entry.CanonicalID != "project/build" {
		t.Fatalf("manifest build entry = %+v, want owned project/build", entry)
	}
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
	assertFileContent(t, filepath.Join(project, ".agents", "skills", "drift-copy", "references", "guide.md"), "project drift\n")
	assertFileContent(t, filepath.Join(mirrorDir, "references", "guide.md"), "guide\n")
	manifest, err := readSkillMirrorManifest(filepath.Join(providerProjectMirrorRoot(SkillProviderClaude, project), skillMirrorManifestFile))
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
	target := SkillMirrorTarget{TargetID: "claude:project:" + RepoFingerprint(project), Provider: SkillProviderClaude, Scope: skillScopeProject, Root: providerProjectMirrorRoot(SkillProviderClaude, project), CanonicalRootID: RepoFingerprint(project)}
	conflicts, err := DetectSkillMirrorConflicts(records, []SkillMirrorTarget{target})
	if err != nil {
		t.Fatalf("DetectSkillMirrorConflicts: %v", err)
	}
	if len(conflicts) != 0 {
		t.Fatalf("conflicts after save-as-new = %+v, want none; canonical=%s", conflicts, canonicalDir)
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
		"provider":     "claude",
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
