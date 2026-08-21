//go:build windows

package heterocinode

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	batchCreateSuspended              = 0x00000004
	batchCreateNewProcessGroup        = 0x00000200
	batchJobObjectLimitKillOnJobClose = 0x00002000
)

type batchProcessGuard struct {
	job windows.Handle
}

// startBatchProcess 以挂起方式启动、绑定 Job Object 后再恢复唯一初始线程。
func startBatchProcess(command *exec.Cmd) (batchProcessGuard, error) {
	if command == nil {
		return batchProcessGuard{}, errors.New("batch command is nil")
	}
	job, err := createBatchJob()
	if err != nil {
		return batchProcessGuard{}, err
	}
	command.SysProcAttr = &syscall.SysProcAttr{CreationFlags: batchCreateSuspended | batchCreateNewProcessGroup}
	if err := command.Start(); err != nil {
		_ = windows.CloseHandle(job)
		return batchProcessGuard{}, err
	}
	guard := batchProcessGuard{job: job}
	if err := assignBatchJob(job, command.Process.Pid); err != nil {
		_ = windows.TerminateJobObject(job, 1)
		_, _ = command.Process.Wait()
		_ = windows.CloseHandle(job)
		return batchProcessGuard{}, err
	}
	if err := resumeBatchProcess(command.Process.Pid); err != nil {
		_ = windows.TerminateJobObject(job, 1)
		_, _ = command.Process.Wait()
		_ = windows.CloseHandle(job)
		return batchProcessGuard{}, err
	}
	return guard, nil
}

func createBatchJob() (windows.Handle, error) {
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return 0, fmt.Errorf("create batch Windows Job Object: %w", err)
	}
	limits := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{
		BasicLimitInformation: windows.JOBOBJECT_BASIC_LIMIT_INFORMATION{LimitFlags: batchJobObjectLimitKillOnJobClose},
	}
	if _, err := windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation, uintptr(unsafe.Pointer(&limits)), uint32(unsafe.Sizeof(limits))); err != nil {
		_ = windows.CloseHandle(job)
		return 0, fmt.Errorf("configure batch Windows Job Object: %w", err)
	}
	return job, nil
}

func assignBatchJob(job windows.Handle, pid int) error {
	process, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE, false, uint32(pid))
	if err != nil {
		return fmt.Errorf("open suspended batch process: %w", err)
	}
	defer windows.CloseHandle(process)
	if err := windows.AssignProcessToJobObject(job, process); err != nil {
		return fmt.Errorf("assign batch process to Windows Job Object: %w", err)
	}
	return nil
}

// resumeBatchProcess 只恢复挂起进程的唯一初始线程。
func resumeBatchProcess(pid int) error {
	snapshot, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPTHREAD, 0)
	if err != nil {
		return fmt.Errorf("snapshot suspended batch process threads: %w", err)
	}
	defer windows.CloseHandle(snapshot)
	threadID, err := findBatchInitialThread(snapshot, pid)
	if err != nil {
		return err
	}
	thread, err := windows.OpenThread(windows.THREAD_SUSPEND_RESUME, false, threadID)
	if err != nil {
		return fmt.Errorf("open suspended batch process thread: %w", err)
	}
	defer windows.CloseHandle(thread)
	previous, err := windows.ResumeThread(thread)
	if err != nil {
		return fmt.Errorf("resume batch process thread: %w", err)
	}
	if previous != 1 {
		return fmt.Errorf("batch process thread suspend count is %d", previous)
	}
	return nil
}

// findBatchInitialThread 在单次线程快照中确认恰好一个初始线程。
func findBatchInitialThread(snapshot windows.Handle, pid int) (uint32, error) {
	entry := windows.ThreadEntry32{Size: uint32(unsafe.Sizeof(windows.ThreadEntry32{}))}
	if err := windows.Thread32First(snapshot, &entry); err != nil {
		return 0, fmt.Errorf("read suspended batch process thread: %w", err)
	}
	var threadID uint32
	count := 0
	for {
		if entry.OwnerProcessID == uint32(pid) {
			threadID = entry.ThreadID
			count++
		}
		err := windows.Thread32Next(snapshot, &entry)
		if errors.Is(err, windows.ERROR_NO_MORE_FILES) {
			break
		}
		if err != nil {
			return 0, fmt.Errorf("enumerate suspended batch process threads: %w", err)
		}
	}
	if count != 1 {
		return 0, fmt.Errorf("suspended batch process has %d initial threads", count)
	}
	return threadID, nil
}

// stop 通过 Job Object 终止 Windows command 及其全部后代。
func (guard batchProcessGuard) stop(command *exec.Cmd) error {
	if guard.job != 0 {
		if err := windows.TerminateJobObject(guard.job, 1); err == nil || isBatchProcessGone(err) {
			return nil
		}
	}
	if command == nil || command.Process == nil {
		return nil
	}
	return command.Process.Kill()
}

func (guard batchProcessGuard) close() error {
	if guard.job == 0 {
		return nil
	}
	return windows.CloseHandle(guard.job)
}

func isBatchProcessGone(err error) bool {
	return errors.Is(err, os.ErrProcessDone) || errors.Is(err, windows.ERROR_INVALID_PARAMETER) || errors.Is(err, windows.ERROR_NOT_FOUND)
}
