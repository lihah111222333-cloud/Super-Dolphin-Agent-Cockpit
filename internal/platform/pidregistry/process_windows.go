//go:build windows

package pidregistry

import (
	"errors"
	"fmt"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Windows 适配说明：没有共享 console session 时不存在可靠的 SIGTERM 语义，
// 因此温和终止和强制终止都映射到 TerminateProcess。forceKill 仍遍历后代进程，
// 用来覆盖 registry 文件比 Job Object 生命周期更长的尾部场景。

const stillActive = 259 // GetExitCodeProcess 返回的 STILL_ACTIVE 状态码。

// isProcessAlive 通过 GetExitCodeProcess 判断 Windows 进程是否仍处于 STILL_ACTIVE。
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

func exactProcessExists(pid int) (bool, error) {
	if pid <= 1 {
		return false, fmt.Errorf("refusing to inspect PID <= 1")
	}
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		if isNoSuchProcessErr(err) {
			return false, nil
		}
		return false, fmt.Errorf("open process %d for existence check: %w", pid, err)
	}
	defer windows.CloseHandle(h)
	var code uint32
	if err := windows.GetExitCodeProcess(h, &code); err != nil {
		return false, fmt.Errorf("read process %d exit code: %w", pid, err)
	}
	return code == stillActive, nil
}

// sendSIGTERM 在 Windows 上退化为 TerminateProcess。
func sendSIGTERM(pid int) error {
	return terminateByPID(pid)
}

// isNoSuchProcessErr 判断 Windows 错误是否表示进程已经不存在。
func isNoSuchProcessErr(err error) bool {
	return errors.Is(err, windows.ERROR_INVALID_PARAMETER) ||
		errors.Is(err, windows.ERROR_NOT_FOUND)
}

// forceKill 先终止后代进程，再终止根进程，避免后代被系统进程重新收养后残留。
func forceKill(pid int) error {
	if pid <= 1 {
		return fmt.Errorf("refusing to kill PID <= 1")
	}
	// 先终止后代，再终止根进程，避免根进程退出后后代被系统进程收养而残留。
	for _, descendant := range collectDescendants(pid) {
		_ = terminateByPID(descendant)
	}
	return terminateByPID(pid)
}

// terminateByPID 打开 PROCESS_TERMINATE 句柄并终止目标进程。
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

// collectDescendants 返回祖先链指向 root 的所有后代 PID。
// 函数只快照一次 Toolhelp32，再从 root BFS，返回值不包含 root 本身。
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
