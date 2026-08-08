//go:build !windows

package remoteci

import (
	"errors"
	"os/exec"
	"testing"
)

func TestRemoteWorkerSupervisorUsesRuntimeDependencyPython(t *testing.T) {
	if remoteWorkerSupervisorPython != "/usr/bin/python3" {
		t.Fatalf("remote worker supervisor Python = %q, want runtime dependency image path", remoteWorkerSupervisorPython)
	}
}

func TestRemoteWorkerSupervisorPropagatesWorkerExitStatus(t *testing.T) {
	_, err := exec.LookPath(remoteWorkerSupervisorPython)
	if err != nil {
		t.Skipf("runtime dependency Python is required: %v", err)
	}
	arguments := remoteWorkerSupervisorCommand("/bin/sh")
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
