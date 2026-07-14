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

func TestFixCommitAllowsMJSFrontendRegressionTest(t *testing.T) {
	root := prepareFixTestGuardRepo(t)
	writeFixTestGuardFile(t, root, "frontend-app/scripts/ui-test-mcp-server.mjs", "export function navigate() { return true }\n")
	writeFixTestGuardFile(t, root, "frontend-app/scripts/ui-test-mcp-server.test.mjs", "import { test } from 'vitest';\ntest('navigate waits for harness', () => {});\n")
	runFixTestGuardGit(t, root, "add", ".")

	msgFile := filepath.Join(root, "COMMIT_EDITMSG")
	if err := os.WriteFile(msgFile, []byte("fix: 修复 UI MCP 导航等待\n"), 0o644); err != nil {
		t.Fatalf("write commit message: %v", err)
	}

	out, err := runFixTestGuard(t, root, "--cached", msgFile)
	if err != nil {
		t.Fatalf("guard rejected mjs frontend regression test: %v\n%s", err, out)
	}
	assertOutputContainsAll(t, out, "fix-test guard OK")
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
	copyFixTestGuardRepoFile(t, root, "scripts/configure_hook_node_runtime.sh", 0o755)
	copyFixTestGuardRepoFile(t, root, "scripts/guard_commit_titles.sh", 0o755)
	runFixTestGuardGit(t, root, "add", ".githooks/pre-push", "scripts/configure_hook_node_runtime.sh", "scripts/guard_commit_titles.sh", "scripts/guard_fix_commits_have_tests.sh")
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

func TestPreCommitRoutesDocsOnlyThroughCachedAIMaintenance(t *testing.T) {
	root := prepareFixTestGuardRepo(t)
	copyFixTestGuardRepoFile(t, root, ".githooks/pre-commit", 0o755)
	copyFixTestGuardRepoFile(t, root, "scripts/configure_hook_node_runtime.sh", 0o755)
	copyFixTestGuardRepoFile(t, root, "scripts/refresh_generated_artifacts.sh", 0o755)
	writePreCommitFakeCodeGuardScript(t, root)
	writeFakeAIMaintenanceGateScript(t, root)
	writePreCommitFakeCodemapMakefile(t, root)
	runFixTestGuardGit(t, root, "add", ".githooks/pre-commit", "scripts/configure_hook_node_runtime.sh", "scripts/refresh_generated_artifacts.sh", "scripts/test_with_guard.sh", "scripts/ai_maintenance_gates.sh", "Makefile")
	runFixTestGuardGit(t, root, "commit", "-m", "chore: 安装 precommit fixture")

	writeFixTestGuardFile(t, root, "docs/readme.md", "docs only\n")
	runFixTestGuardGit(t, root, "add", "docs/readme.md")

	out, err := runPreCommitHook(t, root)
	if err != nil {
		t.Fatalf("pre-commit failed: %v\n%s", err, out)
	}
	assertOutputContainsAll(t, out,
		"[pre-commit] codemap refresh",
		"[generated] refresh codemap artifacts",
		"[generated] refresh AI project map",
		"[pre-commit] AI maintenance gates",
		"--cache-dir .build-cache/ai-maintenance-gates",
		"--cache-max-age 10m",
		"--cache-scope",
		"--diff-cached",
		"gate-index=",
		"pre-commit OK",
	)
	stagedTree := strings.TrimSpace(runFixTestGuardGitOutput(t, root, "write-tree"))
	assertOutputContainsAll(t, out, "gate-tree="+stagedTree)
	assertOutputOmitsAll(t, out, "full codebase guard", "fake code guard", "go vet", "frontend-app tests")
}

func TestPreCommitStagesRefreshedCodemapFiles(t *testing.T) {
	root := prepareFixTestGuardRepo(t)
	copyFixTestGuardRepoFile(t, root, ".githooks/pre-commit", 0o755)
	copyFixTestGuardRepoFile(t, root, "scripts/configure_hook_node_runtime.sh", 0o755)
	copyFixTestGuardRepoFile(t, root, "scripts/refresh_generated_artifacts.sh", 0o755)
	writePreCommitFakeCodeGuardScript(t, root)
	writeFakeAIMaintenanceGateScript(t, root)
	writePreCommitFakeCodemapMakefile(t, root)
	runFixTestGuardGit(t, root, "add", ".githooks/pre-commit", "scripts/configure_hook_node_runtime.sh", "scripts/refresh_generated_artifacts.sh", "scripts/test_with_guard.sh", "scripts/ai_maintenance_gates.sh", "Makefile")
	runFixTestGuardGit(t, root, "commit", "-m", "chore: 安装 precommit fixture")

	writeFixTestGuardFile(t, root, "docs/readme.md", "docs only\n")
	runFixTestGuardGit(t, root, "add", "docs/readme.md")

	out, err := runPreCommitHook(t, root)
	if err != nil {
		t.Fatalf("pre-commit failed: %v\n%s", err, out)
	}
	assertOutputContainsAll(t, out, "[pre-commit] codemap refresh", "[generated] refresh codemap artifacts", "[generated] refresh AI project map", "[pre-commit] AI maintenance gates", "pre-commit OK")

	cached := runFixTestGuardGitOutput(t, root, "diff", "--cached", "--name-only")
	assertOutputContainsAll(t, cached,
		"README.md",
		"docs/doc/codemap/13-archtest-boundaries.md",
		"docs/doc/codemap/README.md",
		"docs/doc/codemap/ai-index.json",
		"docs/doc/codemap/project-map/AI_PROJECT_MAP.md",
		"docs/doc/codemap/project-map/index/other.tsv",
	)

	stagedMap := runFixTestGuardGitOutput(t, root, "show", ":docs/doc/codemap/project-map/AI_PROJECT_MAP.md")
	assertOutputContainsAll(t, stagedMap, "project map refreshed")
	stagedREADME := runFixTestGuardGitOutput(t, root, "show", ":README.md")
	assertOutputContainsAll(t, stagedREADME, "root readme refreshed")
	stagedArchtestMap := runFixTestGuardGitOutput(t, root, "show", ":docs/doc/codemap/13-archtest-boundaries.md")
	assertOutputContainsAll(t, stagedArchtestMap, "archtest map refreshed")
}

func TestPreCommitRejectsNonGoStagedWorktreeMismatch(t *testing.T) {
	root := preparePreCommitGateFixture(t)
	writeFixTestGuardFile(t, root, "frontend-app/src/App.jsx", "export const App = () => 'staged'\n")
	runFixTestGuardGit(t, root, "add", "frontend-app/src/App.jsx")
	writeFixTestGuardFile(t, root, "frontend-app/src/App.jsx", "export const App = () => 'worktree'\n")

	out, err := runPreCommitHook(t, root)
	if err == nil {
		t.Fatalf("pre-commit accepted a non-Go staged/worktree mismatch:\n%s", out)
	}
	assertOutputContainsAll(t, out, "当前代码/门禁提交还存在未暂存或未跟踪 worktree 输入", "frontend-app/src/App.jsx", "可能制造假绿")
}

func TestPreCommitRejectsRenamedPathWithWorktreeMismatch(t *testing.T) {
	root := preparePreCommitGateFixture(t)
	writeFixTestGuardFile(t, root, "docs/original.md", "original\n")
	runFixTestGuardGit(t, root, "add", "docs/original.md")
	runFixTestGuardGit(t, root, "commit", "-m", "docs: 添加重命名样例")
	runFixTestGuardGit(t, root, "mv", "docs/original.md", "docs/renamed.md")
	writeFixTestGuardFile(t, root, "docs/renamed.md", "unstaged replacement\n")

	out, err := runPreCommitHook(t, root)
	if err == nil {
		t.Fatalf("pre-commit accepted a renamed staged/worktree mismatch:\n%s", out)
	}
	assertOutputContainsAll(t, out, "当前代码/门禁提交还存在未暂存或未跟踪 worktree 输入", "docs/renamed.md", "可能制造假绿")
}

func TestPreCommitAcceptsCleanStagedDeletion(t *testing.T) {
	root := preparePreCommitGateFixture(t)
	writeFixTestGuardFile(t, root, "docs/deleted.md", "delete me\n")
	runFixTestGuardGit(t, root, "add", "docs/deleted.md")
	runFixTestGuardGit(t, root, "commit", "-m", "docs: 添加删除样例")
	runFixTestGuardGit(t, root, "rm", "docs/deleted.md")

	out, err := runPreCommitHook(t, root)
	if err != nil {
		t.Fatalf("pre-commit rejected a clean staged deletion: %v\n%s", err, out)
	}
	assertOutputContainsAll(t, out, "--changed-file docs/deleted.md", "pre-commit OK")
}

func TestPreCommitAcceptsCleanBrokenSymlink(t *testing.T) {
	root := preparePreCommitGateFixture(t)
	symlink := filepath.Join(root, "docs", "broken-link.md")
	if err := os.MkdirAll(filepath.Dir(symlink), 0o755); err != nil {
		t.Fatalf("create symlink directory: %v", err)
	}
	if err := os.Symlink("missing-target.md", symlink); err != nil {
		t.Fatalf("create broken symlink: %v", err)
	}
	runFixTestGuardGit(t, root, "add", "docs/broken-link.md")

	out, err := runPreCommitHook(t, root)
	if err != nil {
		t.Fatalf("pre-commit rejected a clean staged broken symlink: %v\n%s", err, out)
	}
	assertOutputContainsAll(t, out, "--changed-file docs/broken-link.md", "pre-commit OK")
}

func TestPreCommitRejectsPartialIndexMismatch(t *testing.T) {
	root := preparePreCommitGateFixture(t)
	writeFixTestGuardFile(t, root, "docs/readme.md", "partial index\n")
	runFixTestGuardGit(t, root, "add", "docs/readme.md")
	realIndex := filepath.Join(root, ".git", "index")
	altIndex := filepath.Join(t.TempDir(), "partial-index")
	data, err := os.ReadFile(realIndex)
	if err != nil {
		t.Fatalf("read real index: %v", err)
	}
	if err := os.WriteFile(altIndex, data, 0o600); err != nil {
		t.Fatalf("write partial index: %v", err)
	}
	writeFixTestGuardFile(t, root, "real-index-only.txt", "real index\n")
	runFixTestGuardGit(t, root, "add", "real-index-only.txt")

	out, err := runPreCommitHookWithEnv(t, root, map[string]string{"GIT_INDEX_FILE": altIndex})
	if err == nil {
		t.Fatalf("pre-commit accepted a partial index mismatch:\n%s", out)
	}
	assertOutputContainsAll(t, out, "partial commit 临时 index 与真实 index 不一致", "无法证明提交内容为绿")
}

func TestPreCommitRunsLongGatesFromStagedSnapshot(t *testing.T) {
	root := preparePreCommitGateFixture(t)
	writeFixTestGuardFile(t, root, ".gitignore", "frontend-app/node_modules/\n")
	writeFixTestGuardFile(t, root, "frontend-app/package.json", "{}\n")
	writeFixTestGuardFile(t, root, "frontend-app/package-lock.json", "{}\n")
	vitePath := filepath.Join(root, "frontend-app", "node_modules", ".bin", "vite")
	if err := os.MkdirAll(filepath.Dir(vitePath), 0o755); err != nil {
		t.Fatalf("mkdir fake frontend dependencies: %v", err)
	}
	if err := os.WriteFile(vitePath, []byte("#!/usr/bin/env bash\n"), 0o755); err != nil {
		t.Fatalf("write fake vite: %v", err)
	}
	runFixTestGuardGit(t, root, "add", ".gitignore", "frontend-app/package.json", "frontend-app/package-lock.json", "scripts/guard_fix_commits_have_tests.sh")
	runFixTestGuardGit(t, root, "commit", "-m", "chore: 纳入 fixture 守卫脚本")
	path := ".githooks/snapshot-input.sh"
	writeFixTestGuardFile(t, root, path, "staged snapshot\n")
	runFixTestGuardGit(t, root, "add", path)

	out, err := runPreCommitHookWithEnv(t, root, map[string]string{
		"GATE_MUTATE_ORIGINAL_PATH":     filepath.Join(root, path),
		"GATE_ASSERT_RELATIVE_PATH":     path,
		"GATE_ASSERT_CONTENT":           "staged snapshot",
		"GATE_ASSERT_NODE_MODULES_COPY": "1",
		"GATE_ASSERT_WORKTREE_INDEX":    "1",
	})
	if err != nil {
		t.Fatalf("pre-commit staged snapshot failed: %v\n%s", err, out)
	}
	assertOutputContainsAll(t, out, "gate-worktree=", "cache-tree=", "worktree-tree=", "pre-commit OK")
	if strings.Contains(out, "gate-worktree="+root) {
		t.Fatalf("AI gate ran in mutable original worktree:\n%s", out)
	}
	data, readErr := os.ReadFile(filepath.Join(root, path))
	if readErr != nil {
		t.Fatalf("read mutated original input: %v", readErr)
	}
	if !strings.Contains(string(data), "mutated during gate") {
		t.Fatalf("fixture did not mutate original worktree during gate: %q", data)
	}
	staged := runFixTestGuardGitOutput(t, root, "show", ":"+path)
	assertOutputContainsAll(t, staged, "staged snapshot")
}

func TestPreCommitChecksGoFormattingInsideStagedSnapshot(t *testing.T) {
	root := preparePreCommitGateFixture(t)
	writeFixTestGuardFile(t, root, "go.mod", "module example.com/precommit\n\ngo 1.24\n")
	runFixTestGuardGit(t, root, "add", "go.mod", "scripts/guard_fix_commits_have_tests.sh")
	runFixTestGuardGit(t, root, "commit", "-m", "chore: 安装 Go fixture")
	writeFixTestGuardFile(t, root, "internal/app/unformatted.go", "package app\n\nfunc unformatted( ){ }\n")
	runFixTestGuardGit(t, root, "add", "internal/app/unformatted.go")

	out, err := runPreCommitHook(t, root)
	if err == nil {
		t.Fatalf("pre-commit accepted unformatted staged Go source:\n%s", out)
	}
	assertOutputContainsAll(t, out, "gofmt (staged snapshot)", "以下 staged Go 文件未格式化", "internal/app/unformatted.go")
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
	copyFixTestGuardRepoFile(t, root, "scripts/configure_hook_node_runtime.sh", 0o755)
	copyFixTestGuardRepoFile(t, root, "scripts/guard_commit_titles.sh", 0o755)
	runFixTestGuardGit(t, root, "add", ".githooks/pre-push", "scripts/configure_hook_node_runtime.sh", "scripts/guard_commit_titles.sh", "scripts/guard_fix_commits_have_tests.sh")
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

	t.Run("deferred E2E packages are excluded", func(t *testing.T) {
		assertPrePushExcludesDeferredE2EScope(t)
	})

	t.Run("frontend app only runs frontend app package tests", func(t *testing.T) {
		assertPrePushFrontendAppOnlyScope(t)
	})

	t.Run("docs only runs no package tests", func(t *testing.T) {
		assertPrePushDocsOnlyScope(t)
	})
}

func TestPrePushAllowsDirtyLocalWorktree(t *testing.T) {
	fixture := newPrePushScopeFixture(t)
	head := commitPrePushDocsOnlyChange(t, fixture.root)

	writeFixTestGuardFile(t, fixture.root, "README.md", "fixture repo\nlocal unstaged edit\n")
	writeFixTestGuardFile(t, fixture.root, "local-staged.txt", "local staged edit\n")
	runFixTestGuardGit(t, fixture.root, "add", "local-staged.txt")
	writeFixTestGuardFile(t, fixture.root, "local-untracked.txt", "local untracked edit\n")

	out := fixture.run(t, head)
	assertOutputContainsAll(t, out, "[pre-push] Chinese commit message guard", "[pre-push] fix-test guard", "pre-push OK")
	assertOutputOmitsAll(t, out, "worktree 有未暂存改动", "index 有已暂存但未提交改动", "worktree 有未跟踪文件")
}

func TestPrePushNewBranchRoutesAllReachableTreeChanges(t *testing.T) {
	fixture := newPrePushScopeFixture(t)
	writeFixTestGuardFile(t, fixture.root, "scripts/guard_commit_titles.sh", "#!/usr/bin/env bash\nexit 0\n")
	if err := os.Chmod(filepath.Join(fixture.root, "scripts", "guard_commit_titles.sh"), 0o755); err != nil {
		t.Fatalf("chmod fake title guard: %v", err)
	}
	runFixTestGuardGit(t, fixture.root, "add", "scripts/guard_commit_titles.sh")
	runFixTestGuardGit(t, fixture.root, "commit", "-m", "chore: 安装新分支标题 fixture")
	writeFixTestGuardFile(t, fixture.root, "internal/app/early.go", "package app\n")
	runFixTestGuardGit(t, fixture.root, "add", "internal/app/early.go")
	runFixTestGuardGit(t, fixture.root, "commit", "-m", "chore: 添加早期后端变更")
	writeFixTestGuardFile(t, fixture.root, "docs/late.md", "late docs\n")
	runFixTestGuardGit(t, fixture.root, "add", "docs/late.md")
	runFixTestGuardGit(t, fixture.root, "commit", "-m", "docs: 添加末尾文档")
	head := strings.TrimSpace(runFixTestGuardGitOutput(t, fixture.root, "rev-parse", "HEAD"))
	zeroSHA := strings.Repeat("0", 40)

	out, err := runPrePushScopeHook(t, fixture.root, prePushStdin(zeroSHA, head), fixture.binDir, fixture.logPath)
	if err != nil {
		t.Fatalf("new-branch pre-push failed: %v\n%s", err, out)
	}
	log := fixture.log(t)
	assertOutputContainsAll(t, log, "--changed-file internal/app/early.go", "--changed-file docs/late.md")
}

type prePushScopeFixture struct {
	root    string
	base    string
	logPath string
	binDir  string
}
