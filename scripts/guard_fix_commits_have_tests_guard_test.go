package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestGuardFixCommitsHaveTestsCached(t *testing.T) {
	tests := []struct {
		name      string
		subject   string
		files     map[string]string
		wantErr   bool
		wantParts []string
	}{
		{
			name:    "rejects plain fix prefix without test",
			subject: "Fix repair timestamp parsing",
			files: map[string]string{
				"internal/app/parser.go": "package app\n\nfunc parse() {}\n",
			},
			wantErr:   true,
			wantParts: []string{"fix 提交缺少锁定 bug 的测试", "Fix repair timestamp parsing"},
		},
		{
			name:    "allows fix with go test",
			subject: "fix(worker): fence async transition",
			files: map[string]string{
				"internal/worker/state.go":      "package worker\n\nfunc transition() string { return \"ready\" }\n",
				"internal/worker/state_test.go": "package worker\n\nimport \"testing\"\n\nfunc TestTransitionBug(t *testing.T) {\n\tif got := transition(); got != \"ready\" {\n\t\tt.Fatalf(\"transition() = %q, want ready\", got)\n\t}\n}\n",
			},
		},
		{
			name:    "allows chinese fix with regression fixture",
			subject: "[修复] 恢复配置补丁回归",
			files: map[string]string{
				"internal/config/patch.go":           "package config\n\nfunc patch() {}\n",
				"internal/config/testdata/case.json": "{}\n",
			},
		},
		{
			name:    "allows non fix without test",
			subject: "chore: refresh docs",
			files: map[string]string{
				"internal/app/parser.go": "package app\n\nfunc parse() {}\n",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := prepareFixTestGuardRepo(t)
			for path, content := range tt.files {
				writeFixTestGuardFile(t, root, path, content)
			}
			runFixTestGuardGit(t, root, "add", ".")
			msgFile := filepath.Join(root, "COMMIT_EDITMSG")
			if err := os.WriteFile(msgFile, []byte(tt.subject+"\n"), 0o644); err != nil {
				t.Fatalf("write commit message: %v", err)
			}

			out, err := runFixTestGuard(t, root, "--cached", msgFile)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("guard succeeded, want failure\n%s", out)
				}
				for _, part := range tt.wantParts {
					if !strings.Contains(out, part) {
						t.Fatalf("output missing %q\n%s", part, out)
					}
				}
				return
			}
			if err != nil {
				t.Fatalf("guard failed: %v\n%s", err, out)
			}
		})
	}
}

func TestGuardFixCommitsHaveTestsRange(t *testing.T) {
	t.Run("rejects pushed fix commit without test", func(t *testing.T) {
		root := prepareFixTestGuardRepo(t)
		writeFixTestGuardFile(t, root, "internal/app/parser.go", "package app\n\nfunc parse() {}\n")
		runFixTestGuardGit(t, root, "add", ".")
		runFixTestGuardGit(t, root, "commit", "-m", "fix: repair parser panic")

		out, err := runFixTestGuard(t, root, "--range", "HEAD~1..HEAD")
		if err == nil {
			t.Fatalf("guard succeeded, want failure\n%s", out)
		}
		for _, part := range []string{"fix commit 缺少锁定 bug 的测试", "fix: repair parser panic"} {
			if !strings.Contains(out, part) {
				t.Fatalf("output missing %q\n%s", part, out)
			}
		}
	})

	t.Run("allows pushed fix commit with test", func(t *testing.T) {
		root := prepareFixTestGuardRepo(t)
		writeFixTestGuardFile(t, root, "internal/app/parser.go", "package app\n\nfunc parse(input string) string {\n\tif input == \"\" {\n\t\treturn \"empty\"\n\t}\n\treturn \"ok\"\n}\n")
		writeFixTestGuardFile(t, root, "internal/app/parser_test.go", "package app\n\nimport \"testing\"\n\nfunc TestParserPanicBug(t *testing.T) {\n\tif got := parse(\"\"); got != \"empty\" {\n\t\tt.Fatalf(\"parse(empty) = %q, want empty\", got)\n\t}\n}\n")
		runFixTestGuardGit(t, root, "add", ".")
		runFixTestGuardGit(t, root, "commit", "-m", "fix!: repair parser panic")

		out, err := runFixTestGuard(t, root, "--range", "HEAD~1..HEAD")
		if err != nil {
			t.Fatalf("guard failed: %v\n%s", err, out)
		}
		if !strings.Contains(out, "fix-test guard OK") {
			t.Fatalf("output missing success marker\n%s", out)
		}
	})
}

