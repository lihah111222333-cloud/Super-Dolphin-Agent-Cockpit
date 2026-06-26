//go:build darwin

package main

import (
	"os/exec"
	"syscall"
)

// configureDetachedCommand 在 macOS 上让后台 updater 脱离原会话。
func configureDetachedCommand(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
}
