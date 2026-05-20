//go:build windows

package exec

import (
	"errors"
	"fmt"
	"os"
	osexec "os/exec"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

// setSandboxProcessAttrs is a no-op on Windows — the Job Object set up after
// Start() replaces Unix-style process groups.
func setSandboxProcessAttrs(cmd *osexec.Cmd) {
	if cmd == nil {
		return
	}
}

// sandboxGuard wraps a Windows Job Object. TerminateJobObject reaps the
// entire subtree on timeout, and JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE handles
// the case where the sandbox caller crashes before close() runs.
type sandboxGuard struct {
	handle windows.Handle
}

type sandboxWindowsHooks struct {
	createJob        func() (windows.Handle, error)
	openProcess      func(pid uint32) (windows.Handle, error)
	assignProcessJob func(job, process windows.Handle) error
	terminateJob     func(job windows.Handle, exitCode uint32) error
	closeHandle      func(windows.Handle) error
}

func defaultSandboxWindowsHooks() sandboxWindowsHooks {
	return sandboxWindowsHooks{
		createJob: createSandboxJob,
		openProcess: func(pid uint32) (windows.Handle, error) {
			return windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE, false, pid)
		},
		assignProcessJob: windows.AssignProcessToJobObject,
		terminateJob:     windows.TerminateJobObject,
		closeHandle:      windows.CloseHandle,
	}
}

func attachSandboxGuard(cmd *osexec.Cmd) (*sandboxGuard, error) {
	return attachSandboxGuardWithHooks(cmd, defaultSandboxWindowsHooks())
}

func attachSandboxGuardWithHooks(cmd *osexec.Cmd, hooks sandboxWindowsHooks) (*sandboxGuard, error) {
	if cmd == nil || cmd.Process == nil {
		return nil, errors.New("missing started process")
	}
	hooks = fillSandboxWindowsHooks(hooks)
	handle, err := hooks.createJob()
	if err != nil {
		return nil, fmt.Errorf("create sandbox job: %w", err)
	}
	procHandle, err := hooks.openProcess(uint32(cmd.Process.Pid))
	if err != nil {
		_ = hooks.closeHandle(handle)
		return nil, fmt.Errorf("open sandbox process: %w", err)
	}
	defer hooks.closeHandle(procHandle)
	if err := hooks.assignProcessJob(handle, procHandle); err != nil {
		_ = hooks.closeHandle(handle)
		return nil, fmt.Errorf("assign process to sandbox job: %w", err)
	}
	return &sandboxGuard{handle: handle}, nil
}

func fillSandboxWindowsHooks(hooks sandboxWindowsHooks) sandboxWindowsHooks {
	defaults := defaultSandboxWindowsHooks()
	if hooks.createJob == nil {
		hooks.createJob = defaults.createJob
	}
	if hooks.openProcess == nil {
		hooks.openProcess = defaults.openProcess
	}
	if hooks.assignProcessJob == nil {
		hooks.assignProcessJob = defaults.assignProcessJob
	}
	if hooks.terminateJob == nil {
		hooks.terminateJob = defaults.terminateJob
	}
	if hooks.closeHandle == nil {
		hooks.closeHandle = defaults.closeHandle
	}
	return hooks
}

func (g *sandboxGuard) close() {
	if g == nil || g.handle == 0 {
		return
	}
	_ = windows.CloseHandle(g.handle)
	g.handle = 0
}

func killSandboxProcess(process *os.Process, guard *sandboxGuard) error {
	var jobErr error
	if guard != nil && guard.handle != 0 {
		if err := windows.TerminateJobObject(guard.handle, 1); err == nil {
			return nil
		} else {
			jobErr = err
		}
	}
	if process == nil {
		return jobErr
	}
	return errors.Join(jobErr, process.Kill())
}

func createSandboxJob() (windows.Handle, error) {
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

// shellRequestArgs returns the argv used by Sandbox.ShellRequest to run an
// arbitrary shell command. On Windows we honour $COMSPEC when set (the
// canonical cmd.exe location) and fall back to "cmd.exe". Note that /C
// terminates cmd after the command finishes, which matches the Unix `-lc`
// semantic closely enough for the exec sandbox.
func shellRequestArgs(command string) []string {
	shell := os.Getenv("COMSPEC")
	if strings.TrimSpace(shell) == "" {
		shell = "cmd.exe"
	}
	return []string{shell, "/C", command}
}
