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

func setupResolutionPreviewDriftFixture(t *testing.T) (string, *platformrpc.Server, string, string) {
	t.Helper()
	project, server, canonicalDir, mirrorDir, _ := setupResolutionPreviewDriftFixtureWithService(t)
	return project, server, canonicalDir, mirrorDir
}

func setupResolutionPreviewDriftFixtureWithService(t *testing.T) (string, *platformrpc.Server, string, string, *service) {
	t.Helper()
	setSkillTestUserHome(t)
	project := t.TempDir()
	superHome := filepath.Join(t.TempDir(), ".super-dolphin")
	svc := &service{root: t.TempDir(), projectRoot: project, projectSkillsRoot: defaultProjectSkillsRoot(project), superDolphinHome: superHome, http: &http.Client{}, mirrorLocks: NewMirrorRootLockRegistry()}
	writeSkillWithSupportFiles(t, filepath.Join(project, ".agents", "skills", "drift"), "drift")
	records, err := newCanonicalStore(superHome).scan(project)
	if err != nil {
		t.Fatalf("scan canonical records: %v", err)
	}
	fingerprint := RepoFingerprint(project)
	target := SkillMirrorTarget{TargetID: "claude:project:" + fingerprint, Provider: SkillProviderClaude, Scope: skillScopeProject, Root: providerProjectMirrorRoot(SkillProviderClaude, project), CanonicalRootID: fingerprint}
	if _, err := PublishSkillMirrors(NewMirrorRootLockRegistry(), context.Background(), records, []SkillMirrorTarget{target}); err != nil {
		t.Fatalf("PublishSkillMirrors: %v", err)
	}
	canonicalDir := filepath.Join(project, ".agents", "skills", "drift")
	mirrorDir := filepath.Join(target.Root, "drift")
	writeFileWithMode(t, filepath.Join(mirrorDir, "references", "guide.md"), "project drift\n", 0o644)
	return project, newSkillRPCTestServer(t, svc), canonicalDir, mirrorDir, svc
}

func setupCanonicalDeletedDriftFixture(t *testing.T) (string, *platformrpc.Server) {
	t.Helper()
	project, server, _ := setupCanonicalDeletedDriftFixtureWithService(t)
	return project, server
}

func setupCanonicalDeletedDriftFixtureWithService(t *testing.T) (string, *platformrpc.Server, *service) {
	t.Helper()
	project, server, canonicalDir, _, svc := setupResolutionPreviewDriftFixtureWithService(t)
	if err := os.RemoveAll(canonicalDir); err != nil {
		t.Fatalf("RemoveAll canonical: %v", err)
	}
	root := providerProjectMirrorRoot(SkillProviderClaude, project)
	if err := os.Rename(filepath.Join(root, "drift"), filepath.Join(root, "deleted")); err != nil {
		t.Fatalf("rename mirror to deleted: %v", err)
	}
	manifest, err := readSkillMirrorManifest(filepath.Join(root, skillMirrorManifestFile))
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	entry := manifest.Skills["drift"]
	entry.CanonicalID = "project/deleted"
	manifest.Skills = map[string]SkillMirrorEntry{"deleted": entry}
	if err := writeSkillMirrorManifest(filepath.Join(root, skillMirrorManifestFile), manifest); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	return project, server, svc
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
	setSkillTestUserHome(t)
	project := t.TempDir()
	superHome := filepath.Join(t.TempDir(), ".super-dolphin")
	svc := &service{root: t.TempDir(), projectRoot: project, projectSkillsRoot: defaultProjectSkillsRoot(project), superDolphinHome: superHome, http: &http.Client{}, mirrorLocks: NewMirrorRootLockRegistry()}
	writeSkillWithSupportFiles(t, filepath.Join(project, ".agents", "skills", "multi"), "multi")
	records, err := newCanonicalStore(superHome).scan(project)
	if err != nil {
		t.Fatalf("scan canonical records: %v", err)
	}
	fingerprint := RepoFingerprint(project)
	claudeTarget := SkillMirrorTarget{TargetID: "claude:project:" + fingerprint, Provider: SkillProviderClaude, Scope: skillScopeProject, Root: filepath.Join(project, ".claude", "skills"), CanonicalRootID: fingerprint}
	if _, err := PublishSkillMirrors(NewMirrorRootLockRegistry(), context.Background(), records, []SkillMirrorTarget{claudeTarget}); err != nil {
		t.Fatalf("PublishSkillMirrors: %v", err)
	}
	writeFileWithMode(t, filepath.Join(claudeTarget.Root, "multi", "references", "guide.md"), "claude drift\n", 0o644)
	return project, newSkillRPCTestServer(t, svc)
}

