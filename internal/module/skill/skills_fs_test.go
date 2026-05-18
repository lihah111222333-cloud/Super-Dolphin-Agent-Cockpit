package skill

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
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
		t.Fatalf("Symlink SKILL.md: %v", err)
	}
	svc := &service{projectRoot: projectRoot, projectSkillsRoot: skillsRoot, superDolphinHome: newTestSuperDolphinHome(t), http: &http.Client{}}

	_, err := svc.WriteLocal(skillTestContext(projectRoot), "escape/SKILL.md", "---\nname: escape\n---\nbody", skillScopeProject)

	if err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("WriteLocal SKILL.md symlink error = %v, want symlink rejection", err)
	}
	assertFileContent(t, outsideFile, "outside")
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
	assertPublishedReportItem(t, report.Published, "codex:project:"+RepoFingerprint(projectRoot), SkillProviderCodex, skillScopeProject, "build", "project/build")
	assertFileContent(t, filepath.Join(projectRoot, ".claude", "skills", "build", skillMainFile), "---\nname: build\n---\nbody")
	assertFileContent(t, filepath.Join(projectRoot, ".codex", "skills", "build", skillMainFile), "---\nname: build\n---\nbody")
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
	assertPublishedReportItem(t, report.Published, "codex:project:"+RepoFingerprint(projectRoot), SkillProviderCodex, skillScopeProject, "build", "project/build")
	assertMissing(t, filepath.Join(outsideRoot, "build", skillMainFile))
}

