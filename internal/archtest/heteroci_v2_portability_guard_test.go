package archtest_test

import (
	"bytes"
	"context"
	"fmt"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"golang.org/x/mod/modfile"
)

const (
	heterociV2FixtureModule  = "example.com/wjboot-v2-portability"
	heterociV3ModulePath     = "github.com/lihah111222333-cloud/super-dolphin-agent"
	heterociV2ModuleMarker   = "wjboot-v2"
	heterociV2CommandTimeout = 90 * time.Second
	heterociV2GitTimeout     = 5 * time.Second
)

func TestHeterociV2PortabilityFixture(t *testing.T) {
	root := heterociV2RepoRoot(t)
	sourcePackage := heterociV2SourcePackage(t, root)
	fixtureRoot, fixturePackage := newHeterociV2Fixture(t)

	productionFiles, productionCount, samePackageTestCount := copyHeterociV2GoSources(t, sourcePackage, fixturePackage)
	assertHeterociV2SourceCounts(t, productionCount, samePackageTestCount)
	goExecutable := heterociV2Executable(t, "go")
	bashExecutable := heterociV2Executable(t, "bash")
	checkHeterociV2ProductionImports(t, productionFiles, heterociV2FixtureModule, goExecutable)
	copyHeterociV2Assets(t, root, fixtureRoot)
	writeHeterociV2FixtureSmokeTest(t, fixturePackage)

	commandEnv := heterociV2CommandEnvironment(t, fixtureRoot, goExecutable, bashExecutable)
	compiledBinary := filepath.Join(fixtureRoot, "heteroci-v2.test")
	runHeterociV2Command(t, fixtureRoot, commandEnv, goExecutable,
		"test", "-mod=mod", "-c", "-o", compiledBinary, "./internal/devtools/heteroci")
	assertHeterociV2CompiledBinary(t, compiledBinary)
	runHeterociV2Command(t, fixtureRoot, commandEnv, goExecutable,
		"test", "-mod=mod", "-count=1", "-v", "./internal/devtools/heteroci")

	deployTest := filepath.Join(fixtureRoot, "scripts", "tests", "test_heteroci_deploy_scripts.sh")
	runHeterociV2Command(t, fixtureRoot, commandEnv, bashExecutable, deployTest)
}

type heterociV2NegativeSourceCase struct {
	name  string
	want  string
	setup func(*testing.T, string)
}

func TestHeterociV2SourceClosureRejectsUnsupportedEntries(t *testing.T) {
	if sourceRoot := os.Getenv("HETEROCI_V2_NEGATIVE_SOURCE"); sourceRoot != "" {
		copyHeterociV2GoSources(t, sourceRoot, os.Getenv("HETEROCI_V2_NEGATIVE_DESTINATION"))
		return
	}
	for _, test := range heterociV2NegativeSourceCases() {
		t.Run(test.name, func(t *testing.T) {
			runHeterociV2NegativeSourceCase(t, test)
		})
	}
}

func heterociV2NegativeSourceCases() []heterociV2NegativeSourceCase {
	return []heterociV2NegativeSourceCase{
		{
			name: "symlink-file",
			want: "contains symlink",
			setup: func(t *testing.T, root string) {
				writeHeterociV2NegativeGoFile(t, filepath.Join(root, "target.go"), "package heteroci\n")
				if err := os.Symlink("target.go", filepath.Join(root, "linked.go")); err != nil {
					t.Skipf("symlink is unavailable: %v", err)
				}
			},
		},
		{
			name: "symlink-directory",
			want: "contains symlink",
			setup: func(t *testing.T, root string) {
				target := t.TempDir()
				if err := os.Symlink(target, filepath.Join(root, "linked")); err != nil {
					t.Skipf("symlink is unavailable: %v", err)
				}
			},
		},
		{
			name: "non-go-asset",
			want: "non-Go asset",
			setup: func(t *testing.T, root string) {
				writeHeterociV2NegativeGoFile(t, filepath.Join(root, "valid.go"), "package heteroci\n")
				writeHeterociV2NegativeGoFile(t, filepath.Join(root, "asset.txt"), "not Go\n")
			},
		},
		{
			name: "nested-directory",
			want: "nested directory/package",
			setup: func(t *testing.T, root string) {
				if err := os.Mkdir(filepath.Join(root, "nested"), 0o755); err != nil {
					t.Fatalf("create nested source directory: %v", err)
				}
				writeHeterociV2NegativeGoFile(t, filepath.Join(root, "nested", "nested.go"), "package heteroci\n")
			},
		},
		{
			name: "go-embed",
			want: "uses go:embed",
			setup: func(t *testing.T, root string) {
				writeHeterociV2NegativeGoFile(t, filepath.Join(root, "embed.go"), "package heteroci\n\n//go:embed z-asset.txt\nvar embedded string\n")
				writeHeterociV2NegativeGoFile(t, filepath.Join(root, "z-asset.txt"), "embedded\n")
			},
		},
	}
}

