package skill

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func TestListSkillsScopesByRequestCWD(t *testing.T) {
	t.Parallel()

	svc, projectA, projectB := setupScopedSkillService(t)
	assertScopedSkillList(t, svc, projectA, "scoped", []string{"local-a", "shared"}, "local-b")
	assertScopedSkillList(t, svc, projectB, "project B", []string{"local-b"}, "local-a")
}

func TestSubdirWriteLocalProjectUsesGitRootCanonicalAndMirrors(t *testing.T) {
	setSkillTestUserHome(t)
	projectRoot, subdir := setupGitProjectSubdir(t)
	svc := &service{
		projectRoot:       projectRoot,
		projectSkillsRoot: defaultProjectSkillsRoot(projectRoot),
		superDolphinHome:  newTestSuperDolphinHome(t),
		http:              &http.Client{},
		mirrorLocks:       NewMirrorRootLockRegistry(),
	}

	out, err := svc.WriteLocal(skillTestContext(subdir), "build", "---\nname: build\n---\nbody", skillScopeProject)
	if err != nil {
		t.Fatalf("WriteLocal(subdir cwd): %v", err)
	}

	result := out.(map[string]any)
	if got, want := result["path"], filepath.Join(projectRoot, ".agents", "skills", "build", skillMainFile); got != want {
		t.Fatalf("WriteLocal path = %v, want %s", got, want)
	}
	assertFileContent(t, filepath.Join(projectRoot, ".agents", "skills", "build", skillMainFile), "---\nname: build\n---\nbody")
	assertMissing(t, filepath.Join(subdir, ".agents", "skills", "build", skillMainFile))

	report := mustMirrorPublishReport(t, result)
	assertPublishedReportItem(t, report.Published, "claude:project:"+RepoFingerprint(projectRoot), SkillProviderClaude, skillScopeProject, "build", "project/build")
	assertFileContent(t, filepath.Join(projectRoot, ".claude", "skills", "build", skillMainFile), "---\nname: build\n---\nbody")
	if _, err := readSkillMirrorManifest(filepath.Join(projectRoot, ".agents", "skills", skillMirrorManifestFile)); err != nil {
		t.Fatalf("read codex self manifest: %v", err)
	}
	assertMissing(t, filepath.Join(subdir, ".claude", "skills", "build", skillMainFile))
	assertMissing(t, filepath.Join(subdir, ".agents", "skills", "build", skillMainFile))
}

func TestSubdirListSkillsScansGitRootProjectSkills(t *testing.T) {
	t.Parallel()

	projectRoot, subdir := setupGitProjectSubdir(t)
	writeTestSkill(t, defaultProjectSkillsRoot(projectRoot), "root-only", "---\nname: root-only\n---\nroot")
	writeTestSkill(t, defaultProjectSkillsRoot(subdir), "subdir-leak", "---\nname: subdir-leak\n---\nsubdir")
	svc := &service{
		projectRoot:       projectRoot,
		projectSkillsRoot: defaultProjectSkillsRoot(projectRoot),
		superDolphinHome:  newTestSuperDolphinHome(t),
		http:              &http.Client{},
	}

	skills, err := svc.ListSkills(skillTestContext(subdir))
	if err != nil {
		t.Fatalf("ListSkills(subdir cwd): %v", err)
	}

	names, _ := skillNamesAndSummaries(skills)
	assertSkillNames(t, "subdir cwd", names, []string{"root-only"}, "subdir-leak")
}

