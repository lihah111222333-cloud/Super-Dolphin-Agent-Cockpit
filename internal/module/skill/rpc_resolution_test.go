package skill

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	platformrpc "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/rpc"
)

func TestSkillResolutionListRPCReturnsCanonicalAndMirrorConflicts(t *testing.T) {
	project, server := setupResolutionListConflictFixture(t)
	got := dispatchResolutionList(t, server, project)

	assertResolutionListConflictItems(t, got.Items)
}

func TestSkillResolutionListHidesMirrorDriftForUnresolvedSameName(t *testing.T) {
	setSkillTestUserHome(t)
	project := t.TempDir()
	superHome := filepath.Join(t.TempDir(), ".super-dolphin")
	svc := &service{
		root:              t.TempDir(),
		projectRoot:       project,
		projectSkillsRoot: defaultProjectSkillsRoot(project),
		superDolphinHome:  superHome,
		http:              &http.Client{},
	}
	writeSkillWithSupportFiles(t, filepath.Join(project, ".agents", "skills", "build"), "build")
	writeSkillWithSupportFiles(t, filepath.Join(superHome, "skills", "personal", personalSkillTypeUser, "build"), "build")

	records, err := newCanonicalStore(superHome).scan(project)
	if err != nil {
		t.Fatalf("scan canonical records: %v", err)
	}
	fingerprint := RepoFingerprint(project)
	claudeTarget := SkillMirrorTarget{TargetID: "claude:project:" + fingerprint, Provider: SkillProviderClaude, Scope: skillScopeProject, Root: providerProjectMirrorRoot(SkillProviderClaude, project), CanonicalRootID: fingerprint}
	if _, err := PublishSkillMirrors(context.Background(), records, []SkillMirrorTarget{claudeTarget}); err != nil {
		t.Fatalf("PublishSkillMirrors: %v", err)
	}
	writeFileWithMode(t, filepath.Join(claudeTarget.Root, "build", "references", "guide.md"), "claude project drift\n", 0o644)

	got := dispatchResolutionList(t, newSkillRPCTestServer(t, svc), project)
	same := findResolutionItem(t, got.Items, skillConflictSameName, "build", "")
	assertResolutionActions(t, same, ResolutionViewDiff, ResolutionKeepSelected, ResolutionRenamePersonal)
	assertResolutionSource(t, same, skillScopeProject, "", "project/build")
	assertResolutionSource(t, same, skillScopePersonal, personalSkillTypeUser, "personal/user/build")
	assertResolutionItemAbsent(t, got.Items, skillConflictMirrorDrift, "build", skillScopeProject)
	assertResolutionItemAbsent(t, got.Items, skillConflictMultiMirrorDrift, "build", skillScopeProject)
	assertResolutionItemAbsent(t, got.Items, skillConflictCanonicalDeletedWithDrift, "build", skillScopeProject)
}

func TestSkillResolutionPreviewRPCBindsMutatingPreviewAndDoesNotWrite(t *testing.T) {
	project, server, canonicalDir, mirrorDir := setupResolutionPreviewDriftFixture(t)
	beforeCanonical, beforeMirror := resolutionPreviewHashes(t, canonicalDir, mirrorDir)
	list := dispatchResolutionList(t, server, project)
	item := findResolutionItem(t, list.Items, "mirror_drift", "drift", skillScopeProject)

	view := dispatchResolutionPreview(t, server, project, item.ConflictID, ResolutionViewDiff)
	assertReadOnlyDiffPreview(t, view)
	syncPreview := dispatchResolutionPreview(t, server, project, item.ConflictID, ResolutionSyncBackCanonical)
	syncItem := syncPreview.Items[0]
	assertMutatingPreviewProof(t, syncItem, beforeMirror, beforeCanonical)
	overwritePreview := dispatchResolutionPreview(t, server, project, item.ConflictID, ResolutionCanonicalOverwrite)
	if overwritePreview.Items[0].PreviewHash == syncItem.PreviewHash {
		t.Fatalf("preview hash did not bind action: sync=%s overwrite=%s", syncItem.PreviewHash, overwritePreview.Items[0].PreviewHash)
	}
	aliasPreview := dispatchResolutionPreview(t, server, project, item.ConflictID, ResolutionCanonicalOverwrites)
	if aliasPreview.Items[0].Action != ResolutionCanonicalOverwrite {
		t.Fatalf("canonical overwrite alias action = %q, want %q", aliasPreview.Items[0].Action, ResolutionCanonicalOverwrite)
	}
	assertResolutionPreviewNoWrite(t, canonicalDir, mirrorDir, beforeCanonical, beforeMirror)
}

