//go:build windows

package toolbridge

import (
	"os/exec"
	"testing"
)

func TestStdioConfigureCommandHidesWindowsConsole(t *testing.T) {
	cmd := exec.Command("mcp-lsp.exe")

	stdioConfigureCommand(cmd)

	if cmd.SysProcAttr == nil {
		t.Fatal("SysProcAttr is nil")
	}
	if !cmd.SysProcAttr.HideWindow {
		t.Fatal("HideWindow = false, want true")
	}
	if cmd.SysProcAttr.CreationFlags&stdioCreateNoWindow == 0 {
		t.Fatalf("CreationFlags = %#x, want CREATE_NO_WINDOW", cmd.SysProcAttr.CreationFlags)
	}
	if cmd.SysProcAttr.CreationFlags&stdioCreateNewProcessGroup == 0 {
		t.Fatalf("CreationFlags = %#x, want CREATE_NEW_PROCESS_GROUP", cmd.SysProcAttr.CreationFlags)
	}
}