func runHeterociV2NegativeSourceCase(t *testing.T, test heterociV2NegativeSourceCase) {
	t.Helper()
	sourceRoot := t.TempDir()
	destinationRoot := filepath.Join(t.TempDir(), "destination")
	test.setup(t, sourceRoot)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, os.Args[0], "-test.run", "^TestHeterociV2SourceClosureRejectsUnsupportedEntries$")
	command.Env = append(os.Environ(),
		"HETEROCI_V2_NEGATIVE_SOURCE="+sourceRoot,
		"HETEROCI_V2_NEGATIVE_DESTINATION="+destinationRoot,
	)
	command.WaitDelay = 2 * time.Second
	output, err := command.CombinedOutput()
	if ctx.Err() != nil {
		t.Fatalf("negative source-closure child timed out: %v", ctx.Err())
	}
	if err == nil {
		t.Fatalf("negative source-closure child unexpectedly passed; output=%s", output)
	}
	outputText := string(output)
	if !strings.Contains(outputText, test.want) || !strings.Contains(outputText, sourceRoot) {
		t.Fatalf("negative source-closure child error=%v output=%s; want %q and path %q", err, output, test.want, sourceRoot)
	}
}

func writeHeterociV2NegativeGoFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write negative source-closure fixture %q: %v", path, err)
	}
}

func heterociV2SourcePackage(t *testing.T, root string) string {
	t.Helper()
	sourcePackage := heterociV2CanonicalPath(
		t,
		"source package",
		filepath.Join(root, "internal", "devtools", "heteroci"),
	)
	if !heterociV2PathWithin(root, sourcePackage) {
		t.Fatalf("portability fixture source escapes canonical repository root: root=%q source=%q", root, sourcePackage)
	}
	if strings.Contains(strings.ToLower(filepath.ToSlash(sourcePackage)), heterociV2ModuleMarker) {
		t.Fatalf("portability fixture source must stay in this repository, got %q", sourcePackage)
	}
	info, err := os.Stat(sourcePackage)
	if err != nil {
		t.Fatalf("stat canonical portability fixture source %q: %v", sourcePackage, err)
	}
	if !info.IsDir() {
		t.Fatalf("canonical portability fixture source is not a directory: %q", sourcePackage)
	}
	return sourcePackage
}

func newHeterociV2Fixture(t *testing.T) (string, string) {
	t.Helper()
	fixtureRoot := t.TempDir()
	fixturePackage := filepath.Join(fixtureRoot, "internal", "devtools", "heteroci")
	if err := os.MkdirAll(fixturePackage, 0o755); err != nil {
		t.Fatalf("create fixture package directory: %v", err)
	}
	if err := os.WriteFile(filepath.Join(fixtureRoot, "go.mod"), []byte("module "+heterociV2FixtureModule+"\n\ngo 1.26.5\n"), 0o644); err != nil {
		t.Fatalf("write isolated V2-like go.mod: %v", err)
	}
	return fixtureRoot, fixturePackage
}

func assertHeterociV2SourceCounts(t *testing.T, productionCount, samePackageTestCount int) {
	t.Helper()
	if productionCount == 0 || samePackageTestCount == 0 {
		t.Fatalf("fixture copied production=%d same-package-tests=%d; expected both", productionCount, samePackageTestCount)
	}
}

