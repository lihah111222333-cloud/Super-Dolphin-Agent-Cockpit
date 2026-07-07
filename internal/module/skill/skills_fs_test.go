package skill

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestReadConfigReturnsExplicitStubBindingState(t *testing.T) {
	t.Parallel()

	svc := newTestSkillService(t)
	out, err := svc.ReadConfig(context.Background(), " agent-1 ")
	if err != nil {
		t.Fatalf("ReadConfig returned error: %v", err)
	}
	result, ok := out.(map[string]any)
	if !ok {
		t.Fatalf("ReadConfig result type mismatch: %T", out)
	}
	if got, _ := result["agent_id"].(string); got != "agent-1" {
		t.Fatalf("agent_id mismatch: got %q", got)
	}
	if got, _ := result["configured"].(bool); got {
		t.Fatal("configured mismatch: got true want false")
	}
	if got, _ := result["binding_count"].(int); got != 0 {
		t.Fatalf("binding_count mismatch: got %d", got)
	}
	if got, _ := result["binding_source"].(string); got != "stub" {
		t.Fatalf("binding_source mismatch: got %q", got)
	}
}

func TestWriteSkillContentRejectsRemovedSystemScope(t *testing.T) {
	t.Parallel()

	svc := newTestSkillService(t)
	_, err := svc.WriteSkillContent(context.Background(), "demo-skill", "# demo")
	if !errors.Is(err, ErrSkillSystemScopeRemoved) {
		t.Fatalf("WriteSkillContent error = %v, want ErrSkillSystemScopeRemoved", err)
	}
}

