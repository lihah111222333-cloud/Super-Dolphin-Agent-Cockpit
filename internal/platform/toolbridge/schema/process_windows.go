//go:build windows

package schema

import (
	"errors"
	"os/exec"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	helperKillOnJobClose = 0x00002000
)

type processGuard struct {
	handle windows.Handle
}

func configureProcess(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{}
}

// attachProcessGuard 将 helper 绑定到关闭即终止的 Windows Job Object。
func attachProcessGuard(cmd *exec.Cmd) (*processGuard, error) {
	if cmd == nil || cmd.Process == nil {
		return nil, errors.New("process is not started")
	}
	handle, err := createKillOnCloseJob()
	if err != nil {
		return nil, err
	}
	processHandle, err := windows.OpenProcess(
		windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE,
		false,
		uint32(cmd.Process.Pid),
	)
	if err != nil {
		_ = windows.CloseHandle(handle)
		return nil, err
	}
	defer windows.CloseHandle(processHandle)
	if err := windows.AssignProcessToJobObject(handle, processHandle); err != nil {
		_ = windows.CloseHandle(handle)
		return nil, err
	}
	return &processGuard{handle: handle}, nil
}

func terminateProcessTree(_ *exec.Cmd, guard *processGuard) error {
	if guard == nil || guard.handle == 0 {
		return errors.New("helper job object is not attached")
	}
	return windows.TerminateJobObject(guard.handle, 1)
}

func closeProcessGuard(guard *processGuard) error {
	if guard == nil || guard.handle == 0 {
		return nil
	}
	err := windows.CloseHandle(guard.handle)
	guard.handle = 0
	return err
}

func createKillOnCloseJob() (windows.Handle, error) {
	handle, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return 0, err
	}
	information := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{
		BasicLimitInformation: windows.JOBOBJECT_BASIC_LIMIT_INFORMATION{
			LimitFlags: helperKillOnJobClose,
		},
	}
	if _, err := windows.SetInformationJobObject(
		handle,
		windows.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&information)),
		uint32(unsafe.Sizeof(information)),
	); err != nil {
		_ = windows.CloseHandle(handle)
		return 0, err
	}
	return handle, nil
}
