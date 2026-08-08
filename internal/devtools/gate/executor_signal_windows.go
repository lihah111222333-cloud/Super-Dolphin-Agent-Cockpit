//go:build windows

package gate

import (
	"io/fs"
	"os/exec"
	"time"
)

func fileOwnerUID(fs.FileInfo) (int, bool) { return 0, false }

func configureCommandCancellation(command *exec.Cmd) {
	command.Cancel = func() error {
		return command.Process.Kill()
	}
	command.WaitDelay = 5 * time.Second
}

func runConfiguredCommand(command *exec.Cmd) error {
	return runConfiguredCommandWithStart(command, nil)
}

// runConfiguredCommandWithStart 仅暴露成功的进程启动边界供计时生产者使用，同时保留命令等待契约。
func runConfiguredCommandWithStart(command *exec.Cmd, onStart func()) error {
	if err := command.Start(); err != nil {
		return err
	}
	if onStart != nil {
		onStart()
	}
	return command.Wait()
}
