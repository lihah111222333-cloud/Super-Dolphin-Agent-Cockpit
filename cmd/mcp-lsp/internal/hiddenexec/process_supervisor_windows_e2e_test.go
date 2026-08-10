//go:build windows

package hiddenexec

import (
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

const (
	windowsSupervisorHostEnv     = "SUPER_DOLPHIN_TEST_WINDOWS_LSP_ORPHAN_HOST"
	windowsSupervisorCWDEnv      = "SUPER_DOLPHIN_TEST_WINDOWS_LSP_ORPHAN_CWD"
	windowsSupervisorReportEnv   = "SUPER_DOLPHIN_TEST_WINDOWS_LSP_ORPHAN_REPORT"
	windowsSupervisorTriggerEnv  = "SUPER_DOLPHIN_TEST_WINDOWS_LSP_ORPHAN_TRIGGER"
	windowsSupervisorWaitTimeout = 5 * time.Second
)

// TestWindowsJobObjectHardParentExitReapsOwnedProcessE2E 验证宿主硬退出会由 kill-on-close Job 回收真实 LSP 根。
func TestWindowsJobObjectHardParentExitReapsOwnedProcessE2E(t *testing.T) {
	fixtureRoot := t.TempDir()
	workingDir := filepath.Join(fixtureRoot, "workspace")
	if err := os.Mkdir(workingDir, 0o700); err != nil {
		t.Fatalf("create Windows LSP working directory: %v", err)
	}
	reportPath := filepath.Join(fixtureRoot, "process-identity.json")
	triggerPath := filepath.Join(fixtureRoot, "host-exit.trigger")
	host := startWindowsSupervisorHost(t, workingDir, reportPath, triggerPath)
	root := readWindowsSupervisorIdentity(t, reportPath)
	t.Cleanup(func() { cleanupWindowsSupervisorFixture(t, root) })
	if err := os.WriteFile(triggerPath, []byte("exit"), 0o600); err != nil {
		t.Fatalf("trigger Windows LSP host hard exit: %v", err)
	}
	if err := host.Wait(); err != nil {
		t.Fatalf("Windows LSP host exit: %v", err)
	}
	requireWindowsSupervisorProcessGone(t, root, windowsSupervisorWaitTimeout)
}

// super-dolphin-ci: helper
func TestWindowsJobObjectOrphanHostHelper(t *testing.T) {
	if os.Getenv(windowsSupervisorHostEnv) != "1" {
		return
	}
	os.Exit(runWindowsSupervisorHost())
}

// startWindowsSupervisorHost 启动持有 Job Object 的中间宿主并等待其公布真实 LSP 身份。
func startWindowsSupervisorHost(t *testing.T, workingDir, reportPath, triggerPath string) *exec.Cmd {
	t.Helper()
	host := exec.Command(os.Args[0], "-test.run=^TestWindowsJobObjectOrphanHostHelper$")
	host.Env = append(
		os.Environ(),
		windowsSupervisorHostEnv+"=1",
		windowsSupervisorCWDEnv+"="+workingDir,
		windowsSupervisorReportEnv+"="+reportPath,
		windowsSupervisorTriggerEnv+"="+triggerPath,
	)
	if err := host.Start(); err != nil {
		t.Fatalf("start Windows LSP orphan host: %v", err)
	}
	t.Cleanup(func() {
		_ = host.Process.Kill()
		_ = host.Wait()
	})
	waitForWindowsSupervisorReport(t, reportPath, 3*time.Second)
	return host
}

// runWindowsSupervisorHost 启动真实 Job owner 后等待外层测试触发进程级硬退出。
func runWindowsSupervisorHost() int {
	workingDir := os.Getenv(windowsSupervisorCWDEnv)
	reportPath := os.Getenv(windowsSupervisorReportEnv)
	triggerPath := os.Getenv(windowsSupervisorTriggerEnv)
	if workingDir == "" || reportPath == "" || triggerPath == "" {
		return 2
	}
	supervised, err := NewPlatformSupervisedCommand("cmd.exe", "/D", "/S", "/C", "ping -n 31 127.0.0.1 >NUL")
	if err != nil {
		return 3
	}
	supervised.Command().Dir = workingDir
	tree, err := supervised.StartProcessTree()
	if err != nil {
		return 4
	}
	root, err := tree.Identity()
	if err != nil {
		return 5
	}
	if err := writeWindowsSupervisorIdentity(reportPath, root); err != nil {
		return 6
	}
	if err := waitForWindowsSupervisorTrigger(triggerPath, 3*time.Second); err != nil {
		return 7
	}
	return 0
}

// writeWindowsSupervisorIdentity 保存宿主退出前由 Job owner 捕获的不可变根身份。
func writeWindowsSupervisorIdentity(reportPath string, root ProcessIdentity) error {
	payload, err := json.Marshal(root)
	if err != nil {
		return err
	}
	return os.WriteFile(reportPath, payload, 0o600)
}

// waitForWindowsSupervisorTrigger 等待父测试创建一次性硬退出触发文件。
func waitForWindowsSupervisorTrigger(triggerPath string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(triggerPath); err == nil {
			return nil
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		time.Sleep(20 * time.Millisecond)
	}
	return errors.New("Windows LSP orphan host trigger timed out")
}

// waitForWindowsSupervisorReport 等待中间宿主公布 Job 内真实 LSP 根身份。
func waitForWindowsSupervisorReport(t *testing.T, reportPath string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(reportPath); err == nil {
			return
		} else if !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("inspect Windows LSP identity report: %v", err)
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("Windows LSP identity report was not published within %s", timeout)
}

// readWindowsSupervisorIdentity 读取并校验 Job owner 的精确进程启动身份。
func readWindowsSupervisorIdentity(t *testing.T, reportPath string) ProcessIdentity {
	t.Helper()
	payload, err := os.ReadFile(reportPath)
	if err != nil {
		t.Fatalf("read Windows LSP identity: %v", err)
	}
	var root ProcessIdentity
	if err := json.Unmarshal(payload, &root); err != nil {
		t.Fatalf("decode Windows LSP identity: %v", err)
	}
	if root.PID <= 1 || root.StartToken == "" {
		t.Fatalf("Windows LSP identity is invalid: %+v", root)
	}
	return root
}

// requireWindowsSupervisorProcessGone 等待 kill-on-close Job 在宿主退出后回收原始根身份。
func requireWindowsSupervisorProcessGone(t *testing.T, root ProcessIdentity, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		current, err := captureWindowsProcessIdentity(root.PID)
		if processTreeProcessGone(err) {
			return
		}
		if err != nil {
			t.Fatalf("inspect Windows LSP root after host exit: %v", err)
		}
		if !current.Equal(root) {
			t.Fatalf("Windows LSP root PID was reused: got %+v want %+v", current, root)
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("Windows LSP root remained after host exit: %+v", root)
}

// cleanupWindowsSupervisorFixture 只在根身份仍精确匹配时清理测试自己的进程。
func cleanupWindowsSupervisorFixture(t *testing.T, root ProcessIdentity) {
	t.Helper()
	current, err := captureWindowsProcessIdentity(root.PID)
	if processTreeProcessGone(err) {
		return
	}
	if err != nil {
		t.Errorf("inspect Windows LSP root during cleanup: %v", err)
		return
	}
	if !current.Equal(root) {
		t.Errorf("Windows LSP root identity changed before cleanup: got %+v want %+v", current, root)
		return
	}
	process, err := os.FindProcess(root.PID)
	if err != nil {
		t.Errorf("open Windows LSP root during cleanup: %v", err)
		return
	}
	if err := process.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
		t.Errorf("kill exact Windows LSP root during cleanup: %v", err)
	}
}
