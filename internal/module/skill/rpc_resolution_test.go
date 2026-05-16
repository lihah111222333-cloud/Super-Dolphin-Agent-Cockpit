package skill

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	platformrpc "github.com/anthropic-ai/super-agent-v3/internal/platform/rpc"
)

func TestSkillResolutionListRPCReturnsCanonicalAndMirrorConflicts(t *testing.T) {
	project := t.TempDir()
	superHome := filepath.Join(t.TempDir(), ".super-dolphin")
	svc := &service{
		root:              t.TempDir(),
		projectRoot:       project,
		projectSkillsRoot: defaultProjectSkillsRoot(project),
		superDolphinHome:  superHome,
		http:              &http.Client{},
	}
	writeSkillWithSupportFiles(t, filepath.Join(project, ".agent", "skills", "drift"), "drift")
	writeSkillWithSupportFiles(t, filepath.Join(project, ".agent", "skills", "scratch"), "scratch")
	writeSkillWithSupportFiles(t, filepath.Join(project, ".agent", "skills", "same"), "same")
	writeSkillWithSupportFiles(t, filepath.Join(superHome, "skills", "personal", "user", "same"), "same")
	writeSkillWithSupportFiles(t, filepath.Join(superHome, "skills", "personal", "user", "notes"), "notes")

	records, err := newCanonicalStore(superHome).scan(project)
	if err != nil {
		t.Fatalf("scan canonical records: %v", err)
	}
	fingerprint := RepoFingerprint(project)
	owner, err := resolveOwnerIdentity(superHome, defaultOwnerOSUID(), defaultAppProfile())
	if err != nil {
		t.Fatalf("resolveOwnerIdentity: %v", err)
	}
	projectTarget := SkillMirrorTarget{TargetID: "codex:project:" + fingerprint, Provider: SkillProviderCodex, Scope: skillScopeProject, Root: filepath.Join(project, ".codex", "skills"), CanonicalRootID: fingerprint}
	personalTarget := SkillMirrorTarget{TargetID: "claude:app-managed:" + owner.OwnerKey, Provider: SkillProviderClaude, Scope: skillScopePersonal, Root: filepath.Join(superHome, "providers", "claude", "skills"), CanonicalRootID: owner.OwnerKey}
	if _, err := PublishSkillMirrors(context.Background(), records, []SkillMirrorTarget{projectTarget, personalTarget}); err != nil {
		t.Fatalf("PublishSkillMirrors: %v", err)
	}
	writeFileWithMode(t, filepath.Join(projectTarget.Root, "drift", "references", "guide.md"), "project drift\n", 0o644)
	writeFileWithMode(t, filepath.Join(personalTarget.Root, "notes", "references", "guide.md"), "personal drift\n", 0o644)
	writeSkillWithSupportFiles(t, filepath.Join(project, ".claude", "skills", "scratch"), "scratch")

	server := newSkillRPCTestServer(t, svc)
	raw, err := server.Dispatch(context.Background(), "skills/resolution_list", json.RawMessage(`{"cwd":"`+project+`"}`))
	if err != nil {
		t.Fatalf("Dispatch resolution_list: %v", err)
	}
	var got skillResolutionListResult
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("Unmarshal resolution_list: %v", err)
	}

	same := findResolutionItem(t, got.Items, "same_name", "same", "")
	assertResolutionActions(t, same, ResolutionViewDiff, ResolutionRenamePersonal, ResolutionDisablePersonalForProject)
	assertResolutionSource(t, same, skillScopeProject, "", "project/same")
	assertResolutionSource(t, same, skillScopePersonal, personalSkillTypeUser, "personal/user/same")

	projectDrift := findResolutionItem(t, got.Items, "mirror_drift", "drift", skillScopeProject)
	assertResolutionActions(t, projectDrift, ResolutionViewDiff, ResolutionSaveAsNewSkill, ResolutionSyncBackCanonical, ResolutionCanonicalOverwrite)
	assertResolutionProviderEntry(t, projectDrift, string(SkillProviderCodex), "drift")

	personalDrift := findResolutionItem(t, got.Items, "mirror_drift", "notes", skillScopePersonal)
	if personalDrift.PersonalType != personalSkillTypeUser {
		t.Fatalf("personal drift personal_type = %q, want user; item=%+v", personalDrift.PersonalType, personalDrift)
	}
	assertResolutionActions(t, personalDrift, ResolutionViewDiff, ResolutionSaveAsNewPersonal, ResolutionSyncBackPersonal, ResolutionPersonalOverwrite)
	assertResolutionProviderEntry(t, personalDrift, string(SkillProviderClaude), "notes")

	unmanaged := findResolutionItem(t, got.Items, "unmanaged_same_name", "scratch", skillScopeProject)
	assertResolutionActions(t, unmanaged, ResolutionViewUnmanaged, ResolutionImportPersonal, ResolutionTakeoverProvider)
	assertResolutionProviderEntry(t, unmanaged, string(SkillProviderClaude), "scratch")
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
	assertResolutionPreviewNoWrite(t, canonicalDir, mirrorDir, beforeCanonical, beforeMirror)
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
		Target:      SkillMirrorTarget{TargetID: "codex:project:" + RepoFingerprint(project), Provider: SkillProviderCodex, Scope: skillScopeProject, Root: filepath.Join(project, ".codex", "skills"), CanonicalRootID: RepoFingerprint(project)},
		PreviewID:   proof.PreviewID,
		PreviewHash: proof.PreviewHash,
	})
	if err != nil {
		t.Fatalf("ResolveSkillMirrorDrift with preview_id: %v", err)
	}
	assertFileContent(t, filepath.Join(canonicalDir, "references", "guide.md"), "project drift\n")
}