func TestPrePushRunsFixTestGuardForPushedRange(t *testing.T) {
	root := prepareFixTestGuardRepo(t)
	copyFixTestGuardRepoFile(t, root, ".githooks/pre-push", 0o755)
	copyFixTestGuardRepoFile(t, root, "scripts/guard_commit_titles.sh", 0o755)
	runFixTestGuardGit(t, root, "add", ".githooks/pre-push", "scripts/guard_commit_titles.sh", "scripts/guard_fix_commits_have_tests.sh")
	runFixTestGuardGit(t, root, "commit", "-m", "chore: install pre-push fixture")
	base := strings.TrimSpace(runFixTestGuardGitOutput(t, root, "rev-parse", "HEAD"))

	writeFixTestGuardFile(t, root, "internal/app/parser.go", "package app\n\nfunc parse() {}\n")
	runFixTestGuardGit(t, root, "add", ".")
	runFixTestGuardGit(t, root, "commit", "-m", "fix: 修复 parser panic")
	head := strings.TrimSpace(runFixTestGuardGitOutput(t, root, "rev-parse", "HEAD"))

	stdin := "refs/heads/main " + head + " refs/heads/main " + base + "\n"
	cmd := exec.Command("bash", filepath.Join(".githooks", "pre-push"))
	cmd.Dir = root
	cmd.Stdin = strings.NewReader(stdin)
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("pre-push succeeded, want failure\n%s", string(out))
	}
	for _, part := range []string{"[pre-push] Chinese commit title guard", "Chinese commit title guard OK", "[pre-push] fix-test guard", "fix commit 缺少锁定 bug 的测试", "fix: 修复 parser panic"} {
		if !strings.Contains(string(out), part) {
			t.Fatalf("pre-push output missing %q\n%s", part, string(out))
		}
	}
}

func TestCommitMsgRunsChineseTitleGuard(t *testing.T) {
	root := prepareFixTestGuardRepo(t)
	copyFixTestGuardRepoFile(t, root, ".githooks/commit-msg", 0o755)
	copyFixTestGuardRepoFile(t, root, "scripts/guard_commit_titles.sh", 0o755)
	writeFixTestGuardFile(t, root, "docs/readme.md", "docs only\n")
	runFixTestGuardGit(t, root, "add", "docs/readme.md")

	t.Run("rejects title without chinese even when body has chinese", func(t *testing.T) {
		msgFile := filepath.Join(root, "COMMIT_EDITMSG")
		if err := os.WriteFile(msgFile, []byte("docs: update guide\n\n这里有中文正文\n"), 0o644); err != nil {
			t.Fatalf("write commit message: %v", err)
		}

		out, err := runCommitMsgHook(t, root, msgFile)
		if err == nil {
			t.Fatalf("commit-msg succeeded, want failure\n%s", out)
		}
		assertOutputContainsAll(t, out, "[commit-msg] Chinese commit title guard", "commit title must contain Chinese text", "title: docs: update guide")
		assertOutputOmitsAll(t, out, "[commit-msg] fix-test guard")
	})

	t.Run("allows chinese non fix title", func(t *testing.T) {
		msgFile := filepath.Join(root, "COMMIT_EDITMSG")
		if err := os.WriteFile(msgFile, []byte("docs: 更新 guide\n"), 0o644); err != nil {
			t.Fatalf("write commit message: %v", err)
		}

		out, err := runCommitMsgHook(t, root, msgFile)
		if err != nil {
			t.Fatalf("commit-msg failed: %v\n%s", err, out)
		}
		assertOutputContainsAll(t, out, "[commit-msg] Chinese commit title guard", "Chinese commit title guard OK", "[commit-msg] fix-test guard")
	})
}

