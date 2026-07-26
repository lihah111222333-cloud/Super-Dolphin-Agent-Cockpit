package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
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
	stagedTree := strings.TrimSpace(runFixTestGuardGitOutput(t, root, "write-tree"))
	out, err := runPreCommitHookWithEnv(t, root, map[string]string{"TMPDIR": tmpRoot, "GATE_FORCE_CLEANUP_FAILURE": "1"})
	if err == nil {
		t.Fatalf("pre-commit cleanup failure succeeded:\n%s", out)
	}
	assertOutputContainsAll(t, out,
		"fixture closure verified staged tree "+stagedTree,
		"fixture hook queued staged tree "+stagedTree+" job=job-0123456789abcdef0123456789abcdef",
		"fixture wait verified staged tree "+stagedTree+" job=job-0123456789abcdef0123456789abcdef",
		"pre-commit cleanup failed",
		"pre-commit cleanup verification failed",
	)
}

func TestPreCommitWaitFailurePreservesRepositoryState(t *testing.T) {
	root := preparePreCommitGateFixture(t)
	writeFixTestGuardFile(t, root, "internal/app/failure.go", "package app\n")
	runFixTestGuardGit(t, root, "add", "internal/app/failure.go")
	originalState := capturePreCommitRepositoryState(t, root)
	tmpRoot := t.TempDir()

	out, err := runPreCommitHookWithEnv(t, root, map[string]string{
		"TMPDIR":                  tmpRoot,
		"GATE_WAIT_FORCE_FAILURE": "1",
	})
	if err == nil {
		t.Fatalf("forced wait failure unexpectedly succeeded:\n%s", out)
	}
	assertOutputContainsAll(t, out, "forced wait failure")
	assertPreCommitRepositoryState(t, root, originalState)
	assertPreCommitFixtureClean(t, root, tmpRoot)
}

func TestPreCommitSIGINTCleansHookOutputFiles(t *testing.T) {
	root := preparePreCommitGateFixture(t)
	writeFixTestGuardFile(t, root, "internal/app/interrupt.go", "package app\n")
	runFixTestGuardGit(t, root, "add", "internal/app/interrupt.go")
	originalState := capturePreCommitRepositoryState(t, root)
	tmpRoot := t.TempDir()
	readyFile := filepath.Join(t.TempDir(), "gate-ready")
	cmd := preCommitCommand(t, root, map[string]string{
		"TMPDIR":                  tmpRoot,
		"GATE_WAIT_READY_FILE":    readyFile,
		"GATE_WAIT_FOR_INTERRUPT": "1",
	})
	var output bytes.Buffer
	cmd.Stdout = &output
	cmd.Stderr = &output
	if err := cmd.Start(); err != nil {
		t.Fatalf("start pre-commit for SIGINT: %v", err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := os.Stat(readyFile); err == nil {
			break
		}
		if time.Now().After(deadline) {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
			t.Fatalf("gate did not become ready for SIGINT:\n%s", output.String())
		}
		time.Sleep(20 * time.Millisecond)
	}
	if err := cmd.Process.Signal(os.Interrupt); err != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		t.Fatalf("send SIGINT to pre-commit: %v", err)
	}
	if err := cmd.Wait(); err == nil {
		t.Fatalf("SIGINT pre-commit unexpectedly succeeded:\n%s", output.String())
	}
	assertPreCommitRepositoryState(t, root, originalState)
	assertPreCommitFixtureClean(t, root, tmpRoot)
}