func TestSkillResolutionListReportsCanonicalOnlyChangeAsMirrorDrift(t *testing.T) {
	project := t.TempDir()
	superHome := filepath.Join(t.TempDir(), ".super-dolphin")
	svc := &service{root: t.TempDir(), projectRoot: project, projectSkillsRoot: defaultProjectSkillsRoot(project), superDolphinHome: superHome, http: &http.Client{}}
	writeSkillWithSupportFiles(t, filepath.Join(project, ".agent", "skills", "stale"), "stale")
	records, err := newCanonicalStore(superHome).scan(project)
	if err != nil {
		t.Fatalf("scan canonical records: %v", err)
	}
	target := SkillMirrorTarget{TargetID: "codex:project:" + RepoFingerprint(project), Provider: SkillProviderCodex, Scope: skillScopeProject, Root: filepath.Join(project, ".codex", "skills"), CanonicalRootID: RepoFingerprint(project)}
	if _, err := PublishSkillMirrors(context.Background(), records, []SkillMirrorTarget{target}); err != nil {
		t.Fatalf("PublishSkillMirrors: %v", err)
	}
	writeFileWithMode(t, filepath.Join(project, ".agent", "skills", "stale", "references", "guide.md"), "canonical v2\n", 0o644)

	got := dispatchResolutionList(t, newSkillRPCTestServer(t, svc), project)
	item := findResolutionItem(t, got.Items, "mirror_drift", "stale", skillScopeProject)
	assertResolutionActions(t, item, ResolutionViewDiff, ResolutionSaveAsNewSkill, ResolutionSyncBackCanonical, ResolutionCanonicalOverwrite)
}

func TestSkillResolutionMirrorTargetsUseResolvedOwnerKey(t *testing.T) {
	project := t.TempDir()
	superHome := filepath.Join(t.TempDir(), ".super-dolphin")
	svc := &service{projectRoot: project, projectSkillsRoot: defaultProjectSkillsRoot(project), superDolphinHome: superHome}
	owner, err := resolveOwnerIdentity(superHome, defaultOwnerOSUID(), defaultAppProfile())
	if err != nil {
		t.Fatalf("resolveOwnerIdentity: %v", err)
	}

	targets := svc.resolutionMirrorTargets(project)
	assertResolutionMirrorTarget(t, targets, "claude:app-managed:"+owner.OwnerKey, owner.OwnerKey)
	assertResolutionMirrorTarget(t, targets, "codex:app-managed:"+owner.OwnerKey, owner.OwnerKey)
}

