package main

import (
	"strings"
	"testing"
)

func TestPreCommitRunsMergeGateFromSyntheticMergeCommit(t *testing.T) {
	root := preparePreCommitGateFixture(t)
	baseBranch := strings.TrimSpace(runFixTestGuardGitOutput(t, root, "branch", "--show-current"))

	runFixTestGuardGit(t, root, "checkout", "-b", "side-one")
	writeFixTestGuardFile(t, root, "docs/side-one.md", "side one\n")
	runFixTestGuardGit(t, root, "add", "docs/side-one.md")
	runFixTestGuardGit(t, root, "commit", "-m", "docs: 添加 side one 输入")
	sideOneSHA := strings.TrimSpace(runFixTestGuardGitOutput(t, root, "rev-parse", "HEAD"))

	runFixTestGuardGit(t, root, "checkout", baseBranch)
	runFixTestGuardGit(t, root, "checkout", "-b", "side-two")
	writeFixTestGuardFile(t, root, "docs/side-two.md", "side two\n")
	runFixTestGuardGit(t, root, "add", "docs/side-two.md")
	runFixTestGuardGit(t, root, "commit", "-m", "docs: 添加 side two 输入")
	sideTwoSHA := strings.TrimSpace(runFixTestGuardGitOutput(t, root, "rev-parse", "HEAD"))

	runFixTestGuardGit(t, root, "checkout", baseBranch)
	writeFixTestGuardFile(t, root, "docs/main.md", "main\n")
	runFixTestGuardGit(t, root, "add", "docs/main.md")
	runFixTestGuardGit(t, root, "commit", "-m", "docs: 添加 main 输入")
	mainSHA := strings.TrimSpace(runFixTestGuardGitOutput(t, root, "rev-parse", "HEAD"))
	runFixTestGuardGit(t, root, "merge", "--no-commit", "--no-ff", "side-one", "side-two")
	warmOut, warmErr := runPreCommitHook(t, root)
	if warmErr != nil {
		t.Fatalf("pre-commit merge fixture warmup failed: %v\n%s", warmErr, warmOut)
	}
	originalState := capturePreCommitRepositoryState(t, root)

	out, err := runPreCommitHookWithEnv(t, root, map[string]string{"GATE_ASSERT_CLEAN": "1"})
	if err != nil {
		t.Fatalf("pre-commit rejected a clean staged merge snapshot: %v\n%s", err, out)
	}
	assertSyntheticGateSnapshot(t, out, originalState.indexTree, mainSHA, mainSHA, sideOneSHA, sideTwoSHA)
	assertPreCommitRepositoryState(t, root, originalState)
	assertOutputContainsAll(t, out, "pre-commit OK")
}

func TestPreCommitRunsInitialGateFromParentlessSyntheticCommit(t *testing.T) {
	root := preparePreCommitGateFixture(t)
	runFixTestGuardGit(t, root, "checkout", "--orphan", "initial-snapshot")
	warmOut, warmErr := runPreCommitHook(t, root)
	if warmErr != nil {
		t.Fatalf("pre-commit initial fixture warmup failed: %v\n%s", warmErr, warmOut)
	}
	originalState := capturePreCommitRepositoryState(t, root)
	if originalState.headCommit != "<unborn>" {
		t.Fatalf("initial fixture unexpectedly has HEAD commit %s", originalState.headCommit)
	}
	emptyTree := strings.TrimSpace(runFixTestGuardGitOutput(t, root, "hash-object", "-t", "tree", "/dev/null"))

	out, err := runPreCommitHookWithEnv(t, root, map[string]string{"GATE_ASSERT_CLEAN": "1"})
	if err != nil {
		t.Fatalf("pre-commit rejected a clean initial staged snapshot: %v\n%s", err, out)
	}
	assertSyntheticGateSnapshot(t, out, originalState.indexTree, emptyTree)
	assertPreCommitRepositoryState(t, root, originalState)
}
