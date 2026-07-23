package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"
)

const fixTestGuardGitTimeout = 5 * time.Second

const commitTitleEnforcementBaselinePath = "scripts/commit_title_enforcement_baseline.txt"

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
	assertOutputContainsAll(t, out, "[pre-push] AI maintenance gates", "pre-push OK")
	assertOutputOmitsAll(t, out, "frontend-app tests")
	log := fixture.log(t)
	assertOutputContainsAll(t, log, "ai-maintenance --skip-deferred-e2e --changed-file go.mod --changed-file internal/app/app.go")
	assertOutputOmitsAll(t, log, "go-test ", "node ", "npx ", "npm ")
}

// markPrePushFixtureGoModChanged 保留 canonical 依赖，只让 go.mod 出现在待推送变更范围内。
func markPrePushFixtureGoModChanged(t *testing.T, root string) {
	t.Helper()
	path := filepath.Join(root, "go.mod")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read fixture go.mod: %v", err)
	}
	writeFixTestGuardFile(t, root, "go.mod", string(data)+"\n// pre-push scope fixture change\n")
}

func commitPrePushGoOnlyChange(t *testing.T, root string) string {
	t.Helper()
	markPrePushFixtureGoModChanged(t, root)
	writeFixTestGuardFile(t, root, "internal/app/app.go", "package app\n\nfunc App() {}\n")
	runFixTestGuardGit(t, root, "add", "go.mod", "internal/app/app.go")
	runFixTestGuardGit(t, root, "commit", "-m", "chore: 更新 app package")
	return strings.TrimSpace(runFixTestGuardGitOutput(t, root, "rev-parse", "HEAD"))
}

func assertPrePushExcludesDeferredE2EScope(t *testing.T) {
	t.Helper()
	fixture := newPrePushScopeFixture(t)
	head := commitPrePushMixedFastAndDeferredE2EChange(t, fixture.root)
	out := fixture.run(t, head)
	assertOutputContainsAll(t, out, "[pre-push] AI maintenance gates", "pre-push OK")
	log := fixture.log(t)
	assertOutputContainsAll(t, log, "ai-maintenance --skip-deferred-e2e --changed-file go.mod --changed-file internal/app/app.go --changed-file internal/provider/claudecli/provider.go --changed-file internal/provider/codexapp/provider.go")
	assertOutputOmitsAll(t, log, "go-test ")
}

func commitPrePushMixedFastAndDeferredE2EChange(t *testing.T, root string) string {
	t.Helper()
	markPrePushFixtureGoModChanged(t, root)
	writeFixTestGuardFile(t, root, "internal/app/app.go", "package app\n\nfunc App() {}\n")
	writeFixTestGuardFile(t, root, "internal/provider/claudecli/provider.go", "package claudecli\n")
	writeFixTestGuardFile(t, root, "internal/provider/codexapp/provider.go", "package codexapp\n")
	runFixTestGuardGit(t, root, "add", "go.mod", "internal/app/app.go", "internal/provider/claudecli/provider.go", "internal/provider/codexapp/provider.go")
	runFixTestGuardGit(t, root, "commit", "-m", "chore: 更新 provider package")
	return strings.TrimSpace(runFixTestGuardGitOutput(t, root, "rev-parse", "HEAD"))
}

func assertPrePushFrontendAppOnlyScope(t *testing.T) {
	t.Helper()
	fixture := newPrePushScopeFixture(t)
	head := commitPrePushFrontendAppOnlyChange(t, fixture.root)
	out := fixture.run(t, head)
	assertOutputContainsAll(t, out, "[pre-push] AI maintenance gates", "pre-push OK")
	assertOutputOmitsAll(t, out, "go package tests")
	log := fixture.log(t)
	assertOutputContainsAll(t, log, "ai-maintenance --skip-deferred-e2e --changed-file frontend-app/package.json --changed-file frontend-app/src/App.jsx")
	assertOutputOmitsAll(t, log, "npm ", "go-test ", "node ", "npx ")
}

