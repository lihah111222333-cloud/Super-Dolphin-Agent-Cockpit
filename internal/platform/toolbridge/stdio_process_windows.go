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
	stdioCreateNewProcessGroup = 0x00000200
	stdioCreateNoWindow        = 0x08000000
)

type stdioProcessGuard struct {
	handle windows.Handle
}

func missingStdioProcessGuard() *stdioProcessGuard {
	return nil
}

func stdioConfigureCommand(cmd *exec.Cmd) {
	if cmd == nil {
		return
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{
		CreationFlags: stdioCreateNewProcessGroup | stdioCreateNoWindow,
		HideWindow:    true,
	}
}

// stdioAttachProcessGuard 处理stdioattach进程守卫。
func stdioAttachProcessGuard(cmd *exec.Cmd) *stdioProcessGuard {
	if cmd == nil || cmd.Process == nil {
		return nil
	}
	handle, err := stdioCreateKillOnCloseJob()
	if err != nil {
		pkglogger.Warn("toolbridge: create stdio MCP job failed", "pid", cmd.Process.Pid, "error", err)
		return missingStdioProcessGuard()
	}
	procHandle, err := windows.OpenProcess(
		windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE,
		false,
		uint32(cmd.Process.Pid),
	)
	if err != nil {
		pkglogger.Warn("toolbridge: open stdio MCP process failed", "pid", cmd.Process.Pid, "error", err)
		_ = windows.CloseHandle(handle)
		return missingStdioProcessGuard()
	}
	defer windows.CloseHandle(procHandle)
	if err := windows.AssignProcessToJobObject(handle, procHandle); err != nil {
		pkglogger.Warn("toolbridge: assign stdio MCP process to job failed", "pid", cmd.Process.Pid, "error", err)
		_ = windows.CloseHandle(handle)
		return missingStdioProcessGuard()
	}
	return &stdioProcessGuard{handle: handle}
}

// stdioTerminateProcessTree 处理stdioterminate进程tree。
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

func stdioExpectedCloseWaitError(err error) error {
	return err
}

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

func stdioProcessGone(err error) bool {
	if err == nil {
		return false
	}
	return errors.Is(err, os.ErrProcessDone) ||
		errors.Is(err, windows.ERROR_INVALID_PARAMETER) ||
		errors.Is(err, windows.ERROR_NOT_FOUND)
}