func TestSkillResolutionListAggregatesMultiMirrorDrift(t *testing.T) {
	project := t.TempDir()
	superHome := filepath.Join(t.TempDir(), ".super-dolphin")
	svc := &service{root: t.TempDir(), projectRoot: project, projectSkillsRoot: defaultProjectSkillsRoot(project), superDolphinHome: superHome, http: &http.Client{}}
	writeSkillWithSupportFiles(t, filepath.Join(project, ".agent", "skills", "multi"), "multi")
	records, err := newCanonicalStore(superHome).scan(project)
	if err != nil {
		t.Fatalf("scan canonical records: %v", err)
	}
	fingerprint := RepoFingerprint(project)
	claudeTarget := SkillMirrorTarget{TargetID: "claude:project:" + fingerprint, Provider: SkillProviderClaude, Scope: skillScopeProject, Root: filepath.Join(project, ".claude", "skills"), CanonicalRootID: fingerprint}
	codexTarget := SkillMirrorTarget{TargetID: "codex:project:" + fingerprint, Provider: SkillProviderCodex, Scope: skillScopeProject, Root: filepath.Join(project, ".codex", "skills"), CanonicalRootID: fingerprint}
	if _, err := PublishSkillMirrors(context.Background(), records, []SkillMirrorTarget{claudeTarget, codexTarget}); err != nil {
		t.Fatalf("PublishSkillMirrors: %v", err)
	}
	writeFileWithMode(t, filepath.Join(claudeTarget.Root, "multi", "references", "guide.md"), "claude drift\n", 0o644)
	writeFileWithMode(t, filepath.Join(codexTarget.Root, "multi", "references", "guide.md"), "codex drift\n", 0o644)

	server := newSkillRPCTestServer(t, svc)
	got := dispatchResolutionList(t, server, project)
	item := findResolutionItem(t, got.Items, "multi_mirror_drift", "multi", skillScopeProject)
	if len(item.ProviderEntries) != 2 {
		t.Fatalf("multi provider entries = %+v, want two entries", item.ProviderEntries)
	}
	assertResolutionActions(t, item, ResolutionViewDiff, ResolutionSaveAsNewSkill, ResolutionSyncBackCanonical, ResolutionCanonicalOverwrite)
}

func TestSkillResolutionPreviewRequiresNewNameForSaveAsNew(t *testing.T) {
	project, server, _, _ := setupResolutionPreviewDriftFixture(t)
	item := findResolutionItem(t, dispatchResolutionList(t, server, project).Items, "mirror_drift", "drift", skillScopeProject)

	_, err := server.Dispatch(context.Background(), "skills/resolution_preview", json.RawMessage(`{"cwd":"`+project+`","conflict_id":"`+item.ConflictID+`","name":"drift","scope":"project","provider":"codex","action":"save_as_new_skill"}`))
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
	if !strings.HasSuffix(preview.Items[0].TargetPath, "/.agent/skills/deleted") {
		t.Fatalf("deleted drift target_path = %q, want canonical restore path", preview.Items[0].TargetPath)
	}
	assertResolutionActions(t, item, ResolutionViewDiff, ResolutionSaveAsNewSkill, ResolutionSyncBackCanonical, ResolutionConfirmDeleteDriftedMirror)
	deletePreview := dispatchResolutionPreview(t, server, project, item.ConflictID, ResolutionConfirmDeleteDriftedMirror)
	if deletePreview.Items[0].TargetPath != deletePreview.Items[0].SourcePath || deletePreview.Items[0].ConfirmDeleteMirrorHash == "" {
		t.Fatalf("confirm-delete preview must bind drifted mirror path/hash: %+v", deletePreview.Items[0])
	}
	if !strings.Contains(deletePreview.Items[0].BackupPath, "/.codex/skills/.super-dolphin-mirror-backup/") {
		t.Fatalf("confirm-delete backup_path = %q, want provider mirror backup path", deletePreview.Items[0].BackupPath)
	}
}

