package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestInstallHooksMakeTargetUsesWorktreeSafeRelativePath(t *testing.T) {
	if _, err := exec.LookPath("make"); err != nil {
		t.Skip("make is required for install-hooks regression")
	}

	root := t.TempDir()
	runInstallHooksGit(t, root, "init", "-q")
	runInstallHooksGit(t, root, "config", "user.email", "hooks@example.test")
	runInstallHooksGit(t, root, "config", "user.name", "Hooks Test")

	if err := os.Mkdir(filepath.Join(root, ".githooks"), 0o755); err != nil {
		t.Fatalf("create .githooks: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, ".githooks", "pre-commit"), []byte("#!/usr/bin/env bash\nexit 0\n"), 0o755); err != nil {
		t.Fatalf("write pre-commit: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("hooks fixture\n"), 0o644); err != nil {
		t.Fatalf("write README: %v", err)
	}
	runInstallHooksGit(t, root, "add", ".githooks/pre-commit", "README.md")
	runInstallHooksGit(t, root, "commit", "-m", "chore: 初始化 hooks fixture")

	staleMainHooks := filepath.Join(t.TempDir(), ".githooks")
	runInstallHooksGit(t, root, "config", "core.hooksPath", staleMainHooks)

	cmd := exec.Command("make", "-f", filepath.Join(scriptRepoRoot(t), "Makefile"), "install-hooks")
	cmd.Dir = root
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("make install-hooks failed: %v\n%s", err, output)
	}

	if got := installHooksGitOutput(t, root, "config", "--get", "core.hooksPath"); got != ".githooks" {
		t.Fatalf("core.hooksPath = %q, want .githooks\nmake output:\n%s", got, output)
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
