//go:build windows

package gateprivate

import (
	"os"
	"os/exec"
)

func configureBoundedCommandPlatform(command *exec.Cmd) {
	command.Cancel = command.Process.Kill
}

func trustedExecutableOwnedByRoot(os.FileInfo) bool {
	return false
}

func trustedExecutableOwnedByCurrentOrRoot(os.FileInfo) bool {
	return false
}

func trustedExecutableOwnedByCurrent(os.FileInfo) bool {
	return false
}