func TestSkillResolutionPreviewMultiMirrorRequiresSourceProvider(t *testing.T) {
	project, server := setupMultiMirrorDriftFixture(t)
	item := findResolutionItem(t, dispatchResolutionList(t, server, project).Items, "multi_mirror_drift", "multi", skillScopeProject)

	_, err := server.Dispatch(context.Background(), "skills/resolution_preview", json.RawMessage(`{"cwd":"`+project+`","conflict_id":"`+item.ConflictID+`","name":"multi","scope":"project","action":"sync_back_to_canonical"}`))
	if err == nil || !strings.Contains(err.Error(), "source_provider") {
		t.Fatalf("multi mirror sync without source_provider error = %v, want source_provider rejection", err)
	}
	raw, err := server.Dispatch(context.Background(), "skills/resolution_preview", json.RawMessage(`{"cwd":"`+project+`","conflict_id":"`+item.ConflictID+`","name":"multi","scope":"project","source_provider":"claude","action":"sync_back_to_canonical"}`))
	if err != nil {
		t.Fatalf("multi mirror sync with source_provider: %v", err)
	}
	var preview skillResolutionPreviewResult
	if err := json.Unmarshal(raw, &preview); err != nil {
		t.Fatalf("Unmarshal preview: %v", err)
	}
	if preview.Items[0].SourceProvider != "claude" {
		t.Fatalf("source_provider = %q, want claude", preview.Items[0].SourceProvider)
	}
	_, err = server.Dispatch(context.Background(), "skills/resolution_preview", json.RawMessage(`{"cwd":"`+project+`","conflict_id":"`+item.ConflictID+`","name":"multi","scope":"project","source_provider":"claude","source_path_id":"provider:codex","action":"sync_back_to_canonical"}`))
	if err == nil || !strings.Contains(err.Error(), "source_provider") {
		t.Fatalf("mismatched source_provider/source_path_id error = %v, want rejection", err)
	}
	assertMultiMirrorViewDiffPreviewsAllProviders(t, server, project, item.ConflictID)
}

