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
			name:    "rejects fix with unrelated fixture only",
			subject: "fix: 修复 parser panic",
			files: map[string]string{
				"internal/app/parser.go":               "package app\n\nfunc parse() {}\n",
				"test/fixtures/other/parser_case.json": "{}\n",
			},
			wantErr:   true,
			wantParts: []string{"fix 提交缺少锁定 bug 的测试", "fix: 修复 parser panic"},
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

func TestFixCommitRejectsUnrelatedTestFile(t *testing.T) {
	root := prepareFixTestGuardRepo(t)
	writeFixTestGuardFile(t, root, "internal/app/parser.go", "package app\n\nfunc parse() {}\n")
	writeFixTestGuardFile(t, root, "internal/other/parser_test.go", "package other\n\nimport \"testing\"\n\nfunc TestOtherParser(t *testing.T) {}\n")
	runFixTestGuardGit(t, root, "add", ".")
	msgFile := filepath.Join(root, "COMMIT_EDITMSG")
	if err := os.WriteFile(msgFile, []byte("fix: 修复 parser panic\n"), 0o644); err != nil {
		t.Fatalf("write commit message: %v", err)
	}

	out, err := runFixTestGuard(t, root, "--cached", msgFile)
	if err == nil {
		t.Fatalf("guard succeeded with unrelated direct test; want failure\n%s", out)
	}
	assertOutputContainsAll(t, out, "fix 提交缺少锁定 bug 的测试", "fix: 修复 parser panic")
}

func TestFixCommitAllowsParentPackageRegressionTest(t *testing.T) {
	root := prepareFixTestGuardRepo(t)
	writeFixTestGuardFile(t, root, "internal/module/prompt/intent/commit.go", "package intent\n\nfunc CommitDraft() {}\n")
	writeFixTestGuardFile(t, root, "internal/module/prompt/intent_commit_test.go", "package prompt\n\nimport \"testing\"\n\nfunc TestCommitDraftSerializesIntent(t *testing.T) {}\n")
	runFixTestGuardGit(t, root, "add", ".")

	msgFile := filepath.Join(root, "COMMIT_EDITMSG")
	if err := os.WriteFile(msgFile, []byte("fix(prompt): 修复并发草稿提交\n"), 0o644); err != nil {
		t.Fatalf("write commit message: %v", err)
	}

	out, err := runFixTestGuard(t, root, "--cached", msgFile)
	if err != nil {
		t.Fatalf("guard rejected parent package regression test: %v\n%s", err, out)
	}
	assertOutputContainsAll(t, out, "fix-test guard OK")
}

func TestFixCommitAllowsRepositoryToolingRegressionTest(t *testing.T) {
	root := prepareFixTestGuardRepo(t)
	writeFixTestGuardFile(t, root, ".githooks/pre-commit", "#!/usr/bin/env bash\nexit 0\n")
	writeFixTestGuardFile(t, root, "Makefile", "install-hooks:\n\t@true\n")
	writeFixTestGuardFile(t, root, "scripts/install_hooks_guard_test.go", "package main\n\nimport \"testing\"\n\nfunc TestInstallHooksUsesLinkedWorktreeRoot(t *testing.T) {}\n")
	runFixTestGuardGit(t, root, "add", ".")

	msgFile := filepath.Join(root, "COMMIT_EDITMSG")
	if err := os.WriteFile(msgFile, []byte("fix: 修复 hooks 工作树路径\n"), 0o644); err != nil {
		t.Fatalf("write commit message: %v", err)
	}

	out, err := runFixTestGuard(t, root, "--cached", msgFile)
	if err != nil {
		t.Fatalf("guard rejected repository tooling regression test: %v\n%s", err, out)
	}
	assertOutputContainsAll(t, out, "fix-test guard OK")
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
	cmd := exec.Command("bash", bashPath(".githooks", "pre-push"))
	cmd.Dir = root
	cmd.Stdin = strings.NewReader(stdin)
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("pre-push succeeded, want failure\n%s", string(out))
	}
	for _, part := range []string{"[pre-push] Chinese commit message guard", "Chinese commit message guard OK", "[pre-push] fix-test guard", "fix commit 缺少锁定 bug 的测试", "fix: 修复 parser panic"} {
		if !strings.Contains(string(out), part) {
			t.Fatalf("pre-push output missing %q\n%s", part, string(out))
		}
	}
}