func copyHeterociV2Assets(t *testing.T, root, fixtureRoot string) {
	t.Helper()
	deploySource := filepath.Join(root, "scripts", "deploy")
	deployDestination := filepath.Join(fixtureRoot, "scripts", "deploy")
	if copied := copyHeterociV2Tree(t, deploySource, deployDestination); copied == 0 {
		t.Fatal("fixture copied no deployment files")
	}
	for _, name := range []string{"Dockerfile.ci-runner-Darwin-compat", "Dockerfile.ci-runner-ubuntu"} {
		copyHeterociV2File(t, filepath.Join(root, name), filepath.Join(fixtureRoot, name))
	}
	copyHeterociV2File(
		t,
		filepath.Join(root, "scripts", "tests", "test_heteroci_deploy_scripts.sh"),
		filepath.Join(fixtureRoot, "scripts", "tests", "test_heteroci_deploy_scripts.sh"),
	)
}

func assertHeterociV2CompiledBinary(t *testing.T, compiledBinary string) {
	t.Helper()
	compiledInfo, err := os.Stat(compiledBinary)
	if err != nil {
		t.Fatalf("stat isolated fixture test binary: %v", err)
	}
	if !compiledInfo.Mode().IsRegular() || compiledInfo.Size() == 0 {
		t.Fatalf("isolated fixture test binary is not a non-empty regular file: mode=%s size=%d", compiledInfo.Mode(), compiledInfo.Size())
	}
}

func heterociV2RepoRoot(t *testing.T) string {
	t.Helper()
	_, sourceFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed while locating repository root")
	}
	sourceFile = heterociV2CanonicalPath(t, "runtime.Caller source", sourceFile)
	root := heterociV2CanonicalPath(t, "repository root", filepath.Join(filepath.Dir(sourceFile), "..", ".."))
	if !heterociV2PathWithin(root, sourceFile) {
		t.Fatalf("runtime.Caller source escaped canonical repository root: root=%q source=%q", root, sourceFile)
	}
	goModPath := heterociV2CanonicalPath(t, "repository go.mod", filepath.Join(root, "go.mod"))
	if goModPath != filepath.Join(root, "go.mod") {
		t.Fatalf("repository go.mod is not owned by canonical root: root=%q go.mod=%q", root, goModPath)
	}
	info, err := os.Stat(goModPath)
	if err != nil {
		t.Fatalf("repository go.mod not found from %q: %v", root, err)
	}
	if !info.Mode().IsRegular() {
		t.Fatalf("repository go.mod is not regular: %q", goModPath)
	}
	heterociV2ValidateGitRepository(t, root)
	heterociV2ValidateGoModuleIdentity(t, goModPath)
	return root
}

func heterociV2CanonicalPath(t *testing.T, kind, value string) string {
	t.Helper()
	if !filepath.IsAbs(value) || filepath.Clean(value) != value {
		t.Fatalf("%s path is not a clean absolute path before symlink resolution: %q", kind, value)
	}
	canonical, err := filepath.EvalSymlinks(value)
	if err != nil {
		t.Fatalf("resolve canonical %s path %q: %v", kind, value, err)
	}
	if !filepath.IsAbs(canonical) || filepath.Clean(canonical) != canonical {
		t.Fatalf("canonical %s path is not a clean absolute path: %q", kind, canonical)
	}
	return canonical
}

func heterociV2PathWithin(base, candidate string) bool {
	relative, err := filepath.Rel(base, candidate)
	if err != nil || filepath.IsAbs(relative) {
		return false
	}
	return relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}

