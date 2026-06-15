package multilsp

import (
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
)

func TestGoRootResolverGoMod(t *testing.T) {
	repo := normalizedTempDir(t)
	writeGoMod(t, repo, "example.com/root")
	target := writeGoFile(t, repo, "main.go")

	info, err := ResolveGoRoot(GoRootRequest{CWD: repo, FilePath: target, Env: []string{}})
	if err != nil {
		t.Fatalf("resolve go.mod root: %v", err)
	}

	assertGoRoot(t, info, GoRootInfo{
		RootKind:      goRootKindGoMod,
		WorkspaceRoot: repo,
		ModuleRoot:    repo,
		GoModPath:     filepath.Join(repo, "go.mod"),
		GOWORKMode:    goworkModeAuto,
		ProjectRoot:   repo,
	})
}

func TestGoRootResolverUsesGoModVersionBeforePATHDefault(t *testing.T) {
	repo := normalizedTempDir(t)
	backend := filepath.Join(repo, "backend")
	writeFile(t, filepath.Join(backend, "go.mod"), "module example.com/backend\n\ngo 1.26.4\n")
	target := writeGoFile(t, backend, "main.go")
	oldGoDir := writeFakeGoVersion(t, repo, "old-go", "go version go1.25.6 darwin/arm64")
	requiredGoDir := writeFakeGoVersion(t, repo, "required-go", "go version go1.26.4 darwin/arm64")

	info, err := ResolveGoRoot(GoRootRequest{
		CWD:      repo,
		FilePath: target,
		Env:      []string{"PATH=" + oldGoDir + string(os.PathListSeparator) + requiredGoDir},
	})
	if err != nil {
		t.Fatalf("ResolveGoRoot() error = %v", err)
	}

	wantPath := "PATH=" + requiredGoDir + string(os.PathListSeparator) + oldGoDir
	wantEnv := []string{wantPath, "GOTOOLCHAIN=local"}
	if got := goRootEnv(info); !reflect.DeepEqual(got, wantEnv) {
		t.Fatalf("go.mod toolchain env = %#v, want %#v", got, wantEnv)
	}
}

func TestGoRootResolverFailsWhenGoModVersionMissingFromPATH(t *testing.T) {
	repo := normalizedTempDir(t)
	backend := filepath.Join(repo, "backend")
	writeFile(t, filepath.Join(backend, "go.mod"), "module example.com/backend\n\ngo 1.26.4\n")
	target := writeGoFile(t, backend, "main.go")
	oldGoDir := writeFakeGoVersion(t, repo, "old-go", "go version go1.25.6 darwin/arm64")

	info, err := ResolveGoRoot(GoRootRequest{
		CWD:      repo,
		FilePath: target,
		Env:      []string{"PATH=" + oldGoDir},
	})
	if err == nil {
		t.Fatalf("ResolveGoRoot() error = nil, got info %#v", info)
	}
	for _, fragment := range []string{"go.mod", "go 1.26.4", "PATH"} {
		if !strings.Contains(err.Error(), fragment) {
			t.Fatalf("ResolveGoRoot() error = %v, want fragment %q", err, fragment)
		}
	}
}

func TestGoRootResolverGoWork(t *testing.T) {
	repo := normalizedTempDir(t)
	backend := filepath.Join(repo, "backend")
	tools := filepath.Join(repo, "tools")
	writeGoMod(t, backend, "example.com/backend")
	writeGoMod(t, tools, "example.com/tools")
	writeFile(t, filepath.Join(repo, "go.work"), "go 1.25.0\n\nuse (\n\t./tools\n\t./backend\n)\n")
	target := writeGoFile(t, backend, "main.go")

	info, err := ResolveGoRoot(GoRootRequest{CWD: repo, FilePath: target, Env: []string{}})
	if err != nil {
		t.Fatalf("resolve go.work root: %v", err)
	}

	assertGoRoot(t, info, GoRootInfo{
		RootKind:      goRootKindGoWork,
		WorkspaceRoot: repo,
		GoWorkPath:    filepath.Join(repo, "go.work"),
		ModuleRoot:    backend,
		GoModPath:     filepath.Join(backend, "go.mod"),
		ModuleRoots:   []string{backend, tools},
		GOWORKMode:    goworkModeAuto,
		ProjectRoot:   repo,
	})
	assertFolderPaths(t, info.workspaceFolderPaths(), []string{repo, backend, tools})
	if got := goRootEnv(info); !reflect.DeepEqual(got, []string{"GOWORK=" + filepath.Join(repo, "go.work")}) {
		t.Fatalf("go.work env = %#v", got)
	}
	assertGoLanguageSpecificContainsTopology(t, info)
}

