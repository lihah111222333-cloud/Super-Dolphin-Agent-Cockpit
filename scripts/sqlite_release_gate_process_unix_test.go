//go:build !windows

package main

import (
	"errors"
	"os/exec"
	"syscall"
)

// configureSQLitePackageProcessGroup 将真实 package smoke 与测试进程组隔离。
func configureSQLitePackageProcessGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

// terminateSQLitePackageProcessGroup 终止入口及仍持有日志管道的全部后代。
func terminateSQLitePackageProcessGroup(cmd *exec.Cmd) error {
	if cmd == nil || cmd.Process == nil {
		return errors.New("SQLite package process is unavailable")
	}
	return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
}
