//go:build windows

package gate

import (
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"time"
)

var errDurationLedgerPlatformUnsupported = errors.New("duration ledger locking is unsupported on windows")

func fileOwnerUID(fs.FileInfo) (int, bool) { return 0, false }

func lockDurationLedgerFile(*os.File) error { return errDurationLedgerPlatformUnsupported }

func unlockDurationLedgerFile(*os.File) error { return errDurationLedgerPlatformUnsupported }

func syncDurationLedgerDirectory(string) error { return errDurationLedgerPlatformUnsupported }

func configureCommandCancellation(command *exec.Cmd) {
	command.Cancel = func() error {
		return command.Process.Kill()
	}
	command.WaitDelay = 5 * time.Second
}

func runConfiguredCommand(command *exec.Cmd) error {
	return command.Run()
}
