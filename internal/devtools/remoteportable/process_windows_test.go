//go:build windows

package remoteportable

import (
	"os/exec"
	"testing"
)

func TestConfigureBoundedCommandUsesWindowsDirectKill(t *testing.T) {
	command := exec.Command("cmd", "/c", "ping -n 30 127.0.0.1 >NUL")
	if err := ConfigureBoundedCommand(command); err != nil {
		t.Fatal(err)
	}
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	if err := command.Cancel(); err != nil {
		t.Fatal(err)
	}
	if err := command.Wait(); err == nil {
		t.Fatal("cancelled Windows process unexpectedly exited successfully")
	}
}
