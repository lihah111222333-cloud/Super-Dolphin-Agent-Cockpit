package skill

import (
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/kelindar/event"
	uidto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/ui"
)

func TestImportLocalDir_BatchContainer_ExpandsSubdirs(t *testing.T) {
	t.Parallel()

	svc, projectRoot := newImportDirTestService(t)
	source := filepath.Join(t.TempDir(), "skills")
	writeImportTestSkill(t, source, "alpha")
	writeImportTestSkill(t, source, "beta")

	out, err := svc.ImportLocalDir(skillTestContext(projectRoot), importSkillDirParams{Path: source, Scope: skillScopeProject})
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

	out, err := svc.ImportLocalDir(skillTestContext(projectRoot), importSkillDirParams{Path: source, Scope: skillScopeProject})
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

	out, err := svc.ImportLocalDir(skillTestContext(projectRoot), importSkillDirParams{Path: source, Scope: skillScopeProject})
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

	out, err := svc.ImportLocalDir(skillTestContext(projectRoot), importSkillDirParams{Path: source, Scope: skillScopeProject})
	if err != nil {
		t.Fatalf("ImportLocalDir() error = %v", err)
	}
	result := mustImportDirResult(t, out)
	if got := importDirNames(result); !reflect.DeepEqual(got, []string{"demo-skill"}) {
		t.Fatalf("imported names = %#v, want demo-skill", got)
	}
	assertImportTargetExists(t, projectRoot, "demo-skill")
	if _, err := os.Stat(filepath.Join(projectRoot, ".agents", "skills", "demo-skill", "references", "guide.md")); err != nil {
		t.Fatalf("resource stat error = %v", err)
	}
}

func TestImportLocalDirRejectsOversizedSupportFile(t *testing.T) {
	t.Parallel()

	svc, projectRoot := newImportDirTestService(t)
	source := filepath.Join(t.TempDir(), "demo-skill")
	mustMkdirAll(t, filepath.Join(source, "references"))
	mustWriteFile(t, filepath.Join(source, skillMainFile), "# demo")
	mustWriteBytes(t, filepath.Join(source, "references", "huge.bin"), make([]byte, maxSkillFileBytes+1))

	out, err := svc.ImportLocalDir(skillTestContext(projectRoot), importSkillDirParams{Path: source, Scope: skillScopeProject})
	if err != nil {
		t.Fatalf("ImportLocalDir() error = %v", err)
	}
	result := mustImportDirResult(t, out)
	if imported := importDirImported(result); len(imported) != 0 {
		t.Fatalf("imported = %#v, want none for oversized support file", imported)
	}
	failures := importDirFailures(result)
	if len(failures) != 1 {
		t.Fatalf("failures = %#v, want one oversized-file failure", failures)
	}
	if got, _ := failures[0]["error"].(string); !strings.Contains(got, "too large") {
		t.Fatalf("failure error = %q, want too large", got)
	}
	assertImportTargetMissing(t, projectRoot, "demo-skill")
}

func TestImportLocalDir_RewritesFrontmatterNameToImportedName(t *testing.T) {
	svc, projectRoot := newImportDirTestService(t)
	source := filepath.Join(t.TempDir(), "docs")
	mustMkdirAll(t, source)
	mustWriteFile(t, filepath.Join(source, skillMainFile), "---\nname: stale\nsummary: imported docs\n---\n# docs\n")

	out, err := svc.ImportLocalDir(skillTestContext(projectRoot), importSkillDirParams{Path: source, Scope: skillScopeProject})
	if err != nil {
		t.Fatalf("ImportLocalDir() error = %v", err)
	}
	if got := importDirNames(mustImportDirResult(t, out)); !reflect.DeepEqual(got, []string{"docs"}) {
		t.Fatalf("imported names = %#v, want docs", got)
	}
	assertFileContent(t, filepath.Join(projectRoot, ".agents", "skills", "docs", skillMainFile), "---\nname: docs\nsummary: imported docs\n---\n# docs\n")
}