func assertMultiMirrorViewDiffPreviewsAllProviders(t *testing.T, server *platformrpc.Server, project, conflictID string) {
	t.Helper()
	viewRaw, err := server.Dispatch(context.Background(), "skills/resolution_preview", json.RawMessage(`{"cwd":"`+project+`","conflict_id":"`+conflictID+`","name":"multi","scope":"project","action":"view_diff"}`))
	if err != nil {
		t.Fatalf("multi mirror view_diff preview: %v", err)
	}
	var view skillResolutionPreviewResult
	if err := json.Unmarshal(viewRaw, &view); err != nil {
		t.Fatalf("Unmarshal multi view_diff preview: %v", err)
	}
	if len(view.Items) != 2 {
		t.Fatalf("multi view_diff items = %+v, want two provider previews", view.Items)
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
	project, server := setupUnmanagedProviderConflictFixture(t)
	item := findResolutionItem(t, dispatchResolutionList(t, server, project).Items, "unmanaged_same_name", "scratch", skillScopeProject)

	raw, err := server.Dispatch(context.Background(), "skills/resolution_preview", json.RawMessage(`{"cwd":"`+project+`","conflict_id":"`+item.ConflictID+`","name":"scratch","scope":"project","provider":"claude","action":"import_to_personal_imported"}`))
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

func setupResolutionPreviewDriftFixture(t *testing.T) (string, *platformrpc.Server, string, string) {
	t.Helper()
	project, server, canonicalDir, mirrorDir, _ := setupResolutionPreviewDriftFixtureWithService(t)
	return project, server, canonicalDir, mirrorDir
}

func setupResolutionPreviewDriftFixtureWithService(t *testing.T) (string, *platformrpc.Server, string, string, *service) {
	t.Helper()
	project := t.TempDir()
	superHome := filepath.Join(t.TempDir(), ".super-dolphin")
	svc := &service{root: t.TempDir(), projectRoot: project, projectSkillsRoot: defaultProjectSkillsRoot(project), superDolphinHome: superHome, http: &http.Client{}}
	writeSkillWithSupportFiles(t, filepath.Join(project, ".agent", "skills", "drift"), "drift")
	records, err := newCanonicalStore(superHome).scan(project)
	if err != nil {
		t.Fatalf("scan canonical records: %v", err)
	}
	fingerprint := RepoFingerprint(project)
	target := SkillMirrorTarget{TargetID: "codex:project:" + fingerprint, Provider: SkillProviderCodex, Scope: skillScopeProject, Root: filepath.Join(project, ".codex", "skills"), CanonicalRootID: fingerprint}
	if _, err := PublishSkillMirrors(context.Background(), records, []SkillMirrorTarget{target}); err != nil {
		t.Fatalf("PublishSkillMirrors: %v", err)
	}
	canonicalDir := filepath.Join(project, ".agent", "skills", "drift")
	mirrorDir := filepath.Join(target.Root, "drift")
	writeFileWithMode(t, filepath.Join(mirrorDir, "references", "guide.md"), "project drift\n", 0o644)
	return project, newSkillRPCTestServer(t, svc), canonicalDir, mirrorDir, svc
}

func setupCanonicalDeletedDriftFixture(t *testing.T) (string, *platformrpc.Server) {
	t.Helper()
	project, server, canonicalDir, _ := setupResolutionPreviewDriftFixture(t)
	if err := os.RemoveAll(canonicalDir); err != nil {
		t.Fatalf("RemoveAll canonical: %v", err)
	}
	if err := os.Rename(filepath.Join(project, ".codex", "skills", "drift"), filepath.Join(project, ".codex", "skills", "deleted")); err != nil {
		t.Fatalf("rename mirror to deleted: %v", err)
	}
	manifest, err := readSkillMirrorManifest(filepath.Join(project, ".codex", "skills", skillMirrorManifestFile))
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	entry := manifest.Skills["drift"]
	entry.CanonicalID = "project/deleted"
	manifest.Skills = map[string]SkillMirrorEntry{"deleted": entry}
	if err := writeSkillMirrorManifest(filepath.Join(project, ".codex", "skills", skillMirrorManifestFile), manifest); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	return project, server
}

func assertResolutionMirrorTarget(t *testing.T, targets []SkillMirrorTarget, targetID, canonicalRootID string) {
	t.Helper()
	for _, target := range targets {
		if target.TargetID == targetID {
			if target.CanonicalRootID != canonicalRootID {
				t.Fatalf("target %s canonical_root_id = %q, want %q", targetID, target.CanonicalRootID, canonicalRootID)
			}
			return
		}
	}
	t.Fatalf("missing target_id %q in %+v", targetID, targets)
}

func setupMultiMirrorDriftFixture(t *testing.T) (string, *platformrpc.Server) {
	t.Helper()
	project := t.TempDir()
	superHome := filepath.Join(t.TempDir(), ".super-dolphin")
	svc := &service{root: t.TempDir(), projectRoot: project, projectSkillsRoot: defaultProjectSkillsRoot(project), superDolphinHome: superHome, http: &http.Client{}}
	writeSkillWithSupportFiles(t, filepath.Join(project, ".agent", "skills", "multi"), "multi")
	records, err := newCanonicalStore(superHome).scan(project)
	if err != nil {
		t.Fatalf("scan canonical records: %v", err)
	}
	fingerprint := RepoFingerprint(project)
	claudeTarget := SkillMirrorTarget{TargetID: "claude:project:" + fingerprint, Provider: SkillProviderClaude, Scope: skillScopeProject, Root: filepath.Join(project, ".claude", "skills"), CanonicalRootID: fingerprint}
	codexTarget := SkillMirrorTarget{TargetID: "codex:project:" + fingerprint, Provider: SkillProviderCodex, Scope: skillScopeProject, Root: filepath.Join(project, ".codex", "skills"), CanonicalRootID: fingerprint}
	if _, err := PublishSkillMirrors(context.Background(), records, []SkillMirrorTarget{claudeTarget, codexTarget}); err != nil {
		t.Fatalf("PublishSkillMirrors: %v", err)
	}
	writeFileWithMode(t, filepath.Join(claudeTarget.Root, "multi", "references", "guide.md"), "claude drift\n", 0o644)
	writeFileWithMode(t, filepath.Join(codexTarget.Root, "multi", "references", "guide.md"), "codex drift\n", 0o644)
	return project, newSkillRPCTestServer(t, svc)
}

func setupUnmanagedProviderConflictFixture(t *testing.T) (string, *platformrpc.Server) {
	t.Helper()
	project := t.TempDir()
	superHome := filepath.Join(t.TempDir(), ".super-dolphin")
	svc := &service{root: t.TempDir(), projectRoot: project, projectSkillsRoot: defaultProjectSkillsRoot(project), superDolphinHome: superHome, http: &http.Client{}}
	writeSkillWithSupportFiles(t, filepath.Join(project, ".agent", "skills", "scratch"), "scratch")
	writeSkillWithSupportFiles(t, filepath.Join(project, ".claude", "skills", "scratch"), "scratch")
	return project, newSkillRPCTestServer(t, svc)
}

func resolutionPreviewHashes(t *testing.T, canonicalDir, mirrorDir string) (string, string) {
	t.Helper()
	beforeMirror, err := stableMirrorDirectoryHash(mirrorDir)
	if err != nil {
		t.Fatalf("stableMirrorDirectoryHash: %v", err)
	}
	return skillDirContentHash(canonicalDir), beforeMirror
}

func dispatchResolutionList(t *testing.T, server *platformrpc.Server, project string) skillResolutionListResult {
	t.Helper()
	raw, err := server.Dispatch(context.Background(), "skills/resolution_list", json.RawMessage(`{"cwd":"`+project+`"}`))
	if err != nil {
		t.Fatalf("Dispatch resolution_list: %v", err)
	}
	var got skillResolutionListResult
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("Unmarshal resolution_list: %v", err)
	}
	return got
}

func dispatchResolutionPreview(t *testing.T, server *platformrpc.Server, project, conflictID, action string) skillResolutionPreviewResult {
	t.Helper()
	raw, err := server.Dispatch(context.Background(), "skills/resolution_preview", json.RawMessage(`{"cwd":"`+project+`","conflict_id":"`+conflictID+`","name":"drift","scope":"project","provider":"codex","action":"`+action+`","include_diff":true}`))
	if err != nil {
		t.Fatalf("Dispatch %s preview: %v", action, err)
	}
	var got skillResolutionPreviewResult
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("Unmarshal %s preview: %v", action, err)
	}
	if len(got.Items) != 1 {
		t.Fatalf("%s preview items = %d, want 1", action, len(got.Items))
	}
	return got
}

