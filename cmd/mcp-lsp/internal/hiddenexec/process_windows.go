//go:build windows

package hiddenexec

import (
	"os/exec"
	"syscall"
)

const (
	createNewProcessGroup = 0x00000200
	createSuspended       = 0x00000004
	createNoWindow        = 0x08000000
)

// configureCommand 在 Windows 上隐藏子进程窗口并让其进入独立进程组。
// nil 命令会被忽略，方便上层统一调用该平台钩子。
func configureCommand(cmd *exec.Cmd) {
	if cmd == nil {
		return
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{
		CreationFlags: createNewProcessGroup | createNoWindow,
		HideWindow:    true,
	}
}
