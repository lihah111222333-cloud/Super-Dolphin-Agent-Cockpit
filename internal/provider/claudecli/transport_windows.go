//go:build windows

package claudecli

import (
	"errors"
	"os"
	"os/exec"
	"unsafe"

	"golang.org/x/sys/windows"

	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
)

// setClaudeProcessAttrs is a no-op on Windows — the Job Object the guard
// sets up after Start() replaces Unix-style process groups.
func setClaudeProcessAttrs(cmd *exec.Cmd) {
	if cmd == nil {
		return
	}
}

// processGuard wraps a Windows Job Object so a single TerminateJobObject can
// reap the claude cli plus any helpers it forks. Kill-on-close protects us
// if this process dies before calling close explicitly.
type processGuard struct {
	handle windows.Handle
}

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
	pid := cmd.Process.Pid
	if pid <= 0 {
		return errors.New("invalid claude pid")
	}
	h, err := windows.OpenProcess(windows.PROCESS_TERMINATE, false, uint32(pid))
	if err != nil {
		if isProcessGoneErr(err) {
			return nil
		}
		return err
	}
	defer windows.CloseHandle(h)
	if err := windows.TerminateProcess(h, 1); err != nil {
		if isProcessGoneErr(err) {
			return nil
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
			LimitFlags: windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE,
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