func TestReadLocalRejectsPathOutsideSkillsRoot(t *testing.T) {
	t.Parallel()

	skillsRoot := t.TempDir()
	outsideRoot := t.TempDir()
	projectRoot := t.TempDir()
	outsidePath := filepath.Join(outsideRoot, "SKILL.md")
	if err := os.WriteFile(outsidePath, []byte("# outside"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	svc := &service{projectRoot: projectRoot, root: skillsRoot, projectSkillsRoot: defaultProjectSkillsRoot(projectRoot), http: &http.Client{}}

	_, err := svc.ReadLocal(skillTestContext(projectRoot), outsidePath)
	if err == nil || err.Error() != "path escapes skills root: "+outsidePath {
		t.Fatalf("ReadLocal() error = %v, want path escapes skills root", err)
	}
}

func TestListLocalFilesRejectsDirOutsideSkillsRoot(t *testing.T) {
	t.Parallel()

	skillsRoot := t.TempDir()
	outsideRoot := t.TempDir()
	projectRoot := t.TempDir()
	svc := &service{projectRoot: projectRoot, root: skillsRoot, projectSkillsRoot: defaultProjectSkillsRoot(projectRoot), http: &http.Client{}}

	_, err := svc.ListLocalFiles(skillTestContext(projectRoot), listSkillFilesParams{Dir: outsideRoot})
	if err == nil || err.Error() != "path escapes skills root: "+outsideRoot {
		t.Fatalf("ListLocalFiles() error = %v, want path escapes skills root", err)
	}
}

func TestWriteLocalRejectsPathOutsideSkillsRoot(t *testing.T) {
	t.Parallel()

	skillsRoot := t.TempDir()
	outsideRoot := t.TempDir()
	projectRoot := t.TempDir()
	outsidePath := filepath.Join(outsideRoot, "note.md")
	if err := os.WriteFile(outsidePath, []byte("before"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	svc := &service{projectRoot: projectRoot, root: skillsRoot, projectSkillsRoot: defaultProjectSkillsRoot(projectRoot), http: &http.Client{}}

	_, err := svc.WriteLocal(skillTestContext(projectRoot), outsidePath, "after")
	if err == nil || err.Error() != "path escapes skills root: "+outsidePath {
		t.Fatalf("WriteLocal() error = %v, want path escapes skills root", err)
	}
}

func TestWriteLocalRejectsRelativeSymlinkEscape(t *testing.T) {
	projectRoot := t.TempDir()
	outsideRoot := t.TempDir()
	outsideSkillDir := filepath.Join(outsideRoot, "escape")
	if err := os.MkdirAll(outsideSkillDir, 0o755); err != nil {
		t.Fatalf("MkdirAll outside skill: %v", err)
	}
	skillsRoot := defaultProjectSkillsRoot(projectRoot)
	if err := os.MkdirAll(skillsRoot, 0o755); err != nil {
		t.Fatalf("MkdirAll skills root: %v", err)
	}
	if err := os.Symlink(outsideSkillDir, filepath.Join(skillsRoot, "escape")); err != nil {
		skipIfSymlinkPrivilegeNotHeld(t, err)
		t.Fatalf("Symlink escape skill: %v", err)
	}
	svc := &service{projectRoot: projectRoot, projectSkillsRoot: skillsRoot, superDolphinHome: newTestSuperDolphinHome(t), http: &http.Client{}}

	_, err := svc.WriteLocal(skillTestContext(projectRoot), "escape/SKILL.md", "---\nname: escape\n---\nbody", skillScopeProject)

	if err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("WriteLocal symlink escape error = %v, want symlink rejection", err)
	}
	assertMissing(t, filepath.Join(outsideSkillDir, skillMainFile))
}

func TestWriteLocalRejectsSkillMainSymlink(t *testing.T) {
	projectRoot := t.TempDir()
	outsideFile := filepath.Join(t.TempDir(), skillMainFile)
	if err := os.WriteFile(outsideFile, []byte("outside"), 0o644); err != nil {
		t.Fatalf("WriteFile outside: %v", err)
	}
	skillsRoot := defaultProjectSkillsRoot(projectRoot)
	skillDir := filepath.Join(skillsRoot, "escape")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatalf("MkdirAll skill dir: %v", err)
	}
	if err := os.Symlink(outsideFile, filepath.Join(skillDir, skillMainFile)); err != nil {
		skipIfSymlinkPrivilegeNotHeld(t, err)
		t.Fatalf("Symlink SKILL.md: %v", err)
	}
	svc := &service{projectRoot: projectRoot, projectSkillsRoot: skillsRoot, superDolphinHome: newTestSuperDolphinHome(t), http: &http.Client{}}

	_, err := svc.WriteLocal(skillTestContext(projectRoot), "escape/SKILL.md", "---\nname: escape\n---\nbody", skillScopeProject)

	if err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("WriteLocal SKILL.md symlink error = %v, want symlink rejection", err)
	}
	assertFileContent(t, outsideFile, "outside")
}

func TestWriteLocalPropagatesIntermediatePathError(t *testing.T) {
	projectRoot := t.TempDir()
	skillsRoot := defaultProjectSkillsRoot(projectRoot)
	if err := os.MkdirAll(skillsRoot, 0o755); err != nil {
		t.Fatalf("MkdirAll skills root: %v", err)
	}
	if err := os.WriteFile(filepath.Join(skillsRoot, "blocked"), []byte("not a directory"), 0o644); err != nil {
		t.Fatalf("WriteFile blocked path: %v", err)
	}
	svc := &service{projectRoot: projectRoot, projectSkillsRoot: skillsRoot, superDolphinHome: newTestSuperDolphinHome(t), http: &http.Client{}}

	_, err := svc.WriteLocal(skillTestContext(projectRoot), "blocked/SKILL.md", "---\nname: blocked\n---\nbody", skillScopeProject)

	if err == nil {
		t.Fatal("WriteLocal intermediate path error = nil, want propagated filesystem error")
	}
	if strings.Contains(err.Error(), "symlink") {
		t.Fatalf("WriteLocal intermediate path error = %v, want original filesystem error", err)
	}
}

func TestWriteLocalRejectsUnsafeFrontmatterSkillName(t *testing.T) {
	projectRoot := t.TempDir()
	svc := &service{projectRoot: projectRoot, projectSkillsRoot: defaultProjectSkillsRoot(projectRoot), superDolphinHome: newTestSuperDolphinHome(t), http: &http.Client{}}

	_, err := svc.WriteLocal(
		skillTestContext(projectRoot),
		"Agent工程学",
		"---\nname: \"../bad\"\n---\n# Agent 工程学\n",
		skillScopeProject,
	)

	if !errors.Is(err, ErrInvalidSkillName) {
		t.Fatalf("WriteLocal invalid frontmatter name error = %v, want ErrInvalidSkillName", err)
	}
	assertMissing(t, filepath.Join(projectRoot, ".agents", "skills", "agent工程学", skillMainFile))
}

func TestReadLocalAcceptsLegacyDisplayNameAlias(t *testing.T) {
	projectRoot := t.TempDir()
	svc := &service{projectRoot: projectRoot, projectSkillsRoot: defaultProjectSkillsRoot(projectRoot), superDolphinHome: newTestSuperDolphinHome(t), http: &http.Client{}}
	writeSkillContent(t, filepath.Join(projectRoot, ".agents", "skills", "Docker 容器化部署"), "Docker 容器化部署", "# legacy body\n")

	out, err := svc.ReadLocal(skillTestContext(projectRoot), "Docker 容器化部署")
	if err != nil {
		t.Fatalf("ReadLocal legacy display alias error = %v", err)
	}
	result, ok := out.(map[string]any)
	if !ok {
		t.Fatalf("ReadLocal result type = %T", out)
	}
	skill, ok := result["skill"].(map[string]any)
	if !ok {
		t.Fatalf("ReadLocal skill type = %T", result["skill"])
	}
	if got, _ := skill["path"].(string); !sameCleanPath(got, filepath.Join(projectRoot, ".agents", "skills", "Docker 容器化部署", skillMainFile)) {
		t.Fatalf("ReadLocal path = %q, want legacy dir SKILL.md", got)
	}
	if got, _ := skill["content"].(string); !strings.Contains(got, "name: Docker 容器化部署") {
		t.Fatalf("ReadLocal content = %q, want original legacy frontmatter", got)
	}
}

func TestDeleteLocalAcceptsLegacyDisplayNameAlias(t *testing.T) {
	projectRoot := t.TempDir()
	svc := &service{projectRoot: projectRoot, projectSkillsRoot: defaultProjectSkillsRoot(projectRoot), superDolphinHome: newTestSuperDolphinHome(t), http: &http.Client{}}
	legacyDir := filepath.Join(projectRoot, ".agents", "skills", "Docker 容器化部署")
	writeSkillContent(t, legacyDir, "Docker 容器化部署", "# legacy body\n")

	out, err := svc.DeleteLocal(skillTestContext(projectRoot), DeleteSkillParams{Name: "Docker 容器化部署", Scope: skillScopeProject})
	if err != nil {
		t.Fatalf("DeleteLocal legacy display alias error = %v", err)
	}
	result, ok := out.(map[string]any)
	if !ok {
		t.Fatalf("DeleteLocal result type = %T", out)
	}
	if got, _ := result["name"].(string); got != "docker-容器化部署" {
		t.Fatalf("DeleteLocal name = %q, want canonical name", got)
	}
	if got, _ := result["dir"].(string); !sameCleanPath(got, legacyDir) {
		t.Fatalf("DeleteLocal dir = %q, want legacy dir", got)
	}
	assertMissing(t, legacyDir)
	assertMissing(t, filepath.Join(projectRoot, ".agents", "skills", "docker-容器化部署"))
}

func TestWriteProjectPolicyRejectsSymlinkFile(t *testing.T) {
	projectRoot := t.TempDir()
	policyDir := defaultProjectSkillsRoot(projectRoot)
	if err := os.MkdirAll(policyDir, 0o755); err != nil {
		t.Fatalf("MkdirAll policy dir: %v", err)
	}
	outsideFile := filepath.Join(t.TempDir(), "policy.json")
	if err := os.WriteFile(outsideFile, []byte("outside"), 0o644); err != nil {
		t.Fatalf("WriteFile outside policy: %v", err)
	}
	if err := os.Symlink(outsideFile, filepath.Join(policyDir, projectSkillPolicyFile)); err != nil {
		skipIfSymlinkPrivilegeNotHeld(t, err)
		t.Fatalf("Symlink policy: %v", err)
	}

	_, err := writeProjectDisablePersonalPolicy(projectRoot, "build", personalSkillTypeUser)

	if err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("writeProjectDisablePersonalPolicy symlink error = %v, want symlink rejection", err)
	}
	assertFileContent(t, outsideFile, "outside")
}

