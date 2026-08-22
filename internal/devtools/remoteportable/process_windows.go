//go:build windows

package remoteportable

import (
	"errors"
	"os/exec"
)

func configurePortableProcess(command *exec.Cmd) error {
	if command == nil {
		return errors.New("portable bounded command is required")
	}
	return nil
}

func cancelPortableProcess(command *exec.Cmd) error {
	if command == nil || command.Process == nil {
		return errors.New("portable bounded command process is unavailable")
	}
	return command.Process.Kill()
}