func commitPrePushFrontendAppOnlyChange(t *testing.T, root string) string {
	t.Helper()
	writeFixTestGuardFile(t, root, "frontend-app/package.json", "{}\n")
	writeFixTestGuardFile(t, root, "frontend-app/src/App.jsx", "export const App = () => null\n")
	runFixTestGuardGit(t, root, "add", "frontend-app")
	runFixTestGuardGit(t, root, "commit", "-m", "chore: 更新 frontend app")
	return strings.TrimSpace(runFixTestGuardGitOutput(t, root, "rev-parse", "HEAD"))
}

func assertPrePushDocsOnlyScope(t *testing.T) {
	t.Helper()
	fixture := newPrePushScopeFixture(t)
	head := commitPrePushDocsOnlyChange(t, fixture.root)
	out := fixture.run(t, head)
	assertOutputOmitsAll(t, out, "go package tests", "frontend-app tests")
	assertOutputContainsAll(t, out, "[pre-push] AI maintenance gates", "pre-push OK")
	log := fixture.log(t)
	assertOutputContainsAll(t, log, "ai-maintenance --skip-deferred-e2e --changed-file docs/readme.md")
	assertOutputOmitsAll(t, log, "go-test", "npm ", "node ", "npx ")
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
	copyFixTestGuardRepoFile(t, root, "scripts/configure_hook_node_runtime.sh", 0o755)
	copyFixTestGuardRepoFile(t, root, "go.mod", 0o644)
	copyCommitTitleGuard(t, root, "")
	writePrePushFakeGoTestScript(t, root)
	writeFakeAIMaintenanceGateScript(t, root)
	copyFixTestGuardRepoFile(t, root, "scripts/ai_maintenance/deferred_e2e_packages.txt", 0o644)
	runFixTestGuardGit(t, root, "add", ".githooks/pre-push", "scripts/configure_hook_node_runtime.sh", "go.mod", "scripts/guard_commit_titles.sh", commitTitleEnforcementBaselinePath, "scripts/guard_fix_commits_have_tests.sh", "scripts/test_with_guard.sh", "scripts/ai_maintenance_gates.sh", "scripts/ai_maintenance/deferred_e2e_packages.txt")
	runFixTestGuardGit(t, root, "commit", "-m", "chore: install pre-push scope fixture")
	return root
}

func writePrePushFakeGoTestScript(t *testing.T, root string) {
	t.Helper()
	content := "#!/usr/bin/env bash\nset -e\nprintf 'go-test %s skip-gosec=%s\\n' \"$*\" \"${SUPER_DOLPHIN_GITHOOK_SKIP_GOSEC:-}\" >>\"$HOOK_SCOPE_LOG\"\nprintf 'fake go package test %s\\n' \"$*\"\n"
	path := filepath.Join(root, "scripts", "test_with_guard.sh")
	if err := os.WriteFile(path, []byte(content), 0o755); err != nil {
		t.Fatalf("write fake test_with_guard.sh: %v", err)
	}
}

func writePreCommitFakeCodeGuardScript(t *testing.T, root string) {
	t.Helper()
	content := "#!/usr/bin/env bash\nset -e\nprintf 'fake code guard %s skip-gosec=%s\\n' \"$*\" \"${SUPER_DOLPHIN_GITHOOK_SKIP_GOSEC:-}\"\nif [ \"$*\" != \"--guard-only\" ]; then\n  echo \"unexpected guard args: $*\" >&2\n  exit 1\nfi\n"
	path := filepath.Join(root, "scripts", "test_with_guard.sh")
	if err := os.WriteFile(path, []byte(content), 0o755); err != nil {
		t.Fatalf("write fake test_with_guard.sh: %v", err)
	}
}