func TestSubdirResolutionUsesGitRootMirrorTargets(t *testing.T) {
	setSkillTestUserHome(t)
	projectRoot, subdir := setupGitProjectSubdir(t)
	superHome := filepath.Join(t.TempDir(), ".super-dolphin")
	svc := &service{
		projectRoot:       projectRoot,
		projectSkillsRoot: defaultProjectSkillsRoot(projectRoot),
		superDolphinHome:  superHome,
		http:              &http.Client{},
	}
	writeSkillWithSupportFiles(t, filepath.Join(projectRoot, ".agents", "skills", "drift"), "drift")
	records, err := newCanonicalStore(superHome).scan(projectRoot)
	if err != nil {
		t.Fatalf("scan canonical records: %v", err)
	}
	target := SkillMirrorTarget{
		TargetID:        "codex:project:" + RepoFingerprint(projectRoot),
		Provider:        SkillProviderCodex,
		Scope:           skillScopeProject,
		Root:            providerProjectMirrorRoot(SkillProviderCodex, projectRoot),
		CanonicalRootID: RepoFingerprint(projectRoot),
	}
	if _, err := PublishSkillMirrors(NewMirrorRootLockRegistry(), context.Background(), records, []SkillMirrorTarget{target}); err != nil {
		t.Fatalf("PublishSkillMirrors: %v", err)
	}
	writeFileWithMode(t, filepath.Join(target.Root, "drift", "references", "guide.md"), "provider drift\n", 0o644)

	got, err := svc.listSkillResolutions(subdir)
	if err != nil {
		t.Fatalf("listSkillResolutions(subdir cwd): %v", err)
	}

	item := findResolutionItem(t, got.Items, "mirror_drift", "drift", skillScopeProject)
	if len(item.ProviderEntries) != 1 {
		t.Fatalf("provider entries = %+v, want one", item.ProviderEntries)
	}
	if !sameCleanPath(filepath.FromSlash(item.ProviderEntries[0].SourcePath), filepath.Join(projectRoot, ".agents", "skills", "drift")) {
		t.Fatalf("source_path = %q, want git-root codex mirror", item.ProviderEntries[0].SourcePath)
	}
	if sameCleanPath(filepath.FromSlash(item.ProviderEntries[0].SourcePath), filepath.Join(subdir, ".agents", "skills", "drift")) {
		t.Fatalf("source_path = %q, must not use subdir codex mirror", item.ProviderEntries[0].SourcePath)
	}
}

func TestSubdirProjectRootIsolationKeepsDifferentGitRootsSeparate(t *testing.T) {
	t.Parallel()

	projectA, _ := setupGitProjectSubdir(t)
	projectB, subdirB := setupGitProjectSubdir(t)
	writeTestSkill(t, defaultProjectSkillsRoot(projectA), "a-only", "---\nname: a-only\n---\na")
	writeTestSkill(t, defaultProjectSkillsRoot(projectB), "b-only", "---\nname: b-only\n---\nb")
	svc := &service{
		projectRoot:       projectA,
		projectSkillsRoot: defaultProjectSkillsRoot(projectA),
		superDolphinHome:  newTestSuperDolphinHome(t),
		http:              &http.Client{},
	}

	skills, err := svc.ListSkills(skillTestContext(subdirB))
	if err != nil {
		t.Fatalf("ListSkills(project B subdir): %v", err)
	}

	names, _ := skillNamesAndSummaries(skills)
	assertSkillNames(t, "project B subdir", names, []string{"b-only"}, "a-only")
}

func setupGitProjectSubdir(t *testing.T) (string, string) {
	t.Helper()
	projectRoot := t.TempDir()
	if err := os.MkdirAll(filepath.Join(projectRoot, ".git"), 0o755); err != nil {
		t.Fatalf("mkdir .git: %v", err)
	}
	subdir := filepath.Join(projectRoot, "pkg", "worker")
	if err := os.MkdirAll(subdir, 0o755); err != nil {
		t.Fatalf("mkdir subdir: %v", err)
	}
	canonicalProjectRoot, err := canonicalProjectPath(projectRoot)
	if err != nil {
		t.Fatalf("canonical project root: %v", err)
	}
	canonicalSubdir, err := canonicalProjectPath(subdir)
	if err != nil {
		t.Fatalf("canonical subdir: %v", err)
	}
	return canonicalProjectRoot, canonicalSubdir
}

func setupScopedSkillService(t *testing.T) (*service, string, string) {
	t.Helper()
	systemRoot := t.TempDir()
	projectA := filepath.Join(t.TempDir(), "wj", "langgraph")
	projectB := filepath.Join(t.TempDir(), "wj", "go-agent-v2")
	for _, root := range []string{projectA, projectB} {
		if err := os.MkdirAll(filepath.Join(root, ".agents", "skills"), 0o755); err != nil {
			t.Fatalf("mkdir project skills root: %v", err)
		}
	}
	writeTestSkill(t, filepath.Join(projectA, ".agents", "skills"), "local-a", "# local a")
	writeTestSkill(t, filepath.Join(projectB, ".agents", "skills"), "local-b", "# local b")
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
	if slices.Contains(wantNames, "shared") && summaries["shared"] != "global" {
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
		if !slices.Contains(names, want) {
			t.Fatalf("%s names = %v, missing %s", label, names, want)
		}
	}
	if slices.Contains(names, leakedName) {
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

	svc := &service{root: t.TempDir(), http: &http.Client{}}

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
