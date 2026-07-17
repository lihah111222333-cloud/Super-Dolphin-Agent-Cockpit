//go:build !windows

package gate

import (
	"os/exec"
	"syscall"
	"time"
)

func configureCommandCancellation(command *exec.Cmd) {
	command.Cancel = func() error {
		return command.Process.Signal(syscall.SIGTERM)
	}
	command.WaitDelay = 5 * time.Second
}
