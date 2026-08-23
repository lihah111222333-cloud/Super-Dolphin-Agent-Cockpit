//go:build linux

package main

import (
	"os/exec"
	"syscall"
)

// configureInterruptProcessGroup 为 Linux hook 测试建立独立进程组。
func configureInterruptProcessGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

// interruptProcessGroup 使用 Linux 非交互 shell 可可靠传播的终止信号。
func interruptProcessGroup(cmd *exec.Cmd) error {
	return syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM)
}
