package skill

import (
	"context"
	"path/filepath"
	"testing"
)

func TestResolveSkillMirrorDriftSyncBackPublishesOtherProjectMirrors(t *testing.T) {
	project := t.TempDir()
	skillDir := filepath.Join(project, ".agents", "skills", "build")
	writeSkillWithSupportFiles(t, skillDir, "build")
	claudeTarget := projectMirrorTargetForTest(project, SkillProviderClaude)
	publishInitialProjectMirrorsForTest(t, project, claudeTarget)
	writeFileWithMode(t, filepath.Join(claudeTarget.Root, "build", "references", "guide.md"), "claude edit\n", 0o644)
	previewHash := mustStableMirrorDirectoryHash(t, filepath.Join(claudeTarget.Root, "build"))
	svc := &service{projectRoot: project, projectSkillsRoot: defaultProjectSkillsRoot(project), auditStore: &capturingSkillAuditStore{}}

	report, err := ResolveSkillMirrorDrift(context.Background(), svc, SkillMirrorResolutionRequest{
		Action:      "sync_back_to_canonical",
		Name:        "build",
		Target:      claudeTarget,
		PreviewHash: previewHash,
	})
	if err != nil {
		t.Fatalf("ResolveSkillMirrorDrift(sync_back_to_canonical): %v report=%+v", err, report)
	}

	assertFileContent(t, filepath.Join(skillDir, "references", "guide.md"), "claude edit\n")
}

func TestResolveSkillMirrorDriftSaveAsNewPublishesNewSkillToOtherProjectMirrors(t *testing.T) {
	project := t.TempDir()
	writeSkillWithSupportFiles(t, filepath.Join(project, ".agents", "skills", "build"), "build")
	claudeTarget := projectMirrorTargetForTest(project, SkillProviderClaude)
	publishInitialProjectMirrorsForTest(t, project, claudeTarget)
	writeFileWithMode(t, filepath.Join(claudeTarget.Root, "build", "references", "guide.md"), "new skill edit\n", 0o644)
	previewHash := mustStableMirrorDirectoryHash(t, filepath.Join(claudeTarget.Root, "build"))
	svc := &service{projectRoot: project, projectSkillsRoot: defaultProjectSkillsRoot(project), auditStore: &capturingSkillAuditStore{}}

	report, err := ResolveSkillMirrorDrift(context.Background(), svc, SkillMirrorResolutionRequest{
		Action:      "save_as_new_skill",
		Name:        "build",
		NewName:     "build-copy",
		Target:      claudeTarget,
		PreviewHash: previewHash,
	})
	if err != nil {
		t.Fatalf("ResolveSkillMirrorDrift(save_as_new_skill): %v report=%+v", err, report)
	}

	assertFileContent(t, filepath.Join(project, ".agents", "skills", "build-copy", skillMainFile), "---\nname: build-copy\n---\n# build\n")
	assertFileContent(t, filepath.Join(project, ".agents", "skills", "build-copy", "references", "guide.md"), "new skill edit\n")
}

func TestTakeoverProviderSkillPublishesTakenOverProjectSkillToOtherMirrors(t *testing.T) {
	project := t.TempDir()
	writeSkillWithSupportFiles(t, filepath.Join(project, ".agents", "skills", "owned"), "owned")
	claudeTarget := projectMirrorTargetForTest(project, SkillProviderClaude)
	writeSkillWithSupportFiles(t, filepath.Join(claudeTarget.Root, "owned"), "owned")
	writeFileWithMode(t, filepath.Join(claudeTarget.Root, "owned", "references", "guide.md"), "provider edit\n", 0o644)
	previewHash := mustStableMirrorDirectoryHash(t, filepath.Join(claudeTarget.Root, "owned"))
	svc := &service{projectRoot: project, projectSkillsRoot: defaultProjectSkillsRoot(project), auditStore: &capturingSkillAuditStore{}}

	report, err := TakeoverProviderSkill(context.Background(), svc, SkillMirrorResolutionRequest{
		Action:      "takeover_provider_skill",
		Name:        "owned",
		Target:      claudeTarget,
		PreviewHash: previewHash,
	})
	if err != nil {
		t.Fatalf("TakeoverProviderSkill: %v report=%+v", err, report)
	}

	assertFileContent(t, filepath.Join(project, ".agents", "skills", "owned", "references", "guide.md"), "provider edit\n")
}

func projectMirrorTargetForTest(project string, provider SkillProvider) SkillMirrorTarget {
	fingerprint := RepoFingerprint(project)
	rootName := ".codex"
	if provider == SkillProviderClaude {
		rootName = ".claude"
	}
	return SkillMirrorTarget{
		TargetID:        string(provider) + ":project:" + fingerprint,
		Provider:        provider,
		Scope:           skillScopeProject,
		Root:            filepath.Join(project, rootName, "skills"),
		CanonicalRootID: fingerprint,
	}
}

func publishInitialProjectMirrorsForTest(t *testing.T, project string, targets ...SkillMirrorTarget) {
	t.Helper()
	records, err := newCanonicalStore("").scan(project)
	if err != nil {
		t.Fatalf("scan canonical records: %v", err)
	}
	if _, err := PublishSkillMirrors(context.Background(), records, targets); err != nil {
		t.Fatalf("PublishSkillMirrors initial: %v", err)
	}
}

func mustStableMirrorDirectoryHash(t *testing.T, dir string) string {
	t.Helper()
	hash, err := stableMirrorDirectoryHash(dir)
	if err != nil {
		t.Fatalf("stableMirrorDirectoryHash(%s): %v", dir, err)
	}
	return hash
}
