package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestPreCommitRunsBackendChecksFromStagedSnapshot(t *testing.T) {
	fixture := newHookCodeCheckFixture(t, ".githooks/pre-commit")
	fixture.writeGoModule(t)
	fixture.writeGoPackages(t)
	fixture.install(t)

	writeFixTestGuardFile(t, fixture.root, "internal/app/app.go", "package app\n\nfunc App() string { return \"changed\" }\n")
	runFixTestGuardGit(t, fixture.root, "add", "internal/app/app.go")
	writeFixTestGuardFile(t, fixture.root, "internal/broken/broken.go", "package broken\n\nfunc Broken( {\n")

	out := fixture.runPreCommit(t)
	assertOutputContainsAll(t, out, "[pre-commit] go package tests:", "fake test-with-guard")
	assertOutputContainsAll(t, fixture.log(t), "test-with-guard ./internal/app ./internal/consumer -count=1")
}

func TestPreCommitRunsFrontendChecksForStagedFrontend(t *testing.T) {
	fixture := newHookCodeCheckFixture(t, ".githooks/pre-commit")
	fixture.writeFrontendPackage(t)
	fixture.install(t)

	writeFixTestGuardFile(t, fixture.root, "cmd/agent-terminal/frontend/vue-app/app.js", "export const app = 'changed'\n")
	runFixTestGuardGit(t, fixture.root, "add", "cmd/agent-terminal/frontend/vue-app/app.js")

	out := fixture.runPreCommit(t)
	assertOutputContainsAll(t, out, "[pre-commit] frontend codebase guard", "[pre-commit] frontend package tests")
	assertOutputContainsAll(t, fixture.log(t), "node scripts/size-guard.cjs", "npx vitest run")
	assertOutputOmitsAll(t, fixture.log(t), "test-with-guard")
}

func TestPreCommitDocsOnlySkipsCodeChecks(t *testing.T) {
	fixture := newHookCodeCheckFixture(t, ".githooks/pre-commit")
	fixture.install(t)

	writeFixTestGuardFile(t, fixture.root, "docs/readme.md", "docs only\n")
	runFixTestGuardGit(t, fixture.root, "add", "docs/readme.md")

	out := fixture.runPreCommit(t)
	assertOutputContainsAll(t, out, "pre-commit OK")
	if log := fixture.log(t); log != "" {
		t.Fatalf("code checks should not run for docs-only commit\n%s", log)
	}
}

func TestPrePushRunsBackendChecksFromPushedCommitWithDirtyWorktree(t *testing.T) {
	fixture := newHookCodeCheckFixture(t, ".githooks/pre-push")
	fixture.writeGoModule(t)
	fixture.writeGoPackages(t)
	fixture.install(t)
	base := strings.TrimSpace(runFixTestGuardGitOutput(t, fixture.root, "rev-parse", "HEAD"))

	writeFixTestGuardFile(t, fixture.root, "internal/app/app.go", "package app\n\nfunc App() string { return \"changed\" }\n")
	runFixTestGuardGit(t, fixture.root, "add", "internal/app/app.go")
	runFixTestGuardGit(t, fixture.root, "commit", "-m", "chore: 更新 app package")
	head := strings.TrimSpace(runFixTestGuardGitOutput(t, fixture.root, "rev-parse", "HEAD"))
	writeFixTestGuardFile(t, fixture.root, "internal/broken/broken.go", "package broken\n\nfunc Broken( {\n")

	out := fixture.runPrePush(t, prePushStdin(base, head))
	assertOutputContainsAll(t, out, "[pre-push] go package tests:", "fake test-with-guard", "pre-push OK")
	assertOutputContainsAll(t, fixture.log(t), "test-with-guard ./internal/app ./internal/consumer -count=1")
	assertOutputOmitsAll(t, out, "worktree 有未暂存改动", "worktree 有未跟踪文件")
}

func TestPreMergeCommitRunsBackendChecksForMergeIndex(t *testing.T) {
	fixture := newHookCodeCheckFixture(t, ".githooks/pre-merge-commit")
	fixture.writeGoModule(t)
	fixture.writeGoPackages(t)
	fixture.install(t)

	writeFixTestGuardFile(t, fixture.root, "internal/app/app.go", "package app\n\nfunc App() string { return \"merged\" }\n")
	runFixTestGuardGit(t, fixture.root, "add", "internal/app/app.go")

	out := fixture.runPreMergeCommit(t)
	assertOutputContainsAll(t, out, "[pre-merge-commit] go package tests:", "fake test-with-guard")
	assertOutputContainsAll(t, fixture.log(t), "test-with-guard ./internal/app ./internal/consumer -count=1")
}