func TestFixTestGuardSkipsMergeCommitSubjects(t *testing.T) {
	root := prepareFixTestGuardRepo(t)
	writeFixTestGuardFile(t, root, "internal/app/parser.go", "package app\n\nfunc parse(input string) string { return input }\n")
	writeFixTestGuardFile(t, root, "internal/app/parser_test.go", "package app\n\nimport \"testing\"\n\nfunc TestParserPanicBug(t *testing.T) {}\n")
	runFixTestGuardGit(t, root, "add", ".")
	runFixTestGuardGit(t, root, "commit", "-m", "fix: 修复 parser panic")
	featureHead := strings.TrimSpace(runFixTestGuardGitOutput(t, root, "rev-parse", "HEAD"))

	runFixTestGuardGit(t, root, "checkout", "-b", "side", "HEAD~1")
	writeFixTestGuardFile(t, root, "docs/side.md", "side\n")
	runFixTestGuardGit(t, root, "add", "docs/side.md")
	runFixTestGuardGit(t, root, "commit", "-m", "docs: 更新 side")
	runFixTestGuardGit(t, root, "merge", "--no-ff", featureHead, "-m", "fix: 修复 parser panic (#1)")

	out, err := runFixTestGuard(t, root, "--range", "HEAD~2..HEAD")
	if err != nil {
		t.Fatalf("guard rejected fix-title merge commit even though child fix carried a test: %v\n%s", err, out)
	}
	assertOutputContainsAll(t, out, "fix-test guard OK")
}

