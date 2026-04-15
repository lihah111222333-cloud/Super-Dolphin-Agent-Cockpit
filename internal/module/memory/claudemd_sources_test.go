package memory

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"testing"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
)

func TestResolveClaudeMdSourcesOrdersLayersAndPreservesRuleMetadata(t *testing.T) {
	t.Setenv(envAdditionalDirectoriesClaudeMd, "1")
	base := t.TempDir()
	managedRoot := filepath.Join(base, "managed")
	userRoot := filepath.Join(base, "user")
	repoRoot := filepath.Join(base, "repo")
	cwd := filepath.Join(repoRoot, "service")
	addDir := filepath.Join(base, "extra")
	autoRoot := filepath.Join(base, "automem")
	teamRoot := filepath.Join(base, "teammem")

	for _, dir := range []string{managedRoot, userRoot, repoRoot, cwd, addDir, autoRoot, teamRoot} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("MkdirAll(%q) error = %v", dir, err)
		}
	}
	writeClaudeFile(t, filepath.Join(managedRoot, "CLAUDE.md"), "managed base")
	writeClaudeFile(t, filepath.Join(managedRoot, ".claude", "rules", "managed.md"), "---\ndescription: Managed rule\n---\nmanaged rule")
	writeClaudeFile(t, filepath.Join(managedRoot, ".claude", "rules", "conditional.md"), "---\npaths:\n  - src/**\n---\nconditional rule")
	writeClaudeFile(t, filepath.Join(userRoot, "CLAUDE.md"), "user base")
	writeClaudeFile(t, filepath.Join(userRoot, ".claude", "rules", "user.md"), "user rule")
	writeClaudeFile(t, filepath.Join(repoRoot, "CLAUDE.md"), "project root")
	writeClaudeFile(t, filepath.Join(repoRoot, ".claude", "CLAUDE.md"), "project private")
	writeClaudeFile(t, filepath.Join(repoRoot, ".claude", "rules", "project.md"), "project rule")
	writeClaudeFile(t, filepath.Join(repoRoot, "CLAUDE.local.md"), "project local")
	writeClaudeFile(t, filepath.Join(cwd, "CLAUDE.md"), "cwd base")
	writeClaudeFile(t, filepath.Join(cwd, "CLAUDE.local.md"), "cwd local")
	writeClaudeFile(t, filepath.Join(addDir, "CLAUDE.md"), "add-dir base")
	writeClaudeFile(t, filepath.Join(addDir, ".claude", "rules", "extra.md"), "extra rule")
	writeClaudeFile(t, memoryIndexPath(autoRoot), "- [Auto](auto.md) — auto memory")
	writeClaudeFile(t, memoryIndexPath(teamRoot), "- [Team](team.md) — team memory")

	sources := ResolveClaudeMdSources(context.Background(), ClaudeMdResolveConfig{
		BuildCtx: contract.BuildCtx{
			GitRoot:                      repoRoot,
			CWD:                          cwd,
			AdditionalWorkingDirectories: []string{addDir},
		},
		MemoryConfig: &Config{Enabled: true, RootDir: base, ProjectRoot: repoRoot, AutoMemPathOverride: autoRoot},
		TeamMemPath:  teamRoot,
		ManagedRoots: []string{managedRoot},
		UserRoot:     userRoot,
	})
	got := sourcePaths(sources)
	want := []string{
		mustResolvedClaudePath(t, filepath.Join(managedRoot, "CLAUDE.md")),
		mustResolvedClaudePath(t, filepath.Join(managedRoot, ".claude", "rules", "conditional.md")),
		mustResolvedClaudePath(t, filepath.Join(managedRoot, ".claude", "rules", "managed.md")),
		mustResolvedClaudePath(t, filepath.Join(userRoot, "CLAUDE.md")),
		mustResolvedClaudePath(t, filepath.Join(userRoot, ".claude", "rules", "user.md")),
		mustResolvedClaudePath(t, filepath.Join(repoRoot, "CLAUDE.md")),
		mustResolvedClaudePath(t, filepath.Join(repoRoot, ".claude", "CLAUDE.md")),
		mustResolvedClaudePath(t, filepath.Join(repoRoot, ".claude", "rules", "project.md")),
		mustResolvedClaudePath(t, filepath.Join(repoRoot, "CLAUDE.local.md")),
		mustResolvedClaudePath(t, filepath.Join(cwd, "CLAUDE.md")),
		mustResolvedClaudePath(t, filepath.Join(cwd, "CLAUDE.local.md")),
		mustResolvedClaudePath(t, filepath.Join(addDir, "CLAUDE.md")),
		mustResolvedClaudePath(t, filepath.Join(addDir, ".claude", "rules", "extra.md")),
		mustResolvedClaudePath(t, memoryIndexPath(autoRoot)),
		mustResolvedClaudePath(t, memoryIndexPath(teamRoot)),
	}
	if !slices.Equal(got, want) {
		t.Fatalf("ResolveClaudeMdSources() paths = %#v, want %#v", got, want)
	}
	conditional := sourceByPath(t, sources, mustResolvedClaudePath(t, filepath.Join(managedRoot, ".claude", "rules", "conditional.md")))
	if !conditional.Conditional || !slices.Equal(conditional.Globs, []string{"src/**"}) {
		t.Fatalf("conditional rule = %#v, want Conditional=true with globs", conditional)
	}
	if conditional.BaseDir != managedRoot || conditional.RuleScope != sourceOriginManaged {
		t.Fatalf("conditional rule metadata = %#v, want baseDir=%q ruleScope=%q", conditional, managedRoot, sourceOriginManaged)
	}
}