func TestCommitMsgRejectsEnglishMergeTitleAndBody(t *testing.T) {
	root := prepareFixTestGuardRepo(t)
	copyFixTestGuardRepoFile(t, root, ".githooks/commit-msg", 0o755)
	copyFixTestGuardRepoFile(t, root, "scripts/guard_commit_titles.sh", 0o755)
	msgFile := filepath.Join(root, "COMMIT_EDITMSG")
	message := "Merge branch 'codex/orch-tools-optimization' into 'main'\n\nCodex/orch tools optimization\n\nSee merge request ai/Super-Dolphin!9\n"
	if err := os.WriteFile(msgFile, []byte(message), 0o644); err != nil {
		t.Fatalf("write commit message: %v", err)
	}

	out, err := runCommitMsgHook(t, root, msgFile)
	if err == nil {
		t.Fatalf("commit-msg succeeded, want failure\n%s", out)
	}
	assertOutputContainsAll(t, out, "[commit-msg] Chinese commit message guard", "commit title must contain Chinese text", "Merge branch 'codex/orch-tools-optimization' into 'main'")
	assertOutputOmitsAll(t, out, "[commit-msg] fix-test guard")
}

func TestPrePushRejectsEnglishMergeCommitTitle(t *testing.T) {
	root := prepareFixTestGuardRepo(t)
	copyFixTestGuardRepoFile(t, root, ".githooks/pre-push", 0o755)
	copyFixTestGuardRepoFile(t, root, "scripts/guard_commit_titles.sh", 0o755)
	copyFixTestGuardRepoFile(t, root, "scripts/guard_fix_commits_have_tests.sh", 0o755)
	base := strings.TrimSpace(runFixTestGuardGitOutput(t, root, "rev-parse", "HEAD"))
	baseBranch := strings.TrimSpace(runFixTestGuardGitOutput(t, root, "branch", "--show-current"))

	runFixTestGuardGit(t, root, "checkout", "-b", "feature")
	writeFixTestGuardFile(t, root, "docs/feature.md", "feature docs\n")
	runFixTestGuardGit(t, root, "add", "docs/feature.md")
	runFixTestGuardGit(t, root, "commit", "-m", "docs: 更新 feature")
	runFixTestGuardGit(t, root, "checkout", baseBranch)
	runFixTestGuardGit(t, root, "merge", "--no-ff", "feature", "-m", "Merge branch 'feature' into 'main'", "-m", "See merge request ai/Super-Dolphin!9")
	head := strings.TrimSpace(runFixTestGuardGitOutput(t, root, "rev-parse", "HEAD"))

	cmd := exec.Command("bash", filepath.Join(".githooks", "pre-push"))
	cmd.Dir = root
	cmd.Stdin = strings.NewReader(prePushStdin(base, head))
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("pre-push succeeded, want failure\n%s", string(out))
	}
	assertOutputContainsAll(t, string(out), "[pre-push] Chinese commit message guard", "commit "+head[:7]+" title must contain Chinese text", "Merge branch 'feature' into 'main'")
	assertOutputOmitsAll(t, string(out), "[pre-push] fix-test guard")
}

func TestInstallHooksWritesAbsoluteHooksPath(t *testing.T) {
	root := t.TempDir()
	runFixTestGuardGit(t, root, "init", "-q")
	writeFixTestGuardFile(t, root, ".githooks/pre-commit", "#!/usr/bin/env bash\n")
	copyFixTestGuardRepoFile(t, root, "scripts/install-hooks.sh", 0o755)

	cmd := exec.Command("bash", filepath.Join("scripts", "install-hooks.sh"))
	cmd.Dir = root
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("install-hooks failed: %v\n%s", err, string(out))
	}
	configCmd := exec.Command("git", "config", "--get", "core.hooksPath")
	configCmd.Dir = root
	configOut, err := configCmd.CombinedOutput()
	if err != nil {
		t.Fatalf("read core.hooksPath failed: %v\n%s", err, string(configOut))
	}
	got := strings.TrimSpace(string(configOut))
	realRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatalf("resolve temp root symlink: %v", err)
	}
	want := filepath.Join(realRoot, ".githooks")
	if got != want {
		t.Fatalf("core.hooksPath = %q, want %q\noutput:\n%s", got, want, string(out))
	}
}

type hookCodeCheckFixture struct {
	root    string
	logPath string
	binDir  string
}

func newHookCodeCheckFixture(t *testing.T, hooks ...string) hookCodeCheckFixture {
	t.Helper()
	root := prepareFixTestGuardRepo(t)
	logPath := filepath.Join(t.TempDir(), "hook-code-checks.log")
	fixture := hookCodeCheckFixture{
		root:    root,
		logPath: logPath,
		binDir:  writeHookCodeCheckFakeBins(t, logPath),
	}
	for _, hook := range hooks {
		copyFixTestGuardRepoFile(t, root, hook, 0o755)
	}
	copyFixTestGuardRepoFile(t, root, ".githooks/run-index-code-checks", 0o755)
	copyFixTestGuardRepoFile(t, root, "scripts/hook_code_checks.sh", 0o755)
	copyFixTestGuardRepoFile(t, root, "scripts/guard_commit_titles.sh", 0o755)
	writeHookCodeCheckFakeTestWithGuard(t, root)
	return fixture
}

