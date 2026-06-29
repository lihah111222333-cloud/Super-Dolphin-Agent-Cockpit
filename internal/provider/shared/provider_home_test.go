package shared

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
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

func TestEnsureAppManagedProviderHomeUsesSuperDolphinHome(t *testing.T) {
	superHome := filepath.Join(t.TempDir(), "sd-home")
	t.Setenv(SuperDolphinHomeEnv, superHome)

	got, err := EnsureAppManagedProviderHome(ProviderClaude)
	if err != nil {
		t.Fatalf("EnsureAppManagedProviderHome() error = %v", err)
	}
	want, err := filepath.EvalSymlinks(filepath.Join(superHome, "providers", "claude"))
	if err != nil {
		t.Fatalf("EvalSymlinks app-managed home: %v", err)
	}
	if got != want {
		t.Fatalf("EnsureAppManagedProviderHome() = %q, want %q", got, want)
	}
	assertDirMode(t, filepath.Join(want, "skills"), 0o700)
}

func TestProviderMirrorTargetsIncludePersonalAndProjectRoots(t *testing.T) {
	superHome := filepath.Join(t.TempDir(), "sd-home")
	userHome := filepath.Join(t.TempDir(), "user-home")
	project := t.TempDir()
	t.Setenv(SuperDolphinHomeEnv, superHome)
	t.Setenv("HOME", userHome)
	t.Setenv("USERPROFILE", userHome)

	targets, err := ProviderMirrorTargets(ProviderClaude, project)
	if err != nil {
		t.Fatalf("ProviderMirrorTargets() error = %v", err)
	}
	if len(targets) != 2 {
		t.Fatalf("targets = %d, want 2", len(targets))
	}
	if targets[0].HomeRoot != filepath.Join(userHome, ".claude") {
		t.Fatalf("personal home = %q", targets[0].HomeRoot)
	}
	if targets[0].SkillsRoot != filepath.Join(userHome, ".claude", "skills") {
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
		skipProviderHomeSymlinkPrivilegeNotHeld(t, err)
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
		skipProviderHomeSymlinkPrivilegeNotHeld(t, err)
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

func TestProviderMirrorTargetsIgnoresInvalidParentGitMarker(t *testing.T) {
	superHome := filepath.Join(t.TempDir(), "sd-home")
	userHome := filepath.Join(t.TempDir(), "user-home")
	t.Setenv(SuperDolphinHomeEnv, superHome)
	t.Setenv("HOME", userHome)
	t.Setenv("USERPROFILE", userHome)
	parent := t.TempDir()
	if err := os.MkdirAll(filepath.Join(parent, ".git"), 0o755); err != nil {
		t.Fatalf("MkdirAll invalid parent .git: %v", err)
	}
	project := filepath.Join(parent, "project")
	if err := os.MkdirAll(project, 0o755); err != nil {
		t.Fatalf("MkdirAll project: %v", err)
	}

	targets, err := ProviderMirrorTargets(ProviderCodex, project)
	if err != nil {
		t.Fatalf("ProviderMirrorTargets() error = %v", err)
	}
	wantProject, err := filepath.EvalSymlinks(project)
	if err != nil {
		t.Fatalf("EvalSymlinks project: %v", err)
	}
	if targets[1].SkillsRoot != filepath.Join(wantProject, ".agents", "skills") {
		t.Fatalf("project skills = %q, want cwd-scoped mirror under %q", targets[1].SkillsRoot, wantProject)
	}
}

func TestProviderMirrorTargetsUsesGitRootForSubdirCWD(t *testing.T) {
	superHome := filepath.Join(t.TempDir(), "sd-home")
	userHome := filepath.Join(t.TempDir(), "user-home")
	t.Setenv(SuperDolphinHomeEnv, superHome)
	t.Setenv("HOME", userHome)
	t.Setenv("USERPROFILE", userHome)
	project := t.TempDir()
	subdir := filepath.Join(project, "cmd", "agent-terminal")
	if err := os.MkdirAll(filepath.Join(project, ".git"), 0o755); err != nil {
		t.Fatalf("MkdirAll .git: %v", err)
	}
	if err := os.WriteFile(filepath.Join(project, ".git", "HEAD"), []byte("ref: refs/heads/main\n"), 0o644); err != nil {
		t.Fatalf("WriteFile .git/HEAD: %v", err)
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
	if targets[1].SkillsRoot != filepath.Join(wantProject, ".agents", "skills") {
		t.Fatalf("project skills = %q, want repo root mirror", targets[1].SkillsRoot)
	}
}

func TestProviderMirrorTargetsRedirectsPackagedProjectMirrorToWritableHome(t *testing.T) {
	superHome := filepath.Join(t.TempDir(), "sd-home")
	resources := filepath.Join(t.TempDir(), "Super Dolphin.app", "Contents", "Resources")
	if err := os.MkdirAll(resources, 0o755); err != nil {
		t.Fatalf("MkdirAll resources: %v", err)
	}
	t.Setenv(SuperDolphinHomeEnv, superHome)
	t.Setenv(ProjectRootEnv, resources)
	t.Setenv(PackagedCodexEnv, "1")
	t.Setenv("SUPER_DOLPHIN_RUNTIME_MODE", "packaged")

	targets, err := ProviderMirrorTargets(ProviderCodex, resources)
	if err != nil {
		t.Fatalf("ProviderMirrorTargets() error = %v", err)
	}
	want := filepath.Join(superHome, "provider-mirrors", "project", "codex", "skills")
	if targets[1].SkillsRoot != want {
		t.Fatalf("packaged project skills = %q, want writable mirror %q", targets[1].SkillsRoot, want)
	}
}

func TestProviderMirrorTargetsRedirectsPackagedAppBundleCWDToWritableHome(t *testing.T) {
	userHome := filepath.Join(t.TempDir(), "clean-user")
	superHome := filepath.Join(userHome, "Library", "Application Support", "Super Dolphin")
	appRoot := filepath.Join(t.TempDir(), "Super Dolphin.app")
	resources := filepath.Join(appRoot, "Contents", "Resources")
	if err := os.MkdirAll(resources, 0o755); err != nil {
		t.Fatalf("MkdirAll resources: %v", err)
	}
	t.Setenv(SuperDolphinHomeEnv, superHome)
	t.Setenv(ProjectRootEnv, resources)
	t.Setenv(PackagedCodexEnv, "1")
	t.Setenv("SUPER_DOLPHIN_RUNTIME_MODE", "packaged")
	t.Setenv("HOME", "")
	t.Setenv("USERPROFILE", "")

	appManagedHome := filepath.Join(superHome, "providers", "codex")
	targets, err := ProviderMirrorTargets(ProviderCodex, appRoot, appManagedHome)
	if err != nil {
		t.Fatalf("ProviderMirrorTargets() error = %v", err)
	}
	want := filepath.Join(superHome, "provider-mirrors", "project", "codex", "skills")
	if targets[1].SkillsRoot != want {
		t.Fatalf("packaged app-bundle cwd project skills = %q, want writable mirror %q", targets[1].SkillsRoot, want)
	}
	if strings.Contains(targets[1].SkillsRoot, ".app"+string(filepath.Separator)+".agents") {
		t.Fatalf("packaged app-bundle cwd must not publish project skills inside app bundle: %#v", targets[1])
	}
	assertAllMirrorTargetsUnderRoot(t, targets, superHome)
}

func TestProviderMirrorTargetsRedirectsPackagedRootLibraryCWDToWritableHome(t *testing.T) {
	if _, err := os.Stat("/Library"); err != nil {
		t.Skipf("/Library unavailable on this host: %v", err)
	}
	userHome := filepath.Join(t.TempDir(), "clean-user")
	superHome := filepath.Join(userHome, "Library", "Application Support", "Super Dolphin")
	resources := filepath.Join(t.TempDir(), "Super Dolphin.app", "Contents", "Resources")
	if err := os.MkdirAll(resources, 0o755); err != nil {
		t.Fatalf("MkdirAll resources: %v", err)
	}
	t.Setenv(SuperDolphinHomeEnv, superHome)
	t.Setenv(ProjectRootEnv, resources)
	t.Setenv(PackagedCodexEnv, "1")
	t.Setenv("SUPER_DOLPHIN_RUNTIME_MODE", "packaged")
	t.Setenv("HOME", "/Library")
	t.Setenv("USERPROFILE", "/Library")

	appManagedHome := filepath.Join(superHome, "providers", "codex")
	targets, err := ProviderMirrorTargets(ProviderCodex, "/Library", appManagedHome)
	if err != nil {
		t.Fatalf("ProviderMirrorTargets() error = %v", err)
	}
	want := filepath.Join(superHome, "provider-mirrors", "project", "codex", "skills")
	if targets[1].SkillsRoot != want {
		t.Fatalf("packaged /Library cwd project skills = %q, want writable mirror %q", targets[1].SkillsRoot, want)
	}
	if strings.HasPrefix(filepath.Clean(targets[1].SkillsRoot), filepath.Clean("/Library/.agents")) {
		t.Fatalf("packaged /Library cwd must not publish project skills under /Library/.agents: %#v", targets[1])
	}
	assertAllMirrorTargetsUnderRoot(t, targets, superHome)
}

func TestProviderHomeBlocksDrift(t *testing.T) {
	assertProviderHomeBlocksActiveConflict(t, "mirror_drift")
}

func TestProviderHomeBlocksUnmanaged(t *testing.T) {
	assertProviderHomeBlocksActiveConflict(t, "unmanaged_provider_skill")
}

func TestProviderHomeBlocksCanonicalDeletedWithDrift(t *testing.T) {
	assertProviderHomeBlocksActiveConflict(t, "canonical_deleted_with_drift")
}

func assertProviderHomeBlocksActiveConflict(t *testing.T, kind string) {
	t.Helper()
	for _, tc := range []struct {
		name     string
		targetID string
		scope    string
	}{
		{name: "project", targetID: "codex:project:repo", scope: "project"},
		{name: "app-managed", targetID: "codex:app-managed:owner", scope: "personal"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			report := contract.SkillMirrorReport{Conflicts: []contract.SkillMirrorReportItem{{
				TargetID:     tc.targetID,
				Scope:        tc.scope,
				ConflictKind: kind,
			}}}

			err := EnsureNoSkillMirrorConflicts(report)
			if err == nil {
				t.Fatalf("EnsureNoSkillMirrorConflicts() error = nil, want active mirror conflict to block provider start")
			}
			if !strings.Contains(err.Error(), kind) || !strings.Contains(err.Error(), tc.scope) {
				t.Fatalf("EnsureNoSkillMirrorConflicts() error = %v, want kind %q and scope %q", err, kind, tc.scope)
			}
		})
	}
}

func TestEnsureNoSkillMirrorConflictsAllowsReportOnlySkillContentConflicts(t *testing.T) {
	kinds := []string{
		"same_name",
		"same_name_scope_conflict",
		"drift",
		"mirror_drift",
		"multi_mirror_drift",
		"canonical_deleted_with_drift",
		"unmanaged",
		"unmanaged_same_name",
		"unmanaged_provider_skill",
	}
	for _, kind := range kinds {
		t.Run(kind, func(t *testing.T) {
			report := contract.SkillMirrorReport{Conflicts: []contract.SkillMirrorReportItem{{
				TargetID:     "codex:user-global:owner",
				Scope:        "personal",
				ConflictKind: kind,
			}}}

			if err := EnsureNoSkillMirrorConflicts(report); err != nil {
				t.Fatalf("EnsureNoSkillMirrorConflicts() error = %v, want UI-only content conflict to remain report-only", err)
			}
		})
	}
}

func TestEnsureNoSkillMirrorConflictsReportsMirrorSafetyConflicts(t *testing.T) {
	kinds := []string{
		"publish_error",
		"publish_targets_unconfigured",
		"mirror_root_symlink",
		"unknown_future_conflict",
		"",
	}
	for _, kind := range kinds {
		t.Run(kind, func(t *testing.T) {
			report := contract.SkillMirrorReport{Conflicts: []contract.SkillMirrorReportItem{{
				TargetID:     "codex:project:repo",
				ConflictKind: kind,
			}}}

			err := EnsureNoSkillMirrorConflicts(report)
			if err == nil || !strings.Contains(err.Error(), "skill mirror conflicts") {
				t.Fatalf("EnsureNoSkillMirrorConflicts() error = %v, want blocking mirror safety conflict", err)
			}
		})
	}
}

func TestProviderMirrorTargetsUsesCodexOfficialGlobalSkillsRoot(t *testing.T) {
	userHome := filepath.Join(t.TempDir(), "user-home")
	project := t.TempDir()
	t.Setenv("HOME", userHome)
	t.Setenv("USERPROFILE", userHome)

	targets, err := ProviderMirrorTargets(ProviderCodex, project)
	if err != nil {
		t.Fatalf("ProviderMirrorTargets() error = %v", err)
	}
	if len(targets) != 2 {
		t.Fatalf("targets = %d, want 2", len(targets))
	}
	if targets[0].HomeRoot != filepath.Join(userHome, ".agents") || targets[0].SkillsRoot != filepath.Join(userHome, ".agents", "skills") {
		t.Fatalf("codex personal target = %#v, want ~/.agents/skills", targets[0])
	}
	wantProject, err := filepath.EvalSymlinks(project)
	if err != nil {
		t.Fatalf("EvalSymlinks project: %v", err)
	}
	if targets[1].SkillsRoot != filepath.Join(wantProject, ".agents", "skills") {
		t.Fatalf("codex project target = %#v, want .agents/skills", targets[1])
	}
}

func TestEnsureProviderHomeDefaultsToUserCLIHome(t *testing.T) {
	userHome := filepath.Join(t.TempDir(), "user-home")
	t.Setenv("HOME", userHome)
	t.Setenv("USERPROFILE", userHome)

	claudeHome, err := EnsureProviderHome(ProviderClaude, "")
	if err != nil {
		t.Fatalf("EnsureProviderHome(claude) error = %v", err)
	}
	wantClaudeHome, err := filepath.EvalSymlinks(filepath.Join(userHome, ".claude"))
	if err != nil {
		t.Fatalf("EvalSymlinks claude home: %v", err)
	}
	if claudeHome != wantClaudeHome {
		t.Fatalf("claude home = %q, want user CLI home", claudeHome)
	}
	codexHome, err := EnsureProviderHome(ProviderCodex, "")
	if err != nil {
		t.Fatalf("EnsureProviderHome(codex) error = %v", err)
	}
	wantCodexHome, err := filepath.EvalSymlinks(filepath.Join(userHome, ".codex"))
	if err != nil {
		t.Fatalf("EvalSymlinks codex home: %v", err)
	}
	if codexHome != wantCodexHome {
		t.Fatalf("codex home = %q, want user CLI home", codexHome)
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
	if runtime.GOOS == "windows" {
		return
	}
	if got := info.Mode().Perm(); got != want {
		t.Fatalf("mode %s = %o, want %o", path, got, want)
	}
}

func assertAllMirrorTargetsUnderRoot(t *testing.T, targets []contract.SkillProviderMirrorTarget, root string) {
	t.Helper()
	cleanRoot := filepath.Clean(root)
	for _, target := range targets {
		for _, path := range []string{target.HomeRoot, target.SkillsRoot} {
			cleanPath := filepath.Clean(path)
			if cleanPath == cleanRoot || strings.HasPrefix(cleanPath, cleanRoot+string(filepath.Separator)) {
				continue
			}
			t.Fatalf("mirror target path %q is outside app-managed root %q: %#v", cleanPath, cleanRoot, target)
		}
	}
}

func skipProviderHomeSymlinkPrivilegeNotHeld(t *testing.T, err error) {
	t.Helper()
	if runtime.GOOS == "windows" && strings.Contains(err.Error(), "privilege") {
		t.Skipf("symlink privilege unavailable: %v", err)
	}
}

func TestProviderHomeDevEmptyIgnoresPackagedLeftovers(t *testing.T) {
	userHome := filepath.Join(t.TempDir(), "user-home")
	superHome := filepath.Join(t.TempDir(), "sd-home")
	project := t.TempDir()
	t.Setenv("HOME", userHome)
	t.Setenv("USERPROFILE", userHome)
	t.Setenv(SuperDolphinHomeEnv, superHome)
	t.Setenv(PackagedCodexEnv, "1")
	t.Setenv(ProjectRootEnv, project)
	t.Setenv("SUPER_DOLPHIN_RUNTIME_MODE", "dev")

	home, err := EnsureProviderHome(ProviderCodex, "")
	if err != nil {
		t.Fatalf("EnsureProviderHome(codex) error = %v", err)
	}
	wantHome, err := filepath.EvalSymlinks(filepath.Join(userHome, ".codex"))
	if err != nil {
		t.Fatalf("EvalSymlinks local codex home: %v", err)
	}
	if home != wantHome {
		t.Fatalf("codex home = %q, want local CLI home %q", home, wantHome)
	}
	targets, err := ProviderMirrorTargets(ProviderCodex, project)
	if err != nil {
		t.Fatalf("ProviderMirrorTargets() error = %v", err)
	}
	if targets[0].HomeRoot != filepath.Join(userHome, ".agents") {
		t.Fatalf("codex personal mirror = %#v, want user provider mirror, not app-managed", targets[0])
	}
}

func TestProviderMirrorTargetsRejectsInvalidRuntimeMode(t *testing.T) {
	userHome := filepath.Join(t.TempDir(), "user-home")
	project := t.TempDir()
	t.Setenv("HOME", userHome)
	t.Setenv("USERPROFILE", userHome)
	t.Setenv(RuntimeModeEnv, "bogus")

	_, err := ProviderMirrorTargets(ProviderCodex, project)
	if err == nil || !strings.Contains(err.Error(), RuntimeModeEnv) {
		t.Fatalf("ProviderMirrorTargets() error = %v, want invalid runtime mode", err)
	}
}

func TestAppManagedProviderHomeRequiresSuperDolphinHome(t *testing.T) {
	t.Setenv(SuperDolphinHomeEnv, "")

	got, err := AppManagedProviderHome(ProviderCodex)
	if err == nil || !strings.Contains(err.Error(), SuperDolphinHomeEnv) {
		t.Fatalf("AppManagedProviderHome() = %q, %v; want missing runtime home error", got, err)
	}
}