func TestPrePushRunsChineseTitleGuardForPushedRange(t *testing.T) {
	root := prepareFixTestGuardRepo(t)
	copyFixTestGuardRepoFile(t, root, ".githooks/pre-push", 0o755)
	copyFixTestGuardRepoFile(t, root, "scripts/guard_commit_titles.sh", 0o755)
	runFixTestGuardGit(t, root, "add", ".githooks/pre-push", "scripts/guard_commit_titles.sh", "scripts/guard_fix_commits_have_tests.sh")
	runFixTestGuardGit(t, root, "commit", "-m", "chore: install pre-push fixture")
	base := strings.TrimSpace(runFixTestGuardGitOutput(t, root, "rev-parse", "HEAD"))

	writeFixTestGuardFile(t, root, "docs/readme.md", "docs only\n")
	runFixTestGuardGit(t, root, "add", "docs/readme.md")
	runFixTestGuardGit(t, root, "commit", "-m", "docs: update guide", "-m", "这里有中文正文")
	head := strings.TrimSpace(runFixTestGuardGitOutput(t, root, "rev-parse", "HEAD"))
	shortHead := strings.TrimSpace(runFixTestGuardGitOutput(t, root, "rev-parse", "--short=7", "HEAD"))

	stdin := "refs/heads/main " + head + " refs/heads/main " + base + "\n"
	cmd := exec.Command("bash", filepath.Join(".githooks", "pre-push"))
	cmd.Dir = root
	cmd.Stdin = strings.NewReader(stdin)
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("pre-push succeeded, want failure\n%s", string(out))
	}
	assertOutputContainsAll(t, string(out), "[pre-push] Chinese commit title guard", "commit "+shortHead+" title must contain Chinese text", "title: docs: update guide")
	assertOutputOmitsAll(t, string(out), "[pre-push] fix-test guard")
}

func TestCICommitGuardRunsFixTestGuardForPullRequestRange(t *testing.T) {
	root := prepareFixTestGuardRepo(t)
	copyFixTestGuardRepoFile(t, root, "scripts/ci_commit_guard.sh", 0o755)
	copyFixTestGuardRepoFile(t, root, "scripts/guard_commit_titles.sh", 0o755)
	base := strings.TrimSpace(runFixTestGuardGitOutput(t, root, "rev-parse", "HEAD"))

	writeFixTestGuardFile(t, root, "internal/app/parser.go", "package app\n\nfunc parse() {}\n")
	runFixTestGuardGit(t, root, "add", ".")
	runFixTestGuardGit(t, root, "commit", "-m", "fix: 修复 parser panic")
	head := strings.TrimSpace(runFixTestGuardGitOutput(t, root, "rev-parse", "HEAD"))

	out, err := runCICommitGuard(t, root, map[string]string{
		"GITHUB_EVENT_NAME": "pull_request",
		"GITHUB_BASE_SHA":   base,
		"GITHUB_HEAD_SHA":   head,
	})
	if err == nil {
		t.Fatalf("ci commit guard succeeded, want failure\n%s", out)
	}
	assertOutputContainsAll(t, out, "[ci-commit-guard] Chinese commit title guard: "+base+".."+head, "Chinese commit title guard OK", "[ci-commit-guard] fix-test guard: "+base+".."+head, "fix commit 缺少锁定 bug 的测试", "fix: 修复 parser panic")
}

