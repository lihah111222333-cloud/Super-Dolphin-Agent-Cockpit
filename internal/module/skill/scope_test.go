package skill

import (
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestListSkillsIgnoresSystemRootAndListsProjectRoot(t *testing.T) {
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
	if _, ok := gotTrust["system-global"]; ok {
		t.Fatalf("system-global should not be listed from legacy root: %#v", gotTrust)
	}
}

func TestWriteLocalRejectsEmptyScopeBeforeProjectWrite(t *testing.T) {
	t.Parallel()

	projectRoot := filepath.Join(t.TempDir(), "repo-a")
	svc := &service{
		projectRoot:       projectRoot,
		projectSkillsRoot: defaultProjectSkillsRoot(projectRoot),
		http:              &http.Client{},
	}

	_, err := svc.WriteLocal(skillTestContext(projectRoot), "empty-scope", "# blocked", "   ")
	if !errors.Is(err, ErrInvalidSkillScope) {
		t.Fatalf("WriteLocal(empty scope) error = %v, want ErrInvalidSkillScope", err)
	}
	if _, statErr := os.Stat(filepath.Join(projectRoot, ".agents", "skills", "empty-scope")); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("empty-scope write reached project storage: %v", statErr)
	}
}

func TestImportLocalDirRejectsEmptyScopeBeforeSourceValidation(t *testing.T) {
	t.Parallel()

	projectRoot := filepath.Join(t.TempDir(), "repo-a")
	svc := &service{
		projectRoot:       projectRoot,
		projectSkillsRoot: defaultProjectSkillsRoot(projectRoot),
		http:              &http.Client{},
	}

	_, err := svc.ImportLocalDir(skillTestContext(projectRoot), importSkillDirParams{Path: filepath.Join(t.TempDir(), "missing"), Scope: " "})
	if !errors.Is(err, ErrInvalidSkillScope) {
		t.Fatalf("ImportLocalDir(empty scope) error = %v, want ErrInvalidSkillScope", err)
	}
}

func TestWriteLocalScopeRoutesProjectSystemAndRejectsEmpty(t *testing.T) {
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
	wantProjectPath := filepath.Join(projectRoot, ".agents", "skills", "project-skill", skillMainFile)
	if projectPath != wantProjectPath {
		t.Fatalf("project path = %q, want %q", projectPath, wantProjectPath)
	}

	if _, err := svc.WriteLocal(skillTestContext(projectRoot), "system-skill", "# system", "system"); !errors.Is(err, ErrSkillSystemScopeRemoved) {
		t.Fatalf("WriteLocal(system) error = %v, want ErrSkillSystemScopeRemoved", err)
	}

	if _, err := svc.WriteLocal(skillTestContext(projectRoot), "default-skill", "# default"); !errors.Is(err, ErrInvalidSkillScope) {
		t.Fatalf("WriteLocal(empty scope) error = %v, want ErrInvalidSkillScope", err)
	}
	if _, err := os.Stat(filepath.Join(projectRoot, ".agents", "skills", "default-skill")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("empty scope reached default project path: %v", err)
	}
}