func setupOrphanUnmanagedProviderConflictFixture(t *testing.T) (string, *platformrpc.Server) {
	t.Helper()
	setSkillTestUserHome(t)
	project := t.TempDir()
	superHome := filepath.Join(t.TempDir(), ".super-dolphin")
	svc := &service{root: t.TempDir(), projectRoot: project, projectSkillsRoot: defaultProjectSkillsRoot(project), superDolphinHome: superHome, http: &http.Client{}, mirrorLocks: NewMirrorRootLockRegistry()}
	writeSkillWithSupportFiles(t, filepath.Join(project, ".claude", "skills", "scratch"), "scratch")
	return project, newSkillRPCTestServer(t, svc)
}

func setupMultiProviderUnmanagedFixture(t *testing.T) (string, *platformrpc.Server) {
	t.Helper()
	setSkillTestUserHome(t)
	project := t.TempDir()
	superHome := filepath.Join(t.TempDir(), ".super-dolphin")
	svc := &service{root: t.TempDir(), projectRoot: project, projectSkillsRoot: defaultProjectSkillsRoot(project), superDolphinHome: superHome, http: &http.Client{}, mirrorLocks: NewMirrorRootLockRegistry()}
	writeSkillWithSupportFiles(t, filepath.Join(project, ".claude", "skills", "scratch"), "scratch")
	return project, newSkillRPCTestServer(t, svc)
}

func resolutionPreviewHashes(t *testing.T, canonicalDir, mirrorDir string) (string, string) {
	t.Helper()
	beforeMirror, err := stableMirrorDirectoryHash(mirrorDir)
	if err != nil {
		t.Fatalf("stableMirrorDirectoryHash: %v", err)
	}
	return mustSkillDirContentHash(t, canonicalDir), beforeMirror
}

func dispatchResolutionList(t *testing.T, server *platformrpc.Server, project string) skillResolutionListResult {
	t.Helper()
	raw, err := server.Dispatch(context.Background(), "skills/resolution_list", mustRawJSON(t, map[string]any{"cwd": project}))
	if err != nil {
		t.Fatalf("Dispatch resolution_list: %v", err)
	}
	var got skillResolutionListResult
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("Unmarshal resolution_list: %v", err)
	}
	return got
}

func setupResolutionListConflictFixture(t *testing.T) (string, *platformrpc.Server) {
	t.Helper()
	setSkillTestUserHome(t)
	project := t.TempDir()
	superHome := filepath.Join(t.TempDir(), ".super-dolphin")
	svc := &service{root: t.TempDir(), projectRoot: project, projectSkillsRoot: defaultProjectSkillsRoot(project), superDolphinHome: superHome, http: &http.Client{}, mirrorLocks: NewMirrorRootLockRegistry()}
	writeSkillWithSupportFiles(t, filepath.Join(project, ".agents", "skills", "drift"), "drift")
	writeSkillWithSupportFiles(t, filepath.Join(project, ".agents", "skills", "scratch"), "scratch")
	writeSkillWithSupportFiles(t, filepath.Join(project, ".agents", "skills", "same"), "same")
	writeSkillWithSupportFiles(t, filepath.Join(superHome, "skills", "personal", "user", "same"), "same")
	writeSkillWithSupportFiles(t, filepath.Join(superHome, "skills", "personal", "user", "loose"), "loose")
	writeSkillWithSupportFiles(t, filepath.Join(superHome, "skills", "personal", "user", "notes"), "notes")

	projectTarget, personalTarget := publishResolutionListConflictMirrors(t, project, superHome)
	writeFileWithMode(t, filepath.Join(projectTarget.Root, "drift", "references", "guide.md"), "project drift\n", 0o644)
	writeFileWithMode(t, filepath.Join(personalTarget.Root, "notes", "references", "guide.md"), "personal drift\n", 0o644)
	writeSkillWithSupportFiles(t, filepath.Join(project, ".claude", "skills", "scratch"), "scratch")
	writeFileWithMode(t, filepath.Join(project, ".claude", "skills", "scratch", "references", "guide.md"), "provider scratch\n", 0o644)
	removeSkillMirrorManifestEntry(t, projectTarget.Root, "scratch")
	removeSkillMirrorManifestEntry(t, personalTarget.Root, "loose")
	return project, newSkillRPCTestServer(t, svc)
}