func TestImportLocalDirPublishesProjectMirrors(t *testing.T) {
	svc, projectRoot := newImportDirTestService(t)
	source := filepath.Join(t.TempDir(), "demo-skill")
	mustMkdirAll(t, filepath.Join(source, "references"))
	mustWriteFile(t, filepath.Join(source, skillMainFile), "---\nname: demo-skill\n---\nbody")
	mustWriteFile(t, filepath.Join(source, "references", "guide.md"), "details")

	out, err := svc.ImportLocalDir(skillTestContext(projectRoot), importSkillDirParams{Path: source, Scope: skillScopeProject})
	if err != nil {
		t.Fatalf("ImportLocalDir() error = %v", err)
	}

	result := mustImportDirResult(t, out)
	report := mustMirrorPublishReport(t, result)
	assertPublishedReportItem(t, report.Published, "claude:project:"+RepoFingerprint(projectRoot), SkillProviderClaude, skillScopeProject, "demo-skill", "project/demo-skill")
	assertFileContent(t, filepath.Join(projectRoot, ".claude", "skills", "demo-skill", "references", "guide.md"), "details")
	assertFileContent(t, filepath.Join(providerProjectMirrorRoot(SkillProviderCodex, projectRoot), "demo-skill", "references", "guide.md"), "details")
	if _, err := readSkillMirrorManifest(filepath.Join(providerProjectMirrorRoot(SkillProviderCodex, projectRoot), skillMirrorManifestFile)); err != nil {
		t.Fatalf("read codex self manifest: %v", err)
	}
}

