//go:build darwin || linux

package hiddenexec

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

const (
	unixSupervisorOrphanHostEnv   = "SUPER_DOLPHIN_TEST_UNIX_LSP_ORPHAN_HOST"
	unixSupervisorOrphanCWDEnv    = "SUPER_DOLPHIN_TEST_UNIX_LSP_ORPHAN_CWD"
	unixSupervisorOrphanReportEnv = "SUPER_DOLPHIN_TEST_UNIX_LSP_ORPHAN_REPORT"
	unixSupervisorOrphanTriggerFD = 3
)

// TestUnixProcessSupervisorHardParentExitDeletedCWD_E2E 验证宿主硬退出且工作目录被删后监管进程会回收完整进程组。
func TestUnixProcessSupervisorHardParentExitDeletedCWD_E2E(t *testing.T) {
	fixtureRoot := t.TempDir()
	workingDir := filepath.Join(fixtureRoot, "deleted-workspace")
	if err := os.Mkdir(workingDir, 0o700); err != nil {
		t.Fatalf("create orphan supervisor working directory: %v", err)
	}
	reportPath := filepath.Join(fixtureRoot, "supervisor-identity.json")
	host, triggerWrite, stdinWrite, outputRead := startUnixSupervisorOrphanHost(t, workingDir, reportPath)

	root := readUnixSupervisorOrphanIdentity(t, reportPath)
	t.Cleanup(func() { cleanupUnixSupervisorOrphanFixture(t, root) })
	if err := os.Remove(workingDir); err != nil {
		t.Fatalf("delete orphan supervisor working directory: %v", err)
	}
	requireUnixSupervisorStableCWD(t, root.PID)
	if _, err := triggerWrite.Write([]byte{'X'}); err != nil {
		t.Fatalf("trigger orphan supervisor host hard exit: %v", err)
	}
	if err := triggerWrite.Close(); err != nil {
		t.Fatalf("close orphan supervisor host exit trigger: %v", err)
	}
	if err := host.Wait(); err != nil {
		t.Fatalf("orphan supervisor host exit: %v", err)
	}
	if err := stdinWrite.Close(); err != nil {
		t.Fatalf("close orphan supervisor inherited stdin writer: %v", err)
	}
	if err := outputRead.Close(); err != nil {
		t.Fatalf("close orphan supervisor inherited output reader: %v", err)
	}

	requireUnixSupervisorGroupGone(t, root, 5*time.Second)
}

// super-dolphin-ci: helper
func TestUnixProcessSupervisorOrphanHostHelper(t *testing.T) {
	if os.Getenv(unixSupervisorOrphanHostEnv) != "1" {
		return
	}
	os.Exit(runUnixSupervisorOrphanHost())
}

// startUnixSupervisorOrphanHost 启动中间宿主并返回硬退出触发器与监管根继承管道的父侧对端。
func startUnixSupervisorOrphanHost(t *testing.T, workingDir, reportPath string) (*exec.Cmd, *os.File, *os.File, *os.File) {
	t.Helper()
	triggerRead, triggerWrite, err := os.Pipe()
	if err != nil {
		t.Fatalf("create orphan host exit trigger: %v", err)
	}
	stdinRead, stdinWrite, err := os.Pipe()
	if err != nil {
		_ = triggerRead.Close()
		_ = triggerWrite.Close()
		t.Fatalf("create orphan host stdin pipe: %v", err)
	}
	outputRead, outputWrite, err := os.Pipe()
	if err != nil {
		_ = triggerRead.Close()
		_ = triggerWrite.Close()
		_ = stdinRead.Close()
		_ = stdinWrite.Close()
		t.Fatalf("create orphan host output pipe: %v", err)
	}
	t.Cleanup(func() {
		_ = triggerRead.Close()
		_ = triggerWrite.Close()
		_ = stdinRead.Close()
		_ = stdinWrite.Close()
		_ = outputRead.Close()
		_ = outputWrite.Close()
	})

	host := exec.Command(os.Args[0], "-test.run=^TestUnixProcessSupervisorOrphanHostHelper$")
	host.Env = append(
		os.Environ(),
		unixSupervisorOrphanHostEnv+"=1",
		unixSupervisorOrphanCWDEnv+"="+workingDir,
		unixSupervisorOrphanReportEnv+"="+reportPath,
	)
	host.Stdin = stdinRead
	host.Stdout = outputWrite
	host.Stderr = outputWrite
	host.ExtraFiles = []*os.File{triggerRead}
	if err := host.Start(); err != nil {
		t.Fatalf("start orphan supervisor host: %v", err)
	}
	if err := triggerRead.Close(); err != nil {
		t.Fatalf("close parent copy of orphan host exit trigger reader: %v", err)
	}
	if err := stdinRead.Close(); err != nil {
		t.Fatalf("close parent copy of orphan host stdin reader: %v", err)
	}
	if err := outputWrite.Close(); err != nil {
		t.Fatalf("close parent copy of orphan host output writer: %v", err)
	}
	waitForUnixSupervisorOrphanReport(t, reportPath, 3*time.Second)
	return host, triggerWrite, stdinWrite, outputRead
}

