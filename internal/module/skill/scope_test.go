package skill

import (
	"net/http"
	"os"
	"path/filepath"
	"testing"
)

func TestListSkillsUnionsProjectGlobalAndLegacyRoots(t *testing.T) {
	t.Parallel()

	systemRoot := t.TempDir()
	projectRoot := filepath.Join(t.TempDir(), "repo-a")
	projectSkillsRoot := defaultProjectSkillsRoot(projectRoot)
	if err := writeProjectSkillRoot(projectSkillsRoot); err != nil {
		t.Fatalf("prepare project root: %v", err)
	}
	writeTestSkill(t, projectSkillsRoot, "project-local", "---\nname: project-local\nsummary: local\n---\nbody")
	writeTestSkill(t, systemRoot, "system-global", "---\nname: system-global\nsummary: global\n---\nbody")
	writeScopedSystemSkill(t, systemRoot, projectRoot, "legacy-user", "---\nname: legacy-user\nsummary: legacy\n---\nbody")
	writeScopedSystemSkill(t, systemRoot, filepath.Join(t.TempDir(), "repo-b"), "legacy-other", "---\nname: legacy-other\nsummary: other\n---\nbody")

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
	if gotTrust["legacy-user"] != TrustUser {
		t.Fatalf("legacy-user trust = %q, want user", gotTrust["legacy-user"])
	}
	if _, ok := gotTrust["legacy-other"]; ok {
		t.Fatalf("legacy-other leaked into cwd-scoped listing: %#v", gotTrust)
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

	systemOut, err := svc.WriteLocal(skillTestContext(projectRoot), "system-skill", "# system", "system")
	if err != nil {
		t.Fatalf("WriteLocal(system) error = %v", err)
	}
	systemPath, _ := systemOut.(map[string]any)["path"].(string)
	wantSystemPath := filepath.Join(systemRoot, "system-skill", skillMainFile)
	if systemPath != wantSystemPath {
		t.Fatalf("system path = %q, want %q", systemPath, wantSystemPath)
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
		t.Fatalf("ImportLocalDir(system) error = %v", err)
	}
	systemImported := systemOut.(map[string]any)["imported"].([]map[string]any)
	systemSkillFile, _ := systemImported[0]["skill_file"].(string)
	wantSystemSkillFile := filepath.Join(systemRoot, "system-import", skillMainFile)
	if systemSkillFile != wantSystemSkillFile {
		t.Fatalf("system import path = %q, want %q", systemSkillFile, wantSystemSkillFile)
	}
}

func writeProjectSkillRoot(root string) error {
	return os.MkdirAll(root, 0o755)
}