func publishResolutionListConflictMirrors(t *testing.T, project, superHome string) (SkillMirrorTarget, SkillMirrorTarget) {
	t.Helper()
	records, err := newCanonicalStore(superHome).scan(project)
	if err != nil {
		t.Fatalf("scan canonical records: %v", err)
	}
	fingerprint := RepoFingerprint(project)
	owner, err := resolveOwnerIdentity(superHome, defaultOwnerOSUID(), defaultAppProfile())
	if err != nil {
		t.Fatalf("resolveOwnerIdentity: %v", err)
	}
	projectTarget := SkillMirrorTarget{TargetID: "claude:project:" + fingerprint, Provider: SkillProviderClaude, Scope: skillScopeProject, Root: providerProjectMirrorRoot(SkillProviderClaude, project), CanonicalRootID: fingerprint}
	personalTarget := SkillMirrorTarget{TargetID: "claude:user-global:" + owner.OwnerKey, Provider: SkillProviderClaude, Scope: skillScopePersonal, Root: providerPersonalMirrorRoot(SkillProviderClaude), CanonicalRootID: owner.OwnerKey}
	if _, err := PublishSkillMirrors(NewMirrorRootLockRegistry(), context.Background(), records, []SkillMirrorTarget{projectTarget, personalTarget}); err != nil {
		t.Fatalf("PublishSkillMirrors: %v", err)
	}
	return projectTarget, personalTarget
}

func removeSkillMirrorManifestEntry(t *testing.T, root, name string) {
	t.Helper()
	manifestPath := filepath.Join(root, skillMirrorManifestFile)
	manifest, err := readSkillMirrorManifest(manifestPath)
	if err != nil {
		t.Fatalf("read mirror manifest: %v", err)
	}
	delete(manifest.Skills, name)
	if err := writeSkillMirrorManifest(manifestPath, manifest); err != nil {
		t.Fatalf("write mirror manifest: %v", err)
	}
}

func dispatchResolutionPreview(t *testing.T, server *platformrpc.Server, project, conflictID, action string) skillResolutionPreviewResult {
	return dispatchResolutionPreviewNamed(t, server, project, conflictID, "drift", action)
}

func dispatchResolutionPreviewNamed(t *testing.T, server *platformrpc.Server, project, conflictID, name, action string) skillResolutionPreviewResult {
	return dispatchResolutionPreviewProviderNamed(t, server, project, conflictID, name, "claude", action)
}

func dispatchResolutionPreviewProviderNamed(t *testing.T, server *platformrpc.Server, project, conflictID, name, provider, action string) skillResolutionPreviewResult {
	t.Helper()
	raw, err := server.Dispatch(context.Background(), "skills/resolution_preview", mustRawJSON(t, map[string]any{
		"cwd":          project,
		"conflict_id":  conflictID,
		"name":         name,
		"scope":        "project",
		"provider":     provider,
		"action":       action,
		"include_diff": true,
	}))
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
	return dispatchResolutionPreviewNamedWithName(t, server, project, conflictID, "drift", action, newName)
}

