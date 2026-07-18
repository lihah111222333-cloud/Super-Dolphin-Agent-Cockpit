//go:build windows

package schema

import (
	"errors"
	"fmt"
	"os"
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
	handle   windows.Handle
	assigned bool
}

// prepareProcessGuard 在 worker 启动前创建 Job，并强制 worker suspended 启动。
func prepareProcessGuard(cmd *exec.Cmd) (*processGuard, error) {
	if cmd == nil {
		return nil, errors.New("process command is nil")
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{}
	creationFlags := reflect.ValueOf(cmd.SysProcAttr).Elem().FieldByName("CreationFlags")
	if !creationFlags.IsValid() || !creationFlags.CanSet() || creationFlags.Kind() != reflect.Uint32 {
		return nil, errors.New("Windows SysProcAttr.CreationFlags is unavailable")
	}
	creationFlags.SetUint(windows.CREATE_SUSPENDED)
	handle, err := createKillOnCloseJob()
	if err != nil {
		return nil, err
	}
	return &processGuard{handle: handle}, nil
}

// attachProcessGuard 在恢复 suspended helper 前绑定关闭即终止的 Windows Job Object。
func attachProcessGuard(cmd *exec.Cmd, guard *processGuard) error {
	return attachProcessGuardWithProbe(cmd, guard, nil)
}

// attachProcessGuardWithProbe 逐阶段绑定 suspended worker 与预建 Job，供故障测试注入真实内部失败。
func attachProcessGuardWithProbe(cmd *exec.Cmd, guard *processGuard, probe processGuardAttachProbe) error {
	if cmd == nil || cmd.Process == nil {
		return errors.New("process is not started")
	}
	if guard == nil || guard.handle == 0 {
		return errors.New("helper job object is not prepared")
	}
	if err := runProcessGuardAttachProbe(probe, processGuardAttachOpenProcess); err != nil {
		return err
	}
	processHandle, err := openGuardedWindowsProcess(cmd)
	if err != nil {
		return err
	}
	return assignAndResumeWindowsProcess(processHandle, guard, probe)
}

// openGuardedWindowsProcess 打开绑定 Job 与恢复 suspended worker 所需的最小权限句柄。
func openGuardedWindowsProcess(cmd *exec.Cmd) (windows.Handle, error) {
	processHandle, err := windows.OpenProcess(
		windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE|windows.PROCESS_SUSPEND_RESUME,
		false,
		uint32(cmd.Process.Pid),
	)
	return processHandle, err
}

// assignAndResumeWindowsProcess 先绑定 Job 并记录 ownership，再恢复 worker 执行。
func assignAndResumeWindowsProcess(processHandle windows.Handle, guard *processGuard, probe processGuardAttachProbe) error {
	if err := windows.AssignProcessToJobObject(guard.handle, processHandle); err != nil {
		return errors.Join(err, windows.CloseHandle(processHandle))
	}
	guard.assigned = true
	if err := runProcessGuardAttachProbe(probe, processGuardAttachAssignJob); err != nil {
		return errors.Join(err, windows.CloseHandle(processHandle))
	}
	resume := windows.NewLazySystemDLL("ntdll.dll").NewProc("NtResumeProcess")
	status, _, _ := resume.Call(uintptr(processHandle))
	closeErr := windows.CloseHandle(processHandle)
	if status != 0 {
		return errors.Join(fmt.Errorf("NtResumeProcess failed with NTSTATUS 0x%x", status), closeErr)
	}
	if closeErr != nil {
		return closeErr
	}
	return nil
}

func runProcessGuardAttachProbe(probe processGuardAttachProbe, stage processGuardAttachStage) error {
	if probe == nil {
		return nil
	}
	return probe(stage)
}

func terminateProcessTree(_ *exec.Cmd, guard *processGuard) error {
	if guard == nil || guard.handle == 0 || !guard.assigned {
		return errors.New("helper job object is not attached")
	}
	return windows.TerminateJobObject(guard.handle, 1)
}

// terminateUnattachedProcessTree 通过 Job 终止已绑定树；未绑定时 direct child 仍处于 suspended 状态。
func terminateUnattachedProcessTree(cmd *exec.Cmd, guard *processGuard) error {
	var jobErr error
	if guard != nil && guard.handle != 0 && guard.assigned {
		jobErr = windows.TerminateJobObject(guard.handle, 1)
	}
	var killErr error
	if cmd == nil || cmd.Process == nil {
		killErr = errors.New("process is not started")
	} else if guard == nil || !guard.assigned {
		killErr = cmd.Process.Kill()
		if errors.Is(killErr, os.ErrProcessDone) {
			killErr = nil
		}
	}
	return errors.Join(jobErr, killErr)
}

func waitGuardedProcess(cmd *exec.Cmd, _ *processGuard) error {
	return cmd.Wait()
}

// closeProcessGuard 关闭 Job Object 并将 guard 转入可重复关闭的终态。
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