func preparePreCommitGateFixture(t *testing.T) string {
	t.Helper()
	root := prepareFixTestGuardRepo(t)
	copyFixTestGuardRepoFile(t, root, ".githooks/pre-commit", 0o755)
	copyFixTestGuardRepoFile(t, root, "scripts/configure_hook_node_runtime.sh", 0o755)
	copyFixTestGuardRepoFile(t, root, "scripts/refresh_generated_artifacts.sh", 0o755)
	writePreCommitFakeCodeGuardScript(t, root)
	writeFakeAIMaintenanceGateScript(t, root)
	writePreCommitFakeAIMaintenancePlanner(t, root)
	writePreCommitFakeCodemapMakefile(t, root)
	writeFixTestGuardFile(t, root, ".gitignore", ".build-cache/\n")
	runFixTestGuardGit(t, root, "add", ".githooks/pre-commit", ".gitignore", "scripts/configure_hook_node_runtime.sh", "scripts/refresh_generated_artifacts.sh", "scripts/test_with_guard.sh", "scripts/guard_fix_commits_have_tests.sh", "scripts/ai_maintenance_gates.sh", "scripts/ai_maintenance/main.go", "go.mod", "Makefile")
	runFixTestGuardGit(t, root, "commit", "-m", "chore: 安装 precommit fixture")
	return root
}

func writeFakeAIMaintenanceGateScript(t *testing.T, root string) {
	t.Helper()
	content := `#!/usr/bin/env bash
set -e
printf 'fake ai maintenance gate %s\n' "$*"
printf 'gate-worktree=%s\n' "$PWD"
printf 'gate-head=%s\n' "$(git rev-parse HEAD)"
printf 'gate-tree=%s\n' "$(git rev-parse 'HEAD^{tree}')"
printf 'gate-head-parents=%s\n' "$(git rev-list --parents -n 1 HEAD)"
gate_status=$(git status --porcelain=v1)
printf 'gate-status=%s\n' "$gate_status"
if [ -n "${GATE_ASSERT_CLEAN:-}" ] && [ -n "$gate_status" ]; then
  echo "gate worktree is not clean: $gate_status" >&2
  exit 1
fi
if [ -n "${GATE_MUTATE_ORIGINAL_PATH:-}" ]; then
  printf 'mutated during gate\n' >"$GATE_MUTATE_ORIGINAL_PATH"
fi
if [ -n "${GATE_ASSERT_RELATIVE_PATH:-}" ]; then
  grep -Fq "${GATE_ASSERT_CONTENT:?}" "$GATE_ASSERT_RELATIVE_PATH"
fi
if [ -n "${GIT_INDEX_FILE:-}" ]; then
  printf 'gate-index=%s gate-index-tree=%s\n' "$GIT_INDEX_FILE" "$(git write-tree)"
fi
if [ -n "${GATE_ASSERT_WORKTREE_INDEX:-}" ]; then
  cache_tree=$(git write-tree)
  worktree_tree=$(unset GIT_INDEX_FILE; git write-tree)
  printf 'cache-tree=%s worktree-tree=%s\n' "$cache_tree" "$worktree_tree"
  [ "$cache_tree" = "$worktree_tree" ]
fi
if [ -n "${GATE_ASSERT_NODE_MODULES_COPY:-}" ]; then
  [ -d frontend-app/node_modules ]
  [ ! -L frontend-app/node_modules ]
  [ -x frontend-app/node_modules/.bin/vite ]
fi
if [ -n "${GATE_READY_FILE:-}" ]; then
  : >"$GATE_READY_FILE"
fi
if [ -n "${GATE_WAIT_FOR_INTERRUPT:-}" ]; then
  sleep 2
fi
if [ -n "${GATE_FORCE_FAILURE:-}" ]; then
  echo "forced gate failure" >&2
  exit 42
fi
if [ -n "${GATE_FORCE_CLEANUP_FAILURE:-}" ]; then
  chmod 0500 "$TMPDIR"
fi
if [ -n "${HOOK_SCOPE_LOG:-}" ]; then
  printf 'soft-generated=%s ai-maintenance %s\n' "${SUPER_DOLPHIN_PRE_PUSH_SOFT_GENERATED_DRIFT:-}" "$*" >>"$HOOK_SCOPE_LOG"
fi
`
	path := filepath.Join(root, "scripts", "ai_maintenance_gates.sh")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir fake ai maintenance gate dir: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o755); err != nil {
		t.Fatalf("write fake ai_maintenance_gates.sh: %v", err)
	}
}

