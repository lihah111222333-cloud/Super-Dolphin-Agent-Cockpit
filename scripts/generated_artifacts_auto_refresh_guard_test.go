package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const generatedArtifactsLaunchAgentLabel = "com.super-agent-v3.generated-artifacts-refresh"

type generatedArtifactsRefreshFixture struct {
	superRoot        string
	wjbootRoot       string
	homeDir          string
	binDir           string
	refreshLog       string
	launchctlLog     string
	launchctlState   string
	managementScript string
}

// TestGeneratedArtifactsAutoRefreshRunOnceRefreshesBothRepos 锁定两个仓库统一刷新入口的调用顺序和参数。
func TestGeneratedArtifactsAutoRefreshRunOnceRefreshesBothRepos(t *testing.T) {
	fixture := newGeneratedArtifactsRefreshFixture(t)

	output, err := fixture.run("run-once", "--wjboot-repo", fixture.wjbootRoot)
	if err != nil {
		t.Fatalf("run-once failed: %v\n%s", err, output)
	}

	want := strings.Join([]string{
		"super|capcontract --repo " + fixture.superRoot,
		"wjboot|project-map --repo " + fixture.wjbootRoot,
	}, "\n")
	if got := strings.TrimSpace(readGeneratedArtifactsRefreshFile(t, fixture.refreshLog)); got != want {
		t.Fatalf("refresh calls = %q, want %q", got, want)
	}
}

// TestGeneratedArtifactsAutoRefreshRunOnceFailsFast 验证首仓刷新失败会阻断第二仓，避免留下半成功假象。
func TestGeneratedArtifactsAutoRefreshRunOnceFailsFast(t *testing.T) {
	fixture := newGeneratedArtifactsRefreshFixture(t)

	output, err := fixture.runWithEnv(map[string]string{"SUPER_REFRESH_EXIT": "7"}, "run-once", "--wjboot-repo", fixture.wjbootRoot)
	if err == nil {
		t.Fatalf("run-once succeeded, want first refresh failure\n%s", output)
	}
	if got := strings.TrimSpace(readGeneratedArtifactsRefreshFile(t, fixture.refreshLog)); got != "super|capcontract --repo "+fixture.superRoot {
		t.Fatalf("refresh calls after failure = %q", got)
	}
}

// TestGeneratedArtifactsAutoRefreshRunOnceManagesLock 验证活跃锁拒绝重入、陈旧锁可安全回收。
func TestGeneratedArtifactsAutoRefreshRunOnceManagesLock(t *testing.T) {
	fixture := newGeneratedArtifactsRefreshFixture(t)
	lockDir := filepath.Join(fixture.superRoot, ".git", "generated-artifacts-auto-refresh.lock")

	if err := os.Mkdir(lockDir, 0o755); err != nil {
		t.Fatalf("create active lock: %v", err)
	}
	writeGeneratedArtifactsRefreshFile(t, filepath.Join(lockDir, "pid"), fmt.Sprintf("%d\n", os.Getpid()), 0o644)
	output, err := fixture.run("run-once", "--wjboot-repo", fixture.wjbootRoot)
	if err == nil || !strings.Contains(output, "already running") {
		t.Fatalf("active lock result = %v, output = %q", err, output)
	}

	if err := os.RemoveAll(lockDir); err != nil {
		t.Fatalf("remove active lock: %v", err)
	}
	if err := os.Mkdir(lockDir, 0o755); err != nil {
		t.Fatalf("create stale lock: %v", err)
	}
	writeGeneratedArtifactsRefreshFile(t, filepath.Join(lockDir, "pid"), "999999\n", 0o644)
	output, err = fixture.run("run-once", "--wjboot-repo", fixture.wjbootRoot)
	if err != nil {
		t.Fatalf("run-once with stale lock failed: %v\n%s", err, output)
	}
	if _, err := os.Stat(lockDir); !os.IsNotExist(err) {
		t.Fatalf("lock remains after successful refresh: %v", err)
	}
}

// TestGeneratedArtifactsAutoRefreshRequiresWJBootRepo 验证后台任务不会猜测或默认选择另一个仓库。
func TestGeneratedArtifactsAutoRefreshRequiresWJBootRepo(t *testing.T) {
	fixture := newGeneratedArtifactsRefreshFixture(t)

	output, err := fixture.run("run-once")
	if err == nil || !strings.Contains(output, "--wjboot-repo is required") {
		t.Fatalf("missing repo result = %v, output = %q", err, output)
	}
}

