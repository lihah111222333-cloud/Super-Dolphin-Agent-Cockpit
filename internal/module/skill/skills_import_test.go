package skill

import (
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	uidto "github.com/anthropic-ai/super-agent-v3/internal/dto/ui"
	"github.com/kelindar/event"
)

func TestImportLocalDir_BatchContainer_ExpandsSubdirs(t *testing.T) {
	t.Parallel()

	svc, projectRoot := newImportDirTestService(t)
	source := filepath.Join(t.TempDir(), "skills")
	writeImportTestSkill(t, source, "alpha")
	writeImportTestSkill(t, source, "beta")

	out, err := svc.ImportLocalDir(skillTestContext(projectRoot), importSkillDirParams{Path: source})
	if err != nil {
		t.Fatalf("ImportLocalDir() error = %v", err)
	}
	result := mustImportDirResult(t, out)
	if failures := importDirFailures(result); len(failures) != 0 {
		t.Fatalf("failures = %#v, want none", failures)
	}
	if got := importDirNames(result); !reflect.DeepEqual(got, []string{"alpha", "beta"}) {
		t.Fatalf("imported names = %#v, want alpha/beta", got)
	}
	assertImportTargetExists(t, projectRoot, "alpha")
	assertImportTargetExists(t, projectRoot, "beta")
	assertImportTargetMissing(t, projectRoot, "skills")
}

func TestImportLocalDir_BatchSkipsSubdirsWithoutSkillFile(t *testing.T) {
	t.Parallel()

	svc, projectRoot := newImportDirTestService(t)
	source := filepath.Join(t.TempDir(), "skills")
	writeImportTestSkill(t, source, "alpha")
	notesDir := filepath.Join(source, "notes")
	mustMkdirAll(t, notesDir)
	mustWriteFile(t, filepath.Join(notesDir, "README.md"), "# notes")

	out, err := svc.ImportLocalDir(skillTestContext(projectRoot), importSkillDirParams{Path: source})
	if err != nil {
		t.Fatalf("ImportLocalDir() error = %v", err)
	}
	result := mustImportDirResult(t, out)
	if got := importDirNames(result); !reflect.DeepEqual(got, []string{"alpha"}) {
		t.Fatalf("imported names = %#v, want alpha", got)
	}
	failures := importDirFailures(result)
	if len(failures) != 1 || failures[0]["source"] != mustCanonicalTestPath(t, notesDir) {
		t.Fatalf("failures = %#v, want notes failure", failures)
	}
	if got, _ := failures[0]["error"].(string); got != "SKILL.md not found" {
		t.Fatalf("failure error = %q, want SKILL.md not found", got)
	}
}

func TestImportLocalDir_BatchPartialFailureCollects(t *testing.T) {
	t.Parallel()

	svc, projectRoot := newImportDirTestService(t)
	source := filepath.Join(t.TempDir(), "skills")
	writeImportTestSkill(t, source, "alpha")
	brokenDir := writeImportTestSkill(t, source, "broken")
	mustSymlink(t, "missing-target", filepath.Join(brokenDir, "bad-link"))
	writeImportTestSkill(t, source, "gamma")

	out, err := svc.ImportLocalDir(skillTestContext(projectRoot), importSkillDirParams{Path: source})
	if err != nil {
		t.Fatalf("ImportLocalDir() error = %v", err)
	}
	result := mustImportDirResult(t, out)
	if got := importDirNames(result); !reflect.DeepEqual(got, []string{"alpha", "gamma"}) {
		t.Fatalf("imported names = %#v, want alpha/gamma", got)
	}
	failures := importDirFailures(result)
	if len(failures) != 1 || failures[0]["source"] != mustCanonicalTestPath(t, brokenDir) {
		t.Fatalf("failures = %#v, want broken failure", failures)
	}
	if got, _ := failures[0]["error"].(string); !strings.Contains(got, "symlink is not allowed") {
		t.Fatalf("failure error = %q, want symlink rejection", got)
	}
	assertImportTargetMissing(t, projectRoot, "broken")
}

