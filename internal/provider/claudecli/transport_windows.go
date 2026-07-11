//go:build windows

package claudecli

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"

	pkglogger "github.com/lihah111222333-cloud/super-dolphin-agent/pkg/logger"
)

// setClaudeProcessAttrs 在 Windows 上不设置 SysProcAttr。
// 进程树控制由 Start 后创建的 Job Object 接管，不能套用 Unix 进程组语义。
func setClaudeProcessAttrs(cmd *exec.Cmd) {
	if cmd == nil {
		return
	}
}

// resolveClaudeBinary 在 Windows 上解开 npm shim，尽量直接启动真实 claude.exe。
// system prompt 常含大块换行文本，经 cmd.exe wrapper 转发会破坏参数并导致 stdout EOF。
// npm wrapper 常见模板如下:
//
//	@ECHO off
//	...
//	"%dp0%\node_modules\@scope\pkg\bin\foo.exe"   %*
//
// 识别到模板时返回内嵌 exe 绝对路径；否则保留 LookPath 结果，让调用链按常规失败。
func resolveClaudeBinary(binary string) string {
	if binary == "" {
		binary = "claude"
	}
	// 显式路径由调用方负责可信性和可执行性。
	if strings.ContainsAny(binary, `\/`) {
		return binary
	}
	resolved, err := exec.LookPath(binary)
	if err != nil {
		return binary
	}
	ext := strings.ToLower(filepath.Ext(resolved))
	if ext != ".cmd" && ext != ".bat" {
		return resolved
	}
	real, ok := unwrapNpmShim(resolved)
	if !ok {
		return resolved
	}
	pkglogger.Info("claudecli: unwrapped npm shim",
		"shim", resolved, "exe", real)
	return real
}

var npmShimExeRE = regexp.MustCompile(`(?i)"%dp0%[\\/]([^"]+\.exe)"`)

func unwrapNpmShim(cmdPath string) (string, bool) {
	data, err := os.ReadFile(cmdPath)
	if err != nil {
		return "", false
	}
	matches := npmShimExeRE.FindSubmatch(data)
	if len(matches) < 2 {
		return "", false
	}
	rel := strings.TrimSpace(string(matches[1]))
	if rel == "" {
		return "", false
	}
	base := filepath.Dir(cmdPath)
	abs := filepath.Clean(filepath.Join(base, rel))
	if _, err := os.Stat(abs); err != nil {
		return "", false
	}
	return abs, true
}

// processGuard 用 Windows Job Object 包住 Claude CLI 进程树。
// TerminateJobObject 可一次回收子进程，kill-on-close 也能覆盖宿主异常退出的清理路径。
type processGuard struct {
	handle windows.Handle
}

// attachProcessGuard 将已启动的 Claude 进程加入 kill-on-close Job Object。
// 创建或绑定失败只记录告警并返回 nil，后续仍会按单进程 PID 路径尝试终止。
func attachProcessGuard(cmd *exec.Cmd) *processGuard {
	if cmd == nil || cmd.Process == nil {
		return nil
	}
	handle, err := createKillOnCloseJob()
	if err != nil {
		pkglogger.Warn("claudecli: create job object failed", "error", err)
		return nil
	}
	procHandle, err := windows.OpenProcess(
		windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE,
		false,
		uint32(cmd.Process.Pid),
	)
	if err != nil {
		pkglogger.Warn("claudecli: open process handle failed",
			"pid", cmd.Process.Pid, "error", err)
		windows.CloseHandle(handle)
		return nil
	}
	defer windows.CloseHandle(procHandle)
	if err := windows.AssignProcessToJobObject(handle, procHandle); err != nil {
		pkglogger.Warn("claudecli: assign process to job failed",
			"pid", cmd.Process.Pid, "error", err)
		windows.CloseHandle(handle)
		return nil
	}
	return &processGuard{handle: handle}
}

func (g *processGuard) close() {
	if g == nil || g.handle == 0 {
		return
	}
	windows.CloseHandle(g.handle)
	g.handle = 0
}

// signalClaudeProcess 在 Windows 上优先终止 Job Object，失败后退到单 PID 终止。
// processSig 的细分语义无法完整映射到 Windows，这里统一作为终止请求处理。
func signalClaudeProcess(cmd *exec.Cmd, guard *processGuard, sig processSig) error {
	_ = sig
	if guard != nil && guard.handle != 0 {
		if err := windows.TerminateJobObject(guard.handle, 1); err == nil {
			return nil
		}
	}
	if cmd == nil || cmd.Process == nil {
		return nil
	}
	return terminateByPid(cmd.Process.Pid)
}

// terminateByPid 按 PID 打开进程并发送 TerminateProcess。
// 进程已退出会返回带上下文的错误，外层 normalize 负责把可接受的 gone 状态归零。
func terminateByPid(pid int) error {
	if pid <= 0 {
		return errors.New("invalid claude pid")
	}
	h, err := windows.OpenProcess(windows.PROCESS_TERMINATE, false, uint32(pid))
	if err != nil {
		if isProcessGoneErr(err) {
			return fmt.Errorf("claude process %d not found before terminate: %w", pid, err)
		}
		return err
	}
	defer windows.CloseHandle(h)
	if err := windows.TerminateProcess(h, 1); err != nil {
		if isProcessGoneErr(err) {
			return fmt.Errorf("claude process %d vanished during terminate: %w", pid, err)
		}
		return err
	}
	return nil
}

func createKillOnCloseJob() (windows.Handle, error) {
	h, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return 0, err
	}
	info := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{
		BasicLimitInformation: windows.JOBOBJECT_BASIC_LIMIT_INFORMATION{
			LimitFlags: 0x2000, // windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
		},
	}
	if _, err := windows.SetInformationJobObject(
		h,
		windows.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&info)),
		uint32(unsafe.Sizeof(info)),
	); err != nil {
		windows.CloseHandle(h)
		return 0, err
	}
	return h, nil
}

func isProcessGoneErr(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, os.ErrProcessDone) {
		return true
	}
	return errors.Is(err, windows.ERROR_INVALID_PARAMETER) ||
		errors.Is(err, windows.ERROR_NOT_FOUND)
}