func dispatchResolutionPreviewWithName(t *testing.T, server *platformrpc.Server, project, conflictID, action, newName string) skillResolutionPreviewResult {
	t.Helper()
	raw, err := server.Dispatch(context.Background(), "skills/resolution_preview", json.RawMessage(`{"cwd":"`+project+`","conflict_id":"`+conflictID+`","name":"drift","scope":"project","provider":"codex","action":"`+action+`","new_name":"`+newName+`"}`))
	if err != nil {
		t.Fatalf("Dispatch %s preview with new_name: %v", action, err)
	}
	var got skillResolutionPreviewResult
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("Unmarshal %s preview: %v", action, err)
	}
	if len(got.Items) != 1 {
		t.Fatalf("%s preview items = %d, want 1", action, len(got.Items))
	}
	return got
}

func assertReadOnlyDiffPreview(t *testing.T, view skillResolutionPreviewResult) {
	t.Helper()
	if view.Items[0].PreviewID != "" || view.Items[0].PreviewHash != "" {
		t.Fatalf("view_diff preview item = %+v, want read-only item without preview id/hash", view.Items[0])
	}
	if view.Items[0].Diff == "" {
		t.Fatalf("view_diff preview diff is empty: %+v", view.Items[0])
	}
}

func assertMutatingPreviewProof(t *testing.T, item skillResolutionPreviewItem, sourceHash, targetHash string) {
	t.Helper()
	if !strings.HasPrefix(item.PreviewID, "resolution-preview:") || item.PreviewHash == "" || item.BackupPath == "" {
		t.Fatalf("preview item missing mutating proof fields: %+v", item)
	}
	if item.SourceHash != sourceHash || item.TargetHash != targetHash {
		t.Fatalf("hashes = source %q target %q, want %q/%q", item.SourceHash, item.TargetHash, sourceHash, targetHash)
	}
}

