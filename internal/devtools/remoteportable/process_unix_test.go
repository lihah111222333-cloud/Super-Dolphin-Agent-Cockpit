//go:build !windows

package remoteportable

import (
	"context"
	"os/exec"
	"testing"
)

func TestConfigureBoundedCommandUsesUnixProcessGroup(t *testing.T) {
	command := exec.CommandContext(context.Background(), "sh", "-c", "sleep 30")
	if err := ConfigureBoundedCommand(command); err != nil {
		t.Fatal(err)
	}
	if command.SysProcAttr == nil || !command.SysProcAttr.Setpgid {
		t.Fatalf("portable command did not configure a process group: %#v", command.SysProcAttr)
	}
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	if err := command.Cancel(); err != nil {
		t.Fatal(err)
	}
	if err := command.Wait(); err == nil {
		t.Fatal("cancelled process group unexpectedly exited successfully")
	}
}