func TestImportLocalDir_SingleStillWorks_BackwardCompat(t *testing.T) {
	t.Parallel()

	svc, projectRoot := newImportDirTestService(t)
	source := filepath.Join(t.TempDir(), "demo-skill")
	mustMkdirAll(t, filepath.Join(source, "references"))
	mustWriteFile(t, filepath.Join(source, skillMainFile), "# demo")
	mustWriteFile(t, filepath.Join(source, "references", "guide.md"), "details")

	out, err := svc.ImportLocalDir(skillTestContext(projectRoot), importSkillDirParams{Path: source})
	if err != nil {
		t.Fatalf("ImportLocalDir() error = %v", err)
	}
	result := mustImportDirResult(t, out)
	if got := importDirNames(result); !reflect.DeepEqual(got, []string{"demo-skill"}) {
		t.Fatalf("imported names = %#v, want demo-skill", got)
	}
	assertImportTargetExists(t, projectRoot, "demo-skill")
	if _, err := os.Stat(filepath.Join(projectRoot, ".agent", "skills", "demo-skill", "references", "guide.md")); err != nil {
		t.Fatalf("resource stat error = %v", err)
	}
}

func TestImportLocalDir_EmptyDirError(t *testing.T) {
	t.Parallel()

	svc, projectRoot := newImportDirTestService(t)
	source := filepath.Join(t.TempDir(), "empty")
	mustMkdirAll(t, source)

	out, err := svc.ImportLocalDir(skillTestContext(projectRoot), importSkillDirParams{Path: source})
	if err != nil {
		t.Fatalf("ImportLocalDir() error = %v", err)
	}
	result := mustImportDirResult(t, out)
	if imported := importDirImported(result); len(imported) != 0 {
		t.Fatalf("imported = %#v, want none", imported)
	}
	failures := importDirFailures(result)
	if len(failures) != 1 || failures[0]["error"] != "no skill directories found" {
		t.Fatalf("failures = %#v, want no skill directories found", failures)
	}
}

func TestImportLocalDir_BatchPublishesSkillsChangedEvent(t *testing.T) {
	dispatcher := event.NewDispatcher()
	defer func() { _ = dispatcher.Close() }()

	got := make(chan uidto.SkillsChanged, 2)
	cancel := event.Subscribe(dispatcher, func(ev uidto.SkillsChanged) { got <- ev })
	defer cancel()

	svc, projectRoot := newImportDirTestService(t)
	svc.bindDispatcher(dispatcher)
	source := filepath.Join(t.TempDir(), "skills")
	writeImportTestSkill(t, source, "alpha")
	writeImportTestSkill(t, source, "beta")

	if _, err := svc.ImportLocalDir(skillTestContext(projectRoot), importSkillDirParams{Path: source}); err != nil {
		t.Fatalf("ImportLocalDir() error = %v", err)
	}
	ev := mustReceiveSkillsChanged(t, got)
	if ev.Action != "import" || ev.Count != 1 || ev.Scope != "project" || ev.Cwd != projectRoot {
		t.Fatalf("skills changed event = %#v", ev)
	}
	if !reflect.DeepEqual(ev.Actions, []string{"import"}) {
		t.Fatalf("skills changed actions = %#v", ev.Actions)
	}
	select {
	case extra := <-got:
		t.Fatalf("unexpected extra skills changed event = %#v", extra)
	case <-time.After(200 * time.Millisecond):
	}
}

func TestImportLocalDir_AutoMixedPaths_DetectsEachSource(t *testing.T) {
	t.Parallel()

	svc, projectRoot := newImportDirTestService(t)
	root := t.TempDir()
	single := filepath.Join(root, "solo")
	mustMkdirAll(t, single)
	mustWriteFile(t, filepath.Join(single, skillMainFile), "# solo")
	container := filepath.Join(root, "bundle")
	writeImportTestSkill(t, container, "alpha")
	writeImportTestSkill(t, container, "beta")

	out, err := svc.ImportLocalDir(skillTestContext(projectRoot), importSkillDirParams{Paths: []string{single, container}})
	if err != nil {
		t.Fatalf("ImportLocalDir() error = %v", err)
	}
	result := mustImportDirResult(t, out)
	if got := importDirNames(result); !reflect.DeepEqual(got, []string{"alpha", "beta", "solo"}) {
		t.Fatalf("imported names = %#v, want alpha/beta/solo", got)
	}
	assertImportTargetMissing(t, projectRoot, "bundle")
}

