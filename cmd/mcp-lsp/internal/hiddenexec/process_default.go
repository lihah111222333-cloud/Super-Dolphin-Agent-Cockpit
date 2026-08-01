//go:build darwin || linux

package hiddenexec

import (
	"os/exec"
	"syscall"
)

// configureCommand 在 Unix 平台为命令建立独立进程组。
// 后续关闭会向负 PID 发信号，确保语言服务器派生的 worker 一并退出。
func configureCommand(cmd *exec.Cmd) {
	if cmd == nil {
		return
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}
