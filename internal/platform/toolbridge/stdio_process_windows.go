//go:build windows

package toolbridge

import (
	"errors"
	"os"
	"os/exec"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"

	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
)

const (
	// Windows CreationFlags 常量用于隐藏 stdio MCP 窗口并创建独立进程组。
	stdioCreateNewProcessGroup = 0x00000200
	stdioCreateNoWindow        = 0x08000000
)

// stdioProcessGuard 保存 Windows Job Object 句柄，Close 时负责释放整棵进程树。
type stdioProcessGuard struct {
	handle windows.Handle
}

// stdioConfigureCommand 配置 Windows stdio MCP 子进程的隐藏窗口和独立进程组。
func stdioConfigureCommand(cmd *exec.Cmd) {
	if cmd == nil {
		return
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{
		CreationFlags: stdioCreateNewProcessGroup | stdioCreateNoWindow,
		HideWindow:    true,
	}
}

// stdioAttachProcessGuard 把 stdio MCP 子进程加入 KillOnClose Job Object。
// Job 创建或绑定失败只记录告警并回退到单进程关闭路径。
func stdioAttachProcessGuard(cmd *exec.Cmd) *stdioProcessGuard {
	if cmd == nil || cmd.Process == nil {
		return nil
	}
	handle, err := stdioCreateKillOnCloseJob()
	if err != nil {
		pkglogger.Warn("toolbridge: create stdio MCP job failed", "pid", cmd.Process.Pid, "error", err)
		return nil
	}
	procHandle, err := windows.OpenProcess(
		windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE,
		false,
		uint32(cmd.Process.Pid),
	)
	if err != nil {
		pkglogger.Warn("toolbridge: open stdio MCP process failed", "pid", cmd.Process.Pid, "error", err)
		_ = windows.CloseHandle(handle)
		return nil
	}
	defer windows.CloseHandle(procHandle)
	if err := windows.AssignProcessToJobObject(handle, procHandle); err != nil {
		pkglogger.Warn("toolbridge: assign stdio MCP process to job failed", "pid", cmd.Process.Pid, "error", err)
		_ = windows.CloseHandle(handle)
		return nil
	}
	return &stdioProcessGuard{handle: handle}
}

// stdioTerminateProcessTree 优先终止 Job Object，失败时再尝试 Kill 单个进程。
func stdioTerminateProcessTree(cmd *exec.Cmd, guard *stdioProcessGuard) error {
	var jobErr error
	if guard != nil && guard.handle != 0 {
		if err := windows.TerminateJobObject(guard.handle, 1); err == nil {
			return nil
		} else if !stdioProcessGone(err) {
			jobErr = err
		}
	}
	if cmd == nil || cmd.Process == nil {
		return jobErr
	}
	err := cmd.Process.Kill()
	if stdioProcessGone(err) {
		err = nil
	}
	return errors.Join(jobErr, err)
}

// stdioExpectedCloseWaitError 在 Windows 上保留 Wait 错误，由调用方决定是否上报。
func stdioExpectedCloseWaitError(err error) error {
	return err
}

// stdioCleanupProcessTree 关闭 Job Object 句柄；KillOnClose 会清理仍挂住的子进程。
func stdioCleanupProcessTree(_ *exec.Cmd, guard *stdioProcessGuard) error {
	if guard == nil || guard.handle == 0 {
		return nil
	}
	err := windows.CloseHandle(guard.handle)
	guard.handle = 0
	if stdioProcessGone(err) {
		return nil
	}
	return err
}

// stdioCreateKillOnCloseJob 创建带 KillOnJobClose 标志的 Windows Job Object。
func stdioCreateKillOnCloseJob() (windows.Handle, error) {
	h, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return 0, err
	}
	info := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{
		BasicLimitInformation: windows.JOBOBJECT_BASIC_LIMIT_INFORMATION{
			LimitFlags: 0x2000,
		},
	}
	if _, err := windows.SetInformationJobObject(
		h,
		windows.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&info)),
		uint32(unsafe.Sizeof(info)),
	); err != nil {
		_ = windows.CloseHandle(h)
		return 0, err
	}
	return h, nil
}

// stdioProcessGone 判断 Windows 进程或 Job 句柄是否已不可用。
func stdioProcessGone(err error) bool {
	if err == nil {
		return false
	}
	return errors.Is(err, os.ErrProcessDone) ||
		errors.Is(err, windows.ERROR_INVALID_PARAMETER) ||
		errors.Is(err, windows.ERROR_NOT_FOUND)
}