func heterociV2ValidateGitRepository(t *testing.T, root string) {
	t.Helper()
	gitExecutable := heterociV2Executable(t, "git")
	topLevelRaw := heterociV2RunGit(t, root, gitExecutable, "rev-parse", "--show-toplevel")
	topLevel := heterociV2ResolveGitPath(t, root, "git top-level", topLevelRaw)
	if topLevel != root {
		t.Fatalf("canonical git top-level does not match canonical source root: git=%q root=%q", topLevel, root)
	}
	commonDir := heterociV2ResolveGitPath(
		t,
		root,
		"git-common-dir",
		heterociV2RunGit(t, root, gitExecutable, "rev-parse", "--git-common-dir"),
	)
	gitDir := heterociV2ResolveGitPath(
		t,
		root,
		"git-dir",
		heterociV2RunGit(t, root, gitExecutable, "rev-parse", "--git-dir"),
	)
	commonInfo, err := os.Stat(commonDir)
	if err != nil || !commonInfo.IsDir() {
		t.Fatalf("git-common-dir is not a canonical directory: %q: %v", commonDir, err)
	}
	if !heterociV2PathWithin(commonDir, gitDir) {
		t.Fatalf("git-common-dir does not own this repository git-dir: common=%q git=%q", commonDir, gitDir)
	}
	objects := heterociV2ResolveGitPath(
		t,
		root,
		"git object directory",
		heterociV2RunGit(t, root, gitExecutable, "rev-parse", "--git-path", "objects"),
	)
	if !heterociV2PathWithin(commonDir, objects) {
		t.Fatalf("git-common-dir does not own this repository object directory: common=%q objects=%q", commonDir, objects)
	}
}

func heterociV2ResolveGitPath(t *testing.T, root, kind, raw string) string {
	t.Helper()
	pathValue := strings.TrimSpace(raw)
	if pathValue == "" {
		t.Fatalf("git returned an empty %s path", kind)
	}
	if !filepath.IsAbs(pathValue) {
		pathValue = filepath.Join(root, pathValue)
	}
	if !filepath.IsAbs(pathValue) || filepath.Clean(pathValue) != pathValue {
		t.Fatalf("git returned a non-clean absolute %s path: %q", kind, pathValue)
	}
	return heterociV2CanonicalPath(t, kind, pathValue)
}

func heterociV2RunGit(t *testing.T, root, gitExecutable string, args ...string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), heterociV2GitTimeout)
	defer cancel()
	command := exec.CommandContext(ctx, gitExecutable, args...)
	command.Dir = root
	command.Env = append(os.Environ(), "GIT_OPTIONAL_LOCKS=0")
	command.WaitDelay = 2 * time.Second
	output, err := command.Output()
	if err != nil {
		if ctx.Err() != nil {
			t.Fatalf("bounded git command %q timed out after %s: %v", args, heterociV2GitTimeout, ctx.Err())
		}
		t.Fatalf("bounded git command %q failed: %v", args, err)
	}
	return strings.TrimSpace(string(output))
}

func heterociV2ValidateGoModuleIdentity(t *testing.T, goModPath string) {
	t.Helper()
	raw, err := os.ReadFile(goModPath)
	if err != nil {
		t.Fatalf("read canonical go.mod %q: %v", goModPath, err)
	}
	parsed, err := modfile.Parse(goModPath, raw, nil)
	if err != nil {
		t.Fatalf("parse canonical go.mod %q: %v", goModPath, err)
	}
	if parsed.Module == nil || parsed.Module.Mod.Path == "" {
		t.Fatalf("canonical go.mod %q is missing a module directive", goModPath)
	}
	modulePath := parsed.Module.Mod.Path
	canonicalModulePath := path.Clean(modulePath)
	if strings.Contains(strings.ToLower(canonicalModulePath), heterociV2ModuleMarker) {
		t.Fatalf("canonical go.mod resolves to the rejected V2 module identity: %q", modulePath)
	}
	if modulePath != heterociV3ModulePath {
		t.Fatalf("canonical go.mod module identity = %q, want exact V3 identity %q", modulePath, heterociV3ModulePath)
	}
}

func copyHeterociV2GoSources(t *testing.T, sourceDir, destinationDir string) ([]string, int, int) {
	t.Helper()
	result := heterociV2SourceCopyResult{productionFiles: make([]string, 0)}
	walkErr := filepath.WalkDir(sourceDir, func(sourcePath string, entry fs.DirEntry, walkErr error) error {
		return copyHeterociV2SourceEntry(t, sourceDir, destinationDir, sourcePath, entry, walkErr, &result)
	})
	if walkErr != nil {
		t.Fatalf("copy heteroci source closure %q: %v", sourceDir, walkErr)
	}
	return result.productionFiles, result.productionCount, result.samePackageTestCount
}