func TestResolveClaudeMdSourcesBareWithoutAdditionalDirsDisablesAll(t *testing.T) {
	base := t.TempDir()
	managedRoot := filepath.Join(base, "managed")
	userRoot := filepath.Join(base, "user")
	repoRoot := filepath.Join(base, "repo")
	for _, dir := range []string{managedRoot, userRoot, repoRoot} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("MkdirAll(%q) error = %v", dir, err)
		}
	}
	writeClaudeFile(t, filepath.Join(managedRoot, "CLAUDE.md"), "managed base")
	writeClaudeFile(t, filepath.Join(userRoot, "CLAUDE.md"), "user base")
	writeClaudeFile(t, filepath.Join(repoRoot, "CLAUDE.md"), "project base")
	if got := ResolveClaudeMdSources(context.Background(), ClaudeMdResolveConfig{
		BuildCtx:     contract.BuildCtx{GitRoot: repoRoot, CWD: repoRoot, SessionFlags: map[string]bool{"bare_mode": true}},
		ManagedRoots: []string{managedRoot},
		UserRoot:     userRoot,
	}); len(got) != 0 {
		t.Fatalf("ResolveClaudeMdSources() = %#v, want nil/empty under bare mode without add-dir", got)
	}
}

func TestResolveClaudeMdSourcesBareHonorsExplicitAdditionalDirs(t *testing.T) {
	t.Setenv(envAdditionalDirectoriesClaudeMd, "1")
	base := t.TempDir()
	managedRoot := filepath.Join(base, "managed")
	userRoot := filepath.Join(base, "user")
	repoRoot := filepath.Join(base, "repo")
	addDir := filepath.Join(base, "extra")
	for _, dir := range []string{managedRoot, userRoot, repoRoot, addDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("MkdirAll(%q) error = %v", dir, err)
		}
	}
	writeClaudeFile(t, filepath.Join(managedRoot, "CLAUDE.md"), "managed base")
	writeClaudeFile(t, filepath.Join(userRoot, "CLAUDE.md"), "user base")
	writeClaudeFile(t, filepath.Join(repoRoot, "CLAUDE.md"), "project base")
	writeClaudeFile(t, filepath.Join(addDir, "CLAUDE.md"), "add-dir base")
	sources := ResolveClaudeMdSources(context.Background(), ClaudeMdResolveConfig{
		BuildCtx: contract.BuildCtx{
			GitRoot:                      repoRoot,
			CWD:                          repoRoot,
			SessionFlags:                 map[string]bool{"bare_mode": true},
			AdditionalWorkingDirectories: []string{addDir},
		},
		ManagedRoots: []string{managedRoot},
		UserRoot:     userRoot,
	})
	got := sourcePaths(sources)
	want := []string{
		mustResolvedClaudePath(t, filepath.Join(managedRoot, "CLAUDE.md")),
		mustResolvedClaudePath(t, filepath.Join(userRoot, "CLAUDE.md")),
		mustResolvedClaudePath(t, filepath.Join(addDir, "CLAUDE.md")),
	}
	if !slices.Equal(got, want) {
		t.Fatalf("ResolveClaudeMdSources() under bare add-dir = %#v, want %#v", got, want)
	}
}