func TestWriteLocalPublishesProjectMirrors(t *testing.T) {
	projectRoot := t.TempDir()
	svc := &service{projectRoot: projectRoot, projectSkillsRoot: defaultProjectSkillsRoot(projectRoot), superDolphinHome: newTestSuperDolphinHome(t), http: &http.Client{}}

	out, err := svc.WriteLocal(skillTestContext(projectRoot), "build", "---\nname: build\n---\nbody", skillScopeProject)
	if err != nil {
		t.Fatalf("WriteLocal() error = %v", err)
	}

	result := out.(map[string]any)
	report := mustMirrorPublishReport(t, result)
	assertPublishedReportItem(t, report.Published, "claude:project:"+RepoFingerprint(projectRoot), SkillProviderClaude, skillScopeProject, "build", "project/build")
	assertFileContent(t, filepath.Join(projectRoot, ".claude", "skills", "build", skillMainFile), "---\nname: build\n---\nbody")
	assertFileContent(t, filepath.Join(providerProjectMirrorRoot(SkillProviderCodex, projectRoot), "build", skillMainFile), "---\nname: build\n---\nbody")
	if _, err := readSkillMirrorManifest(filepath.Join(providerProjectMirrorRoot(SkillProviderCodex, projectRoot), skillMirrorManifestFile)); err != nil {
		t.Fatalf("read codex self manifest: %v", err)
	}
}

