//go:build windows

package pidregistry

import (
	"fmt"
	"path/filepath"
	"strconv"
	"strings"

	"golang.org/x/sys/windows"
)

// readProcessIdentity 从 Windows 进程句柄读取创建时间和镜像路径。
func readProcessIdentity(pid int) (processIdentity, error) {
	if pid <= 1 {
		return processIdentity{}, fmt.Errorf("pidregistry: invalid process identity PID %d", pid)
	}
	handle, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		return processIdentity{}, fmt.Errorf("pidregistry: open process identity: %w", err)
	}
	defer windows.CloseHandle(handle)
	var creation, exit, kernel, user windows.Filetime
	if err := windows.GetProcessTimes(handle, &creation, &exit, &kernel, &user); err != nil {
		return processIdentity{}, fmt.Errorf("pidregistry: read process creation time: %w", err)
	}
	buffer := make([]uint16, windows.MAX_PATH)
	size := uint32(len(buffer))
	if err := windows.QueryFullProcessImageName(handle, 0, &buffer[0], &size); err != nil {
		return processIdentity{}, fmt.Errorf("pidregistry: read process executable: %w", err)
	}
	executable := filepath.Clean(strings.TrimSpace(windows.UTF16ToString(buffer[:size])))
	if executable == "." || executable == "" {
		return processIdentity{}, fmt.Errorf("pidregistry: process executable missing for PID %d", pid)
	}
	startToken := strconv.FormatUint(uint64(creation.HighDateTime)<<32|uint64(creation.LowDateTime), 10)
	return processIdentity{startToken: startToken, executable: executable}, nil
}