func TestSkillResolutionListUsesRealProjectMirrorRootForSymlinkCWD(t *testing.T) {
	realProject, server, _, _ := setupResolutionPreviewDriftFixture(t)
	aliasProject := filepath.Join(t.TempDir(), "alias-project")
	if err := os.Symlink(realProject, aliasProject); err != nil {
		skipIfSymlinkPrivilegeNotHeld(t, err)
		t.Fatalf("Symlink project: %v", err)
	}

	list := dispatchResolutionList(t, server, aliasProject)
	item := findResolutionItem(t, list.Items, "mirror_drift", "drift", skillScopeProject)

	if len(item.ProviderEntries) == 0 {
		t.Fatalf("symlink cwd resolution item missing provider entries: %+v", item)
	}
	if !strings.Contains(item.ProviderEntries[0].SourcePath, realProject) {
		t.Fatalf("source_path = %q, want real project root %q", item.ProviderEntries[0].SourcePath, realProject)
	}
}

func TestSkillResolutionListReportsLegacyMirrorRootSymlinkAsConflict(t *testing.T) {
	setSkillTestUserHome(t)
	project := t.TempDir()
	superHome := filepath.Join(t.TempDir(), ".super-dolphin")
	svc := &service{projectRoot: project, projectSkillsRoot: defaultProjectSkillsRoot(project), superDolphinHome: superHome, http: &http.Client{}}
	writeSkillWithSupportFiles(t, filepath.Join(project, ".agents", "skills", "build"), "build")
	legacyCache := filepath.Join(t.TempDir(), "skills-cache")
	if err := os.MkdirAll(legacyCache, 0o755); err != nil {
		t.Fatalf("MkdirAll legacy cache: %v", err)
	}
	claudeRoot := providerProjectMirrorRoot(SkillProviderClaude, project)
	if err := os.MkdirAll(filepath.Dir(claudeRoot), 0o755); err != nil {
		t.Fatalf("MkdirAll claude root parent: %v", err)
	}
	if err := os.Symlink(legacyCache, claudeRoot); err != nil {
		skipIfSymlinkPrivilegeNotHeld(t, err)
		t.Fatalf("Symlink legacy root: %v", err)
	}

	got := dispatchResolutionList(t, newSkillRPCTestServer(t, svc), project)
	item := findResolutionItemByKindAndScope(t, got.Items, "mirror_root_symlink", skillScopeProject)

	assertResolutionActions(t, item, ResolutionViewUnmanaged, "replace_provider_root_symlink")
	if len(item.ProviderEntries) != 1 {
		t.Fatalf("provider entries = %+v, want one root symlink entry", item.ProviderEntries)
	}
	if item.ProviderEntries[0].Provider != string(SkillProviderClaude) || item.ProviderEntries[0].SourceHash == "" {
		t.Fatalf("provider entry = %+v, want claude with source hash", item.ProviderEntries[0])
	}
	if !sameCleanPath(filepath.FromSlash(item.ProviderEntries[0].SourcePath), claudeRoot) {
		t.Fatalf("source_path = %q, want symlink root %q", item.ProviderEntries[0].SourcePath, claudeRoot)
	}
	if !sameCleanPath(filepath.FromSlash(item.ProviderEntries[0].TargetPath), claudeRoot) {
		t.Fatalf("target_path = %q, want replacement root %q", item.ProviderEntries[0].TargetPath, claudeRoot)
	}
}