func TestCICommitGuardRunsFixTestGuardForPushRange(t *testing.T) {
	root := prepareFixTestGuardRepo(t)
	copyFixTestGuardRepoFile(t, root, "scripts/ci_commit_guard.sh", 0o755)
	copyFixTestGuardRepoFile(t, root, "scripts/guard_commit_titles.sh", 0o755)
	base := strings.TrimSpace(runFixTestGuardGitOutput(t, root, "rev-parse", "HEAD"))

	writeFixTestGuardFile(t, root, "internal/app/parser.go", "package app\n\nfunc parse(input string) string {\n\tif input == \"\" {\n\t\treturn \"empty\"\n\t}\n\treturn \"ok\"\n}\n")
	writeFixTestGuardFile(t, root, "internal/app/parser_test.go", "package app\n\nimport \"testing\"\n\nfunc TestParserPanicBug(t *testing.T) {\n\tif got := parse(\"\"); got != \"empty\" {\n\t\tt.Fatalf(\"parse(empty) = %q, want empty\", got)\n\t}\n}\n")
	runFixTestGuardGit(t, root, "add", ".")
	runFixTestGuardGit(t, root, "commit", "-m", "fix: 修复 parser panic")
	head := strings.TrimSpace(runFixTestGuardGitOutput(t, root, "rev-parse", "HEAD"))

	out, err := runCICommitGuard(t, root, map[string]string{
		"GITHUB_EVENT_NAME":   "push",
		"GITHUB_EVENT_BEFORE": base,
		"GITHUB_SHA":          head,
	})
	if err != nil {
		t.Fatalf("ci commit guard failed: %v\n%s", err, out)
	}
	assertOutputContainsAll(t, out, "[ci-commit-guard] Chinese commit title guard: "+base+".."+head, "Chinese commit title guard OK", "[ci-commit-guard] fix-test guard: "+base+".."+head, "fix-test guard OK")
}

func TestCICommitGuardRejectsTitleWithoutChinese(t *testing.T) {
	root := prepareFixTestGuardRepo(t)
	copyFixTestGuardRepoFile(t, root, "scripts/ci_commit_guard.sh", 0o755)
	copyFixTestGuardRepoFile(t, root, "scripts/guard_commit_titles.sh", 0o755)
	base := strings.TrimSpace(runFixTestGuardGitOutput(t, root, "rev-parse", "HEAD"))

	writeFixTestGuardFile(t, root, "docs/readme.md", "docs only\n")
	runFixTestGuardGit(t, root, "add", "docs/readme.md")
	runFixTestGuardGit(t, root, "commit", "-m", "docs: update guide", "-m", "这里有中文正文")
	head := strings.TrimSpace(runFixTestGuardGitOutput(t, root, "rev-parse", "HEAD"))
	shortHead := strings.TrimSpace(runFixTestGuardGitOutput(t, root, "rev-parse", "--short=7", "HEAD"))

	out, err := runCICommitGuard(t, root, map[string]string{
		"GITHUB_EVENT_NAME":   "push",
		"GITHUB_EVENT_BEFORE": base,
		"GITHUB_SHA":          head,
	})
	if err == nil {
		t.Fatalf("ci commit guard succeeded, want failure\n%s", out)
	}
	assertOutputContainsAll(t, out, "[ci-commit-guard] Chinese commit title guard: "+base+".."+head, "commit "+shortHead+" title must contain Chinese text", "title: docs: update guide")
	assertOutputOmitsAll(t, out, "[ci-commit-guard] fix-test guard")
}

func TestCIWorkflowRunsCommitGuard(t *testing.T) {
	workflow := locateFixTestGuardRepoFile(t, ".github/workflows/ci.yml")
	data, err := os.ReadFile(workflow)
	if err != nil {
		t.Fatalf("read ci workflow: %v", err)
	}

	content := string(data)
	assertOutputContainsAll(t, content,
		"commit-guard:",
		"fetch-depth: 0",
		"GITHUB_BASE_SHA: ${{ github.event.pull_request.base.sha }}",
		"GITHUB_HEAD_SHA: ${{ github.event.pull_request.head.sha }}",
		"GITHUB_EVENT_BEFORE: ${{ github.event.before }}",
		"./scripts/ci_commit_guard.sh",
		"needs: commit-guard",
	)
}

func TestPrePushScopesPackageTestsByChangedLanguage(t *testing.T) {
	t.Run("go only runs go package tests", func(t *testing.T) {
		assertPrePushGoOnlyScope(t)
	})

	t.Run("frontend only runs frontend package tests", func(t *testing.T) {
		assertPrePushFrontendOnlyScope(t)
	})

	t.Run("docs only runs no package tests", func(t *testing.T) {
		assertPrePushDocsOnlyScope(t)
	})
}

