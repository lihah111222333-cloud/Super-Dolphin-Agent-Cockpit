package main

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

func TestPreCommitCleanupFailureFailsHook(t *testing.T) {
	root := preparePreCommitGateFixture(t)
	tmpRoot := t.TempDir()
	t.Cleanup(func() {
		_ = os.Chmod(tmpRoot, 0o700)
		listed := runFixTestGuardGitOutput(t, root, "worktree", "list", "--porcelain")
		for line := range strings.SplitSeq(listed, "\n") {
			path, ok := strings.CutPrefix(line, "worktree ")
			if !ok || path == root {
				continue
			}
			cmd := exec.Command("git", "worktree", "remove", "--force", path)
			cmd.Dir = root
			_ = cmd.Run()
		}
	})
	writeFixTestGuardFile(t, root, "internal/app/cleanup.go", "package app\n")
	runFixTestGuardGit(t, root, "add", "internal/app/cleanup.go")
	out, err := runPreCommitHookWithEnv(t, root, map[string]string{"TMPDIR": tmpRoot, "GATE_FORCE_CLEANUP_FAILURE": "1"})
	if err == nil {
		t.Fatalf("pre-commit cleanup failure succeeded:\n%s", out)
	}
	assertOutputContainsAll(t, out, "pre-commit cleanup failed", "pre-commit cleanup verification failed")
}