func TestFilterInjectedMemoryFilesAppliesLayeredGates(t *testing.T) {
	sources := []ClaudeMdSource{
		{Path: "/user/CLAUDE.md", Type: sourceTypeUser, Origin: sourceOriginUser},
		{Path: "/repo/CLAUDE.md", Type: sourceTypeProject, Origin: sourceOriginProject},
		{Path: "/repo/CLAUDE.local.md", Type: sourceTypeLocal, Origin: sourceOriginProject},
		{Path: "/extra/CLAUDE.md", Type: sourceTypeProject, Origin: sourceOriginAddDir},
		{Path: "/etc/claude-code/CLAUDE.md", Type: sourceTypeManaged, Origin: sourceOriginManaged},
		{Path: "/memory/MEMORY.md", Type: sourceTypeAutoMem, Origin: sourceOriginAutoMem},
		{Path: "/team/MEMORY.md", Type: sourceTypeTeamMem, Origin: sourceOriginTeamMem},
	}
	filtered := FilterInjectedMemoryFiles(sources, contract.BuildCtx{}, MemoryGateSnapshot{
		InjectMemoryIndex:        false,
		InjectTeamMemIndex:       false,
		SkipProjectLocalClaudeMd: true,
	}, []string{"/user/*"})
	got := sourcePaths(filtered)
	want := []string{"/repo/CLAUDE.local.md", "/extra/CLAUDE.md", "/etc/claude-code/CLAUDE.md"}
	if !slices.Equal(got, want) {
		t.Fatalf("FilterInjectedMemoryFiles() paths = %#v, want %#v", got, want)
	}
}

func TestFilterInjectedMemoryFilesNestedWorktreeSkipsCheckedInAncestorsOnly(t *testing.T) {
	repoRoot, worktreeRoot, cwd := newNestedWorktreeClaudeFixture(t)
	sources := []ClaudeMdSource{
		{Path: filepath.Join(repoRoot, "CLAUDE.md"), Type: sourceTypeProject, Origin: sourceOriginProject, BaseDir: repoRoot},
		{Path: filepath.Join(repoRoot, "CLAUDE.local.md"), Type: sourceTypeLocal, Origin: sourceOriginProject, BaseDir: repoRoot},
		{Path: filepath.Join(worktreeRoot, "CLAUDE.md"), Type: sourceTypeProject, Origin: sourceOriginProject, BaseDir: worktreeRoot},
		{Path: filepath.Join(worktreeRoot, "CLAUDE.local.md"), Type: sourceTypeLocal, Origin: sourceOriginProject, BaseDir: worktreeRoot},
	}
	filtered := FilterInjectedMemoryFiles(sources, contract.BuildCtx{
		CWD:        cwd,
		GitRoot:    worktreeRoot,
		IsWorktree: true,
	}, MemoryGateSnapshot{SkipProjectLocalClaudeMd: true}, nil)
	got := sourcePaths(filtered)
	want := []string{
		filepath.Join(repoRoot, "CLAUDE.local.md"),
		filepath.Join(worktreeRoot, "CLAUDE.md"),
		filepath.Join(worktreeRoot, "CLAUDE.local.md"),
	}
	if !slices.Equal(got, want) {
		t.Fatalf("FilterInjectedMemoryFiles(nested worktree) paths = %#v, want %#v", got, want)
	}
}

