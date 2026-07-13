package skill

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSeedBuiltInSkillsIsCatalogOnlyAndDoesNotWritePersonalHub(t *testing.T) {
	home := t.TempDir()
	existingDir := filepath.Join(home, "skills", "personal", personalSkillTypeHub, "测试驱动开发")
	if err := os.MkdirAll(existingDir, 0o755); err != nil {
		t.Fatalf("mkdir existing skill: %v", err)
	}
	const custom = "---\nname: 测试驱动开发\n---\n\ncustom user edit"
	if err := os.WriteFile(filepath.Join(existingDir, skillMainFile), []byte(custom), 0o644); err != nil {
		t.Fatalf("write existing skill: %v", err)
	}

	written, err := seedBuiltInSkills(home)
	if err != nil {
		t.Fatalf("seedBuiltInSkills: %v", err)
	}
	if written != 0 {
		t.Fatalf("seedBuiltInSkills wrote %d skills, want catalog-only no-op", written)
	}
	assertSkillFileContent(t, filepath.Join(existingDir, skillMainFile), custom)
	assertSkillFileMissing(t, filepath.Join(home, "skills", "personal", personalSkillTypeHub, "使用超能力", skillMainFile))
}

func TestEmbeddedBuiltInSkillsDoNotEnterRuntimeCanonicalSet(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	superDolphinHome := filepath.Join(home, ".super-dolphin")
	t.Setenv("SUPER_DOLPHIN_HOME", superDolphinHome)

	names, err := listBuiltInSkillNames()
	if err != nil {
		t.Fatalf("listBuiltInSkillNames: %v", err)
	}
	if len(names) == 0 {
		t.Fatal("listBuiltInSkillNames returned no embedded skills")
	}
	embeddedName := names[0]
	projectRoot := filepath.Join(t.TempDir(), "repo")
	projectSkillsRoot := defaultProjectSkillsRoot(projectRoot)
	writeTestSkill(t, projectSkillsRoot, embeddedName, "---\nname: "+embeddedName+"\n---\n# project")

	svc := &service{
		projectRoot:       projectRoot,
		projectSkillsRoot: projectSkillsRoot,
		superDolphinHome:  superDolphinHome,
	}
	skills, err := svc.ListSkills(skillTestContext(projectRoot))
	if err != nil {
		t.Fatalf("ListSkills: %v", err)
	}
	if matches := countSkillsNamed(skills, embeddedName); matches != 1 {
		t.Fatalf("ListSkills returned %d entries named %q, want exactly the project skill", matches, embeddedName)
	}

	resolutions, err := svc.listSkillResolutions(projectRoot)
	if err != nil {
		t.Fatalf("listSkillResolutions: %v", err)
	}
	assertNoSameNameResolutionForSkill(t, resolutions.Items, embeddedName)
}

func countSkillsNamed(skills []SkillInfo, name string) int {
	matches := 0
	for _, item := range skills {
		if item.Name == name {
			matches++
		}
	}
	return matches
}

func assertNoSameNameResolutionForSkill(t *testing.T, items []skillResolutionItem, name string) {
	t.Helper()
	for _, item := range items {
		if item.Kind == skillConflictSameName && item.Name == name {
			t.Fatalf("embedded skill %q created same-name resolution: %+v", name, item)
		}
	}
}

func assertSkillFileMissing(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Stat(path); err == nil {
		t.Fatalf("skill file exists, want missing: %s", path)
	} else if !os.IsNotExist(err) {
		t.Fatalf("stat skill file %s: %v", path, err)
	}
}

func assertSkillFileContent(t *testing.T, path, want string) {
	t.Helper()
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read skill file %s: %v", path, err)
	}
	if string(got) != want {
		t.Fatalf("skill file %s overwritten:\n%s", path, got)
	}
}
