//go:build windows

package main

import (
	"errors"
	"os/exec"
)

// configureSQLitePackageProcessGroup 保留 Windows 入口进程的原生终止模型。
func configureSQLitePackageProcessGroup(cmd *exec.Cmd) {
	_ = cmd
}

// terminateSQLitePackageProcessGroup 在 Windows 上终止 package 入口进程。
func terminateSQLitePackageProcessGroup(cmd *exec.Cmd) error {
	if cmd == nil || cmd.Process == nil {
		return errors.New("SQLite package process is unavailable")
	}
	return cmd.Process.Kill()
}
