//go:build windows

package processctl

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

type Guard struct {
	handle windows.Handle
}

const (
	ctrlBreakEvent        = 1
	createNewProcessGroup = 0x00000200
)

// Configure 应用运行时配置。
func Configure(cmd *exec.Cmd) {
	if cmd == nil {
		return
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: createNewProcessGroup}
}

// Attach 处理attach。
func Attach(cmd *exec.Cmd, logger *slog.Logger) *Guard {
	if cmd == nil || cmd.Process == nil {
		return nil
	}
	handle, err := createKillOnCloseJob()
	if err != nil {
		logGuardWarning(logger, "orchestration: create agent job failed", cmd, err)
		return nil
	}
	procHandle, err := windows.OpenProcess(
		windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE,
		false,
		uint32(cmd.Process.Pid),
	)
	if err != nil {
		logGuardWarning(logger, "orchestration: open agent process failed", cmd, err)
		_ = windows.CloseHandle(handle)
		return nil
	}
	defer windows.CloseHandle(procHandle)
	if err := windows.AssignProcessToJobObject(handle, procHandle); err != nil {
		logGuardWarning(logger, "orchestration: assign agent process to job failed", cmd, err)
		_ = windows.CloseHandle(handle)
		return nil
	}
	return &Guard{handle: handle}
}

func logGuardWarning(logger *slog.Logger, msg string, cmd *exec.Cmd, err error) {
	if logger == nil {
		return
	}
	pid := 0
	if cmd != nil && cmd.Process != nil {
		pid = cmd.Process.Pid
	}
	logger.Warn(msg, "pid", pid, "error", err)
}

// Close 关闭编排资源。
func (g *Guard) Close() {
	if g == nil || g.handle == 0 {
		return
	}
	_ = windows.CloseHandle(g.handle)
	g.handle = 0
}

// RequestStop 处理请求stop。
func RequestStop(cmd *exec.Cmd, guard *Guard) error {
	if err := interruptProcessGroup(cmd); err == nil || isProcessGoneErr(err) {
		return nil
	}
	return ForceStop(cmd, guard)
}

func interruptProcessGroup(cmd *exec.Cmd) error {
	if cmd == nil || cmd.Process == nil {
		return nil
	}
	pid := cmd.Process.Pid
	if pid <= 0 {
		return errors.New("invalid agent pid")
	}
	return windows.GenerateConsoleCtrlEvent(ctrlBreakEvent, uint32(pid))
}

// ForceStop 处理强制stop。
func ForceStop(cmd *exec.Cmd, guard *Guard) error {
	var jobErr error
	if guard != nil && guard.handle != 0 {
		if err := windows.TerminateJobObject(guard.handle, 1); err == nil {
			return nil
		} else if !isProcessGoneErr(err) {
			jobErr = err
		}
	}
	if cmd == nil || cmd.Process == nil {
		return jobErr
	}
	err := terminatePID(cmd.Process.Pid)
	if isProcessGoneErr(err) {
		err = nil
	}
	return errors.Join(jobErr, err)
}

func createKillOnCloseJob() (windows.Handle, error) {
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

func terminatePID(pid int) error {
	if pid <= 0 {
		return errors.New("invalid agent pid")
	}
	h, err := windows.OpenProcess(windows.PROCESS_TERMINATE, false, uint32(pid))
	if err != nil {
		return fmt.Errorf("open process %d: %w", pid, err)
	}
	defer windows.CloseHandle(h)
	if err := windows.TerminateProcess(h, 1); err != nil {
		return fmt.Errorf("terminate process %d: %w", pid, err)
	}
	return nil
}

func isProcessGoneErr(err error) bool {
	if err == nil {
		return false
	}
	return errors.Is(err, os.ErrProcessDone) ||
		errors.Is(err, windows.ERROR_INVALID_PARAMETER) ||
		errors.Is(err, windows.ERROR_NOT_FOUND)
}
