//go:build !windows

package remoteportable

import (
	"errors"
	"os/exec"
	"syscall"
)

func configurePortableProcess(command *exec.Cmd) error {
	if command == nil {
		return errors.New("portable bounded command is required")
	}
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	return nil
}

func cancelPortableProcess(command *exec.Cmd) error {
	if command == nil || command.Process == nil {
		return errors.New("portable bounded command process is unavailable")
	}
	err := syscall.Kill(-command.Process.Pid, syscall.SIGKILL)
	if err == syscall.ESRCH {
		return nil
	}
	return err
}