func TestWriteLocalPublishSkipsCanonicalSameNameConflicts(t *testing.T) {
	projectRoot := t.TempDir()
	superDolphinHome := filepath.Join(t.TempDir(), ".super-dolphin")
	writeSkillWithSupportFiles(t, filepath.Join(projectRoot, ".agent", "skills", "old"), "old")
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
	if err := os.RemoveAll(filepath.Join(projectRoot, ".agent", "skills", "old")); err != nil {
		t.Fatalf("RemoveAll old canonical skill: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(projectRoot, ".agent", "skills", "safe"), 0o755); err != nil {
		t.Fatalf("MkdirAll safe canonical skill: %v", err)
	}
	if err := os.WriteFile(filepath.Join(projectRoot, ".agent", "skills", "safe", skillMainFile), []byte("---\nname: safe\n---\nsafe"), 0o644); err != nil {
		t.Fatalf("WriteFile safe canonical skill: %v", err)
	}

	out, err := svc.WriteLocal(skillTestContext(projectRoot), "build", "---\nname: build\n---\nproject", skillScopeProject)
	if err != nil {
		t.Fatalf("WriteLocal() error = %v", err)
	}

	report := mustMirrorPublishReport(t, out.(map[string]any))
	assertConflictReportItem(t, report.Conflicts, "claude:project:"+RepoFingerprint(projectRoot), SkillProviderClaude, skillScopeProject, "build", "", "same_name")
	assertFileContent(t, filepath.Join(projectRoot, ".agent", "skills", "build", skillMainFile), "---\nname: build\n---\nproject")
	assertMissing(t, filepath.Join(projectRoot, ".claude", "skills", "build", skillMainFile))
	assertFileContent(t, filepath.Join(projectRoot, ".claude", "skills", "old", skillMainFile), "---\nname: old\n---\nold")
	assertMissing(t, filepath.Join(projectRoot, ".claude", "skills", "safe", skillMainFile))
	if len(report.Published) != 0 || len(report.Deleted) != 0 {
		t.Fatalf("same-name write-time publish wrote mirrors: published=%+v deleted=%+v", report.Published, report.Deleted)
	}
}

func TestWriteLocalPublishConflictKeepsCanonicalResult(t *testing.T) {
	projectRoot := t.TempDir()
	claudeMirror := filepath.Join(projectRoot, ".claude", "skills", "build")
	mustMkdirAll(t, claudeMirror)
	mustWriteFile(t, filepath.Join(claudeMirror, skillMainFile), "unmanaged")
	svc := &service{projectRoot: projectRoot, projectSkillsRoot: defaultProjectSkillsRoot(projectRoot), superDolphinHome: newTestSuperDolphinHome(t), http: &http.Client{}}

	out, err := svc.WriteLocal(skillTestContext(projectRoot), "build", "---\nname: build\n---\ncanonical", skillScopeProject)
	if err != nil {
		t.Fatalf("WriteLocal() error = %v", err)
	}

	result := out.(map[string]any)
	report := mustMirrorPublishReport(t, result)
	assertConflictReportItem(t, report.Conflicts, "claude:project:"+RepoFingerprint(projectRoot), SkillProviderClaude, skillScopeProject, "build", "project/build", "unmanaged")
	assertFileContent(t, filepath.Join(projectRoot, ".agent", "skills", "build", skillMainFile), "---\nname: build\n---\ncanonical")
	assertFileContent(t, filepath.Join(claudeMirror, skillMainFile), "unmanaged")
}

func TestWriteLocalPublishErrorReportIncludesDetail(t *testing.T) {
	projectRoot := t.TempDir()
	legacyRoot := filepath.Join(projectRoot, ".claude", "skills")
	mustMkdirAll(t, filepath.Dir(legacyRoot))
	if err := os.Symlink(filepath.Join(t.TempDir(), "skills-cache"), legacyRoot); err != nil {
		t.Fatalf("Symlink legacy root: %v", err)
	}
	svc := &service{projectRoot: projectRoot, projectSkillsRoot: defaultProjectSkillsRoot(projectRoot), superDolphinHome: newTestSuperDolphinHome(t), http: &http.Client{}}

	out, err := svc.WriteLocal(skillTestContext(projectRoot), "build", "---\nname: build\n---\ncanonical", skillScopeProject)
	if err != nil {
		t.Fatalf("WriteLocal() error = %v", err)
	}

	report := mustMirrorPublishReport(t, out.(map[string]any))
	item := findConflictReportItem(t, report.Conflicts, "claude:project:"+RepoFingerprint(projectRoot), SkillProviderClaude, skillScopeProject, "build", "project/build", "publish_error")
	if !strings.Contains(item.Error, "symlink") {
		t.Fatalf("publish error detail = %q, want symlink detail", item.Error)
	}
	assertFileContent(t, filepath.Join(projectRoot, ".agent", "skills", "build", skillMainFile), "---\nname: build\n---\ncanonical")
}

func TestWriteLocalPersonalPublishesAppManagedMirrors(t *testing.T) {
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
	assertPublishedReportItem(t, report.Published, "claude:app-managed:"+owner.OwnerKey, SkillProviderClaude, skillScopePersonal, "notes", "personal/user/notes")
	assertPublishedReportItem(t, report.Published, "codex:app-managed:"+owner.OwnerKey, SkillProviderCodex, skillScopePersonal, "notes", "personal/user/notes")
	assertFileContent(t, filepath.Join(superDolphinHome, "providers", "claude", "skills", "notes", skillMainFile), "---\nname: notes\n---\nbody")
	assertFileContent(t, filepath.Join(superDolphinHome, "providers", "codex", "skills", "notes", skillMainFile), "---\nname: notes\n---\nbody")
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
	assertMissing(t, filepath.Join(projectRoot, ".codex", "skills", "build"))
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

func TestExpandReturnsNotFoundForMissingSkill(t *testing.T) {
	t.Parallel()

	svc, _ := newExpandTestService(t)
	_, err := svc.Expand(expandTestContext(svc), skillExpandParams{Name: "ghost"})
	if !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("Expand() error = %v, want os.ErrNotExist", err)
	}
	if err == nil || err.Error() != "skill not found: ghost" {
		t.Fatalf("Expand() error text = %v, want skill not found", err)
	}
}

func TestExpandRejectsPathEscapeSection(t *testing.T) {
	t.Parallel()

	svc, root := newExpandTestService(t)
	writeExpandTestSkill(t, root, "demo", "---\nname: demo\n---\nbody")

	_, err := svc.Expand(expandTestContext(svc), skillExpandParams{Name: "demo", Section: "../escape"})
	if !errors.Is(err, errInvalidSkillExpandParam) {
		t.Fatalf("Expand() error = %v, want invalid params", err)
	}
}

func TestExpandFullSkillContentHashUsesPreTruncationBytes(t *testing.T) {
	t.Parallel()

	svc, root := newExpandTestService(t)
	content := "---\nname: demo\nsummary: short\n---\n## Usage\nhello world"
	path := writeExpandTestSkill(t, root, "demo", content)

	res, err := svc.Expand(expandTestContext(svc), skillExpandParams{Name: "demo", MaxBytes: 10})
	if err != nil {
		t.Fatalf("Expand() error = %v", err)
	}
	if res.Path != filepath.Join(path, skillMainFile) {
		t.Fatalf("Expand() path = %q", res.Path)
	}
	if res.Section != "" {
		t.Fatalf("Expand() section = %q, want empty", res.Section)
	}
	if !res.Truncated || res.Content != content[:10] {
		t.Fatalf("Expand() truncation = (%v, %q)", res.Truncated, res.Content)
	}
	if res.TotalBytes != int64(len(content)) {
		t.Fatalf("Expand() total_bytes = %d, want %d", res.TotalBytes, len(content))
	}
	wantHash := sha256.Sum256([]byte(content))
	if res.ContentHash != hex.EncodeToString(wantHash[:]) {
		t.Fatalf("Expand() content_hash = %q", res.ContentHash)
	}
}

func TestExpandMarkdownSectionTruncatesAndHashesSelection(t *testing.T) {
	t.Parallel()

	svc, root := newExpandTestService(t)
	body := "---\nname: demo\nsummary: short\n---\n## Intro\nhello\n\n### Details\n" + strings.Repeat("x", 32) + "\n\n## Done\nbye"
	path := writeExpandTestSkill(t, root, "demo", body)
	selected := "### Details\n" + strings.Repeat("x", 32)

	res, err := svc.Expand(expandTestContext(svc), skillExpandParams{Name: "demo", Section: "### Details", MaxBytes: 12})
	if err != nil {
		t.Fatalf("Expand() error = %v", err)
	}
	if res.Path != filepath.Join(path, skillMainFile) {
		t.Fatalf("Expand() path = %q", res.Path)
	}
	if res.Section != "### Details" {
		t.Fatalf("Expand() section = %q", res.Section)
	}
	if !res.Truncated || res.Content != selected[:12] {
		t.Fatalf("Expand() truncation = (%v, %q)", res.Truncated, res.Content)
	}
	if res.TotalBytes != int64(len(selected)) {
		t.Fatalf("Expand() total_bytes = %d, want %d", res.TotalBytes, len(selected))
	}
	wantHash := sha256.Sum256([]byte(selected))
	if res.ContentHash != hex.EncodeToString(wantHash[:]) {
		t.Fatalf("Expand() content_hash = %q", res.ContentHash)
	}
}

func TestReadRemoteHonorsContextCancellation(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
			return
		case <-time.After(200 * time.Millisecond):
			_, _ = w.Write([]byte("# remote"))
		}
	}))
	defer server.Close()

	svc := newTestSkillService(t)
	svc.http = &http.Client{Timeout: 15 * time.Second}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()

	_, err := svc.ReadRemote(ctx, server.URL)
	if err == nil {
		t.Fatal("ReadRemote() error = nil, want context cancellation")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("ReadRemote() error = %v, want context deadline exceeded", err)
	}
}