type heterociV2SourceCopyResult struct {
	productionFiles      []string
	productionCount      int
	samePackageTestCount int
}

func copyHeterociV2SourceEntry(
	t *testing.T,
	sourceRoot, destinationRoot, sourcePath string,
	entry fs.DirEntry,
	walkErr error,
	result *heterociV2SourceCopyResult,
) error {
	if walkErr != nil {
		return fmt.Errorf("walk source closure at %q: %w", sourcePath, walkErr)
	}
	if sourcePath == sourceRoot {
		return validateHeterociV2SourceRoot(sourcePath, entry)
	}
	if err := validateHeterociV2SourceEntry(sourcePath, entry); err != nil {
		return err
	}
	destinationPath, err := heterociV2SafeJoin(destinationRoot, entry.Name())
	if err != nil {
		return fmt.Errorf("destination path for %q: %w", sourcePath, err)
	}
	isProduction, isSamePackageTest := copyHeterociV2GoSource(t, sourcePath, destinationPath)
	if isSamePackageTest {
		result.samePackageTestCount++
	}
	if isProduction {
		result.productionCount++
		result.productionFiles = append(result.productionFiles, sourcePath)
	}
	return nil
}

func validateHeterociV2SourceRoot(sourcePath string, entry fs.DirEntry) error {
	if entry.Type()&os.ModeSymlink != 0 {
		return fmt.Errorf("heteroci source root is a symlink: %q", sourcePath)
	}
	if !entry.IsDir() {
		return fmt.Errorf("heteroci source root is not a directory: %q", sourcePath)
	}
	return nil
}

