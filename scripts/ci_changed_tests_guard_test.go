package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestCIChangedTestsRunsAffectedGoPackagesAndReverseDependencies(t *testing.T) {
	root := prepareCIChangedTestsRepo(t)
	writeFixTestGuardFile(t, root, "go.mod", "module example.com/cichanged\n\ngo 1.22\n")
	writeFixTestGuardFile(t, root, "internal/base/base.go", "package base\n\nfunc Value() string { return \"old\" }\n")
	writeFixTestGuardFile(t, root, "internal/dependent/dependent.go", "package dependent\n\nimport \"example.com/cichanged/internal/base\"\n\nfunc Value() string { return base.Value() }\n")
	runFixTestGuardGit(t, root, "add", "go.mod", "internal/base/base.go", "internal/dependent/dependent.go")
	runFixTestGuardGit(t, root, "commit", "-m", "chore: 准备 go package graph")
	base := strings.TrimSpace(runFixTestGuardGitOutput(t, root, "rev-parse", "HEAD"))

	writeFixTestGuardFile(t, root, "internal/base/base.go", "package base\n\nfunc Value() string { return \"new\" }\n")
	runFixTestGuardGit(t, root, "add", "internal/base/base.go")
	runFixTestGuardGit(t, root, "commit", "-m", "chore: 更新 base package")
	head := strings.TrimSpace(runFixTestGuardGitOutput(t, root, "rev-parse", "HEAD"))

	logPath := filepath.Join(t.TempDir(), "ci-changed-tests.log")
	writePrePushFakeGoTestScript(t, root)
	out, err := runCIChangedTests(t, root, logPath, map[string]string{
		"GITHUB_EVENT_NAME":   "push",
		"GITHUB_EVENT_BEFORE": base,
		"GITHUB_SHA":          head,
	})
	if err != nil {
		t.Fatalf("ci changed tests failed: %v\n%s", err, out)
	}
	assertOutputContainsAll(t, out, "[ci-changed-tests] go affected package tests:", "./internal/base", "./internal/dependent")
	assertOutputContainsAll(t, readPrePushScopeLog(t, logPath), "go-test ./internal/base ./internal/dependent -count=1")
}

func TestCIChangedTestsSkipsPackageTestsForDocsOnlyRange(t *testing.T) {
	root := prepareCIChangedTestsRepo(t)
	base := strings.TrimSpace(runFixTestGuardGitOutput(t, root, "rev-parse", "HEAD"))
	writeFixTestGuardFile(t, root, "docs/readme.md", "docs only\n")
	runFixTestGuardGit(t, root, "add", "docs/readme.md")
	runFixTestGuardGit(t, root, "commit", "-m", "docs: 更新 guide")
	head := strings.TrimSpace(runFixTestGuardGitOutput(t, root, "rev-parse", "HEAD"))

	logPath := filepath.Join(t.TempDir(), "ci-changed-tests.log")
	writePrePushFakeGoTestScript(t, root)
	out, err := runCIChangedTests(t, root, logPath, map[string]string{
		"GITHUB_EVENT_NAME":   "push",
		"GITHUB_EVENT_BEFORE": base,
		"GITHUB_SHA":          head,
	})
	if err != nil {
		t.Fatalf("ci changed tests failed: %v\n%s", err, out)
	}
	assertOutputContainsAll(t, out, "[ci-changed-tests] no guarded code changes; skipping package tests")
	if log := readPrePushScopeLog(t, logPath); log != "" {
		t.Fatalf("package command log should be empty for docs-only CI range\n%s", log)
	}
}

func TestCIChangedTestsRunsFrontendOnlyWithoutGoPackages(t *testing.T) {
	root := prepareCIChangedTestsRepo(t)
	base := strings.TrimSpace(runFixTestGuardGitOutput(t, root, "rev-parse", "HEAD"))
	writeFixTestGuardFile(t, root, "cmd/agent-terminal/frontend/scripts/size-guard.cjs", "console.log('ok')\n")
	writeFixTestGuardFile(t, root, "cmd/agent-terminal/frontend/src/App.vue", "<template><main /></template>\n")
	runFixTestGuardGit(t, root, "add", "cmd/agent-terminal/frontend")
	runFixTestGuardGit(t, root, "commit", "-m", "chore: 更新 frontend package")
	head := strings.TrimSpace(runFixTestGuardGitOutput(t, root, "rev-parse", "HEAD"))

	logPath := filepath.Join(t.TempDir(), "ci-changed-tests.log")
	binDir := writePrePushScopeFakeBins(t, logPath)
	out, err := runCIChangedTests(t, root, logPath, map[string]string{
		"GITHUB_EVENT_NAME":   "push",
		"GITHUB_EVENT_BEFORE": base,
		"GITHUB_SHA":          head,
		"PATH":                binDir + string(os.PathListSeparator) + os.Getenv("PATH"),
	})
	if err != nil {
		t.Fatalf("ci changed tests failed: %v\n%s", err, out)
	}
	assertOutputContainsAll(t, out, "[ci-changed-tests] frontend codebase guard", "[ci-changed-tests] frontend package tests")
	log := readPrePushScopeLog(t, logPath)
	assertOutputContainsAll(t, log, "node scripts/size-guard.cjs", "npx vitest run")
	assertOutputOmitsAll(t, log, "go-test ")
}

func TestCIWorkflowRunsChangedTestsGate(t *testing.T) {
	workflow := locateFixTestGuardRepoFile(t, ".github/workflows/ci.yml")
	data, err := os.ReadFile(workflow)
	if err != nil {
		t.Fatalf("read ci workflow: %v", err)
	}

	content := string(data)
	assertOutputContainsAll(t, content, "./scripts/ci_changed_tests.sh")
	assertOutputOmitsAll(t, content, "./scripts/test_with_guard.sh $(go list ./...")
}

func prepareCIChangedTestsRepo(t *testing.T) string {
	t.Helper()
	root := prepareFixTestGuardRepo(t)
	copyFixTestGuardRepoFile(t, root, "scripts/ci_changed_tests.sh", 0o755)
	return root
}

func runCIChangedTests(t *testing.T, root, logPath string, env map[string]string) (string, error) {
	t.Helper()
	if err := os.WriteFile(logPath, nil, 0o644); err != nil {
		t.Fatalf("write log file: %v", err)
	}
	cmd := exec.Command("bash", filepath.Join("scripts", "ci_changed_tests.sh"))
	cmd.Dir = root
	cmd.Env = append(os.Environ(), "HOOK_SCOPE_LOG="+logPath)
	for key, value := range env {
		cmd.Env = append(cmd.Env, key+"="+value)
	}
	out, err := cmd.CombinedOutput()
	return string(out), err
}