// TestGeneratedArtifactsAutoRefreshInstallLifecycle 验证 plist 契约、幂等重装、状态查询和定点卸载。
func TestGeneratedArtifactsAutoRefreshInstallLifecycle(t *testing.T) {
	fixture := newGeneratedArtifactsRefreshFixture(t)
	plistPath := filepath.Join(fixture.homeDir, "Library", "LaunchAgents", generatedArtifactsLaunchAgentLabel+".plist")
	installGeneratedArtifactsRefreshAgentTwice(t, fixture)
	assertGeneratedArtifactsRefreshPlist(t, fixture, plistPath)
	assertGeneratedArtifactsRefreshLaunchctlCalls(t, fixture)
	assertGeneratedArtifactsRefreshStatusAndUninstall(t, fixture, plistPath)
}

// installGeneratedArtifactsRefreshAgentTwice 验证重复安装走幂等重载路径。
func installGeneratedArtifactsRefreshAgentTwice(t *testing.T, fixture generatedArtifactsRefreshFixture) {
	t.Helper()
	for i := range 2 {
		output, err := fixture.run("install", "--wjboot-repo", fixture.wjbootRoot)
		if err != nil {
			t.Fatalf("install %d failed: %v\n%s", i+1, err, output)
		}
	}
}

// assertGeneratedArtifactsRefreshPlist 检查定时间隔、执行参数和日志路径契约。
func assertGeneratedArtifactsRefreshPlist(t *testing.T, fixture generatedArtifactsRefreshFixture, plistPath string) {
	t.Helper()
	plist := readGeneratedArtifactsRefreshFile(t, plistPath)
	for _, want := range []string{
		generatedArtifactsLaunchAgentLabel,
		"<key>StartInterval</key>",
		"<integer>300</integer>",
		"<key>RunAtLoad</key>",
		"<true/>",
		"<string>run-once</string>",
		"<string>--wjboot-repo</string>",
		fixture.wjbootRoot,
		"<key>StandardOutPath</key>",
		"<key>StandardErrorPath</key>",
	} {
		if !strings.Contains(plist, want) {
			t.Errorf("plist missing %q\n%s", want, plist)
		}
	}
}

// assertGeneratedArtifactsRefreshLaunchctlCalls 检查首次加载、立即启动和重复安装卸载旧任务。
func assertGeneratedArtifactsRefreshLaunchctlCalls(t *testing.T, fixture generatedArtifactsRefreshFixture) {
	t.Helper()
	launchctlLog := readGeneratedArtifactsRefreshFile(t, fixture.launchctlLog)
	for _, want := range []string{"bootstrap gui/", "kickstart -k gui/", "bootout gui/"} {
		if !strings.Contains(launchctlLog, want) {
			t.Errorf("launchctl log missing %q\n%s", want, launchctlLog)
		}
	}
}

// assertGeneratedArtifactsRefreshStatusAndUninstall 检查加载状态、定点卸载和日志保留边界。
func assertGeneratedArtifactsRefreshStatusAndUninstall(t *testing.T, fixture generatedArtifactsRefreshFixture, plistPath string) {
	t.Helper()
	if output, err := fixture.run("status"); err != nil {
		t.Fatalf("status failed: %v\n%s", err, output)
	}
	logPath := filepath.Join(fixture.homeDir, "Library", "Logs", "super-agent-v3", "generated-artifacts-refresh.log")
	writeGeneratedArtifactsRefreshFile(t, logPath, "retained\n", 0o644)
	if output, err := fixture.run("uninstall"); err != nil {
		t.Fatalf("uninstall failed: %v\n%s", err, output)
	}
	if _, err := os.Stat(plistPath); !os.IsNotExist(err) {
		t.Fatalf("plist remains after uninstall: %v", err)
	}
	if got := readGeneratedArtifactsRefreshFile(t, logPath); got != "retained\n" {
		t.Fatalf("uninstall changed retained log: %q", got)
	}
}