func TestSkillResolutionListSupportsProjectOnlySameNameSelection(t *testing.T) {
	skipWindowsShortMirrorIntegration(t)
	setSkillTestUserHome(t)

	project := t.TempDir()
	superHome := filepath.Join(t.TempDir(), ".super-dolphin")
	svc := &service{projectRoot: project, projectSkillsRoot: defaultProjectSkillsRoot(project), superDolphinHome: superHome, http: &http.Client{}}
	writeSkillContent(t, filepath.Join(project, ".agents", "skills", "security-engineer"), "security", "# a\n")
	writeSkillContent(t, filepath.Join(project, ".agents", "skills", "security-standards"), "security", "# b\n")

	item := findResolutionItem(t, dispatchResolutionList(t, newSkillRPCTestServer(t, svc), project).Items, "same_name", "security", "")

	assertResolutionActions(t, item, ResolutionViewDiff, ResolutionKeepSelected, ResolutionRenamePersonal)
	assertResolutionSource(t, item, skillScopeProject, "", "project/security-engineer")
	assertResolutionSource(t, item, skillScopeProject, "", "project/security-standards")
}

func TestSkillResolutionPreviewIDAuthorizesSyncBackApply(t *testing.T) {
	project, server, canonicalDir, _, svc := setupResolutionPreviewDriftFixtureWithService(t)
	svc.auditStore = &capturingSkillAuditStore{}
	item := findResolutionItem(t, dispatchResolutionList(t, server, project).Items, "mirror_drift", "drift", skillScopeProject)
	preview := dispatchResolutionPreview(t, server, project, item.ConflictID, ResolutionSyncBackCanonical)
	proof := preview.Items[0]

	_, err := ResolveSkillMirrorDrift(context.Background(), svc, SkillMirrorResolutionRequest{
		Action:      ResolutionSyncBackCanonical,
		Name:        "drift",
		Target:      SkillMirrorTarget{TargetID: "claude:project:" + RepoFingerprint(project), Provider: SkillProviderClaude, Scope: skillScopeProject, Root: providerProjectMirrorRoot(SkillProviderClaude, project), CanonicalRootID: RepoFingerprint(project)},
		PreviewID:   proof.PreviewID,
		PreviewHash: proof.PreviewHash,
	})
	if err != nil {
		t.Fatalf("ResolveSkillMirrorDrift with preview_id: %v", err)
	}
	assertFileContent(t, filepath.Join(canonicalDir, "references", "guide.md"), "project drift\n")
}

func TestSkillResolutionMirrorTargetsUseResolvedOwnerKey(t *testing.T) {
	setSkillTestUserHome(t)
	project := t.TempDir()
	superHome := filepath.Join(t.TempDir(), ".super-dolphin")
	svc := &service{projectRoot: project, projectSkillsRoot: defaultProjectSkillsRoot(project), superDolphinHome: superHome}
	owner, err := resolveOwnerIdentity(superHome, defaultOwnerOSUID(), defaultAppProfile())
	if err != nil {
		t.Fatalf("resolveOwnerIdentity: %v", err)
	}

	targets := svc.resolutionMirrorTargets(project)
	assertResolutionMirrorTarget(t, targets, "claude:user-global:"+owner.OwnerKey, owner.OwnerKey)
	assertResolutionMirrorTarget(t, targets, "codex:user-global:"+owner.OwnerKey, owner.OwnerKey)
}

func TestSkillResolutionPreviewRequiresNewNameForSaveAsNew(t *testing.T) {
	project, server, _, _ := setupResolutionPreviewDriftFixture(t)
	item := findResolutionItem(t, dispatchResolutionList(t, server, project).Items, "mirror_drift", "drift", skillScopeProject)

	_, err := server.Dispatch(context.Background(), "skills/resolution_preview", mustRawJSON(t, map[string]any{
		"cwd":         project,
		"conflict_id": item.ConflictID,
		"name":        "drift",
		"scope":       "project",
		"provider":    "claude",
		"action":      "save_as_new_skill",
	}))
	if err == nil || !strings.Contains(err.Error(), "new_name") {
		t.Fatalf("save_as_new without new_name error = %v, want new_name rejection", err)
	}
	preview := dispatchResolutionPreviewWithName(t, server, project, item.ConflictID, ResolutionSaveAsNewSkill, "drift-copy")
	if !strings.HasSuffix(preview.Items[0].TargetPath, "/drift-copy") {
		t.Fatalf("save_as_new target_path = %q, want drift-copy target", preview.Items[0].TargetPath)
	}
}

