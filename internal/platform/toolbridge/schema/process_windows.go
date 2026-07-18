//go:build windows

package schema

import (
	"errors"
	"fmt"
	"os/exec"
	"reflect"
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

func configureProcess(cmd *exec.Cmd) error {
	cmd.SysProcAttr = &syscall.SysProcAttr{}
	creationFlags := reflect.ValueOf(cmd.SysProcAttr).Elem().FieldByName("CreationFlags")
	if !creationFlags.IsValid() || !creationFlags.CanSet() || creationFlags.Kind() != reflect.Uint32 {
		return errors.New("Windows SysProcAttr.CreationFlags is unavailable")
	}
	creationFlags.SetUint(windows.CREATE_SUSPENDED)
	return nil
}

// attachProcessGuard 在恢复 suspended helper 前绑定关闭即终止的 Windows Job Object。
func attachProcessGuard(cmd *exec.Cmd) (*processGuard, error) {
	if cmd == nil || cmd.Process == nil {
		return nil, errors.New("process is not started")
	}
	handle, err := createKillOnCloseJob()
	if err != nil {
		return nil, err
	}
	processHandle, err := windows.OpenProcess(
		windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE|windows.PROCESS_SUSPEND_RESUME,
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
	resume := windows.NewLazySystemDLL("ntdll.dll").NewProc("NtResumeProcess")
	status, _, _ := resume.Call(uintptr(processHandle))
	if status != 0 {
		_ = windows.CloseHandle(handle)
		return nil, fmt.Errorf("NtResumeProcess failed with NTSTATUS 0x%x", status)
	}
	return &processGuard{handle: handle}, nil
}

func terminateProcessTree(_ *exec.Cmd, guard *processGuard) error {
	if guard == nil || guard.handle == 0 {
		return errors.New("helper job object is not attached")
	}
	return windows.TerminateJobObject(guard.handle, 1)
}

func waitGuardedProcess(cmd *exec.Cmd, _ *processGuard) error {
	return cmd.Wait()
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