func writePreCommitFakeAIMaintenancePlanner(t *testing.T, root string) {
	t.Helper()
	writeFixTestGuardFile(t, root, "go.mod", "module example.com/precommitfixture\n\ngo 1.24\n")
	content := `package main

import (
	"encoding/json"
	"os"
)

const manifest = "docs/doc/codemap/capability-contract/capability_manifest.json"

func main() {
	generated := []string{}
	for i := 0; i+1 < len(os.Args); i++ {
		if os.Args[i] != "--changed-file" {
			continue
		}
		if os.Args[i+1] == "internal/provider/producer.go" {
			generated = []string{manifest}
		}
	}
	if err := json.NewEncoder(os.Stdout).Encode(map[string]any{"generated_files": generated}); err != nil {
		panic(err)
	}
}
`
	writeFixTestGuardFile(t, root, "scripts/ai_maintenance/main.go", content)
}

func writePreCommitFakeCodemapMakefile(t *testing.T, root string) {
	t.Helper()
	content := ".PHONY: codemap-refresh project-map-refresh capcontract-refresh\n\n" +
		"codemap-refresh:\n" +
		"\t@mkdir -p docs/doc/codemap\n" +
		"\t@printf 'root readme refreshed\\n' > README.md\n" +
		"\t@printf 'archtest map refreshed\\n' > docs/doc/codemap/13-archtest-boundaries.md\n" +
		"\t@printf 'readme refreshed\\n' > docs/doc/codemap/README.md\n" +
		"\t@printf '{\"generated\":true}\\n' > docs/doc/codemap/ai-index.json\n\n" +
		"project-map-refresh:\n" +
		"\t@mkdir -p docs/doc/codemap/project-map/index\n" +
		"\t@printf 'project map refreshed\\n' > docs/doc/codemap/project-map/AI_PROJECT_MAP.md\n" +
		"\t@printf 'drift refreshed\\n' > docs/doc/codemap/project-map/AI_PROJECT_DRIFT.md\n" +
		"\t@printf '{\"generated\":true}\\n' > docs/doc/codemap/project-map/AI_PROJECT_MANIFEST.json\n" +
		"\t@printf 'path\\tmodule\\n' > docs/doc/codemap/project-map/index/other.tsv\n\n" +
		"capcontract-refresh:\n" +
		"\t@mkdir -p docs/doc/codemap/capability-contract\n" +
		"\t@printf '{\"capability\":\"refreshed\"}\\n' > docs/doc/codemap/capability-contract/capability_manifest.json\n"
	if err := os.WriteFile(filepath.Join(root, "Makefile"), []byte(content), 0o644); err != nil {
		t.Fatalf("write fake Makefile: %v", err)
	}
}

