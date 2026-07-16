//go:build darwin

package pidregistry

import (
	"bytes"
	"errors"
	"fmt"

	"golang.org/x/sys/unix"
)

func processIDs() ([]int, error) {
	processes, err := unix.SysctlKinfoProcSlice("kern.proc.all")
	if err != nil {
		return nil, fmt.Errorf("pidregistry: list Darwin processes: %w", err)
	}
	pids := make([]int, 0, len(processes))
	for _, process := range processes {
		if process.Proc.P_pid > 1 {
			pids = append(pids, int(process.Proc.P_pid))
		}
	}
	return pids, nil
}

// processArguments 从 Darwin 内核读取指定 PID 的原始 argv。
func processArguments(pid int) ([]string, error) {
	raw, err := unix.SysctlRaw("kern.procargs2", pid)
	if err != nil {
		if errors.Is(err, unix.ESRCH) || errors.Is(err, unix.EINVAL) {
			return nil, ErrStableProcessNotFound
		}
		return nil, fmt.Errorf("pidregistry: read Darwin process arguments: %w", err)
	}
	if len(raw) <= 4 {
		return nil, ErrStableProcessNotFound
	}
	parts := bytes.Split(raw[4:], []byte{0})
	arguments := make([]string, 0, len(parts))
	for _, part := range parts {
		if len(part) > 0 {
			arguments = append(arguments, string(part))
		}
	}
	return arguments, nil
}