func TestGoRootResolverGoWorkQuotedUsePath(t *testing.T) {
	repo := normalizedTempDir(t)
	spaced := filepath.Join(repo, "module with space")
	tools := filepath.Join(repo, "tools")
	writeGoMod(t, spaced, "example.com/spaced")
	writeGoMod(t, tools, "example.com/tools")
	writeFile(t, filepath.Join(repo, "go.work"), "go 1.25.0\n\nuse (\n\t\"./module with space\"\n\t./tools\n)\n")
	target := writeGoFile(t, spaced, "main.go")

	info, err := ResolveGoRoot(GoRootRequest{CWD: repo, FilePath: target, Env: []string{}})
	if err != nil {
		t.Fatalf("resolve go.work quoted use path: %v", err)
	}

	assertGoRoot(t, info, GoRootInfo{
		RootKind:      goRootKindGoWork,
		WorkspaceRoot: repo,
		GoWorkPath:    filepath.Join(repo, "go.work"),
		ModuleRoot:    spaced,
		GoModPath:     filepath.Join(spaced, "go.mod"),
		ModuleRoots:   []string{spaced, tools},
		GOWORKMode:    goworkModeAuto,
		ProjectRoot:   repo,
	})
	assertFolderPaths(t, info.workspaceFolderPaths(), []string{repo, spaced, tools})
}

func TestGoRootResolverGoWorkFileTarget(t *testing.T) {
	repo := normalizedTempDir(t)
	backend := filepath.Join(repo, "backend")
	writeGoMod(t, backend, "example.com/backend")
	goWorkPath := filepath.Join(repo, "go.work")
	writeFile(t, goWorkPath, "go 1.25.0\n\nuse ./backend\n")

	info, err := ResolveGoRoot(GoRootRequest{CWD: repo, FilePath: goWorkPath, Env: []string{}})
	if err != nil {
		t.Fatalf("resolve go.work file target: %v", err)
	}
	if info.RootKind != goRootKindGoWork || info.WorkspaceRoot != repo || info.GoWorkPath != goWorkPath {
		t.Fatalf("unexpected go.work file target info: %#v", info)
	}
	if info.ModuleRoot != "" || info.GoModPath != "" {
		t.Fatalf("go.work file target should not force a module root: %#v", info)
	}
	assertFolderPaths(t, info.workspaceFolderPaths(), []string{repo, backend})
}

func TestGoRootResolverExplicitGoWork(t *testing.T) {
	repo := normalizedTempDir(t)
	backend := filepath.Join(repo, "backend")
	writeGoMod(t, backend, "example.com/backend")
	goWorkPath := filepath.Join(repo, "go.work")
	writeFile(t, goWorkPath, "go 1.25.0\n\nuse ./backend\n")
	target := writeGoFile(t, backend, "main.go")

	info, err := ResolveGoRoot(GoRootRequest{
		CWD:      filepath.Join(repo, "unrelated"),
		FilePath: target,
		Env:      []string{"GOWORK=" + goWorkPath},
	})
	if err != nil {
		t.Fatalf("resolve explicit go.work root: %v", err)
	}
	if info.RootKind != goRootKindGoWork || info.GOWORKMode != goworkModeExplicit {
		t.Fatalf("expected explicit go.work root, got %#v", info)
	}
	if info.GoWorkPath != goWorkPath || info.WorkspaceRoot != repo || info.ModuleRoot != backend {
		t.Fatalf("unexpected explicit go.work info: %#v", info)
	}
}

func TestGOWORKAutoUsesAutoDiscovery(t *testing.T) {
	runGOWORKAutoUsesAutoDiscovery(t)
}

func TestGoRootResolverGOWORKAutoUsesAutoDiscovery(t *testing.T) {
	runGOWORKAutoUsesAutoDiscovery(t)
}

