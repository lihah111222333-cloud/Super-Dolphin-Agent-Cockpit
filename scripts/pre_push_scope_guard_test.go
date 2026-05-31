package main

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestPrePushIncludesReverseDependenciesForGoChanges(t *testing.T) {
	root := preparePrePushScopeRepo(t)
	writeFixTestGuardFile(t, root, "go.mod", "module example.com/prepushscope\n\ngo 1.22\n")
	writeFixTestGuardFile(t, root, "internal/base/base.go", "package base\n\nfunc Value() string { return \"old\" }\n")
	writeFixTestGuardFile(t, root, "internal/dependent/dependent.go", "package dependent\n\nimport \"example.com/prepushscope/internal/base\"\n\nfunc Value() string { return base.Value() }\n")
	runFixTestGuardGit(t, root, "add", "go.mod", "internal/base/base.go", "internal/dependent/dependent.go")
	runFixTestGuardGit(t, root, "commit", "-m", "chore: 准备 go package graph")
	base := strings.TrimSpace(runFixTestGuardGitOutput(t, root, "rev-parse", "HEAD"))

	writeFixTestGuardFile(t, root, "internal/base/base.go", "package base\n\nfunc Value() string { return \"new\" }\n")
	runFixTestGuardGit(t, root, "add", "internal/base/base.go")
	runFixTestGuardGit(t, root, "commit", "-m", "chore: 更新 base package")
	head := strings.TrimSpace(runFixTestGuardGitOutput(t, root, "rev-parse", "HEAD"))

	logPath := filepath.Join(t.TempDir(), "hook-scope.log")
	binDir := writePrePushScopeFakeBins(t, logPath)
	out, err := runPrePushScopeHook(t, root, prePushStdin(base, head), binDir, logPath)
	if err != nil {
		t.Fatalf("pre-push failed: %v\n%s", err, out)
	}
	assertOutputContainsAll(t, out, "[pre-push] go affected package tests:", "./internal/base", "./internal/dependent", "pre-push OK")
	assertOutputContainsAll(t, readPrePushScopeLog(t, logPath), "go-test ./internal/base ./internal/dependent -count=1")
}
