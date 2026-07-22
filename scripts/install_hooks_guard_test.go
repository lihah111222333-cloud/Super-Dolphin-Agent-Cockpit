package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestInstallHooksMakeTargetUsesWorktreeSafeRelativePath(t *testing.T) {
	root := prepareInstallHooksRepo(t)
	commitInstallHooksFixture(t, root)

	runInstallHooksGit(t, root, "config", "core.hooksPath", ".githooks")

	launcher := writeInstallHooksLauncher(t, root, 0o700)
	output, err := runInstallHooksMake(t, root, "SUPER_DOLPHIN_GATE_LAUNCHER="+launcher)
	if err != nil {
		t.Fatalf("make install-hooks failed: %v\n%s", err, output)
	}

	if got := installHooksGitOutput(t, root, "config", "--get", "core.hooksPath"); got != ".githooks" {
		t.Fatalf("core.hooksPath = %q, want .githooks\nmake output:\n%s", got, output)
	}
	if got := installHooksGitOutput(t, root, "config", "--local", "--get", "superdolphin.gateLauncher"); got != launcher {
		t.Fatalf("superdolphin.gateLauncher = %q, want %q", got, launcher)
	}

	linkedRoot := filepath.Join(t.TempDir(), "linked")
	runInstallHooksGit(t, root, "worktree", "add", "-q", "--detach", linkedRoot, "HEAD")
	if got := installHooksGitOutput(t, linkedRoot, "config", "--get", "core.hooksPath"); got != ".githooks" {
		t.Fatalf("linked worktree core.hooksPath = %q, want .githooks", got)
	}
	if _, err := os.Stat(filepath.Join(linkedRoot, ".githooks", "pre-commit")); err != nil {
		t.Fatalf("linked worktree .githooks/pre-commit is missing: %v", err)
	}
}

func TestInstallHooksMakeTargetRefusesConflictingHooksPath(t *testing.T) {
	root := prepareInstallHooksRepo(t)
	conflictingHooksPath := filepath.Join(t.TempDir(), ".githooks")
	runInstallHooksGit(t, root, "config", "core.hooksPath", conflictingHooksPath)

	launcher := writeInstallHooksLauncher(t, root, 0o700)
	output, err := runInstallHooksMake(t, root, "SUPER_DOLPHIN_GATE_LAUNCHER="+launcher)
	if err == nil {
		t.Fatalf("make install-hooks unexpectedly succeeded with conflicting core.hooksPath\n%s", output)
	}
	if !strings.Contains(string(output), "existing core.hooksPath = "+conflictingHooksPath+"; refusing to replace it automatically") {
		t.Fatalf("make install-hooks did not report the conflicting core.hooksPath\n%s", output)
	}
	if got := installHooksGitOutput(t, root, "config", "--get", "core.hooksPath"); got != conflictingHooksPath {
		t.Fatalf("core.hooksPath = %q, want conflicting path %q", got, conflictingHooksPath)
	}
}

func TestInstallHooksMakeTargetRejectsUnprovisionedLauncher(t *testing.T) {
	root := prepareInstallHooksRepo(t)
	writeInstallHooksPreCommit(t, root)

	output, err := runInstallHooksMake(t, root)
	if err == nil || !strings.Contains(string(output), "launcher is not provisioned") {
		t.Fatalf("unprovisioned launcher result error=%v output=%s", err, output)
	}
	check := exec.Command("git", "config", "--local", "--get", "core.hooksPath")
	check.Dir = root
	if output, err := check.Output(); err == nil || len(output) != 0 {
		t.Fatalf("hooks were installed without a provisioned launcher: %q", output)
	}
}

func TestInstallHooksMakeTargetRejectsGroupWritableLauncher(t *testing.T) {
	root := prepareInstallHooksRepo(t)
	launcher := writeInstallHooksLauncher(t, root, 0o700)
	if err := os.Chmod(launcher, 0o770); err != nil {
		t.Fatalf("make launcher group-writable: %v", err)
	}

	output, err := runInstallHooksMake(t, root, "SUPER_DOLPHIN_GATE_LAUNCHER="+launcher)
	if err == nil || !strings.Contains(string(output), "permit group or world writes") {
		t.Fatalf("group-writable launcher result error=%v output=%s", err, output)
	}
}

func prepareInstallHooksRepo(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("make"); err != nil {
		t.Skip("make is required for install-hooks regression")
	}

	root := t.TempDir()
	copyFixTestGuardRepoFile(t, root, "scripts/ai_maintenance/deferred_e2e_packages.txt", 0o644)
	copyFixTestGuardRepoFile(t, root, "scripts/install-hooks.sh", 0o755)
	copyFixTestGuardRepoFile(t, root, ".githooks/trusted-gate-launcher.sh", 0o755)
	runInstallHooksGit(t, root, "init", "-q")
	return root
}

func commitInstallHooksFixture(t *testing.T, root string) {
	t.Helper()
	runInstallHooksGit(t, root, "config", "user.email", "hooks@example.test")
	runInstallHooksGit(t, root, "config", "user.name", "Hooks Test")
	writeInstallHooksPreCommit(t, root)
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("hooks fixture\n"), 0o644); err != nil {
		t.Fatalf("write README: %v", err)
	}
	runInstallHooksGit(t, root, "add", ".githooks/pre-commit", "README.md")
	runInstallHooksGit(t, root, "commit", "-m", "chore: 初始化 hooks fixture")
}

func writeInstallHooksPreCommit(t *testing.T, root string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(root, ".githooks"), 0o755); err != nil {
		t.Fatalf("create .githooks: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, ".githooks", "pre-commit"), []byte("#!/usr/bin/env bash\nexit 0\n"), 0o755); err != nil {
		t.Fatalf("write pre-commit: %v", err)
	}
}

func writeInstallHooksLauncher(t *testing.T, root string, mode os.FileMode) string {
	t.Helper()
	launcher := filepath.Join(root, "super-dolphin-gate")
	if err := os.WriteFile(launcher, []byte("#!/usr/bin/env bash\nexit 0\n"), mode); err != nil {
		t.Fatalf("write launcher: %v", err)
	}
	return launcher
}

func runInstallHooksMake(t *testing.T, root string, env ...string) ([]byte, error) {
	t.Helper()
	cmd := exec.Command("make", "-f", filepath.Join(scriptRepoRoot(t), "Makefile"), "install-hooks")
	cmd.Dir = root
	cmd.Env = append(os.Environ(), env...)
	return cmd.CombinedOutput()
}

func runInstallHooksGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	_ = installHooksGitOutput(t, dir, args...)
}

func installHooksGitOutput(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s failed in %s: %v\n%s", strings.Join(args, " "), dir, err, output)
	}
	return strings.TrimSpace(string(output))
}
