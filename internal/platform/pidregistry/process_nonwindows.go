//go:build !windows

package pidregistry

import (
	"errors"
	"fmt"
	"syscall"
)

// isProcessAlive 在 Unix 上通过 kill(pid, 0) 判断进程是否存在且可访问。
func isProcessAlive(pid int) bool {
	if pid <= 1 {
		return false
	}
	return syscall.Kill(pid, 0) == nil
}

func exactProcessExists(pid int) (bool, error) {
	if pid <= 1 {
		return false, fmt.Errorf("refusing to inspect PID <= 1")
	}
	err := syscall.Kill(pid, 0)
	switch {
	case err == nil, errors.Is(err, syscall.EPERM):
		return true, nil
	case errors.Is(err, syscall.ESRCH):
		return false, nil
	default:
		return false, fmt.Errorf("inspect process %d existence: %w", pid, err)
	}
}

// sendSIGTERM 向目标进程发送温和终止信号。
func sendSIGTERM(pid int) error {
	return syscall.Kill(pid, syscall.SIGTERM)
}

// isNoSuchProcessErr 判断系统错误是否表示进程已经不存在。
func isNoSuchProcessErr(err error) bool {
	return errors.Is(err, syscall.ESRCH)
}

// forceKill 优先按进程组强制结束，失败后再尝试单进程 SIGKILL。
func forceKill(pid int) error {
	if pid <= 1 {
		return fmt.Errorf("refusing to kill PID <= 1")
	}
	if err := syscall.Kill(-pid, syscall.SIGKILL); err == nil {
		return nil
	}
	err := syscall.Kill(pid, syscall.SIGKILL)
	if errors.Is(err, syscall.ESRCH) {
		return nil
	}
	return err
}