func TestSkillResolutionPreviewCanonicalDeletedWithDriftHasTargetPath(t *testing.T) {
	project, server := setupCanonicalDeletedDriftFixture(t)
	item := findResolutionItem(t, dispatchResolutionList(t, server, project).Items, "canonical_deleted_with_drift", "deleted", skillScopeProject)

	preview := dispatchResolutionPreview(t, server, project, item.ConflictID, ResolutionSyncBackCanonical)
	if preview.Items[0].SourcePath == "" || preview.Items[0].TargetPath == "" || preview.Items[0].SourceHash == "" {
		t.Fatalf("deleted drift preview missing source/target binding: %+v", preview.Items[0])
	}
	if !strings.HasSuffix(preview.Items[0].TargetPath, "/.agents/skills/deleted") {
		t.Fatalf("deleted drift target_path = %q, want canonical restore path", preview.Items[0].TargetPath)
	}
	assertResolutionActions(t, item, ResolutionViewDiff, ResolutionSaveAsNewSkill, ResolutionSyncBackCanonical, ResolutionConfirmDeleteDriftedMirror)
	deletePreview := dispatchResolutionPreview(t, server, project, item.ConflictID, ResolutionConfirmDeleteDriftedMirror)
	if deletePreview.Items[0].TargetPath != deletePreview.Items[0].SourcePath || deletePreview.Items[0].ConfirmDeleteMirrorHash == "" {
		t.Fatalf("confirm-delete preview must bind drifted mirror path/hash: %+v", deletePreview.Items[0])
	}
	if strings.Contains(deletePreview.Items[0].BackupPath, "/.claude/skills/.super-dolphin-mirror-backup/") ||
		!strings.Contains(deletePreview.Items[0].BackupPath, "/.claude/.super-dolphin-mirror-backup/skills/") {
		t.Fatalf("confirm-delete backup_path = %q, want provider backup outside skills root", deletePreview.Items[0].BackupPath)
	}
}

func TestSkillResolutionPreviewSingleMirrorDoesNotRequireSourceProvider(t *testing.T) {
	project, server := setupMultiMirrorDriftFixture(t)
	item := findResolutionItem(t, dispatchResolutionList(t, server, project).Items, "mirror_drift", "multi", skillScopeProject)

	raw, err := server.Dispatch(context.Background(), "skills/resolution_preview", mustRawJSON(t, map[string]any{
		"cwd":         project,
		"conflict_id": item.ConflictID,
		"name":        "multi",
		"scope":       "project",
		"action":      "sync_back_to_canonical",
	}))
	if err != nil {
		t.Fatalf("single mirror sync preview: %v", err)
	}
	var preview skillResolutionPreviewResult
	if err := json.Unmarshal(raw, &preview); err != nil {
		t.Fatalf("Unmarshal preview: %v", err)
	}
	if preview.Items[0].SourceProvider != "claude" {
		t.Fatalf("source_provider = %q, want claude", preview.Items[0].SourceProvider)
	}
	_, err = server.Dispatch(context.Background(), "skills/resolution_preview", mustRawJSON(t, map[string]any{
		"cwd":             project,
		"conflict_id":     item.ConflictID,
		"name":            "multi",
		"scope":           "project",
		"source_provider": "claude",
		"source_path_id":  "provider:codex",
		"action":          "sync_back_to_canonical",
	}))
	if err == nil || !strings.Contains(err.Error(), "source_provider") {
		t.Fatalf("mismatched source_provider/source_path_id error = %v, want rejection", err)
	}
	assertSingleMirrorViewDiffPreviewsProvider(t, server, project, item.ConflictID)
}

