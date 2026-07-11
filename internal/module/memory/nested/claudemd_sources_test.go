package nested

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
	memshared "github.com/lihah111222333-cloud/super-dolphin-agent/internal/module/memory/shared"
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

	deps := newTestDependencies(testDepsOptions{nestedEnabled: true, autoMemRoot: autoRoot, teamRoot: teamRoot})
	sources := mustResolveClaudeMdSources(t, ClaudeMdResolveConfig{
		BuildCtx: contract.BuildCtx{
			GitRoot:                      repoRoot,
			CWD:                          cwd,
			AdditionalWorkingDirectories: []string{addDir},
		},
		Dependencies: deps,
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
		// AutoMem/TeamMem 的 MEMORY.md 不再经由 nested ClaudeMd 注入；
		// prompt 阶段的记忆入口由 MemoryEntrypointProvider 单独负责。
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
	if got := mustResolveClaudeMdSources(t, ClaudeMdResolveConfig{
		BuildCtx:     contract.BuildCtx{GitRoot: repoRoot, CWD: repoRoot, SessionFlags: map[string]bool{"bare_mode": true}},
		Dependencies: newTestDependencies(testDepsOptions{}),
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
	sources := mustResolveClaudeMdSources(t, ClaudeMdResolveConfig{
		BuildCtx: contract.BuildCtx{
			GitRoot:                      repoRoot,
			CWD:                          repoRoot,
			SessionFlags:                 map[string]bool{"bare_mode": true},
			AdditionalWorkingDirectories: []string{addDir},
		},
		Dependencies: newTestDependencies(testDepsOptions{}),
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

func TestResolveClaudeMdSourcesReturnsRuleStatErrors(t *testing.T) {
	badRoot := filepath.Join(t.TempDir(), "bad\x00root")
	_, err := ruleMarkdownFiles(badRoot)
	if err == nil {
		t.Fatal("ruleMarkdownFiles() error = nil, want invalid rules path error")
	}
	if !strings.Contains(err.Error(), "nested rule markdown stat") {
		t.Fatalf("ruleMarkdownFiles() error = %v, want rule stat context", err)
	}
}

func TestResolveClaudeMdCandidatePathReturnsStatErrors(t *testing.T) {
	_, _, ok, err := resolveClaudeMdCandidatePath(filepath.Join(t.TempDir(), "bad\x00path"))
	if err == nil {
		t.Fatal("resolveClaudeMdCandidatePath() error = nil, want stat error")
	}
	if ok {
		t.Fatal("resolveClaudeMdCandidatePath() ok = true, want false on stat error")
	}
	if !strings.Contains(err.Error(), "ClaudeMd candidate stat") {
		t.Fatalf("resolveClaudeMdCandidatePath() error = %v, want stat context", err)
	}
}

func TestLoadStandardClaudeMdSourceReturnsSafeReadErrors(t *testing.T) {
	root := t.TempDir()
	outside := filepath.Join(t.TempDir(), "CLAUDE.md")
	writeClaudeFile(t, outside, "outside")
	link := filepath.Join(root, "CLAUDE.md")
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("symlink unsupported: %v", err)
	}

	_, ok, err := loadStandardClaudeMdSource(claudeMdCandidate{
		BaseDir: root,
		Path:    link,
		Type:    sourceTypeProject,
		Origin:  sourceOriginProject,
	})
	if err == nil {
		t.Fatal("loadStandardClaudeMdSource() error = nil, want SafeReadEntrypoint error")
	}
	if ok {
		t.Fatal("loadStandardClaudeMdSource() ok = true, want false on read error")
	}
	if !strings.Contains(err.Error(), "load ClaudeMd source") {
		t.Fatalf("loadStandardClaudeMdSource() error = %v, want source path context", err)
	}
	if !errors.Is(err, memshared.ErrSafeReadContainment) {
		t.Fatalf("loadStandardClaudeMdSource() error = %v, want ErrSafeReadContainment", err)
	}
}

func TestResolveClaudeMdSourcesReturnsContainmentErrors(t *testing.T) {
	root := t.TempDir()
	outside := filepath.Join(t.TempDir(), "CLAUDE.md")
	writeClaudeFile(t, outside, "outside")
	link := filepath.Join(root, "CLAUDE.md")
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("symlink unsupported: %v", err)
	}

	_, err := ResolveClaudeMdSources(context.Background(), ClaudeMdResolveConfig{
		BuildCtx:     contract.BuildCtx{GitRoot: root, CWD: root},
		Dependencies: newTestDependencies(testDepsOptions{}),
		ManagedRoots: nil,
		UserRoot:     "",
	})
	if err == nil {
		t.Fatal("ResolveClaudeMdSources() error = nil, want containment error")
	}
	if !errors.Is(err, memshared.ErrSafeReadContainment) {
		t.Fatalf("ResolveClaudeMdSources() error = %v, want ErrSafeReadContainment", err)
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
	filtered := FilterInjectedMemoryFiles(sources, contract.BuildCtx{}, GateSnapshot{
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
	}, GateSnapshot{SkipProjectLocalClaudeMd: true}, nil)
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

// TestCombinedClaudeMdSourcesNoLongerInjectsAutoOrTeamMemoryFiles 锁定 nested ClaudeMd
// 不再承载 AutoMem/TeamMem 的行为；这两类 MEMORY.md 由 MemoryEntrypointProvider
// 在 prompt 阶段注入，并继续受 gate 控制。
func TestCombinedClaudeMdSourcesNoLongerInjectsAutoOrTeamMemoryFiles(t *testing.T) {
	base := t.TempDir()
	repoRoot := filepath.Join(base, "repo")
	autoRoot := filepath.Join(base, "automem")
	teamRoot := filepath.Join(base, "teammem")
	for _, dir := range []string{repoRoot, autoRoot, teamRoot} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("MkdirAll(%q) error = %v", dir, err)
		}
	}
	writeClaudeFile(t, memoryIndexPath(autoRoot), "- [Private](private.md) — private memory")
	writeClaudeFile(t, memoryIndexPath(teamRoot), "- [Team](team.md) — shared memory")

	deps := newTestDependencies(testDepsOptions{
		nestedEnabled: true,
		autoMemRoot:   autoRoot,
		teamRoot:      teamRoot,
		gate: func(contract.BuildCtx) GateSnapshot {
			return GateSnapshot{InjectMemoryIndex: true, InjectTeamMemIndex: false}
		},
	})
	buildCtx := contract.BuildCtx{GitRoot: repoRoot, CWD: repoRoot}
	provider := NewClaudeMdSourcesProvider(deps, stubTeamMemoryManager{path: teamRoot}, nil)
	sources, err := provider.ResolveClaudeMdSources(context.Background(), buildCtx)
	if err != nil {
		t.Fatalf("ResolveClaudeMdSources() error = %v", err)
	}
	if hasSourceType(sources, sourceTypeAutoMem) {
		t.Fatalf("ResolveClaudeMdSources() unexpectedly contained %q source after Phase 1.6: %#v", sourceTypeAutoMem, sources)
	}
	if hasSourceType(sources, sourceTypeTeamMem) {
		t.Fatalf("ResolveClaudeMdSources() unexpectedly contained %q source after Phase 1.6: %#v", sourceTypeTeamMem, sources)
	}
}

type testDepsOptions struct {
	nestedEnabled bool
	autoMemRoot   string
	teamRoot      string
	gate          func(contract.BuildCtx) GateSnapshot
}

func newTestDependencies(opts testDepsOptions) Dependencies {
	autoRoot := cleanClaudeMdPath(opts.autoMemRoot)
	teamRoot := cleanClaudeMdPath(opts.teamRoot)
	gateFn := opts.gate
	if gateFn == nil {
		gateFn = func(buildCtx contract.BuildCtx) GateSnapshot {
			bare := buildCtx.SessionFlags != nil && buildCtx.SessionFlags["bare_mode"]
			hasAdditionalDirs := len(buildCtx.AdditionalWorkingDirectories) > 0
			autoEnabled := !bare || hasAdditionalDirs
			return GateSnapshot{
				BareMode:                 bare,
				HasAdditionalDirsForBare: hasAdditionalDirs,
				DisableClaudeMds:         bare && !hasAdditionalDirs,
				InjectMemoryIndex:        autoEnabled,
				InjectTeamMemIndex:       autoEnabled && teamRoot != "",
			}
		}
	}
	return Dependencies{
		NestedEnabled: opts.nestedEnabled,
		Gate:          gateFn,
		AutoMemRoot: func(contract.BuildCtx) string {
			return autoRoot
		},
		TeamRoot: func(contract.BuildCtx) string {
			return teamRoot
		},
	}
}

type stubTeamMemoryManager struct {
	path       string
	entrypoint string
}

func (s stubTeamMemoryManager) GetTeamMemPath(buildCtx ...contract.BuildCtx) string {
	return cleanClaudeMdPath(s.path)
}

func (s stubTeamMemoryManager) GetTeamMemEntrypoint(buildCtx ...contract.BuildCtx) string {
	if strings.TrimSpace(s.entrypoint) != "" {
		return cleanClaudeMdPath(s.entrypoint)
	}
	if strings.TrimSpace(s.path) == "" {
		return ""
	}
	return memoryIndexPath(cleanClaudeMdPath(s.path))
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
	resolved, _, ok, err := resolveClaudeMdCandidatePath(path)
	if err != nil {
		t.Fatalf("resolveClaudeMdCandidatePath(%q) error = %v", path, err)
	}
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

// TestResolveClaudeMdSourcesSuppressedByOverlay 锁定 overlay 场景的防重复注入短路。
// 当底层 CLI harness 已原生加载 CLAUDE.md 时，SuppressForOverlay 必须丢弃所有
// claudeMd source，避免后续重新启用 UserContextText 后与 harness 自带副本重复注入。
func TestResolveClaudeMdSourcesSuppressedByOverlay(t *testing.T) {
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

	cfg := ClaudeMdResolveConfig{
		BuildCtx:     contract.BuildCtx{GitRoot: repoRoot, CWD: repoRoot},
		ManagedRoots: []string{managedRoot},
		UserRoot:     userRoot,
	}

	// 对照组：未开启 overlay 时 source 应正常加载。
	cfg.Dependencies = newTestDependencies(testDepsOptions{})
	if got := mustResolveClaudeMdSources(t, cfg); len(got) == 0 {
		t.Fatalf("counter-baseline: ResolveClaudeMdSources() = empty, want non-empty (overlay off)")
	}

	// 开启 overlay 后所有 source 都必须被丢弃。
	cfg.Dependencies = newTestDependencies(testDepsOptions{
		gate: func(contract.BuildCtx) GateSnapshot {
			return GateSnapshot{SuppressForOverlay: true}
		},
	})
	if got := mustResolveClaudeMdSources(t, cfg); len(got) != 0 {
		t.Fatalf("ResolveClaudeMdSources() under SuppressForOverlay = %#v, want nil/empty", got)
	}
}

func mustResolveClaudeMdSources(t *testing.T, cfg ClaudeMdResolveConfig) []ClaudeMdSource {
	t.Helper()
	sources, err := ResolveClaudeMdSources(context.Background(), cfg)
	if err != nil {
		t.Fatalf("ResolveClaudeMdSources() error = %v", err)
	}
	return sources
}
