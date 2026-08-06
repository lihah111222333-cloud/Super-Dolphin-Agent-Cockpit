package skill

import (
	"context"
	"net/http"
	"path/filepath"
	"testing"
)

func TestSkillResolutionListReportsCanonicalOnlyChangeAsMirrorDrift(t *testing.T) {
	setSkillTestUserHome(t)
	project := t.TempDir()
	superHome := filepath.Join(t.TempDir(), ".super-dolphin")
	svc := &service{root: t.TempDir(), projectRoot: project, projectSkillsRoot: defaultProjectSkillsRoot(project), superDolphinHome: superHome, http: &http.Client{}, mirrorLocks: NewMirrorRootLockRegistry()}
	writeSkillWithSupportFiles(t, filepath.Join(project, ".agents", "skills", "stale"), "stale")
	records, err := newCanonicalStore(superHome).scan(project)
	if err != nil {
		t.Fatalf("scan canonical records: %v", err)
	}
	target := SkillMirrorTarget{TargetID: "claude:project:" + RepoFingerprint(project), Provider: SkillProviderClaude, Scope: skillScopeProject, Root: providerProjectMirrorRoot(SkillProviderClaude, project), CanonicalRootID: RepoFingerprint(project)}
	if _, err := PublishSkillMirrors(NewMirrorRootLockRegistry(), context.Background(), records, []SkillMirrorTarget{target}); err != nil {
		t.Fatalf("PublishSkillMirrors: %v", err)
	}
	writeFileWithMode(t, filepath.Join(project, ".agents", "skills", "stale", "references", "guide.md"), "canonical v2\n", 0o644)

	got := dispatchResolutionList(t, newSkillRPCTestServer(t, svc), project)
	item := findResolutionItem(t, got.Items, "mirror_drift", "stale", skillScopeProject)
	assertResolutionActions(t, item, ResolutionViewDiff, ResolutionSaveAsNewSkill, ResolutionSyncBackCanonical, ResolutionCanonicalOverwrite)
}

func TestSkillResolutionListReportsProjectMirrorDriftAfterCanonicalChange(t *testing.T) {
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

	item := findResolutionItem(t, dispatchResolutionList(t, newSkillRPCTestServer(t, svc), project).Items, "mirror_drift", "multi", skillScopeProject)
	if len(item.ProviderEntries) != 1 || item.ProviderEntries[0].Provider != string(SkillProviderClaude) {
		t.Fatalf("provider entries = %+v, want Claude project mirror", item.ProviderEntries)
	}
	assertResolutionActions(t, item, ResolutionViewDiff, ResolutionSaveAsNewSkill, ResolutionSyncBackCanonical, ResolutionCanonicalOverwrite)
}
