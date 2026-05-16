package skill

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"testing"
)

func TestListSkillsScopesByRequestCWD(t *testing.T) {
	t.Parallel()

	svc, projectA, projectB := setupScopedSkillService(t)
	assertScopedSkillList(t, svc, projectA, "scoped", []string{"local-a", "shared"}, "local-b")
	assertScopedSkillList(t, svc, projectB, "project B", []string{"local-b"}, "local-a")
}

func setupScopedSkillService(t *testing.T) (*service, string, string) {
	t.Helper()
	systemRoot := t.TempDir()
	projectA := filepath.Join(t.TempDir(), "wj", "langgraph")
	projectB := filepath.Join(t.TempDir(), "wj", "go-agent-v2")
	for _, root := range []string{projectA, projectB} {
		if err := os.MkdirAll(filepath.Join(root, ".agent", "skills"), 0o755); err != nil {
			t.Fatalf("mkdir project skills root: %v", err)
		}
	}
	writeTestSkill(t, filepath.Join(projectA, ".agent", "skills"), "local-a", "# local a")
	writeTestSkill(t, filepath.Join(projectB, ".agent", "skills"), "local-b", "# local b")
	writeScopedSystemSkill(t, systemRoot, projectA, "shared", "---\nname: shared\nsummary: global\n---\nA")

	svc := &service{
		root:              systemRoot,
		projectRoot:       projectB,
		projectSkillsRoot: defaultProjectSkillsRoot(projectB),
		http:              &http.Client{},
	}

	return svc, projectA, projectB
}

func assertScopedSkillList(t *testing.T, svc *service, cwd, label string, wantNames []string, leakedName string) {
	t.Helper()
	skills, err := svc.ListSkills(WithCWD(context.Background(), cwd))
	if err != nil {
		t.Fatalf("ListSkills %s: %v", label, err)
	}
	names, summaries := skillNamesAndSummaries(skills)
	if len(skills) != len(wantNames) {
		t.Fatalf("len(%s skills) = %d, want %d (%v)", label, len(skills), len(wantNames), names)
	}
	assertSkillNames(t, label, names, wantNames, leakedName)
	if containsString(wantNames, "shared") && summaries["shared"] != "global" {
		got := summaries["shared"]
		t.Fatalf("%s shared summary = %q, want global", label, got)
	}
}

func skillNamesAndSummaries(skills []SkillInfo) ([]string, map[string]string) {
	names := make([]string, 0, len(skills))
	summaries := map[string]string{}
	for _, item := range skills {
		names = append(names, item.Name)
		summaries[item.Name] = item.Summary
	}
	return names, summaries
}

func assertSkillNames(t *testing.T, label string, names, wantNames []string, leakedName string) {
	t.Helper()
	for _, want := range wantNames {
		if !containsString(names, want) {
			t.Fatalf("%s names = %v, missing %s", label, names, want)
		}
	}
	if containsString(names, leakedName) {
		t.Fatalf("%s names leaked %s: %v", label, leakedName, names)
	}
}

func TestListSkillsEmptyCWDReturnsErrMissingCWD(t *testing.T) {
	t.Parallel()

	svc := &service{root: t.TempDir(), http: &http.Client{}}

	for _, ctx := range []context.Context{
		context.Background(),
		WithCWD(context.Background(), ""),
	} {
		_, err := svc.ListSkills(ctx)
		if !errors.Is(err, ErrMissingCWD) {
			t.Fatalf("ListSkills() error = %v, want ErrMissingCWD", err)
		}
	}
}

func TestAllSkillServiceMethodsRequireCWD(t *testing.T) {
	t.Parallel()

	svc, root := newExpandTestService(t)
	writeExpandTestSkill(t, root, "demo", "---\nname: demo\n---\nbody")

	cases := []struct {
		name string
		call func() error
	}{
		{
			name: "ListSkills",
			call: func() error {
				_, err := svc.ListSkills(context.Background())
				return err
			},
		},
		{
			name: "Expand",
			call: func() error {
				_, err := svc.Expand(context.Background(), skillExpandParams{Name: "demo"})
				return err
			},
		},
	}

	for _, tc := range cases {
		err := tc.call()
		if !errors.Is(err, ErrMissingCWD) {
			t.Fatalf("%s error = %v, want ErrMissingCWD", tc.name, err)
		}
	}
}

func writeScopedSystemSkill(t *testing.T, systemRoot, cwd, name, content string) string {
	t.Helper()
	_ = systemRoot
	dir := filepath.Join(defaultProjectSkillsRoot(cwd), name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir system skill: %v", err)
	}
	path := filepath.Join(dir, skillMainFile)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write system skill: %v", err)
	}
	return path
}

func containsString(items []string, needle string) bool {
	for _, item := range items {
		if item == needle {
			return true
		}
	}
	return false
}