type prePushScopeFixture struct {
	root    string
	base    string
	logPath string
	binDir  string
}

func newPrePushScopeFixture(t *testing.T) prePushScopeFixture {
	t.Helper()
	root := preparePrePushScopeRepo(t)
	logPath := filepath.Join(t.TempDir(), "hook-scope.log")
	return prePushScopeFixture{
		root:    root,
		base:    strings.TrimSpace(runFixTestGuardGitOutput(t, root, "rev-parse", "HEAD")),
		logPath: logPath,
		binDir:  writePrePushScopeFakeBins(t, logPath),
	}
}

func (f prePushScopeFixture) run(t *testing.T, head string) string {
	t.Helper()
	out, err := runPrePushScopeHook(t, f.root, prePushStdin(f.base, head), f.binDir, f.logPath)
	if err != nil {
		t.Fatalf("pre-push failed: %v\n%s", err, out)
	}
	return out
}

func (f prePushScopeFixture) log(t *testing.T) string {
	t.Helper()
	return readPrePushScopeLog(t, f.logPath)
}

func assertPrePushGoOnlyScope(t *testing.T) {
	t.Helper()
	fixture := newPrePushScopeFixture(t)
	head := commitPrePushGoOnlyChange(t, fixture.root)
	out := fixture.run(t, head)
	assertOutputContainsAll(t, out, "[pre-push] go package tests: ./internal/app", "fake go package test ./internal/app -count=1", "pre-push OK")
	assertOutputOmitsAll(t, out, "frontend package tests")
	log := fixture.log(t)
	assertOutputContainsAll(t, log, "go-test ./internal/app -count=1")
	assertOutputOmitsAll(t, log, "node ", "npx ")
}

func commitPrePushGoOnlyChange(t *testing.T, root string) string {
	t.Helper()
	writeFixTestGuardFile(t, root, "go.mod", "module example.com/prepushscope\n\ngo 1.22\n")
	writeFixTestGuardFile(t, root, "internal/app/app.go", "package app\n\nfunc App() {}\n")
	runFixTestGuardGit(t, root, "add", "go.mod", "internal/app/app.go")
	runFixTestGuardGit(t, root, "commit", "-m", "chore: 更新 app package")
	return strings.TrimSpace(runFixTestGuardGitOutput(t, root, "rev-parse", "HEAD"))
}

func assertPrePushFrontendOnlyScope(t *testing.T) {
	t.Helper()
	fixture := newPrePushScopeFixture(t)
	head := commitPrePushFrontendOnlyChange(t, fixture.root)
	out := fixture.run(t, head)
	assertOutputContainsAll(t, out, "[pre-push] frontend codebase guard", "[pre-push] frontend package tests", "pre-push OK")
	assertOutputOmitsAll(t, out, "go package tests")
	log := fixture.log(t)
	assertOutputContainsAll(t, log, "node scripts/size-guard.cjs", "npx vitest run")
	assertOutputOmitsAll(t, log, "go-test ")
}

func commitPrePushFrontendOnlyChange(t *testing.T, root string) string {
	t.Helper()
	writeFixTestGuardFile(t, root, "cmd/agent-terminal/frontend/scripts/size-guard.cjs", "console.log('ok')\n")
	writeFixTestGuardFile(t, root, "cmd/agent-terminal/frontend/vue-app/app.js", "export const app = true\n")
	runFixTestGuardGit(t, root, "add", "cmd/agent-terminal/frontend")
	runFixTestGuardGit(t, root, "commit", "-m", "chore: 更新 frontend package")
	return strings.TrimSpace(runFixTestGuardGitOutput(t, root, "rev-parse", "HEAD"))
}

