//go:build windows

package processprobe

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"

	"golang.org/x/sys/windows"
)

// Probe opens only a query-limited process handle; it never requests a control right.
func Probe(ctx context.Context, pid int) (snapshot Snapshot, retErr error) {
	if ctx == nil {
		return newSnapshot(pid, 0, "", "", "", "", "", false, runtime.GOOS, ReasonProbeFailed, []string{"context"}, "context is nil"), errors.New("process probe context is nil")
	}
	if pid <= 1 {
		return newSnapshot(pid, 0, "", "", "", "", "", false, runtime.GOOS, ReasonProbeFailed, []string{"pid"}, "pid must be greater than one"), fmt.Errorf("process probe PID %d is invalid", pid)
	}
	if err := ctx.Err(); err != nil {
		return newSnapshot(pid, 0, "", "", "", "", "", false, runtime.GOOS, ReasonProbeFailed, []string{"context"}, "probe cancelled"), err
	}
	handle, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if errors.Is(err, windows.ERROR_INVALID_PARAMETER) {
		return newSnapshot(pid, 0, "", "", "", "", "", false, runtime.GOOS, ReasonUnknown, []string{"alive", "start_identity"}, "process is not alive"), fmt.Errorf("process PID %d is not alive", pid)
	}
	if err != nil {
		return newSnapshot(pid, 0, "", "", "", "", "", false, runtime.GOOS, ReasonPermissionDenied, []string{"query_handle"}, "query handle denied"), err
	}
	defer func() { retErr = errors.Join(retErr, windows.CloseHandle(handle)) }()
	var creation, exit, kernel, user windows.Filetime
	if err := windows.GetProcessTimes(handle, &creation, &exit, &kernel, &user); err != nil {
		return newSnapshot(pid, 0, "", "", "", "", "", true, runtime.GOOS, ReasonProbeFailed, []string{"start_identity"}, "process start query failed"), err
	}
	buffer := make([]uint16, windows.MAX_PATH)
	size := uint32(len(buffer))
	if err := windows.QueryFullProcessImageName(handle, 0, &buffer[0], &size); err != nil {
		return newSnapshot(pid, 0, "", "", "", "", "", true, runtime.GOOS, ReasonProbeFailed, []string{"executable"}, "process executable query failed"), err
	}
	executable := filepath.Base(strings.TrimSpace(windows.UTF16ToString(buffer[:size])))
	if executable == "." || executable == "" {
		return newSnapshot(pid, 0, "", "", "", "", "", true, runtime.GOOS, ReasonProbeFailed, []string{"executable"}, "process executable is empty"), errors.New("process executable is empty")
	}
	startIdentity := strconv.FormatUint(uint64(creation.HighDateTime)<<32|uint64(creation.LowDateTime), 10)
	return newSnapshot(pid, 0, "", "", "", startIdentity, executable, true, runtime.GOOS, "", []string{"session_id", "process_group_id", "uid"}, ""), nil
}