// runUnixSupervisorOrphanHost 启动真实监管根和语言服务器后直接退出，模拟宿主无法执行清理逻辑。
func runUnixSupervisorOrphanHost() int {
	workingDir := os.Getenv(unixSupervisorOrphanCWDEnv)
	reportPath := os.Getenv(unixSupervisorOrphanReportEnv)
	if workingDir == "" || reportPath == "" {
		return 2
	}
	supervised, err := NewUnixSupervisedCommand("/bin/sh", "-c", "sleep 30 & wait")
	if err != nil {
		return 3
	}
	command := supervised.Command()
	command.Dir = workingDir
	command.Stdin = os.Stdin
	command.Stdout = os.Stdout
	command.Stderr = os.Stderr
	tree, err := supervised.StartProcessTree()
	if err != nil {
		_ = supervised.Close()
		return 4
	}
	root, err := tree.Identity()
	if err != nil {
		_ = supervised.Close()
		return 5
	}
	if err := waitForUnixSupervisorDescendantIdentity(root, 3*time.Second); err != nil {
		_ = supervised.Close()
		return 6
	}
	if err := writeUnixSupervisorOrphanIdentityReport(reportPath, root); err != nil {
		_ = supervised.Close()
		return 7
	}
	trigger := os.NewFile(unixSupervisorOrphanTriggerFD, "unix-lsp-orphan-host-exit-trigger")
	if trigger == nil {
		_ = supervised.Close()
		return 8
	}
	defer trigger.Close()
	var payloadByte [1]byte
	if _, err := io.ReadFull(trigger, payloadByte[:]); err != nil {
		_ = supervised.Close()
		return 9
	}
	return 0
}

// writeUnixSupervisorOrphanIdentityReport 原子编码测试监管根的启动身份。
func writeUnixSupervisorOrphanIdentityReport(reportPath string, root ProcessIdentity) error {
	payload, err := json.Marshal(root)
	if err != nil {
		return fmt.Errorf("encode orphan supervisor identity: %w", err)
	}
	if err := os.WriteFile(reportPath, payload, 0o600); err != nil {
		return fmt.Errorf("write orphan supervisor identity: %w", err)
	}
	return nil
}

// waitForUnixSupervisorOrphanReport 等待中间宿主公布已启动监管根的精确身份。
func waitForUnixSupervisorOrphanReport(t *testing.T, reportPath string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(reportPath); err == nil {
			return
		} else if !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("inspect orphan supervisor identity report: %v", err)
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("orphan supervisor identity report was not published within %s", timeout)
}