func assertPrePushDocsOnlyScope(t *testing.T) {
	t.Helper()
	fixture := newPrePushScopeFixture(t)
	head := commitPrePushDocsOnlyChange(t, fixture.root)
	out := fixture.run(t, head)
	assertOutputOmitsAll(t, out, "go package tests", "frontend package tests")
	assertOutputContainsAll(t, out, "pre-push OK")
	if log := fixture.log(t); log != "" {
		t.Fatalf("package command log should be empty for docs-only push\n%s", log)
	}
}

func commitPrePushDocsOnlyChange(t *testing.T, root string) string {
	t.Helper()
	writeFixTestGuardFile(t, root, "docs/readme.md", "docs only\n")
	runFixTestGuardGit(t, root, "add", "docs/readme.md")
	runFixTestGuardGit(t, root, "commit", "-m", "docs: 更新 guide")
	return strings.TrimSpace(runFixTestGuardGitOutput(t, root, "rev-parse", "HEAD"))
}

func assertOutputContainsAll(t *testing.T, output string, parts ...string) {
	t.Helper()
	for _, want := range parts {
		if !strings.Contains(output, want) {
			t.Fatalf("output missing %q\n%s", want, output)
		}
	}
}

func assertOutputOmitsAll(t *testing.T, output string, parts ...string) {
	t.Helper()
	for _, forbidden := range parts {
		if strings.Contains(output, forbidden) {
			t.Fatalf("output unexpectedly contains %q\n%s", forbidden, output)
		}
	}
}

func prepareFixTestGuardRepo(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "scripts"), 0o755); err != nil {
		t.Fatalf("mkdir scripts: %v", err)
	}
	source := locateFixTestGuardScript(t)
	data, err := os.ReadFile(source)
	if err != nil {
		t.Fatalf("read guard script: %v", err)
	}
	target := filepath.Join(root, "scripts", "guard_fix_commits_have_tests.sh")
	if err := os.WriteFile(target, data, 0o755); err != nil {
		t.Fatalf("copy guard script: %v", err)
	}

	runFixTestGuardGit(t, root, "init", "-q")
	runFixTestGuardGit(t, root, "config", "user.email", "guard@example.test")
	runFixTestGuardGit(t, root, "config", "user.name", "Guard Test")
	writeFixTestGuardFile(t, root, "README.md", "fixture repo\n")
	runFixTestGuardGit(t, root, "add", "README.md")
	runFixTestGuardGit(t, root, "commit", "-m", "chore: init")
	return root
}

func preparePrePushScopeRepo(t *testing.T) string {
	t.Helper()
	root := prepareFixTestGuardRepo(t)
	copyFixTestGuardRepoFile(t, root, ".githooks/pre-push", 0o755)
	copyFixTestGuardRepoFile(t, root, "scripts/guard_commit_titles.sh", 0o755)
	writePrePushFakeGoTestScript(t, root)
	runFixTestGuardGit(t, root, "add", ".githooks/pre-push", "scripts/guard_commit_titles.sh", "scripts/guard_fix_commits_have_tests.sh", "scripts/test_with_guard.sh")
	runFixTestGuardGit(t, root, "commit", "-m", "chore: install pre-push scope fixture")
	return root
}

func writePrePushFakeGoTestScript(t *testing.T, root string) {
	t.Helper()
	content := "#!/usr/bin/env bash\nset -e\nprintf 'go-test %s\\n' \"$*\" >>\"$HOOK_SCOPE_LOG\"\nprintf 'fake go package test %s\\n' \"$*\"\n"
	path := filepath.Join(root, "scripts", "test_with_guard.sh")
	if err := os.WriteFile(path, []byte(content), 0o755); err != nil {
		t.Fatalf("write fake test_with_guard.sh: %v", err)
	}
}

func writePrePushScopeFakeBins(t *testing.T, logPath string) string {
	t.Helper()
	binDir := t.TempDir()
	for name, content := range map[string]string{
		"node": "#!/usr/bin/env bash\nprintf 'node %s\\n' \"$*\" >>\"$HOOK_SCOPE_LOG\"\n",
		"npx":  "#!/usr/bin/env bash\nprintf 'npx %s\\n' \"$*\" >>\"$HOOK_SCOPE_LOG\"\n",
	} {
		path := filepath.Join(binDir, name)
		if err := os.WriteFile(path, []byte(content), 0o755); err != nil {
			t.Fatalf("write fake %s: %v", name, err)
		}
	}
	if err := os.WriteFile(logPath, nil, 0o644); err != nil {
		t.Fatalf("write log file: %v", err)
	}
	return binDir
}