func TestImportLocalDirScopeRoutesProjectAndSystemGlobal(t *testing.T) {
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
	projectOut, err := svc.ImportLocalDir(skillTestContext(projectRoot), importSkillDirParams{Path: projectSource, Scope: skillScopeProject})
	if err != nil {
		t.Fatalf("ImportLocalDir(project) error = %v", err)
	}
	projectImported := projectOut.(map[string]any)["imported"].([]map[string]any)
	projectSkillFile, _ := projectImported[0]["skill_file"].(string)
	wantProjectSkillFile := filepath.Join(projectRoot, ".agents", "skills", "project-import", skillMainFile)
	if projectSkillFile != wantProjectSkillFile {
		t.Fatalf("project import path = %q, want %q", projectSkillFile, wantProjectSkillFile)
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
	if len(failures) != 1 || !strings.Contains(failures[0]["error"].(string), ErrSkillSystemScopeRemoved.Error()) {
		t.Fatalf("system import failures = %#v, want system scope removed", failures)
	}
}

func TestListSkillsIgnoresLegacySkillsRootAtRuntime(t *testing.T) {
	oldRoot := t.TempDir()
	projectRoot := filepath.Join(t.TempDir(), "repo-a")
	t.Setenv("SKILLS_ROOT", oldRoot)
	t.Setenv("SUPER_DOLPHIN_HOME", filepath.Join(t.TempDir(), ".super-dolphin"))
	writeTestSkill(t, oldRoot, "legacy-global", "---\nname: legacy-global\nsummary: legacy\n---\nbody")

	svc := NewService(projectRoot)
	skills, err := svc.ListSkills(skillTestContext(projectRoot))
	if err != nil {
		t.Fatalf("ListSkills() error = %v", err)
	}
	for _, item := range skills {
		if item.Name == "legacy-global" {
			t.Fatalf("ListSkills() included legacy SKILLS_ROOT skill: %+v", item)
		}
	}
}

func TestDeleteLocalStructuredTargetDoesNotCrossDeleteSameName(t *testing.T) {
	projectRoot := filepath.Join(t.TempDir(), "repo-a")
	home := t.TempDir()
	superDolphinHome := filepath.Join(home, ".super-dolphin")
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("SUPER_DOLPHIN_HOME", superDolphinHome)
	projectSkillsRoot := defaultProjectSkillsRoot(projectRoot)
	personalUserRoot := filepath.Join(superDolphinHome, "skills", "personal", personalSkillTypeUser)
	writeTestSkill(t, projectSkillsRoot, "build", "# project build")
	writeTestSkill(t, personalUserRoot, "build", "# personal build")
	svc := &service{
		projectRoot:       projectRoot,
		projectSkillsRoot: projectSkillsRoot,
		superDolphinHome:  superDolphinHome,
		http:              &http.Client{},
		auditStore:        &capturingSkillAuditStore{},
	}

	if _, err := svc.DeleteLocal(skillTestContext(projectRoot), DeleteSkillParams{Name: "build", Scope: skillScopeProject}); err != nil {
		t.Fatalf("DeleteLocal(project) error = %v", err)
	}
	assertMissing(t, filepath.Join(projectSkillsRoot, "build", skillMainFile))
	assertExists(t, filepath.Join(personalUserRoot, "build", skillMainFile))

	if _, err := svc.DeleteLocal(skillTestContext(projectRoot), DeleteSkillParams{Name: "build", Scope: skillScopePersonal, PersonalType: personalSkillTypeUser}); err != nil {
		t.Fatalf("DeleteLocal(personal/user) error = %v", err)
	}
	assertMissing(t, filepath.Join(personalUserRoot, "build", skillMainFile))
	assertArchiveContainsSkill(t, superDolphinHome, filepath.Join(skillScopePersonal, personalSkillTypeUser, "build"))
	for _, tc := range []struct {
		provider SkillProvider
		root     string
	}{
		{provider: SkillProviderClaude, root: filepath.Join(home, ".claude", "skills")},
		{provider: SkillProviderCodex, root: filepath.Join(home, ".agents", "skills")},
	} {
		manifest, err := readSkillMirrorManifest(filepath.Join(tc.root, skillMirrorManifestFile))
		if err != nil {
			t.Fatalf("read %s personal mirror manifest: %v", tc.provider, err)
		}
		if manifest.Scope != skillScopePersonal || manifest.Provider != string(tc.provider) {
			t.Fatalf("%s personal mirror manifest = %+v, want scope=%q provider=%q", tc.provider, manifest, skillScopePersonal, tc.provider)
		}
		if len(manifest.Skills) != 0 {
			t.Fatalf("%s personal mirror manifest skills = %+v, want empty after delete", tc.provider, manifest.Skills)
		}
		assertMissing(t, filepath.Join(tc.root, "build", skillMainFile))
	}
}

func writeProjectSkillRoot(root string) error {
	return os.MkdirAll(root, 0o755)
}

func assertExists(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("expected %s to exist: %v", path, err)
	}
}

func assertMissing(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected %s to be missing, stat err=%v", path, err)
	}
}

func assertArchiveContainsSkill(t *testing.T, home, archiveSuffix string) {
	t.Helper()
	archiveRoot := filepath.Join(home, "skills", ".archive")
	matches, err := filepath.Glob(filepath.Join(archiveRoot, "*", archiveSuffix, skillMainFile))
	if err != nil {
		t.Fatalf("glob archive: %v", err)
	}
	if len(matches) != 1 {
		t.Fatalf("archive matches = %v, want one SKILL.md under %s", matches, archiveSuffix)
	}
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
	if !errors.Is(err, ErrSkillSystemScopeRemoved) {
		t.Fatalf("WriteLocal(system) error = %v, want ErrSkillSystemScopeRemoved", err)
	}
	if _, statErr := os.Stat(filepath.Join(svc.root, "blocked-system", skillMainFile)); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("system write should not create file, stat err=%v", statErr)
	}
}