func assertResolutionPreviewNoWrite(t *testing.T, canonicalDir, mirrorDir, beforeCanonical, beforeMirror string) {
	t.Helper()
	if after := skillDirContentHash(canonicalDir); after != beforeCanonical {
		t.Fatalf("canonical changed during preview: before=%s after=%s", beforeCanonical, after)
	}
	afterMirror, err := stableMirrorDirectoryHash(mirrorDir)
	if err != nil {
		t.Fatalf("stableMirrorDirectoryHash after preview: %v", err)
	}
	if afterMirror != beforeMirror {
		t.Fatalf("mirror changed during preview: before=%s after=%s", beforeMirror, afterMirror)
	}
}

func findResolutionItem(t *testing.T, items []skillResolutionItem, kind, name, scope string) skillResolutionItem {
	t.Helper()
	for _, item := range items {
		if item.Kind == kind && item.Name == name && (scope == "" || item.Scope == scope) {
			if item.ConflictID == "" {
				t.Fatalf("item has empty conflict_id: %+v", item)
			}
			return item
		}
	}
	t.Fatalf("missing resolution item kind=%q name=%q scope=%q in %+v", kind, name, scope, items)
	return skillResolutionItem{}
}

func assertResolutionActions(t *testing.T, item skillResolutionItem, want ...string) {
	t.Helper()
	if len(item.AvailableActions) != len(want) {
		t.Fatalf("actions = %v, want %v; item=%+v", item.AvailableActions, want, item)
	}
	for i, action := range want {
		if item.AvailableActions[i] != action {
			t.Fatalf("actions = %v, want %v; item=%+v", item.AvailableActions, want, item)
		}
	}
}

func assertResolutionProviderEntry(t *testing.T, item skillResolutionItem, provider, name string) {
	t.Helper()
	for _, entry := range item.ProviderEntries {
		if entry.Provider == provider && strings.HasSuffix(filepath.ToSlash(entry.SourcePath), "/"+name) && entry.SourceHash != "" {
			return
		}
	}
	t.Fatalf("missing provider entry provider=%q name=%q in %+v", provider, name, item.ProviderEntries)
}

func assertResolutionSource(t *testing.T, item skillResolutionItem, scope, personalType, canonicalID string) {
	t.Helper()
	for _, source := range item.Sources {
		if source.Scope == scope && source.PersonalType == personalType && source.CanonicalID == canonicalID && source.ContentHash != "" {
			return
		}
	}
	t.Fatalf("missing source scope=%q personal_type=%q canonical_id=%q in %+v", scope, personalType, canonicalID, item.Sources)
}