func (f hookCodeCheckFixture) writeGoModule(t *testing.T) {
	t.Helper()
	writeFixTestGuardFile(t, f.root, "go.mod", "module example.com/hookscope\n\ngo 1.22\n")
}

func (f hookCodeCheckFixture) writeGoPackages(t *testing.T) {
	t.Helper()
	writeFixTestGuardFile(t, f.root, "internal/app/app.go", "package app\n\nfunc App() string { return \"ready\" }\n")
	writeFixTestGuardFile(t, f.root, "internal/consumer/consumer.go", "package consumer\n\nimport \"example.com/hookscope/internal/app\"\n\nfunc Value() string { return app.App() }\n")
}

func (f hookCodeCheckFixture) writeFrontendPackage(t *testing.T) {
	t.Helper()
	writeFixTestGuardFile(t, f.root, "cmd/agent-terminal/frontend/package.json", "{\"type\":\"module\"}\n")
	writeFixTestGuardFile(t, f.root, "cmd/agent-terminal/frontend/scripts/size-guard.cjs", "console.log('size guard ok')\n")
	writeFixTestGuardFile(t, f.root, "cmd/agent-terminal/frontend/vue-app/app.js", "export const app = 'ready'\n")
}

func (f hookCodeCheckFixture) install(t *testing.T) {
	t.Helper()
	runFixTestGuardGit(t, f.root, "add", ".")
	runFixTestGuardGit(t, f.root, "commit", "-m", "chore: 安装 hook fixture")
}

func (f hookCodeCheckFixture) runPreCommit(t *testing.T) string {
	t.Helper()
	return runHookCodeCheckCommand(t, f.root, f.binDir, f.logPath, ".githooks/pre-commit", "")
}

func (f hookCodeCheckFixture) runPreMergeCommit(t *testing.T) string {
	t.Helper()
	return runHookCodeCheckCommand(t, f.root, f.binDir, f.logPath, ".githooks/pre-merge-commit", "")
}

func (f hookCodeCheckFixture) runPrePush(t *testing.T, stdin string) string {
	t.Helper()
	return runHookCodeCheckCommand(t, f.root, f.binDir, f.logPath, ".githooks/pre-push", stdin)
}

func (f hookCodeCheckFixture) log(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile(f.logPath)
	if err != nil {
		t.Fatalf("read hook log: %v", err)
	}
	return string(data)
}

func runHookCodeCheckCommand(t *testing.T, root, binDir, logPath, script, stdin string) string {
	t.Helper()
	cmd := exec.Command("bash", script)
	cmd.Dir = root
	if stdin != "" {
		cmd.Stdin = strings.NewReader(stdin)
	}
	cmd.Env = append(os.Environ(),
		"PATH="+binDir+string(os.PathListSeparator)+os.Getenv("PATH"),
		"HOOK_SCOPE_LOG="+logPath,
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%s failed: %v\n%s", script, err, string(out))
	}
	return string(out)
}

func writeHookCodeCheckFakeTestWithGuard(t *testing.T, root string) {
	t.Helper()
	content := "#!/usr/bin/env bash\nset -e\nprintf 'test-with-guard %s\\n' \"$*\" >>\"$HOOK_SCOPE_LOG\"\nprintf 'fake test-with-guard %s\\n' \"$*\"\n"
	path := filepath.Join(root, "scripts", "test_with_guard.sh")
	if err := os.WriteFile(path, []byte(content), 0o755); err != nil {
		t.Fatalf("write fake test_with_guard.sh: %v", err)
	}
}

func writeHookCodeCheckFakeBins(t *testing.T, logPath string) string {
	t.Helper()
	binDir := t.TempDir()
	for name, content := range map[string]string{
		"node": "#!/usr/bin/env bash\nprintf 'node %s\\n' \"$*\" >>\"$HOOK_SCOPE_LOG\"\nprintf 'fake node %s\\n' \"$*\"\n",
		"npx":  "#!/usr/bin/env bash\nprintf 'npx %s\\n' \"$*\" >>\"$HOOK_SCOPE_LOG\"\nprintf 'fake npx %s\\n' \"$*\"\n",
		"npm":  "#!/usr/bin/env bash\nprintf 'npm %s\\n' \"$*\" >>\"$HOOK_SCOPE_LOG\"\nprintf 'fake npm %s\\n' \"$*\"\n",
	} {
		path := filepath.Join(binDir, name)
		if err := os.WriteFile(path, []byte(content), 0o755); err != nil {
			t.Fatalf("write fake %s: %v", name, err)
		}
	}
	if err := os.WriteFile(logPath, nil, 0o644); err != nil {
		t.Fatalf("write hook log: %v", err)
	}
	return binDir
}
