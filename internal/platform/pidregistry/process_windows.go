//go:build windows

package pidregistry

import (
	"errors"
	"fmt"
	"unsafe"

	"golang.org/x/sys/windows"
)

// The Windows adapter: graceful SIGTERM semantics do not exist without a
// shared console session, so SIGTERM and SIGKILL both map to
// TerminateProcess. Live children of app instances that registered with us
// are typically attached to the parent's Job Object (see codexapp /
// claudecli Phase 2 guards), which means kill-on-close fires as soon as the
// parent exits. forceKill still walks descendants here so we cover the
// tail case where a stale registry file outlives the Job.

const stillActive = 259 // STILL_ACTIVE exit code returned by GetExitCodeProcess.

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
	return code == stillActive
}

func sendSIGTERM(pid int) error {
	return terminateByPID(pid)
}

func isNoSuchProcessErr(err error) bool {
	return errors.Is(err, windows.ERROR_INVALID_PARAMETER) ||
		errors.Is(err, windows.ERROR_NOT_FOUND)
}

func forceKill(pid int) error {
	if pid <= 1 {
		return fmt.Errorf("refusing to kill PID <= 1")
	}
	// Walk descendants first so that when we finally terminate the root,
	// we do not leave survivors that a new parent (PID 4 / System) has
	// already reparented.
	for _, descendant := range collectDescendants(pid) {
		_ = terminateByPID(descendant)
	}
	return terminateByPID(pid)
}

func terminateByPID(pid int) error {
	h, err := windows.OpenProcess(windows.PROCESS_TERMINATE, false, uint32(pid))
	if err != nil {
		if isNoSuchProcessErr(err) {
			return fmt.Errorf("pidregistry: process %d not found before terminate: %w", pid, err)
		}
		return err
	}
	defer windows.CloseHandle(h)
	if err := windows.TerminateProcess(h, 1); err != nil {
		if isNoSuchProcessErr(err) {
			return fmt.Errorf("pidregistry: process %d vanished during terminate: %w", pid, err)
		}
		return err
	}
	return nil
}

// collectDescendants returns PIDs whose ancestry chain leads back to root.
// We snapshot Toolhelp32 once, build a children index, then BFS from root.
// Duplicates and the root itself are excluded from the returned slice.
// collectDescendants 收集descendants。
func collectDescendants(root int) []int {
	snap, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return nil
	}
	defer windows.CloseHandle(snap)

	var entry windows.ProcessEntry32
	entry.Size = uint32(unsafe.Sizeof(entry))
	if err := windows.Process32First(snap, &entry); err != nil {
		return nil
	}

	children := make(map[int][]int, 256)
	for {
		pid := int(entry.ProcessID)
		ppid := int(entry.ParentProcessID)
		children[ppid] = append(children[ppid], pid)
		if err := windows.Process32Next(snap, &entry); err != nil {
			break
		}
	}

	visited := map[int]struct{}{root: {}}
	var out []int
	queue := []int{root}
	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]
		for _, child := range children[current] {
			if _, seen := visited[child]; seen {
				continue
			}
			if child <= 1 {
				continue
			}
			visited[child] = struct{}{}
			out = append(out, child)
			queue = append(queue, child)
		}
	}
	return out
}
