//go:build windows

package claudecli

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
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

// resolveClaudeBinary unwraps an npm shim .cmd/.ps1 so we spawn the real
// claude.exe directly. Claude Code's `--system-prompt` arg is a 12KB+ block
// with embedded newlines — routing it through cmd.exe (which is what Go does
// for .cmd/.bat targets post-CVE-2024-24576) causes silent arg corruption
// and the child exits immediately, surfacing as EOF on stdout.
//
// npm's wrapper template looks like:
//
//	@ECHO off
//	...
//	"%dp0%\node_modules\@scope\pkg\bin\foo.exe"   %*
//
// If we recognise that pattern we return the absolute path to the embedded
// .exe; otherwise we return the input unchanged (Go's LookPath still runs).
// resolveClaudeBinary 解析claude二进制。
func resolveClaudeBinary(binary string) string {
	if binary == "" {
		binary = "claude"
	}
	// Explicit path — trust the caller.
	if strings.ContainsAny(binary, `\/`) {
		return binary
	}
	resolved, err := exec.LookPath(binary)
	if err != nil {
		return binary
	}
	ext := strings.ToLower(filepath.Ext(resolved))
	if ext != ".cmd" && ext != ".bat" {
		return resolved
	}
	real, ok := unwrapNpmShim(resolved)
	if !ok {
		return resolved
	}
	pkglogger.Info("claudecli: unwrapped npm shim",
		"shim", resolved, "exe", real)
	return real
}

var npmShimExeRE = regexp.MustCompile(`(?i)"%dp0%[\\/]([^"]+\.exe)"`)

func unwrapNpmShim(cmdPath string) (string, bool) {
	data, err := os.ReadFile(cmdPath)
	if err != nil {
		return "", false
	}
	matches := npmShimExeRE.FindSubmatch(data)
	if len(matches) < 2 {
		return "", false
	}
	rel := strings.TrimSpace(string(matches[1]))
	if rel == "" {
		return "", false
	}
	base := filepath.Dir(cmdPath)
	abs := filepath.Clean(filepath.Join(base, rel))
	if _, err := os.Stat(abs); err != nil {
		return "", false
	}
	return abs, true
}

// processGuard wraps a Windows Job Object so a single TerminateJobObject can
// reap the claude cli plus any helpers it forks. Kill-on-close protects us
// if this process dies before calling close explicitly.
type processGuard struct {
	handle windows.Handle
}

func missingProcessGuard() *processGuard {
	return nil
}

// attachProcessGuard 处理attach进程守卫。
func attachProcessGuard(cmd *exec.Cmd) *processGuard {
	if cmd == nil || cmd.Process == nil {
		return nil
	}
	handle, err := createKillOnCloseJob()
	if err != nil {
		pkglogger.Warn("claudecli: create job object failed", "error", err)
		return missingProcessGuard()
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
		return missingProcessGuard()
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

// signalClaudeProcess 处理signalclaude进程。
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
	return terminateByPid(cmd.Process.Pid)
}

// terminateByPid 按进程 ID处理terminate。
func terminateByPid(pid int) error {
	if pid <= 0 {
		return errors.New("invalid claude pid")
	}
	h, err := windows.OpenProcess(windows.PROCESS_TERMINATE, false, uint32(pid))
	if err != nil {
		if isProcessGoneErr(err) {
			return fmt.Errorf("claude process %d not found before terminate: %w", pid, err)
		}
		return err
	}
	defer windows.CloseHandle(h)
	if err := windows.TerminateProcess(h, 1); err != nil {
		if isProcessGoneErr(err) {
			return fmt.Errorf("claude process %d vanished during terminate: %w", pid, err)
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
