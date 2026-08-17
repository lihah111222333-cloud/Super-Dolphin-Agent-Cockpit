//go:build darwin || linux

package hiddenexec

import (
	"os/exec"
	"syscall"
)

// configureCommand 在 Unix 平台为命令建立独占 session 与 PGID。
// 每个 owner 都从 Setsid 开始，后续 action-time 只允许操作该 session 的已知成员。
func configureCommand(cmd *exec.Cmd) {
	if cmd == nil {
		return
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
}
