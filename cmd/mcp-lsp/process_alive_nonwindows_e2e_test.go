//go:build e2e && !windows

package main

import (
	"errors"
	"syscall"
)

// processAliveForE2E 使用 signal zero 检查 Unix 测试目标进程是否存活。
func processAliveForE2E(pid int) (bool, error) {
	err := syscall.Kill(pid, 0)
	if err == nil || errors.Is(err, syscall.EPERM) {
		return true, nil
	}
	if errors.Is(err, syscall.ESRCH) {
		return false, nil
	}
	return false, err
}