func TestImportLocalDir_AutoPrefersSingleWhenRootSkillExists(t *testing.T) {
	t.Parallel()

	svc, projectRoot := newImportDirTestService(t)
	source := filepath.Join(t.TempDir(), "mixed")
	mustMkdirAll(t, source)
	mustWriteFile(t, filepath.Join(source, skillMainFile), "# mixed")
	writeImportTestSkill(t, source, "alpha")

	autoOut, err := svc.ImportLocalDir(skillTestContext(projectRoot), importSkillDirParams{Path: source})
	if err != nil {
		t.Fatalf("auto ImportLocalDir() error = %v", err)
	}
	if got := importDirNames(mustImportDirResult(t, autoOut)); !reflect.DeepEqual(got, []string{"mixed"}) {
		t.Fatalf("auto imported names = %#v, want mixed", got)
	}
	assertImportTargetExists(t, projectRoot, "mixed")
	assertImportTargetMissing(t, projectRoot, "alpha")

	batchOut, err := svc.ImportLocalDir(skillTestContext(projectRoot), importSkillDirParams{Path: source, Mode: "batch"})
	if err != nil {
		t.Fatalf("batch ImportLocalDir() error = %v", err)
	}
	if got := importDirNames(mustImportDirResult(t, batchOut)); !reflect.DeepEqual(got, []string{"alpha"}) {
		t.Fatalf("batch imported names = %#v, want alpha", got)
	}
	assertImportTargetExists(t, projectRoot, "alpha")
}

func TestImportLocalDir_BatchRejectsNameOverride(t *testing.T) {
	t.Parallel()

	svc, projectRoot := newImportDirTestService(t)
	source := filepath.Join(t.TempDir(), "skills")
	writeImportTestSkill(t, source, "alpha")

	_, err := svc.ImportLocalDir(skillTestContext(projectRoot), importSkillDirParams{
		Path: source,
		Mode: "batch",
		Name: "override",
	})
	if err == nil || err.Error() != "name is not allowed in batch mode" {
		t.Fatalf("ImportLocalDir() error = %v, want name rejection", err)
	}
}

func newImportDirTestService(t *testing.T) (*service, string) {
	t.Helper()
	projectRoot := t.TempDir()
	svc := NewService(projectRoot).(*service)
	svc.root = t.TempDir()
	svc.projectSkillsRoot = defaultProjectSkillsRoot(projectRoot)
	return svc, projectRoot
}

func writeImportTestSkill(t *testing.T, container, name string) string {
	t.Helper()
	dir := filepath.Join(container, name)
	mustMkdirAll(t, dir)
	mustWriteFile(t, filepath.Join(dir, skillMainFile), "# "+name)
	return dir
}

func mustMkdirAll(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("MkdirAll(%q) error = %v", dir, err)
	}
}

func mustWriteFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile(%q) error = %v", path, err)
	}
}

func mustSymlink(t *testing.T, oldname, newname string) {
	t.Helper()
	if err := os.Symlink(oldname, newname); err != nil {
		t.Fatalf("Symlink(%q, %q) error = %v", oldname, newname, err)
	}
}

func mustCanonicalTestPath(t *testing.T, path string) string {
	t.Helper()
	resolved, err := canonicalProjectPath(path)
	if err != nil {
		t.Fatalf("canonicalProjectPath(%q) error = %v", path, err)
	}
	return resolved
}

func mustImportDirResult(t *testing.T, out any) map[string]any {
	t.Helper()
	result, ok := out.(map[string]any)
	if !ok {
		t.Fatalf("ImportLocalDir() result type = %T", out)
	}
	return result
}

func importDirImported(result map[string]any) []map[string]any {
	imported, _ := result["imported"].([]map[string]any)
	return imported
}

func importDirFailures(result map[string]any) []map[string]any {
	failures, _ := result["failures"].([]map[string]any)
	return failures
}

func importDirNames(result map[string]any) []string {
	imported := importDirImported(result)
	names := make([]string, 0, len(imported))
	for _, item := range imported {
		name, _ := item["name"].(string)
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func assertImportTargetExists(t *testing.T, projectRoot, name string) {
	t.Helper()
	if _, err := os.Stat(filepath.Join(projectRoot, ".agent", "skills", name, skillMainFile)); err != nil {
		t.Fatalf("target %q SKILL.md stat error = %v", name, err)
	}
}

func assertImportTargetMissing(t *testing.T, projectRoot, name string) {
	t.Helper()
	target := filepath.Join(projectRoot, ".agent", "skills", name)
	if _, err := os.Stat(target); !errorsIsNotExist(err) {
		t.Fatalf("target %q stat error = %v, want missing", name, err)
	}
}

func errorsIsNotExist(err error) bool {
	return err != nil && os.IsNotExist(err)
}