// waitForUnixSupervisorDescendantIdentity 等待真实语言服务器进入监管根的稳定进程组。
func waitForUnixSupervisorDescendantIdentity(root ProcessIdentity, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		table, err := processTable()
		if err != nil {
			return fmt.Errorf("read process table while waiting for orphan fixture: %w", err)
		}
		for _, member := range table {
			if member.PID != root.PID && sameProcessTreeGroup(member, root) {
				return nil
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	return errors.New("orphan supervisor fixture did not start an owned descendant")
}

// readUnixSupervisorOrphanIdentity 读取中间宿主在硬退出前持久化的精确监管根身份。
func readUnixSupervisorOrphanIdentity(t *testing.T, reportPath string) ProcessIdentity {
	t.Helper()
	payload, err := os.ReadFile(reportPath)
	if err != nil {
		t.Fatalf("read orphan supervisor identity: %v", err)
	}
	var root ProcessIdentity
	if err := json.Unmarshal(payload, &root); err != nil {
		t.Fatalf("decode orphan supervisor identity: %v", err)
	}
	if root.PID <= 1 || root.SessionID != root.PID || root.ProcessGroupID != root.PID || root.StartToken == "" {
		t.Fatalf("orphan supervisor identity is invalid: %+v", root)
	}
	return root
}

// requireUnixSupervisorStableCWD 验证监管根自身已脱离可删除工作区并绑定到仍存在的目录。
func requireUnixSupervisorStableCWD(t *testing.T, pid int) {
	t.Helper()
	cwd, err := unixSupervisorProcessCWD(pid)
	if err != nil {
		t.Fatalf("inspect Unix supervisor cwd: %v", err)
	}
	if _, err := os.Stat(cwd); err != nil {
		t.Fatalf("Unix supervisor cwd is not stable: path=%q err=%v", cwd, err)
	}
}

// unixSupervisorProcessCWD 使用各 Unix 内核的只读进程接口解析监管根当前目录。
func unixSupervisorProcessCWD(pid int) (string, error) {
	if runtime.GOOS == "linux" {
		cwd, err := os.Readlink("/proc/" + strconv.Itoa(pid) + "/cwd")
		if err != nil {
			return "", fmt.Errorf("read Linux supervisor cwd: %w", err)
		}
		return cwd, nil
	}
	output, err := exec.Command("/usr/sbin/lsof", "-a", "-p", strconv.Itoa(pid), "-d", "cwd", "-Fn").Output()
	if err != nil {
		return "", fmt.Errorf("read Darwin supervisor cwd: %w", err)
	}
	for line := range strings.SplitSeq(string(output), "\n") {
		if !strings.HasPrefix(line, "n") {
			continue
		}
		cwd := strings.TrimPrefix(line, "n")
		if cwd == "" {
			break
		}
		return cwd, nil
	}
	return "", fmt.Errorf("Darwin supervisor cwd is unavailable: %q", strings.TrimSpace(string(output)))
}

// requireUnixSupervisorGroupGone 等待监管根及其全部同组后代在严格时限内消失。
func requireUnixSupervisorGroupGone(t *testing.T, root ProcessIdentity, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		members, err := unixSupervisorGroupMembers(root)
		if err != nil {
			t.Fatalf("inspect orphan supervisor group: %v", err)
		}
		if len(members) == 0 {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	members, err := unixSupervisorGroupMembers(root)
	if err != nil {
		t.Fatalf("inspect timed out orphan supervisor group: %v", err)
	}
	t.Fatalf("orphan supervisor group remained after %s: %+v", timeout, members)
}

// unixSupervisorGroupMembers 返回仍匹配监管根不可变 session/PGID 身份的成员。
func unixSupervisorGroupMembers(root ProcessIdentity) ([]ProcessIdentity, error) {
	table, err := processTable()
	if err != nil {
		return nil, err
	}
	members := make([]ProcessIdentity, 0)
	for _, member := range table {
		if sameProcessTreeGroup(member, root) {
			members = append(members, member)
		}
	}
	return members, nil
}

// cleanupUnixSupervisorOrphanFixture 仅在根身份仍精确匹配时强制清理测试拥有的独立进程组。
func cleanupUnixSupervisorOrphanFixture(t *testing.T, root ProcessIdentity) {
	t.Helper()
	current, err := captureProcessIdentity(root.PID)
	if errors.Is(err, syscall.ESRCH) || errors.Is(err, syscall.EIO) || errors.Is(err, os.ErrNotExist) {
		return
	}
	if err != nil {
		t.Errorf("capture orphan fixture identity during cleanup: %v", err)
		return
	}
	if !current.Equal(root) {
		t.Errorf("orphan fixture identity changed before cleanup: got %+v want %+v", current, root)
		return
	}
	if err := syscall.Kill(-root.ProcessGroupID, syscall.SIGKILL); err != nil && !errors.Is(err, syscall.ESRCH) {
		t.Errorf("kill exact orphan fixture process group: %v", err)
	}
}