func TestImportLocalDirPublishErrorBlocksAndRollsBackCanonical(t *testing.T) {
	svc, projectRoot := newImportDirTestService(t)
	claudeHome := filepath.Join(projectRoot, ".claude")
	mustMkdirAll(t, claudeHome)
	if err := os.Chmod(claudeHome, 0o555); err != nil {
		t.Fatalf("Chmod readonly Claude home: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(claudeHome, 0o755) })
	source := filepath.Join(t.TempDir(), "demo-skill")
	mustMkdirAll(t, source)
	mustWriteFile(t, filepath.Join(source, skillMainFile), "---\nname: demo-skill\n---\nbody")

	_, err := svc.ImportLocalDir(skillTestContext(projectRoot), importSkillDirParams{Path: source, Scope: skillScopeProject})
	assertMirrorPublishBlockingError(t, err)
	assertImportTargetMissing(t, projectRoot, "demo-skill")
}

func TestImportLocalDirRejectsSymlinkSkillsRoot(t *testing.T) {
	svc, projectRoot := newImportDirTestService(t)
	outsideRoot := filepath.Join(t.TempDir(), "outside-skills")
	mustMkdirAll(t, outsideRoot)
	projectSkillsRoot := defaultProjectSkillsRoot(projectRoot)
	mustMkdirAll(t, filepath.Dir(projectSkillsRoot))
	mustSymlink(t, outsideRoot, projectSkillsRoot)
	source := filepath.Join(t.TempDir(), "demo-skill")
	mustMkdirAll(t, source)
	mustWriteFile(t, filepath.Join(source, skillMainFile), "---\nname: demo-skill\n---\nbody")

	out, err := svc.ImportLocalDir(skillTestContext(projectRoot), importSkillDirParams{Path: source, Scope: skillScopeProject})
	if err != nil {
		t.Fatalf("ImportLocalDir() error = %v", err)
	}
	failures := importDirFailures(mustImportDirResult(t, out))
	if len(failures) != 1 {
		t.Fatalf("failures = %#v, want symlink rejection", failures)
	}
	got, _ := failures[0]["error"].(string)
	if !strings.Contains(got, "symlink") {
		t.Fatalf("failure error = %q, want symlink rejection", got)
	}
	assertMissing(t, filepath.Join(outsideRoot, "demo-skill", skillMainFile))
}

func TestImportLocalDir_EmptyDirError(t *testing.T) {
	t.Parallel()

	svc, projectRoot := newImportDirTestService(t)
	source := filepath.Join(t.TempDir(), "empty")
	mustMkdirAll(t, source)

	out, err := svc.ImportLocalDir(skillTestContext(projectRoot), importSkillDirParams{Path: source, Scope: skillScopeProject})
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
	startSkillsChangedRunnerCleanup(t, svc)
	source := filepath.Join(t.TempDir(), "skills")
	writeImportTestSkill(t, source, "alpha")
	writeImportTestSkill(t, source, "beta")

	if _, err := svc.ImportLocalDir(skillTestContext(projectRoot), importSkillDirParams{Path: source, Scope: skillScopeProject}); err != nil {
		t.Fatalf("ImportLocalDir() error = %v", err)
	}
	ev := mustReceiveSkillsChanged(t, got)
	assertImportSkillsChangedEvent(t, ev, projectRoot)
	select {
	case extra := <-got:
		t.Fatalf("unexpected extra skills changed event = %#v", extra)
	case <-time.After(200 * time.Millisecond):
	}
}

func assertImportSkillsChangedEvent(t *testing.T, ev uidto.SkillsChanged, projectRoot string) {
	t.Helper()
	if ev.Action != "import" || ev.Count != 1 || ev.Scope != "project" || ev.Cwd != "" || ev.RepoFingerprint != RepoFingerprint(projectRoot) || ev.RelativePath != "." {
		t.Fatalf("skills changed event = %#v", ev)
	}
	if !reflect.DeepEqual(ev.Actions, []string{"import"}) {
		t.Fatalf("skills changed actions = %#v", ev.Actions)
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

	out, err := svc.ImportLocalDir(skillTestContext(projectRoot), importSkillDirParams{Paths: []string{single, container}, Scope: skillScopeProject})
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

	autoOut, err := svc.ImportLocalDir(skillTestContext(projectRoot), importSkillDirParams{Path: source, Scope: skillScopeProject})
	if err != nil {
		t.Fatalf("auto ImportLocalDir() error = %v", err)
	}
	if got := importDirNames(mustImportDirResult(t, autoOut)); !reflect.DeepEqual(got, []string{"mixed"}) {
		t.Fatalf("auto imported names = %#v, want mixed", got)
	}
	assertImportTargetExists(t, projectRoot, "mixed")
	assertImportTargetMissing(t, projectRoot, "alpha")

	batchOut, err := svc.ImportLocalDir(skillTestContext(projectRoot), importSkillDirParams{Path: source, Scope: skillScopeProject, Mode: "batch"})
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
		Scope: skillScopeProject,
		Path:  source,
		Mode:  "batch",
		Name:  "override",
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
	svc.superDolphinHome = newTestSuperDolphinHome(t)
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

func mustWriteBytes(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll(%q) error = %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("WriteFile(%q) error = %v", path, err)
	}
}

func mustSymlink(t *testing.T, oldname, newname string) {
	t.Helper()
	if err := os.Symlink(oldname, newname); err != nil {
		skipIfSymlinkPrivilegeNotHeld(t, err)
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
	if _, err := os.Stat(filepath.Join(projectRoot, ".agents", "skills", name, skillMainFile)); err != nil {
		t.Fatalf("target %q SKILL.md stat error = %v", name, err)
	}
}

func assertImportTargetMissing(t *testing.T, projectRoot, name string) {
	t.Helper()
	target := filepath.Join(projectRoot, ".agents", "skills", name)
	if _, err := os.Stat(target); !errorsIsNotExist(err) {
		t.Fatalf("target %q stat error = %v, want missing", name, err)
	}
}

func errorsIsNotExist(err error) bool {
	return err != nil && os.IsNotExist(err)
}

func TestImportLocalDirRejectsSourceInsideProjectSkillsRoot(t *testing.T) {
	t.Parallel()

	systemRoot := t.TempDir()
	projectRoot := t.TempDir()
	projectSkillsRoot := defaultProjectSkillsRoot(projectRoot)
	sourceDir := filepath.Join(projectSkillsRoot, "demo-skill")
	if err := os.MkdirAll(sourceDir, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(sourceDir, skillMainFile), []byte("# demo"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	svc := &service{root: systemRoot, projectRoot: projectRoot, projectSkillsRoot: projectSkillsRoot}

	out, err := svc.ImportLocalDir(skillTestContext(projectRoot), importSkillDirParams{Path: sourceDir, Scope: skillScopeProject})
	if err != nil {
		t.Fatalf("ImportLocalDir() error = %v", err)
	}
	result, ok := out.(map[string]any)
	if !ok {
		t.Fatalf("ImportLocalDir() result type = %T", out)
	}
	failures, ok := result["failures"].([]map[string]any)
	if !ok || len(failures) != 1 {
		t.Fatalf("ImportLocalDir() failures = %#v, want single failure", result["failures"])
	}
	if got := failures[0]["error"]; got != "source is inside skills root: "+sourceDir {
		t.Fatalf("ImportLocalDir() failure error = %#v", got)
	}
}

func TestEnsureSourceOutsideRootsReturnsRootResolutionError(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	root := filepath.Join(dir, "loop")
	mustSymlink(t, root, root)
	source := filepath.Join(t.TempDir(), "source")
	mustMkdirAll(t, source)

	err := ensureSourceOutsideRoots([]string{root}, source, source)
	if err == nil || !strings.Contains(err.Error(), "resolve skills root") {
		t.Fatalf("ensureSourceOutsideRoots() error = %v, want root resolution error", err)
	}
}

func TestImportLocalDirAcceptsSourceOutsideProjectRoot(t *testing.T) {
	t.Parallel()

	projectRoot := t.TempDir()
	skillsRoot := t.TempDir()
	outsideRoot := t.TempDir()
	sourceDir := filepath.Join(outsideRoot, "demo-skill")
	if err := os.MkdirAll(sourceDir, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(sourceDir, skillMainFile), []byte("# demo"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	svc := &service{projectRoot: projectRoot, root: skillsRoot, projectSkillsRoot: defaultProjectSkillsRoot(projectRoot)}

	out, err := svc.ImportLocalDir(skillTestContext(projectRoot), importSkillDirParams{Path: sourceDir, Scope: "project"})
	if err != nil {
		t.Fatalf("ImportLocalDir() error = %v", err)
	}
	result, ok := out.(map[string]any)
	if !ok {
		t.Fatalf("ImportLocalDir() result type = %T", out)
	}
	if got, present := result["failures"]; present {
		t.Fatalf("ImportLocalDir() failures = %#v, want no failures", got)
	}
	imported, ok := result["imported"].([]map[string]any)
	if !ok || len(imported) != 1 {
		t.Fatalf("ImportLocalDir() imported = %#v, want single import", result["imported"])
	}
	if gotName, _ := imported[0]["name"].(string); gotName != "demo-skill" {
		t.Fatalf("ImportLocalDir() imported name = %q, want demo-skill", gotName)
	}
	if _, err := os.Stat(filepath.Join(projectRoot, ".agents", "skills", "demo-skill", skillMainFile)); err != nil {
		t.Fatalf("ImportLocalDir() target SKILL.md stat err = %v", err)
	}
}

func TestImportLocalDirAcceptsLegacyRootAsExplicitSource(t *testing.T) {
	t.Parallel()

	skillsRoot := t.TempDir()
	projectRoot := t.TempDir()
	sourceDir := filepath.Join(skillsRoot, "demo-skill")
	if err := os.MkdirAll(sourceDir, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(sourceDir, skillMainFile), []byte("# demo"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	svc := &service{projectRoot: projectRoot, root: skillsRoot, projectSkillsRoot: defaultProjectSkillsRoot(projectRoot)}

	out, err := svc.ImportLocalDir(skillTestContext(projectRoot), importSkillDirParams{Path: sourceDir, Scope: skillScopeProject})
	if err != nil {
		t.Fatalf("ImportLocalDir() error = %v", err)
	}
	result, ok := out.(map[string]any)
	if !ok {
		t.Fatalf("ImportLocalDir() result type = %T", out)
	}
	if got, present := result["failures"]; present {
		t.Fatalf("ImportLocalDir() failures = %#v, want no failures", got)
	}
	if _, err := os.Stat(filepath.Join(projectRoot, ".agents", "skills", "demo-skill", skillMainFile)); err != nil {
		t.Fatalf("imported project SKILL.md stat err = %v", err)
	}
}

func TestImportLocalDirConvertsSafeLegacyDisplayName(t *testing.T) {
	t.Parallel()

	projectRoot := t.TempDir()
	sourceRoot := t.TempDir()
	sourceDir := filepath.Join(sourceRoot, "Docker 容器化部署")
	writeSkillContent(t, sourceDir, "Docker 容器化部署", "# docker\n")
	svc := &service{projectRoot: projectRoot, root: t.TempDir(), projectSkillsRoot: defaultProjectSkillsRoot(projectRoot)}

	out, err := svc.ImportLocalDir(skillTestContext(projectRoot), importSkillDirParams{Path: sourceDir, Scope: "project"})
	if err != nil {
		t.Fatalf("ImportLocalDir() error = %v", err)
	}
	result, ok := out.(map[string]any)
	if !ok {
		t.Fatalf("ImportLocalDir() result type = %T", out)
	}
	if got, present := result["failures"]; present {
		t.Fatalf("ImportLocalDir() failures = %#v, want no failures", got)
	}
	imported, ok := result["imported"].([]map[string]any)
	if !ok || len(imported) != 1 {
		t.Fatalf("ImportLocalDir() imported = %#v, want single import", result["imported"])
	}
	if gotName, _ := imported[0]["name"].(string); gotName != "docker-容器化部署" {
		t.Fatalf("ImportLocalDir() imported name = %q, want canonical name", gotName)
	}
	assertFileContent(t, filepath.Join(projectRoot, ".agents", "skills", "docker-容器化部署", skillMainFile), "---\nname: docker-容器化部署\ndisplay_name: \"Docker 容器化部署\"\n---\n# docker\n")
}

func TestImportLocalDirRejectsExistingTarget(t *testing.T) {
	t.Parallel()

	projectRoot := t.TempDir()
	sourceDir := filepath.Join(projectRoot, "demo-skill")
	if err := os.MkdirAll(sourceDir, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(sourceDir, skillMainFile), []byte("# demo"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	skillsRoot := t.TempDir()
	existingDir := filepath.Join(projectRoot, ".agents", "skills", "demo-skill")
	if err := os.MkdirAll(existingDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(existing) error = %v", err)
	}
	svc := &service{projectRoot: projectRoot, root: skillsRoot, projectSkillsRoot: defaultProjectSkillsRoot(projectRoot)}

	out, err := svc.ImportLocalDir(skillTestContext(projectRoot), importSkillDirParams{Path: sourceDir, Scope: "project"})
	if err != nil {
		t.Fatalf("ImportLocalDir() error = %v", err)
	}
	result, ok := out.(map[string]any)
	if !ok {
		t.Fatalf("ImportLocalDir() result type = %T", out)
	}
	failures, ok := result["failures"].([]map[string]any)
	if !ok || len(failures) != 1 {
		t.Fatalf("ImportLocalDir() failures = %#v, want single failure", result["failures"])
	}
	if got := failures[0]["error"]; got != "skill already exists: demo-skill" {
		t.Fatalf("ImportLocalDir() failure error = %#v", got)
	}
}
