package shared

import (
	"os"
	"path/filepath"
	"testing"
)

func TestAppManagedProviderHomeUsesSuperDolphinHome(t *testing.T) {
	superHome := filepath.Join(t.TempDir(), "sd-home")
	t.Setenv(SuperDolphinHomeEnv, superHome)

	got, err := AppManagedProviderHome(ProviderCodex)
	if err != nil {
		t.Fatalf("AppManagedProviderHome() error = %v", err)
	}
	want := filepath.Join(superHome, "providers", "codex")
	if got != want {
		t.Fatalf("AppManagedProviderHome() = %q, want %q", got, want)
	}
}

func TestProviderMirrorTargetsIncludePersonalAndProjectRoots(t *testing.T) {
	superHome := filepath.Join(t.TempDir(), "sd-home")
	project := t.TempDir()
	t.Setenv(SuperDolphinHomeEnv, superHome)

	targets, err := ProviderMirrorTargets(ProviderClaude, project)
	if err != nil {
		t.Fatalf("ProviderMirrorTargets() error = %v", err)
	}
	if len(targets) != 2 {
		t.Fatalf("targets = %d, want 2", len(targets))
	}
	if targets[0].HomeRoot != filepath.Join(superHome, "providers", "claude") {
		t.Fatalf("personal home = %q", targets[0].HomeRoot)
	}
	if targets[0].SkillsRoot != filepath.Join(superHome, "providers", "claude", "skills") {
		t.Fatalf("personal skills = %q", targets[0].SkillsRoot)
	}
	wantProject, err := filepath.EvalSymlinks(project)
	if err != nil {
		t.Fatalf("EvalSymlinks project: %v", err)
	}
	if targets[1].SkillsRoot != filepath.Join(wantProject, ".claude", "skills") {
		t.Fatalf("project skills = %q", targets[1].SkillsRoot)
	}
}

func TestProviderMirrorTargetsUsesExplicitHome(t *testing.T) {
	explicitHome := filepath.Join(t.TempDir(), "custom-codex")
	project := t.TempDir()
	t.Setenv("CUSTOM_CODEX_HOME", explicitHome)

	targets, err := ProviderMirrorTargets(ProviderCodex, project, "$CUSTOM_CODEX_HOME")
	if err != nil {
		t.Fatalf("ProviderMirrorTargets() error = %v", err)
	}
	if targets[0].HomeRoot != explicitHome || targets[0].SkillsRoot != filepath.Join(explicitHome, "skills") || !targets[0].AllowExplicitHome {
		t.Fatalf("explicit personal target = %#v", targets[0])
	}
}

func TestProviderMirrorTargetsRequiresRealCWDAndAbsoluteHome(t *testing.T) {
	explicitHome := filepath.Join(t.TempDir(), "custom-codex")
	project := t.TempDir()

	requireProviderMirrorTargetsError(t, "", explicitHome)
	requireProviderMirrorTargetsError(t, ".", explicitHome)
	requireProviderMirrorTargetsError(t, "./", explicitHome)
	requireProviderMirrorTargetsError(t, "relative/project", explicitHome)
	requireProviderMirrorTargetsError(t, project, "relative-codex-home")
	requireEnsureProviderHomeError(t, "relative-codex-home")
}

func requireProviderMirrorTargetsError(t *testing.T, cwd, home string) {
	t.Helper()
	if _, err := ProviderMirrorTargets(ProviderCodex, cwd, home); err == nil {
		t.Fatalf("ProviderMirrorTargets(%q, %q) error = nil, want rejection", cwd, home)
	}
}

func requireEnsureProviderHomeError(t *testing.T, home string) {
	t.Helper()
	if _, err := EnsureProviderHome(ProviderCodex, home); err == nil {
		t.Fatalf("EnsureProviderHome(%q) error = nil, want rejection", home)
	}
}