func validateHeterociV2SourceEntry(sourcePath string, entry fs.DirEntry) error {
	if entry.Type()&os.ModeSymlink != 0 {
		return fmt.Errorf("heteroci source closure contains symlink: %q", sourcePath)
	}
	if entry.IsDir() {
		return fmt.Errorf("heteroci source closure contains nested directory/package: %q", sourcePath)
	}
	info, err := entry.Info()
	if err != nil {
		return fmt.Errorf("inspect heteroci source closure entry %q: %w", sourcePath, err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("heteroci source closure contains symlink: %q", sourcePath)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("heteroci source closure entry is not a regular file: %q mode=%s", sourcePath, info.Mode())
	}
	if filepath.Ext(entry.Name()) != ".go" {
		return fmt.Errorf("heteroci source closure contains non-Go asset: %q", sourcePath)
	}
	return nil
}

func copyHeterociV2GoSource(t *testing.T, sourcePath, destinationPath string) (bool, bool) {
	t.Helper()
	raw, err := os.ReadFile(sourcePath)
	if err != nil {
		t.Fatalf("read heteroci source %q: %v", sourcePath, err)
	}
	parsed, err := parser.ParseFile(token.NewFileSet(), sourcePath, raw, parser.ImportsOnly|parser.ParseComments)
	if err != nil {
		t.Fatalf("parse heteroci source %q: %v", sourcePath, err)
	}
	for _, group := range parsed.Comments {
		for _, comment := range group.List {
			if strings.HasPrefix(strings.TrimSpace(comment.Text), "//go:embed") {
				t.Fatalf("heteroci source %q uses go:embed; embedded assets are outside the portability closure", sourcePath)
			}
		}
	}
	isTest := strings.HasSuffix(filepath.Base(sourcePath), "_test.go")
	if isTest {
		switch parsed.Name.Name {
		case "heteroci":
			// Same-package tests are part of the isolated source closure.
		case "heteroci_test":
			// External tests remain an explicit skip: they exercise the package through V3 imports.
			return false, false
		default:
			t.Fatalf("test source %q has unsupported package %q; want heteroci or explicit heteroci_test", sourcePath, parsed.Name.Name)
		}
	} else if parsed.Name.Name != "heteroci" {
		t.Fatalf("production source %q has package %q, want heteroci", sourcePath, parsed.Name.Name)
	}
	copyHeterociV2File(t, sourcePath, destinationPath)
	return !isTest, isTest
}

func copyHeterociV2Tree(t *testing.T, sourceRoot, destinationRoot string) int {
	t.Helper()
	count := 0
	err := filepath.WalkDir(sourceRoot, func(sourcePath string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(sourceRoot, sourcePath)
		if err != nil {
			return fmt.Errorf("relative path for %q: %w", sourcePath, err)
		}
		destinationPath, err := heterociV2SafeJoin(destinationRoot, relative)
		if err != nil {
			return fmt.Errorf("destination path for %q: %w", sourcePath, err)
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("refusing symlink in portability fixture source: %q", sourcePath)
		}
		if entry.IsDir() {
			return os.MkdirAll(destinationPath, 0o755)
		}
		if !entry.Type().IsRegular() {
			return fmt.Errorf("deployment fixture source is not a regular file: %q", sourcePath)
		}
		copyHeterociV2File(t, sourcePath, destinationPath)
		count++
		return nil
	})
	if err != nil {
		t.Fatalf("copy deployment fixture tree %q: %v", sourceRoot, err)
	}
	return count
}

func copyHeterociV2File(t *testing.T, sourcePath, destinationPath string) {
	t.Helper()
	heterociV2RequireCleanAbsolutePath(t, "source", sourcePath)
	heterociV2RequireCleanAbsolutePath(t, "destination", destinationPath)
	linkInfo := heterociV2SourceFileInfo(t, sourcePath)
	raw, err := os.ReadFile(sourcePath)
	if err != nil {
		t.Fatalf("read fixture source %q: %v", sourcePath, err)
	}
	if err := os.MkdirAll(filepath.Dir(destinationPath), 0o755); err != nil {
		t.Fatalf("create fixture destination directory for %q: %v", destinationPath, err)
	}
	if err := os.WriteFile(destinationPath, raw, linkInfo.Mode().Perm()); err != nil {
		t.Fatalf("write fixture destination %q: %v", destinationPath, err)
	}
}

func heterociV2RequireCleanAbsolutePath(t *testing.T, kind, path string) {
	t.Helper()
	if !filepath.IsAbs(path) || filepath.Clean(path) != path {
		t.Fatalf("%s path is not a clean absolute path: %q", kind, path)
	}
}

func heterociV2SourceFileInfo(t *testing.T, sourcePath string) fs.FileInfo {
	t.Helper()
	linkInfo, err := os.Lstat(sourcePath)
	if err != nil {
		t.Fatalf("lstat fixture source %q: %v", sourcePath, err)
	}
	if linkInfo.Mode()&os.ModeSymlink != 0 || !linkInfo.Mode().IsRegular() {
		t.Fatalf("fixture source must be a regular non-symlink file: %q mode=%s", sourcePath, linkInfo.Mode())
	}
	return linkInfo
}

func heterociV2SafeJoin(base, relative string) (string, error) {
	if !filepath.IsAbs(base) || filepath.Clean(base) != base {
		return "", fmt.Errorf("base path is not clean absolute: %q", base)
	}
	if filepath.IsAbs(relative) {
		return "", fmt.Errorf("relative path is absolute: %q", relative)
	}
	cleanRelative := filepath.Clean(relative)
	if cleanRelative == ".." || strings.HasPrefix(cleanRelative, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("relative path escapes base: %q", relative)
	}
	joined := filepath.Join(base, cleanRelative)
	back, err := filepath.Rel(base, joined)
	if err != nil {
		return "", fmt.Errorf("resolve joined path: %w", err)
	}
	if back == ".." || strings.HasPrefix(back, ".."+string(filepath.Separator)) || filepath.IsAbs(back) {
		return "", fmt.Errorf("joined path escapes base: %q", relative)
	}
	return joined, nil
}

func checkHeterociV2ProductionImports(t *testing.T, productionFiles []string, fixtureModule, goExecutable string) {
	t.Helper()
	resolvedGo, err := filepath.EvalSymlinks(goExecutable)
	if err != nil {
		t.Fatalf("resolve Go executable symlinks for import guard: %v", err)
	}
	stdlibRoot := filepath.Join(filepath.Dir(filepath.Dir(resolvedGo)), "src")
	info, err := os.Stat(stdlibRoot)
	if err != nil || !info.IsDir() {
		t.Fatalf("Go standard library root is unavailable: %q: %v", stdlibRoot, err)
	}
	for _, sourcePath := range productionFiles {
		checkHeterociV2ProductionFileImports(t, sourcePath, stdlibRoot, fixtureModule)
	}
}

func checkHeterociV2ProductionFileImports(t *testing.T, sourcePath, stdlibRoot, fixtureModule string) {
	t.Helper()
	raw, err := os.ReadFile(sourcePath)
	if err != nil {
		t.Fatalf("read production source for import guard %q: %v", sourcePath, err)
	}
	parsed, err := parser.ParseFile(token.NewFileSet(), sourcePath, raw, parser.ImportsOnly)
	if err != nil {
		t.Fatalf("parse production source for import guard %q: %v", sourcePath, err)
	}
	for _, importSpec := range parsed.Imports {
		importPath, err := strconv.Unquote(importSpec.Path.Value)
		if err != nil {
			t.Fatalf("unquote production import in %q: %v", sourcePath, err)
		}
		if heterociV2ImportAllowed(stdlibRoot, fixtureModule, importPath) {
			continue
		}
		t.Fatalf("production source %q imports non-stdlib/non-V2 module %q", sourcePath, importPath)
	}
}

func heterociV2ImportAllowed(stdlibRoot, fixtureModule, importPath string) bool {
	if heterociV2IsStandardImport(stdlibRoot, importPath) {
		return true
	}
	if importPath == fixtureModule {
		return true
	}
	return strings.HasPrefix(importPath, fixtureModule+"/")
}

func heterociV2IsStandardImport(stdlibRoot, importPath string) bool {
	if importPath == "" || strings.HasPrefix(importPath, ".") || strings.ContainsRune(importPath, '\x00') {
		return false
	}
	standardPath := filepath.Join(stdlibRoot, filepath.FromSlash(importPath))
	info, err := os.Stat(standardPath)
	return err == nil && info.IsDir()
}

func writeHeterociV2FixtureSmokeTest(t *testing.T, fixturePackage string) {
	t.Helper()
	path := filepath.Join(fixturePackage, "v2_portability_fixture_test.go")
	const source = `package heteroci

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestV2PortabilityFixtureStrictManifestAndCapacityBlocker(t *testing.T) {
	manifest := snapshotTestManifest(1)
	raw, err := json.Marshal(manifest)
	if err != nil {
		t.Fatalf("marshal V2 fixture manifest: %v", err)
	}
	directory := t.TempDir()
	canonicalPath := filepath.Join(directory, "manifest.json")
	if err := os.WriteFile(canonicalPath, raw, 0o600); err != nil {
		t.Fatalf("write V2 fixture manifest: %v", err)
	}
	loaded, err := LoadManifestFile(canonicalPath)
	if err != nil {
		t.Fatalf("LoadManifestFile(valid) failed: %v", err)
	}
	if len(loaded.Nodes) != 1 {
		t.Fatalf("LoadManifestFile(valid) = %#v, want one dynamic node", loaded)
	}

	unknownPath := filepath.Join(directory, "unknown.json")
	unknownRaw := append(append([]byte(nil), raw[:len(raw)-1]...), []byte(",\"unknown\":true}")...)
	if err := os.WriteFile(unknownPath, unknownRaw, 0o600); err != nil {
		t.Fatalf("write strict-invalid V2 fixture manifest: %v", err)
	}
	if got, err := LoadManifestFile(unknownPath); err == nil || !reflect.DeepEqual(got, Manifest{}) {
		t.Fatalf("LoadManifestFile(unknown field) = %#v, %v; want zero manifest and error", got, err)
	} else if strings.Contains(err.Error(), unknownPath) {
		t.Fatalf("LoadManifestFile error leaked manifest path %q: %v", unknownPath, err)
	}

	now := snapshotTestNow()
	snapshot := snapshotForManifest(loaded, now)
	snapshot.Nodes[0].Available = ResourceCapacity{}
	units := []ExecutionUnit{{
		UnitID: "capacity-blocked",
		TargetPlatform: PlatformLinuxAMD64,
		RequiredRuntime: RuntimeUbuntuContainer,
		EstimatedDurationMS: 100,
		CPUMilli: 5_000,
		MemoryBytes: 9 * 1024 * 1024 * 1024,
	}}
	plan, decision, err := PlanRoutingDecision(loaded, snapshot, units, now, snapshotTestHeartbeatTTL)
	if err == nil || len(plan.UnassignedUnitIDs) != 1 || !reflect.DeepEqual(decision, RoutingDecision{}) {
		t.Fatalf("PlanRoutingDecision(capacity blocker) = plan:%#v decision:%#v err:%v; want replayable plan, zero decision and blocker", plan, decision, err)
	}
	var blockerErr *CapacityBlockerError
	if !errors.As(err, &blockerErr) || len(blockerErr.Blockers) != 1 || blockerErr.Blockers[0].ReasonCode != CapacityBlockerReasonCapacityUnavailable || blockerErr.ValidateAgainstPlan(plan) != nil {
		t.Fatalf("capacity blocker error = %#v; want one explicit capacity blocker", err)
	}
}
`
	if err := os.WriteFile(path, []byte(source), 0o644); err != nil {
		t.Fatalf("write V2 fixture smoke test: %v", err)
	}
}

func heterociV2Executable(t *testing.T, name string) string {
	t.Helper()
	path, err := exec.LookPath(name)
	if err != nil {
		t.Fatalf("resolve %q executable: %v", name, err)
	}
	path, err = filepath.Abs(path)
	if err != nil {
		t.Fatalf("make %q executable path absolute: %v", name, err)
	}
	if filepath.Clean(path) != path {
		t.Fatalf("%q executable path is not clean: %q", name, path)
	}
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() {
		t.Fatalf("%q executable is not a regular file: %q: %v", name, path, err)
	}
	return path
}

func heterociV2CommandEnvironment(t *testing.T, fixtureRoot, goExecutable, bashExecutable string) []string {
	t.Helper()
	sha256Executable := heterociV2Executable(t, "sha256sum")
	cacheDir := filepath.Join(fixtureRoot, ".gocache")
	tmpDir := filepath.Join(fixtureRoot, ".gotmp")
	moduleCacheDir := filepath.Join(fixtureRoot, ".gomodcache")
	gopathDir := filepath.Join(fixtureRoot, ".gopath")
	homeDir := filepath.Join(fixtureRoot, ".home")
	for _, directory := range []string{cacheDir, tmpDir, moduleCacheDir, gopathDir, homeDir} {
		if err := os.MkdirAll(directory, 0o755); err != nil {
			t.Fatalf("create isolated Go command directory %q: %v", directory, err)
		}
	}
	pathEntries := []string{filepath.Dir(goExecutable), filepath.Dir(bashExecutable), filepath.Dir(sha256Executable), "/usr/bin", "/bin"}
	return []string{
		"PATH=" + strings.Join(pathEntries, string(os.PathListSeparator)),
		"HOME=" + homeDir,
		"TMPDIR=" + tmpDir,
		"GOTMPDIR=" + tmpDir,
		"GOCACHE=" + cacheDir,
		"GOMODCACHE=" + moduleCacheDir,
		"GOPATH=" + gopathDir,
		"GOWORK=off",
		"GO111MODULE=on",
		"GOTOOLCHAIN=local",
		"GOFLAGS=-mod=mod",
		"GOPROXY=off",
		"GOSUMDB=off",
		"GONOSUMDB=*",
		"CGO_ENABLED=0",
	}
}

func runHeterociV2Command(t *testing.T, directory string, environment []string, executable string, args ...string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), heterociV2CommandTimeout)
	defer cancel()
	command := exec.CommandContext(ctx, executable, args...)
	command.Dir = directory
	command.Env = environment
	command.WaitDelay = 2 * time.Second
	var output bytes.Buffer
	command.Stdout = &output
	command.Stderr = &output
	if err := command.Run(); err != nil {
		if ctx.Err() != nil {
			t.Fatalf("bounded command %q timed out after %s: %v\n%s", executable, heterociV2CommandTimeout, ctx.Err(), output.String())
		}
		t.Fatalf("command %q %q failed: %v\n%s", executable, args, err, output.String())
	}
}
