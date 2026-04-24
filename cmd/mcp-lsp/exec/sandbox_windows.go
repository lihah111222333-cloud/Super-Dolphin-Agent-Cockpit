//go:build windows

package exec

import (
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

func attachSandboxGuard(cmd *osexec.Cmd) *sandboxGuard {
	if cmd == nil || cmd.Process == nil {
		return nil
	}
	handle, err := createSandboxJob()
	if err != nil {
		return nil
	}
	procHandle, err := windows.OpenProcess(
		windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE,
		false,
		uint32(cmd.Process.Pid),
	)
	if err != nil {
		windows.CloseHandle(handle)
		return nil
	}
	defer windows.CloseHandle(procHandle)
	if err := windows.AssignProcessToJobObject(handle, procHandle); err != nil {
		windows.CloseHandle(handle)
		return nil
	}
	return &sandboxGuard{handle: handle}
}

func (g *sandboxGuard) close() {
	if g == nil || g.handle == 0 {
		return
	}
	windows.CloseHandle(g.handle)
	g.handle = 0
}

func killSandboxProcess(process *os.Process, guard *sandboxGuard) {
	if guard != nil && guard.handle != 0 {
		if err := windows.TerminateJobObject(guard.handle, 1); err == nil {
			return
		}
	}
	if process == nil {
		return
	}
	_ = process.Kill()
}

func createSandboxJob() (windows.Handle, error) {
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
