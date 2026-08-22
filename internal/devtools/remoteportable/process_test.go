package remoteportable

import (
	"os/exec"
	"testing"
	"time"
)

func TestConfigureBoundedCommandRequiresCommand(t *testing.T) {
	if err := ConfigureBoundedCommand(nil); err == nil {
		t.Fatal("ConfigureBoundedCommand accepted nil command")
	}
}

func TestConfigureBoundedCommandSetsCancellationBoundary(t *testing.T) {
	command := exec.Command("go", "version")
	if err := ConfigureBoundedCommand(command); err != nil {
		t.Fatal(err)
	}
	if command.WaitDelay != 5*time.Second || command.Cancel == nil {
		t.Fatalf("bounded command configuration = wait_delay=%s cancel=%v", command.WaitDelay, command.Cancel != nil)
	}
}