func runGOWORKAutoUsesAutoDiscovery(t *testing.T) {
	t.Helper()
	repo := normalizedTempDir(t)
	backend := filepath.Join(repo, "backend")
	writeGoMod(t, backend, "example.com/backend")
	goWorkPath := filepath.Join(repo, "go.work")
	writeFile(t, goWorkPath, "go 1.25.0\n\nuse ./backend\n")
	target := writeGoFile(t, backend, "main.go")

	info, err := ResolveGoRoot(GoRootRequest{
		CWD:      repo,
		FilePath: target,
		Env:      []string{"GOWORK=auto"},
	})
	if err != nil {
		t.Fatalf("GOWORK=auto should use automatic go.work discovery: %v", err)
	}
	assertGoRoot(t, info, GoRootInfo{
		RootKind:      goRootKindGoWork,
		WorkspaceRoot: repo,
		GoWorkPath:    goWorkPath,
		ModuleRoot:    backend,
		GoModPath:     filepath.Join(backend, "go.mod"),
		ModuleRoots:   []string{backend},
		GOWORKMode:    goworkModeAuto,
		ProjectRoot:   repo,
	})
	if got := goRootEnv(info); !reflect.DeepEqual(got, []string{"GOWORK=" + goWorkPath}) {
		t.Fatalf("GOWORK=auto resolved env = %#v, want discovered go.work", got)
	}
}

func TestBrokenGoWorkFailsFast(t *testing.T) {
	runBrokenGoWorkFailsClosed(t)
}

func TestGoRootResolverBrokenGoWorkFailsFast(t *testing.T) {
	runBrokenGoWorkFailsClosed(t)
}

func TestResolveGoWorkRootRejectsBrokenGoWork(t *testing.T) {
	runBrokenGoWorkFailsClosed(t)
}

func TestGoRootResolverBrokenGoWorkFailsClosed(t *testing.T) {
	runBrokenGoWorkFailsClosed(t)
}

func runBrokenGoWorkFailsClosed(t *testing.T) {
	t.Helper()
	repo := normalizedTempDir(t)
	backend := filepath.Join(repo, "backend")
	writeGoMod(t, backend, "example.com/backend")
	goWorkPath := filepath.Join(repo, "go.work")
	writeFile(t, goWorkPath, "go 1.25.0\n\nuse (\n\t./backend\n")
	target := writeGoFile(t, backend, "main.go")

	info, err := ResolveGoRoot(GoRootRequest{CWD: repo, FilePath: target, Env: []string{}})
	if err == nil {
		t.Fatalf("broken go.work error = nil, got info %#v", info)
	}
	for _, fragment := range []string{"parse go.work", goWorkPath} {
		if !strings.Contains(err.Error(), fragment) {
			t.Fatalf("broken go.work error = %v, want fragment %q", err, fragment)
		}
	}
	assertGoRoot(t, info, GoRootInfo{})
}

func TestWorkspaceFolderAndGoWorkspaceKeyHashes(t *testing.T) {
	repo := normalizedTempDir(t)
	backend := filepath.Join(repo, "backend")
	tools := filepath.Join(repo, "tools")
	info := GoRootInfo{
		RootKind:      goRootKindMultiModule,
		WorkspaceRoot: repo,
		ModuleRoots:   []string{tools, backend, backend},
		GOWORKMode:    goworkModeAuto,
		ProjectRoot:   repo,
	}

	assertFolderPaths(t, info.workspaceFolderPaths(), []string{repo, backend, tools})
	folders := workspaceFolders(info)
	if len(folders) != 3 || folders[0].URI != fileURIFromPath(repo) {
		t.Fatalf("workspaceFolders should keep workspace root first, got %#v", folders)
	}
	specific := goLanguageSpecific(info)
	for _, key := range []string{"goWorkPath", "goModPath", "moduleRoot", "goworkMode", "moduleRootsHash", "workspaceFoldersHash"} {
		if _, ok := specific[key]; !ok {
			t.Fatalf("languageSpecific missing %q: %#v", key, specific)
		}
	}
	if specific["moduleRootsHash"] == "" || specific["workspaceFoldersHash"] == "" {
		t.Fatalf("expected non-empty topology hashes: %#v", specific)
	}
	key := goWorkspaceKey(info)
	if !strings.Contains(key, "moduleRootsHash=") || !strings.Contains(key, "workspaceFoldersHash=") {
		t.Fatalf("workspace key does not include topology hashes: %q", key)
	}
}

