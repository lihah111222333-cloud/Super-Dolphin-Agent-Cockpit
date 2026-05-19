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
	svc := &service{root: t.TempDir(), projectRoot: project, projectSkillsRoot: defaultProjectSkillsRoot(project), superDolphinHome: superHome, http: &http.Client{}}
	writeSkillWithSupportFiles(t, filepath.Join(project, ".agent", "skills", "stale"), "stale")
	records, err := newCanonicalStore(superHome).scan(project)
	if err != nil {
		t.Fatalf("scan canonical records: %v", err)
	}
	target := SkillMirrorTarget{TargetID: "codex:project:" + RepoFingerprint(project), Provider: SkillProviderCodex, Scope: skillScopeProject, Root: providerProjectMirrorRoot(SkillProviderCodex, project), CanonicalRootID: RepoFingerprint(project)}
	if _, err := PublishSkillMirrors(context.Background(), records, []SkillMirrorTarget{target}); err != nil {
		t.Fatalf("PublishSkillMirrors: %v", err)
	}
	writeFileWithMode(t, filepath.Join(project, ".agent", "skills", "stale", "references", "guide.md"), "canonical v2\n", 0o644)

	got := dispatchResolutionList(t, newSkillRPCTestServer(t, svc), project)
	item := findResolutionItem(t, got.Items, "mirror_drift", "stale", skillScopeProject)
	assertResolutionActions(t, item, ResolutionViewDiff, ResolutionSaveAsNewSkill, ResolutionSyncBackCanonical, ResolutionCanonicalOverwrite)
}

func TestSkillResolutionListAggregatesMultiMirrorDrift(t *testing.T) {
	setSkillTestUserHome(t)
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
	codexTarget := SkillMirrorTarget{TargetID: "codex:project:" + fingerprint, Provider: SkillProviderCodex, Scope: skillScopeProject, Root: providerProjectMirrorRoot(SkillProviderCodex, project), CanonicalRootID: fingerprint}
	if _, err := PublishSkillMirrors(context.Background(), records, []SkillMirrorTarget{claudeTarget, codexTarget}); err != nil {
		t.Fatalf("PublishSkillMirrors: %v", err)
	}
	writeFileWithMode(t, filepath.Join(claudeTarget.Root, "multi", "references", "guide.md"), "claude drift\n", 0o644)
	writeFileWithMode(t, filepath.Join(codexTarget.Root, "multi", "references", "guide.md"), "codex drift\n", 0o644)

	item := findResolutionItem(t, dispatchResolutionList(t, newSkillRPCTestServer(t, svc), project).Items, "multi_mirror_drift", "multi", skillScopeProject)
	if len(item.ProviderEntries) != 2 {
		t.Fatalf("multi provider entries = %+v, want two entries", item.ProviderEntries)
	}
	assertResolutionActions(t, item, ResolutionViewDiff, ResolutionSaveAsNewSkill, ResolutionSyncBackCanonical, ResolutionCanonicalOverwrite)
}
