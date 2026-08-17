//go:build unix

package main

import (
	"errors"
	"os/exec"
	"syscall"
	"time"
)

// configurePeerCommandCancellation makes the MCP peer and its language-server children one killable group.
func configurePeerCommandCancellation(command *exec.Cmd) {
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
