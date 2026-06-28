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
	assertOutputOmitsAll(t, out, "frontend-app tests")
	log := fixture.log(t)
	assertOutputContainsAll(t, log, "go-test ./internal/app -count=1")
	assertOutputOmitsAll(t, log, "node ", "npx ", "npm ")
}

func commitPrePushGoOnlyChange(t *testing.T, root string) string {
	t.Helper()
	writeFixTestGuardFile(t, root, "go.mod", "module example.com/prepushscope\n\ngo 1.22\n")
	writeFixTestGuardFile(t, root, "internal/app/app.go", "package app\n\nfunc App() {}\n")
	runFixTestGuardGit(t, root, "add", "go.mod", "internal/app/app.go")
	runFixTestGuardGit(t, root, "commit", "-m", "chore: 更新 app package")
	return strings.TrimSpace(runFixTestGuardGitOutput(t, root, "rev-parse", "HEAD"))
}

func assertPrePushFrontendAppOnlyScope(t *testing.T) {
	t.Helper()
	fixture := newPrePushScopeFixture(t)
	head := commitPrePushFrontendAppOnlyChange(t, fixture.root)
	out := fixture.run(t, head)
	assertOutputContainsAll(t, out, "[pre-push] frontend-app lint", "[pre-push] frontend-app tests", "[pre-push] frontend-app build", "pre-push OK")
	assertOutputOmitsAll(t, out, "go package tests")
	log := fixture.log(t)
	assertOutputContainsAll(t, log, "npm run lint", "npm test", "npm run build")
	assertOutputOmitsAll(t, log, "go-test ", "node ", "npx ")
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
		"go":   "#!/usr/bin/env bash\nprintf 'go %s\\n' \"$*\" >>\"$HOOK_SCOPE_LOG\"\nif [ \"${1:-}\" = \"list\" ]; then shift; printf '%s\\n' \"$@\"; fi\n",
		"node": "#!/usr/bin/env bash\nprintf 'node %s\\n' \"$*\" >>\"$HOOK_SCOPE_LOG\"\n",
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

func prePushStdin(base, head string) string {
	return "refs/heads/main " + head + " refs/heads/main " + base + "\n"
}

func runPrePushScopeHook(t *testing.T, root, stdin, binDir, logPath string) (string, error) {
	t.Helper()
	cmd := exec.Command("bash", bashPath(".githooks", "pre-push"))
	cmd.Dir = root
	cmd.Stdin = strings.NewReader(stdin)
	env := append(os.Environ(),
		"PATH="+bashArg("", binDir)+":/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin",
		"HOOK_SCOPE_LOG="+bashArg("", logPath),
	)
	cmd.Env = appendWSLEnvKeysWithGitPath(t, env, "PATH", "HOOK_SCOPE_LOG")
	out, err := cmd.CombinedOutput()
	return string(out), err
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
