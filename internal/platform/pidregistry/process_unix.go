//go:build !windows

package pidregistry

import (
	"errors"
	"fmt"
	"syscall"
)

func isProcessAlive(pid int) bool {
	if pid <= 1 {
		return false
	}
	return syscall.Kill(pid, 0) == nil
}

func sendSIGTERM(pid int) error {
	return syscall.Kill(pid, syscall.SIGTERM)
}

func isNoSuchProcessErr(err error) bool {
	return errors.Is(err, syscall.ESRCH)
}

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