func dispatchResolutionPreviewNamedWithName(t *testing.T, server *platformrpc.Server, project, conflictID, name, action, newName string) skillResolutionPreviewResult {
	t.Helper()
	raw, err := server.Dispatch(context.Background(), "skills/resolution_preview", mustRawJSON(t, map[string]any{
		"cwd":         project,
		"conflict_id": conflictID,
		"name":        name,
		"scope":       "project",
		"provider":    "claude",
		"action":      action,
		"new_name":    newName,
	}))
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

func assertPreviewProviders(t *testing.T, items []skillResolutionPreviewItem, want ...string) {
	t.Helper()
	got := make(map[string]bool, len(items))
	for _, item := range items {
		got[item.SourceProvider] = true
	}
	for _, provider := range want {
		if !got[provider] {
			t.Fatalf("preview providers = %+v, missing %q", items, provider)
		}
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
	if after := mustSkillDirContentHash(t, canonicalDir); after != beforeCanonical {
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

func assertResolutionListConflictItems(t *testing.T, items []skillResolutionItem) {
	t.Helper()
	same := findResolutionItem(t, items, "same_name", "same", "")
	assertResolutionActions(t, same, ResolutionViewDiff, ResolutionKeepSelected, ResolutionRenamePersonal)
	assertResolutionSource(t, same, skillScopeProject, "", "project/same")
	assertResolutionSource(t, same, skillScopePersonal, personalSkillTypeUser, "personal/user/same")

	projectDrift := findResolutionItem(t, items, "mirror_drift", "drift", skillScopeProject)
	assertResolutionActions(t, projectDrift, ResolutionViewDiff, ResolutionSaveAsNewSkill, ResolutionSyncBackCanonical, ResolutionCanonicalOverwrite)
	assertResolutionProviderEntry(t, projectDrift, string(SkillProviderClaude), "drift")

	personalDrift := findResolutionItem(t, items, "mirror_drift", "notes", skillScopePersonal)
	if personalDrift.PersonalType != personalSkillTypeUser {
		t.Fatalf("personal drift personal_type = %q, want user; item=%+v", personalDrift.PersonalType, personalDrift)
	}
	assertResolutionActions(t, personalDrift, ResolutionViewDiff, ResolutionSaveAsNewPersonal, ResolutionSyncBackPersonal, ResolutionPersonalOverwrite)
	assertResolutionProviderEntry(t, personalDrift, string(SkillProviderClaude), "notes")

	unmanaged := findResolutionItem(t, items, "unmanaged_same_name", "scratch", skillScopeProject)
	assertResolutionActions(t, unmanaged, ResolutionViewDiff, ResolutionSyncBackCanonical, ResolutionCanonicalOverwrite, ResolutionSaveAsNewSkill)
	assertResolutionProviderEntry(t, unmanaged, string(SkillProviderClaude), "scratch")
	personalUnmanaged := findResolutionItem(t, items, "unmanaged_same_name", "loose", skillScopePersonal)
	assertResolutionActions(t, personalUnmanaged, ResolutionViewUnmanaged, ResolutionImportPersonal)
	assertResolutionProviderEntry(t, personalUnmanaged, string(SkillProviderClaude), "loose")
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

func findResolutionItemByKindAndScope(t *testing.T, items []skillResolutionItem, kind, scope string) skillResolutionItem {
	t.Helper()
	for _, item := range items {
		if item.Kind == kind && (scope == "" || item.Scope == scope) {
			if item.ConflictID == "" {
				t.Fatalf("item has empty conflict_id: %+v", item)
			}
			return item
		}
	}
	t.Fatalf("missing resolution item kind=%q scope=%q in %+v", kind, scope, items)
	return skillResolutionItem{}
}

func assertResolutionItemAbsent(t *testing.T, items []skillResolutionItem, kind, name, scope string) {
	t.Helper()
	for _, item := range items {
		if item.Kind == kind && item.Name == name && (scope == "" || item.Scope == scope) {
			t.Fatalf("unexpected resolution item kind=%q name=%q scope=%q in %+v", kind, name, scope, items)
		}
	}
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
