//go:build windows

package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

const guardJobLimitKillOnClose = 0x00002000

type guardProcessTreeLease struct {
	cmd             *exec.Cmd
	process         *os.Process
	job             windows.Handle
	processReleased bool
	handedOff       bool
	reaped          bool
}

// configureGuardProcessTree 让 Windows Guard suspended 启动，避免绑定 Job 前派生子进程。
func configureGuardProcessTree(cmd *exec.Cmd) error {
	if cmd == nil {
		return errors.New("Guard command is required")
	}
	if cmd.SysProcAttr != nil {
		return errors.New("Guard command process attributes are already configured")
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{
		CreationFlags: windows.CREATE_SUSPENDED | windows.CREATE_NEW_PROCESS_GROUP,
		HideWindow:    true,
	}
	return nil
}

// attachGuardProcessTree 将 suspended Guard 放入 kill-on-close Job 后才恢复执行。
func attachGuardProcessTree(cmd *exec.Cmd) (*guardProcessTreeLease, error) {
	if cmd == nil || cmd.Process == nil || cmd.Process.Pid <= 1 {
		return nil, errors.New("started Guard direct-child process is required")
	}
	job, err := createGuardKillOnCloseJob()
	if err != nil {
		return nil, err
	}
	lease := &guardProcessTreeLease{cmd: cmd, process: cmd.Process, job: job}
	processHandle, err := windows.OpenProcess(
		windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE|windows.PROCESS_SUSPEND_RESUME,
		false,
		uint32(cmd.Process.Pid),
	)
	if err != nil {
		return nil, errors.Join(err, closeGuardJobHandle(lease, false))
	}
	if err := windows.AssignProcessToJobObject(job, processHandle); err != nil {
		return nil, errors.Join(err, windows.CloseHandle(processHandle), closeGuardJobHandle(lease, false))
	}
	resumeErr := resumeGuardProcess(processHandle)
	processCloseErr := windows.CloseHandle(processHandle)
	if err := errors.Join(resumeErr, processCloseErr); err != nil {
		return nil, errors.Join(err, windows.TerminateJobObject(job, 1), closeGuardJobHandle(lease, false))
	}
	return lease, nil
}

// stopGuardProcessTree 终止 Windows Job、关闭句柄并同步 Wait direct child。
func stopGuardProcessTree(cmd *exec.Cmd, lease *guardProcessTreeLease) error {
	if err := validateGuardProcessTreeLease(cmd, lease); err != nil {
		return err
	}
	if lease.handedOff || lease.processReleased {
		return errors.New("Guard process tree lease was already handed off")
	}
	if lease.reaped {
		return nil
	}
	terminateErr := windows.TerminateJobObject(lease.job, 1)
	closeErr := closeGuardJobHandle(lease, false)
	waitErr := normalizeGuardProcessWait(cmd.Wait())
	lease.reaped = true
	return errors.Join(terminateErr, closeErr, waitErr)
}

// handoffGuardProcessTree 先解除 Job 的 close-kill，再释放 direct child 并关闭 Job handle。
func handoffGuardProcessTree(cmd *exec.Cmd, lease *guardProcessTreeLease) error {
	if err := validateGuardProcessTreeLease(cmd, lease); err != nil {
		return err
	}
	if lease.reaped {
		return errors.New("reaped Guard process tree cannot be handed off")
	}
	if lease.handedOff {
		return errors.New("Guard process tree lease was already handed off")
	}
	if err := setGuardJobKillOnClose(lease.job, false); err != nil {
		return errors.Join(err, stopGuardProcessTree(cmd, lease))
	}
	if err := cmd.Process.Release(); err != nil {
		return errors.Join(err, stopGuardProcessTree(cmd, lease))
	}
	lease.processReleased = true
	if err := closeGuardJobHandle(lease, false); err != nil {
		cleanupErr := errors.Join(windows.TerminateJobObject(lease.job, 1), closeGuardJobHandle(lease, false))
		return errors.Join(err, cleanupErr)
	}
	lease.handedOff = true
	return nil
}

// validateGuardProcessTreeLease 拒绝 PID reuse 后替换的 Windows process handle。
func validateGuardProcessTreeLease(cmd *exec.Cmd, lease *guardProcessTreeLease) error {
	if cmd == nil || cmd.Process == nil || lease == nil || lease.job == 0 {
		return errors.New("Guard process tree direct-child ownership is required")
	}
	if lease.cmd != cmd || lease.process != cmd.Process {
		return errors.New("Guard process tree direct-child ownership does not match")
	}
	return nil
}

// createGuardKillOnCloseJob 创建默认关闭即终止整树的 Windows Job。
func createGuardKillOnCloseJob() (windows.Handle, error) {
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return 0, err
	}
	if err := setGuardJobKillOnClose(job, true); err != nil {
		return 0, errors.Join(err, windows.CloseHandle(job))
	}
	return job, nil
}

// setGuardJobKillOnClose 显式切换 Job close 时的树终止语义。
func setGuardJobKillOnClose(job windows.Handle, enabled bool) error {
	if job == 0 {
		return errors.New("Guard Job handle is required")
	}
	var flags uint32
	if enabled {
		flags = guardJobLimitKillOnClose
	}
	information := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{
		BasicLimitInformation: windows.JOBOBJECT_BASIC_LIMIT_INFORMATION{LimitFlags: flags},
	}
	_, err := windows.SetInformationJobObject(
		job,
		windows.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&information)),
		uint32(unsafe.Sizeof(information)),
	)
	return err
}

// closeGuardJobHandle 可选解除 close-kill 后关闭 Job，并清空 lease 中的唯一 handle。
func closeGuardJobHandle(lease *guardProcessTreeLease, disarm bool) error {
	if lease == nil || lease.job == 0 {
		return nil
	}
	if disarm {
		if err := setGuardJobKillOnClose(lease.job, false); err != nil {
			return err
		}
	}
	if err := windows.CloseHandle(lease.job); err != nil {
		return err
	}
	lease.job = 0
	return nil
}

// resumeGuardProcess 恢复已安全绑定 Job 的 suspended Guard。
func resumeGuardProcess(process windows.Handle) error {
	resume := windows.NewLazySystemDLL("ntdll.dll").NewProc("NtResumeProcess")
	status, _, _ := resume.Call(uintptr(process))
	if status != 0 {
		return fmt.Errorf("NtResumeProcess failed with NTSTATUS 0x%x", status)
	}
	return nil
}

// normalizeGuardProcessWait 将 Job 终止导致的退出状态归一化为成功回收。
func normalizeGuardProcessWait(err error) error {
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return nil
	}
	return err
}
