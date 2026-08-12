package main

import (
	"strings"
	"testing"
)

func TestPreCommitRunsCodeGuardFromSyntheticMergeCommit(t *testing.T) {
	root := preparePreCommitGateFixture(t)
	baseBranch := strings.TrimSpace(runFixTestGuardGitOutput(t, root, "branch", "--show-current"))

	runFixTestGuardGit(t, root, "checkout", "-b", "side")
	writeFixTestGuardFile(t, root, "docs/side.md", "side\n")
	runFixTestGuardGit(t, root, "add", "docs/side.md")
	runFixTestGuardGit(t, root, "commit", "-m", "docs: 添加 side 输入")

	runFixTestGuardGit(t, root, "checkout", baseBranch)
	writeFixTestGuardFile(t, root, "docs/main.md", "main\n")
	runFixTestGuardGit(t, root, "add", "docs/main.md")
	runFixTestGuardGit(t, root, "commit", "-m", "docs: 添加 main 输入")
	runFixTestGuardGit(t, root, "merge", "--no-commit", "--no-ff", "side")
	stagedTree := strings.TrimSpace(runFixTestGuardGitOutput(t, root, "write-tree"))
	stagedPaths := runFixTestGuardGitOutput(t, root, "ls-tree", "-r", "--name-only", stagedTree)
	assertOutputContainsAll(t, stagedPaths, "docs/main.md", "docs/side.md")

	out, err := runPreCommitHookWithEnv(t, root, nil)
	if err != nil {
		t.Fatalf("pre-commit rejected a clean staged merge snapshot: %v\n%s", err, out)
	}
	assertOutputContainsAll(t, out, "fake code guard --light-guard-only", "tree="+stagedTree, "pre-commit OK")
}
