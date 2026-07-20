//go:build windows

package dreamexec

import (
	"os/exec"
	"time"
)

func configureDreamCommandCancellation(command *exec.Cmd) {
	command.Cancel = func() error { return command.Process.Kill() }
	command.WaitDelay = 500 * time.Millisecond
}