func prePushStdin(base, head string) string {
	return "refs/heads/main " + head + " refs/heads/main " + base + "\n"
}

func runPrePushScopeHook(t *testing.T, root, stdin, binDir, logPath string) (string, error) {
	t.Helper()
	cmd := exec.Command("bash", filepath.Join(".githooks", "pre-push"))
	cmd.Dir = root
	cmd.Stdin = strings.NewReader(stdin)
	cmd.Env = append(os.Environ(),
		"PATH="+binDir+string(os.PathListSeparator)+os.Getenv("PATH"),
		"HOOK_SCOPE_LOG="+logPath,
	)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func runCICommitGuard(t *testing.T, root string, env map[string]string, args ...string) (string, error) {
	t.Helper()
	cmdArgs := append([]string{filepath.Join("scripts", "ci_commit_guard.sh")}, args...)
	cmd := exec.Command("bash", cmdArgs...)
	cmd.Dir = root
	cmd.Env = os.Environ()
	for key, value := range env {
		cmd.Env = append(cmd.Env, key+"="+value)
	}
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func runCommitMsgHook(t *testing.T, root, msgFile string) (string, error) {
	t.Helper()
	cmd := exec.Command("bash", filepath.Join(".githooks", "commit-msg"), msgFile)
	cmd.Dir = root
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func readPrePushScopeLog(t *testing.T, logPath string) string {
	t.Helper()
	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read log: %v", err)
	}
	return string(data)
}

func copyFixTestGuardRepoFile(t *testing.T, root, path string, mode os.FileMode) {
	t.Helper()
	source := locateFixTestGuardRepoFile(t, path)
	data, err := os.ReadFile(source)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	target := filepath.Join(root, filepath.FromSlash(path))
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(target), err)
	}
	if err := os.WriteFile(target, data, mode); err != nil {
		t.Fatalf("copy %s: %v", path, err)
	}
}

func locateFixTestGuardScript(t *testing.T) string {
	t.Helper()
	for _, path := range []string{
		"guard_fix_commits_have_tests.sh",
		filepath.Join("scripts", "guard_fix_commits_have_tests.sh"),
	} {
		if _, err := os.Stat(path); err == nil {
			return path
		}
	}
	t.Fatal("guard_fix_commits_have_tests.sh not found")
	return ""
}

func locateFixTestGuardRepoFile(t *testing.T, path string) string {
	t.Helper()
	for _, candidate := range []string{
		filepath.FromSlash(path),
		filepath.Join("..", filepath.FromSlash(path)),
	} {
		if _, err := os.Stat(candidate); err == nil {
			return candidate
		}
	}
	t.Fatalf("%s not found", path)
	return ""
}

func writeFixTestGuardFile(t *testing.T, root, path, content string) {
	t.Helper()
	full := filepath.Join(root, filepath.FromSlash(path))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(full), err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func runFixTestGuard(t *testing.T, root string, args ...string) (string, error) {
	t.Helper()
	cmdArgs := append([]string{filepath.Join("scripts", "guard_fix_commits_have_tests.sh")}, args...)
	cmd := exec.Command("bash", cmdArgs...)
	cmd.Dir = root
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func runFixTestGuardGit(t *testing.T, root string, args ...string) {
	t.Helper()
	_ = runFixTestGuardGitOutput(t, root, args...)
}

func runFixTestGuardGitOutput(t *testing.T, root string, args ...string) string {
	t.Helper()
	cmdArgs := append([]string{"-c", "core.hooksPath=/dev/null"}, args...)
	cmd := exec.Command("git", cmdArgs...)
	cmd.Dir = root
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s failed: %v\n%s", strings.Join(args, " "), err, string(out))
	}
	return string(out)
}
