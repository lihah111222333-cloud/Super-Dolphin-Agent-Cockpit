//go:build !windows

package gateprivate

import (
	"errors"
	"os/exec"
	"syscall"
	"time"
)

// ConfigureCommandCancellation 让上下文取消覆盖命令的完整进程组，并限制管道回收等待。
func ConfigureCommandCancellation(command *exec.Cmd, waitDelay time.Duration) {
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	command.Cancel = func() error {
		err := syscall.Kill(-command.Process.Pid, syscall.SIGKILL)
		if errors.Is(err, syscall.ESRCH) {
			return nil
		}
		return err
	}
	command.WaitDelay = waitDelay
}
