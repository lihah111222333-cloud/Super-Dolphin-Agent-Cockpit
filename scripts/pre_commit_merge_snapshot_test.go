package main

import (
	"strings"
	"testing"
)

func TestPreCommitRunsMergeGateFromSyntheticMergeCommit(t *testing.T) {
	root := preparePreCommitGateFixture(t)
	baseBranch := strings.TrimSpace(runFixTestGuardGitOutput(t, root, "branch", "--show-current"))

	runFixTestGuardGit(t, root, "checkout", "-b", "side")
	writeFixTestGuardFile(t, root, "docs/side.md", "side\n")
	runFixTestGuardGit(t, root, "add", "docs/side.md")
	runFixTestGuardGit(t, root, "commit", "-m", "docs: 添加 side 输入")
	sideSHA := strings.TrimSpace(runFixTestGuardGitOutput(t, root, "rev-parse", "HEAD"))

	runFixTestGuardGit(t, root, "checkout", baseBranch)
	writeFixTestGuardFile(t, root, "docs/main.md", "main\n")
	runFixTestGuardGit(t, root, "add", "docs/main.md")
	runFixTestGuardGit(t, root, "commit", "-m", "docs: 添加 main 输入")
	mainSHA := strings.TrimSpace(runFixTestGuardGitOutput(t, root, "rev-parse", "HEAD"))
	runFixTestGuardGit(t, root, "merge", "--no-commit", "--no-ff", "side")

	out, err := runPreCommitHook(t, root)
	if err != nil {
		t.Fatalf("pre-commit rejected a clean staged merge snapshot: %v\n%s", err, out)
	}
	assertOutputContainsAll(t, out, "gate-head-parents=", mainSHA, sideSHA, "pre-commit OK")
}
