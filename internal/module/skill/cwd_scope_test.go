package skill

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	platformshared "github.com/anthropic-ai/super-agent-v3/internal/platform/shared"
)

func TestListSkillsScopesByRequestCWD(t *testing.T) {
	t.Parallel()

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
	writeScopedSystemSkill(t, systemRoot, projectA, "shared", "---\nname: shared\nsummary: from-a\n---\nA")
	writeScopedSystemSkill(t, systemRoot, projectB, "shared", "---\nname: shared\nsummary: from-b\n---\nB")

	svc := &service{
		root:              systemRoot,
		projectRoot:       projectB,
		projectSkillsRoot: defaultProjectSkillsRoot(projectB),
		http:              &http.Client{},
	}

	skills, err := svc.ListSkills(WithCWD(context.Background(), projectA))
	if err != nil {
		t.Fatalf("ListSkills scoped: %v", err)
	}
	names := make([]string, 0, len(skills))
	summaries := map[string]string{}
	for _, item := range skills {
		names = append(names, item.Name)
		summaries[item.Name] = item.Summary
	}
	if len(skills) != 2 {
		t.Fatalf("len(scoped skills) = %d, want 2 (%v)", len(skills), names)
	}
	if !containsString(names, "local-a") || !containsString(names, "shared") {
		t.Fatalf("scoped names = %v, want local-a + shared", names)
	}
	if containsString(names, "local-b") {
		t.Fatalf("scoped names leaked project B local skill: %v", names)
	}
	if got := summaries["shared"]; got != "from-a" {
		t.Fatalf("shared summary = %q, want from-a", got)
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
		{
			name: "ExpandBody",
			call: func() error {
				_, err := svc.ExpandBody(context.Background(), ExpandBodyParams{Name: "demo"})
				return err
			},
		},
		{
			name: "ReadResource",
			call: func() error {
				_, err := svc.ReadResource(context.Background(), ReadResourceParams{Name: "demo", Path: "ref.md"})
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

func TestExpandBodyScopesDuplicateSkillNameByCWD(t *testing.T) {
	t.Parallel()

	systemRoot := t.TempDir()
	projectA := filepath.Join(t.TempDir(), "wj", "langgraph")
	projectB := filepath.Join(t.TempDir(), "wj", "go-agent-v2")
	writeScopedSystemSkill(t, systemRoot, projectA, "shared", "---\nname: shared\nsummary: from-a\n---\n## Body\nproject-a")
	writeScopedSystemSkill(t, systemRoot, projectB, "shared", "---\nname: shared\nsummary: from-b\n---\n## Body\nproject-b")
	svc := &service{root: systemRoot, http: &http.Client{}}

	got, err := svc.ExpandBody(WithCWD(context.Background(), projectB), ExpandBodyParams{Name: "shared"})
	if err != nil {
		t.Fatalf("ExpandBody scoped: %v", err)
	}
	if !strings.Contains(got.Content, "project-b") || strings.Contains(got.Content, "project-a") {
		t.Fatalf("ExpandBody content = %q, want only project-b", got.Content)
	}
}

func writeScopedSystemSkill(t *testing.T, systemRoot, cwd, name, content string) string {
	t.Helper()
	projectKey := platformshared.ProjectKeyFromCwd(cwd)
	dir := filepath.Join(systemRoot, projectKey, "by-id", name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir scoped skill: %v", err)
	}
	path := filepath.Join(dir, skillMainFile)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write scoped skill: %v", err)
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
