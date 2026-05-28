package orchestration

import (
	"os/exec"
	"runtime"
)

func newLongRunningTestCommand() *exec.Cmd {
	command := longRunningTestCommandLine()
	return exec.Command(command[0], command[1:]...)
}

func longRunningTestCommandLine() []string {
	if runtime.GOOS == "windows" {
		return []string{"powershell.exe", "-NoProfile", "-NonInteractive", "-Command", "Start-Sleep -Seconds 60"}
	}
	return []string{"sh", "-c", "sleep 60"}
}
