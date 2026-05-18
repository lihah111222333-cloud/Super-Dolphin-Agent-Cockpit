package skill

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSeedBuiltInSkillsInstallsHubSkillsAndPreservesExisting(t *testing.T) {
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
	if written == 0 {
		t.Fatalf("seedBuiltInSkills wrote 0 skills, want missing built-ins installed")
	}
	assertSkillFileContent(t, filepath.Join(existingDir, skillMainFile), custom)
	assertSkillFileExists(t, filepath.Join(home, "skills", "personal", personalSkillTypeHub, "使用超能力", skillMainFile))
}

func assertSkillFileExists(t *testing.T, path string) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat seeded skill %s: %v", path, err)
	}
	if info.IsDir() {
		t.Fatalf("seeded skill path is directory: %s", path)
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
