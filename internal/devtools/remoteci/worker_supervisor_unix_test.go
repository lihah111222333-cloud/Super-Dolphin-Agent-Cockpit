//go:build !windows

package remoteci

import (
	"errors"
	"os/exec"
	"testing"
)

func TestRemoteWorkerSupervisorPropagatesWorkerExitStatus(t *testing.T) {
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 is required")
	}
	arguments := remoteWorkerSupervisorCommand("/bin/sh")
	arguments[0] = python
	for index, argument := range arguments {
		if len(argument) > remoteWorkerCommandElementLimit {
			t.Fatalf("command element %d length = %d, want <= %d", index, len(argument), remoteWorkerCommandElementLimit)
		}
	}
	arguments = append(arguments, "-c", "exit 23")
	command := exec.Command(arguments[0], arguments[1:]...)
	err = command.Run()
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) || exitErr.ExitCode() != 23 {
		t.Fatalf("supervisor exit error = %v", err)
	}
}
