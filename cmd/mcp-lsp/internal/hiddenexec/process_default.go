//go:build !windows

package hiddenexec

import "os/exec"

// configureCommand 在非 Windows 平台保留默认进程属性。
func configureCommand(_ *exec.Cmd) {
	return
}
