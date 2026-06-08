//go:build windows

package hiddenexec

import (
	"os/exec"
	"testing"
)

func TestConfigureCommandHidesWindowsConsole(t *testing.T) {
	cmd := exec.Command("gopls.exe")
	configureCommand(cmd)
	if cmd.SysProcAttr == nil {
		t.Fatal("SysProcAttr is nil")
	}
	if !cmd.SysProcAttr.HideWindow {
		t.Fatal("HideWindow is false")
	}
	if cmd.SysProcAttr.CreationFlags&createNoWindow == 0 {
		t.Fatal("CREATE_NO_WINDOW flag is missing")
	}
	if cmd.SysProcAttr.CreationFlags&createNewProcessGroup == 0 {
		t.Fatal("CREATE_NEW_PROCESS_GROUP flag is missing")
	}
}
