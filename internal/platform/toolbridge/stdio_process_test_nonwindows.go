//go:build !windows

package toolbridge

import "syscall"

func stdioTestProcessAlive(pid int) bool {
	if pid <= 1 {
		return false
	}
	return syscall.Kill(pid, 0) == nil
}
