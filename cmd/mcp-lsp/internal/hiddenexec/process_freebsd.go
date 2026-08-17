//go:build freebsd

package hiddenexec

import (
	"os/exec"
	"syscall"
)

// configureCommand 在 FreeBSD 上为普通命令建立独占 session 与 PGID。
// FreeBSD 的进程树销毁权限仍由现有“非 Darwin/Linux/Windows”实现快速失败；
// 本文件只补齐 exec 命令的 Unix 会话边界，不宣称支持进程树清理能力。
func configureCommand(cmd *exec.Cmd) {
	if cmd == nil {
		return
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
}