func TestPreCommitRunsCodeGuardForDocsOnlyCommit(t *testing.T) {
	root := prepareFixTestGuardRepo(t)
	copyFixTestGuardRepoFile(t, root, ".githooks/pre-commit", 0o755)
	writePreCommitFakeCodeGuardScript(t, root)
	runFixTestGuardGit(t, root, "add", ".githooks/pre-commit", "scripts/test_with_guard.sh")
	runFixTestGuardGit(t, root, "commit", "-m", "chore: 安装 precommit fixture")

	writeFixTestGuardFile(t, root, "docs/readme.md", "docs only\n")
	runFixTestGuardGit(t, root, "add", "docs/readme.md")

	out, err := runPreCommitHook(t, root)
	if err != nil {
		t.Fatalf("pre-commit failed: %v\n%s", err, out)
	}
	assertOutputContainsAll(t, out, "[pre-commit] full codebase guard", "fake code guard --guard-only", "pre-commit OK")
	assertOutputOmitsAll(t, out, "go vet", "frontend-app tests")
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
		assertOutputContainsAll(t, out, "[commit-msg] Chinese commit message guard", "commit title must contain Chinese text", "title: docs: update guide")
		assertOutputOmitsAll(t, out, "[commit-msg] fix-test guard")
	})

	t.Run("rejects english title with spaces", func(t *testing.T) {
		msgFile := filepath.Join(root, "COMMIT_EDITMSG")
		if err := os.WriteFile(msgFile, []byte("refactor: split orch tool definitions\n"), 0o644); err != nil {
			t.Fatalf("write commit message: %v", err)
		}

		out, err := runCommitMsgHook(t, root, msgFile)
		if err == nil {
			t.Fatalf("commit-msg succeeded, want failure\n%s", out)
		}
		assertOutputContainsAll(t, out, "[commit-msg] Chinese commit message guard", "commit title must contain Chinese text", "title: refactor: split orch tool definitions")
		assertOutputOmitsAll(t, out, "[commit-msg] fix-test guard")
	})

	t.Run("rejects body without chinese when present", func(t *testing.T) {
		msgFile := filepath.Join(root, "COMMIT_EDITMSG")
		if err := os.WriteFile(msgFile, []byte("docs: 更新 guide\n\nEnglish body only\n"), 0o644); err != nil {
			t.Fatalf("write commit message: %v", err)
		}

		out, err := runCommitMsgHook(t, root, msgFile)
		if err == nil {
			t.Fatalf("commit-msg succeeded, want failure\n%s", out)
		}
		assertOutputContainsAll(t, out, "[commit-msg] Chinese commit message guard", "commit body must contain Chinese text when present", "body: English body only")
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
		assertOutputContainsAll(t, out, "[commit-msg] Chinese commit message guard", "Chinese commit message guard OK", "[commit-msg] fix-test guard")
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
	cmd := exec.Command("bash", bashPath(".githooks", "pre-push"))
	cmd.Dir = root
	cmd.Stdin = strings.NewReader(stdin)
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("pre-push succeeded, want failure\n%s", string(out))
	}
	assertOutputContainsAll(t, string(out), "[pre-push] Chinese commit message guard", "commit "+shortHead+" title must contain Chinese text", "title: docs: update guide")
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
	assertOutputContainsAll(t, out, "[ci-commit-guard] Chinese commit message guard: "+base+".."+head, "Chinese commit message guard OK", "[ci-commit-guard] fix-test guard: "+base+".."+head, "fix commit 缺少锁定 bug 的测试", "fix: 修复 parser panic")
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
	assertOutputContainsAll(t, out, "[ci-commit-guard] Chinese commit message guard: "+base+".."+head, "Chinese commit message guard OK", "[ci-commit-guard] fix-test guard: "+base+".."+head, "fix-test guard OK")
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
	assertOutputContainsAll(t, out, "[ci-commit-guard] Chinese commit message guard: "+base+".."+head, "commit "+shortHead+" title must contain Chinese text", "title: docs: update guide")
	assertOutputOmitsAll(t, out, "[ci-commit-guard] fix-test guard")
}

func TestCICommitGuardRejectsBodyWithoutChinese(t *testing.T) {
	root := prepareFixTestGuardRepo(t)
	copyFixTestGuardRepoFile(t, root, "scripts/ci_commit_guard.sh", 0o755)
	copyFixTestGuardRepoFile(t, root, "scripts/guard_commit_titles.sh", 0o755)
	base := strings.TrimSpace(runFixTestGuardGitOutput(t, root, "rev-parse", "HEAD"))

	writeFixTestGuardFile(t, root, "docs/readme.md", "docs only\n")
	runFixTestGuardGit(t, root, "add", "docs/readme.md")
	runFixTestGuardGit(t, root, "commit", "-m", "docs: 更新 guide", "-m", "English body only")
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
	assertOutputContainsAll(t, out, "[ci-commit-guard] Chinese commit message guard: "+base+".."+head, "commit "+shortHead+" body must contain Chinese text when present", "body: English body only")
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

func TestCIWorkflowDoesNotExcludeProductionPackages(t *testing.T) {
	workflow := locateFixTestGuardRepoFile(t, ".github/workflows/ci.yml")
	data, err := os.ReadFile(workflow)
	if err != nil {
		t.Fatalf("read ci workflow: %v", err)
	}
	content := string(data)
	for _, forbidden := range []string{"grep -v", "/internal/provider/dreamexec"} {
		if strings.Contains(content, forbidden) {
			t.Fatalf("ci workflow contains production package exclusion %q:\n%s", forbidden, content)
		}
	}
}

func TestPrePushScopesPackageTestsByChangedLanguage(t *testing.T) {
	t.Run("go only runs go package tests", func(t *testing.T) {
		assertPrePushGoOnlyScope(t)
	})

	t.Run("frontend app only runs frontend app package tests", func(t *testing.T) {
		assertPrePushFrontendAppOnlyScope(t)
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