func writePrePushScopeFakeBins(t *testing.T, logPath string) string {
	t.Helper()
	binDir := t.TempDir()
	for name, content := range map[string]string{
		"go":   "#!/usr/bin/env bash\nprintf 'go %s\\n' \"$*\" >>\"$HOOK_SCOPE_LOG\"\nif [ \"${1:-}\" = \"list\" ]; then shift; printf '%s\\n' \"$@\"; fi\n",
		"make": "#!/usr/bin/env bash\nprintf 'make %s\\n' \"$*\" >>\"$HOOK_SCOPE_LOG\"\n",
		"node": "#!/usr/bin/env bash\ncase \"${1:-}\" in\n  -e)\n    [ \"${2:-}\" = \"process.exit(0)\" ] || exit 1\n    exit 0\n    ;;\n  -p)\n    case \"${2:-}\" in\n      'require(\"node:fs\").realpathSync(process.execPath)') printf '%s\\n' \"$0\" ;;\n      'process.version') printf '%s\\n' 'v20.0.0' ;;\n      *) exit 1 ;;\n    esac\n    ;;\n  *) printf 'node %s\\n' \"$*\" >>\"$HOOK_SCOPE_LOG\" ;;\nesac\n",
		"npx":  "#!/usr/bin/env bash\nprintf 'npx %s\\n' \"$*\" >>\"$HOOK_SCOPE_LOG\"\n",
		"npm":  "#!/usr/bin/env bash\nprintf 'npm %s\\n' \"$*\" >>\"$HOOK_SCOPE_LOG\"\n",
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

func TestPreCommitCreatesDeterministicEmbedPlaceholderFromStagedSnapshot(t *testing.T) {
	for _, mutableIgnoredArtifact := range []bool{false, true} {
		name := "without ignored artifact"
		if mutableIgnoredArtifact {
			name = "with mutable ignored artifact"
		}
		t.Run(name, func(t *testing.T) {
			root := preparePreCommitGateFixture(t)
			writeFixTestGuardFile(t, root, ".gitignore", ".build-cache/\ncmd/agent-terminal/web-dist/\n")
			runFixTestGuardGit(t, root, "add", ".gitignore", "scripts/guard_fix_commits_have_tests.sh")
			runFixTestGuardGit(t, root, "commit", "-m", "chore: 安装 embed fixture")
			writeFixTestGuardFile(t, root, "cmd/agent-terminal/main.go", "package main\n\nimport \"embed\"\n\n//go:embed all:web-dist\nvar frontend embed.FS\n\nfunc main() { _ = frontend }\n")
			if mutableIgnoredArtifact {
				writeFixTestGuardFile(t, root, "cmd/agent-terminal/web-dist/index.html", "mutable ignored artifact\n")
			}
			runFixTestGuardGit(t, root, "add", "cmd/agent-terminal/main.go")
			out, err := runPreCommitHookWithEnv(t, root, map[string]string{
				"GATE_ASSERT_RELATIVE_PATH": "cmd/agent-terminal/web-dist/index.html",
				"GATE_ASSERT_CONTENT":       "staged snapshot",
			})
			if err != nil {
				t.Fatalf("pre-commit embed placeholder failed: %v\n%s", err, out)
			}
			assertOutputContainsAll(t, out, "go vet (staged snapshot)", "pre-commit OK")
		})
	}
}

func prePushStdin(base, head string) string {
	return "refs/heads/main " + head + " refs/heads/main " + base + "\n"
}

func runPrePushScopeHook(t *testing.T, root, stdin, binDir, logPath string) (string, error) {
	t.Helper()
	cmd := exec.Command("bash", bashPath(".githooks", "pre-push"))
	cmd.Dir = root
	cmd.Stdin = strings.NewReader(stdin)
	capcontractGoBin := os.Getenv("CAPCONTRACT_PATH_RULES_GO_BIN")
	if capcontractGoBin == "" {
		resolvedGo, err := exec.LookPath("go")
		if err != nil {
			t.Fatalf("resolve real Go executable for path-rules fixture: %v", err)
		}
		capcontractGoBin = bashAbsolutePath(resolvedGo)
	}
	env := append(os.Environ(),
		"PATH="+bashArg("", binDir)+":/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin",
		"HOOK_SCOPE_LOG="+bashArg("", logPath),
		"CAPCONTRACT_PATH_RULES_GO_BIN="+capcontractGoBin,
		"SUPER_DOLPHIN_HOOK_NODE_BIN="+bashArg("", binDir),
	)
	cmd.Env = appendWSLEnvKeysWithGitPath(
		t,
		env,
		"PATH",
		"HOOK_SCOPE_LOG",
		"CAPCONTRACT_PATH_RULES_GO_BIN",
		"SUPER_DOLPHIN_HOOK_NODE_BIN",
	)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func runPreCommitHook(t *testing.T, root string) (string, error) {
	t.Helper()
	return runPreCommitHookWithEnv(t, root, nil)
}

func runPreCommitHookWithEnv(t *testing.T, root string, extra map[string]string) (string, error) {
	t.Helper()
	cmd := preCommitCommand(t, root, extra)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func preCommitCommand(t *testing.T, root string, extra map[string]string) *exec.Cmd {
	t.Helper()
	cmd := exec.Command("bash", bashPath(".githooks", "pre-commit"))
	cmd.Dir = root
	env := make([]string, 0, len(os.Environ())+len(extra))
	for _, item := range os.Environ() {
		key, _, _ := strings.Cut(item, "=")
		if _, replaced := extra[key]; !replaced {
			env = append(env, item)
		}
	}
	keys := []string{"PATH"}
	for key, value := range extra {
		env = append(env, key+"="+value)
		keys = append(keys, key)
	}
	cmd.Env = appendWSLEnvKeysWithGitPath(t, env, keys...)
	return cmd
}

func assertPreCommitFixtureClean(t *testing.T, root, tmpRoot string) {
	t.Helper()
	entries, err := os.ReadDir(tmpRoot)
	if err != nil {
		t.Fatalf("read controlled TMPDIR: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("pre-commit leaked controlled TMPDIR entries: %v", entries)
	}
	worktrees := runFixTestGuardGitOutput(t, root, "worktree", "list", "--porcelain")
	canonicalRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatalf("canonicalize fixture root: %v", err)
	}
	if strings.Count(worktrees, "worktree ") != 1 || !strings.Contains(worktrees, "worktree "+canonicalRoot) {
		t.Fatalf("fixture worktrees after hook = %q, want only %s", worktrees, root)
	}
}

type preCommitRepositoryState struct {
	headRef    string
	headCommit string
	indexTree  string
	stagedDiff string
	refs       string
	mergeHeads string
}

func capturePreCommitRepositoryState(t *testing.T, root string) preCommitRepositoryState {
	t.Helper()
	headCommit := "<unborn>"
	cmd := exec.Command("git", "-c", "gc.auto=0", "rev-parse", "--verify", "HEAD")
	cmd.Dir = root
	if out, err := cmd.CombinedOutput(); err == nil {
		headCommit = strings.TrimSpace(string(out))
	}
	mergeHeads := ""
	cmd = exec.Command("git", "-c", "gc.auto=0", "rev-parse", "--verify", "MERGE_HEAD")
	cmd.Dir = root
	if out, err := cmd.CombinedOutput(); err == nil {
		mergeHeads = strings.TrimSpace(string(out))
	}
	return preCommitRepositoryState{
		headRef:    strings.TrimSpace(runFixTestGuardGitOutput(t, root, "symbolic-ref", "-q", "HEAD")),
		headCommit: headCommit,
		indexTree:  strings.TrimSpace(runFixTestGuardGitOutput(t, root, "write-tree")),
		stagedDiff: runFixTestGuardGitOutput(t, root, "diff", "--cached", "--binary"),
		refs:       runFixTestGuardGitOutput(t, root, "show-ref"),
		mergeHeads: mergeHeads,
	}
}

func assertPreCommitRepositoryState(t *testing.T, root string, want preCommitRepositoryState) {
	t.Helper()
	if got := capturePreCommitRepositoryState(t, root); got != want {
		t.Fatalf("repository state changed by staged gate\ngot:  %#v\nwant: %#v", got, want)
	}
}

func preCommitOutputValue(t *testing.T, output, prefix string) string {
	t.Helper()
	for line := range strings.SplitSeq(output, "\n") {
		if value, ok := strings.CutPrefix(line, prefix); ok {
			return value
		}
	}
	t.Fatalf("output missing prefix %q\n%s", prefix, output)
	return ""
}

func assertSyntheticGateSnapshot(t *testing.T, output, expectedTree, diffBase string, parents ...string) {
	t.Helper()
	gateCommit := preCommitOutputValue(t, output, "gate-head=")
	if gateCommit == "" || gateCommit == diffBase {
		t.Fatalf("gate commit %q must be a distinct synthetic commit from %q\n%s", gateCommit, diffBase, output)
	}
	assertOutputContainsAll(t, output,
		"gate-tree="+expectedTree,
		"gate-status=\n",
		"--diff-range "+diffBase+".."+gateCommit,
	)
	assertOutputOmitsAll(t, output, "--diff-cached")
	wantParents := "gate-head-parents=" + gateCommit
	for _, parent := range parents {
		wantParents += " " + parent
	}
	assertOutputContainsAll(t, output, wantParents)
}

func runCICommitGuard(t *testing.T, root string, env map[string]string, args ...string) (string, error) {
	t.Helper()
	keys := make([]string, 0, len(env))
	for key := range env {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	cmdArgs := append([]string{bashPath("scripts", "ci_commit_guard.sh")}, bashArgs(root, args)...)
	cmd := exec.Command("bash", cmdArgs...)
	cmd.Dir = root
	cmd.Env = os.Environ()
	if len(keys) > 0 {
		wslEnv := strings.Join(keys, ":")
		if existing := os.Getenv("WSLENV"); existing != "" {
			wslEnv = existing + ":" + wslEnv
		}
		cmd.Env = append(cmd.Env, "WSLENV="+wslEnv)
	}
	for _, key := range keys {
		cmd.Env = append(cmd.Env, key+"="+env[key])
	}
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func runCommitMsgHook(t *testing.T, root, msgFile string) (string, error) {
	t.Helper()
	cmd := exec.Command("bash", bashPath(".githooks", "commit-msg"), bashArg(root, msgFile))
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

func prepareCommitTitleBaselineRepo(t *testing.T) (string, string) {
	t.Helper()
	root := prepareFixTestGuardRepo(t)
	eventBase := strings.TrimSpace(runFixTestGuardGitOutput(t, root, "rev-parse", "HEAD"))
	baseline := commitCommitGuardFixture(t, root, "docs/legacy.md", "legacy\n", "chore: legacy English title")
	copyCommitTitleGuard(t, root, baseline)
	runFixTestGuardGit(t, root, "add", "scripts/guard_commit_titles.sh", commitTitleEnforcementBaselinePath)
	runFixTestGuardGit(t, root, "commit", "-m", "chore: 安装提交标题门禁")
	return root, eventBase
}

func copyCommitTitleGuard(t *testing.T, root, baseline string) {
	t.Helper()
	if baseline == "" {
		baseline = strings.TrimSpace(runFixTestGuardGitOutput(t, root, "rev-parse", "HEAD"))
	}
	copyFixTestGuardRepoFile(t, root, "scripts/guard_commit_titles.sh", 0o755)
	writeFixTestGuardFile(t, root, commitTitleEnforcementBaselinePath, baseline+"\n")
}

func commitCommitGuardFixture(t *testing.T, root, path, content, subject string) string {
	t.Helper()
	writeFixTestGuardFile(t, root, path, content)
	runFixTestGuardGit(t, root, "add", path)
	runFixTestGuardGit(t, root, "commit", "-m", subject)
	return strings.TrimSpace(runFixTestGuardGitOutput(t, root, "rev-parse", "HEAD"))
}

func runCommitTitleGuard(t *testing.T, root string, args ...string) (string, error) {
	t.Helper()
	cmdArgs := append([]string{bashPath("scripts", "guard_commit_titles.sh")}, bashArgs(root, args)...)
	cmd := exec.Command("bash", cmdArgs...)
	cmd.Dir = root
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func commitGuardEventEnv(eventName, base, head string) map[string]string {
	if eventName == "pull_request" {
		return map[string]string{
			"GITHUB_EVENT_NAME": "pull_request",
			"GITHUB_BASE_SHA":   base,
			"GITHUB_HEAD_SHA":   head,
		}
	}
	return map[string]string{
		"GITHUB_EVENT_NAME":   "push",
		"GITHUB_EVENT_BEFORE": base,
		"GITHUB_SHA":          head,
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
	cmdArgs := append([]string{bashPath("scripts", "guard_fix_commits_have_tests.sh")}, bashArgs(root, args)...)
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
	cmdArgs := append([]string{
		"-c", "core.hooksPath=/dev/null",
		"-c", "commit.gpgsign=false",
		"-c", "tag.gpgsign=false",
		"-c", "gc.auto=0",
	}, args...)
	ctx, cancel := context.WithTimeout(context.Background(), fixTestGuardGitTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", cmdArgs...)
	cmd.Dir = root
	cmd.Env = append(os.Environ(), "GIT_EDITOR=true", "GIT_PAGER=cat")
	out, err := cmd.CombinedOutput()
	if ctx.Err() != nil {
		t.Fatalf("git %s timed out after %s\n%s", strings.Join(args, " "), fixTestGuardGitTimeout, string(out))
	}
	if err != nil {
		t.Fatalf("git %s failed: %v\n%s", strings.Join(args, " "), err, string(out))
	}
	return string(out)
}