func TestCombinedClaudeMdSourcesSkipTeamEntrypointWhenKairosActive(t *testing.T) {
	withTeamMemoryRuntimeReady(t, true)
	base := t.TempDir()
	repoRoot := filepath.Join(base, "repo")
	autoRoot := filepath.Join(base, "automem")
	teamRoot := filepath.Join(autoRoot, teamMemoryRootDirName)
	for _, dir := range []string{repoRoot, autoRoot, teamRoot} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("MkdirAll(%q) error = %v", dir, err)
		}
	}
	writeClaudeFile(t, memoryIndexPath(autoRoot), "- [Private](private.md) — private memory")
	writeClaudeFile(t, memoryIndexPath(teamRoot), "- [Team](team.md) — shared memory")

	cfg := &Config{
		Enabled:             true,
		RootDir:             base,
		ProjectRoot:         repoRoot,
		AutoMemPathOverride: autoRoot,
		Features:            MemoryFeatureFlags{TeamMemory: true, Kairos: true},
	}
	buildCtx := contract.BuildCtx{GitRoot: repoRoot, CWD: repoRoot}
	gate := ResolveMemoryGate(buildCtx, cfg)
	if !gate.KairosActive {
		t.Fatalf("ResolveMemoryGate().KairosActive = false, want true")
	}
	if gate.InjectTeamMemIndex {
		t.Fatalf("ResolveMemoryGate().InjectTeamMemIndex = true, want false during Kairos")
	}
	provider := NewClaudeMdSourcesProvider(cfg, NewTeamMemoryManager(cfg), nil)
	sources := provider.ResolveClaudeMdSources(context.Background(), buildCtx)
	if hasSourceType(sources, sourceTypeTeamMem) {
		t.Fatalf("ResolveClaudeMdSources() unexpectedly retained %q source under Kairos: %#v", sourceTypeTeamMem, sources)
	}
	if !hasSourceType(sources, sourceTypeAutoMem) {
		t.Fatalf("ResolveClaudeMdSources() missing %q source under Kairos: %#v", sourceTypeAutoMem, sources)
	}
}

func writeClaudeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll(%q) error = %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile(%q) error = %v", path, err)
	}
}

func sourcePaths(sources []ClaudeMdSource) []string {
	paths := make([]string, 0, len(sources))
	for _, source := range sources {
		paths = append(paths, source.Path)
	}
	return paths
}

func sourceByPath(t *testing.T, sources []ClaudeMdSource, path string) ClaudeMdSource {
	t.Helper()
	for _, source := range sources {
		if source.Path == path {
			return source
		}
	}
	t.Fatalf("source %q not found in %#v", path, sources)
	return ClaudeMdSource{}
}

func sourceByType(t *testing.T, sources []ClaudeMdSource, sourceType string) ClaudeMdSource {
	t.Helper()
	for _, source := range sources {
		if source.Type == sourceType {
			return source
		}
	}
	t.Fatalf("source type %q not found in %#v", sourceType, sources)
	return ClaudeMdSource{}
}

func hasSourceType(sources []ClaudeMdSource, sourceType string) bool {
	for _, source := range sources {
		if source.Type == sourceType {
			return true
		}
	}
	return false
}

func mustResolvedClaudePath(t *testing.T, path string) string {
	t.Helper()
	resolved, _, ok := resolveClaudeMdCandidatePath(path)
	if !ok {
		t.Fatalf("resolveClaudeMdCandidatePath(%q) = false", path)
	}
	return resolved
}

func newNestedWorktreeClaudeFixture(t *testing.T) (string, string, string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skipf("git not available: %v", err)
	}
	repoRoot := t.TempDir()
	runGit(t, repoRoot, "init")
	runGit(t, repoRoot, "config", "user.email", "test@example.com")
	runGit(t, repoRoot, "config", "user.name", "Test User")
	writeClaudeFile(t, filepath.Join(repoRoot, "README.md"), "root")
	runGit(t, repoRoot, "add", "README.md")
	runGit(t, repoRoot, "commit", "-m", "init")
	worktreeRoot := filepath.Join(repoRoot, ".claude", "worktrees", "feature")
	runGit(t, repoRoot, "worktree", "add", "-b", "feature", worktreeRoot)
	cwd := filepath.Join(worktreeRoot, "pkg")
	if err := os.MkdirAll(cwd, 0o755); err != nil {
		t.Fatalf("MkdirAll(%q) error = %v", cwd, err)
	}
	return repoRoot, worktreeRoot, cwd
}

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v in %q error = %v\n%s", args, dir, err, string(out))
	}
}