func TestGoWorkspaceKeyTopologyHashesAreCanonical(t *testing.T) {
	repo := normalizedTempDir(t)
	backend := filepath.Join(repo, "backend")
	tools := filepath.Join(repo, "tools")
	base := GoRootInfo{
		RootKind:      goRootKindGoWork,
		WorkspaceRoot: repo,
		GoWorkPath:    filepath.Join(repo, "go.work"),
		ModuleRoot:    backend,
		GoModPath:     filepath.Join(backend, "go.mod"),
		GOWORKMode:    goworkModeAuto,
		ProjectRoot:   repo,
	}
	withUnsortedDuplicates := base
	withUnsortedDuplicates.ModuleRoots = []string{tools, backend, backend}
	withSortedUnique := base
	withSortedUnique.ModuleRoots = []string{backend, tools}

	unsortedSpecific := goLanguageSpecific(withUnsortedDuplicates)
	sortedSpecific := goLanguageSpecific(withSortedUnique)
	if unsortedSpecific["moduleRootsHash"] != sortedSpecific["moduleRootsHash"] {
		t.Fatalf("moduleRootsHash should be stable after sort/dedupe: %q vs %q", unsortedSpecific["moduleRootsHash"], sortedSpecific["moduleRootsHash"])
	}
	if unsortedSpecific["workspaceFoldersHash"] != sortedSpecific["workspaceFoldersHash"] {
		t.Fatalf("workspaceFoldersHash should be stable after sort/dedupe: %q vs %q", unsortedSpecific["workspaceFoldersHash"], sortedSpecific["workspaceFoldersHash"])
	}
	if goWorkspaceKey(withUnsortedDuplicates) != goWorkspaceKey(withSortedUnique) {
		t.Fatalf("canonical-equivalent Go topology should produce the same workspace key")
	}

	withoutTools := base
	withoutTools.ModuleRoots = []string{backend}
	if goWorkspaceKey(withSortedUnique) == goWorkspaceKey(withoutTools) {
		t.Fatalf("workspace key must change when Go module topology changes")
	}
}

func TestGoRootResolverLinkedWorktreeWorkspaceKey(t *testing.T) {
	wtA := filepath.Join(normalizedTempDir(t), "wt-a")
	wtB := filepath.Join(normalizedTempDir(t), "wt-b")
	writeGoMod(t, wtA, "example.com/root")
	writeGoMod(t, wtB, "example.com/root")
	targetA := writeGoFile(t, wtA, "main.go")
	targetB := writeGoFile(t, wtB, "main.go")

	infoA, err := ResolveGoRoot(GoRootRequest{CWD: wtA, FilePath: targetA, Env: []string{}})
	if err != nil {
		t.Fatalf("resolve worktree A: %v", err)
	}
	infoB, err := ResolveGoRoot(GoRootRequest{CWD: wtB, FilePath: targetB, Env: []string{}})
	if err != nil {
		t.Fatalf("resolve worktree B: %v", err)
	}
	if goWorkspaceKey(infoA) == goWorkspaceKey(infoB) {
		t.Fatalf("linked worktree workspace keys should differ: %q", goWorkspaceKey(infoA))
	}
	if parts := goWorkspaceKeyPartsFor(infoA); parts.ProjectRoot != wtA || parts.WorkspaceRoot != wtA {
		t.Fatalf("workspace key A should use physical worktree root, got %#v", parts)
	}
	if parts := goWorkspaceKeyPartsFor(infoB); parts.ProjectRoot != wtB || parts.WorkspaceRoot != wtB {
		t.Fatalf("workspace key B should use physical worktree root, got %#v", parts)
	}
}

func TestGoRootResolverLinkedWorktreeSymlinkAliasCanonicalKey(t *testing.T) {
	realRoot := filepath.Join(normalizedTempDir(t), "real")
	writeGoMod(t, realRoot, "example.com/root")
	realTarget := writeGoFile(t, realRoot, "main.go")
	aliasRoot := filepath.Join(normalizedTempDir(t), "alias")
	if err := os.Symlink(realRoot, aliasRoot); err != nil {
		if isSymlinkPrivilegeNotHeld(err) {
			t.Skipf("symlink privilege unavailable: %v", err)
		}
		t.Fatalf("create symlink alias: %v", err)
	}
	aliasTarget := filepath.Join(aliasRoot, "main.go")

	realInfo, err := ResolveGoRoot(GoRootRequest{CWD: realRoot, FilePath: realTarget, Env: []string{}})
	if err != nil {
		t.Fatalf("resolve real root: %v", err)
	}
	aliasInfo, err := ResolveGoRoot(GoRootRequest{CWD: aliasRoot, FilePath: aliasTarget, Env: []string{}})
	if err != nil {
		t.Fatalf("resolve symlink alias root: %v", err)
	}
	if goWorkspaceKey(realInfo) != goWorkspaceKey(aliasInfo) {
		t.Fatalf("same physical workspace via symlink alias should share canonical key\nreal:  %q\nalias: %q", goWorkspaceKey(realInfo), goWorkspaceKey(aliasInfo))
	}
	if aliasInfo.WorkspaceRoot != realRoot || aliasInfo.ProjectRoot != realRoot {
		t.Fatalf("symlink alias should resolve to physical root %q, got %#v", realRoot, aliasInfo)
	}
}

