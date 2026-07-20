//go:build !windows

package dreamexec

import (
	"errors"
	"os/exec"
	"syscall"
	"time"
)

func configureDreamCommandCancellation(command *exec.Cmd) {
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	command.Cancel = func() error {
		err := syscall.Kill(-command.Process.Pid, syscall.SIGKILL)
		if errors.Is(err, syscall.ESRCH) {
			return nil
		}
		return err
	}
	command.WaitDelay = 500 * time.Millisecond
}
