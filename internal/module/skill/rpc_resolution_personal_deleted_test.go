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

func TestSkillResolutionPreviewPersonalCanonicalDeletedWithDriftUsesSuperDolphinHome(t *testing.T) {
	project, server, svc := setupPersonalCanonicalDeletedDriftFixture(t)
	item := findResolutionItem(t, dispatchResolutionList(t, server, project).Items, "canonical_deleted_with_drift", "deleted-personal", skillScopePersonal)

	preview := dispatchResolutionPreviewForScope(t, server, project, item.ConflictID, "deleted-personal", skillScopePersonal, string(SkillProviderClaude), ResolutionSyncBackPersonal)
	wantSuffix := "/.super-dolphin/skills/personal/user/deleted-personal"
	if !strings.HasSuffix(preview.Items[0].TargetPath, wantSuffix) {
		t.Fatalf("personal deleted drift target_path = %q, want suffix %q from Super-Dolphin home %q", preview.Items[0].TargetPath, wantSuffix, svc.resolvedSuperDolphinHome())
	}
	if strings.Contains(preview.Items[0].TargetPath, "/.claude/skills/personal/") || strings.Contains(preview.Items[0].TargetPath, "/.agents/skills/personal/") {
		t.Fatalf("personal deleted drift target_path = %q, must not derive canonical path from provider mirror root", preview.Items[0].TargetPath)
	}
	assertResolutionActions(t, item, ResolutionViewDiff, ResolutionSaveAsNewPersonal, ResolutionSyncBackPersonal, ResolutionConfirmDeleteDriftedMirror)
}

func setupPersonalCanonicalDeletedDriftFixture(t *testing.T) (string, *platformrpc.Server, *service) {
	t.Helper()
	setSkillTestUserHome(t)
	project := t.TempDir()
	superHome := filepath.Join(t.TempDir(), ".super-dolphin")
	svc := &service{root: t.TempDir(), projectRoot: project, projectSkillsRoot: defaultProjectSkillsRoot(project), superDolphinHome: superHome, http: &http.Client{}}
	owner, err := resolveOwnerIdentity(superHome, defaultOwnerOSUID(), defaultAppProfile())
	if err != nil {
		t.Fatalf("resolveOwnerIdentity: %v", err)
	}
	writeSkillWithSupportFiles(t, filepath.Join(superHome, "skills", "personal", personalSkillTypeUser, "drift-personal"), "drift-personal")
	records, err := newCanonicalStore(superHome).scan(project)
	if err != nil {
		t.Fatalf("scan canonical records: %v", err)
	}
	target := SkillMirrorTarget{TargetID: "claude:user-global:" + owner.OwnerKey, Provider: SkillProviderClaude, Scope: skillScopePersonal, Root: providerPersonalMirrorRoot(SkillProviderClaude), CanonicalRootID: owner.OwnerKey}
	if _, err := PublishSkillMirrors(context.Background(), records, []SkillMirrorTarget{target}); err != nil {
		t.Fatalf("PublishSkillMirrors: %v", err)
	}
	if err := os.RemoveAll(filepath.Join(superHome, "skills", "personal", personalSkillTypeUser, "drift-personal")); err != nil {
		t.Fatalf("RemoveAll personal canonical: %v", err)
	}
	if err := os.Rename(filepath.Join(target.Root, "drift-personal"), filepath.Join(target.Root, "deleted-personal")); err != nil {
		t.Fatalf("rename personal mirror: %v", err)
	}
	manifestPath := filepath.Join(target.Root, skillMirrorManifestFile)
	manifest, err := readSkillMirrorManifest(manifestPath)
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	entry := manifest.Skills["drift-personal"]
	entry.CanonicalID = "personal/user/deleted-personal"
	manifest.Skills = map[string]SkillMirrorEntry{"deleted-personal": entry}
	if err := writeSkillMirrorManifest(manifestPath, manifest); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	return project, newSkillRPCTestServer(t, svc), svc
}

func dispatchResolutionPreviewForScope(t *testing.T, server *platformrpc.Server, project, conflictID, name, scope, provider, action string) skillResolutionPreviewResult {
	t.Helper()
	raw, err := server.Dispatch(context.Background(), "skills/resolution_preview", mustRawJSON(t, map[string]any{
		"cwd":          project,
		"conflict_id":  conflictID,
		"name":         name,
		"scope":        scope,
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
