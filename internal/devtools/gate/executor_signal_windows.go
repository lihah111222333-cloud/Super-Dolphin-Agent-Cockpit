//go:build windows

package gate

import (
	"os/exec"
	"time"
)

func configureCommandCancellation(command *exec.Cmd) {
	command.Cancel = func() error {
		return command.Process.Kill()
	}
	command.WaitDelay = 5 * time.Second
}