func TestWriteLocalProjectIgnoresConfiguredMirrorTargets(t *testing.T) {
	projectRoot := t.TempDir()
	outsideRoot := filepath.Join(t.TempDir(), "skills")
	svc := &service{
		projectRoot:       projectRoot,
		projectSkillsRoot: defaultProjectSkillsRoot(projectRoot),
		superDolphinHome:  newTestSuperDolphinHome(t),
		http:              &http.Client{},
		mirrorTargets: []SkillMirrorTarget{{
			TargetID:        "claude:project:spoofed",
			Provider:        SkillProviderClaude,
			Scope:           skillScopeProject,
			Root:            outsideRoot,
			CanonicalRootID: "spoofed",
		}},
	}

	out, err := svc.WriteLocal(skillTestContext(projectRoot), "build", "---\nname: build\n---\nbody", skillScopeProject)
	if err != nil {
		t.Fatalf("WriteLocal() error = %v", err)
	}

	report := mustMirrorPublishReport(t, out.(map[string]any))
	assertPublishedReportItem(t, report.Published, "claude:project:"+RepoFingerprint(projectRoot), SkillProviderClaude, skillScopeProject, "build", "project/build")
	assertMissing(t, filepath.Join(outsideRoot, "build", skillMainFile))
	if _, err := readSkillMirrorManifest(filepath.Join(providerProjectMirrorRoot(SkillProviderCodex, projectRoot), skillMirrorManifestFile)); err != nil {
		t.Fatalf("read codex self manifest: %v", err)
	}
}

