package multilsp

import (
	"os"
	"path/filepath"
	"reflect"
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

func TestGoRootResolverGOWORKOff(t *testing.T) {
	repo := normalizedTempDir(t)
	backend := filepath.Join(repo, "backend")
	writeGoMod(t, backend, "example.com/backend")
	writeFile(t, filepath.Join(repo, "go.work"), "go 1.25.0\n\nuse ./backend\n")
	target := writeGoFile(t, backend, "main.go")

	info, err := ResolveGoRoot(GoRootRequest{CWD: repo, FilePath: target, Env: []string{"GOWORK=off"}})
	if err != nil {
		t.Fatalf("resolve GOWORK=off root: %v", err)
	}

	assertGoRoot(t, info, GoRootInfo{
		RootKind:      goRootKindGoMod,
		WorkspaceRoot: backend,
		ModuleRoot:    backend,
		GoModPath:     filepath.Join(backend, "go.mod"),
		GOWORKMode:    goworkModeOff,
		ProjectRoot:   repo,
	})
	if info.GoWorkPath != "" {
		t.Fatalf("GOWORK=off should ignore go.work, got %q", info.GoWorkPath)
	}
	if got := goRootEnv(info); !reflect.DeepEqual(got, []string{"GOWORK=off"}) {
		t.Fatalf("GOWORK=off env = %#v", got)
	}
	assertFolderPaths(t, info.workspaceFolderPaths(), []string{backend})
	assertGoLanguageSpecificContainsTopology(t, info)
}

func TestGoRootResolverSingleSubmodule(t *testing.T) {
	repo := normalizedTempDir(t)
	backend := filepath.Join(repo, "backend")
	writeGoMod(t, backend, "example.com/backend")

	info, err := ResolveGoRoot(GoRootRequest{CWD: repo, FilePath: repo, Env: []string{}})
	if err != nil {
		t.Fatalf("resolve single submodule root: %v", err)
	}

	assertGoRoot(t, info, GoRootInfo{
		RootKind:      goRootKindSingleSubmodule,
		WorkspaceRoot: backend,
		ModuleRoot:    backend,
		GoModPath:     filepath.Join(backend, "go.mod"),
		ModuleRoots:   []string{backend},
		GOWORKMode:    goworkModeAuto,
		ProjectRoot:   repo,
	})
	assertFolderPaths(t, info.workspaceFolderPaths(), []string{backend})
	assertGoLanguageSpecificContainsTopology(t, info)
}

func TestGoRootResolverMultiModule(t *testing.T) {
	repo := normalizedTempDir(t)
	backend := filepath.Join(repo, "backend")
	tools := filepath.Join(repo, "tools")
	writeGoMod(t, backend, "example.com/backend")
	writeGoMod(t, tools, "example.com/tools")

	info, err := ResolveGoRoot(GoRootRequest{CWD: repo, FilePath: repo, Env: []string{}})
	if err != nil {
		t.Fatalf("resolve multi module root: %v", err)
	}

	assertGoRoot(t, info, GoRootInfo{
		RootKind:      goRootKindMultiModule,
		WorkspaceRoot: repo,
		ModuleRoots:   []string{backend, tools},
		GOWORKMode:    goworkModeAuto,
		ProjectRoot:   repo,
	})
	assertFolderPaths(t, info.workspaceFolderPaths(), []string{repo, backend, tools})
	assertGoLanguageSpecificContainsTopology(t, info)
}

func TestGoRootResolverNestedModule(t *testing.T) {
	repo := normalizedTempDir(t)
	writeGoMod(t, repo, "example.com/root")
	nested := filepath.Join(repo, "plugins", "x")
	writeGoMod(t, nested, "example.com/pluginx")
	target := writeGoFile(t, nested, "main.go")

	info, err := ResolveGoRoot(GoRootRequest{CWD: repo, FilePath: target, Env: []string{}})
	if err != nil {
		t.Fatalf("resolve nested module root: %v", err)
	}

	assertGoRoot(t, info, GoRootInfo{
		RootKind:      goRootKindGoMod,
		WorkspaceRoot: nested,
		ModuleRoot:    nested,
		GoModPath:     filepath.Join(nested, "go.mod"),
		ModuleRoots:   []string{nested},
		GOWORKMode:    goworkModeAuto,
		ProjectRoot:   repo,
	})
	assertFolderPaths(t, info.workspaceFolderPaths(), []string{nested})
	assertGoLanguageSpecificContainsTopology(t, info)
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

func assertGoLanguageSpecificContainsTopology(t *testing.T, info GoRootInfo) {
	t.Helper()
	specific := goLanguageSpecific(info)
	for _, key := range []string{"goWorkPath", "goModPath", "moduleRoot", "goworkMode", "moduleRootsHash", "workspaceFoldersHash"} {
		if _, ok := specific[key]; !ok {
			t.Fatalf("languageSpecific missing %s: %#v", key, specific)
		}
	}
	if specific["goWorkPath"] != info.GoWorkPath ||
		specific["goModPath"] != info.GoModPath ||
		specific["moduleRoot"] != info.ModuleRoot ||
		specific["goworkMode"] != info.GOWORKMode {
		t.Fatalf("languageSpecific did not preserve go paths/mode: %#v for %#v", specific, info)
	}
	if specific["moduleRootsHash"] == "" || specific["workspaceFoldersHash"] == "" {
		t.Fatalf("languageSpecific missing topology hashes: %#v", specific)
	}
	key := goWorkspaceKey(info)
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