// newGeneratedArtifactsRefreshFixture 构造真实脚本执行边界，仅替换两个生成器入口和 launchctl 外部进程。
func newGeneratedArtifactsRefreshFixture(t *testing.T) generatedArtifactsRefreshFixture {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatalf("resolve fixture temp dir: %v", err)
	}
	fixture := generatedArtifactsRefreshFixture{
		superRoot:      filepath.Join(root, "super-agent-v3"),
		wjbootRoot:     filepath.Join(root, "wjboot-v2"),
		homeDir:        filepath.Join(root, "home"),
		binDir:         filepath.Join(root, "bin"),
		refreshLog:     filepath.Join(root, "refresh.log"),
		launchctlLog:   filepath.Join(root, "launchctl.log"),
		launchctlState: filepath.Join(root, "launchctl.loaded"),
	}
	fixture.managementScript = filepath.Join(fixture.superRoot, "scripts", "generated_artifacts_auto_refresh.sh")

	for _, dir := range []string{filepath.Join(fixture.superRoot, "scripts"), filepath.Join(fixture.wjbootRoot, "scripts"), fixture.homeDir, fixture.binDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("create fixture dir %s: %v", dir, err)
		}
	}
	copyGeneratedArtifactsRefreshFile(t, filepath.Join(scriptRepoRoot(t), "scripts", "generated_artifacts_auto_refresh.sh"), fixture.managementScript, 0o755)
	writeGeneratedArtifactsRefreshFile(t, filepath.Join(fixture.superRoot, "scripts", "refresh_generated_artifacts.sh"), `#!/usr/bin/env bash
set -euo pipefail
printf 'super|%s\n' "$*" >>"$REFRESH_TEST_LOG"
exit "${SUPER_REFRESH_EXIT:-0}"
`, 0o755)
	writeGeneratedArtifactsRefreshFile(t, filepath.Join(fixture.wjbootRoot, "scripts", "refresh_generated_artifacts.sh"), `#!/usr/bin/env bash
set -euo pipefail
printf 'wjboot|%s\n' "$*" >>"$REFRESH_TEST_LOG"
`, 0o755)
	writeGeneratedArtifactsRefreshFile(t, filepath.Join(fixture.binDir, "launchctl"), `#!/usr/bin/env bash
set -euo pipefail
printf '%s\n' "$*" >>"$LAUNCHCTL_TEST_LOG"
case "$1" in
  print) test -f "$LAUNCHCTL_STATE" ;;
  bootout) rm -f "$LAUNCHCTL_STATE" ;;
  bootstrap) : >"$LAUNCHCTL_STATE" ;;
  kickstart) exit 0 ;;
  *) echo "unexpected launchctl command: $1" >&2; exit 64 ;;
esac
`, 0o755)

	cmd := exec.Command("git", "init", "-q", fixture.superRoot)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("init fixture git repo: %v\n%s", err, output)
	}
	return fixture
}

// run 执行管理脚本并继承 fixture 的隔离 HOME、PATH 和观测日志。
func (fixture generatedArtifactsRefreshFixture) run(args ...string) (string, error) {
	return fixture.runWithEnv(nil, args...)
}

// runWithEnv 允许单个用例注入刷新失败，不改变其他 fixture 的真实执行行为。
func (fixture generatedArtifactsRefreshFixture) runWithEnv(extra map[string]string, args ...string) (string, error) {
	cmd := exec.Command("bash", append([]string{fixture.managementScript}, args...)...)
	cmd.Dir = fixture.superRoot
	env := append(os.Environ(),
		"HOME="+fixture.homeDir,
		"PATH="+fixture.binDir+string(os.PathListSeparator)+os.Getenv("PATH"),
		"REFRESH_TEST_LOG="+fixture.refreshLog,
		"LAUNCHCTL_TEST_LOG="+fixture.launchctlLog,
		"LAUNCHCTL_STATE="+fixture.launchctlState,
	)
	for key, value := range extra {
		env = append(env, key+"="+value)
	}
	cmd.Env = env
	output, err := cmd.CombinedOutput()
	return string(output), err
}

// copyGeneratedArtifactsRefreshFile 复制被测脚本到临时仓库，使路径推导和 Git 锁逻辑走真实边界。
func copyGeneratedArtifactsRefreshFile(t *testing.T, source, target string, mode os.FileMode) {
	t.Helper()
	data, err := os.ReadFile(source)
	if err != nil {
		t.Fatalf("read %s: %v", source, err)
	}
	if err := os.WriteFile(target, data, mode); err != nil {
		t.Fatalf("write %s: %v", target, err)
	}
}

// writeGeneratedArtifactsRefreshFile 写入测试 fixture 文件并保留调用方指定权限。
func writeGeneratedArtifactsRefreshFile(t *testing.T, path, content string, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("create parent for %s: %v", path, err)
	}
	if err := os.WriteFile(path, []byte(content), mode); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

// readGeneratedArtifactsRefreshFile 读取测试日志或生成的 plist，失败时立即终止用例。
func readGeneratedArtifactsRefreshFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(data)
}