func TestProviderMirrorTargetsUsesCanonicalExplicitHome(t *testing.T) {
	realHome := filepath.Join(t.TempDir(), "real-codex")
	if err := os.MkdirAll(realHome, 0o700); err != nil {
		t.Fatalf("MkdirAll real home: %v", err)
	}
	aliasHome := filepath.Join(t.TempDir(), "alias-codex")
	if err := os.Symlink(realHome, aliasHome); err != nil {
		t.Fatalf("Symlink explicit home: %v", err)
	}
	home, err := EnsureProviderHome(ProviderCodex, aliasHome)
	if err != nil {
		t.Fatalf("EnsureProviderHome() error = %v", err)
	}
	targets, err := ProviderMirrorTargets(ProviderCodex, t.TempDir(), home)
	if err != nil {
		t.Fatalf("ProviderMirrorTargets() error = %v", err)
	}
	wantHome, err := filepath.EvalSymlinks(realHome)
	if err != nil {
		t.Fatalf("EvalSymlinks real home: %v", err)
	}
	if targets[0].HomeRoot != wantHome || targets[0].SkillsRoot != filepath.Join(wantHome, "skills") {
		t.Fatalf("explicit target = %#v, want real home %q", targets[0], wantHome)
	}
}

func TestProviderMirrorTargetsUsesCanonicalProjectRoot(t *testing.T) {
	superHome := filepath.Join(t.TempDir(), "sd-home")
	t.Setenv(SuperDolphinHomeEnv, superHome)
	realProject := filepath.Join(t.TempDir(), "real-project")
	if err := os.MkdirAll(realProject, 0o755); err != nil {
		t.Fatalf("MkdirAll real project: %v", err)
	}
	aliasProject := filepath.Join(t.TempDir(), "alias-project")
	if err := os.Symlink(realProject, aliasProject); err != nil {
		t.Fatalf("Symlink project: %v", err)
	}

	targets, err := ProviderMirrorTargets(ProviderClaude, aliasProject)
	if err != nil {
		t.Fatalf("ProviderMirrorTargets() error = %v", err)
	}
	wantProject, err := filepath.EvalSymlinks(realProject)
	if err != nil {
		t.Fatalf("EvalSymlinks real project: %v", err)
	}
	if targets[1].SkillsRoot != filepath.Join(wantProject, ".claude", "skills") {
		t.Fatalf("project skills = %q, want canonical project root %q", targets[1].SkillsRoot, wantProject)
	}
}

func TestProviderMirrorTargetsUsesGitRootForSubdirCWD(t *testing.T) {
	superHome := filepath.Join(t.TempDir(), "sd-home")
	t.Setenv(SuperDolphinHomeEnv, superHome)
	project := t.TempDir()
	subdir := filepath.Join(project, "cmd", "agent-terminal")
	if err := os.MkdirAll(filepath.Join(project, ".git"), 0o755); err != nil {
		t.Fatalf("MkdirAll .git: %v", err)
	}
	if err := os.MkdirAll(subdir, 0o755); err != nil {
		t.Fatalf("MkdirAll subdir: %v", err)
	}

	targets, err := ProviderMirrorTargets(ProviderCodex, subdir)
	if err != nil {
		t.Fatalf("ProviderMirrorTargets() error = %v", err)
	}
	wantProject, err := filepath.EvalSymlinks(project)
	if err != nil {
		t.Fatalf("EvalSymlinks project: %v", err)
	}
	if targets[1].SkillsRoot != filepath.Join(wantProject, ".codex", "skills") {
		t.Fatalf("project skills = %q, want repo root mirror", targets[1].SkillsRoot)
	}
}

func TestEnsureProviderHomeCreatesPrivateHomeAndSkills(t *testing.T) {
	home := filepath.Join(t.TempDir(), "provider-home")

	got, err := EnsureProviderHome(ProviderClaude, home)
	if err != nil {
		t.Fatalf("EnsureProviderHome() error = %v", err)
	}
	want, err := filepath.EvalSymlinks(home)
	if err != nil {
		t.Fatalf("EvalSymlinks home: %v", err)
	}
	if got != want {
		t.Fatalf("EnsureProviderHome() = %q, want %q", got, want)
	}
	assertDirMode(t, home, 0o700)
	assertDirMode(t, filepath.Join(home, "skills"), 0o700)
}

func assertDirMode(t *testing.T, path string, want os.FileMode) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat %s: %v", path, err)
	}
	if got := info.Mode().Perm(); got != want {
		t.Fatalf("mode %s = %o, want %o", path, got, want)
	}
}
