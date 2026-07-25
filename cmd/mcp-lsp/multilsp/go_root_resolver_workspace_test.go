package multilsp

import (
	"path/filepath"
	"reflect"
	"testing"
)

func TestExternalGOWORKOutsideTrustedScopeFallsBackToUpwardRoot(t *testing.T) {
	runExternalGOWORKOutsideTrustedScopeFallsBackToUpwardRoot(t)
}

func TestGoRootResolverExplicitGoWorkOutsideProjectFallsBackToUpwardRoot(t *testing.T) {
	runExternalGOWORKOutsideTrustedScopeFallsBackToUpwardRoot(t)
}

func runExternalGOWORKOutsideTrustedScopeFallsBackToUpwardRoot(t *testing.T) {
	t.Helper()
	repo := normalizedTempDir(t)
	writeGoMod(t, repo, "example.com/current")
	target := writeGoFile(t, repo, "main.go")

	external := normalizedTempDir(t)
	externalMod := filepath.Join(external, "external")
	writeGoMod(t, externalMod, "example.com/external")
	externalGoWork := filepath.Join(external, "go.work")
	writeFile(t, externalGoWork, "go 1.25.0\n\nuse ./external\n")

	info, err := ResolveGoRoot(GoRootRequest{
		CWD:      repo,
		FilePath: target,
		Env:      []string{"GOWORK=" + externalGoWork},
	})
	if err != nil {
		t.Fatalf("external GOWORK outside current scope should fall back to upward root: %v", err)
	}
	assertGoRoot(t, info, GoRootInfo{
		RootKind:      goRootKindGoMod,
		WorkspaceRoot: repo,
		ModuleRoot:    repo,
		GoModPath:     filepath.Join(repo, "go.mod"),
		GOWORKMode:    goworkModeOff,
		ProjectRoot:   repo,
	})
	if info.GoWorkPath != "" {
		t.Fatalf("external GOWORK should be ignored for unrelated target, got go.work %q", info.GoWorkPath)
	}
	if got := goRootEnv(info); !reflect.DeepEqual(got, []string{"GOWORK=off"}) {
		t.Fatalf("external GOWORK should be explicitly disabled for gopls, got %#v", got)
	}
}

func TestGoRootResolverAncestorGoWorkOutsideUseListFallsBackToNearestGoMod(t *testing.T) {
	repo := normalizedTempDir(t)
	mainModule := filepath.Join(repo, "main")
	worktree := filepath.Join(repo, ".worktrees", "feature")
	writeGoMod(t, mainModule, "example.com/main")
	writeGoMod(t, worktree, "example.com/worktree")
	writeFile(t, filepath.Join(repo, "go.work"), "go 1.25.0\n\nuse ./main\n")
	target := writeGoFile(t, worktree, "main.go")

	info, err := ResolveGoRoot(GoRootRequest{CWD: worktree, FilePath: target, Env: []string{}})
	if err != nil {
		t.Fatalf("ancestor go.work outside use list should fall back to nearest go.mod: %v", err)
	}

	assertGoRoot(t, info, GoRootInfo{
		RootKind:      goRootKindGoMod,
		WorkspaceRoot: worktree,
		ModuleRoot:    worktree,
		GoModPath:     filepath.Join(worktree, "go.mod"),
		GOWORKMode:    goworkModeOff,
		ProjectRoot:   worktree,
	})
	if info.GoWorkPath != "" {
		t.Fatalf("ancestor go.work outside use list should not be selected, got %q", info.GoWorkPath)
	}
	if got := goRootEnv(info); !reflect.DeepEqual(got, []string{"GOWORK=off"}) {
		t.Fatalf("ancestor go.work outside use list should be explicitly disabled for gopls, got %#v", got)
	}
	assertFolderPaths(t, info.workspaceFolderPaths(), []string{worktree})
}

func TestGoRootResolverGoWorkRootTargetKeepsGoWork(t *testing.T) {
	repo := normalizedTempDir(t)
	backend := filepath.Join(repo, "backend")
	writeGoMod(t, backend, "example.com/backend")
	goWorkPath := filepath.Join(repo, "go.work")
	writeFile(t, goWorkPath, "go 1.25.0\n\nuse ./backend\n")

	info, err := ResolveGoRoot(GoRootRequest{CWD: repo, FilePath: repo, Env: []string{}})
	if err != nil {
		t.Fatalf("workspace root target should keep go.work: %v", err)
	}
	assertGoRoot(t, info, GoRootInfo{
		RootKind:      goRootKindGoWork,
		WorkspaceRoot: repo,
		GoWorkPath:    goWorkPath,
		ModuleRoots:   []string{backend},
		GOWORKMode:    goworkModeAuto,
		ProjectRoot:   repo,
	})
	if got := goRootEnv(info); !reflect.DeepEqual(got, []string{"GOWORK=" + goWorkPath}) {
		t.Fatalf("workspace root target env = %#v, want go.work env", got)
	}
	assertFolderPaths(t, info.workspaceFolderPaths(), []string{repo, backend})
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

func TestGoWorkUseListSkipsHiddenAndUnderscoreDirs(t *testing.T) {
	runSingleSubmoduleSkipsHiddenAndUnderscoreDirs(t)
}

func TestGoRootResolverSingleSubmoduleSkipsHiddenAndUnderscoreDirs(t *testing.T) {
	runSingleSubmoduleSkipsHiddenAndUnderscoreDirs(t)
}

func runSingleSubmoduleSkipsHiddenAndUnderscoreDirs(t *testing.T) {
	t.Helper()
	repo := normalizedTempDir(t)
	backend := filepath.Join(repo, "backend")
	hidden := filepath.Join(repo, ".hidden")
	underscore := filepath.Join(repo, "_tools")
	writeGoMod(t, backend, "example.com/backend")
	writeGoMod(t, hidden, "example.com/hidden")
	writeGoMod(t, underscore, "example.com/tools")

	info, err := ResolveGoRoot(GoRootRequest{CWD: repo, FilePath: repo, Env: []string{}})
	if err != nil {
		t.Fatalf("resolve child modules with hidden/underscore dirs: %v", err)
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