func assertGoLanguageSpecificContainsTopology(t *testing.T, info GoRootInfo) {
	t.Helper()
	specific := goLanguageSpecific(info)
	assertLanguageSpecificKeys(t, specific)
	assertLanguageSpecificPaths(t, specific, info)
	assertWorkspaceKeyFragments(t, goWorkspaceKey(info))
}

func assertLanguageSpecificKeys(t *testing.T, specific map[string]string) {
	t.Helper()
	for _, key := range []string{"goWorkPath", "goModPath", "moduleRoot", "goworkMode", "moduleRootsHash", "workspaceFoldersHash"} {
		if _, ok := specific[key]; !ok {
			t.Fatalf("languageSpecific missing %s: %#v", key, specific)
		}
	}
}

func assertLanguageSpecificPaths(t *testing.T, specific map[string]string, info GoRootInfo) {
	t.Helper()
	if specific["goWorkPath"] != info.GoWorkPath ||
		specific["goModPath"] != info.GoModPath ||
		specific["moduleRoot"] != info.ModuleRoot ||
		specific["goworkMode"] != info.GOWORKMode {
		t.Fatalf("languageSpecific did not preserve go paths/mode: %#v for %#v", specific, info)
	}
	if specific["moduleRootsHash"] == "" || specific["workspaceFoldersHash"] == "" {
		t.Fatalf("languageSpecific missing topology hashes: %#v", specific)
	}
}

func assertWorkspaceKeyFragments(t *testing.T, key string) {
	t.Helper()
	for _, fragment := range []string{"goModPath=", "goWorkPath=", "goworkMode=", "moduleRoot=", "moduleRootsHash=", "workspaceFoldersHash="} {
		if !strings.Contains(key, fragment) {
			t.Fatalf("workspace key %q missing %q", key, fragment)
		}
	}
}

func assertGoRoot(t *testing.T, got GoRootInfo, want GoRootInfo) {
	t.Helper()
	if got.RootKind != want.RootKind ||
		got.WorkspaceRoot != want.WorkspaceRoot ||
		got.GoWorkPath != want.GoWorkPath ||
		got.ModuleRoot != want.ModuleRoot ||
		got.GoModPath != want.GoModPath ||
		got.GOWORKMode != want.GOWORKMode ||
		got.ProjectRoot != want.ProjectRoot {
		t.Fatalf("go root mismatch\ngot:  %#v\nwant: %#v", got, want)
	}
	if want.ModuleRoots != nil {
		assertFolderPaths(t, got.ModuleRoots, want.ModuleRoots)
	}
}

func assertFolderPaths(t *testing.T, got, want []string) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("paths mismatch\ngot:  %#v\nwant: %#v", got, want)
	}
}

func normalizedTempDir(t *testing.T) string {
	t.Helper()
	dir, err := normalizeOptionalPath(t.TempDir(), "")
	if err != nil {
		t.Fatalf("normalize temp dir: %v", err)
	}
	return dir
}

func writeGoMod(t *testing.T, dir, module string) {
	t.Helper()
	writeFile(t, filepath.Join(dir, "go.mod"), "module "+module+"\n\ngo 1.25.0\n")
}

func writeFakeGoVersion(t *testing.T, root, name, output string) string {
	t.Helper()
	dir := filepath.Join(root, name)
	body := "#!/bin/sh\n/bin/echo '" + output + "'\nexit 0\n"
	executable := "go"
	if runtime.GOOS == "windows" {
		executable = "go.cmd"
		body = "@echo off\r\necho " + output + "\r\nexit /b 0\r\n"
	}
	path := filepath.Join(dir, executable)
	writeFile(t, path, body)
	if err := os.Chmod(path, 0o755); err != nil {
		t.Fatalf("chmod fake go: %v", err)
	}
	return dir
}

func writeGoFile(t *testing.T, dir, name string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	writeFile(t, path, "package main\n")
	return path
}

func writeFile(t *testing.T, path, contents string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