func TestWriteLocalPublishConflictBlocksBeforeCanonicalWrite(t *testing.T) {
	projectRoot := t.TempDir()
	superDolphinHome := filepath.Join(t.TempDir(), ".super-dolphin")
	writeSkillWithSupportFiles(t, filepath.Join(projectRoot, ".agents", "skills", "old"), "old")
	writeSkillWithSupportFiles(t, filepath.Join(superDolphinHome, "skills", "personal", "user", "build"), "build")
	svc := &service{
		projectRoot:       projectRoot,
		projectSkillsRoot: defaultProjectSkillsRoot(projectRoot),
		superDolphinHome:  superDolphinHome,
		http:              &http.Client{},
	}
	initial, err := svc.WriteLocal(skillTestContext(projectRoot), "old", "---\nname: old\n---\nold", skillScopeProject)
	if err != nil {
		t.Fatalf("WriteLocal(old) error = %v", err)
	}
	initialReport := mustMirrorPublishReport(t, initial.(map[string]any))
	assertPublishedReportItem(t, initialReport.Published, "claude:project:"+RepoFingerprint(projectRoot), SkillProviderClaude, skillScopeProject, "old", "project/old")
	assertMirrorFile(t, filepath.Join(projectRoot, ".claude", "skills", "old", skillMainFile), false)
	if err := os.RemoveAll(filepath.Join(projectRoot, ".agents", "skills", "old")); err != nil {
		t.Fatalf("RemoveAll old canonical skill: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(projectRoot, ".agents", "skills", "safe"), 0o755); err != nil {
		t.Fatalf("MkdirAll safe canonical skill: %v", err)
	}
	if err := os.WriteFile(filepath.Join(projectRoot, ".agents", "skills", "safe", skillMainFile), []byte("---\nname: safe\n---\nsafe"), 0o644); err != nil {
		t.Fatalf("WriteFile safe canonical skill: %v", err)
	}

	_, err = svc.WriteLocal(skillTestContext(projectRoot), "build", "---\nname: build\n---\nproject", skillScopeProject)
	assertMirrorBlockingErrorContains(t, err, "canonical_deleted_with_drift")
	assertMissing(t, filepath.Join(projectRoot, ".agents", "skills", "build", skillMainFile))
	assertMissing(t, filepath.Join(projectRoot, ".claude", "skills", "build", skillMainFile))
	assertFileContent(t, filepath.Join(projectRoot, ".claude", "skills", "old", skillMainFile), "---\nname: old\n---\nold")
	assertMissing(t, filepath.Join(projectRoot, ".claude", "skills", "safe", skillMainFile))
}

func TestWriteLocalPublishConflictBlocksCanonicalWrite(t *testing.T) {
	projectRoot := t.TempDir()
	claudeMirror := filepath.Join(projectRoot, ".claude", "skills", "build")
	mustMkdirAll(t, claudeMirror)
	mustWriteFile(t, filepath.Join(claudeMirror, skillMainFile), "unmanaged")
	svc := &service{projectRoot: projectRoot, projectSkillsRoot: defaultProjectSkillsRoot(projectRoot), superDolphinHome: newTestSuperDolphinHome(t), http: &http.Client{}}

	_, err := svc.WriteLocal(skillTestContext(projectRoot), "build", "---\nname: build\n---\ncanonical", skillScopeProject)
	assertMirrorBlockingErrorContains(t, err, "unmanaged")
	assertMissing(t, filepath.Join(projectRoot, ".agents", "skills", "build", skillMainFile))
	assertFileContent(t, filepath.Join(claudeMirror, skillMainFile), "unmanaged")
}

func TestWriteLocalPublishErrorBlocksAndRollsBackCanonical(t *testing.T) {
	projectRoot := t.TempDir()
	claudeHome := filepath.Join(projectRoot, ".claude")
	mustMkdirAll(t, claudeHome)
	if err := os.Chmod(claudeHome, 0o555); err != nil {
		t.Fatalf("Chmod readonly Claude home: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(claudeHome, 0o755) })
	svc := &service{projectRoot: projectRoot, projectSkillsRoot: defaultProjectSkillsRoot(projectRoot), superDolphinHome: newTestSuperDolphinHome(t), http: &http.Client{}}

	_, err := svc.WriteLocal(skillTestContext(projectRoot), "build", "---\nname: build\n---\ncanonical", skillScopeProject)
	assertMirrorPublishBlockingError(t, err)
	assertMissing(t, filepath.Join(projectRoot, ".agents", "skills", "build", skillMainFile))
}

func TestDeleteLocalPublishErrorBlocksAndRestoresCanonical(t *testing.T) {
	projectRoot := t.TempDir()
	svc := &service{projectRoot: projectRoot, projectSkillsRoot: defaultProjectSkillsRoot(projectRoot), superDolphinHome: newTestSuperDolphinHome(t), http: &http.Client{}}
	if _, err := svc.WriteLocal(skillTestContext(projectRoot), "build", "---\nname: build\n---\nbody", skillScopeProject); err != nil {
		t.Fatalf("WriteLocal() error = %v", err)
	}
	claudeRoot := providerProjectMirrorRoot(SkillProviderClaude, projectRoot)
	if err := os.Chmod(claudeRoot, 0o555); err != nil {
		t.Fatalf("Chmod readonly Claude mirror root: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(claudeRoot, 0o755) })

	_, err := svc.DeleteLocal(skillTestContext(projectRoot), DeleteSkillParams{Name: "build", Scope: skillScopeProject})
	assertMirrorPublishBlockingError(t, err)
	assertFileContent(t, filepath.Join(projectRoot, ".agents", "skills", "build", skillMainFile), "---\nname: build\n---\nbody")
}

func assertMirrorPublishBlockingError(t *testing.T, err error) {
	t.Helper()

	assertMirrorBlockingErrorContains(t, err, "publish_error")
}

func assertMirrorBlockingErrorContains(t *testing.T, err error, wants ...string) {
	t.Helper()

	if err == nil {
		t.Fatal("mirror blocking error = nil")
	}
	for _, want := range append([]string{"mirror"}, wants...) {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("mirror blocking error = %q, want detail %q", err.Error(), want)
		}
	}
}

func TestWriteLocalPersonalPublishesUserGlobalMirrors(t *testing.T) {
	setSkillTestUserHome(t)
	projectRoot := t.TempDir()
	superDolphinHome := filepath.Join(t.TempDir(), ".super-dolphin")
	svc := &service{
		projectRoot:       projectRoot,
		projectSkillsRoot: defaultProjectSkillsRoot(projectRoot),
		superDolphinHome:  superDolphinHome,
		auditStore:        &capturingSkillAuditStore{},
	}

	out, err := svc.WriteLocal(skillTestContext(projectRoot), "notes", "---\nname: notes\n---\nbody", skillScopePersonal, personalSkillTypeUser)
	if err != nil {
		t.Fatalf("WriteLocal(personal) error = %v", err)
	}

	result := out.(map[string]any)
	report := mustMirrorPublishReport(t, result)
	owner, err := resolveOwnerIdentity(superDolphinHome, defaultOwnerOSUID(), defaultAppProfile())
	if err != nil {
		t.Fatalf("resolve owner: %v", err)
	}
	assertPublishedReportItem(t, report.Published, "claude:user-global:"+owner.OwnerKey, SkillProviderClaude, skillScopePersonal, "notes", "personal/user/notes")
	assertPublishedReportItem(t, report.Published, "codex:user-global:"+owner.OwnerKey, SkillProviderCodex, skillScopePersonal, "notes", "personal/user/notes")
	assertFileContent(t, filepath.Join(providerPersonalMirrorRoot(SkillProviderClaude), "notes", skillMainFile), "---\nname: notes\n---\nbody")
	assertFileContent(t, filepath.Join(providerPersonalMirrorRoot(SkillProviderCodex), "notes", skillMainFile), "---\nname: notes\n---\nbody")
}

func TestDeleteLocalRemovesOwnedProjectMirrors(t *testing.T) {
	projectRoot := t.TempDir()
	svc := &service{projectRoot: projectRoot, projectSkillsRoot: defaultProjectSkillsRoot(projectRoot), superDolphinHome: newTestSuperDolphinHome(t), http: &http.Client{}}
	if _, err := svc.WriteLocal(skillTestContext(projectRoot), "build", "---\nname: build\n---\nbody", skillScopeProject); err != nil {
		t.Fatalf("WriteLocal() error = %v", err)
	}

	out, err := svc.DeleteLocal(skillTestContext(projectRoot), DeleteSkillParams{Name: "build", Scope: skillScopeProject})
	if err != nil {
		t.Fatalf("DeleteLocal() error = %v", err)
	}

	result := out.(map[string]any)
	report := mustMirrorPublishReport(t, result)
	assertDeletedReportItem(t, report.Deleted, "claude:project:"+RepoFingerprint(projectRoot), SkillProviderClaude, skillScopeProject, "build", "project/build")
	assertMissing(t, filepath.Join(projectRoot, ".claude", "skills", "build"))
	assertMissing(t, filepath.Join(providerProjectMirrorRoot(SkillProviderCodex, projectRoot), "build"))
}

func TestReadLocalAcceptsPathInsideProjectSkillsRoot(t *testing.T) {
	t.Parallel()

	systemRoot := t.TempDir()
	projectRoot := t.TempDir()
	projectSkillsRoot := defaultProjectSkillsRoot(projectRoot)
	skillPath := filepath.Join(projectSkillsRoot, "demo", skillMainFile)
	if err := os.MkdirAll(filepath.Dir(skillPath), 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(skillPath, []byte("# demo"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	svc := &service{root: systemRoot, projectRoot: projectRoot, projectSkillsRoot: projectSkillsRoot, http: &http.Client{}}

	out, err := svc.ReadLocal(skillTestContext(projectRoot), skillPath)
	if err != nil {
		t.Fatalf("ReadLocal() error = %v", err)
	}
	result, ok := out.(map[string]any)
	if !ok {
		t.Fatalf("ReadLocal() result type = %T", out)
	}
	skill, ok := result["skill"].(map[string]any)
	if !ok {
		t.Fatalf("ReadLocal() skill type = %T", result["skill"])
	}
	if got, _ := skill["content"].(string); got != "# demo" {
		t.Fatalf("ReadLocal() content = %q, want # demo", got)
	}
}

func TestReadLocalGeneratedSummarySkipsFrontmatter(t *testing.T) {
	t.Parallel()

	systemRoot := t.TempDir()
	projectRoot := t.TempDir()
	projectSkillsRoot := defaultProjectSkillsRoot(projectRoot)
	skillPath := filepath.Join(projectSkillsRoot, "demo", skillMainFile)
	if err := os.MkdirAll(filepath.Dir(skillPath), 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	content := "---\nname: demo\n---\n# Demo\nUse this skill to verify generated summaries.\n"
	if err := os.WriteFile(skillPath, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	svc := &service{root: systemRoot, projectRoot: projectRoot, projectSkillsRoot: projectSkillsRoot, http: &http.Client{}}

	out, err := svc.ReadLocal(skillTestContext(projectRoot), skillPath)
	if err != nil {
		t.Fatalf("ReadLocal() error = %v", err)
	}
	result, ok := out.(map[string]any)
	if !ok {
		t.Fatalf("ReadLocal() result type = %T", out)
	}
	skill, ok := result["skill"].(map[string]any)
	if !ok {
		t.Fatalf("ReadLocal() skill type = %T", result["skill"])
	}
	if got, _ := skill["summary"].(string); got != "Use this skill to verify generated summaries." {
		t.Fatalf("ReadLocal() summary = %q, want generated body summary", got)
	}
	if got, _ := skill["summary_source"].(string); got != "generated" {
		t.Fatalf("ReadLocal() summary_source = %q, want generated", got)
	}
}

func TestReadLocalSummaryPrefersDescriptionOverInternalMarkers(t *testing.T) {
	t.Parallel()

	systemRoot := t.TempDir()
	projectRoot := t.TempDir()
	projectSkillsRoot := defaultProjectSkillsRoot(projectRoot)
	skillPath := filepath.Join(projectSkillsRoot, "demo", skillMainFile)
	if err := os.MkdirAll(filepath.Dir(skillPath), 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	content := "---\nname: demo\ndescription: Use skills before responding\nsummary: <SUBAGENT-STOP>\n---\n\n<SUBAGENT-STOP>\nskip for subagents\n</SUBAGENT-STOP>\n\n## Body\n"
	if err := os.WriteFile(skillPath, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	svc := &service{root: systemRoot, projectRoot: projectRoot, projectSkillsRoot: projectSkillsRoot, http: &http.Client{}}

	out, err := svc.ReadLocal(skillTestContext(projectRoot), skillPath)
	if err != nil {
		t.Fatalf("ReadLocal() error = %v", err)
	}
	result := out.(map[string]any)
	skill := result["skill"].(map[string]any)
	if got, _ := skill["summary"].(string); got != "Use skills before responding" {
		t.Fatalf("ReadLocal() summary = %q, want description", got)
	}
	if got, _ := skill["summary_source"].(string); got != "description" {
		t.Fatalf("ReadLocal() summary_source = %q, want description", got)
	}
}

func TestReadLocalGeneratedSummarySkipsInternalMarkerBlocks(t *testing.T) {
	t.Parallel()

	systemRoot := t.TempDir()
	projectRoot := t.TempDir()
	projectSkillsRoot := defaultProjectSkillsRoot(projectRoot)
	skillPath := filepath.Join(projectSkillsRoot, "demo", skillMainFile)
	if err := os.MkdirAll(filepath.Dir(skillPath), 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	content := "---\nname: demo\n---\n\n<SUBAGENT-STOP>\nskip for subagents\n</SUBAGENT-STOP>\n\nActual summary from body.\n"
	if err := os.WriteFile(skillPath, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	svc := &service{root: systemRoot, projectRoot: projectRoot, projectSkillsRoot: projectSkillsRoot, http: &http.Client{}}

	out, err := svc.ReadLocal(skillTestContext(projectRoot), skillPath)
	if err != nil {
		t.Fatalf("ReadLocal() error = %v", err)
	}
	result := out.(map[string]any)
	skill := result["skill"].(map[string]any)
	if got, _ := skill["summary"].(string); got != "Actual summary from body." {
		t.Fatalf("ReadLocal() summary = %q, want marker block skipped", got)
	}
	if got, _ := skill["summary_source"].(string); got != "generated" {
		t.Fatalf("ReadLocal() summary_source = %q, want generated", got)
	}
}

func TestListSkillsIgnoresLegacySystemRoot(t *testing.T) {
	t.Parallel()

	systemRoot := t.TempDir()
	projectRoot := t.TempDir()
	projectSkillsRoot := defaultProjectSkillsRoot(projectRoot)
	writeTestSkill(t, systemRoot, "from-system", "# system")
	writeTestSkill(t, projectSkillsRoot, "from-project", "# project")
	svc := &service{root: systemRoot, projectRoot: projectRoot, projectSkillsRoot: projectSkillsRoot, http: &http.Client{}}

	skills, err := svc.ListSkills(skillTestContext(projectRoot))
	if err != nil {
		t.Fatalf("ListSkills() error = %v", err)
	}
	names := make(map[string]bool, len(skills))
	for _, skill := range skills {
		names[skill.Name] = true
	}
	if names["from-system"] || !names["from-project"] {
		t.Fatalf("ListSkills() names = %#v, want project only", names)
	}
}

func TestReadRemoteHonorsContextCancellation(t *testing.T) {
	t.Parallel()

	svc := newTestSkillService(t)
	svc.http = &http.Client{Transport: skillRoundTripFunc(func(req *http.Request) (*http.Response, error) {
		select {
		case <-req.Context().Done():
			return nil, req.Context().Err()
		case <-time.After(200 * time.Millisecond):
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader("# remote")),
				Header:     make(http.Header),
				Request:    req,
			}, nil
		}
	})}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()

	_, err := svc.ReadRemote(ctx, "https://example.com/skill.md")
	if err == nil {
		t.Fatal("ReadRemote() error = nil, want context cancellation")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("ReadRemote() error = %v, want context deadline exceeded", err)
	}
}

func TestReadRemoteRejectsOversizeBody(t *testing.T) {
	t.Parallel()

	svc := newTestSkillService(t)
	svc.http = &http.Client{Transport: skillRoundTripFunc(func(req *http.Request) (*http.Response, error) {
		body := bytes.Repeat([]byte("x"), maxSkillFileBytes+1)
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(bytes.NewReader(body)),
			Header:     make(http.Header),
			Request:    req,
		}, nil
	})}

	_, err := svc.ReadRemote(context.Background(), "https://example.com/SKILL.md")
	if err == nil || !strings.Contains(err.Error(), "remote skill too large") {
		t.Fatalf("ReadRemote() error = %v, want remote skill too large", err)
	}
}

type skillRoundTripFunc func(*http.Request) (*http.Response, error)

func (f skillRoundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func TestReadRemoteRejectsLoopbackURL(t *testing.T) {
	t.Parallel()

	called := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		_, _ = w.Write([]byte("# private skill"))
	}))
	defer server.Close()

	svc := newTestSkillService(t)
	_, err := svc.ReadRemote(context.Background(), server.URL)
	if err == nil || !strings.Contains(err.Error(), "private network") {
		t.Fatalf("ReadRemote() error = %v, want private network rejection", err)
	}
	if called {
		t.Fatal("ReadRemote() contacted loopback server before rejecting URL")
	}
}