func assertSingleMirrorViewDiffPreviewsProvider(t *testing.T, server *platformrpc.Server, project, conflictID string) {
	t.Helper()
	viewRaw, err := server.Dispatch(context.Background(), "skills/resolution_preview", mustRawJSON(t, map[string]any{
		"cwd":         project,
		"conflict_id": conflictID,
		"name":        "multi",
		"scope":       "project",
		"action":      "view_diff",
	}))
	if err != nil {
		t.Fatalf("mirror view_diff preview: %v", err)
	}
	var view skillResolutionPreviewResult
	if err := json.Unmarshal(viewRaw, &view); err != nil {
		t.Fatalf("Unmarshal mirror view_diff preview: %v", err)
	}
	if len(view.Items) != 1 || view.Items[0].Provider != string(SkillProviderClaude) {
		t.Fatalf("view_diff items = %+v, want Claude provider preview", view.Items)
	}
}

func TestSkillResolutionPreviewIDIsStoredServerSide(t *testing.T) {
	project, server, _, _, svc := setupResolutionPreviewDriftFixtureWithService(t)
	item := findResolutionItem(t, dispatchResolutionList(t, server, project).Items, "mirror_drift", "drift", skillScopeProject)

	first := dispatchResolutionPreview(t, server, project, item.ConflictID, ResolutionSyncBackCanonical)
	second := dispatchResolutionPreview(t, server, project, item.ConflictID, ResolutionSyncBackCanonical)
	if first.Items[0].PreviewID == "" || first.Items[0].PreviewID == second.Items[0].PreviewID {
		t.Fatalf("preview ids = %q and %q, want non-empty unique server-side ids", first.Items[0].PreviewID, second.Items[0].PreviewID)
	}
	svc.resolutionPreviewMu.Lock()
	defer svc.resolutionPreviewMu.Unlock()
	if len(svc.resolutionPreviews) < 2 {
		t.Fatalf("stored previews = %d, want at least two", len(svc.resolutionPreviews))
	}
}

func TestSkillResolutionPreviewImportPersonalTargetsImportedCanonical(t *testing.T) {
	project, server := setupOrphanUnmanagedProviderConflictFixture(t)
	item := findResolutionItem(t, dispatchResolutionList(t, server, project).Items, "unmanaged_provider_skill", "scratch", skillScopeProject)

	raw, err := server.Dispatch(context.Background(), "skills/resolution_preview", mustRawJSON(t, map[string]any{
		"cwd":         project,
		"conflict_id": item.ConflictID,
		"name":        "scratch",
		"scope":       "project",
		"provider":    "claude",
		"action":      "import_to_personal_imported",
	}))
	if err != nil {
		t.Fatalf("import_to_personal_imported preview: %v", err)
	}
	var preview skillResolutionPreviewResult
	if err := json.Unmarshal(raw, &preview); err != nil {
		t.Fatalf("Unmarshal import preview: %v", err)
	}
	if !strings.Contains(preview.Items[0].TargetPath, "/.super-dolphin/skills/personal/imported/scratch") {
		t.Fatalf("import personal target_path = %q, want imported personal canonical", preview.Items[0].TargetPath)
	}
}

func TestSkillResolutionPreviewViewUnmanagedPreviewsAllProviders(t *testing.T) {
	project, server := setupMultiProviderUnmanagedFixture(t)
	item := findResolutionItem(t, dispatchResolutionList(t, server, project).Items, "unmanaged_provider_skill", "scratch", skillScopeProject)
	if len(item.ProviderEntries) != 1 || item.ProviderEntries[0].Provider != string(SkillProviderClaude) {
		t.Fatalf("provider entries = %+v, want Claude project mirror", item.ProviderEntries)
	}

	raw, err := server.Dispatch(context.Background(), "skills/resolution_preview", mustRawJSON(t, map[string]any{
		"cwd":         project,
		"conflict_id": item.ConflictID,
		"name":        "scratch",
		"scope":       "project",
		"action":      "view_unmanaged",
	}))
	if err != nil {
		t.Fatalf("view_unmanaged preview: %v", err)
	}
	var preview skillResolutionPreviewResult
	if err := json.Unmarshal(raw, &preview); err != nil {
		t.Fatalf("Unmarshal view unmanaged preview: %v", err)
	}
	if len(preview.Items) != 1 {
		t.Fatalf("view_unmanaged items = %+v, want one provider preview", preview.Items)
	}
	assertPreviewProviders(t, preview.Items, string(SkillProviderClaude))
}
