//go:build windows

package codexapp

import (
	"errors"
	"os"
	"os/exec"
	"unsafe"

	"golang.org/x/sys/windows"

	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
)

// processSig is the platform-neutral signal abstraction codexapp uses to
// manage its child processes. On Windows there is no equivalent of Unix
// signals without a shared console session, so all three values collapse
// to TerminateProcess for Phase 1. Graceful shutdown lands in Phase 2 via
// GenerateConsoleCtrlEvent on attached console children, or by posting
// WM_CLOSE to the app-server if it ever owns a window.
type processSig int

const (
	sigInterrupt processSig = iota
	sigTerminate
	sigForceKill
)

// setCodexProcessAttrs is intentionally a no-op on Windows — Unix process
// groups do not exist. Phase 2 will attach the child to a Job Object so the
// supervisor can terminate the whole subtree when the parent exits.
func setCodexProcessAttrs(cmd *exec.Cmd) {
	if cmd == nil {
		return
	}
}

// wrapWithFDLimit is a no-op wrapper on Windows. There is no `ulimit`
// equivalent, and the default handle limit on Windows (16384+) is already
// adequate for our batch-agent workload. Just exec the command directly.
func wrapWithFDLimit(argv []string) *exec.Cmd {
	return exec.Command(argv[0], argv[1:]...)
}

// processGuard wraps a Windows Job Object. AssignProcessToJobObject makes
// every descendant of the assigned process share the Job, so TerminateJobObject
// reliably reaps the whole subtree — the Windows equivalent of killing a Unix
// process group. JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE ensures the Job is also
// torn down if our process crashes before it can call terminate explicitly.
type processGuard struct {
	handle windows.Handle
}

func attachProcessGuard(cmd *exec.Cmd) *processGuard {
	if cmd == nil || cmd.Process == nil {
		return nil
	}
	handle, err := createKillOnCloseJob()
	if err != nil {
		pkglogger.Warn("codexapp: create job object failed", "error", err)
		return nil
	}
	procHandle, err := windows.OpenProcess(
		windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE,
		false,
		uint32(cmd.Process.Pid),
	)
	if err != nil {
		pkglogger.Warn("codexapp: open process handle failed",
			"pid", cmd.Process.Pid, "error", err)
		windows.CloseHandle(handle)
		return nil
	}
	defer windows.CloseHandle(procHandle)
	if err := windows.AssignProcessToJobObject(handle, procHandle); err != nil {
		pkglogger.Warn("codexapp: assign process to job failed",
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
	// Closing the Job handle triggers JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE,
	// which terminates any still-running descendants. This is the
	// "parent crashed without explicit cleanup" safety net.
	windows.CloseHandle(g.handle)
	g.handle = 0
}

func signalCodexProcess(cmd *exec.Cmd, guard *processGuard, sig processSig) error {
	_ = sig
	if guard != nil && guard.handle != 0 {
		if err := windows.TerminateJobObject(guard.handle, 1); err == nil {
			return nil
		}
		// Fall through to per-process termination if the Job handle is
		// already closed or invalid.
	}
	if cmd == nil || cmd.Process == nil {
		return nil
	}
	return terminatePID(cmd.Process.Pid)
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

func sendSignalToPID(pid int, sig processSig) error {
	_ = sig
	return terminatePID(pid)
}

func isProcessAlive(pid int) bool {
	if pid <= 1 {
		return false
	}
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		return false
	}
	defer windows.CloseHandle(h)
	var code uint32
	if err := windows.GetExitCodeProcess(h, &code); err != nil {
		return false
	}
	const stillActive = 259
	return code == stillActive
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

func killMCPProcess(pid int) error {
	if pid <= 1 {
		return errors.New("refusing to kill PID <= 1")
	}
	return terminatePID(pid)
}

func terminatePID(pid int) error {
	if pid <= 0 {
		return errors.New("invalid codex pid")
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

// discoverAllProcesses enumerates every process via Toolhelp32 and filters
// out our managed MCP sidecars by matching the leaf of ExeFile against the
// managedMCPBinaries set. ExeFile is the image file name, so an incoming
// "mcp-orch.exe" is matched against the "mcp-orch" table entry — we strip
// the .exe suffix before lookup.
func discoverAllProcesses() (map[int]int, []mcpProcessInfo) {
	snap, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		pkglogger.Warn("orphan cleanup: Toolhelp32 snapshot failed", "error", err)
		return nil, nil
	}
	defer windows.CloseHandle(snap)

	var entry windows.ProcessEntry32
	entry.Size = uint32(unsafe.Sizeof(entry))
	if err := windows.Process32First(snap, &entry); err != nil {
		pkglogger.Warn("orphan cleanup: Process32First failed", "error", err)
		return nil, nil
	}

	allProcs := make(map[int]int, 256)
	var mcpProcs []mcpProcessInfo
	for {
		pid := int(entry.ProcessID)
		ppid := int(entry.ParentProcessID)
		allProcs[pid] = ppid

		name := windows.UTF16ToString(entry.ExeFile[:])
		if bin := matchManagedBinary(name); bin != "" {
			mcpProcs = append(mcpProcs, mcpProcessInfo{pid: pid, ppid: ppid, binary: bin})
		}

		if err := windows.Process32Next(snap, &entry); err != nil {
			break
		}
	}
	return allProcs, mcpProcs
}

// discoverAppServerProcessList cannot be fully implemented on Windows without
// reading a target process's PEB (the only way to recover its full argv).
// Going that deep is gated behind NtQueryInformationProcess + manual PEB
// traversal, which is fragile across Windows versions. For Phase 2 we rely
// on Job Objects to keep every spawned app-server within its parent's
// supervision tree; stale cleanup therefore has nothing extra to find when
// the parent exited cleanly. The fallback path returns an empty list, which
// makes the orphan sweeper a no-op on Windows.
func discoverAppServerProcessList() (map[int]int, []appServerProcessInfo) {
	allProcs, _ := discoverAllProcesses()
	return allProcs, nil
}

func matchManagedBinary(exeFile string) string {
	name := exeFile
	if idx := lastPathSep(name); idx >= 0 {
		name = name[idx+1:]
	}
	if dot := lastDotExe(name); dot >= 0 {
		name = name[:dot]
	}
	if _, ok := managedMCPBinaries[name]; ok {
		return name
	}
	return ""
}

func lastPathSep(s string) int {
	for i := len(s) - 1; i >= 0; i-- {
		if s[i] == '\\' || s[i] == '/' {
			return i
		}
	}
	return -1
}

func lastDotExe(s string) int {
	// Only strip a ".exe" suffix — "server.v1" etc. stay intact.
	if len(s) < 4 {
		return -1
	}
	suffix := s[len(s)-4:]
	if suffix == ".exe" || suffix == ".EXE" {
		return len(s) - 4
	}
	return -1
}
