package skill

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
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

func TestWorktreeSkillUsesShellSafeCreationRecipe(t *testing.T) {
	canonical := readRepoSkill(t, ".agents", "skills", "使用git工作区", skillMainFile)
	embeddedBytes, err := builtInSkillFS.ReadFile(builtInSkillRoot + "/使用git工作区/" + skillMainFile)
	if err != nil {
		t.Fatalf("read embedded worktree skill: %v", err)
	}
	embedded := string(embeddedBytes)

	for label, body := range map[string]string{
		"canonical": canonical,
		"embedded":  embedded,
	} {
		for _, want := range []string{
			`base_branch=$(git branch --show-current)`,
			`branch="codex/<short-task-name>"`,
			`path=".worktrees/<short-task-name>"`,
			`git worktree add "$path" -b "$branch" "$base_branch"`,
			"不要把 `git worktree add ...` 拼成字符串变量再执行",
			"如果必须动态组装命令，使用 shell 数组并以 `\"${cmd[@]}\"` 执行",
		} {
			if !strings.Contains(body, want) {
				t.Fatalf("%s worktree skill missing shell-safe recipe text %q", label, want)
			}
		}
		for _, forbidden := range []string{
			`git worktree add "$path" -b "$BRANCH_NAME"`,
			`eval "$cmd"`,
		} {
			if strings.Contains(body, forbidden) {
				t.Fatalf("%s worktree skill keeps unsafe or stale recipe %q", label, forbidden)
			}
		}
	}
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

func readRepoSkill(t *testing.T, parts ...string) string {
	t.Helper()
	_, currentFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve current test file")
	}
	repoRoot := filepath.Clean(filepath.Join(filepath.Dir(currentFile), "..", "..", ".."))
	data, err := os.ReadFile(filepath.Join(append([]string{repoRoot}, parts...)...))
	if err != nil {
		t.Fatalf("read repo skill %v: %v", parts, err)
	}
	return string(data)
}
