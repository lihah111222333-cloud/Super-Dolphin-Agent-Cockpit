package skill

import (
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestListSkillsUnionsProjectAndSystemRoots(t *testing.T) {
	t.Parallel()

	systemRoot := t.TempDir()
	projectRoot := filepath.Join(t.TempDir(), "repo-a")
	projectSkillsRoot := defaultProjectSkillsRoot(projectRoot)
	if err := writeProjectSkillRoot(projectSkillsRoot); err != nil {
		t.Fatalf("prepare project root: %v", err)
	}
	writeTestSkill(t, projectSkillsRoot, "project-local", "---\nname: project-local\nsummary: local\n---\nbody")
	writeTestSkill(t, systemRoot, "system-global", "---\nname: system-global\nsummary: global\n---\nbody")

	svc := &service{
		root:              systemRoot,
		projectRoot:       projectRoot,
		projectSkillsRoot: projectSkillsRoot,
		http:              &http.Client{},
	}

	skills, err := svc.ListSkills(skillTestContext(projectRoot))
	if err != nil {
		t.Fatalf("ListSkills() error = %v", err)
	}

	gotTrust := make(map[string]TrustScope, len(skills))
	for _, item := range skills {
		gotTrust[item.Name] = item.Trust
	}

	if gotTrust["project-local"] != TrustProject {
		t.Fatalf("project-local trust = %q, want project", gotTrust["project-local"])
	}
	if gotTrust["system-global"] != TrustUser {
		t.Fatalf("system-global trust = %q, want user", gotTrust["system-global"])
	}
}

func TestWriteLocalScopeRoutesProjectSystemAndDefaultProject(t *testing.T) {
	t.Parallel()

	systemRoot := t.TempDir()
	projectRoot := filepath.Join(t.TempDir(), "repo-a")
	svc := &service{
		root:              systemRoot,
		projectRoot:       projectRoot,
		projectSkillsRoot: defaultProjectSkillsRoot(projectRoot),
		http:              &http.Client{},
	}

	projectOut, err := svc.WriteLocal(skillTestContext(projectRoot), "project-skill", "# project", "project")
	if err != nil {
		t.Fatalf("WriteLocal(project) error = %v", err)
	}
	projectPath, _ := projectOut.(map[string]any)["path"].(string)
	wantProjectPath := filepath.Join(projectRoot, ".agent", "skills", "project-skill", skillMainFile)
	if projectPath != wantProjectPath {
		t.Fatalf("project path = %q, want %q", projectPath, wantProjectPath)
	}

	if _, err := svc.WriteLocal(skillTestContext(projectRoot), "system-skill", "# system", "system"); !errors.Is(err, ErrSkillSystemReviewRequired) {
		t.Fatalf("WriteLocal(system) error = %v, want ErrSkillSystemReviewRequired", err)
	}

	defaultOut, err := svc.WriteLocal(skillTestContext(projectRoot), "default-skill", "# default")
	if err != nil {
		t.Fatalf("WriteLocal(default) error = %v", err)
	}
	defaultPath, _ := defaultOut.(map[string]any)["path"].(string)
	wantDefaultPath := filepath.Join(projectRoot, ".agent", "skills", "default-skill", skillMainFile)
	if defaultPath != wantDefaultPath {
		t.Fatalf("default path = %q, want %q", defaultPath, wantDefaultPath)
	}
}

func TestImportLocalDirScopeRoutesDefaultProjectAndSystemGlobal(t *testing.T) {
	t.Parallel()

	systemRoot := t.TempDir()
	projectRoot := filepath.Join(t.TempDir(), "repo-a")
	svc := &service{
		root:              systemRoot,
		projectRoot:       projectRoot,
		projectSkillsRoot: defaultProjectSkillsRoot(projectRoot),
		http:              &http.Client{},
	}

	projectSource := filepath.Join(t.TempDir(), "project-import")
	if err := os.MkdirAll(projectSource, 0o755); err != nil {
		t.Fatalf("mkdir project source: %v", err)
	}
	if err := os.WriteFile(filepath.Join(projectSource, skillMainFile), []byte("# project"), 0o644); err != nil {
		t.Fatalf("write project source: %v", err)
	}
	projectOut, err := svc.ImportLocalDir(skillTestContext(projectRoot), importSkillDirParams{Path: projectSource})
	if err != nil {
		t.Fatalf("ImportLocalDir(default) error = %v", err)
	}
	projectImported := projectOut.(map[string]any)["imported"].([]map[string]any)
	projectSkillFile, _ := projectImported[0]["skill_file"].(string)
	wantProjectSkillFile := filepath.Join(projectRoot, ".agent", "skills", "project-import", skillMainFile)
	if projectSkillFile != wantProjectSkillFile {
		t.Fatalf("default import path = %q, want %q", projectSkillFile, wantProjectSkillFile)
	}

	systemSource := filepath.Join(t.TempDir(), "system-import")
	if err := os.MkdirAll(systemSource, 0o755); err != nil {
		t.Fatalf("mkdir system source: %v", err)
	}
	if err := os.WriteFile(filepath.Join(systemSource, skillMainFile), []byte("# system"), 0o644); err != nil {
		t.Fatalf("write system source: %v", err)
	}
	systemOut, err := svc.ImportLocalDir(skillTestContext(projectRoot), importSkillDirParams{Path: systemSource, Scope: "system"})
	if err != nil {
		t.Fatalf("ImportLocalDir(system) wrapper error = %v", err)
	}
	failures := systemOut.(map[string]any)["failures"].([]map[string]any)
	if len(failures) != 1 || !strings.Contains(failures[0]["error"].(string), ErrSkillSystemReviewRequired.Error()) {
		t.Fatalf("system import failures = %#v, want review required", failures)
	}
}

func writeProjectSkillRoot(root string) error {
	return os.MkdirAll(root, 0o755)
}

func TestSkillSystemScopeWriteRequiresReview(t *testing.T) {
	t.Parallel()

	projectRoot := filepath.Join(t.TempDir(), "repo-a")
	svc := &service{
		root:              t.TempDir(),
		projectRoot:       projectRoot,
		projectSkillsRoot: defaultProjectSkillsRoot(projectRoot),
		http:              &http.Client{},
	}
	_, err := svc.WriteLocal(skillTestContext(projectRoot), "blocked-system", "# blocked", skillScopeSystem)
	if !errors.Is(err, ErrSkillSystemReviewRequired) {
		t.Fatalf("WriteLocal(system) error = %v, want ErrSkillSystemReviewRequired", err)
	}
	if _, statErr := os.Stat(filepath.Join(svc.root, "blocked-system", skillMainFile)); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("system write should not create file, stat err=%v", statErr)
	}
}
